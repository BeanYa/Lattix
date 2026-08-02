// Lattix Agent：受控服务器上的独立二进制，systemd 托管（设计文档 §3）。
// 主动拨出至 Backend /api/agent/ws，一条 WS 长连接承担全部双向通信（§2）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"lattix/agent/internal/selfupdate"
	"lattix/agent/internal/servertest"
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
	serverTestWorker := flag.Bool("server-test-worker", false, "run the internal server test worker")
	flag.Parse()
	if *serverTestWorker {
		if err := servertest.WorkerMain(os.Stdin, os.Stdout); err != nil {
			log.Printf("server test worker: %v", err)
			os.Exit(1)
		}
		return
	}
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
	panelRuntime := newPanelStateTracker(st.PanelObservation)
	testManager, err := servertest.NewManager(filepath.Dir(*statePath), version)
	if err != nil {
		log.Fatalf("load server test task: %v", err)
	}
	commandQueue, err := newPersistentCommandQueue(
		filepath.Join(filepath.Dir(*statePath), "command-queue.json"),
		func(envelope shared.Envelope) {
			if envelope.Type == shared.TypeServerTestRun {
				_ = testManager.WaitIdle(context.Background())
			}
		},
	)
	if err != nil {
		log.Fatalf("load command queue: %v", err)
	}
	connectionPath := filepath.Join(filepath.Dir(*statePath), "connection.json")
	saveConnectionStatus := func(connected bool, connectionErr error) {
		message := ""
		if connectionErr != nil {
			message = connectionErr.Error()
			if len(message) > 512 {
				message = message[:512]
			}
		}
		if err := state.SaveConnectionStatus(connectionPath, state.ConnectionStatus{
			Connected: connected, Panel: *panel, ServerID: st.ServerID,
			AgentVersion: version, PID: os.Getpid(), ChangedAt: time.Now().UTC(), LastError: message,
		}); err != nil {
			log.Printf("save connection status: %v", err)
		}
	}
	saveConnectionStatus(false, nil)
	tok := selectInitialToken(st.Token, *token)
	if tok == "" {
		log.Fatal("-token is required for first connect")
	}
	failures := 0
	for {
		newTok, err := run(*panel, tok, *statePath, *settingsPath, connectionPath, mgr, &st, runtime, panelRuntime, testManager, commandQueue)
		saveConnectionStatus(false, err)
		if newTok != "" {
			tok = newTok // 内存兜底：state 落盘失败时仍能凭内存中的 token 重连（§5）
			failures = 0
		} else {
			failures++
		}
		if err != nil {
			if authenticationRejected(err) {
				st.AuthRejected = true
				_ = state.Save(*statePath, st)
				log.Printf("connection: 面板明确拒绝当前凭证（面板可能已重建或凭证已替换）；已停止自动重试，请使用新面板安装命令重新绑定后重启 Agent")
				waitForShutdown()
				return
			}
			settings, _, _, _ := runtime.snapshot()
			delay := reconnectDelay(settings, failures, websocketCloseCode(err))
			if errors.Is(err, errPanelUnavailable) {
				delay = unavailableRetryDelay()
			}
			if observed, _ := panelRuntime.snapshot(); observed.RetryPolicy.MinMS > 0 {
				delay = lifecycleRetryDelay(observed, delay)
			}
			log.Printf("connection: %v (retrying in %s)", err, delay.Round(time.Millisecond))
			time.Sleep(delay)
		}
	}
}

// waitForShutdown keeps the managed process alive after an explicit credential
// rejection. This prevents systemd Restart=always and the user-mode watchdog
// from turning a terminal authentication result into an infinite retry loop.
// A reinstall/restart supplies a new bootstrap credential and terminates this
// process through SIGTERM/SIGINT.
func waitForShutdown() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	signal.Stop(stop)
}

// safeConn 串行化 WS 写帧（业务回执与遥测协程并发写，gorilla 不允许并发写）。
type safeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// WS 应用层心跳（§2）：Agent 每 30s 发 Ping，Panel 回 Pong；任一侧
// wsReadTimeout 内无任何字节即判定连接死亡并由外层策略退避重连。
const wsReadTimeout = 90 * time.Second

// wsWriteTimeout 是 pong 应答等控制帧的单次写超时。
const wsWriteTimeout = 10 * time.Second

