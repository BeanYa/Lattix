// Lattix Agent：受控服务器上的独立二进制，systemd 托管（设计文档 §3）。
// 主动拨出至 Backend /api/agent/ws，一条 WS 长连接承担全部双向通信（§2）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

var version = "dev"

func main() {
	panel := flag.String("panel", "ws://127.0.0.1:8080/api/agent/ws", "Backend WS 地址")
	token := flag.String("token", "", "bootstrap token 或长期服务器 token（§11）")
	flag.Parse()

	if *token == "" {
		log.Fatal("-token is required")
	}

	for {
		if err := run(*panel, *token); err != nil {
			log.Printf("connection: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(panel, token string) error {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 首连认证（§5）：bootstrap token 在此换发长期凭证。
	hello := shared.Envelope{
		ID:   fmt.Sprintf("hello-%d", time.Now().UnixNano()),
		Type: shared.TypeHello,
		Payload: mustJSON(shared.HelloPayload{
			Token:        token,
			AgentVersion: version,
			// TODO(MVP): 采集 xray 版本与运行状态（§13）。
		}),
	}
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}

	// TODO(MVP): apply_node 落地流水线（§6）、xray gRPC 热操作、
	// add_user/remove_user 扇出处理、apply_result 上报、长期凭证落盘。
	for {
		var env shared.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}
		log.Printf("recv type=%s id=%s", env.Type, env.ID)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
