package panel

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/nettrust"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// 面板 TLS 监听模式（main 启动时确定，DB 设置优先于启动参数，重启生效）。
const (
	TLSModeOff  = "off"
	TLSModeCert = "cert"
	TLSModeACME = "acme"
	TLSModePath = "path" // 域名路径模式：<tls-dir>/<域名>/fullchain.pem|privkey.pem，热加载免重启续期
)

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
		DNSNames:   append([]string{}, c.DNSNames...),
		NotAfter:   c.NotAfter.Format(time.RFC3339),
		Expired:    time.Now().After(c.NotAfter),
	}
}

// settingsDTO 是 GET /api/settings 的响应：保存值 + 运行态 + 是否需要重启。
type settingsDTO struct {
	Timezone         string       `json:"timezone"`         // IANA 时区；空 = 浏览器本地
	TrafficTimezone  string       `json:"traffic_timezone"` // 流量日/月桶边界使用的 IANA 时区
	PublicURL        string       `json:"public_url"`       // 空 = 从请求推断
	PanelShort       string       `json:"panel_short"`      // 面板缩写（链路命名模板/订阅分组名）；空 = 默认 Lattix
	TrustedProxies   string       `json:"trusted_proxies"`  // 追加可信反代网段（CIDR 逗号分隔）；空 = 仅内建默认（回环+内网/容器网段）
	TLSMode          string       `json:"tls_mode"`         // 保存的待生效模式；空 = 跟随启动参数
	TLSCert          *certInfoDTO `json:"tls_cert"`         // 保存的证书摘要（cert 为 PEM，path 为目录文件）
	TLSKeySet        bool         `json:"tls_key_set"`      // 已保存私钥
	TLSDomain        string       `json:"tls_domain"`       // path 模式域名
	TLSDir           string       `json:"tls_dir"`          // 证书根目录（绝对路径，path 模式）
	ACMEDomain       string       `json:"acme_domain"`
	ACMEEmail        string       `json:"acme_email"`
	RunningTLSMode   string       `json:"running_tls_mode"` // 当前进程实际监听模式
	RestartRequired  bool         `json:"restart_required"` // TLS/ACME 保存值与运行态不一致
	AdminUser        string       `json:"admin_user"`
	PanelVersion     string       `json:"panel_version"`     // 当前面板版本（构建注入）
	PasswordOverride bool         `json:"password_override"` // 密码已被设置页覆盖（否则为启动参数）
	// 事件告警（§19）：三项全空 = 告警关闭。bot token 不回显（与 tls_key 同风格），仅给置位标记。
	AlertWebhookURL          string                    `json:"alert_webhook_url"`
	AlertTelegramBotTokenSet bool                      `json:"alert_telegram_bot_token_set"`
	AlertTelegramChatID      string                    `json:"alert_telegram_chat_id"`
	OperationLogLimit        int                       `json:"operation_log_limit"`
	RequestLogMaxMB          int                       `json:"request_log_max_mb"`
	RequestLogLevel          string                    `json:"request_log_level"` // 最低记录级别 debug|info|warning|error
	LogDir                   string                    `json:"log_dir"`
	RequestLogUsageBytes     int64                     `json:"request_log_usage_bytes"`
	RequestLogDropped        uint64                    `json:"request_log_dropped"`
	BackupIncludesLogs       bool                      `json:"backup_includes_logs"`
	Agent                    shared.AgentSettings      `json:"agent"`
	ServerSettings           shared.ServerSettings     `json:"server_settings"`          // 面板级默认（defaultsetting）
	ServerSettingsRevision   int64                     `json:"server_settings_revision"` // 默认值当前 revision
	ReleaseInspection        releaseInspectionSettings `json:"release_inspection"`
	BillingInspection        inspectionSchedule        `json:"billing_inspection"`
	ExchangeInspection       inspectionSchedule        `json:"exchange_rate_inspection"`
	ReportingCurrency        string                    `json:"reporting_currency"`
}