// maxWSMessageBytes bounds a single control-plane envelope in either
// direction. Normal configuration and telemetry messages are far smaller.
const maxWSMessageBytes = 1 << 20

func (s *safeConn) writeJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *safeConn) writeControl(messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteControl(messageType, data, time.Now().Add(wsWriteTimeout))
}

// run 建立连接并完成首连认证，返回本次换发/确认的 token 与断开原因。
// st 为已加载的落盘状态：session.open 换发后更新凭证字段并整体落盘（保留链 piece 记录）。
func run(panel, token, statePath, settingsPath, connectionPath string, mgr *xray.Manager, st *state.State, runtime *runtimeSettings, panelRuntime *panelStateTracker, testManager *servertest.Manager, commandQueue *persistentCommandQueue) (string, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, response, err := websocket.DefaultDialer.Dial(panel, header)
	if err != nil {
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if explicitAuthenticationRejection(response) {
			return "", errAuthenticationRejected
		}
		if panelTemporarilyUnavailable(response) {
			return "", errPanelUnavailable
		}
		return "", err
	}
	defer conn.Close()
	conn.SetReadLimit(maxWSMessageBytes)
	done := make(chan struct{})
	defer close(done)
	sc := &safeConn{conn: conn}

	// 独立的 liveness Ping 负责保活；latency Ping 只在 active 生命周期采样。
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))

	// Every authenticated WebSocket starts with an application session open.
	xrayVer, xrayRunning := mgr.Version()
	openID := shared.NewMessageID()
	open := shared.Envelope{
		Kind:      shared.KindRequest,
		Type:      shared.TypeSessionOpen,
		RequestID: openID,
		TraceID:   openID,
		Data: mustJSON(shared.SessionOpenPayload{
			ProtocolVersion: 1,
			AgentVersion:    version,
			XrayVersion:     xrayVer,
			XrayRunning:     xrayRunning,
			NICAddresses:    nonLoopbackAddrs(),
			LastLifecycle:   lifecycleVersion(st.PanelObservation),
		}),
	}
	if err := sc.writeJSON(open); err != nil {
		return "", fmt.Errorf("send session open: %w", err)
	}

	// The first response carries the complete lifecycle snapshot.
	var resp shared.Envelope
	if err := conn.ReadJSON(&resp); err != nil {
		return "", fmt.Errorf("read session open response: %w", err)
	}
	if err := resp.Validate(); err != nil {
		return "", fmt.Errorf("invalid session open response: %w", err)
	}
	if resp.Kind != shared.KindResponse || resp.Type != shared.TypeSessionOpen || resp.RequestID != openID {
		return "", fmt.Errorf("unexpected first frame: kind=%s type=%s request_id=%s",
			resp.Kind, resp.Type, resp.RequestID)
	}
	if resp.Code != shared.CodeOK {
		return "", fmt.Errorf("session open failed: %s: %s", resp.Code, resp.Message)
	}
	var opened shared.SessionOpenResult
	if err := json.Unmarshal(resp.Data, &opened); err != nil {
		return "", fmt.Errorf("bad session open result: %w", err)
	}
	newToken := token
	if opened.IssuedToken != "" {
		newToken = opened.IssuedToken
	}
	credential, err := shared.ParseCredential(newToken)
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
	if opened.PanelState.PanelInstanceID != credential.PanelInstanceID ||
		!panelRuntime.apply(opened.PanelState, true) {
		return "", fmt.Errorf("invalid panel lifecycle snapshot")
	}
	st.Token, st.ServerID = newToken, opened.ServerID
	st.PanelInstanceID, st.CredentialEpoch = credential.PanelInstanceID, credential.Epoch
	st.PanelObservation = &opened.PanelState
	st.AuthRejected = false
	saved := state.Save(statePath, *st) == nil
	if !saved {
		// 落盘失败由外层内存兜底（§5），但进程重启前未落盘将需凭证刷新。
		log.Printf("save state: failed (WARNING: in-memory token will be used for reconnects)")
	}
	log.Printf("authenticated as server %d session=%s kind=%s", opened.ServerID, opened.SessionID, opened.SessionKind)
	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		log.Printf("ensure telemetry features: %v (traffic stats may be unavailable)", err)
	}
	latency := newLatencyTracker()
	latency.setEnabled(opened.PanelState.State == shared.PanelStateActive)
	conn.SetPongHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return latency.handlePong(data)
	})
	if opened.CredentialExchangeID != "" && saved {
		if err := exchangeCredential(sc, conn, opened.CredentialExchangeID); err != nil {
			return newToken, err
		}
	}
	// A lightweight keepalive proves the new session in both directions. It is
	// deliberately excluded from latency samples.
	time.Sleep(time.Duration(rand.Intn(1001)) * time.Millisecond)
	if err := sendLiveness(sc); err != nil {
		return newToken, fmt.Errorf("initial liveness: %w", err)
	}
	if err := markSessionReady(sc, conn, opened.SessionID, panelRuntime, statePath, st, latency); err != nil {
		return newToken, err
	}
	testManager.Attach(func(envelope shared.Envelope) error { return sc.writeJSON(envelope) })
	defer testManager.Detach()
	queueAttachment := commandQueue.Attach(func(envelope shared.Envelope) {
		handle(sc, mgr, envelope, statePath, settingsPath, st, runtime, panelRuntime, latency, testManager, nil)
	})
	defer commandQueue.Detach(queueAttachment)
	if err := state.SaveConnectionStatus(connectionPath, state.ConnectionStatus{
		Connected: true, Panel: panel, ServerID: opened.ServerID,
		AgentVersion: version, PID: os.Getpid(), ChangedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("save connected status: %v", err)
	}
	sendSettingsSync(sc, runtime)

	// Liveness remains active in every connected lifecycle state.
	go func() {
		time.Sleep(time.Duration(rand.Int63n(int64(heartbeatInterval))))
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := sendLiveness(sc); err != nil {
					log.Printf("liveness: %v", err)
					_ = conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	go runLatencyProbes(done, conn, sc, latency, panelRuntime)

	// Telemetry does not wait for a latency sample.
	go func() {
		t := newTelemetry(mgr, latency.snapshot)
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
			return newToken, err
		}
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout)) // 任何消息到达即续期
		handle(sc, mgr, env, statePath, settingsPath, st, runtime, panelRuntime, latency, testManager, commandQueue)
	}
}

