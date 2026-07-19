// Package ws 实现 Agent 控制通道端点（设计文档 §5）：
// Agent 携带服务器 token 拨出至 /api/agent/ws，Backend 永不主动外连 Agent。
package ws

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

var upgrader = websocket.Upgrader{
	// MVP 运行于本地/受信网络（§12）。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AgentHandler 返回 /api/agent/ws 的处理函数。在线/离线状态由 WS 连接是否存在直接推导（§5）。
func AgentHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade: %v", err)
			return
		}
		defer conn.Close()
		log.Printf("agent connected: %s", r.RemoteAddr)

		// TODO(MVP): hello 认证（bootstrap token 换发长期凭证，§5/§11）、在线状态登记、
		// commands 表离线队列补发（§2）、消息分发与 apply_result 处理。
		for {
			var env shared.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				log.Printf("agent disconnected: %v", err)
				return
			}
			log.Printf("recv type=%s id=%s", env.Type, env.ID)
		}
	}
}
