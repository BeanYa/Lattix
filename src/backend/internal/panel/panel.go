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
	"os"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/extsub"
	"lattix/backend/internal/lifecycle"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/nettrust"
	"lattix/backend/internal/panel/cdn"
	"lattix/backend/internal/panel/exchange"
	"lattix/backend/internal/panel/releases"
	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/panel/selfupdate"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
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
	Lifecycle        *lifecycle.Manager
}

// Server 聚合面板 API 的依赖。
type Server struct {
	st            *store.Store
	disp          *dispatch.Dispatcher
	req           ws.AgentRequester
	lifecycle     *lifecycle.Manager
	cfg           Config
	alerter       *alert.Notifier
	upd           *selfupdate.Updater // 面板自更新状态机（版本检测 + 下载/替换/自重启）
	releases      *releases.Catalog
	exchange      *exchange.Catalog
	cdn           *cdn.Catalog
	subscriptions *sub.Server
	extSubs       *extsub.Service
	onlineUsers   *OnlineUsersTracker // 在线用户快照聚合（telemetry 喂入，用户列表 API 读取）
	scheduler     *scheduler.TaskScheduler
	opLog         *logging.OperationStore
	reqLog        *logging.RequestLog
	observes      *progress.Registry // 旁路操作进度观察（nil = 关闭）
	startedAt     time.Time
	runtimeMu     sync.Mutex
	lastCPU       runtimeCPUSample

	routePolicies         map[string]logging.LogPolicy
	debugRoutes           map[string]bool // 轮询/状态类路由：成功请求记录为 debug 级别
	methodFallbacks       map[string]bool // 已注册 405 回退路由的裸路径（同路径多方法时避免重复注册）
	idempotencyMu         sync.Mutex
	authOnce              sync.Once
	loginAttempts         *loginLimiter
	loginUsernameAttempts *loginLimiter // per-username 兜底桶（防 XFF 伪造绕过 per-IP 限流）
	bcryptSlots           chan struct{}
	tasks                 sync.WaitGroup
}

// SetSubscriptionService wires the snapshot compiler after PanelBase is
// available and before background tasks or HTTP serving start.
func (s *Server) SetSubscriptionService(service *sub.Server) {
	s.subscriptions = service
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "subscription.templates.refresh", RunOnStart: true, Timeout: 10 * time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(6 * time.Hour) },
		Run:     func(ctx context.Context) error { return service.RefreshTemplates(ctx, "") },
	})
}

// SetExternalSubscriptionService wires the external subscription importer and
// its periodic sync task.
func (s *Server) SetExternalSubscriptionService(service *extsub.Service) {
	s.extSubs = service
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "external_subscriptions.sync", Timeout: 10 * time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(15 * time.Minute) },
		Run: func(ctx context.Context) error {
			synced, err := service.SyncDue(ctx)
			if err != nil {
				return err
			}
			s.republishExternalSubUsers(ctx, synced)
			return nil
		},
	})
}

func (s *Server) StartBackgroundTasks(ctx context.Context) {
	if s.subscriptions != nil {
		s.subscriptions.StartRegenerator(ctx)
	}
	s.tasks.Add(1)
	go func() {
		defer s.tasks.Done()
		s.scheduler.Run(ctx)
	}()
}

