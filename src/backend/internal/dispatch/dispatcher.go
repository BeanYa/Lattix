// Package dispatch 负责命令生命周期（§2、§4）：入队 commands 表、经 Requester 投递、
// 按响应回写命令与节点状态机；并实现 hello 认证（bootstrap token 换发长期凭证，§11）。
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// Dispatcher 串联 store 与 Requester。
type Dispatcher struct {
	st  *store.Store
	req ws.AgentRequester

	// Alerter 事件告警（§19）：nil = 关闭；仅状态跃迁时调用，发送方自行防抖动。
	Alerter      *alert.Notifier
	OperationLog *logging.OperationStore
	RequestLog   *logging.RequestLog

	// DestCandidates 是面板内置的 dest 白名单（§6 预检 fallback）：链编排下发
	// apply_node/portal 时携带（panel 初始化时注入，与单机节点同一份）。
	DestCandidates []string
	PanelVersion   string
	PanelPublicURL string

	mu      sync.Mutex
	flushMu map[int64]*sync.Mutex // 每服务器一把，避免并发 Flush 重复投递
}

// New 创建 Dispatcher。
func New(st *store.Store, req ws.AgentRequester) *Dispatcher {
	return &Dispatcher{st: st, req: req, flushMu: make(map[int64]*sync.Mutex)}
}

// Enqueue 将命令写入 commands 表（queued）并尽力立即投递；离线则滞留，待重连补发（§2）。
func (d *Dispatcher) Enqueue(ctx context.Context, serverID int64, typ string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}
	requestID := shared.NewMessageID()
	traceID := logging.TraceID(ctx)
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	id, err := d.st.EnqueueCommand(ctx, requestID, traceID, serverID, typ, raw)
	if err != nil {
		return 0, err
	}
	d.Flush(ctx, serverID)
	return id, nil
}

// maxCommandAttempts 是命令投递次数上限；超过即死信（failed，§2）。
const maxCommandAttempts = 10

// Flush 投递该服务器全部待发命令；agent 离线时停止并滞留（§2 离线排队）。
// attempts 超过 maxCommandAttempts 的命令标记 failed（死信），不再重发。
func (d *Dispatcher) Flush(ctx context.Context, serverID int64) {
	lock := d.serverLock(serverID)
	lock.Lock()
	defer lock.Unlock()

	cmds, err := d.st.QueuedCommands(ctx, serverID)
	if err != nil {
		log.Printf("dispatch: flush server %d: %v", serverID, err)
		return
	}
	for _, c := range cmds {
		if c.Attempts >= maxCommandAttempts {
			log.Printf("dispatch: command %d dead-lettered after %d attempts", c.ID, c.Attempts)
			if err := d.st.DeadLetterCommand(ctx, c.ID); err != nil {
				log.Printf("dispatch: dead-letter command %d: %v", c.ID, err)
			}
			d.recordOperation(logging.OperationEvent{
				Severity: logging.SeverityError, Category: logging.CategoryCommand,
				Action: "command.dead_lettered", ServerID: &serverID,
				Detail: map[string]any{"command_id": c.ID, "type": c.Type, "attempts": c.Attempts},
			})
			// apply_node 死信：节点不能永远卡 applying，置 failed 供管理员重试（§6）。
			if c.Type == shared.TypeApplyNode {
				var p shared.ApplyNodePayload
				if err := json.Unmarshal(c.Data, &p); err == nil && p.NodeID != 0 {
					reason := fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts)
					if err := d.st.SetNodeFailed(ctx, p.NodeID, reason); err != nil {
						log.Printf("dispatch: dead-letter node %d failed: %v", p.NodeID, err)
					}
					d.alertNodeFailed(serverID, p.NodeID, reason)
					d.failChainByNode(ctx, p.NodeID, reason) // 链出口业务死信 → 链 failed 定位到跳（§21）
				}
			}
			// apply_chain_hop 死信：跳置 failed，链 failed 定位到跳（§21）。
			if c.Type == shared.TypeApplyChainHop {
				var p shared.ApplyChainHopPayload
				if err := json.Unmarshal(c.Data, &p); err == nil && p.HopID != 0 {
					reason := fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts)
					if hop, err := d.st.ChainHopByID(ctx, p.HopID); err == nil {
						d.failHop(ctx, hop, reason)
					}
				}
			}
			continue
		}
		env := shared.Envelope{
			Kind:      shared.KindRequest,
			Type:      c.Type,
			RequestID: c.RequestID,
			TraceID:   c.TraceID,
			Data:      c.Data,
		}
		if err := d.req.Send(ctx, serverID, env); err != nil {
			return // 离线：剩余命令滞留 queued，待重连补发
		}
		if err := d.st.MarkCommandSent(ctx, c.ID); err != nil {
			log.Printf("dispatch: mark sent %d: %v", c.ID, err)
		}
	}
}

