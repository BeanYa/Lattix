// Package panel 实现面板管理 API（设计文档 §10）：HTTP + session（账号密码登录，单管理员）。
package panel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
)

// Config 是面板运行配置。
type Config struct {
	AdminUser     string          // 管理员账号（单管理员，§14 多管理员属后续迭代）
	AdminPass     string          // 管理员密码（DB 设置页改密后被 bcrypt 哈希覆盖）
	PublicURL     string          // 面板对外地址（生成安装命令/订阅链接）；空 = 从请求推断（DB 设置可覆盖）
	Secure        bool            // 面板自身以 TLS 服务（自带证书或 ACME，§12）
	RunningTLS    AppliedTLS      // 当前进程实际生效的 TLS 快照（main 启动时确定，重启生效）
	TLSDir        string          // 域名路径模式证书根目录（<tls-dir>/<域名>/fullchain.pem|privkey.pem）
	Version       string          // 面板版本（构建注入）；dev 时安装命令回退面板 /resource 托管源
	GitHubRepo    string          // GitHub 仓库（org/repo）：release 安装命令与 agent 升级下载基址
	ResourceDir   string          // release 镜像目录（install.sh + agent 包 + checksums.txt，/resource/ 托管）
	InstallScript string          // install.sh 文件路径（/resource/install.sh 缺失时的 dev 回退）
	Alerter       *alert.Notifier // 事件告警（§19）；nil = 关闭
}

// Server 聚合面板 API 的依赖。
type Server struct {
	st      *store.Store
	disp    *dispatch.Dispatcher
	req     ws.Requester
	cfg     Config
	alerter *alert.Notifier

	installScript []byte // dev 回退脚本（-install_script 指定的原始脚本，可能未 stamp）
}

// New 创建面板 API 服务。
func New(st *store.Store, disp *dispatch.Dispatcher, req ws.Requester, cfg Config) (*Server, error) {
	// dev 回退脚本读取失败不致命（正式部署经 /resource/install.sh 托管）。
	script, _ := os.ReadFile(cfg.InstallScript)
	// 链编排器与单机节点共用同一份 dest 白名单（§6/§21）。
	disp.DestCandidates = destCandidates
	return &Server{st: st, disp: disp, req: req, cfg: cfg, alerter: cfg.Alerter, installScript: script}, nil
}

// RegisterRoutes 注册面板路由（管理 API 均需登录；install.sh 与 /resource/ 公开，§11 引导流程）。
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/dashboard", s.requireAuth(s.handleDashboard))

	mux.HandleFunc("GET /api/servers", s.requireAuth(s.handleListServers))
	mux.HandleFunc("POST /api/servers", s.requireAuth(s.handleCreateServer))
	mux.HandleFunc("POST /api/servers/{id}/rotate-token", s.requireAuth(s.handleRotateToken))
	mux.HandleFunc("POST /api/servers/{id}/repair", s.requireAuth(s.handleRepairServer))
	mux.HandleFunc("POST /api/servers/{id}/upgrade", s.requireAuth(s.handleUpgradeXray))
	mux.HandleFunc("POST /api/servers/{id}/upgrade-agent", s.requireAuth(s.handleUpgradeAgent))
	mux.HandleFunc("PATCH /api/servers/{id}", s.requireAuth(s.handleUpdateServer))
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuth(s.handleDeleteServer))

	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.handleListNodes))
	mux.HandleFunc("POST /api/nodes", s.requireAuth(s.handleCreateNode))
	mux.HandleFunc("POST /api/nodes/{id}/retry", s.requireAuth(s.handleRetryNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.requireAuth(s.handleDeleteNode))

	mux.HandleFunc("GET /api/chains", s.requireAuth(s.handleListChains))
	mux.HandleFunc("POST /api/chains", s.requireAuth(s.handleCreateChain))
	mux.HandleFunc("POST /api/chains/{id}/retry", s.requireAuth(s.handleRetryChain))
	mux.HandleFunc("DELETE /api/chains/{id}", s.requireAuth(s.handleDeleteChain))

	mux.HandleFunc("GET /api/users", s.requireAuth(s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.requireAuth(s.handleCreateUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.requireAuth(s.handleUpdateUser))
	mux.HandleFunc("PUT /api/users/{id}/nodes", s.requireAuth(s.handleSetUserNodes))
	mux.HandleFunc("DELETE /api/users/{id}", s.requireAuth(s.handleDeleteUser))

	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handleUpdateSettings))
	mux.HandleFunc("PUT /api/settings/password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("POST /api/settings/restart", s.requireAuth(s.handleRestart))
	mux.HandleFunc("POST /api/settings/alerts/test", s.requireAuth(s.handleTestAlerts))

	mux.HandleFunc("GET /api/backup", s.requireAuth(s.handleBackup))

	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	// /resource/ 托管 release 镜像（install.sh + agent 包 + checksums.txt，与 GitHub
	// release 同布局），由面板安装/更新时落地，供 install.sh --source panel 下载（§11）。
	mux.Handle("GET /resource/", http.StripPrefix("/resource/", http.FileServer(http.Dir(s.cfg.ResourceDir))))
}

// handleInstallScript 提供 Agent 引导安装脚本（§11）；参数经一行命令的 --panel/--token 传入。
// 优先托管 resource 镜像中的 CI stamp 版（与面板同版本）；缺失时回退 -install-script
// 指定的原始脚本（dev 用，占位符未替换时仅 --source panel 可执行）。
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if p := filepath.Join(s.cfg.ResourceDir, "install.sh"); fileExists(p) {
		http.ServeFile(w, r, p)
		return
	}
	if len(s.installScript) > 0 {
		w.Write(s.installScript)
		return
	}
	http.NotFound(w, r)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// PanelBase 导出 panelBase，供订阅落地页生成绝对链接（与面板 DTO 同一判定链）。
func (s *Server) PanelBase(r *http.Request) string { return s.panelBase(r) }

// panelBase 返回面板对外地址：DB 设置（设置页）> 启动参数 PublicURL > 从请求推断。
// HTTPS 判定：面板自身 TLS、直连 TLS，或反代经 X-Forwarded-Proto 声明（§12）。
func (s *Server) panelBase(r *http.Request) string {
	if v := s.getSetting(r.Context(), store.SettingPublicURL); v != "" {
		return v
	}
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// isSecure 报告当前请求是否经 HTTPS 到达（含反代终止 TLS 的场景）。
func (s *Server) isSecure(r *http.Request) bool {
	return s.cfg.Secure || r.TLS != nil ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// randomHex 生成 n 字节随机十六进制串（bootstrap token、sub_token、short_id 等）。
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