func (s *Server) WaitBackground(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.tasks.Wait()
		s.upd.Wait()
		close(done)
	}()
	select {
	case <-done:
		if s.subscriptions != nil {
			return s.subscriptions.WaitRegenerator(ctx)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// New 创建面板 API 服务：内部构造 Dispatcher，不可变配置与事件回调经
// dispatch.Options/Events 一次性注入（D2），之后不再写入。
func New(st *store.Store, req ws.AgentRequester, cfg Config) (*Server, error) {
	if cfg.LifecycleContext == nil {
		cfg.LifecycleContext = context.Background()
	}
	s := &Server{
		st: st, req: req, cfg: cfg, alerter: cfg.Alerter, lifecycle: cfg.Lifecycle,
		opLog: cfg.OperationLog, reqLog: cfg.RequestLog,
		onlineUsers:     &OnlineUsersTracker{resolve: onlineUserResolver(st)},
		observes:        progress.NewRegistry(),
		startedAt:       time.Now(),
		routePolicies:   make(map[string]logging.LogPolicy),
		debugRoutes:     make(map[string]bool),
		methodFallbacks: make(map[string]bool),
	}
	// 链编排器与单机节点共用同一份 dest 白名单（§6/§21）。
	opts := dispatch.Options{
		Context:        cfg.LifecycleContext,
		Alerter:        cfg.Alerter,
		OperationLog:   cfg.OperationLog,
		RequestLog:     cfg.RequestLog,
		DestCandidates: destCandidates,
		PanelVersion:   cfg.Version,
		PanelPublicURL: cfg.PublicURL,
	}
	if cfg.GitHubRepo != "" {
		opts.AgentReleaseBase = "https://github.com/" + cfg.GitHubRepo + "/releases/download"
	}
	if cfg.Lifecycle != nil {
		opts.PanelLifecycle = cfg.Lifecycle.Snapshot
	}
	// 发布事件转发给订阅服务（SetSubscriptionService 接线前到达的忽略）；
	// dispatcher 的 telemetry 处理喂入在线用户快照。
	s.disp = dispatch.New(st, req, opts, dispatch.Events{
		OnNodePublished:     s.onNodePublished,
		OnChainPublished:    s.onChainPublished,
		OnEndpointPublished: s.onEndpointPublished,
		OnOnlineUsers:       s.onlineUsers.ApplySnapshot,
	})
	// 观察 ID 读取器注入请求日志中间件（避免 logging → progress 反向依赖）。
	logging.SetObserveIDReader(s.observes.ObserveIDFromContext)
	s.upd = selfupdate.New(cfg.Version, cfg.GitHubRepo, selfupdate.Hooks{
		TransitionLifecycle: func(ctx context.Context, state, fault string, wait bool) error {
			_, err := s.transitionLifecycle(ctx, state, fault, wait)
			return err
		},
		RecordOperation:        s.recordOperation,
		EnqueueAgentUpgradeAll: s.disp.EnqueueAgentUpgradeAll,
		RequestRestart:         cfg.RequestRestart,
	})
	s.releases = releases.New(st, cfg.GitHubRepo)
	s.exchange = exchange.New(st)
	s.cdn = cdn.New(st)
	s.scheduler = scheduler.NewTaskScheduler(s.inspectionLocation)
	s.registerCoreTasks()
	return s, nil
}

// Dispatcher 返回面板持有的命令分发器（New 内部构造；main 据此接线 hub 认证/消息回调）。
func (s *Server) Dispatcher() *dispatch.Dispatcher { return s.disp }

// onNodePublished/onChainPublished/onEndpointPublished 是 dispatcher 发布事件的面板侧
// 实现：订阅服务接线（SetSubscriptionService）前到达的事件直接忽略，与接线前回调为 nil
// 的旧行为一致。
func (s *Server) onNodePublished(ctx context.Context, nodeID int64) error {
	if s.subscriptions == nil {
		return nil
	}
	return s.subscriptions.EnqueueUsersForNode(ctx, nodeID)
}

func (s *Server) onChainPublished(ctx context.Context, chainID int64) error {
	if s.subscriptions == nil {
		return nil
	}
	return s.subscriptions.EnqueueUsersForChain(ctx, chainID)
}

func (s *Server) onEndpointPublished(ctx context.Context, endpointID int64) error {
	if s.subscriptions == nil {
		return nil
	}
	return s.subscriptions.EnqueueUsersForEndpoint(ctx, endpointID)
}

// ObserverRegistry 返回旁路观察注册表（sub regenerator 等侧通道调用方注入用）。
func (s *Server) ObserverRegistry() *progress.Registry {
	if s.observes == nil {
		s.observes = progress.NewRegistry()
	}
	return s.observes
}

// OnlineUsers 返回在线用户快照聚合器（telemetry 帧喂入，用户列表 API 读取）；
// 未显式构造时惰性初始化，避免零值 Server 上误用解引用。
func (s *Server) OnlineUsers() *OnlineUsersTracker {
	if s.onlineUsers == nil {
		s.onlineUsers = &OnlineUsersTracker{resolve: onlineUserResolver(s.st)}
	}
	return s.onlineUsers
}

// envDuration 解析以环境变量覆盖的调度周期：空值、解析失败或非正数时回退默认值。
func envDuration(name string, fallback time.Duration) time.Duration {
	if value := os.Getenv(name); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func (s *Server) registerCoreTasks() {
	expiryInterval := envDuration("LATTIX_EXPIRY_SWEEP_INTERVAL", expirySweepIntervalDefault)
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "user.expiry", RunOnStart: true, Timeout: time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(expiryInterval) },
		Run:     func(ctx context.Context) error { s.sweepExpiredUsers(ctx); return nil },
	})
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "metrics.retention", RunOnStart: true, Timeout: time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(time.Hour) },
		Run:     s.cleanupMetricHistory,
	})
	for _, kind := range []string{releases.KindAgent, releases.KindXray} {
		kind := kind
		s.scheduler.Register(scheduler.ScheduledTask{
			Name: "release." + kind, RunOnStart: true, Timeout: 45 * time.Second,
			Trigger: func(ctx context.Context) scheduler.TaskTrigger {
				settings := s.releaseInspectionSettings(ctx)
				if kind == releases.KindAgent {
					return settings.Agent
				}
				return settings.Xray
			},
			Run: func(ctx context.Context) error { return s.releases.Refresh(ctx, kind) },
		})
	}
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "billing.lifecycle", RunOnStart: true, Timeout: time.Minute,
		Trigger: func(ctx context.Context) scheduler.TaskTrigger { return s.billingInspectionSchedule(ctx) },
		Run:     s.inspectBilling,
	})
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "exchange_rates.refresh", RunOnStart: true, Timeout: 45 * time.Second,
		Trigger: func(ctx context.Context) scheduler.TaskTrigger { return s.exchangeInspectionSchedule(ctx) },
		Run:     s.exchange.Refresh,
	})
	cdnRefreshInterval := envDuration("LATTIX_CDN_REFRESH_INTERVAL", cdn.RefreshIntervalDefault)
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "cdn.catalog.refresh", Timeout: 2 * time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(cdnRefreshInterval) },
		Run:     s.cdn.Refresh,
	})
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "traffic.reset", RunOnStart: true, Timeout: time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(expiryInterval) },
		Run:     s.sweepTrafficReset,
	})
}

