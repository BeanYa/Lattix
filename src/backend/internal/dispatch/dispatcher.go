// Package dispatch 负责命令生命周期（§2、§4）：入队 commands 表、经 Requester 投递、
// 按响应回写命令与节点状态机；并实现 session 认证（bootstrap token 换发长期凭证，§11）。
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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
	st   *store.Store
	req  ws.AgentRequester
	fsm  *chainFSM    // 链路状态机：所有链状态变更的唯一入口
	efsm *endpointFSM // 共享端点状态机：所有端点状态变更的唯一入口

	// Alerter 事件告警（§19）：nil = 关闭；仅状态跃迁时调用，发送方自行防抖动。
	Alerter      *alert.Notifier
	OperationLog *logging.OperationStore
	RequestLog   *logging.RequestLog

	// DestCandidates 是面板内置的 dest 白名单（§6 预检 fallback）：链编排下发
	// apply_node/portal 时携带（panel 初始化时注入，与单机节点同一份）。
	DestCandidates      []string
	PanelVersion        string
	PanelPublicURL      string
	AgentReleaseBase    string
	PanelLifecycle      func() shared.PanelLifecycleSnapshot
	OnNodePublished     func(context.Context, int64) error
	OnChainPublished    func(context.Context, int64) error
	OnEndpointPublished func(context.Context, int64) error
	// OnOnlineUsers 接收 telemetry 帧中的在线用户快照（panel.Server 注入；nil = 不记录）。
	OnOnlineUsers func(serverID int64, users []shared.OnlineUserStat, now time.Time)

	mu      sync.Mutex
	flushMu map[int64]*sync.Mutex // 每服务器一把，避免并发 Flush 重复投递

	testProgressMu sync.RWMutex
	testProgress   map[int64]shared.ServerTestProgressPayload

	endpointRetryMu   sync.Mutex
	endpointRetriedAt map[int64]time.Time // 端点自动重试退避：key endpointID → 上次自动补发时间

	// xray.cleanup / xray.rebuild 的同步回执（requestID → chan），每类命令一份注册表（各自独立锁）。
	cleanupWaiters *syncWaiters[shared.CleanupXrayResult]
	rebuildWaiters *syncWaiters[shared.RebuildXrayResult]
}

// syncWaiterOut 是一次同步命令等待的投递结果。
type syncWaiterOut[R any] struct {
	result *R
	err    error
}

// syncWaiters 管理一类「同步命令 + 回执等待」命令的 waiter 注册表（requestID → chan）。
type syncWaiters[R any] struct {
	mu      sync.Mutex
	waiters map[string]chan syncWaiterOut[R]
}

func newSyncWaiters[R any]() *syncWaiters[R] {
	return &syncWaiters[R]{waiters: make(map[string]chan syncWaiterOut[R])}
}

func (w *syncWaiters[R]) register(requestID string) chan syncWaiterOut[R] {
	ch := make(chan syncWaiterOut[R], 1)
	w.mu.Lock()
	w.waiters[requestID] = ch
	w.mu.Unlock()
	return ch
}

func (w *syncWaiters[R]) unregister(requestID string) {
	w.mu.Lock()
	delete(w.waiters, requestID)
	w.mu.Unlock()
}

// deliver 把回执投递给同步等待者（handleCommandResponse 调用）。
func (w *syncWaiters[R]) deliver(requestID string, result *R, errorMessage string) {
	w.mu.Lock()
	ch, ok := w.waiters[requestID]
	delete(w.waiters, requestID)
	w.mu.Unlock()
	if !ok {
		return
	}
	out := syncWaiterOut[R]{result: result}
	if errorMessage != "" {
		out.err = fmt.Errorf("%s", errorMessage)
	}
	ch <- out
}

// New 创建 Dispatcher。
func New(st *store.Store, req ws.AgentRequester) *Dispatcher {
	d := &Dispatcher{
		st: st, req: req, flushMu: make(map[int64]*sync.Mutex),
		testProgress:      make(map[int64]shared.ServerTestProgressPayload),
		cleanupWaiters:    newSyncWaiters[shared.CleanupXrayResult](),
		rebuildWaiters:    newSyncWaiters[shared.RebuildXrayResult](),
		endpointRetriedAt: make(map[int64]time.Time),
	}
	d.fsm = &chainFSM{d: d}
	d.efsm = &endpointFSM{d: d}
	return d
}

func (d *Dispatcher) EnqueueServerTest(
	ctx context.Context,
	serverID int64,
	categories []shared.ServerTestCategory,
	catalog shared.ServerTestCatalogSnapshot,
) (*store.ServerTestTask, error) {
	traceID := logging.TraceID(ctx)
	task, _, err := d.st.EnqueueServerTest(ctx, serverID, traceID, categories, catalog)
	if err != nil {
		return nil, err
	}
	d.testProgressMu.Lock()
	delete(d.testProgress, serverID)
	d.testProgressMu.Unlock()
	d.Flush(ctx, serverID)
	return task, nil
}

