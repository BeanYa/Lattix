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

	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// Dispatcher 串联 store 与 Requester。
type Dispatcher struct {
	st  *store.Store
	req ws.Requester

	mu      sync.Mutex
	flushMu map[int64]*sync.Mutex // 每服务器一把，避免并发 Flush 重复投递
}

// New 创建 Dispatcher。
func New(st *store.Store, req ws.Requester) *Dispatcher {
	return &Dispatcher{st: st, req: req, flushMu: make(map[int64]*sync.Mutex)}
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
			if err := d.st.MarkCommandFailed(ctx, c.ID); err != nil {
				log.Printf("dispatch: dead-letter command %d: %v", c.ID, err)
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
	if srv.Address != "" {
		remoteAddr = srv.Address
	}
	if err := d.st.TouchServer(ctx, srv.ID, p.XrayVersion, remoteAddr); err != nil {
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
	default:
		log.Printf("dispatch: server %d: ignore message type=%s id=%s", serverID, env.Type, env.ID)
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
	if p.OK {
		realized, _ := json.Marshal(p.RealizedConfig)
		if err := d.st.MarkCommandAcked(ctx, cmdID); err != nil {
			log.Printf("dispatch: ack command %d: %v", cmdID, err)
		}
		// NodeID 0 表示非节点命令（add_user/remove_user 等），不触碰节点状态机。
		if p.NodeID != 0 {
			if err := d.st.SetNodeActive(ctx, p.NodeID, realized); err != nil {
				log.Printf("dispatch: node %d active: %v", p.NodeID, err)
			}
			log.Printf("dispatch: server %d: node %d active (command %d)", serverID, p.NodeID, cmdID)
		} else {
			log.Printf("dispatch: server %d: command %d acked", serverID, cmdID)
		}
	} else {
		if err := d.st.MarkCommandFailed(ctx, cmdID); err != nil {
			log.Printf("dispatch: fail command %d: %v", cmdID, err)
		}
		if p.NodeID != 0 {
			if err := d.st.SetNodeFailed(ctx, p.NodeID, p.Error); err != nil {
				log.Printf("dispatch: node %d failed: %v", p.NodeID, err)
			}
			log.Printf("dispatch: server %d: node %d failed (command %d): %s", serverID, p.NodeID, cmdID, p.Error)
		} else {
			log.Printf("dispatch: server %d: command %d failed: %s", serverID, cmdID, p.Error)
		}
	}
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