// AuthenticateHello 实现 ws.Authenticator：按 token 查找服务器。
// 仅 bootstrap 状态（last_seen_at 为空，§5/§11）时换发长期凭证；长期 token 稳定不轮换。
// 管理员已指定地址（servers.address 非空）时不被 RemoteAddr 覆盖（§4/§9）。
func (d *Dispatcher) AuthenticateHello(ctx context.Context, p shared.HelloPayload, remoteAddr string) (int64, shared.HelloResult, error) {
	srv, err := d.st.ServerByToken(ctx, p.Token)
	if err != nil {
		return 0, shared.HelloResult{}, fmt.Errorf("unknown token")
	}
	token := srv.Token
	if srv.LastSeenAt == nil {
		// bootstrap 状态：换发长期凭证（bootstrap 失效）。
		credential, parseErr := shared.ParseCredential(srv.Token)
		if parseErr != nil {
			return 0, shared.HelloResult{}, fmt.Errorf("invalid stored credential")
		}
		panelID, idErr := d.st.PanelInstanceID(ctx)
		if idErr != nil || credential.PanelInstanceID != panelID {
			return 0, shared.HelloResult{}, fmt.Errorf("credential belongs to a different panel")
		}
		token, err = shared.NewCredential(panelID, credential.Epoch)
		if err != nil {
			return 0, shared.HelloResult{}, err
		}
		if err := d.st.RotateServerToken(ctx, srv.ID, token); err != nil {
			return 0, shared.HelloResult{}, err
		}
	}
	learnedAddr := remoteAddr
	if srv.Address != "" {
		remoteAddr = srv.Address
	}
	// 网卡地址候选（§9）：agent 上报非空列表时持久化。
	var nicAddrs string
	if len(p.NICAddresses) > 0 {
		if b, err := json.Marshal(p.NICAddresses); err == nil {
			nicAddrs = string(b)
		}
	}
	if err := d.st.TouchServer(ctx, srv.ID, p.XrayVersion, p.AgentVersion, remoteAddr, learnedAddr, nicAddrs); err != nil {
		log.Printf("dispatch: touch server %d: %v", srv.ID, err)
	}
	return srv.ID, shared.HelloResult{ServerID: srv.ID, Token: token}, nil
}

// OnAgentConnect 在 agent hello 认证完成后调用（ws.Hub.OnConnect）：
// 重置 sent 未终态的命令为 queued（§2 重发语义）并补发全部滞留命令。
func (d *Dispatcher) OnAgentConnect(ctx context.Context, serverID int64) {
	if err := d.st.ResetSentCommands(ctx, serverID); err != nil {
		log.Printf("dispatch: reset sent commands for server %d: %v", serverID, err)
	}
	d.Flush(ctx, serverID)
}

// HandleMessage 处理 agent 上行业务信封（注入 ws.Hub.OnMessage）。
func (d *Dispatcher) HandleMessage(serverID int64, env shared.Envelope) {
	switch env.Kind {
	case shared.KindResponse:
		d.handleCommandResponse(serverID, env)
	case shared.KindRequest:
		switch env.Type {
		case shared.TypeSettingsSync:
			d.handleAgentSettingsSync(serverID, env)
		default:
			log.Printf("dispatch: server %d: ignore request type=%s request_id=%s",
				serverID, env.Type, env.RequestID)
		}
	case shared.KindEvent:
		switch env.Type {
		case shared.TypeTelemetry:
			d.handleTelemetry(serverID, env)
		case shared.TypeDriftReport:
			d.handleDriftReport(serverID, env)
		default:
			log.Printf("dispatch: server %d: ignore event type=%s request_id=%s",
				serverID, env.Type, env.RequestID)
		}
	default:
		log.Printf("dispatch: server %d: ignore kind=%s type=%s request_id=%s",
			serverID, env.Kind, env.Type, env.RequestID)
	}
}

