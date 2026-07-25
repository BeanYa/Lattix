// Lattix Agent：受控服务器上的独立二进制，systemd 托管（设计文档 §3）。
// 主动拨出至 Backend /api/agent/ws，一条 WS 长连接承担全部双向通信（§2）。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lattix/agent/internal/selfupdate"
	"lattix/agent/internal/state"
	"lattix/agent/internal/xray"
	"lattix/shared"
)

var (
	version    = "dev"
	githubRepo = "BeanYa/Lattix"
)

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
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

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
	mgr.SetChainPieces(st.ChainPieces) // 链 piece 落盘记录：重启重建 config.json 的依据（§21.1）
	tok := st.Token
	if tok == "" {
		tok = *token // 首连使用 bootstrap token（§11）
	}
	if tok == "" {
		log.Fatal("-token is required for first connect")
	}
	for {
		newTok, err := run(*panel, tok, *statePath, mgr, &st, *telemetryInterval, *driftInterval)
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

// WS 应用层心跳（§2）：panel 每 30s 发 ping，任一侧 wsReadTimeout 内无任何字节
// （含 ping/pong 控制帧）即判定连接死亡，读循环报错退出后由外层 5s 退避重连。
const wsReadTimeout = 90 * time.Second

// wsWriteTimeout 是 pong 应答等控制帧的单次写超时。
const wsWriteTimeout = 10 * time.Second

func (s *safeConn) writeJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

// run 建立连接并完成首连认证，返回本次换发/确认的 token 与断开原因。
// st 为已加载的落盘状态：hello 换发后更新凭证字段并整体落盘（保留链 piece 记录）。
func run(panel, token, statePath string, mgr *xray.Manager, st *state.State, telemetryInterval, driftInterval time.Duration) (string, error) {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	sc := &safeConn{conn: conn}

	// 应用层心跳（§2）：读超时 90s，任何消息到达即续期；收到 ping 回 pong 并续期
	// （gorilla 默认 handler 只回 pong 不续期，故显式设置；WriteControl 并发安全）。
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPingHandler(func(data string) error {
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(wsWriteTimeout))
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		}
		return err
	})

	// 首连认证（§5）：携带 token、agent 版本、xray 版本与运行状态（§13），
	// 以及本机网卡的非回环地址（§9 公网地址候选，面板编辑地址时下拉可选）。
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
			NICAddresses: nonLoopbackAddrs(),
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
	st.Token, st.ServerID = hr.Token, hr.ServerID
	if err := state.Save(statePath, *st); err != nil {
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
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout)) // 任何消息到达即续期
		handle(sc, mgr, env, statePath, st)
	}
}

// handle 按消息类型分发：apply 流水线与热操作（§6），结果经 apply_result 上报（§5）。
func handle(sc *safeConn, mgr *xray.Manager, env shared.Envelope, statePath string, st *state.State) {
	switch env.Type {
	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("apply_node id=%s node=%d users=%d", env.ID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs, p.DestCandidates, p.PortCandidates)
		replyApplyResult(sc, env.ID, resultOf(p.NodeID, realized, err))

	case shared.TypeApplyChainHop:
		var p shared.ApplyChainHopPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("apply_chain_hop id=%s chain=%d hop=%d kind=%s", env.ID, p.ChainID, p.HopID, p.Kind)
		realized, err := mgr.ApplyChainHop(p)
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyApplyResult(sc, env.ID, resultOfHop(p.HopID, p.Kind, realized, err))

	case shared.TypeRemoveChainHop:
		var p shared.RemoveChainHopPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_chain_hop id=%s hop=%d kind=%s", env.ID, p.HopID, p.Kind)
		err := mgr.RemoveChainHop(p.HopID, p.Kind)
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyApplyResult(sc, env.ID, resultOfHop(p.HopID, p.Kind, nil, err))

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
		if len(p.Nodes) == 0 {
			// §16 差量扇出后载荷必带目标节点；缺省 Nodes 的旧载荷不再兼容，显式回执错误。
			replyApplyResult(sc, env.ID, resultOf(0, nil, errors.New("nodes field required")))
			return
		}
		err := mgr.AddUser(p.UUID, p.Nodes)
		replyApplyResult(sc, env.ID, resultOf(0, nil, err)) // NodeID 0 = 非节点命令

	case shared.TypeRemoveUser:
		var p shared.RemoveUserPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("remove_user id=%s uuid=%s", env.ID, p.UUID)
		if len(p.Nodes) == 0 {
			// 同 add_user：缺省 Nodes 的旧载荷不再兼容，显式回执错误。
			replyApplyResult(sc, env.ID, resultOf(0, nil, errors.New("nodes field required")))
			return
		}
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

	case shared.TypeUpgradeAgent:
		// 自升级（§18）：先完成下载校验与原子替换，回执后退出，由 systemd 拉起新二进制。
		var p shared.UpgradeAgentPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("upgrade_agent id=%s version=%s", env.ID, p.Version)
		upgraded, err := selfupdate.Apply(p.Version, p.ReleaseBase, version, githubRepo)
		replyApplyResult(sc, env.ID, resultOf(0, nil, err))
		if err != nil || !upgraded {
			return // 失败或已是目标版本（幂等），无需重启
		}
		log.Printf("upgrade_agent: 二进制已替换，退出等待 systemd 拉起")
		go exitAfter(time.Second)

	case shared.TypeUninstall:
		// 先回执再自毁：panel 删除服务器时下发（§10）。
		var p shared.UninstallPayload
		if !parsePayload(env, &p) {
			return
		}
		log.Printf("uninstall id=%s purge_xray=%v", env.ID, p.PurgeXray)
		replyApplyResult(sc, env.ID, resultOf(0, nil, nil))
		scheduleUninstall(p.PurgeXray, mgr)

	default:
		// 协议演化规则：不认识的命令显式回执失败（面板据此终态不重试），而非静默丢弃。
		log.Printf("recv unknown type=%s id=%s", env.Type, env.ID)
		replyApplyResult(sc, env.ID, resultOf(0, nil,
			fmt.Errorf("%s: %s", shared.ErrUnsupportedPrefix, env.Type)))
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

// resultOfHop 构造链跳配置件回执（§21.1）：hop_id/kind 定位 piece，NodeID 恒 0。
func resultOfHop(hopID int64, kind string, realized *shared.RealizedConfig, err error) shared.ApplyResultPayload {
	if err != nil {
		return shared.ApplyResultPayload{HopID: hopID, Kind: kind, OK: false, Error: err.Error()}
	}
	return shared.ApplyResultPayload{HopID: hopID, Kind: kind, OK: true, RealizedConfig: realized}
}

// persistChainPieces 把链 piece 记录随 state 落盘（重启重建 config.json 的依据，§21.1）。
func persistChainPieces(statePath string, st *state.State, mgr *xray.Manager) {
	st.ChainPieces = mgr.ChainPieces()
	if err := state.Save(statePath, *st); err != nil {
		log.Printf("save state (chain pieces): %v", err)
	}
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

// nonLoopbackAddrs 枚举本机网卡的非回环 IP（v4/v6，跳过 down 的接口），
// 随 hello 上报作为面板公网地址候选（§9）。采集失败返回 nil，不阻断连接。
func nonLoopbackAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}
