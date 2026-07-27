// Lattix Agent：受控服务器上的独立二进制，systemd 托管（设计文档 §3）。
// 主动拨出至 Backend /api/agent/ws，一条 WS 长连接承担全部双向通信（§2）。
package main

import (
	"encoding/json"
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
	statePath := flag.String("state", "/opt/lattix-agent/data/state.json", "长期凭证状态文件路径")
	settingsPath := flag.String("settings", "/opt/lattix-agent/data/settings.json", "面板同步设置文件路径")
	xrayBin := flag.String("xray-bin", "/opt/lattix-agent/bin/xray", "xray 二进制路径")
	xrayConfig := flag.String("xray-config", "/opt/lattix-agent/config/xray.json", "xray 配置文件路径（agent 独占管理，§6）")
	xrayAPI := flag.String("xray-api", "127.0.0.1:10085", "xray gRPC API 地址")
	xrayRunner := flag.String("xray-runner", "systemd", "xray 服务控制方式：systemd | exec（dev 联调）")
	releaseBase := flag.String("xray-release-base", "", "xray release 下载基址（默认官方 GitHub，可指向镜像，§18）")
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
	st, err := state.Load(*statePath)
	if err != nil {
		log.Printf("load state: %v (ignored)", err)
	}
	mgr.SetChainPieces(st.ChainPieces) // 链 piece 落盘记录：重启重建 config.json 的依据（§21.1）
	document, err := state.LoadSettings(*settingsPath)
	if err != nil {
		log.Printf("load settings: %v (using defaults)", err)
	}
	runtime := newRuntimeSettings(document)
	tok := selectInitialToken(st.Token, *token)
	if tok == "" {
		log.Fatal("-token is required for first connect")
	}
	failures := 0
	for {
		newTok, err := run(*panel, tok, *statePath, *settingsPath, mgr, &st, runtime)
		if newTok != "" {
			tok = newTok // 内存兜底：state 落盘失败时仍能凭内存中的 token 重连（§5）
			failures = 0
		} else {
			failures++
		}
		if err != nil {
			settings, _, _, _ := runtime.snapshot()
			delay := reconnectDelay(settings, failures, websocketCloseCode(err))
			log.Printf("connection: %v (retrying in %s)", err, delay.Round(time.Millisecond))
			time.Sleep(delay)
		}
	}
}

