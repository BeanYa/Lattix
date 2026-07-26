package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lattix/backend/internal/store"
)

// 面板 TLS 监听模式（main 启动时确定，DB 设置优先于启动参数，重启生效）。
const (
	TLSModeOff  = "off"
	TLSModeCert = "cert"
	TLSModeACME = "acme"
	TLSModePath = "path" // 域名路径模式：<tls-dir>/<域名>/fullchain.pem|privkey.pem，热加载免重启续期
)

// agent 安装资源来源（SettingResourceSource 取值；空 = GitHub release 钉版，默认）。
const ResourceSourcePanel = "panel" // 面板 /resource 镜像（install.sh 与 agent 包经面板分发）

// AppliedTLS 是当前进程实际生效的 TLS 配置快照（可能来自 DB 设置或启动参数），
// 用于与设置页保存的待生效值对比，得出 restart_required。
type AppliedTLS struct {
	Mode       string // off|cert|acme|path
	CertPEM    string // cert 模式实际加载的证书 PEM（启动参数给路径时读文件内容）
	KeyPEM     string
	ACMEDomain string
	ACMEEmail  string
	Domain     string // path 模式的域名
}

// certInfoDTO 是证书的只读摘要（私钥永远不出接口）。
type certInfoDTO struct {
	CommonName string   `json:"common_name"`
	DNSNames   []string `json:"dns_names"`
	NotAfter   string   `json:"not_after"`
	Expired    bool     `json:"expired"`
}

// parseCertInfo 解析 PEM 证书（首张）；解析失败返回错误（保存前校验用）。
func parseCertInfo(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("证书 PEM 无法解析")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("证书解析失败: " + err.Error())
	}
	return cert, nil
}

func toCertInfoDTO(c *x509.Certificate) certInfoDTO {
	return certInfoDTO{
		CommonName: c.Subject.CommonName,
		DNSNames:   c.DNSNames,
		NotAfter:   c.NotAfter.Format(time.RFC3339),
		Expired:    time.Now().After(c.NotAfter),
	}
}

// settingsDTO 是 GET /api/settings 的响应：保存值 + 运行态 + 是否需要重启。
type settingsDTO struct {
	Timezone         string       `json:"timezone"`    // IANA 时区；空 = 浏览器本地
	PublicURL        string       `json:"public_url"`  // 空 = 从请求推断
	TLSMode          string       `json:"tls_mode"`    // 保存的待生效模式；空 = 跟随启动参数
	TLSCert          *certInfoDTO `json:"tls_cert"`    // 保存的证书摘要（cert 为 PEM，path 为目录文件）
	TLSKeySet        bool         `json:"tls_key_set"` // 已保存私钥
	TLSDomain        string       `json:"tls_domain"`  // path 模式域名
	TLSDir           string       `json:"tls_dir"`     // 证书根目录（绝对路径，path 模式）
	ACMEDomain       string       `json:"acme_domain"`
	ACMEEmail        string       `json:"acme_email"`
	RunningTLSMode   string       `json:"running_tls_mode"` // 当前进程实际监听模式
	RestartRequired  bool         `json:"restart_required"` // TLS/ACME 保存值与运行态不一致
	AdminUser        string       `json:"admin_user"`
	PanelVersion     string       `json:"panel_version"` // 当前面板版本（构建注入）
	PasswordOverride bool         `json:"password_override"` // 密码已被设置页覆盖（否则为启动参数）
	// 事件告警（§19）：三项全空 = 告警关闭。bot token 不回显（与 tls_key 同风格），仅给置位标记。
	AlertWebhookURL          string `json:"alert_webhook_url"`
	AlertTelegramBotTokenSet bool   `json:"alert_telegram_bot_token_set"`
	AlertTelegramChatID      string `json:"alert_telegram_chat_id"`
	// agent 安装资源来源：panel = 面板 /resource 镜像；空 = GitHub release 钉版（默认）。
	ResourceSource string `json:"resource_source"`
}