func (d *Dispatcher) ServerTestProgress(serverID int64) *shared.ServerTestProgressPayload {
	d.testProgressMu.RLock()
	defer d.testProgressMu.RUnlock()
	progress, ok := d.testProgress[serverID]
	if !ok {
		return nil
	}
	copy := progress
	copy.Categories = append([]shared.ServerTestCategoryProgress(nil), progress.Categories...)
	return &copy
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

// EnqueueAgentUpgradeAll 为所有已登记服务器入队 agent 自升级命令。
// 命令沿用持久化队列语义，离线 agent 会在重连后收到。
func (d *Dispatcher) EnqueueAgentUpgradeAll(ctx context.Context, version, releaseBase string, force bool) (int, error) {
	servers, err := d.st.ListServers(ctx)
	if err != nil {
		return 0, fmt.Errorf("list servers for agent upgrade: %w", err)
	}
	queued := 0
	for _, server := range servers {
		if _, err := d.Enqueue(ctx, server.ID, shared.TypeUpgradeAgent, shared.UpgradeAgentPayload{
			Version: version, ReleaseBase: releaseBase, Force: force,
		}); err != nil {
			return queued, fmt.Errorf("enqueue agent upgrade for server %d: %w", server.ID, err)
		}
		queued++
	}
	return queued, nil
}

const (
	uninstallMaxAttempts = 10
	uninstallRetryBase   = 100 * time.Millisecond
	uninstallRetryCap    = 10 * time.Second
)

// UninstallWithRetry makes one best-effort uninstall RPC with a bounded retry
// budget. Every delivery reuses the same request ID so the Agent can handle it
// idempotently; Panel deletion remains authoritative even when no ACK arrives.
func (d *Dispatcher) UninstallWithRetry(ctx context.Context, serverID int64, payload shared.UninstallPayload) (bool, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, 0, fmt.Errorf("marshal uninstall payload: %w", err)
	}
	requestID := shared.NewMessageID()
	traceID := logging.TraceID(ctx)
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	commandID, err := d.st.EnqueueCommand(ctx, requestID, traceID, serverID, shared.TypeUninstall, raw)
	if err != nil {
		return false, 0, err
	}
	envelope := shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeUninstall,
		RequestID: requestID, TraceID: traceID, Data: raw,
	}

	for attempt := 1; attempt <= uninstallMaxAttempts; attempt++ {
		if err := d.st.MarkCommandSent(ctx, commandID); err != nil {
			return false, attempt - 1, err
		}
		_ = d.req.Send(ctx, serverID, envelope)
		acked, err := d.waitForCommandACK(ctx, requestID, uninstallRetryDelay(attempt))
		if err != nil {
			return false, attempt, err
		}
		if acked {
			return true, attempt, nil
		}
	}
	return false, uninstallMaxAttempts, nil
}

// CleanupXraySync 同步下发 xray.cleanup 并等待 agent 回执（面板「清理 xray 缓存」，
// §docs/xray-cleanup-design.md §6）：命令照常落库，回执数据经进程内 waiter 投递。
// 重发复用同一 request id（agent 命令队列按 request id 幂等去重）。
func (d *Dispatcher) CleanupXraySync(ctx context.Context, serverID int64, payload shared.CleanupXrayPayload) (*shared.CleanupXrayResult, error) {
	return runSyncCommand(ctx, d, serverID, shared.TypeCleanupXray, payload, d.cleanupWaiters,
		"marshal cleanup payload", "agent 未回执清理命令（已重试 %d 次）")
}

// RebuildXraySync 同步下发 xray.rebuild 并等待 agent 回执（面板「重建 xray 配置」，
// §docs/rebuild-xray-config-design.md）：命令照常落库，回执数据经进程内 waiter 投递。
// 重发复用同一 request id（agent 命令队列按 request id 幂等去重）。
func (d *Dispatcher) RebuildXraySync(ctx context.Context, serverID int64, payload shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error) {
	return runSyncCommand(ctx, d, serverID, shared.TypeRebuildXray, payload, d.rebuildWaiters,
		"marshal rebuild payload", "agent 未回执重建命令（已重试 %d 次）")
}