func (d *Dispatcher) handleAgentSettingsSync(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var payload shared.AgentSettingsSyncPayload
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInvalidArgument, "invalid settings sync payload", nil)
		return
	}
	if len(payload.LastApplyError) > 512 {
		payload.LastApplyError = payload.LastApplyError[:512]
	}
	if err := d.st.ReportAgentSettings(ctx, serverID, payload.AppliedRevision, payload.LastApplyError); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to record settings status", nil)
		return
	}
	settings, err := d.st.AgentSettings(ctx)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load settings", nil)
		return
	}
	panelID, err := d.st.PanelInstanceID(ctx)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load panel identity", nil)
		return
	}
	changed := payload.PanelInstanceID != panelID ||
		payload.AppliedRevision != settings.Revision ||
		payload.LastApplyError != ""
	result := shared.AgentSettingsSyncResult{Changed: changed}
	if changed {
		doc := shared.AgentSettingsDocument{
			SchemaVersion: shared.AgentSettingsSchemaVersion,
			Panel: shared.PanelMetadata{
				InstanceID: panelID,
				Version:    d.PanelVersion,
				PublicURL:  d.panelPublicURL(ctx),
				WSURL:      panelWSURL(d.panelPublicURL(ctx)),
			},
			Agent: settings,
		}
		result.Settings = &doc
	}
	d.replyAgentRequest(ctx, serverID, env, shared.CodeOK, "", result)
}

func (d *Dispatcher) replyAgentRequest(ctx context.Context, serverID int64, request shared.Envelope, code, message string, data any) {
	if data == nil {
		data = struct{}{}
	}
	response := shared.Envelope{
		Kind: shared.KindResponse, Type: request.Type,
		RequestID: request.RequestID, TraceID: request.TraceID,
		Code: code, Message: message, Data: marshalMessageData(data),
	}
	if err := d.req.Send(ctx, serverID, response); err != nil {
		log.Printf("dispatch: server %d: send %s response: %v", serverID, request.Type, err)
	}
}

func (d *Dispatcher) panelPublicURL(ctx context.Context) string {
	if value, err := d.st.GetSetting(ctx, store.SettingPublicURL); err == nil && value != "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(d.PanelPublicURL, "/")
}

func panelWSURL(publicURL string) string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/api/agent/ws"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// NotifyAgentSettingsChanged is best effort. Agents also pull after hello and
// periodically, so losing this hint cannot leave them permanently stale.
func (d *Dispatcher) NotifyAgentSettingsChanged(ctx context.Context, revision int64) {
	servers, err := d.st.ListServers(ctx)
	if err != nil {
		log.Printf("dispatch: list agents for settings notification: %v", err)
		return
	}
	for _, server := range servers {
		if !d.req.IsOnline(server.ID) {
			continue
		}
		id := shared.NewMessageID()
		env := shared.Envelope{
			Kind: shared.KindEvent, Type: shared.TypeSettingsChanged,
			RequestID: id, TraceID: id,
			Data: marshalMessageData(shared.AgentSettingsChangedPayload{Revision: revision}),
		}
		if err := d.req.Send(ctx, server.ID, env); err != nil {
			log.Printf("dispatch: notify server %d settings revision %d: %v", server.ID, revision, err)
		}
	}
}