func lifecycleVersion(snapshot *shared.PanelLifecycleSnapshot) *shared.LifecycleVersion {
	if snapshot == nil || snapshot.Epoch == "" || snapshot.Revision == 0 {
		return nil
	}
	version := snapshot.Version()
	return &version
}

func exchangeCredential(sc *safeConn, conn *websocket.Conn, exchangeID string) error {
	id := shared.NewMessageID()
	request := shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeCredentialCommit,
		RequestID: id, TraceID: id,
		Data: mustJSON(shared.CredentialCommitPayload{ExchangeID: exchangeID}),
	}
	if err := sc.writeJSON(request); err != nil {
		return fmt.Errorf("send credential commit: %w", err)
	}
	response, err := readExpectedResponse(conn, shared.TypeCredentialCommit, id)
	if err != nil {
		return fmt.Errorf("credential commit: %w", err)
	}
	if response.Code != shared.CodeOK {
		return fmt.Errorf("credential commit failed: %s: %s", response.Code, response.Message)
	}
	return nil
}

func markSessionReady(sc *safeConn, conn *websocket.Conn, sessionID string,
	panelRuntime *panelStateTracker, statePath string, st *state.State, latency *latencyTracker) error {
	for attempts := 0; attempts < 3; attempts++ {
		observed, _ := panelRuntime.snapshot()
		id := shared.NewMessageID()
		request := shared.Envelope{
			Kind: shared.KindRequest, Type: shared.TypeSessionReady,
			RequestID: id, TraceID: id,
			Data: mustJSON(shared.SessionReadyPayload{
				SessionID: sessionID, Lifecycle: observed.Version(),
			}),
		}
		if err := sc.writeJSON(request); err != nil {
			return fmt.Errorf("send session ready: %w", err)
		}
		response, err := readExpectedResponse(conn, shared.TypeSessionReady, id)
		if err != nil {
			return fmt.Errorf("session ready: %w", err)
		}
		if response.Code == shared.CodeOK {
			return nil
		}
		if response.Code != shared.CodeConflict {
			return fmt.Errorf("session ready failed: %s: %s", response.Code, response.Message)
		}
		var current shared.PanelLifecycleSnapshot
		if err := json.Unmarshal(response.Data, &current); err != nil ||
			current.PanelInstanceID != st.PanelInstanceID || !panelRuntime.apply(current, false) {
			return fmt.Errorf("session ready returned invalid lifecycle")
		}
		latency.setEnabled(current.State == shared.PanelStateActive)
		st.PanelObservation = &current
		if err := state.Save(statePath, *st); err != nil {
			log.Printf("save panel lifecycle: %v", err)
		}
	}
	return fmt.Errorf("session ready lifecycle changed repeatedly")
}

