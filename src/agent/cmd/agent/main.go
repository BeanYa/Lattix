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

	"lattix/agent/internal/state"
	"lattix/shared"
)

var version = "dev"

func main() {
	panel := flag.String("panel", "ws://127.0.0.1:8080/api/agent/ws", "Backend WS 地址")
	token := flag.String("token", "", "bootstrap token（§11）；state 文件已有长期凭证时忽略本参数")
	statePath := flag.String("state", "/etc/lattix-agent.state.json", "长期凭证状态文件路径")
	flag.Parse()

	for {
		st, err := state.Load(*statePath)
		if err != nil {
			log.Printf("load state: %v (ignored)", err)
		}
		tok := st.Token
		if tok == "" {
			tok = *token // 首连使用 bootstrap token（§11）
		}
		if tok == "" {
			log.Fatal("-token is required for first connect")
		}
		if err := run(*panel, tok, *statePath); err != nil {
			log.Printf("connection: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(panel, token, statePath string) error {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 首连认证（§5）：携带 token、agent 版本、xray 版本与运行状态。
	helloID := fmt.Sprintf("hello-%d", time.Now().UnixNano())
	hello := shared.Envelope{
		ID:   helloID,
		Type: shared.TypeHello,
		Payload: mustJSON(shared.HelloPayload{
			Token:        token,
			AgentVersion: version,
			// TODO(阶段 2): 采集 xray 版本与运行状态（§13）。
		}),
	}
	if err := conn.WriteJSON(hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	// 第一帧必须是 hello 响应（panel 保证 HelloResult 先于任何补发命令到达）。
	var resp shared.Envelope
	if err := conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("read hello response: %w", err)
	}
	if resp.Type != shared.TypeHello || resp.ID != helloID {
		return fmt.Errorf("unexpected first frame: type=%s id=%s", resp.Type, resp.ID)
	}
	var hr shared.HelloResult
	if err := json.Unmarshal(resp.Payload, &hr); err != nil {
		return fmt.Errorf("bad hello result: %w", err)
	}
	if err := state.Save(statePath, state.State{Token: hr.Token, ServerID: hr.ServerID}); err != nil {
		// 保存失败本次连接仍可用，但重启后旧凭证已失效（panel 每次 hello 均换发）。
		log.Printf("save state: %v (WARNING: next reconnect will fail auth)", err)
	}
	log.Printf("authenticated as server %d", hr.ServerID)

	for {
		var env shared.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}
		handle(conn, env)
	}
}

// handle 按消息类型分发。apply 流水线与热操作在阶段 2 实现（§6）。
func handle(conn *websocket.Conn, env shared.Envelope) {
	switch env.Type {
	case shared.TypeApplyNode, shared.TypeRemoveNode:
		// 占位：回 failed 使面板节点状态机可推进（§6），阶段 2 替换为真实流水线。
		var p struct {
			NodeID int64 `json:"node_id"`
		}
		json.Unmarshal(env.Payload, &p)
		log.Printf("recv %s id=%s node=%d (not implemented yet)", env.Type, env.ID, p.NodeID)
		replyApplyResult(conn, env.ID, shared.ApplyResultPayload{
			NodeID: p.NodeID,
			OK:     false,
			Error:  "apply pipeline not implemented yet",
		})
	case shared.TypeAddUser, shared.TypeRemoveUser:
		// TODO(阶段 2)：xray gRPC 热操作（AlterInbound Add/RemoveUserOperation）。
		log.Printf("recv %s id=%s (not implemented yet)", env.Type, env.ID)
	default:
		log.Printf("recv unknown type=%s id=%s", env.Type, env.ID)
	}
}

// replyApplyResult 上报执行结果：与请求同 id 即响应帧（§5）。
func replyApplyResult(conn *websocket.Conn, reqID string, p shared.ApplyResultPayload) {
	env := shared.Envelope{ID: reqID, Type: shared.TypeApplyResult, Payload: mustJSON(p)}
	if err := conn.WriteJSON(env); err != nil {
		log.Printf("reply apply_result: %v", err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