// handleGetSettings 处理 GET /api/settings。
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dto := settingsDTO{
		Timezone:                 s.getSetting(ctx, store.SettingTimezone),
		PublicURL:                s.getSetting(ctx, store.SettingPublicURL),
		TLSMode:                  s.getSetting(ctx, store.SettingTLSMode),
		TLSKeySet:                s.getSetting(ctx, store.SettingTLSKeyPEM) != "",
		TLSDomain:                s.getSetting(ctx, store.SettingTLSDomain),
		TLSDir:                   s.cfg.TLSDir,
		ACMEDomain:               s.getSetting(ctx, store.SettingACMEDomain),
		ACMEEmail:                s.getSetting(ctx, store.SettingACMEEmail),
		RunningTLSMode:           s.cfg.RunningTLS.Mode,
		AdminUser:                s.cfg.AdminUser,
		PanelVersion:             s.cfg.Version,
		PasswordOverride:         s.getSetting(ctx, store.SettingAdminPassBcrypt) != "",
		AlertWebhookURL:          s.getSetting(ctx, store.SettingAlertWebhookURL),
		AlertTelegramBotTokenSet: s.getSetting(ctx, store.SettingAlertTelegramBotToken) != "",
		AlertTelegramChatID:      s.getSetting(ctx, store.SettingAlertTelegramChatID),
		ResourceSource:           s.getSetting(ctx, store.SettingResourceSource),
	}
	if certPEM := s.getSetting(ctx, store.SettingTLSCertPEM); certPEM != "" {
		if cert, err := parseCertInfo(certPEM); err == nil {
			info := toCertInfoDTO(cert)
			dto.TLSCert = &info
		}
	}
	// path 模式：摘要取自目录内当前文件（外部 ACME 可能已续期替换）。
	if dto.TLSMode == TLSModePath && dto.TLSDomain != "" {
		certPath, _ := DirCertPaths(s.cfg.TLSDir, dto.TLSDomain)
		if b, err := os.ReadFile(certPath); err == nil {
			if cert, err := parseCertInfo(string(b)); err == nil {
				info := toCertInfoDTO(cert)
				dto.TLSCert = &info
			}
		}
	}
	dto.RestartRequired = s.tlsRestartRequired(ctx, dto.TLSMode, dto.ACMEDomain, dto.ACMEEmail, dto.TLSDomain)
	writeJSON(w, http.StatusOK, dto)
}

// tlsRestartRequired 比较保存的 TLS 设置与当前运行快照。
// 未保存 tls_mode（跟随启动参数）时不存在"待生效"差异。
func (s *Server) tlsRestartRequired(ctx context.Context, mode, acmeDomain, acmeEmail, tlsDomain string) bool {
	if mode == "" {
		return false
	}
	a := s.cfg.RunningTLS
	if mode != a.Mode {
		return true
	}
	switch mode {
	case TLSModeCert:
		return s.getSetting(ctx, store.SettingTLSCertPEM) != a.CertPEM ||
			s.getSetting(ctx, store.SettingTLSKeyPEM) != a.KeyPEM
	case TLSModeACME:
		return acmeDomain != a.ACMEDomain || acmeEmail != a.ACMEEmail
	case TLSModePath:
		return tlsDomain != a.Domain
	}
	return false
}

// updateSettingsRequest 是 PUT /api/settings 的请求体。
// TLS 段整体保存、重启生效；cert/key 留空表示保持已保存值不变。
type updateSettingsRequest struct {
	Timezone   string `json:"timezone"`
	PublicURL  string `json:"public_url"`
	TLSMode    string `json:"tls_mode"` // ""=跟随启动参数 off|cert|acme|path
	TLSCertPEM string `json:"tls_cert_pem"`
	TLSKeyPEM  string `json:"tls_key_pem"`
	TLSDomain  string `json:"tls_domain"` // path 模式域名
	ACMEDomain string `json:"acme_domain"`
	ACMEEmail  string `json:"acme_email"`
	// 事件告警（§19）：webhook/chat_id 随表单覆盖（允许清空）；bot token 留空 = 保持已保存值。
	AlertWebhookURL       string `json:"alert_webhook_url"`
	AlertTelegramBotToken string `json:"alert_telegram_bot_token"`
	AlertTelegramChatID   string `json:"alert_telegram_chat_id"`
	// agent 安装资源来源：""|github = GitHub release 钉版（默认）；panel = 面板 /resource 镜像。
	ResourceSource string `json:"resource_source"`
}