func marshalMessageData(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

// handleTelemetry 落库周期遥测（§13）：xray 版本、主机指标、流量增量（仅统计）。
func (d *Dispatcher) handleTelemetry(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var p shared.TelemetryPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		log.Printf("dispatch: server %d: bad telemetry data: %v", serverID, err)
		return
	}
	if p.XrayVersion != "" {
		if err := d.st.UpdateServerVersion(ctx, serverID, p.XrayVersion); err != nil {
			log.Printf("dispatch: server %d: update xray version: %v", serverID, err)
		}
	}
	if p.Host != nil {
		metrics := store.ServerMetrics{
			Load1: p.Host.Load1, Load5: p.Host.Load5, Load15: p.Host.Load15,
			CPUPercent: p.Host.CPUPercent, MemTotal: p.Host.MemTotal, MemUsed: p.Host.MemUsed,
			DiskTotal: p.Host.DiskTotal, DiskUsed: p.Host.DiskUsed,
			NetworkInterface: p.Host.NetworkInterface,
			NetworkTXBytes:   p.Host.NetworkTXBytes, NetworkRXBytes: p.Host.NetworkRXBytes,
			NetworkTXBPS: p.Host.NetworkTXBPS, NetworkRXBPS: p.Host.NetworkRXBPS,
			UptimeSeconds: p.Host.UptimeSeconds, LatencyMS: p.Host.LatencyMS,
		}
		if err := d.st.SaveServerMetrics(ctx, serverID, metrics); err != nil {
			log.Printf("dispatch: server %d: upsert metrics: %v", serverID, err)
		}
	}
	for _, td := range p.Traffic {
		switch {
		case td.Node != "":
			var nodeID int64
			if _, err := fmt.Sscanf(td.Node, "node_%d", &nodeID); err != nil || nodeID == 0 {
				continue
			}
			if err := d.st.AddTraffic(ctx, nodeID, "", td.Up, td.Down); err != nil {
				log.Printf("dispatch: server %d: add node traffic: %v", serverID, err)
			}
		case td.User != "":
			if err := d.st.AddTraffic(ctx, 0, td.User, td.Up, td.Down); err != nil {
				log.Printf("dispatch: server %d: add user traffic: %v", serverID, err)
			}
		}
	}
}

// handleDriftReport 落库配置漂移状态（§17 reconcile，仅在变化时上报）。
func (d *Dispatcher) handleDriftReport(serverID int64, env shared.Envelope) {
	var p shared.DriftPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		log.Printf("dispatch: server %d: bad drift data: %v", serverID, err)
		return
	}
	if err := d.st.SetServerDrift(context.Background(), serverID, p.Drifted); err != nil {
		log.Printf("dispatch: server %d: set drift: %v", serverID, err)
		return
	}
	if p.Drifted {
		log.Printf("dispatch: server %d: 配置漂移（外部修改），待管理员修复", serverID)
		if d.Alerter != nil {
			d.Alerter.Notify(serverID, alert.EventConfigDrift, "", "xray 配置被外部修改，待管理员修复")
		}
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityWarning, Category: logging.CategoryServer,
			Action: "server.config_drift_detected", ServerID: &serverID,
			Detail:    "xray 配置被外部修改，待管理员修复",
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
	} else {
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryServer,
			Action: "server.config_drift_cleared", ServerID: &serverID,
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
	}
}

