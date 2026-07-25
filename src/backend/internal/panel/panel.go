// Package panel 实现面板管理 API（设计文档 §10）：HTTP + session（账号密码登录，单管理员）。
package panel

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
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
	Version       string          // 面板版本（构建注入）；dev 时安装命令回退面板托管模式
	GitHubRepo    string          // GitHub 仓库（org/repo）：release 安装命令与 agent 升级下载基址
	DistDir       string          // agent 二进制等发布产物目录（/dist/ 托管）
	InstallScript string          // install.sh 文件路径（/install.sh 托管）
	Alerter       *alert.Notifier // 事件告警（§19）；nil = 关闭
}

// Server 聚合面板 API 的依赖。
type Server struct {
	st      *store.Store
	disp    *dispatch.Dispatcher
	req     ws.Requester
	cfg     Config
	alerter *alert.Notifier

	installScript []byte
}

// New 创建面板 API 服务。
func New(st *store.Store, disp *dispatch.Dispatcher, req ws.Requester, cfg Config) (*Server, error) {
	script, err := os.ReadFile(cfg.InstallScript)
	if err != nil {
		return nil, fmt.Errorf("read install script: %w", err)
	}
	// 注入 dist 中 agent 二进制的 SHA256（明文 HTTP 下的完整性锚点，§11/§12）。
	script = injectAgentSHA256(script, cfg.DistDir)
	return &Server{st: st, disp: disp, req: req, cfg: cfg, alerter: cfg.Alerter, installScript: script}, nil
}

// injectAgentSHA256 将 install.sh 中的 {{AGENT_SHA256_<ARCH>}} 占位符替换为
// dist 目录下对应二进制的 SHA256；文件缺失时替换为 SKIP（install.sh 跳过校验）。
func injectAgentSHA256(script []byte, distDir string) []byte {
	for _, arch := range []string{"amd64", "arm64"} {
		sum := "SKIP"
		if b, err := os.ReadFile(filepath.Join(distDir, "lattix-agent-linux-"+arch)); err == nil {
			h := sha256.Sum256(b)
			sum = hex.EncodeToString(h[:])
		}
		script = bytes.ReplaceAll(script,
			[]byte("{{AGENT_SHA256_"+strings.ToUpper(arch)+"}}"), []byte(sum))
	}
	return script
}

// RegisterRoutes 注册面板路由（管理 API 均需登录；install.sh 与 /dist/ 公开，§11 引导流程）。
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
	mux.Handle("GET /dist/", http.StripPrefix("/dist/", http.FileServer(http.Dir(s.cfg.DistDir))))
}

// handleInstallScript 提供 Agent 引导安装脚本（§11）；参数经一行命令的 --panel/--token 传入。
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(s.installScript)
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
