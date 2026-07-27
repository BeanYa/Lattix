// Lattix Backend：面板 HTTP API + Agent WS 端点 + SQLite（设计文档 §2、§3）。
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/bcrypt"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/panel"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	panelweb "lattix/backend/internal/web"
	"lattix/backend/internal/ws"
)

// 构建注入（CI release 经 -ldflags -X 覆盖）：面板版本与默认 GitHub 仓库。
var (
	version    = "dev"
	githubRepo = "BeanYa/Lattix"
)

// defaultTLSDir follows the account that runs the panel. A systemd root service
// therefore uses /root/cert, while a user-run process uses that user's ~/cert.
func defaultTLSDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, "cert")
	}
	return "cert"
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	// 自重启派生的新进程：等待旧进程退出释放监听端口后再启动（panel.restartSelf）。
	if pidStr := os.Getenv("LATTIX_RESTART_WAIT_PID"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil {
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err != nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		fmt.Println(version)
		return
	}

	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	dbPath := flag.String("db", envOr("LATTIX_DB", "lattix.db"), "SQLite 数据库文件路径")
	logDir := flag.String("log-dir", envOr("LATTIX_LOG_DIR", ""), "日志目录；空 = 数据库同级 logs/")
	staticDir := flag.String("static", envOr("LATTIX_STATIC", ""), "frontend 构建产物覆盖目录（空 = 使用二进制内嵌前端）")
	adminUser := flag.String("admin-user", envOr("LATTIX_ADMIN_USER", "admin"), "管理员账号（单管理员，§10）")
	adminPass := flag.String("admin-pass", envOr("LATTIX_ADMIN_PASS", "lattix-admin"), "管理员密码（MVP 本地/受信网络，§12）")
	publicURL := flag.String("public-url", envOr("LATTIX_PUBLIC_URL", ""), "面板对外地址（生成安装命令/订阅链接），默认从请求推断")
	ghRepo := flag.String("github-repo", "", "GitHub 仓库（org/repo，生成 release 安装命令/升级下载基址）；空 = 构建注入值")
	tlsCert := flag.String("tls-cert", "", "TLS 证书文件（自带证书，须与 -tls-key 同用，§12）")
	tlsKey := flag.String("tls-key", "", "TLS 私钥文件")
	tlsDir := flag.String("tls-dir", envOr("LATTIX_TLS_DIR", defaultTLSDir()), "域名路径模式证书根目录（默认 ~/cert；<tls-dir>/<域名>/fullchain.pem|privkey.pem，外部 ACME 写入）")
	acmeDomain := flag.String("tls-acme-domain", "", "ACME 自动证书域名（Let's Encrypt，TLS-ALPN-01，需 443 端口公网可达）")
	acmeCache := flag.String("tls-acme-cache", envOr("LATTIX_ACME_CACHE", "acme-cache"), "ACME 证书缓存目录")
	acmeEmail := flag.String("tls-acme-email", "", "ACME 账号邮箱（可选，过期通知用）")
	resetAdmin := flag.String("reset-admin", "", "重置管理员密码为指定值后退出（不启动面板）；bcrypt 落库覆盖启动参数，改密即全部会话失效（latx reset-admin 使用）")
	flag.Parse()

	// -reset-admin：与设置页改密同一代码路径（bcrypt 哈希写 settings，§10；
	// 会话签名密钥派生自密码哈希，改密即全部会话失效）。面板运行中执行安全（busy_timeout）。
	if *resetAdmin != "" {
		if len(*resetAdmin) < 8 {
			log.Fatal("新密码至少 8 位")
		}
		st, err := store.Open(*dbPath)
		if err != nil {
			log.Fatalf("store: %v", err)
		}
		defer st.Close()
		hash, err := bcrypt.GenerateFromPassword([]byte(*resetAdmin), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("bcrypt: %v", err)
		}
		if err := st.SetSetting(context.Background(), store.SettingAdminPassBcrypt, string(hash)); err != nil {
			log.Fatalf("写入密码哈希: %v", err)
		}
		fmt.Println("管理员密码已重置（覆盖 -admin-pass 启动参数）；所有会话已失效，需重新登录。")
		return
	}

	// 证书根目录统一按绝对路径处理（相对路径按启动时工作目录解析），避免
	// 不同启动方式（nohup/systemd）工作目录不一致导致找不到证书；实际值经 API 展示在设置页。
	tlsDirAbs, err := filepath.Abs(*tlsDir)
	if err != nil {
		log.Fatalf("tls-dir: %v", err)
	}
	dbPathAbs, err := filepath.Abs(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	logDirValue := *logDir
	if logDirValue == "" {
		logDirValue = filepath.Join(filepath.Dir(dbPathAbs), "logs")
	}
	logDirAbs, err := filepath.Abs(logDirValue)
	if err != nil {
		log.Fatalf("log-dir: %v", err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	operationLimit := settingInt(st, store.SettingOperationLogLimit, 1000)
	requestLogMB := settingInt(st, store.SettingRequestLogMaxMB, 10)
	opLog, err := logging.OpenOperationStore(filepath.Join(logDirAbs, "operation.db"), operationLimit)
	if err != nil {
		log.Fatalf("operation log: %v", err)
	}
	defer opLog.Close()
	reqLog, err := logging.OpenRequestLog(filepath.Join(logDirAbs, "requests"), int64(requestLogMB)<<20)
	if err != nil {
		log.Fatalf("request log: %v", err)
	}
	reqLog.SetDropReporter(func(count uint64, reason string) {
		if err := opLog.Record(context.Background(), logging.OperationEvent{
			Severity: logging.SeverityError, Category: logging.CategoryLog,
			Action: "request_log.dropped", Detail: map[string]any{"count": count, "reason": reason},
		}); err != nil {
			log.Printf("main: record dropped request logs: %v", err)
		}
	})

	// TLS 生效解析（§10 设置页）：DB 保存的 tls_mode 优先于启动参数，重启生效。
	// tls_mode 未设置（空）时完全跟随启动参数。
	ctx := context.Background()
	dbTLSMode, _ := st.GetSetting(ctx, store.SettingTLSMode)
	dbCertPEM, _ := st.GetSetting(ctx, store.SettingTLSCertPEM)
	dbKeyPEM, _ := st.GetSetting(ctx, store.SettingTLSKeyPEM)
	dbTLSDomain, _ := st.GetSetting(ctx, store.SettingTLSDomain)
	dbACMEDomain, _ := st.GetSetting(ctx, store.SettingACMEDomain)
	dbACMEEmail, _ := st.GetSetting(ctx, store.SettingACMEEmail)

	// applied 是当前进程实际生效的 TLS 快照，透传给面板用于 restart_required 对比。
	applied := panel.AppliedTLS{Mode: panel.TLSModeOff}
	var keyPair *tls.Certificate
	var dirCertGetter func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	switch dbTLSMode {
	case panel.TLSModeOff:
		// 设置页显式关闭 TLS（覆盖启动参数）。
	case panel.TLSModeCert:
		kp, err := tls.X509KeyPair([]byte(dbCertPEM), []byte(dbKeyPEM))
		if err != nil {
			log.Fatalf("设置页保存的证书/私钥不可用: %v", err)
		}
		keyPair = &kp
		applied = panel.AppliedTLS{Mode: panel.TLSModeCert, CertPEM: dbCertPEM, KeyPEM: dbKeyPEM}
	case panel.TLSModePath:
		// 域名路径模式：证书由外部 ACME（安装脚本）写入 <tls-dir>/<域名>/，
		// GetCertificate 按 mtime 热加载，续期替换文件免重启。
		if !panel.ValidTLSDomain(dbTLSDomain) {
			log.Fatalf("设置页 tls_mode=path 但域名无效: %q", dbTLSDomain)
		}
		dirCertGetter = panel.NewDirCertGetter(tlsDirAbs, dbTLSDomain)
		applied = panel.AppliedTLS{Mode: panel.TLSModePath, Domain: dbTLSDomain}
	case panel.TLSModeACME:
		if dbACMEDomain == "" {
			log.Fatal("设置页 tls_mode=acme 但 acme_domain 为空")
		}
		*acmeDomain = dbACMEDomain
		if dbACMEEmail != "" {
			*acmeEmail = dbACMEEmail
		}
		applied = panel.AppliedTLS{Mode: panel.TLSModeACME, ACMEDomain: *acmeDomain, ACMEEmail: *acmeEmail}
	case "":
		// 跟随启动参数。
		useCert := *tlsCert != "" || *tlsKey != ""
		useACME := *acmeDomain != ""
		if useCert && (*tlsCert == "" || *tlsKey == "") {
			log.Fatal("-tls-cert 与 -tls-key 须同时提供")
		}
		if useCert && useACME {
			log.Fatal("-tls-cert/-tls-key 与 -tls-acme-domain 互斥")
		}
		switch {
		case useACME:
			applied = panel.AppliedTLS{Mode: panel.TLSModeACME, ACMEDomain: *acmeDomain, ACMEEmail: *acmeEmail}
		case useCert:
			kp, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
			if err != nil {
				log.Fatalf("load tls key pair: %v", err)
			}
			keyPair = &kp
			certPEM, _ := os.ReadFile(*tlsCert)
			keyPEM, _ := os.ReadFile(*tlsKey)
			applied = panel.AppliedTLS{Mode: panel.TLSModeCert, CertPEM: string(certPEM), KeyPEM: string(keyPEM)}
		}
	default:
		log.Fatalf("未知的 tls_mode 设置: %q", dbTLSMode)
	}
	secure := applied.Mode != panel.TLSModeOff
	if err := opLog.StartRun(context.Background(), version); err != nil {
		log.Printf("main: record panel start: %v", err)
	}

	// 控制通道（§5）：hub 负责传输，dispatcher 负责命令生命周期与认证。
	hub := ws.NewHub()
	dispatcher := dispatch.New(st, hub, version)
	dispatcher.OperationLog = opLog
	hub.Auth = dispatcher
	hub.OnConnect = func(serverID int64) {
		// agent 重连后重置并补发离线期间滞留的命令（§2）。
		dispatcher.OnAgentConnect(context.Background(), serverID)
		// 链 degraded 推导（§21.1）：重算受影响链，全部跳在线且跳 active 的恢复 active。
		dispatcher.RecomputeChainsByServer(serverID)
	}
	hub.OnOnline = func(serverID int64) {
		sid := serverID
		if err := opLog.Record(context.Background(), logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryAgent, Action: "agent.online",
			ServerID: &sid, Detail: "WS 连接建立，服务器在线",
		}); err != nil {
			log.Printf("main: record agent.online event: %v", err)
		}
	}
	hub.OnReconnect = func(serverID int64) {
		sid := serverID
		if err := opLog.Record(context.Background(), logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryAgent, Action: "agent.reconnected",
			ServerID: &sid, Detail: "新的 WS 连接替换原连接，服务器保持在线",
		}); err != nil {
			log.Printf("main: record agent.reconnected event: %v", err)
		}
	}
	hub.OnMessage = dispatcher.HandleMessage

	// 事件告警（§19）：offline 跃迁挂在 hub 注销路径；漂移/节点失败在 dispatcher 处理点。
	notifier := alert.New(st)
	dispatcher.Alerter = notifier
	hub.OnDisconnect = func(serverID int64) {
		notifier.Notify(serverID, alert.EventServerOffline, "", "WS 连接断开，服务器离线")
		sid := serverID
		if err := opLog.Record(context.Background(), logging.OperationEvent{
			Severity: logging.SeverityWarning, Category: logging.CategoryAgent, Action: "agent.offline",
			ServerID: &sid, Detail: "WS 连接断开，服务器离线",
		}); err != nil {
			log.Printf("main: record agent.offline event: %v", err)
		}
		// 链 degraded 推导（§21.1）：任一跳 server 离线 → degraded + chain_degraded 告警。
		dispatcher.RecomputeChainsByServer(serverID)
	}

	// 面板管理 API（§10）。
	repo := githubRepo
	if *ghRepo != "" {
		repo = *ghRepo
	}
	restartCh := make(chan string, 1)
	ps, err := panel.New(st, dispatcher, hub, panel.Config{
		AdminUser:    *adminUser,
		AdminPass:    *adminPass,
		PublicURL:    *publicURL,
		Secure:       secure,
		RunningTLS:   applied,
		TLSDir:       tlsDirAbs,
		Version:      version,
		GitHubRepo:   repo,
		Alerter:      notifier,
		OperationLog: opLog,
		RequestLog:   reqLog,
		LogDir:       logDirAbs,
		RequestRestart: func(reason string) {
			select {
			case restartCh <- reason:
			default:
			}
		},
	})
	if err != nil {
		log.Fatalf("panel: %v", err)
	}
	hub.OnUpgrade = func(r *http.Request) {
		logging.LogWebSocketUpgrade(reqLog, r, ps.Operator)
	}

	// 用户有效期 sweeper（§9）：到期置 expired 并扇出 remove_user。
	// 默认 1 分钟周期；LATTIX_EXPIRY_SWEEP_INTERVAL（Go duration）可覆盖（dev/e2e 用）。
	sweepInterval := time.Duration(0)
	if v := os.Getenv("LATTIX_EXPIRY_SWEEP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("LATTIX_EXPIRY_SWEEP_INTERVAL 无效: %v", err)
		}
		sweepInterval = d
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go ps.RunExpirySweeper(runCtx, sweepInterval)

	mux := http.NewServeMux()

	// Agent 控制通道（§5）。
	mux.Handle("GET /api/agent/ws", hub)

	// 面板 API（§10、§11）。
	ps.RegisterRoutes(mux)

	// 订阅（§9）：mihomo（Clash.Meta）格式 YAML（浏览器访问为落地页）；/links 为分享链接集合（§14）。
	subSrv := sub.New(st, ps.PanelBase)
	mux.Handle("GET /sub/{token}", subSrv)
	mux.HandleFunc("GET /sub/{token}/links", subSrv.HandleLinks)

	// Frontend SPA 构建产物（§3），客户端路由回退到 index.html。
	frontendFS := panelweb.Dist()
	if *staticDir != "" {
		frontendFS = os.DirFS(*staticDir)
	}
	mux.Handle("/", spaHandler(frontendFS))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logging.RequestMiddleware(reqLog, ps.Operator, mux),
		ReadHeaderTimeout: 15 * time.Second,
	}
	var serve func() error
	switch applied.Mode {
	case panel.TLSModeACME:
		// ACME 自动证书（Let's Encrypt，TLS-ALPN-01：仅需 443 可达，无需 80 端口，§12）。
		m := &autocert.Manager{
			Cache:      autocert.DirCache(*acmeCache),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*acmeDomain),
			Email:      *acmeEmail,
		}
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12}
		log.Printf("lattix backend listening on %s (HTTPS ACME: %s, admin: %s)", *addr, *acmeDomain, *adminUser)
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	case panel.TLSModeCert:
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*keyPair}, MinVersion: tls.VersionTLS12}
		log.Printf("lattix backend listening on %s (HTTPS 自带证书, admin: %s)", *addr, *adminUser)
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	case panel.TLSModePath:
		// 域名路径模式：GetCertificate 按文件 mtime 热加载（外部 ACME 续期免重启）。
		srv.TLSConfig = &tls.Config{GetCertificate: dirCertGetter, MinVersion: tls.VersionTLS12}
		log.Printf("lattix backend listening on %s (HTTPS 域名路径: %s, admin: %s)", *addr, applied.Domain, *adminUser)
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	default:
		log.Printf("lattix backend listening on %s (admin: %s)", *addr, *adminUser)
		serve = srv.ListenAndServe
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- serve() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	reason := ""
	shouldRestart := false
	select {
	case sig := <-signals:
		reason = "signal:" + sig.String()
	case reason = <-restartCh:
		shouldRestart = true
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			reason = "server_error"
			_ = opLog.Record(context.Background(), logging.OperationEvent{
				Severity: logging.SeverityError, Category: logging.CategoryPanel,
				Action: "panel.server_failed", Detail: map[string]string{"error": err.Error()},
			})
			log.Printf("server: %v", err)
		} else {
			reason = "server_closed"
		}
	}

	cancelRun()
	shutdownDeadline := time.Now().Add(5 * time.Second)
	serverCtx, cancelServer := context.WithDeadline(context.Background(), shutdownDeadline.Add(-time.Second))
	if err := srv.Shutdown(serverCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	cancelServer()
	logCtx, cancelLogs := context.WithDeadline(context.Background(), shutdownDeadline)
	defer cancelLogs()
	// 关停阶段不再从请求日志反向写操作日志，确保 panel.stopped 是最后一条
	// 生命周期记录，并优先清除运行标记，避免队列排空超时被误判为异常退出。
	reqLog.SetDropReporter(nil)
	if err := opLog.StopRun(logCtx, reason); err != nil {
		log.Printf("main: record panel stop: %v", err)
	}
	if err := reqLog.Close(logCtx); err != nil {
		log.Printf("main: close request log: %v", err)
	}
	if shouldRestart {
		if err := panel.RestartSelf(); err != nil {
			log.Printf("restart: %v", err)
			_ = opLog.Record(context.Background(), logging.OperationEvent{
				Severity: logging.SeverityError, Category: logging.CategoryPanel,
				Action: "panel.restart_failed", Detail: map[string]string{"error": err.Error()},
			})
		}
	}
}

// spaHandler 服务静态产物；路径不存在时回退 index.html（React SPA 客户端路由）。
func spaHandler(content fs.FS) http.Handler {
	files := http.FileServerFS(content)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r.URL.Path)), "/")
		if name != "." && name != "" {
			if info, err := fs.Stat(content, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(content, "index.html")
		if err != nil {
			http.Error(w, "frontend is not embedded; run the frontend build first or use -static", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

func settingInt(st *store.Store, key string, fallback int) int {
	raw, err := st.GetSetting(context.Background(), key)
	if err != nil || raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