// runSyncCommand 同步下发命令并等待 agent 回执（CleanupXraySync/RebuildXraySync 共用）：
// 命令照常落库，回执数据经进程内 waiter 投递；无回执则按退避重发（同 request id，agent 幂等）。
func runSyncCommand[R any](ctx context.Context, d *Dispatcher, serverID int64, typ string, payload any,
	waiters *syncWaiters[R], marshalErrHint, timeoutErrFormat string) (*R, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", marshalErrHint, err)
	}
	requestID := shared.NewMessageID()
	traceID := logging.TraceID(ctx)
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	commandID, err := d.st.EnqueueCommand(ctx, requestID, traceID, serverID, typ, raw)
	if err != nil {
		return nil, err
	}
	envelope := shared.Envelope{
		Kind: shared.KindRequest, Type: typ,
		RequestID: requestID, TraceID: traceID, Data: raw,
	}
	waiter := waiters.register(requestID)
	defer waiters.unregister(requestID)
	for attempt := 1; attempt <= uninstallMaxAttempts; attempt++ {
		if err := d.st.MarkCommandSent(ctx, commandID); err != nil {
			return nil, err
		}
		if err := d.req.Send(ctx, serverID, envelope); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case out := <-waiter:
			return out.result, out.err
		case <-time.After(uninstallRetryDelay(attempt)):
			// 无回执则重发（同 request id，agent 幂等）
		}
	}
	return nil, fmt.Errorf(timeoutErrFormat, uninstallMaxAttempts)
}

func (d *Dispatcher) waitForCommandACK(ctx context.Context, requestID string, timeout time.Duration) (bool, error) {
	deadline := time.NewTimer(timeout)
	poll := time.NewTicker(25 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	for {
		command, err := d.st.CommandByRequestID(ctx, requestID)
		if err != nil {
			return false, err
		}
		if command.Status == store.CommandStatusAcked {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-poll.C:
		}
	}
}

func uninstallRetryDelay(attempt int) time.Duration {
	delay := uninstallRetryBase
	for i := 1; i < attempt && delay < uninstallRetryCap; i++ {
		delay *= 2
	}
	if delay > uninstallRetryCap {
		return uninstallRetryCap
	}
	return delay
}

func (d *Dispatcher) enqueueRevisionTask(ctx context.Context, serverID int64, typ string, payload any,
	revisionID int64, taskKey string) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}
	requestID := shared.NewMessageID()
	traceID := logging.TraceID(ctx)
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	id, err := d.st.EnqueueRevisionTaskCommand(ctx, requestID, traceID, serverID, typ, raw, revisionID, taskKey)
	if err != nil {
		return 0, err
	}
	d.Flush(ctx, serverID)
	return id, nil
}

// maxCommandAttempts 是命令投递次数上限；超过即死信（failed，§2）。
const maxCommandAttempts = 10

