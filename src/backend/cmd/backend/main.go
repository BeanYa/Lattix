// Lattix Backend：面板 HTTP API + Agent WS 端点 + SQLite（设计文档 §2、§3）。
package main

import (
	"flag"
	"log"
	"net/http"

	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	dbPath := flag.String("db", "lattix.db", "SQLite 数据库文件路径")
	staticDir := flag.String("static", "src/frontend/dist", "frontend 构建产物目录（由 backend 直接托管，§3）")
	flag.Parse()

	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	mux := http.NewServeMux()

	// Agent 控制通道（§5）。
	mux.Handle("GET /api/agent/ws", ws.AgentHandler(db))

	// 订阅（§9）：mihomo（Clash.Meta）格式 YAML。
	mux.HandleFunc("GET /sub/{token}", notImplemented("subscription"))

	// 面板管理 API（§10）：HTTP + session（账号密码登录）。
	mux.HandleFunc("POST /api/login", notImplemented("login"))
	mux.HandleFunc("GET /api/dashboard", notImplemented("dashboard"))
	mux.HandleFunc("GET /api/servers", notImplemented("servers"))
	mux.HandleFunc("POST /api/servers", notImplemented("servers"))
	mux.HandleFunc("GET /api/nodes", notImplemented("nodes"))
	mux.HandleFunc("POST /api/nodes", notImplemented("nodes"))
	mux.HandleFunc("GET /api/users", notImplemented("users"))
	mux.HandleFunc("POST /api/users", notImplemented("users"))

	// Frontend SPA 构建产物（§3）。
	mux.Handle("/", http.FileServer(http.Dir(*staticDir)))

	log.Printf("lattix backend listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func notImplemented(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, name+": not implemented (MVP skeleton)", http.StatusNotImplemented)
	}
}
