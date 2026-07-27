// Package panel 实现面板管理 API（设计文档 §10）：HTTP + session（账号密码登录，单管理员）。
package panel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// Config 是面板运行配置。
type Config struct {
	AdminUser        string          // 管理员账号（单管理员，§14 多管理员属后续迭代）
	AdminPass        string          // 管理员密码（DB 设置页改密后被 bcrypt 哈希覆盖）
	PublicURL        string          // 面板对外地址（生成安装命令/订阅链接）；空 = 从请求推断（DB 设置可覆盖）
	Secure           bool            // 面板自身以 TLS 服务（自带证书或 ACME，§12）
	RunningTLS       AppliedTLS      // 当前进程实际生效的 TLS 快照（main 启动时确定，重启生效）
	TLSDir           string          // 域名路径模式证书根目录（<tls-dir>/<域名>/fullchain.pem|privkey.pem）
	Version          string          // 面板版本（构建注入）
	GitHubRepo       string          // GitHub 仓库（org/repo）：release 安装命令、面板自更新与 agent 升级下载基址
	Alerter          *alert.Notifier // 事件告警（§19）；nil = 关闭
	OperationLog     *logging.OperationStore
	RequestLog       *logging.RequestLog
	LogDir           string
	RequestRestart   func(reason string) error
	LifecycleContext context.Context
}

// Server 聚合面板 API 的依赖。
type Server struct {
	st      *store.Store
	disp    *dispatch.Dispatcher
	req     ws.AgentRequester
	cfg     Config
	alerter *alert.Notifier
	upd     *panelUpdater // 面板自更新状态机（版本检测 + 下载/替换/自重启）
	opLog   *logging.OperationStore
	reqLog  *logging.RequestLog

	routePolicies map[string]logging.LogPolicy
	idempotencyMu sync.Mutex
	tasks         sync.WaitGroup
}

func (s *Server) StartExpirySweeper(ctx context.Context, interval time.Duration) {
	s.tasks.Add(1)
	go func() {
		defer s.tasks.Done()
		s.RunExpirySweeper(ctx, interval)
	}()
}

func (s *Server) WaitBackground(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.tasks.Wait()
		s.upd.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// New 创建面板 API 服务。
func New(st *store.Store, disp *dispatch.Dispatcher, req ws.AgentRequester, cfg Config) (*Server, error) {
	if cfg.LifecycleContext == nil {
		cfg.LifecycleContext = context.Background()
	}
	// 链编排器与单机节点共用同一份 dest 白名单（§6/§21）。
	disp.DestCandidates = destCandidates
	disp.PanelVersion = cfg.Version
	disp.PanelPublicURL = cfg.PublicURL
	s := &Server{
		st: st, disp: disp, req: req, cfg: cfg, alerter: cfg.Alerter,
		opLog: cfg.OperationLog, reqLog: cfg.RequestLog,
		routePolicies: make(map[string]logging.LogPolicy),
	}
	s.upd = newPanelUpdater(s)
	return s, nil
}

// RegisterRoutes 注册面板路由（管理 API 均需登录）。
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	read := rpcRouteOptions{Auth: true}
	polledRead := rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly}
	write := rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true}

	s.registerRPC(mux, http.MethodPost, "/api/auth/login",
		rpcRouteOptions{SameOrigin: true}, s.handleLogin)
	s.registerRPC(mux, http.MethodPost, "/api/auth/logout",
		rpcRouteOptions{Auth: true, CSRF: true}, s.handleLogout)
	s.registerRPC(mux, http.MethodGet, "/api/auth/me",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly}, s.handleMe)

	s.registerRPC(mux, http.MethodGet, "/api/dashboard/get", polledRead, s.handleDashboard)

	s.registerRPC(mux, http.MethodGet, "/api/server/list", polledRead, s.handleListServers)
	s.registerRPC(mux, http.MethodGet, "/api/server/list-metric-samples",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogNone, AllowedQuery: []string{"limit"}},
		s.handleListMetricSamples)
	s.registerRPC(mux, http.MethodGet, "/api/server/get-metric-history",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogNone, AllowedQuery: []string{"server_id", "hours"}},
		s.handleGetMetricHistory)
	s.registerRPC(mux, http.MethodGet, "/api/server/list-commands",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"server_id", "limit"}},
		s.handleListCommands)
	s.registerRPC(mux, http.MethodPost, "/api/server/create", write, s.handleCreateServer)
	s.registerRPC(mux, http.MethodPost, "/api/server/update",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleUpdateServer)
	s.registerRPC(mux, http.MethodPost, "/api/server/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleDeleteServer)
	s.registerRPC(mux, http.MethodPost, "/api/server/rotate-token",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleRotateToken)
	s.registerRPC(mux, http.MethodPost, "/api/server/repair",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleRepairServer)
	s.registerRPC(mux, http.MethodPost, "/api/server/upgrade-xray",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id", "version"}},
		s.handleUpgradeXray)
	s.registerRPC(mux, http.MethodPost, "/api/server/upgrade-agent",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id", "version"}},
		s.handleUpgradeAgent)

	s.registerRPC(mux, http.MethodGet, "/api/node/list", read, s.handleListNodes)
	s.registerRPC(mux, http.MethodPost, "/api/node/create", write, s.handleCreateNode)
	s.registerRPC(mux, http.MethodPost, "/api/node/retry",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"node_id"}},
		s.handleRetryNode)
	s.registerRPC(mux, http.MethodPost, "/api/node/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"node_id"}},
		s.handleDeleteNode)

	s.registerRPC(mux, http.MethodGet, "/api/chain/list", polledRead, s.handleListChains)
	s.registerRPC(mux, http.MethodPost, "/api/chain/create", write, s.handleCreateChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/retry",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"chain_id"}},
		s.handleRetryChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"chain_id"}},
		s.handleDeleteChain)

	s.registerRPC(mux, http.MethodGet, "/api/user/list", polledRead, s.handleListUsers)
	s.registerRPC(mux, http.MethodPost, "/api/user/create", write, s.handleCreateUser)
	s.registerRPC(mux, http.MethodPost, "/api/user/update",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleUpdateUser)
	s.registerRPC(mux, http.MethodPost, "/api/user/set-nodes",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleSetUserNodes)
	s.registerRPC(mux, http.MethodPost, "/api/user/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleDeleteUser)

	s.registerRPC(mux, http.MethodGet, "/api/setting/get", read, s.handleGetSettings)
	s.registerRPC(mux, http.MethodPost, "/api/setting/update", write, s.handleUpdateSettings)
	s.registerRPC(mux, http.MethodPost, "/api/setting/change-password",
		rpcRouteOptions{Auth: true, CSRF: true}, s.handleChangePassword)
	s.registerRPC(mux, http.MethodPost, "/api/setting/test-alerts", write, s.handleTestAlerts)

	s.registerRPC(mux, http.MethodPost, "/api/panel/restart", write, s.handleRestart)
	s.registerRPC(mux, http.MethodGet, "/api/panel/get-version", read, s.handlePanelVersion)
	s.registerRPC(mux, http.MethodPost, "/api/panel/start-update", write, s.handlePanelUpdateStart)
	s.registerRPC(mux, http.MethodGet, "/api/panel/get-update-status",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly}, s.handlePanelUpdateStatus)

	s.registerRPC(mux, http.MethodGet, "/api/backup/download", read, s.handleBackup)

	logRead := rpcRouteOptions{
		Auth: true, LogPolicy: logging.LogNone,
		AllowedQuery: []string{"severity", "category", "server_id", "operator", "q", "from", "to", "limit", "offset"},
	}
	s.registerRPC(mux, http.MethodGet, "/api/log/list-operations", logRead, s.handleListOperationLog)
	s.registerRPC(mux, http.MethodGet, "/api/log/list-requests",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogNone, AllowedQuery: []string{"limit"}},
		s.handleListRequestLog)
	s.registerRPC(mux, http.MethodPost, "/api/log/clear-operations", write, s.handleClearOperationLog)
	s.registerRPC(mux, http.MethodPost, "/api/log/clear-requests", write, s.handleClearRequestLog)
}