func readExpectedResponse(conn *websocket.Conn, typ, requestID string) (shared.Envelope, error) {
	var response shared.Envelope
	if err := conn.ReadJSON(&response); err != nil {
		return response, err
	}
	if err := response.Validate(); err != nil {
		return response, err
	}
	if response.Kind != shared.KindResponse || response.Type != typ || response.RequestID != requestID {
		return response, fmt.Errorf("unexpected response: kind=%s type=%s request_id=%s",
			response.Kind, response.Type, response.RequestID)
	}
	return response, nil
}

func runLatencyProbes(done <-chan struct{}, conn *websocket.Conn, sc *safeConn,
	latency *latencyTracker, panelRuntime *panelStateTracker) {
	for {
		observed, changed := panelRuntime.snapshot()
		if observed.State != shared.PanelStateActive {
			select {
			case <-changed:
				continue
			case <-done:
				return
			}
		}

		resumeWindow := time.Duration(observed.LatencyResumeWindowMS) * time.Millisecond
		if resumeWindow < 5*time.Second {
			resumeWindow = 5 * time.Second
		}
		if resumeWindow > 5*time.Minute {
			resumeWindow = 5 * time.Minute
		}
		resume := time.NewTimer(time.Duration(rand.Int63n(int64(resumeWindow) + 1)))
		select {
		case <-resume.C:
		case <-changed:
			if !resume.Stop() {
				<-resume.C
			}
			continue
		case <-done:
			if !resume.Stop() {
				<-resume.C
			}
			return
		}
		if err := latency.sendProbe(sc); err != nil {
			log.Printf("latency probe: %v", err)
			_ = conn.Close()
			return
		}

		ticker := time.NewTicker(heartbeatInterval)
		selectLoop := true
		for selectLoop {
			select {
			case <-ticker.C:
				if err := latency.sendProbe(sc); err != nil {
					log.Printf("latency probe: %v", err)
					_ = conn.Close()
					ticker.Stop()
					return
				}
			case <-changed:
				ticker.Stop()
				selectLoop = false
			case <-done:
				ticker.Stop()
				return
			}
		}
	}
}