// endpointAutoRetryMinInterval 端点自动重试最小间隔（抑制 failed 端点重复补发刷屏）。
// var 而非 const：测试可临时调小。
var endpointAutoRetryMinInterval = 60 * time.Second

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
	versionMismatch := false
	if target := strings.TrimSpace(d.PanelVersion); target != "" && target != "dev" {
		if server, err := d.st.ServerByID(ctx, serverID); err == nil {
			versionMismatch = server.AgentVersion != target
		}
	}
	for _, c := range cmds {
		if versionMismatch && !isExactAgentUpgrade(c, d.PanelVersion) {
			continue
		}
		if c.Attempts >= maxCommandAttempts {
			log.Printf("dispatch: command %d dead-lettered after %d attempts", c.ID, c.Attempts)
			if err := d.st.DeadLetterCommand(ctx, c.ID); err != nil {
				log.Printf("dispatch: dead-letter command %d: %v", c.ID, err)
			}
			d.setRevisionTaskResult(ctx, c.ID, false,
				fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts))
			d.recordOperation(logging.OperationEvent{
				Severity: logging.SeverityError, Category: logging.CategoryCommand,
				Action: "command.dead_lettered", ServerID: &serverID,
				Detail: d.commandDetail(ctx, c, 0,
					fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts)),
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
			if c.Type == shared.TypeApplySharedEndpoint {
				var p shared.ApplySharedEndpointPayload
				if json.Unmarshal(c.Data, &p) == nil && p.EndpointID != 0 {
					_ = d.efsm.Transition(ctx, p.EndpointID, store.EndpointStatusFailed,
						fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts), nil)
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

func isExactAgentUpgrade(command store.Command, version string) bool {
	if command.Type != shared.TypeUpgradeAgent {
		return false
	}
	var payload shared.UpgradeAgentPayload
	return json.Unmarshal(command.Data, &payload) == nil && payload.Version == version
}

func (d *Dispatcher) AuthenticateToken(ctx context.Context, token string) (ws.AuthResult, error) {
	srv, err := d.st.ServerByToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return ws.AuthResult{}, ws.ErrAuthentication
	}
	if err != nil {
		return ws.AuthResult{}, err
	}
	credential, err := shared.ParseCredential(token)
	if err != nil {
		return ws.AuthResult{}, ws.ErrAuthentication
	}
	panelID, err := d.st.PanelInstanceID(ctx)
	if err != nil {
		return ws.AuthResult{}, err
	}
	if credential.PanelInstanceID != panelID {
		return ws.AuthResult{}, ws.ErrAuthentication
	}
	return ws.AuthResult{ServerID: srv.ID, Reconnect: srv.LastConnectedAt != nil}, nil
}

// OpenSession records the current Agent capabilities and prepares an
// idempotent bootstrap-to-long-term credential exchange when required.
func (d *Dispatcher) OpenSession(ctx context.Context, auth ws.AuthResult, p shared.SessionOpenPayload, remoteAddr string) (ws.OpenSessionResult, error) {
	srv, err := d.st.ServerByID(ctx, auth.ServerID)
	if err != nil {
		return ws.OpenSessionResult{}, err
	}
	result := ws.OpenSessionResult{}
	if !srv.CredentialCommitted {
		if srv.CredentialPendingToken == "" {
			credential, parseErr := shared.ParseCredential(srv.Token)
			if parseErr != nil {
				return result, fmt.Errorf("invalid stored credential")
			}
			panelID, idErr := d.st.PanelInstanceID(ctx)
			if idErr != nil {
				return result, idErr
			}
			if credential.PanelInstanceID != panelID {
				return result, ws.ErrAuthentication
			}
			pending, createErr := shared.NewCredential(panelID, credential.Epoch)
			if createErr != nil {
				return result, createErr
			}
			exchangeID := shared.NewMessageID()
			if _, setErr := d.st.SetPendingCredential(ctx, srv.ID, pending, exchangeID); setErr != nil {
				return result, setErr
			}
			srv, err = d.st.ServerByID(ctx, srv.ID)
			if err != nil {
				return result, err
			}
		}
		result.IssuedToken = srv.CredentialPendingToken
		result.ExchangeID = srv.CredentialExchangeID
	}
	learnedAddr := preferredAgentAddress(remoteAddr, p.NICAddresses)
	if srv.AddressMode == store.AddressModeManual {
		remoteAddr = srv.Address
	} else {
		remoteAddr = learnedAddr
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
	// 地址列表双族采集（§9）：访问流学到的公网地址居首，NIC 公网地址全部并入去重，
	// 管理员手工条目保留；仅本次上报了网卡地址时刷新（否则无法还原手工条目）。
	if len(p.NICAddresses) > 0 {
		learnedForList := ""
		if isPublicAgentIP(learnedAddr) {
			learnedForList = learnedAddr
		}
		var nicPublic []string
		for _, candidate := range p.NICAddresses {
			if isPublicAgentIP(candidate) {
				nicPublic = append(nicPublic, candidate)
			}
		}
		if err := d.st.RefreshServerAddresses(ctx, srv, learnedForList, nicPublic); err != nil {
			log.Printf("dispatch: refresh server %d addresses: %v", srv.ID, err)
		}
	}
	d.ensureAgentVersion(ctx, srv.ID, p.AgentVersion)
	return result, nil
}

func (d *Dispatcher) CommitCredential(ctx context.Context, serverID int64, exchangeID string) error {
	committed, err := d.st.CommitPendingCredential(ctx, serverID, exchangeID)
	if err != nil {
		return err
	}
	if !committed {
		return fmt.Errorf("credential exchange is no longer pending")
	}
	return nil
}

func (d *Dispatcher) ensureAgentVersion(ctx context.Context, serverID int64, reported string) {
	target := strings.TrimSpace(d.PanelVersion)
	if target == "" || target == "dev" || reported == target {
		return
	}
	commands, err := d.st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		log.Printf("dispatch: server %d inspect agent version commands: %v", serverID, err)
		return
	}
	for _, command := range commands {
		if command.ServerID != serverID || (command.Status != store.CommandStatusQueued && command.Status != store.CommandStatusSent) {
			continue
		}
		var payload shared.UpgradeAgentPayload
		if json.Unmarshal(command.Data, &payload) == nil && payload.Version == target {
			return
		}
	}
	if _, err := d.Enqueue(ctx, serverID, shared.TypeUpgradeAgent, shared.UpgradeAgentPayload{
		Version: target, ReleaseBase: d.AgentReleaseBase,
	}); err != nil {
		log.Printf("dispatch: server %d enqueue agent synchronization to %s: %v", serverID, target, err)
	}
}

// preferredAgentAddress keeps a publicly routable socket peer when available.
// Some container managers SNAT published connections to the bridge gateway; in
// that case the agent's reported public NIC is a better subscription address.
func preferredAgentAddress(remoteAddr string, nicAddresses []string) string {
	if isPublicAgentIP(remoteAddr) {
		return remoteAddr
	}
	var publicIPv6 string
	for _, candidate := range nicAddresses {
		if !isPublicAgentIP(candidate) {
			continue
		}
		if ip := net.ParseIP(candidate); ip != nil && ip.To4() != nil {
			return candidate
		}
		if publicIPv6 == "" {
			publicIPv6 = candidate
		}
	}
	if publicIPv6 != "" {
		return publicIPv6
	}
	return remoteAddr
}

func isPublicAgentIP(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// RFC 6598 shared address space is not publicly routable.
	_, sharedRange, _ := net.ParseCIDR("100.64.0.0/10")
	return !sharedRange.Contains(ip)
}

// OnAgentConnect 在 agent session.ready 完成后调用（ws.Hub.OnConnect）：
// 重置 sent 未终态的命令为 queued（§2 重发语义）并补发全部滞留命令；
// 同时自愈该服务器上未 active 的共享端点（重新下发 apply_shared_endpoint）；
// 恢复该服务器上处于编排中的链（覆盖 Agent 离线期间编排停滞场景）。
func (d *Dispatcher) OnAgentConnect(ctx context.Context, serverID int64) {
	if err := d.st.ResetSentCommands(ctx, serverID); err != nil {
		log.Printf("dispatch: reset sent commands for server %d: %v", serverID, err)
	}
	d.Flush(ctx, serverID)
	// 自愈：服务器上有未生效的共享端点时重新 reconcile（覆盖命令丢失/死信场景）。
	endpoints, err := d.st.NonActiveEndpointsByServer(ctx, serverID)
	if err != nil {
		log.Printf("dispatch: list non-active endpoints for server %d: %v", serverID, err)
		return
	}
	for _, ep := range endpoints {
		if err := d.ReconcileSharedEndpoint(ctx, ep.ID); err != nil {
			log.Printf("dispatch: reconcile shared endpoint %d on connect: %v", ep.ID, err)
		}
	}
	// 恢复编排：该服务器上 applying/waiting_for_agent/active_unconfirmed 的链重新推进。
	d.fsm.ResumeChainsByServer(ctx, serverID)
}

// ReconcileStaleEndpoints 周期性自愈：遍历所有未 active 的共享端点，对服务器在线的重新下发部署命令。
// 覆盖命令死信、命令丢失、服务器短暂离线后恢复等场景。由 main.go 定时调用。
func (d *Dispatcher) ReconcileStaleEndpoints(ctx context.Context) {
	servers, err := d.st.ListServers(ctx)
	if err != nil {
		log.Printf("dispatch: reconcile stale endpoints: list servers: %v", err)
		return
	}
	for _, srv := range servers {
		if !d.req.IsOnline(srv.ID) {
			continue
		}
		endpoints, err := d.st.NonActiveEndpointsByServer(ctx, srv.ID)
		if err != nil {
			log.Printf("dispatch: reconcile stale endpoints server %d: %v", srv.ID, err)
			continue
		}
		for _, ep := range endpoints {
			if err := d.ReconcileSharedEndpoint(ctx, ep.ID); err != nil {
				log.Printf("dispatch: reconcile stale endpoint %d: %v", ep.ID, err)
			} else {
				log.Printf("dispatch: reconcile stale endpoint %d (server %d, status %s)", ep.ID, srv.ID, ep.Status)
			}
			// 兜底链评估：覆盖回执丢失等边缘场景导致的链状态过期
			//（Evaluate 幂等且 active→degraded 仅首次触发告警，重复调用无副作用）。
			if chainIDs, err := d.st.ChainIDsByEndpoint(ctx, ep.ID); err == nil {
				for _, cid := range chainIDs {
					d.recomputeChain(ctx, cid)
				}
			}
		}
	}
}

// InvalidateChainForServerDeletion 服务器删除时级联失效链（§10，经 FSM 校验转换合法性）。
// 由 panel.handleDeleteServer 调用，替代直接调用 store 方法。
func (d *Dispatcher) InvalidateChainForServerDeletion(ctx context.Context, chainID, serverID int64, reason string) error {
	return d.fsm.InvalidateForServerDeletion(ctx, chainID, serverID, reason)
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
		case shared.TypeServerSettingsSync:
			d.handleServerSettingsSync(serverID, env)
		case shared.TypeServerTestResult:
			d.handleServerTestResult(serverID, env)
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
		case shared.TypeServerTestProgress:
			d.handleServerTestProgress(serverID, env)
		default:
			log.Printf("dispatch: server %d: ignore event type=%s request_id=%s",
				serverID, env.Type, env.RequestID)
		}
	default:
		log.Printf("dispatch: server %d: ignore kind=%s type=%s request_id=%s",
			serverID, env.Kind, env.Type, env.RequestID)
	}
}