// handleGetSettings 处理 GET /api/settings。
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dto := settingsDTO{
		Timezone:                 s.getSetting(ctx, store.SettingTimezone),
		TrafficTimezone:          firstNonEmpty(s.getSetting(ctx, store.SettingTrafficTimezone), store.DefaultTrafficTimezone),
		PublicURL:                s.getSetting(ctx, store.SettingPublicURL),
		PanelShort:               store.EffectivePanelShort(s.getSetting(ctx, store.SettingPanelShort)),
		TrustedProxies:           s.getSetting(ctx, store.SettingTrustedProxies),
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
		OperationLogLimit:        settingInt(s.getSetting(ctx, store.SettingOperationLogLimit), 1000),
		RequestLogMaxMB:          settingInt(s.getSetting(ctx, store.SettingRequestLogMaxMB), 10),
		RequestLogLevel:          firstNonEmpty(s.getSetting(ctx, store.SettingRequestLogLevel), string(logging.SeverityDebug)),
		LogDir:                   s.cfg.LogDir,
		BackupIncludesLogs:       false,
		ReleaseInspection:        s.releaseInspectionSettings(ctx),
		BillingInspection:        s.billingInspectionSchedule(ctx),
		ExchangeInspection:       s.exchangeInspectionSchedule(ctx),
		ReportingCurrency:        firstNonEmpty(s.getSetting(ctx, store.SettingReportingCurrency), "CNY"),
	}
	agentSettings, err := s.st.AgentSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto.Agent = agentSettings
	dto.ServerSettings, dto.ServerSettingsRevision, err = s.st.DefaultServerSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.opLog != nil {
		dto.OperationLogLimit = s.opLog.MaxEntries()
	}
	if s.reqLog != nil {
		if status, err := s.reqLog.Status(ctx); err == nil {
			dto.RequestLogUsageBytes = status.UsageBytes
			dto.RequestLogDropped = status.Dropped
			dto.RequestLogMaxMB = int(status.MaxBytes >> 20)
		}
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
	Timezone        string `json:"timezone"`
	TrafficTimezone string `json:"traffic_timezone"`
	PublicURL       string `json:"public_url"`
	PanelShort      string `json:"panel_short"`     // 空 = 恢复默认 Lattix
	TrustedProxies  string `json:"trusted_proxies"` // CIDR 逗号分隔；空 = 仅内建默认（回环+内网/容器网段）
	TLSMode         string `json:"tls_mode"`        // ""=跟随启动参数 off|cert|acme|path
	TLSCertPEM      string `json:"tls_cert_pem"`
	TLSKeyPEM       string `json:"tls_key_pem"`
	TLSDomain       string `json:"tls_domain"` // path 模式域名
	ACMEDomain      string `json:"acme_domain"`
	ACMEEmail       string `json:"acme_email"`
	// 事件告警（§19）：webhook/chat_id 随表单覆盖（允许清空）；bot token 留空 = 保持已保存值。
	AlertWebhookURL       string                     `json:"alert_webhook_url"`
	AlertTelegramBotToken string                     `json:"alert_telegram_bot_token"`
	AlertTelegramChatID   string                     `json:"alert_telegram_chat_id"`
	OperationLogLimit     int                        `json:"operation_log_limit"`
	RequestLogMaxMB       int                        `json:"request_log_max_mb"`
	RequestLogLevel       string                     `json:"request_log_level"` // 空 = 默认 debug
	Agent                 *shared.AgentSettings      `json:"agent"`
	ServerSettings        *shared.ServerSettings     `json:"server_settings"` // nil = 不变
	ReleaseInspection     *releaseInspectionSettings `json:"release_inspection"`
	BillingInspection     *inspectionSchedule        `json:"billing_inspection"`
	ExchangeInspection    *inspectionSchedule        `json:"exchange_rate_inspection"`
	ReportingCurrency     string                     `json:"reporting_currency"`
}

