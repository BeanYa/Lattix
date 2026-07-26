// Lattix Backend：面板 HTTP API + Agent WS 端点 + SQLite（设计文档 §2、§3）。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/bcrypt"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/panel"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/backend/internal/ws"
)

// 构建注入（CI release 经 -ldflags -X 覆盖）：面板版本与默认 GitHub 仓库。
// version 为 dev 时安装命令回退面板 /resource 托管源（无对应 release 可钉）。
var (
	version    = "dev"
	githubRepo = "BeanYa/Lattix"
)

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
	dbPath := flag.String("db", "lattix.db", "SQLite 数据库文件路径")
	staticDir := flag.String("static", "src/frontend/dist", "frontend 构建产物目录（由 backend 直接托管，§3）")
	adminUser := flag.String("admin-user", "admin", "管理员账号（单管理员，§10）")
	adminPass := flag.String("admin-pass", "lattix-admin", "管理员密码（MVP 本地/受信网络，§12）")
	publicURL := flag.String("public-url", "", "面板对外地址（生成安装命令/订阅链接），默认从请求推断")
	resourceDir := flag.String("resource", "resource", "release 镜像目录（install.sh + agent 包 + checksums.txt，/resource/ 托管，§11）")
	// 已废弃：/dist 托管已移除（改为 /resource release 镜像）。保留该 flag 仅为兼容
	// 旧版 install-panel.sh 生成的 systemd unit（升级后不致启动失败），值被忽略。
	_ = flag.String("dist", "", "已废弃（no-op），改用 -resource")
	installScript := flag.String("install-script", "scripts/install.sh", "install.sh 文件路径（/resource/install.sh 缺失时的 dev 回退）")
	ghRepo := flag.String("github-repo", "", "GitHub 仓库（org/repo，生成 release 安装命令/升级下载基址）；空 = 构建注入值")
	tlsCert := flag.String("tls-cert", "", "TLS 证书文件（自带证书，须与 -tls-key 同用，§12）")
	tlsKey := flag.String("tls-key", "", "TLS 私钥文件")
	tlsDir := flag.String("tls-dir", "certs", "域名路径模式证书根目录（<tls-dir>/<域名>/fullchain.pem|privkey.pem，外部 ACME 写入；启动时解析为绝对路径）")
	acmeDomain := flag.String("tls-acme-domain", "", "ACME 自动证书域名（Let's Encrypt，TLS-ALPN-01，需 443 端口公网可达）")
	acmeCache := flag.String("tls-acme-cache", "acme-cache", "ACME 证书缓存目录")
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

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

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

	// 控制通道（§5）：hub 负责传输，dispatcher 负责命令生命周期与认证。
	hub := ws.NewHub()
	dispatcher := dispatch.New(st, hub, version)
	hub.Auth = dispatcher
	hub.OnConnect = func(serverID int64) {
		// agent 重连后重置并补发离线期间滞留的命令（§2）。
		dispatcher.OnAgentConnect(context.Background(), serverID)
		// 链 degraded 推导（§21.1）：重算受影响链，全部跳在线且跳 active 的恢复 active。
		dispatcher.RecomputeChainsByServer(serverID)
	}
	hub.OnMessage = dispatcher.HandleMessage

	// 事件告警（§19）：offline 跃迁挂在 hub 注销路径；漂移/节点失败在 dispatcher 处理点。
	notifier := alert.New(st)
	dispatcher.Alerter = notifier
	hub.OnDisconnect = func(serverID int64) {
		notifier.Notify(serverID, alert.EventServerOffline, "", "WS 连接断开，服务器离线")
		// 事件日志（§log）：agent 离线跃迁，留痕便于事后排查掉线时间线。
		sid := serverID
		if err := st.RecordEvent(context.Background(), store.EventCategoryAgent, "agent.offline",
			&sid, nil, "WS 连接断开，服务器离线", "", ""); err != nil {
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
	// 前端产物目录按绝对路径处理（与 tls-dir 同理），面板自更新整体替换该目录。
	staticDirAbs, err := filepath.Abs(*staticDir)
	if err != nil {
		log.Fatalf("static dir: %v", err)
	}
	ps, err := panel.New(st, dispatcher, hub, panel.Config{
		AdminUser:     *adminUser,
		AdminPass:     *adminPass,
		PublicURL:     *publicURL,
		Secure:        secure,
		RunningTLS:    applied,
		TLSDir:        tlsDirAbs,
		Version:       version,
		GitHubRepo:    repo,
		StaticDir:     staticDirAbs,
		ResourceDir:   *resourceDir,
		InstallScript: *installScript,
		Alerter:       notifier,
	})
	if err != nil {
		log.Fatalf("panel: %v", err)
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
	go ps.RunExpirySweeper(ctx, sweepInterval)

	mux := http.NewServeMux()

	// Agent 控制通道（§5）。
	mux.Handle("GET /api/agent/ws", hub)

	// 面板 API + install.sh / /resource/ 托管（§10、§11）。
	ps.RegisterRoutes(mux)

	// 订阅（§9）：mihomo（Clash.Meta）格式 YAML（浏览器访问为落地页）；/links 为分享链接集合（§14）。
	subSrv := sub.New(st, ps.PanelBase)
	mux.Handle("GET /sub/{token}", subSrv)
	mux.HandleFunc("GET /sub/{token}/links", subSrv.HandleLinks)

	// Frontend SPA 构建产物（§3），客户端路由回退到 index.html。
	mux.Handle("/", spaHandler(*staticDir))

	srv := &http.Server{Addr: *addr, Handler: mux}
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
		log.Fatal(srv.ListenAndServeTLS("", ""))
	case panel.TLSModeCert:
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*keyPair}, MinVersion: tls.VersionTLS12}
		log.Printf("lattix backend listening on %s (HTTPS 自带证书, admin: %s)", *addr, *adminUser)
		log.Fatal(srv.ListenAndServeTLS("", ""))
	case panel.TLSModePath:
		// 域名路径模式：GetCertificate 按文件 mtime 热加载（外部 ACME 续期免重启）。
		srv.TLSConfig = &tls.Config{GetCertificate: dirCertGetter, MinVersion: tls.VersionTLS12}
		log.Printf("lattix backend listening on %s (HTTPS 域名路径: %s, admin: %s)", *addr, applied.Domain, *adminUser)
		log.Fatal(srv.ListenAndServeTLS("", ""))
	default:
		log.Printf("lattix backend listening on %s (admin: %s)", *addr, *adminUser)
		log.Fatal(srv.ListenAndServe())
	}
}

// spaHandler 服务静态产物；路径不存在时回退 index.html（React SPA 客户端路由）。
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