// RegisterRoutes 注册面板路由（管理 API 均需登录）。
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	read := rpcRouteOptions{Auth: true}
	polledRead := rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly, Debug: true}
	write := rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true}

	s.registerRPC(mux, http.MethodPost, "/api/auth/login",
		rpcRouteOptions{SameOrigin: true}, s.handleLogin)
	s.registerRPC(mux, http.MethodPost, "/api/auth/logout",
		rpcRouteOptions{Auth: true, CSRF: true}, s.handleLogout)
	s.registerRPC(mux, http.MethodGet, "/api/auth/me",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly, Debug: true}, s.handleMe)

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
	s.registerRPC(mux, http.MethodGet, "/api/server/get-test",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"server_id"}, Debug: true}, s.handleGetServerTest)
	s.registerRPC(mux, http.MethodPost, "/api/server/run-test",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleRunServerTest)
	s.registerRPC(mux, http.MethodGet, "/api/server-test/catalog-status", read, s.handleCDNCatalogStatus)
	s.registerRPC(mux, http.MethodPost, "/api/server-test/refresh-catalog", write, s.handleRefreshCDNCatalog)
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
	s.registerRPC(mux, http.MethodPost, "/api/server/cleanup-xray",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id", "dry_run"}},
		s.handleCleanupXray)
	s.registerRPC(mux, http.MethodPost, "/api/server/rebuild-xray",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id"}},
		s.handleRebuildXray)
	s.registerRPC(mux, http.MethodPost, "/api/server/upgrade-xray",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id", "version"}},
		s.handleUpgradeXray)
	s.registerRPC(mux, http.MethodPost, "/api/server/upgrade-agent",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"server_id", "version"}},
		s.handleUpgradeAgent)
	s.registerRPC(mux, http.MethodPost, "/api/server/confirm-renewal", write, s.handleConfirmRenewal)
	s.registerRPC(mux, http.MethodGet, "/api/billing/stats",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"from", "to", "granularity", "rate_mode"}},
		s.handleBillingStats)
	s.registerRPC(mux, http.MethodGet, "/api/billing/stats/estimated",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"from", "to", "granularity", "rate_mode"}},
		s.handleEstimatedBillingStats)
	s.registerRPC(mux, http.MethodGet, "/api/provider/list", read, s.handleListProviders)
	s.registerRPC(mux, http.MethodPost, "/api/provider/create", write, s.handleCreateProvider)
	s.registerRPC(mux, http.MethodPost, "/api/provider/update", write, s.handleUpdateProvider)
	s.registerRPC(mux, http.MethodPost, "/api/provider/delete", write, s.handleDeleteProvider)
	s.registerRPC(mux, http.MethodGet, "/api/exchange-rate/list", read, s.handleExchangeRates)
	s.registerRPC(mux, http.MethodPost, "/api/exchange-rate/refresh", write, s.handleRefreshExchangeRates)
	s.registerRPC(mux, http.MethodPost, "/api/exchange-rate/save-custom", write, s.handleSaveCustomExchangeRate)
	s.registerRPC(mux, http.MethodPost, "/api/exchange-rate/delete-custom", write, s.handleDeleteCustomExchangeRate)
	s.registerRPC(mux, http.MethodGet, "/api/server/list-release-versions",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"kind"}}, s.handleListReleaseVersions)

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
	s.registerRPC(mux, http.MethodPost, "/api/chain/edit", write, s.handleEditChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/force-publish", write, s.handleForcePublishChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/retry",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"chain_id"}},
		s.handleRetryChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"chain_id"}},
		s.handleDeleteChain)
	s.registerRPC(mux, http.MethodPost, "/api/chain/set-traffic-multiplier", write, s.handleSetChainTrafficMultiplier)
	s.registerRPC(mux, http.MethodPost, "/api/chain/reset-traffic", write, s.handleResetChainTraffic)
	s.registerRPC(mux, http.MethodGet, "/api/chain/get-traffic-history",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"chain_id", "hop_id", "days"}},
		s.handleGetChainTrafficHistory)

	s.registerRPC(mux, http.MethodGet, "/api/user/list", polledRead, s.handleListUsers)
	s.registerRPC(mux, http.MethodPost, "/api/user/create", write, s.handleCreateUser)
	s.registerRPC(mux, http.MethodPost, "/api/user/update",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleUpdateUser)
	s.registerRPC(mux, http.MethodPost, "/api/user/set-nodes",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleSetUserNodes)
	s.registerRPC(mux, http.MethodPost, "/api/user/set-external-subscriptions",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleSetUserExternalSubscriptions)
	s.registerRPC(mux, http.MethodPost, "/api/user/delete",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleDeleteUser)
	s.registerRPC(mux, http.MethodPost, "/api/user/sub-settings",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleUpdateUserSubSettings)
	s.registerRPC(mux, http.MethodGet, "/api/user/traffic-history",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"user_id"}},
		s.handleUserTrafficHistory)
	s.registerRPC(mux, http.MethodPost, "/api/user/regenerate-subscription",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleRegenerateUserSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/user/reset-subscription-token",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleResetUserSubscriptionToken)
	s.registerRPC(mux, http.MethodGet, "/api/user/subscription-preview",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"user_id", "format", "stage"}},
		s.handleUserSubscriptionPreview)

	s.registerRPC(mux, http.MethodGet, "/api/subscription/categories", read, s.handleSubscriptionCategories)
	s.registerRPC(mux, http.MethodGet, "/api/subscription/templates", read, s.handleSubscriptionTemplates)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/save", write, s.handleSaveSubscriptionTemplate)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/clone", write, s.handleCloneSubscriptionTemplate)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/delete", write, s.handleDeleteSubscriptionTemplate)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/refresh", write, s.handleRefreshSubscriptionTemplates)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/assign",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_ids", "template_id"}},
		s.handleAssignSubscriptionTemplate)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/unassign",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_ids", "template_id"}},
		s.handleUnassignSubscriptionTemplate)

	s.registerRPC(mux, http.MethodGet, "/api/external-subscription/list", read, s.handleListExternalSubscriptions)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/create", write, s.handleCreateExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/update", write, s.handleUpdateExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/delete", write, s.handleDeleteExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/sync", write, s.handleSyncExternalSubscription)
	s.registerRPC(mux, http.MethodGet, "/api/external-subscription/chains",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"id"}}, s.handleListExternalChains)

	s.registerRPC(mux, http.MethodGet, "/api/link-group/list", read, s.handleListLinkGroups)
	s.registerRPC(mux, http.MethodPost, "/api/link-group/create", write, s.handleCreateLinkGroup)
	s.registerRPC(mux, http.MethodPost, "/api/link-group/update", write, s.handleUpdateLinkGroup)
	s.registerRPC(mux, http.MethodPost, "/api/link-group/delete", write, s.handleDeleteLinkGroup)
	s.registerRPC(mux, http.MethodGet, "/api/user-group/list", read, s.handleListUserGroups)
	s.registerRPC(mux, http.MethodPost, "/api/user-group/create", write, s.handleCreateUserGroup)
	s.registerRPC(mux, http.MethodPost, "/api/user-group/update", write, s.handleUpdateUserGroup)
	s.registerRPC(mux, http.MethodPost, "/api/user-group/delete", write, s.handleDeleteUserGroup)

	s.registerRPC(mux, http.MethodGet, "/api/setting/get", read, s.handleGetSettings)
	s.registerRPC(mux, http.MethodPost, "/api/setting/update", write, s.handleUpdateSettings)
	s.registerRPC(mux, http.MethodPost, "/api/setting/change-password",
		rpcRouteOptions{Auth: true, CSRF: true}, s.handleChangePassword)
	s.registerRPC(mux, http.MethodPost, "/api/setting/test-alerts", write, s.handleTestAlerts)
	s.registerRPC(mux, http.MethodGet, "/api/setting/sub", read, s.handleGetSubSettings)
	s.registerRPC(mux, http.MethodPost, "/api/setting/sub", write, s.handleUpdateSubSettings)

	s.registerRPC(mux, http.MethodPost, "/api/panel/restart", write, s.handleRestart)
	s.registerRPC(mux, http.MethodGet, "/api/panel/state",
		rpcRouteOptions{Auth: true, Debug: true}, s.handlePanelState)
	s.registerRPC(mux, http.MethodGet, "/api/panel/runtime",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogNone, Debug: true}, s.handlePanelRuntime)
	s.registerRPC(mux, http.MethodGet, "/api/panel/get-version", read, s.handlePanelVersion)
	s.registerRPC(mux, http.MethodPost, "/api/panel/start-update", write, s.handlePanelUpdateStart)
	s.registerRPC(mux, http.MethodGet, "/api/panel/get-update-status",
		rpcRouteOptions{Auth: true, LogPolicy: logging.LogFailuresOnly, Debug: true}, s.handlePanelUpdateStatus)

	s.registerRPC(mux, http.MethodGet, "/api/observe-task/get",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"observe_id"}, LogPolicy: logging.LogFailuresOnly, Debug: true},
		s.handleGetObserveTask)

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
// 尾斜杠统一去除：安装命令按字符串拼接 /api/agent/ws，带尾斜杠会拼出 //api/agent/ws，
// ServeMux 返回 307 而 agent 不跟随重定向，表现为永久 bad handshake。
// HTTPS 判定：面板自身 TLS、直连 TLS，或反代经 X-Forwarded-Proto 声明（§12）。
func (s *Server) panelBase(r *http.Request) string {
	if v := s.getSetting(r.Context(), store.SettingPublicURL); v != "" {
		return strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/")
	}
	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// isSecure 报告当前请求是否经 HTTPS 到达（含反代终止 TLS 的场景）。
// 反代声明（X-Forwarded-Proto）仅在可信对端时采信：回环、内建内网/容器网段
// （docker 桥接、局域网、CGNAT 等，1panel/openresty 部署无需配置即命中），
// 或设置页 trusted_proxies 追加的网段（如 CDN 回源）；公网对端直连不采信，防伪造。
// 信任判定统一收敛在 nettrust。
func (s *Server) isSecure(r *http.Request) bool {
	if s.cfg.Secure || r.TLS != nil {
		return true
	}
	return nettrust.Default.ForwardedHTTPS(r)
}

type rpcResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
	ObserveID string `json:"observe_id,omitempty"`
}

type rpcResponseWriter interface {
	SetRPCOutcome(code, safeMessage string)
	RPCIDs() (requestID, traceID string)
	ObserveID() string
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
	observeID := ""
	if rw, ok := w.(rpcResponseWriter); ok {
		observeID = rw.ObserveID()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		Code: code, Message: message, Data: data, RequestID: requestID, TraceID: traceID, ObserveID: observeID,
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
	if requestID == "" {
		requestID = shared.NewMessageID()
	}
	if traceID == "" {
		traceID = requestID
	}
	observeID := ""
	if rw, ok := w.(rpcResponseWriter); ok {
		observeID = rw.ObserveID()
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		Code:      fmt.Sprintf("HTTP_%d", status),
		Message:   message,
		Data:      nil,
		RequestID: requestID,
		TraceID:   traceID,
		ObserveID: observeID,
	})
}

// WriteProtocolError writes the protocol envelope used by API fallbacks outside Server routes.
func WriteProtocolError(w http.ResponseWriter, status int, message string) {
	writeProtocolError(w, status, message)
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
