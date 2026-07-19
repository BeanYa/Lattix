// Lattix Backend：面板 HTTP API + Agent WS 端点 + SQLite（设计文档 §2、§3）。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/panel"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/backend/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	dbPath := flag.String("db", "lattix.db", "SQLite 数据库文件路径")
	staticDir := flag.String("static", "src/frontend/dist", "frontend 构建产物目录（由 backend 直接托管，§3）")
	adminUser := flag.String("admin-user", "admin", "管理员账号（单管理员，§10）")
	adminPass := flag.String("admin-pass", "lattix-admin", "管理员密码（MVP 本地/受信网络，§12）")
	publicURL := flag.String("public-url", "", "面板对外地址（生成安装命令/订阅链接），默认从请求推断")
	distDir := flag.String("dist", "dist", "agent 二进制等发布产物目录（/dist/ 托管）")
	installScript := flag.String("install-script", "scripts/install.sh", "install.sh 文件路径")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// 控制通道（§5）：hub 负责传输，dispatcher 负责命令生命周期与认证。
	hub := ws.NewHub()
	dispatcher := dispatch.New(st, hub)
	hub.Auth = dispatcher
	hub.OnConnect = func(serverID int64) {
		// agent 重连后补发离线期间滞留的命令（§2）。
		dispatcher.Flush(context.Background(), serverID)
	}
	hub.OnMessage = dispatcher.HandleMessage

	// 面板管理 API（§10）。
	ps, err := panel.New(st, dispatcher, hub, panel.Config{
		AdminUser:     *adminUser,
		AdminPass:     *adminPass,
		PublicURL:     *publicURL,
		DistDir:       *distDir,
		InstallScript: *installScript,
	})
	if err != nil {
		log.Fatalf("panel: %v", err)
	}

	mux := http.NewServeMux()

	// Agent 控制通道（§5）。
	mux.Handle("GET /api/agent/ws", hub)

	// 面板 API + install.sh / /dist/ 托管（§10、§11）。
	ps.RegisterRoutes(mux)

	// 订阅（§9）：mihomo（Clash.Meta）格式 YAML。
	mux.Handle("GET /sub/{token}", sub.New(st))

	// Frontend SPA 构建产物（§3），客户端路由回退到 index.html。
	mux.Handle("/", spaHandler(*staticDir))

	log.Printf("lattix backend listening on %s (admin: %s)", *addr, *adminUser)
	log.Fatal(http.ListenAndServe(*addr, mux))
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
