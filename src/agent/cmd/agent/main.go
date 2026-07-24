// Lattix Agent：受控服务器上的独立二进制，systemd 托管（设计文档 §3）。
// 主动拨出至 Backend /api/agent/ws，一条 WS 长连接承担全部双向通信（§2）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sync"
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
	releaseBase := flag.String("xray-release-base", "", "xray release 下载基址（默认官方 GitHub，可指向镜像，§18）")
	telemetryInterval := flag.Duration("telemetry-interval", 60*time.Second, "遥测上报间隔（§13）")
	driftInterval := flag.Duration("drift-interval", 15*time.Second, "配置漂移检测间隔（§17）")
	flag.Parse()

	mgr := xray.NewManager(*xrayBin, *xrayConfig, *xrayAPI, xray.NewRunner(*xrayRunner, *xrayBin, *xrayConfig))
	if *releaseBase != "" {
		mgr.SetReleaseBase(*releaseBase) // 镜像/代理下载源（§18）
	}
	// 旧版本生成的 config.json 补齐遥测配置（stats/policy/StatsService，§13）。
	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		log.Printf("ensure telemetry features: %v (traffic stats may be unavailable)", err)
	}

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
	for {
		newTok, err := run(*panel, tok, *statePath, mgr, *telemetryInterval, *driftInterval)
		if newTok != "" {
			tok = newTok // 内存兜底：state 落盘失败时仍能凭内存中的 token 重连（§5）
		}
		if err != nil {
			log.Printf("connection: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
		}
	}
}

// safeConn 串行化 WS 写帧（业务回执与遥测协程并发写，gorilla 不允许并发写）。
type safeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *safeConn) writeJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

// run 建立连接并完成首连认证，返回本次换发/确认的 token 与断开原因。
func run(panel, token, statePath string, mgr *xray.Manager, telemetryInterval, driftInterval time.Duration) (string, error) {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	sc := &safeConn{conn: conn}

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
	if err := sc.writeJSON(hello); err != nil {
		return "", fmt.Errorf("send hello: %w", err)
	}

	// 第一帧必须是 hello 响应（panel 保证 HelloResult 先于任何补发命令到达）。
	var resp shared.Envelope
	if err := conn.ReadJSON(&resp); err != nil {
		return "", fmt.Errorf("read hello response: %w", err)
	}
	if resp.Type != shared.TypeHello || resp.ID != helloID {
		return "", fmt.Errorf("unexpected first frame: type=%s id=%s", resp.Type, resp.ID)
	}
	var hr shared.HelloResult
	if err := json.Unmarshal(resp.Payload, &hr); err != nil {
		return "", fmt.Errorf("bad hello result: %w", err)
	}
	if err := state.Save(statePath, state.State{Token: hr.Token, ServerID: hr.ServerID}); err != nil {
		// 落盘失败由外层内存兜底（§5），但进程重启前未落盘将需凭证刷新。
		log.Printf("save state: %v (WARNING: in-memory token will be used for reconnects)", err)
	}
	log.Printf("authenticated as server %d", hr.ServerID)

	// 遥测循环（§13）：立即上报一帧（基线），随后按间隔上报；写失败即退出（连接已断）。
	go func() {
		t := newTelemetry(mgr)
		send := func() bool {
			env := shared.Envelope{
				ID:      fmt.Sprintf("telemetry-%d", time.Now().UnixNano()),
				Type:    shared.TypeTelemetry,
				Payload: mustJSON(t.collect()),
			}
			if err := sc.writeJSON(env); err != nil {
				logTelemetryError(err)
				return false
			}
			return true
		}
		if !send() {
			return
		}
		ticker := time.NewTicker(telemetryInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !send() {
				return
			}
		}
	}()

	// 配置漂移检测（§17 reconcile）：仅在状态变化时上报。
	go func() {
		drifted := false
		check := func() bool {
			d, err := mgr.ConfigDrift()
			if err != nil {
				log.Printf("drift check: %v", err)
				return true
			}
			if d != drifted {
				drifted = d
				if d {
					log.Printf("config drift detected: config.json 被外部修改")
				}
				env := shared.Envelope{
					ID:      fmt.Sprintf("drift-%d", time.Now().UnixNano()),
					Type:    shared.TypeDriftReport,
					Payload: mustJSON(shared.DriftPayload{Drifted: d}),
				}
				if err := sc.writeJSON(env); err != nil {
					log.Printf("drift report: %v", err)
					return false
				}
			}
			return true
		}
		if !check() {
			return
		}
		ticker := time.NewTicker(driftInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !check() {
				return
			}
		}
	}()

	for {
		var env shared.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return hr.Token, err
		}
		handle(sc, mgr, env)
	}
}

// handle 按消息类型分发：apply 流水线与热操作（§6），结果经 apply_result 上报（§5）。
func handle(sc *safeConn, mgr *xray.Manager, env shared.Envelope) {
	switch env.Type {
	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("apply_node id=%s node=%d users=%d", env.ID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs, p.DestCandidates)
		replyApplyResult(sc, env.ID, resultOf(p.NodeID, realized, err))

	case shared.TypeRemoveNode:
		var p shared.RemoveNodePayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_node id=%s node=%d", env.ID, p.NodeID)
		err := mgr.RemoveNode(p.NodeID)
		replyApplyResult(sc, env.ID, resultOf(p.NodeID, nil, err))

	case shared.TypeAddUser:
		var p shared.AddUserPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("add_user id=%s uuid=%s", env.ID, p.UUID)
		err := mgr.AddUser(p.UUID, p.Nodes)
		replyApplyResult(sc, env.ID, resultOf(0, nil, err)) // NodeID 0 = 非节点命令

	case shared.TypeRemoveUser:
		var p shared.RemoveUserPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_user id=%s uuid=%s", env.ID, p.UUID)
		err := mgr.RemoveUser(p.UUID, p.Nodes)
		replyApplyResult(sc, env.ID, resultOf(0, nil, err))

	case shared.TypeUpgradeXray:
		var p shared.UpgradeXrayPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("upgrade_xray id=%s version=%s", env.ID, p.Version)
		err := mgr.UpgradeXray(p.Version)
		replyApplyResult(sc, env.ID, resultOf(0, nil, err))

	case shared.TypeUninstall:
		// 先回执再自毁：panel 删除服务器时下发（§10）。
		var p shared.UninstallPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("uninstall id=%s purge_xray=%v", env.ID, p.PurgeXray)
		replyApplyResult(sc, env.ID, resultOf(0, nil, nil))
		scheduleUninstall(p.PurgeXray)

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
func replyApplyResult(sc *safeConn, reqID string, p shared.ApplyResultPayload) {
	env := shared.Envelope{ID: reqID, Type: shared.TypeApplyResult, Payload: mustJSON(p)}
	if err := sc.writeJSON(env); err != nil {
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