// handleCommandResponse 回写命令状态与节点状态机：成功 acked/active，失败 failed。
func (d *Dispatcher) handleCommandResponse(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var p shared.ApplyResultPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		log.Printf("dispatch: server %d: bad response data: %v", serverID, err)
		return
	}
	// 归属校验：只接受命令所属服务器的回执，防跨服务器伪造/串线（§5）。
	cmd, err := d.st.CommandByRequestID(ctx, env.RequestID)
	if err != nil {
		log.Printf("dispatch: server %d: response for unknown request %s: %v", serverID, env.RequestID, err)
		return
	}
	cmdID := cmd.ID
	if cmd.ServerID != serverID {
		log.Printf("dispatch: server %d: response for command %d owned by server %d, ignored", serverID, cmdID, cmd.ServerID)
		return
	}
	if env.Type != cmd.Type || env.TraceID != cmd.TraceID {
		log.Printf("dispatch: server %d: response correlation mismatch command=%d", serverID, cmdID)
		return
	}
	d.logWebSocketRPC(serverID, *cmd, env)
	if env.Code == shared.CodeOK || env.Code == shared.CodeAccepted {
		// 仅 sent → acked；死信后迟到的 ack 不得翻回终态，也不得触碰节点状态机。
		acked, err := d.st.MarkCommandAcked(ctx, cmdID)
		if err != nil {
			log.Printf("dispatch: ack command %d: %v", cmdID, err)
			return
		}
		if !acked {
			log.Printf("dispatch: server %d: response for command %d not in sent state, ignored", serverID, cmdID)
			return
		}
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryCommand,
			Action: "command.succeeded", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    map[string]any{"command_id": cmdID, "type": cmd.Type, "hop_id": p.HopID},
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
		// 链跳配置件回执（§21）：路由到链编排器，不触碰节点状态机。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p, "")
			return
		}
		realized, _ := json.Marshal(p.RealizedConfig)
		// NodeID 0 表示非节点命令（add_user/remove_user 等），不触碰节点状态机。
		if p.NodeID != 0 {
			if err := d.st.SetNodeActive(ctx, p.NodeID, realized); err != nil {
				log.Printf("dispatch: node %d active: %v", p.NodeID, err)
			}
			log.Printf("dispatch: server %d: node %d active (command %d)", serverID, p.NodeID, cmdID)
			d.advanceChainByNode(ctx, p.NodeID) // 链出口业务就绪 → 推进链编排（§21 阶段 2 起）
		} else {
			log.Printf("dispatch: server %d: command %d acked", serverID, cmdID)
		}
	} else {
		errorMessage := env.Message
		if errorMessage == "" {
			errorMessage = env.Code
		}
		failed, err := d.st.MarkCommandFailedWithError(ctx, cmdID, errorMessage)
		if err != nil {
			log.Printf("dispatch: fail command %d: %v", cmdID, err)
			return
		}
		if !failed {
			log.Printf("dispatch: server %d: response for command %d not in sent state, ignored", serverID, cmdID)
			return
		}
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityError, Category: logging.CategoryCommand,
			Action: "command.failed", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    map[string]any{"command_id": cmdID, "type": cmd.Type, "hop_id": p.HopID, "error": errorMessage},
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
		// 链跳配置件回执（§21）：路由到链编排器（失败定位到跳，链置 failed）。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p, errorMessage)
			return
		}
		if p.NodeID != 0 {
			if err := d.st.SetNodeFailed(ctx, p.NodeID, errorMessage); err != nil {
				log.Printf("dispatch: node %d failed: %v", p.NodeID, err)
			}
			log.Printf("dispatch: server %d: node %d failed (command %d): %s", serverID, p.NodeID, cmdID, errorMessage)
			d.alertNodeFailed(serverID, p.NodeID, errorMessage)
			d.failChainByNode(ctx, p.NodeID, errorMessage) // 链出口业务失败 → 链 failed 定位到跳（§21）
		} else {
			log.Printf("dispatch: server %d: command %d failed: %s", serverID, cmdID, errorMessage)
		}
	}
}

func (d *Dispatcher) logWebSocketRPC(serverID int64, cmd store.Command, env shared.Envelope) {
	if d.RequestLog == nil {
		return
	}
	duration := int64(0)
	if cmd.UpdatedAt != "" {
		if sentAt, err := time.Parse("2006-01-02 15:04:05", cmd.UpdatedAt); err == nil {
			duration = time.Since(sentAt).Milliseconds()
		}
	}
	logging.LogWebSocketRPC(d.RequestLog, logging.RequestEntry{
		RequestID:  env.RequestID,
		TraceID:    env.TraceID,
		RPCType:    env.Type,
		RPCCode:    env.Code,
		DurationMS: duration,
		Attributes: map[string]string{
			"server_id":  fmt.Sprintf("%d", serverID),
			"command_id": fmt.Sprintf("%d", cmd.ID),
		},
		ErrorSummary: env.Message,
	})
}

// alertNodeFailed 上报节点置 failed 事件（§19）：apply_result 失败与死信两条路径共用。
func (d *Dispatcher) alertNodeFailed(serverID, nodeID int64, reason string) {
	if d.Alerter == nil {
		return
	}
	d.Alerter.Notify(serverID, alert.EventNodeFailed, fmt.Sprintf("node_%d", nodeID), reason)
}

func (d *Dispatcher) serverLock(serverID int64) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	l, ok := d.flushMu[serverID]
	if !ok {
		l = &sync.Mutex{}
		d.flushMu[serverID] = l
	}
	return l
}

func (d *Dispatcher) recordOperation(event logging.OperationEvent) {
	if d.OperationLog == nil {
		return
	}
	if err := d.OperationLog.Record(context.Background(), event); err != nil {
		log.Printf("dispatch: record operation %s: %v", event.Action, err)
	}
}

func optionalID(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