// handleUpdateSettings 处理 PUT /api/settings：校验后落库。
// public_url/timezone 立即生效；TLS/ACME 由 main 在下次启动时读取（重启生效）。
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	beforeWebhookURL := s.getSetting(ctx, store.SettingAlertWebhookURL)
	before := map[string]any{
		"timezone":                     s.getSetting(ctx, store.SettingTimezone),
		"traffic_timezone":             firstNonEmpty(s.getSetting(ctx, store.SettingTrafficTimezone), store.DefaultTrafficTimezone),
		"public_url":                   s.getSetting(ctx, store.SettingPublicURL),
		"panel_short":                  store.EffectivePanelShort(s.getSetting(ctx, store.SettingPanelShort)),
		"trusted_proxies":              s.getSetting(ctx, store.SettingTrustedProxies),
		"tls_mode":                     s.getSetting(ctx, store.SettingTLSMode),
		"tls_domain":                   s.getSetting(ctx, store.SettingTLSDomain),
		"acme_domain":                  s.getSetting(ctx, store.SettingACMEDomain),
		"acme_email":                   s.getSetting(ctx, store.SettingACMEEmail),
		"alert_webhook_set":            beforeWebhookURL != "",
		"alert_telegram_chat_id":       s.getSetting(ctx, store.SettingAlertTelegramChatID),
		"alert_telegram_bot_token_set": s.getSetting(ctx, store.SettingAlertTelegramBotToken) != "",
		"operation_log_limit":          settingInt(s.getSetting(ctx, store.SettingOperationLogLimit), 1000),
		"request_log_max_mb":           settingInt(s.getSetting(ctx, store.SettingRequestLogMaxMB), 10),
		"request_log_level":            firstNonEmpty(s.getSetting(ctx, store.SettingRequestLogLevel), string(logging.SeverityDebug)),
		"release_inspection":           s.releaseInspectionSettings(ctx),
		"billing_inspection":           s.billingInspectionSchedule(ctx),
		"exchange_rate_inspection":     s.exchangeInspectionSchedule(ctx),
		"server_settings":              func() shared.ServerSettings { v, _, _ := s.st.DefaultServerSettings(ctx); return v }(),
		"reporting_currency":           firstNonEmpty(s.getSetting(ctx, store.SettingReportingCurrency), "CNY"),
	}

	if req.OperationLogLimit == 0 {
		req.OperationLogLimit = 1000
	}
	if req.OperationLogLimit < 100 || req.OperationLogLimit > 100000 {
		writeError(w, http.StatusBadRequest, "操作日志保留条数须为 100–100000")
		return
	}
	if req.RequestLogMaxMB == 0 {
		req.RequestLogMaxMB = 10
	}
	if req.RequestLogMaxMB < 1 || req.RequestLogMaxMB > 1024 {
		writeError(w, http.StatusBadRequest, "请求日志缓存须为 1–1024 MB")
		return
	}
	switch logging.Severity(req.RequestLogLevel) {
	case logging.SeverityDebug, logging.SeverityInfo, logging.SeverityWarning, logging.SeverityError:
	default:
		req.RequestLogLevel = string(logging.SeverityDebug)
	}
	if req.Agent != nil {
		// The panel owns revision assignment; clients edit only the values.
		req.Agent.Revision = 1
		if err := req.Agent.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.ServerSettings != nil {
		if err := req.ServerSettings.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.ReleaseInspection != nil {
		if err := req.ReleaseInspection.Agent.validate(); err != nil {
			writeError(w, http.StatusBadRequest, "agent release "+err.Error())
			return
		}
		if err := req.ReleaseInspection.Xray.validate(); err != nil {
			writeError(w, http.StatusBadRequest, "xray release "+err.Error())
			return
		}
	}
	for label, schedule := range map[string]*inspectionSchedule{"计费巡检": req.BillingInspection, "汇率刷新": req.ExchangeInspection} {
		if schedule != nil && (schedule.Unit != "day" || schedule.Every != 1 || schedule.validate() != nil) {
			writeError(w, http.StatusBadRequest, label+"仅支持每天指定时间执行")
			return
		}
	}
	req.ReportingCurrency = strings.ToUpper(strings.TrimSpace(req.ReportingCurrency))
	if req.ReportingCurrency == "" {
		req.ReportingCurrency = "CNY"
	}
	if !supportedCurrencies[req.ReportingCurrency] {
		writeError(w, http.StatusBadRequest, "不支持的统计币种")
		return
	}

	req.Timezone = strings.TrimSpace(req.Timezone)
	if req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err != nil {
			writeError(w, http.StatusBadRequest, "无效的时区（须为 IANA 名称，如 Asia/Shanghai）")
			return
		}
	}
	req.TrafficTimezone = strings.TrimSpace(req.TrafficTimezone)
	if req.TrafficTimezone == "" {
		req.TrafficTimezone = store.DefaultTrafficTimezone
	}
	if _, err := time.LoadLocation(req.TrafficTimezone); err != nil {
		writeError(w, http.StatusBadRequest, "无效的流量统计时区（须为 IANA 名称，如 Asia/Shanghai）")
		return
	}

	req.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if req.PublicURL != "" {
		u, err := url.Parse(req.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "对外地址须形如 http(s)://域名或IP[:端口]")
			return
		}
	}

	// 面板缩写进入链路命名与订阅分组名：拒绝模板定界符与换行，限长避免组名臃肿。
	req.PanelShort = strings.TrimSpace(req.PanelShort)
	if req.PanelShort != "" {
		if strings.ContainsAny(req.PanelShort, "{}") || strings.ContainsAny(req.PanelShort, "\r\n") {
			writeError(w, http.StatusBadRequest, "面板缩写不能包含 {} 或换行")
			return
		}
		if utf8.RuneCountInString(req.PanelShort) > 30 {
			writeError(w, http.StatusBadRequest, "面板缩写不能超过 30 个字符")
			return
		}
	}

	req.TrustedProxies = strings.Join(strings.Fields(req.TrustedProxies), ",")
	if req.TrustedProxies != "" {
		if err := nettrust.Validate(req.TrustedProxies); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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

	mutations := make([]store.SettingMutation, 0, 20)
	set := func(key, value string) {
		mutations = append(mutations, store.SettingMutation{Key: key, Value: value})
	}
	del := func(key string) {
		mutations = append(mutations, store.SettingMutation{Key: key, Delete: true})
	}

	if req.Timezone == "" {
		del(store.SettingTimezone)
	} else {
		set(store.SettingTimezone, req.Timezone)
	}
	set(store.SettingTrafficTimezone, req.TrafficTimezone)
	if req.PublicURL == "" {
		del(store.SettingPublicURL)
	} else {
		set(store.SettingPublicURL, req.PublicURL)
	}
	if req.PanelShort == "" {
		del(store.SettingPanelShort)
	} else {
		set(store.SettingPanelShort, req.PanelShort)
	}
	if req.TrustedProxies == "" {
		del(store.SettingTrustedProxies)
	} else {
		set(store.SettingTrustedProxies, req.TrustedProxies)
	}

	if req.TLSMode == "" {
		del(store.SettingTLSMode)
	} else {
		set(store.SettingTLSMode, req.TLSMode)
	}
	if req.TLSCertPEM != "" {
		set(store.SettingTLSCertPEM, req.TLSCertPEM)
	}
	if req.TLSKeyPEM != "" {
		set(store.SettingTLSKeyPEM, req.TLSKeyPEM)
	}
	// 域名仅在非空时覆盖（避免误清空）；邮箱随表单覆盖（允许清空）。
	if strings.TrimSpace(req.TLSDomain) != "" {
		set(store.SettingTLSDomain, strings.TrimSpace(req.TLSDomain))
	}
	if strings.TrimSpace(req.ACMEDomain) != "" {
		set(store.SettingACMEDomain, strings.TrimSpace(req.ACMEDomain))
	}
	set(store.SettingACMEEmail, strings.TrimSpace(req.ACMEEmail))

	// 事件告警（§19）：webhook/chat_id 随表单覆盖（允许清空）；bot token 留空保持不变。
	set(store.SettingAlertWebhookURL, req.AlertWebhookURL)
	set(store.SettingAlertTelegramChatID, strings.TrimSpace(req.AlertTelegramChatID))
	if t := strings.TrimSpace(req.AlertTelegramBotToken); t != "" {
		set(store.SettingAlertTelegramBotToken, t)
	}
	set(store.SettingOperationLogLimit, strconv.Itoa(req.OperationLogLimit))
	set(store.SettingRequestLogMaxMB, strconv.Itoa(req.RequestLogMaxMB))
	set(store.SettingRequestLogLevel, req.RequestLogLevel)
	releaseInspectionChanged := req.ReleaseInspection != nil
	billingInspectionChanged := req.BillingInspection != nil
	exchangeInspectionChanged := req.ExchangeInspection != nil
	if req.ReleaseInspection != nil {
		raw, err := json.Marshal(req.ReleaseInspection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		set(store.SettingReleaseInspection, string(raw))
	}
	if req.BillingInspection != nil {
		raw, err := json.Marshal(req.BillingInspection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		set(store.SettingBillingInspection, string(raw))
	}
	if req.ExchangeInspection != nil {
		raw, err := json.Marshal(req.ExchangeInspection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		set(store.SettingExchangeInspection, string(raw))
	}
	set(store.SettingReportingCurrency, req.ReportingCurrency)

	after := map[string]any{
		"timezone": req.Timezone, "traffic_timezone": req.TrafficTimezone,
		"public_url": req.PublicURL, "panel_short": store.EffectivePanelShort(req.PanelShort),
		"trusted_proxies": req.TrustedProxies, "tls_mode": req.TLSMode,
		"tls_domain": tlsDomain, "acme_domain": acmeDomain, "acme_email": strings.TrimSpace(req.ACMEEmail),
		"alert_webhook_set":            req.AlertWebhookURL != "",
		"alert_telegram_chat_id":       strings.TrimSpace(req.AlertTelegramChatID),
		"alert_telegram_bot_token_set": before["alert_telegram_bot_token_set"].(bool) || strings.TrimSpace(req.AlertTelegramBotToken) != "",
		"operation_log_limit":          req.OperationLogLimit, "request_log_max_mb": req.RequestLogMaxMB,
		"request_log_level":  req.RequestLogLevel,
		"release_inspection": before["release_inspection"],
		"billing_inspection": before["billing_inspection"], "exchange_rate_inspection": before["exchange_rate_inspection"],
		"server_settings":    before["server_settings"],
		"reporting_currency": req.ReportingCurrency,
	}
	var serverSettingsRevision int64
	var err error
	if req.ServerSettings != nil {
		serverSettingsRevision, err = s.st.UpdateDefaultServerSettings(ctx, *req.ServerSettings)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		after["server_settings"] = *req.ServerSettings
	}

	updatedAgent, err := s.st.ApplySettings(ctx, mutations, req.Agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// trusted_proxies 立即生效（无需重启）：影响协议推断、订阅链接与日志的
	// X-Forwarded-* 采纳范围，保存即热更新进程级判定；回环与内网/容器网段
	// 为内建默认可信，此处配置是在其上追加公网网段（如 CDN 回源）。
	if err := nettrust.Default.Configure(req.TrustedProxies); err != nil {
		log.Printf("panel: apply trusted proxies: %v", err)
	}
	// 面板缩写进入链路命名与订阅来源分组名：变更后全量用户订阅异步重发布，
	// 使新组名立即生效（快照模型下不触发则维持旧内容）。
	if store.EffectivePanelShort(req.PanelShort) != before["panel_short"] && s.subscriptions != nil {
		users, err := s.st.ListUsers(ctx)
		if err != nil {
			log.Printf("panel: list users for panel_short republish: %v", err)
		} else if len(users) > 0 {
			ids := make([]int64, 0, len(users))
			for _, u := range users {
				ids = append(ids, u.ID)
			}
			s.subscriptions.EnqueueUsers(ids, s.panelBase(r))
		}
	}
	if releaseInspectionChanged || billingInspectionChanged || exchangeInspectionChanged {
		s.scheduler.notifyChanged()
	}
	if s.opLog != nil {
		if err := s.opLog.SetMaxEntries(ctx, req.OperationLogLimit); err != nil {
			log.Printf("panel: apply operation log retention: %v", err)
		}
	}
	if s.reqLog != nil {
		if err := s.reqLog.SetMaxBytes(ctx, int64(req.RequestLogMaxMB)<<20); err != nil {
			log.Printf("panel: apply request log retention: %v", err)
		}
	}
	if updatedAgent != nil {
		s.disp.NotifyAgentSettingsChanged(ctx, updatedAgent.Revision)
	}
	if req.ServerSettings != nil {
		s.disp.NotifyServerSettingsChanged(ctx, 0, serverSettingsRevision)
	}

	if req.ReleaseInspection != nil {
		after["release_inspection"] = *req.ReleaseInspection
	}
	if req.BillingInspection != nil {
		after["billing_inspection"] = *req.BillingInspection
	}
	if req.ExchangeInspection != nil {
		after["exchange_rate_inspection"] = *req.ExchangeInspection
	}
	if req.TLSCertPEM != "" {
		after["tls_certificate"] = "已变更"
	}
	if req.TLSKeyPEM != "" {
		after["tls_private_key"] = "已变更"
	}
	changes := changedValues(before, after)
	// URL 可能在 query 中携带签名，token 也属于凭证；只记录“发生变更”，
	// 避免两个非空值互换时漏记，同时绝不把原值写入日志。
	if req.AlertWebhookURL != beforeWebhookURL {
		changes["alert_webhook_url"] = "已变更"
	}
	if strings.TrimSpace(req.AlertTelegramBotToken) != "" {
		changes["alert_telegram_bot_token"] = "已变更"
	}
	if len(changes) > 0 {
		s.audit(r, "settings.updated", nil, nil, changes)
	}
	if updatedAgent != nil {
		s.audit(r, "agent.settings.updated", nil, nil, map[string]any{
			"revision":        updatedAgent.Revision,
			"reconnect":       updatedAgent.Reconnect,
			"telemetry":       updatedAgent.Telemetry,
			"drift_detection": updatedAgent.DriftDetection,
		})
	}
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
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	passwordOK, err := s.checkPassword(r.Context(), req.CurrentPassword)
	if err != nil {
		if errors.Is(err, errBcryptBusy) {
			writeLoginRateLimit(w, time.Second)
			return
		}
		log.Printf("panel: verify current password: %v", err)
		writeRPC(w, shared.CodeServiceUnavailable, "认证服务暂时不可用", nil)
		return
	}
	if !passwordOK {
		writeError(w, http.StatusForbidden, "当前密码错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码至少 8 位")
		return
	}
	hash, err := s.generatePasswordHash(req.NewPassword)
	if err != nil {
		if errors.Is(err, errBcryptBusy) {
			writeLoginRateLimit(w, time.Second)
			return
		}
		log.Printf("panel: hash new admin password: %v", err)
		writeRPC(w, shared.CodeServiceUnavailable, "认证服务暂时不可用", nil)
		return
	}
	if err := s.st.SetSetting(r.Context(), store.SettingAdminPassBcrypt, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "auth.password_changed", nil, nil, map[string]string{"password": "已变更"})
	writeJSON(w, http.StatusOK, nil)
}

// checkPassword 校验登录密码：DB 中有 bcrypt 哈希则以其为准，否则比对启动参数。
func (s *Server) checkPassword(ctx context.Context, pw string) (bool, error) {
	ok, _, err := s.authenticatePassword(ctx, pw)
	return ok, err
}

// authenticatePassword returns the exact credential snapshot that was
// verified. Login must sign with this snapshot so a concurrent password change
// cannot turn an old-password check into a session signed by the new password.
func (s *Server) authenticatePassword(ctx context.Context, pw string) (bool, string, error) {
	h, err := s.st.GetSetting(ctx, store.SettingAdminPassBcrypt)
	if err != nil {
		return false, "", fmt.Errorf("read admin password hash: %w", err)
	}
	if h != "" {
		if !s.acquireBcryptSlot() {
			return false, "", errBcryptBusy
		}
		defer s.releaseBcryptSlot()
		err := bcrypt.CompareHashAndPassword([]byte(h), []byte(pw))
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, "", nil
		}
		if err != nil {
			return false, "", fmt.Errorf("compare admin password hash: %w", err)
		}
		return true, h, nil
	}
	if subtle.ConstantTimeCompare([]byte(pw), []byte(s.cfg.AdminPass)) != 1 {
		return false, "", nil
	}
	return true, s.cfg.AdminPass, nil
}

// credentialKey 是会话签名密钥的派生源（改密即换源，全部会话失效）。
func (s *Server) credentialKey(ctx context.Context) (string, error) {
	h, err := s.st.GetSetting(ctx, store.SettingAdminPassBcrypt)
	if err != nil {
		return "", fmt.Errorf("read session credential: %w", err)
	}
	if h != "" {
		return h, nil
	}
	return s.cfg.AdminPass, nil
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

func settingInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func changedValues(before, after map[string]any) map[string]any {
	changes := map[string]any{}
	for key, afterValue := range after {
		beforeValue := before[key]
		if !reflect.DeepEqual(beforeValue, afterValue) {
			changes[key] = map[string]any{"before": beforeValue, "after": afterValue}
		}
	}
	return changes
}