// safeConn 串行化 WS 写帧（业务回执与遥测协程并发写，gorilla 不允许并发写）。
type safeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// WS 应用层心跳（§2）：panel 每 30s 发 ping，任一侧 wsReadTimeout 内无任何字节
// （含 ping/pong 控制帧）即判定连接死亡，读循环报错退出后由外层策略退避重连。
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
func run(panel, token, statePath, settingsPath string, mgr *xray.Manager, st *state.State, runtime *runtimeSettings) (string, error) {
	conn, _, err := websocket.DefaultDialer.Dial(panel, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
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
	helloID := shared.NewMessageID()
	hello := shared.Envelope{
		Kind:      shared.KindRequest,
		Type:      shared.TypeHello,
		RequestID: helloID,
		TraceID:   helloID,
		Data: mustJSON(shared.HelloPayload{
			Token:        token,
			AgentVersion: version,
			XrayVersion:  xrayVer,
			XrayRunning:  xrayRunning,
			Reconnect:    st.ServerID != 0 && st.Token == token,
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
	if resp.Kind != shared.KindResponse || resp.Type != shared.TypeHello || resp.RequestID != helloID {
		return "", fmt.Errorf("unexpected first frame: kind=%s type=%s request_id=%s",
			resp.Kind, resp.Type, resp.RequestID)
	}
	if resp.Code != shared.CodeOK {
		return "", fmt.Errorf("hello failed: %s: %s", resp.Code, resp.Message)
	}
	var hr shared.HelloResult
	if err := json.Unmarshal(resp.Data, &hr); err != nil {
		return "", fmt.Errorf("bad hello result: %w", err)
	}
	credential, err := shared.ParseCredential(hr.Token)
	if err != nil {
		return "", fmt.Errorf("bad issued credential: %w", err)
	}
	crossPanelRebind := st.PanelInstanceID != "" && st.PanelInstanceID != credential.PanelInstanceID
	if crossPanelRebind {
		if err := mgr.ResetForPanelRebind(); err != nil {
			return "", fmt.Errorf("reset previous panel configuration: %w", err)
		}
		*st = state.State{}
		runtime.resetForPanelRebind()
	}
	st.Token, st.ServerID = hr.Token, hr.ServerID
	st.PanelInstanceID, st.CredentialEpoch = credential.PanelInstanceID, credential.Epoch
	if err := state.Save(statePath, *st); err != nil {
		// 落盘失败由外层内存兜底（§5），但进程重启前未落盘将需凭证刷新。
		log.Printf("save state: %v (WARNING: in-memory token will be used for reconnects)", err)
	}
	log.Printf("authenticated as server %d", hr.ServerID)
	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		log.Printf("ensure telemetry features: %v (traffic stats may be unavailable)", err)
	}
	sendSettingsSync(sc, runtime)

	// 遥测循环（§13）：立即上报一帧（基线），随后按间隔上报；写失败即退出（连接已断）。
	go func() {
		t := newTelemetry(mgr)
		send := func() bool {
			messageID := shared.NewMessageID()
			env := shared.Envelope{
				Kind:      shared.KindEvent,
				Type:      shared.TypeTelemetry,
				RequestID: messageID,
				TraceID:   messageID,
				Data:      mustJSON(t.collect()),
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
		for {
			if !runtime.waitInterval(done, func(settings shared.AgentSettings) time.Duration {
				return time.Duration(settings.Telemetry.IntervalSeconds) * time.Second
			}) {
				return
			}
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
				messageID := shared.NewMessageID()
				env := shared.Envelope{
					Kind:      shared.KindEvent,
					Type:      shared.TypeDriftReport,
					RequestID: messageID,
					TraceID:   messageID,
					Data:      mustJSON(shared.DriftPayload{Drifted: d}),
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
		for {
			if !runtime.waitInterval(done, func(settings shared.AgentSettings) time.Duration {
				return time.Duration(settings.DriftDetection.IntervalSeconds) * time.Second
			}) {
				return
			}
			if !check() {
				return
			}
		}
	}()

	go func() {
		for {
			// Periodic pull is the recovery path for a lost changed hint.
			timer := time.NewTimer(time.Duration(48+time.Now().UnixNano()%25) * time.Second)
			select {
			case <-timer.C:
			case <-done:
				if !timer.Stop() {
					<-timer.C
				}
				return
			}
			if err := sendSettingsSync(sc, runtime); err != nil {
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
		handle(sc, mgr, env, statePath, settingsPath, st, runtime)
	}
}

// handle 按消息类型分发：命令响应沿用请求的 type/request_id/trace_id。
func handle(sc *safeConn, mgr *xray.Manager, env shared.Envelope, statePath, settingsPath string, st *state.State, runtime *runtimeSettings) {
	if env.Kind == shared.KindResponse && env.Type == shared.TypeSettingsSync {
		handleSettingsSyncResponse(sc, env, settingsPath, runtime)
		return
	}
	if env.Kind == shared.KindEvent && env.Type == shared.TypeSettingsChanged {
		if err := sendSettingsSync(sc, runtime); err != nil {
			log.Printf("settings changed pull: %v", err)
		}
		return
	}
	if env.Kind != shared.KindRequest {
		log.Printf("ignore non-request message kind=%s type=%s request_id=%s", env.Kind, env.Type, env.RequestID)
		return
	}
	switch env.Type {
	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("node.apply request_id=%s node=%d users=%d", env.RequestID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs, p.DestCandidates, p.PortCandidates)
		replyResult(sc, env, resultOf(p.NodeID, realized), err)

	case shared.TypeApplyChainHop:
		var p shared.ApplyChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.apply request_id=%s chain=%d hop=%d kind=%s", env.RequestID, p.ChainID, p.HopID, p.Kind)
		realized, err := mgr.ApplyChainHop(p)
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfHop(p.HopID, p.Kind, realized), err)

	case shared.TypeRemoveChainHop:
		var p shared.RemoveChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.remove request_id=%s hop=%d kind=%s", env.RequestID, p.HopID, p.Kind)
		err := mgr.RemoveChainHop(p.HopID, p.Kind)
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfHop(p.HopID, p.Kind, nil), err)

	case shared.TypeRemoveNode:
		var p shared.RemoveNodePayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("node.remove request_id=%s node=%d", env.RequestID, p.NodeID)
		err := mgr.RemoveNode(p.NodeID)
		replyResult(sc, env, resultOf(p.NodeID, nil), err)

	case shared.TypeAddUser:
		var p shared.AddUserPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("user.add request_id=%s uuid=%s", env.RequestID, p.UUID)
		if len(p.Nodes) == 0 {
			replyCode(sc, env, shared.CodeInvalidArgument, "nodes field required", resultOf(0, nil))
			return
		}
		err := mgr.AddUser(p.UUID, p.Nodes)
		replyResult(sc, env, resultOf(0, nil), err) // NodeID 0 = 非节点命令

	case shared.TypeRemoveUser:
		var p shared.RemoveUserPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("user.remove request_id=%s uuid=%s", env.RequestID, p.UUID)
		if len(p.Nodes) == 0 {
			replyCode(sc, env, shared.CodeInvalidArgument, "nodes field required", resultOf(0, nil))
			return
		}
		err := mgr.RemoveUser(p.UUID, p.Nodes)
		replyResult(sc, env, resultOf(0, nil), err)

	case shared.TypeUpgradeXray:
		var p shared.UpgradeXrayPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("xray.upgrade request_id=%s version=%s", env.RequestID, p.Version)
		err := mgr.UpgradeXray(p.Version)
		replyResult(sc, env, resultOf(0, nil), err)

	case shared.TypeUpgradeAgent:
		// 自升级（§18）：先完成下载校验与原子替换，回执后退出，由 systemd 拉起新二进制。
		var p shared.UpgradeAgentPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("agent.upgrade request_id=%s version=%s", env.RequestID, p.Version)
		upgraded, err := selfupdate.Apply(p.Version, p.ReleaseBase, version, githubRepo)
		replyResult(sc, env, resultOf(0, nil), err)
		if err != nil || !upgraded {
			return // 失败或已是目标版本（幂等），无需重启
		}
		log.Printf("upgrade_agent: 二进制已替换，退出等待 systemd 拉起")
		go exitAfter(time.Second)

	case shared.TypeUninstall:
		// 先回执再自毁：panel 删除服务器时下发（§10）。
		var p shared.UninstallPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("agent.uninstall request_id=%s purge_xray=%v", env.RequestID, p.PurgeXray)
		replyResult(sc, env, resultOf(0, nil), nil)
		scheduleUninstall(p.PurgeXray, mgr)

	default:
		log.Printf("recv unknown type=%s request_id=%s", env.Type, env.RequestID)
		replyCode(sc, env, shared.CodeUnsupportedAction, "unsupported action", nil)
	}
}

func sendSettingsSync(sc *safeConn, runtime *runtimeSettings) error {
	_, panelID, revision, applyError := runtime.snapshot()
	id := shared.NewMessageID()
	return sc.writeJSON(shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeSettingsSync,
		RequestID: id, TraceID: id,
		Data: mustJSON(shared.AgentSettingsSyncPayload{
			PanelInstanceID: panelID,
			AppliedRevision: revision,
			LastApplyError:  applyError,
		}),
	})
}

func handleSettingsSyncResponse(sc *safeConn, env shared.Envelope, settingsPath string, runtime *runtimeSettings) {
	if env.Code != shared.CodeOK {
		runtime.fail(env.Message)
		log.Printf("settings sync failed: %s: %s", env.Code, env.Message)
		return
	}
	var result shared.AgentSettingsSyncResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		runtime.fail("invalid settings sync response")
		return
	}
	if !result.Changed {
		return
	}
	if result.Settings == nil {
		runtime.fail("settings sync response omitted settings")
		return
	}
	if err := result.Settings.Validate(); err != nil {
		runtime.fail(err.Error())
		log.Printf("reject panel settings: %v", err)
		return
	}
	if err := state.SaveSettings(settingsPath, *result.Settings); err != nil {
		runtime.fail(err.Error())
		log.Printf("save panel settings: %v", err)
		return
	}
	runtime.apply(*result.Settings)
	log.Printf("applied agent settings revision=%d", result.Settings.Agent.Revision)
	if err := sendSettingsSync(sc, runtime); err != nil {
		log.Printf("confirm agent settings revision: %v", err)
	}
}

func parseData(sc *safeConn, env shared.Envelope, v any) bool {
	if err := json.Unmarshal(env.Data, v); err != nil {
		log.Printf("bad %s data: %v", env.Type, err)
		replyCode(sc, env, shared.CodeInvalidArgument, "invalid message data", nil)
		return false
	}
	return true
}

func resultOf(nodeID int64, realized *shared.RealizedConfig) shared.ApplyResultPayload {
	return shared.ApplyResultPayload{NodeID: nodeID, RealizedConfig: realized}
}

// resultOfHop 构造链跳配置件回执（§21.1）：hop_id/kind 定位 piece，NodeID 恒 0。
func resultOfHop(hopID int64, kind string, realized *shared.RealizedConfig) shared.ApplyResultPayload {
	return shared.ApplyResultPayload{HopID: hopID, Kind: kind, RealizedConfig: realized}
}

// persistChainPieces 把链 piece 记录随 state 落盘（重启重建 config.json 的依据，§21.1）。
func persistChainPieces(statePath string, st *state.State, mgr *xray.Manager) {
	st.ChainPieces = mgr.ChainPieces()
	if err := state.Save(statePath, *st); err != nil {
		log.Printf("save state (chain pieces): %v", err)
	}
}

func replyResult(sc *safeConn, request shared.Envelope, data any, err error) {
	if err != nil {
		replyCode(sc, request, shared.CodeInternalError, err.Error(), data)
		return
	}
	replyCode(sc, request, shared.CodeOK, "", data)
}

func replyCode(sc *safeConn, request shared.Envelope, code, message string, data any) {
	env := shared.Envelope{
		Kind:      shared.KindResponse,
		Type:      request.Type,
		RequestID: request.RequestID,
		TraceID:   request.TraceID,
		Code:      code,
		Message:   message,
		Data:      mustJSON(data),
	}
	if err := sc.writeJSON(env); err != nil {
		log.Printf("reply %s: %v", request.Type, err)
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
