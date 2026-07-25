// Package dispatch 负责命令生命周期（§2、§4）：入队 commands 表、经 Requester 投递、
// 按响应回写命令与节点状态机；并实现 hello 认证（bootstrap token 换发长期凭证，§11）。
package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// Dispatcher 串联 store 与 Requester。
type Dispatcher struct {
	st  *store.Store
	req ws.Requester

	panelVersion string // 面板自身版本（构建注入），hello 兼容窗口判定用

	// Alerter 事件告警（§19）：nil = 关闭；仅状态跃迁时调用，发送方自行防抖动。
	Alerter *alert.Notifier

	// DestCandidates 是面板内置的 dest 白名单（§6 预检 fallback）：链编排下发
	// apply_node/portal 时携带（panel 初始化时注入，与单机节点同一份）。
	DestCandidates []string

	mu      sync.Mutex
	flushMu map[int64]*sync.Mutex // 每服务器一把，避免并发 Flush 重复投递
}

// New 创建 Dispatcher。
func New(st *store.Store, req ws.Requester, panelVersion string) *Dispatcher {
	return &Dispatcher{st: st, req: req, panelVersion: panelVersion, flushMu: make(map[int64]*sync.Mutex)}
}

// Enqueue 将命令写入 commands 表（queued）并尽力立即投递；离线则滞留，待重连补发（§2）。
// Envelope.ID 即 commands.id（字符串化），用于请求/响应关联。
func (d *Dispatcher) Enqueue(ctx context.Context, serverID int64, typ string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}
	id, err := d.st.EnqueueCommand(ctx, serverID, typ, raw)
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
// agent 落后出兼容窗口（upgrade_needed）时仅放行 upgrade_agent / uninstall，
// 其余命令滞留 queued，待 agent 升级后随正常 Flush 补发。
func (d *Dispatcher) Flush(ctx context.Context, serverID int64) {
	lock := d.serverLock(serverID)
	lock.Lock()
	defer lock.Unlock()

	upgradeOnly := false
	if srv, err := d.st.ServerByID(ctx, serverID); err == nil && srv.UpgradeNeeded {
		upgradeOnly = true
	}
	cmds, err := d.st.QueuedCommands(ctx, serverID)
	if err != nil {
		log.Printf("dispatch: flush server %d: %v", serverID, err)
		return
	}
	for _, c := range cmds {
		if upgradeOnly && c.Type != shared.TypeUpgradeAgent && c.Type != shared.TypeUninstall {
			continue // 兼容窗口外：常规命令滞留，待 agent 升级后补发
		}
		if c.Attempts >= maxCommandAttempts {
			log.Printf("dispatch: command %d dead-lettered after %d attempts", c.ID, c.Attempts)
			if err := d.st.DeadLetterCommand(ctx, c.ID); err != nil {
				log.Printf("dispatch: dead-letter command %d: %v", c.ID, err)
			}
			// apply_node 死信：节点不能永远卡 applying，置 failed 供管理员重试（§6）。
			if c.Type == shared.TypeApplyNode {
				var p shared.ApplyNodePayload
				if err := json.Unmarshal(c.Payload, &p); err == nil && p.NodeID != 0 {
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
				if err := json.Unmarshal(c.Payload, &p); err == nil && p.HopID != 0 {
					reason := fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts)
					if hop, err := d.st.ChainHopByID(ctx, p.HopID); err == nil {
						d.failHop(ctx, hop, reason)
					}
				}
			}
			continue
		}
		env := shared.Envelope{
			ID:      strconv.FormatInt(c.ID, 10),
			Type:    c.Type,
			Payload: c.Payload,
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
	// 兼容窗口（§18）：主版本不一致拒绝连接；落后超窗口置 upgrade_needed 标志。
	if reason, needed := evaluateAgentVersion(d.panelVersion, p.AgentVersion); reason != "" {
		return 0, shared.HelloResult{}, fmt.Errorf("%s", reason)
	} else if err := d.st.SetServerUpgradeNeeded(ctx, srv.ID, needed); err != nil {
		log.Printf("dispatch: set upgrade_needed server %d: %v", srv.ID, err)
	} else if needed {
		log.Printf("dispatch: server %d agent %s 落后面板 %s 超出兼容窗口，仅放行升级/卸载命令",
			srv.ID, p.AgentVersion, d.panelVersion)
	}
	token := srv.Token
	if srv.LastSeenAt == nil {
		// bootstrap 状态：换发长期凭证（bootstrap 失效）。
		token, err = randomToken()
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
	// 网卡地址候选（§9）：agent 上报时持久化（旧版 agent 不上报则保留旧值）。
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
	switch env.Type {
	case shared.TypeApplyResult:
		d.handleApplyResult(serverID, env)
	case shared.TypeTelemetry:
		d.handleTelemetry(serverID, env)
	case shared.TypeDriftReport:
		d.handleDriftReport(serverID, env)
	default:
		log.Printf("dispatch: server %d: ignore message type=%s id=%s", serverID, env.Type, env.ID)
	}
}

// handleTelemetry 落库周期遥测（§13）：xray 版本、主机指标、流量增量（仅统计）。
func (d *Dispatcher) handleTelemetry(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var p shared.TelemetryPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		log.Printf("dispatch: server %d: bad telemetry payload: %v", serverID, err)
		return
	}
	if p.XrayVersion != "" {
		if err := d.st.UpdateServerVersion(ctx, serverID, p.XrayVersion); err != nil {
			log.Printf("dispatch: server %d: update xray version: %v", serverID, err)
		}
	}
	if p.Host != nil {
		if err := d.st.UpsertServerMetrics(ctx, serverID, p.Host.Load1, p.Host.CPUPercent, p.Host.MemTotal, p.Host.MemUsed); err != nil {
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
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		log.Printf("dispatch: server %d: bad drift payload: %v", serverID, err)
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
	}
}

// handleApplyResult 回写命令状态与节点状态机（§6）：成功 acked/active，失败 failed。
func (d *Dispatcher) handleApplyResult(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var p shared.ApplyResultPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		log.Printf("dispatch: server %d: bad apply_result payload: %v", serverID, err)
		return
	}
	cmdID, err := strconv.ParseInt(env.ID, 10, 64)
	if err != nil {
		log.Printf("dispatch: server %d: bad apply_result id %q: %v", serverID, env.ID, err)
		return
	}
	// 归属校验：只接受命令所属服务器的回执，防跨服务器伪造/串线（§5）。
	cmd, err := d.st.CommandByID(ctx, cmdID)
	if err != nil {
		log.Printf("dispatch: server %d: apply_result for unknown command %d: %v", serverID, cmdID, err)
		return
	}
	if cmd.ServerID != serverID {
		log.Printf("dispatch: server %d: apply_result for command %d owned by server %d, ignored", serverID, cmdID, cmd.ServerID)
		return
	}
	if p.OK {
		// 仅 sent → acked；死信后迟到的 ack 不得翻回终态，也不得触碰节点状态机。
		acked, err := d.st.MarkCommandAcked(ctx, cmdID)
		if err != nil {
			log.Printf("dispatch: ack command %d: %v", cmdID, err)
			return
		}
		if !acked {
			log.Printf("dispatch: server %d: apply_result for command %d not in sent state, ignored", serverID, cmdID)
			return
		}
		// 链跳配置件回执（§21）：路由到链编排器，不触碰节点状态机。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p)
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
		failed, err := d.st.MarkCommandFailed(ctx, cmdID)
		if err != nil {
			log.Printf("dispatch: fail command %d: %v", cmdID, err)
			return
		}
		if !failed {
			log.Printf("dispatch: server %d: apply_result for command %d not in sent state, ignored", serverID, cmdID)
			return
		}
		// 链跳配置件回执（§21）：路由到链编排器（失败定位到跳，链置 failed）。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p)
			return
		}
		if p.NodeID != 0 {
			if err := d.st.SetNodeFailed(ctx, p.NodeID, p.Error); err != nil {
				log.Printf("dispatch: node %d failed: %v", p.NodeID, err)
			}
			log.Printf("dispatch: server %d: node %d failed (command %d): %s", serverID, p.NodeID, cmdID, p.Error)
			d.alertNodeFailed(serverID, p.NodeID, p.Error)
			d.failChainByNode(ctx, p.NodeID, p.Error) // 链出口业务失败 → 链 failed 定位到跳（§21）
		} else {
			log.Printf("dispatch: server %d: command %d failed: %s", serverID, cmdID, p.Error)
		}
	}
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

// randomToken 生成 32 字节随机十六进制凭证。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