func (d *Dispatcher) handleServerTestProgress(serverID int64, env shared.Envelope) {
	var progress shared.ServerTestProgressPayload
	if err := json.Unmarshal(env.Data, &progress); err != nil || progress.SchemaVersion != shared.ServerTestSchemaVersion ||
		!shared.ValidMessageID(progress.TaskID) || progress.Generation < 1 || progress.Sequence == 0 ||
		(progress.Status != shared.ServerTestAccepted && progress.Status != shared.ServerTestRunning) {
		log.Printf("dispatch: server %d: invalid server test progress", serverID)
		return
	}
	task, err := d.st.ServerTestByServerID(context.Background(), serverID)
	if err != nil || task.TaskID != progress.TaskID || task.Generation != progress.Generation || task.Status.Terminal() {
		return
	}
	d.testProgressMu.Lock()
	previous, exists := d.testProgress[serverID]
	if !exists || previous.TaskID != progress.TaskID || progress.Sequence > previous.Sequence {
		d.testProgress[serverID] = progress
	}
	d.testProgressMu.Unlock()
	if _, err := d.st.UpdateServerTestState(context.Background(), serverID, progress.TaskID, progress.Generation, progress.Status); err != nil {
		log.Printf("dispatch: server %d: persist server test state: %v", serverID, err)
	}
}

