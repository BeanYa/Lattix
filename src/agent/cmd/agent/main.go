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
	"lattix/agent/internal/xray"
	"lattix/shared"
)

var version = "dev"

func main() {
	panel := flag.String("panel", "ws://127.0.0.1:8080/api/agent/ws", "Backend WS 地址")
	token := flag.String("token", "", "bootstrap token（§11）；state 文件已有长期凭证时忽略本参数")
	statePath := flag.String("state", "/etc/lattix-agent.state.json", "长期凭证状态文件路径")
	xrayBin := flag.String("xray-bin", "/usr/local/bin/xray", "xray 二进制路径")
	xrayConfig := flag.String("xray-config", "/usr/local/etc/xray/config.json", "xray 配置文件路径（agent 独占管理，§6）")
	xrayAPI := flag.String("xray-api", "127.0.0.1:10085", "xray gRPC API 地址")
	xrayRunner := flag.String("xray-runner", "systemd", "xray 服务控制方式：systemd | exec（dev 联调）")
	flag.Parse()

	mgr := xray.NewManager(*xrayBin, *xrayConfig, *xrayAPI, xray.NewRunner(*xrayRunner, *xrayBin, *xrayConfig))

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
		if err := run(*panel, tok, *statePath, mgr); err != nil {
			log.Printf("connection: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(panel, token, statePath string, mgr *xray.Manager) error {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 首连认证（§5）：携带 token、agent 版本、xray 版本与运行状态（§13）。
	xrayVer, xrayRunning := mgr.Version()
	helloID := fmt.Sprintf("hello-%d", time.Now().UnixNano())
	hello := shared.Envelope{
		ID:   helloID,
		Type: shared.TypeHello,
		Payload: mustJSON(shared.HelloPayload{
			Token:        token,
			AgentVersion: version,
			XrayVersion:  xrayVer,
			XrayRunning:  xrayRunning,
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
		handle(conn, mgr, env)
	}
}

// handle 按消息类型分发：apply 流水线与热操作（§6），结果经 apply_result 上报（§5）。
func handle(conn *websocket.Conn, mgr *xray.Manager, env shared.Envelope) {
	switch env.Type {
	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("apply_node id=%s node=%d users=%d", env.ID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs)
		replyApplyResult(conn, env.ID, resultOf(p.NodeID, realized, err))

	case shared.TypeRemoveNode:
		var p shared.RemoveNodePayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_node id=%s node=%d", env.ID, p.NodeID)
		err := mgr.RemoveNode(p.NodeID)
		replyApplyResult(conn, env.ID, resultOf(p.NodeID, nil, err))

	case shared.TypeAddUser:
		var p shared.AddUserPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("add_user id=%s uuid=%s", env.ID, p.UUID)
		err := mgr.AddUser(p.UUID)
		replyApplyResult(conn, env.ID, resultOf(0, nil, err)) // NodeID 0 = 非节点命令

	case shared.TypeRemoveUser:
		var p shared.RemoveUserPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_user id=%s uuid=%s", env.ID, p.UUID)
		err := mgr.RemoveUser(p.UUID)
		replyApplyResult(conn, env.ID, resultOf(0, nil, err))

	default:
		log.Printf("recv unknown type=%s id=%s", env.Type, env.ID)
	}
}

func parsePayload(env shared.Envelope, v any) bool {
	if err := json.Unmarshal(env.Payload, v); err != nil {
		log.Printf("bad %s payload: %v", env.Type, err)
		return false
	}
	return true
}

func resultOf(nodeID int64, realized *shared.RealizedConfig, err error) shared.ApplyResultPayload {
	if err != nil {
		return shared.ApplyResultPayload{NodeID: nodeID, OK: false, Error: err.Error()}
	}
	return shared.ApplyResultPayload{NodeID: nodeID, OK: true, RealizedConfig: realized}
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