// Operator 返回当前请求对应的管理员名称，供外层请求日志中间件记录。
func (s *Server) Operator(r *http.Request) string {
	operator, _ := s.currentUser(r)
	return operator
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

type rpcResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

type rpcResponseWriter interface {
	SetRPCOutcome(code, safeMessage string)
	RPCIDs() (requestID, traceID string)
}

func writeJSON(w http.ResponseWriter, legacyCode int, data any) {
	code := shared.CodeOK
	if legacyCode == http.StatusAccepted {
		code = shared.CodeAccepted
	}
	writeRPC(w, code, "", data)
}

func writeRPC(w http.ResponseWriter, code, message string, data any) {
	requestID, traceID := "", ""
	if rw, ok := w.(rpcResponseWriter); ok {
		rw.SetRPCOutcome(code, message)
		requestID, traceID = rw.RPCIDs()
	}
	if requestID == "" {
		requestID = shared.NewMessageID()
	}
	if traceID == "" {
		traceID = requestID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		Code: code, Message: message, Data: data, RequestID: requestID, TraceID: traceID,
	})
}

func writeError(w http.ResponseWriter, legacyCode int, msg string) {
	writeRPC(w, rpcCodeForLegacyStatus(legacyCode), msg, nil)
}

func readJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeProtocolError(w http.ResponseWriter, status int, message string) {
	requestID, traceID := "", ""
	if rw, ok := w.(rpcResponseWriter); ok {
		requestID, traceID = rw.RPCIDs()
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		Code:      fmt.Sprintf("HTTP_%d", status),
		Message:   message,
		Data:      nil,
		RequestID: requestID,
		TraceID:   traceID,
	})
}

func rpcCodeForLegacyStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return shared.CodeAuthRequired
	case http.StatusForbidden:
		return shared.CodeAuthInvalidCredentials
	case http.StatusNotFound:
		return shared.CodeNotFound
	case http.StatusConflict:
		return shared.CodeConflict
	case http.StatusLocked:
		return shared.CodeOperationLocked
	case http.StatusBadGateway:
		return shared.CodeUpstreamError
	case http.StatusServiceUnavailable:
		return shared.CodeServiceUnavailable
	case http.StatusInternalServerError:
		return shared.CodeInternalError
	default:
		return shared.CodeInvalidArgument
	}
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

// randomHex 生成 n 字节随机十六进制串（bootstrap token、sub_token、short_id 等）。
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
