// Package panel 实现面板管理 API（设计文档 §10）：HTTP + session（账号密码登录，单管理员）。
package panel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
)

// Config 是面板运行配置。
type Config struct {
	AdminUser     string // 管理员账号（单管理员，§14 多管理员属后续迭代）
	AdminPass     string // 管理员密码
	PublicURL     string // 面板对外地址（生成安装命令/订阅链接）；空 = 从请求推断
	DistDir       string // agent 二进制等发布产物目录（/dist/ 托管）
	InstallScript string // install.sh 文件路径（/install.sh 托管）
}

// Server 聚合面板 API 的依赖。
type Server struct {
	st   *store.Store
	disp *dispatch.Dispatcher
	req  ws.Requester
	cfg  Config

	installScript []byte
}

// New 创建面板 API 服务。
func New(st *store.Store, disp *dispatch.Dispatcher, req ws.Requester, cfg Config) (*Server, error) {
	script, err := os.ReadFile(cfg.InstallScript)
	if err != nil {
		return nil, fmt.Errorf("read install script: %w", err)
	}
	return &Server{st: st, disp: disp, req: req, cfg: cfg, installScript: script}, nil
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
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuth(s.handleDeleteServer))

	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.handleListNodes))
	mux.HandleFunc("POST /api/nodes", s.requireAuth(s.handleCreateNode))
	mux.HandleFunc("POST /api/nodes/{id}/retry", s.requireAuth(s.handleRetryNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.requireAuth(s.handleDeleteNode))

	mux.HandleFunc("GET /api/users", s.requireAuth(s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.requireAuth(s.handleCreateUser))
	mux.HandleFunc("DELETE /api/users/{id}", s.requireAuth(s.handleDeleteUser))

	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.Handle("GET /dist/", http.StripPrefix("/dist/", http.FileServer(http.Dir(s.cfg.DistDir))))
}

// handleInstallScript 提供 Agent 引导安装脚本（§11）；参数经一行命令的 --panel/--token 传入。
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(s.installScript)
}

// panelBase 返回面板对外地址：优先配置的 PublicURL，否则从请求推断（MVP 为 HTTP，§12）。
func (s *Server) panelBase(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
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