func (d *Dispatcher) handleServerTestResult(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var chunk shared.ServerTestResultChunkPayload
	if err := json.Unmarshal(env.Data, &chunk); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInvalidArgument, "invalid server test result chunk", nil)
		return
	}
	outcome, err := d.st.SaveServerTestResultChunk(ctx, serverID, chunk)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInvalidArgument, err.Error(), nil)
		return
	}
	if outcome == store.ServerTestChunkComplete || outcome == store.ServerTestChunkSuperseded {
		d.testProgressMu.Lock()
		delete(d.testProgress, serverID)
		d.testProgressMu.Unlock()
	}
	d.replyAgentRequest(ctx, serverID, env, shared.CodeOK, "", shared.ServerTestResultACK{Status: string(outcome)})
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

// handleServerSettingsSync 处理 agent.settings.sync 的平行通道：agent 上报已应用
// effective revision，面板比对后返回逐服务器合并的 ServerSettingsDocument。
func (d *Dispatcher) handleServerSettingsSync(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var payload shared.ServerSettingsSyncPayload
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInvalidArgument, "invalid server settings sync payload", nil)
		return
	}
	if len(payload.LastApplyError) > 512 {
		payload.LastApplyError = payload.LastApplyError[:512]
	}
	if err := d.st.ReportServerSettings(ctx, serverID, payload.AppliedRevision, payload.LastApplyError); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to record server settings status", nil)
		return
	}
	settings, revision, err := d.st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load server settings", nil)
		return
	}
	panelID, err := d.st.PanelInstanceID(ctx)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load panel identity", nil)
		return
	}
	changed := (payload.PanelInstanceID != "" && payload.PanelInstanceID != panelID) ||
		payload.AppliedRevision != revision ||
		payload.LastApplyError != ""
	result := shared.ServerSettingsSyncResult{Changed: changed}
	if changed {
		result.Settings = &shared.ServerSettingsDocument{
			SchemaVersion: shared.ServerSettingsSchemaVersion,
			Revision:      revision,
			Server:        settings,
		}
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

// NotifyAgentSettingsChanged is best effort. Agents also pull after session.open and
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

// NotifyServerSettingsChanged is best effort; agents also pull after session.open
// and periodically. serverID=0 notifies every online server (default changed),
// otherwise only that server (custom override changed).
func (d *Dispatcher) NotifyServerSettingsChanged(ctx context.Context, serverID int64, revision int64) {
	servers, err := d.st.ListServers(ctx)
	if err != nil {
		log.Printf("dispatch: list agents for server settings notification: %v", err)
		return
	}
	for _, server := range servers {
		if serverID != 0 && server.ID != serverID {
			continue
		}
		if !d.req.IsOnline(server.ID) {
			continue
		}
		id := shared.NewMessageID()
		env := shared.Envelope{
			Kind: shared.KindEvent, Type: shared.TypeServerSettingsChanged,
			RequestID: id, TraceID: id,
			Data: marshalMessageData(shared.ServerSettingsChangedPayload{Revision: revision}),
		}
		if err := d.req.Send(ctx, server.ID, env); err != nil {
			log.Printf("dispatch: notify server %d server settings revision %d: %v", server.ID, revision, err)
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
		latencyProbeActive := d.latencyProbeActive(p.Host.LatencyProbeActive)
		metrics := store.ServerMetrics{
			Load1: p.Host.Load1, Load5: p.Host.Load5, Load15: p.Host.Load15,
			CPUPercent: p.Host.CPUPercent, MemTotal: p.Host.MemTotal, MemUsed: p.Host.MemUsed,
			DiskTotal: p.Host.DiskTotal, DiskUsed: p.Host.DiskUsed,
			NetworkInterface: p.Host.NetworkInterface,
			NetworkTXBytes:   p.Host.NetworkTXBytes, NetworkRXBytes: p.Host.NetworkRXBytes,
			NetworkTXBPS: p.Host.NetworkTXBPS, NetworkRXBPS: p.Host.NetworkRXBPS,
			UptimeSeconds: p.Host.UptimeSeconds, LatencyMS: p.Host.LatencyMS,
		}
		if err := d.st.SaveServerMetrics(ctx, serverID, metrics, latencyProbeActive); err != nil {
			log.Printf("dispatch: server %d: upsert metrics: %v", serverID, err)
		}
	}
	if len(p.Traffic) > 0 {
		counters := make([]store.TrafficCounterSnapshot, 0, len(p.Traffic))
		for _, counter := range p.Traffic {
			counters = append(counters, store.TrafficCounterSnapshot{
				NodeID: counter.NodeID, EndpointID: counter.EndpointID, HopID: counter.HopID, User: counter.User,
				Up: counter.Up, Down: counter.Down,
			})
		}
		if err := d.st.ApplyTrafficSnapshot(ctx, serverID, p.XrayInstanceID, counters, time.Now().UTC()); err != nil {
			log.Printf("dispatch: server %d: apply traffic snapshot: %v", serverID, err)
		}
	}
	// 在线用户快照：每帧全量覆盖该服务器记录（空快照 [] = 清除，保留在线时为 0 的语义）。
	// nil = 该帧未携带在线数据（agent 查询失败/不支持），保留旧快照直至新鲜度窗口过期。
	if p.OnlineUsers != nil && d.OnOnlineUsers != nil {
		d.OnOnlineUsers(serverID, p.OnlineUsers, time.Now().UTC())
	}
}

func (d *Dispatcher) latencyProbeActive(reported *bool) bool {
	if d.PanelLifecycle != nil && d.PanelLifecycle().State != shared.PanelStateActive {
		return false
	}
	return reported == nil || *reported
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
		if cmd.Type == shared.TypeServerTestRun {
			var testPayload shared.ServerTestRunPayload
			if err := json.Unmarshal(cmd.Data, &testPayload); err == nil {
				if _, err := d.st.UpdateServerTestState(ctx, serverID, testPayload.TaskID, testPayload.Generation, shared.ServerTestAccepted); err != nil {
					log.Printf("dispatch: server %d: accept server test: %v", serverID, err)
				}
			}
			return
		}
		d.setRevisionTaskResult(ctx, cmdID, true, "")
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryCommand,
			Action: "command.succeeded", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    d.commandDetail(ctx, *cmd, p.HopID, ""),
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
		// xray.cleanup：差异/结果投递给同步等待者（面板清理接口），不触碰节点状态机。
		if cmd.Type == shared.TypeCleanupXray {
			d.cleanupWaiters.deliver(cmd.RequestID, p.Cleanup, "")
			log.Printf("dispatch: server %d: cleanup xray command %d acked", serverID, cmdID)
			return
		}
		if cmd.Type == shared.TypeRebuildXray {
			d.rebuildWaiters.deliver(cmd.RequestID, p.Rebuild, "")
			log.Printf("dispatch: server %d: rebuild xray command %d acked", serverID, cmdID)
			return
		}
		// 清理命令只更新命令/修订任务，不得触碰当前工作拓扑的节点状态。
		if cmd.Type == shared.TypeRemoveChainHop || cmd.Type == shared.TypeRemoveNode ||
			cmd.Type == shared.TypeRemoveSharedEndpoint {
			log.Printf("dispatch: server %d: cleanup command %d acked", serverID, cmdID)
			return
		}
		// 链跳配置件回执（§21）：路由到链编排器，不触碰节点状态机。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p, "")
			return
		}
		if p.EndpointID != 0 {
			d.clearEndpointRetry(p.EndpointID)
			realized, _ := json.Marshal(p.RealizedConfig)
			// 端点状态机：active 副作用（链重算 + 订阅重建）在 onEnter 分派。
			if err := d.efsm.Transition(ctx, p.EndpointID, store.EndpointStatusActive, "部署回执确认", realized); err != nil {
				log.Printf("dispatch: shared endpoint %d active: %v", p.EndpointID, err)
			}
			return
		}
		realized, _ := json.Marshal(p.RealizedConfig)
		// NodeID 0 表示非节点命令（add_user/remove_user 等），不触碰节点状态机。
		if p.NodeID != 0 {
			if err := d.st.SetNodeActive(ctx, p.NodeID, realized); err != nil {
				log.Printf("dispatch: node %d active: %v", p.NodeID, err)
			} else if d.OnNodePublished != nil {
				if err := d.OnNodePublished(ctx, p.NodeID); err != nil {
					log.Printf("dispatch: enqueue subscriptions for node %d: %v", p.NodeID, err)
				}
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
		if cmd.Type == shared.TypeServerTestRun {
			var testPayload shared.ServerTestRunPayload
			if err := json.Unmarshal(cmd.Data, &testPayload); err == nil {
				if err := d.st.FailServerTestCommand(ctx, serverID, testPayload.TaskID, testPayload.Generation, env.Code, errorMessage); err != nil {
					log.Printf("dispatch: server %d: fail server test command: %v", serverID, err)
				}
			}
			d.testProgressMu.Lock()
			delete(d.testProgress, serverID)
			d.testProgressMu.Unlock()
			return
		}
		d.setRevisionTaskResult(ctx, cmdID, false, errorMessage)
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityError, Category: logging.CategoryCommand,
			Action: "command.failed", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    d.commandDetail(ctx, *cmd, p.HopID, errorMessage),
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
		// xray.cleanup：失败回执投递给同步等待者（面板清理接口），保留命令失败记录。
		if cmd.Type == shared.TypeCleanupXray {
			d.cleanupWaiters.deliver(cmd.RequestID, p.Cleanup, errorMessage)
			log.Printf("dispatch: server %d: cleanup xray command %d failed: %s", serverID, cmdID, errorMessage)
			return
		}
		if cmd.Type == shared.TypeRebuildXray {
			d.rebuildWaiters.deliver(cmd.RequestID, p.Rebuild, errorMessage)
			log.Printf("dispatch: server %d: rebuild xray command %d failed: %s", serverID, cmdID, errorMessage)
			return
		}
		// 清理失败保留任务记录，不能让已发布的数据面 revision 回滚或失效。
		if cmd.Type == shared.TypeRemoveChainHop || cmd.Type == shared.TypeRemoveNode ||
			cmd.Type == shared.TypeRemoveSharedEndpoint {
			log.Printf("dispatch: server %d: cleanup command %d failed: %s", serverID, cmdID, errorMessage)
			return
		}
		// 链跳配置件回执（§21）：路由到链编排器（失败定位到跳，链置 failed）。
		if p.HopID != 0 {
			d.handleChainHopResult(serverID, p, errorMessage)
			return
		}
		if p.EndpointID != 0 {
			// 端点状态机：failed 副作用（链重算 + 订阅重建）在 onEnter 分派。
			if err := d.efsm.Transition(ctx, p.EndpointID, store.EndpointStatusFailed, errorMessage, nil); err != nil {
				log.Printf("dispatch: shared endpoint %d failed: %v", p.EndpointID, err)
			}
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

func (d *Dispatcher) setRevisionTaskResult(ctx context.Context, commandID int64, success bool, errorMessage string) {
	task, err := d.st.RevisionTaskByCommandID(ctx, commandID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		log.Printf("dispatch: lookup revision task for command %d: %v", commandID, err)
		return
	}
	if err := d.st.SetRevisionTaskResult(ctx, task.ID, success, errorMessage); err != nil {
		log.Printf("dispatch: update revision task %d: %v", task.ID, err)
		return
	}
	if task.Phase == "cleanup" {
		d.refreshCleanupStatus(ctx, task.RevisionID)
	}
}

// commandDetail 构造命令操作日志 Detail：统一携带 command_id/type/attempts/hop_id，
// shared-endpoint 命令附加 endpoint_id 与使用它的 chain_ids，失败/死信附加 error。
func (d *Dispatcher) commandDetail(ctx context.Context, cmd store.Command, hopID int64, errMsg string) map[string]any {
	detail := map[string]any{
		"command_id": cmd.ID,
		"type":       cmd.Type,
		"hop_id":     hopID,
		"attempts":   cmd.Attempts,
	}
	if endpointID, ok := commandEndpointID(cmd); ok {
		detail["endpoint_id"] = endpointID
		if chainIDs, err := d.st.ChainIDsByEndpoint(ctx, endpointID); err != nil {
			log.Printf("dispatch: chain ids for endpoint %d: %v", endpointID, err)
		} else if len(chainIDs) > 0 {
			detail["chain_ids"] = chainIDs
		}
	}
	if errMsg != "" {
		detail["error"] = errMsg
	}
	return detail
}

// commandEndpointID 从命令数据解析 shared-endpoint 相关命令的端点 id（非端点命令返回 false）。
func commandEndpointID(cmd store.Command) (int64, bool) {
	switch cmd.Type {
	case shared.TypeApplySharedEndpoint:
		var p shared.ApplySharedEndpointPayload
		if json.Unmarshal(cmd.Data, &p) == nil && p.EndpointID != 0 {
			return p.EndpointID, true
		}
	case shared.TypeRemoveSharedEndpoint:
		var p shared.RemoveSharedEndpointPayload
		if json.Unmarshal(cmd.Data, &p) == nil && p.EndpointID != 0 {
			return p.EndpointID, true
		}
	}
	return 0, false
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

// allowEndpointAutoRetry 端点自动重试退避：距上次自动补发不足间隔时拒绝；允许时记录本次时间。
func (d *Dispatcher) allowEndpointAutoRetry(endpointID int64) bool {
	d.endpointRetryMu.Lock()
	defer d.endpointRetryMu.Unlock()
	if last, ok := d.endpointRetriedAt[endpointID]; ok && time.Since(last) < endpointAutoRetryMinInterval {
		return false
	}
	d.endpointRetriedAt[endpointID] = time.Now()
	return true
}

// clearEndpointRetry 清除端点的自动重试退避记录（apply 成功回执时调用）。
func (d *Dispatcher) clearEndpointRetry(endpointID int64) {
	d.endpointRetryMu.Lock()
	defer d.endpointRetryMu.Unlock()
	delete(d.endpointRetriedAt, endpointID)
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