// handle 按消息类型分发：命令响应沿用请求的 type/request_id/trace_id。
func handle(sc *safeConn, mgr *xray.Manager, env shared.Envelope, statePath, settingsPath string, st *state.State, runtime *runtimeSettings, panelRuntime *panelStateTracker, latency *latencyTracker, testManager *servertest.Manager, commandQueue *persistentCommandQueue) {
	if env.Kind == shared.KindRequest && env.Type == shared.TypeLifecycleChanged {
		var payload shared.LifecycleChangedPayload
		if err := json.Unmarshal(env.Data, &payload); err != nil ||
			payload.PanelState.PanelInstanceID != st.PanelInstanceID ||
			!panelRuntime.apply(payload.PanelState, false) {
			replyCode(sc, env, shared.CodeInvalidArgument, "invalid lifecycle snapshot", nil)
			return
		}
		latency.setEnabled(payload.PanelState.State == shared.PanelStateActive)
		st.PanelObservation = &payload.PanelState
		if err := state.Save(statePath, *st); err != nil {
			log.Printf("save panel lifecycle: %v", err)
		}
		replyCode(sc, env, shared.CodeOK, "", struct{}{})
		return
	}
	if env.Kind == shared.KindResponse && env.Type == shared.TypeSettingsSync {
		handleSettingsSyncResponse(sc, env, settingsPath, runtime)
		return
	}
	if env.Kind == shared.KindResponse && testManager.HandleResponse(env) {
		return
	}
	if env.Kind == shared.KindEvent && env.Type == shared.TypeSettingsChanged {
		if err := sendSettingsSync(sc, runtime); err != nil {
			log.Printf("settings changed pull: %v", err)
		}
		return
	}
	if env.Kind == shared.KindRequest && commandQueue != nil {
		if err := commandQueue.Submit(env); err != nil {
			replyCode(sc, env, shared.CodeInternalError, err.Error(), struct{}{})
		}
		return
	}
	if env.Kind != shared.KindRequest {
		log.Printf("ignore non-request message kind=%s type=%s request_id=%s", env.Kind, env.Type, env.RequestID)
		return
	}
	switch env.Type {
	case shared.TypeServerTestRun:
		var p shared.ServerTestRunPayload
		if !parseData(sc, env, &p) {
			return
		}
		if err := p.Validate(); err != nil {
			replyCode(sc, env, shared.CodeInvalidArgument, err.Error(), struct{}{})
			return
		}
		if err := testManager.Accept(p); err != nil {
			replyCode(sc, env, shared.CodeConflict, err.Error(), struct{}{})
			return
		}
		log.Printf("server-test.run request_id=%s task=%s generation=%d categories=%d", env.RequestID, p.TaskID, p.Generation, len(p.Categories))
		replyCode(sc, env, shared.CodeAccepted, "", struct{}{})

	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("node.apply request_id=%s node=%d users=%d", env.RequestID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs, p.DestCandidates, p.PortCandidates)
		if err != nil {
			log.Printf("node.apply failed request_id=%s node=%d: %v", env.RequestID, p.NodeID, err)
		}
		replyResult(sc, env, resultOf(p.NodeID, realized), err)

	case shared.TypeApplyChainHop:
		var p shared.ApplyChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.apply request_id=%s chain=%d hop=%d kind=%s", env.RequestID, p.ChainID, p.HopID, p.Kind)
		realized, err := mgr.ApplyChainHop(p)
		if err != nil {
			log.Printf("chain-hop.apply failed request_id=%s chain=%d hop=%d kind=%s: %v",
				env.RequestID, p.ChainID, p.HopID, p.Kind, err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfHop(p.HopID, p.Kind, realized), err)

	case shared.TypeApplySharedEndpoint:
		var p shared.ApplySharedEndpointPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("shared-endpoint.apply request_id=%s endpoint=%d clients=%d routes=%d",
			env.RequestID, p.EndpointID, len(p.Clients), len(p.Routes))
		realized, err := mgr.ApplySharedEndpoint(p)
		if err != nil {
			log.Printf("shared-endpoint.apply failed request_id=%s endpoint=%d clients=%d routes=%d: %v",
				env.RequestID, p.EndpointID, len(p.Clients), len(p.Routes), err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfEndpoint(p.EndpointID, realized), err)

	case shared.TypeRemoveSharedEndpoint:
		var p shared.RemoveSharedEndpointPayload
		if !parseData(sc, env, &p) {
			return
		}
		err := mgr.RemoveSharedEndpoint(p.EndpointID)
		if err != nil {
			log.Printf("shared-endpoint.remove failed request_id=%s endpoint=%d: %v", env.RequestID, p.EndpointID, err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfEndpoint(p.EndpointID, nil), err)

	case shared.TypeCleanupXray:
		var p shared.CleanupXrayPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("xray.cleanup request_id=%s dry_run=%v expected_inbounds=%d expected_pieces=%d",
			env.RequestID, p.DryRun, len(p.ExpectedInboundTags), len(p.ExpectedPieces))
		result, err := mgr.CleanupXray(p)
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, shared.ApplyResultPayload{Cleanup: result}, err)

	case shared.TypeRemoveChainHop:
		var p shared.RemoveChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.remove request_id=%s hop=%d kind=%s", env.RequestID, p.HopID, p.Kind)
		err := mgr.RemoveChainHop(p.HopID, p.Kind)
		if err != nil {
			log.Printf("chain-hop.remove failed request_id=%s hop=%d kind=%s: %v", env.RequestID, p.HopID, p.Kind, err)
		}
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
		log.Printf("agent.upgrade request_id=%s version=%s force=%v", env.RequestID, p.Version, p.Force)
		upgraded, err := selfupdate.Apply(p.Version, p.ReleaseBase, version, githubRepo, p.Force)
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

func resultOfEndpoint(endpointID int64, realized *shared.RealizedConfig) shared.ApplyResultPayload {
	return shared.ApplyResultPayload{EndpointID: endpointID, RealizedConfig: realized}
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
// 随 session.open 上报作为面板公网地址候选（§9）。采集失败返回 nil，不阻断连接。
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