// handleUpdateSettings 处理 PUT /api/settings：校验后落库。
// public_url/timezone 立即生效；TLS/ACME 由 main 在下次启动时读取（重启生效）。
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	req.Timezone = strings.TrimSpace(req.Timezone)
	if req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err != nil {
			writeError(w, http.StatusBadRequest, "无效的时区（须为 IANA 名称，如 Asia/Shanghai）")
			return
		}
	}

	req.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if req.PublicURL != "" {
		u, err := url.Parse(req.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "对外地址须形如 http(s)://域名或IP[:端口]")
			return
		}
	}

	switch req.TLSMode {
	case "", TLSModeOff, TLSModeCert, TLSModeACME, TLSModePath:
	default:
		writeError(w, http.StatusBadRequest, "无效的 TLS 模式")
		return
	}

	// 告警 webhook（§19）：非空时须为 http(s) 地址。
	req.AlertWebhookURL = strings.TrimSpace(req.AlertWebhookURL)
	if req.AlertWebhookURL != "" {
		u, err := url.Parse(req.AlertWebhookURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "告警 Webhook 地址须形如 http(s)://...")
			return
		}
	}

	// 安装资源来源（§11）：github 为空值语义（默认），panel 走面板 /resource 镜像。
	req.ResourceSource = strings.TrimSpace(req.ResourceSource)
	switch req.ResourceSource {
	case "", "github", ResourceSourcePanel:
	default:
		writeError(w, http.StatusBadRequest, "无效的安装资源来源（仅支持 github|panel）")
		return
	}

	// 证书/私钥：body 提供则校验并覆盖，留空保持已保存值。
	if req.TLSCertPEM != "" || req.TLSKeyPEM != "" {
		if req.TLSCertPEM == "" || req.TLSKeyPEM == "" {
			writeError(w, http.StatusBadRequest, "证书与私钥须同时提供")
			return
		}
		if _, err := tls.X509KeyPair([]byte(req.TLSCertPEM), []byte(req.TLSKeyPEM)); err != nil {
			writeError(w, http.StatusBadRequest, "证书/私钥校验失败: "+err.Error())
			return
		}
		if _, err := parseCertInfo(req.TLSCertPEM); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// 合并已保存值后校验目标模式的完整性。
	certPEM := firstNonEmpty(req.TLSCertPEM, s.getSetting(ctx, store.SettingTLSCertPEM))
	keyPEM := firstNonEmpty(req.TLSKeyPEM, s.getSetting(ctx, store.SettingTLSKeyPEM))
	tlsDomain := firstNonEmpty(strings.TrimSpace(req.TLSDomain), s.getSetting(ctx, store.SettingTLSDomain))
	acmeDomain := firstNonEmpty(strings.TrimSpace(req.ACMEDomain), s.getSetting(ctx, store.SettingACMEDomain))
	switch req.TLSMode {
	case TLSModeCert:
		if certPEM == "" || keyPEM == "" {
			writeError(w, http.StatusBadRequest, "自带证书模式需要证书与私钥（粘贴 PEM 或上传文件）")
			return
		}
	case TLSModeACME:
		if acmeDomain == "" {
			writeError(w, http.StatusBadRequest, "ACME 模式需要域名")
			return
		}
	case TLSModePath:
		// 域名路径模式：证书须已存在于 <tls-dir>/<域名>/ 且证书/私钥配对有效，
		// 保证保存即可用（安装脚本先 ACME 申请写目录，再回填域名）。
		if tlsDomain == "" {
			writeError(w, http.StatusBadRequest, "域名路径模式需要域名")
			return
		}
		if !ValidTLSDomain(tlsDomain) {
			writeError(w, http.StatusBadRequest, "无效的域名")
			return
		}
		certPath, keyPath := DirCertPaths(s.cfg.TLSDir, tlsDomain)
		if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("证书目录内无可用证书（期望 %s 与 %s）: %v", certPath, keyPath, err))
			return
		}
	}

	set := func(key, value string) bool {
		if err := s.st.SetSetting(ctx, key, value); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return false
		}
		return true
	}
	del := func(key string) bool {
		if err := s.st.DeleteSetting(ctx, key); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return false
		}
		return true
	}

	if req.Timezone == "" {
		if !del(store.SettingTimezone) {
			return
		}
	} else if !set(store.SettingTimezone, req.Timezone) {
		return
	}
	if req.PublicURL == "" {
		if !del(store.SettingPublicURL) {
			return
		}
	} else if !set(store.SettingPublicURL, req.PublicURL) {
		return
	}

	if req.TLSMode == "" {
		if !del(store.SettingTLSMode) {
			return
		}
	} else if !set(store.SettingTLSMode, req.TLSMode) {
		return
	}
	if req.TLSCertPEM != "" && !set(store.SettingTLSCertPEM, req.TLSCertPEM) {
		return
	}
	if req.TLSKeyPEM != "" && !set(store.SettingTLSKeyPEM, req.TLSKeyPEM) {
		return
	}
	// 域名仅在非空时覆盖（避免误清空）；邮箱随表单覆盖（允许清空）。
	if strings.TrimSpace(req.TLSDomain) != "" && !set(store.SettingTLSDomain, strings.TrimSpace(req.TLSDomain)) {
		return
	}
	if strings.TrimSpace(req.ACMEDomain) != "" && !set(store.SettingACMEDomain, strings.TrimSpace(req.ACMEDomain)) {
		return
	}
	if !set(store.SettingACMEEmail, strings.TrimSpace(req.ACMEEmail)) {
		return
	}

	// 事件告警（§19）：webhook/chat_id 随表单覆盖（允许清空）；bot token 留空保持不变。
	if !set(store.SettingAlertWebhookURL, req.AlertWebhookURL) {
		return
	}
	if !set(store.SettingAlertTelegramChatID, strings.TrimSpace(req.AlertTelegramChatID)) {
		return
	}
	if t := strings.TrimSpace(req.AlertTelegramBotToken); t != "" && !set(store.SettingAlertTelegramBotToken, t) {
		return
	}

	// 安装资源来源：panel 落库；github/空 = 删除设置（恢复默认 GitHub 钉版）。
	if req.ResourceSource == ResourceSourcePanel {
		if !set(store.SettingResourceSource, ResourceSourcePanel) {
			return
		}
	} else if !del(store.SettingResourceSource) {
		return
	}

	s.audit(r, "settings.update", nil, nil, map[string]any{
		"timezone": req.Timezone, "public_url": req.PublicURL,
		"tls_mode": req.TLSMode, "resource_source": req.ResourceSource,
	})
	s.handleGetSettings(w, r)
}

// handleChangePassword 处理 PUT /api/settings/password：
// 校验当前密码后写入 bcrypt 哈希（DB 覆盖启动参数；改密即全部会话失效，需重新登录）。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !s.checkPassword(req.CurrentPassword) {
		writeError(w, http.StatusForbidden, "当前密码错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码至少 8 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.SetSetting(r.Context(), store.SettingAdminPassBcrypt, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "admin.change_password", nil, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// checkPassword 校验登录密码：DB 中有 bcrypt 哈希则以其为准，否则比对启动参数。
func (s *Server) checkPassword(pw string) bool {
	if h := s.getSetting(context.Background(), store.SettingAdminPassBcrypt); h != "" {
		return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil
	}
	return pw == s.cfg.AdminPass
}

// credentialKey 是会话签名密钥的派生源（改密即换源，全部会话失效）。
func (s *Server) credentialKey() string {
	if h := s.getSetting(context.Background(), store.SettingAdminPassBcrypt); h != "" {
		return h
	}
	return s.cfg.AdminPass
}

// getSetting 读取设置，出错按未设置处理（设置为非关键路径，降级到启动参数）。
func (s *Server) getSetting(ctx context.Context, key string) string {
	v, err := s.st.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
