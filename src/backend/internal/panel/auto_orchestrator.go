package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// HealthEventKind 表示触发自动编排观察者的健康信号类别。
type HealthEventKind string

const (
	HealthEventBanChanged    HealthEventKind = "ban_changed"
	HealthEventPairProbe     HealthEventKind = "pair_probe"
	HealthEventQuotaCheck    HealthEventKind = "quota_check"
	HealthEventAgentOffline  HealthEventKind = "agent_offline"
	HealthEventAgentOnline   HealthEventKind = "agent_online"
	HealthEventPolicyChanged HealthEventKind = "policy_changed"
)

// HealthEvent 是投递给 AutoOrchestrator 的轻量级观察者事件。
type HealthEvent struct {
	Kind     HealthEventKind
	ServerID int64
	ChainID  int64
}

const (
	defaultOrchestratorCoalesceWindow = 300 * time.Millisecond
	defaultOrchestratorSweepInterval  = 30 * time.Second
	defaultPairUnreachableStreak      = 3
)

// AutoOrchestrator 采用「观察者事件响应 + 单协程防抖合并 Worker」架构，
// 监听四类服务器/链路健康信号（三网封禁、段间 TCP/ICMP 不可达、当期流量耗尽、Agent 离线超时），
// 并按管理员在各跳位（Hop Slot）配置的候选节点池自动生成新 Revision 完成 Make-Before-Break 切换。
type AutoOrchestrator struct {
	s              *Server
	events         chan HealthEvent
	coalesceWindow time.Duration
	sweepInterval  time.Duration

	mu             sync.Mutex
	offlineSince   map[int64]time.Time
	quotaExhausted map[int64]bool
	exhaustAlertAt map[string]time.Time

	workers    *chainWorkerManager
	workerTick time.Duration
}

func newAutoOrchestrator(s *Server) *AutoOrchestrator {
	o := &AutoOrchestrator{
		s:              s,
		events:         make(chan HealthEvent, 256),
		coalesceWindow: defaultOrchestratorCoalesceWindow,
		sweepInterval:  defaultOrchestratorSweepInterval,
		offlineSince:   make(map[int64]time.Time),
		quotaExhausted: make(map[int64]bool),
		exhaustAlertAt: make(map[string]time.Time),
		workerTick:     2 * time.Second,
	}
	o.workers = newChainWorkerManager(o)
	return o
}

// emit 非阻塞向编排事件总线投递一条健康信号（队列满时由周期巡检兜底，绝不阻塞主路径）。
func (o *AutoOrchestrator) emit(ev HealthEvent) {
	if o == nil {
		return
	}
	select {
	case o.events <- ev:
	default:
	}
}

// NotifyServerBanChanged 在 ban-watch 状态机发生状态跃迁时触发。
func (o *AutoOrchestrator) NotifyServerBanChanged(serverID int64, _ string) {
	o.emit(HealthEvent{Kind: HealthEventBanChanged, ServerID: serverID})
}

// NotifyChainUpdated 在链路创建、编辑或策略更新后触发。
func (o *AutoOrchestrator) NotifyChainUpdated(chainID int64) {
	o.emit(HealthEvent{Kind: HealthEventPolicyChanged, ChainID: chainID})
}

// OnAgentDisconnect 记录服务器离线起始时间并投递事件。
func (o *AutoOrchestrator) OnAgentDisconnect(serverID int64) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if _, exists := o.offlineSince[serverID]; !exists {
		o.offlineSince[serverID] = time.Now()
	}
	o.mu.Unlock()
	o.emit(HealthEvent{Kind: HealthEventAgentOffline, ServerID: serverID})
}

// OnAgentConnect 清除服务器离线计时并触发恢复/回切评估。
func (o *AutoOrchestrator) OnAgentConnect(serverID int64) {
	if o == nil {
		return
	}
	o.mu.Lock()
	delete(o.offlineSince, serverID)
	o.mu.Unlock()
	o.emit(HealthEvent{Kind: HealthEventAgentOnline, ServerID: serverID})
}

// OnLatencyReport 在一轮服务器对 TCP/ICMP 测速落库后更新 server_pair_watch 状态机并按需触发编排。
func (o *AutoOrchestrator) OnLatencyReport(ctx context.Context, fromServerID int64, samples []store.ServerLatencySample) {
	if o == nil || len(samples) == 0 {
		return
	}
	// 按目标服务器归组：同一轮里对同一目标可能同时包含 tcp 与 icmp 样本，任一可达即视为本轮可达。
	reachableByTarget := make(map[int64]bool)
	for _, sample := range samples {
		if sample.ToServer <= 0 {
			continue
		}
		if _, seen := reachableByTarget[sample.ToServer]; !seen {
			reachableByTarget[sample.ToServer] = false
		}
		if sample.LatencyMS != nil {
			reachableByTarget[sample.ToServer] = true
		}
	}
	if len(reachableByTarget) == 0 {
		return
	}
	watches, err := o.s.st.ServerPairWatchMap(ctx)
	if err != nil {
		log.Printf("auto_orchestrator: load pair watch map: %v", err)
		return
	}
	now := time.Now().UTC()
	changed := false
	for toServerID, reachable := range reachableByTarget {
		key := [2]int64{fromServerID, toServerID}
		prev := watches[key]
		prev.FromServer = fromServerID
		prev.ToServer = toServerID
		next := prev
		if reachable {
			if prev.LossStreak == 0 && (prev.State == "" || prev.State == store.PairStateNormal) {
				continue
			}
			next.LossStreak = 0
			next.State = store.PairStateNormal
			next.UpdatedAt = now
		} else {
			next.LossStreak = prev.LossStreak + 1
			if next.LossStreak >= defaultPairUnreachableStreak {
				next.State = store.PairStateUnreachable
			} else if next.State == "" {
				next.State = store.PairStateNormal
			}
			next.UpdatedAt = now
		}
		if err := o.s.st.UpsertServerPairWatch(ctx, next); err != nil {
			log.Printf("auto_orchestrator: upsert pair watch %d->%d: %v", fromServerID, toServerID, err)
			continue
		}
		changed = true
	}
	if changed {
		o.emit(HealthEvent{Kind: HealthEventPairProbe, ServerID: fromServerID})
	}
}

// OnTelemetryMetrics 在主机遥测更新网卡字节后检查当期流量配额是否耗尽。
func (o *AutoOrchestrator) OnTelemetryMetrics(ctx context.Context, serverID int64) {
	if o == nil {
		return
	}
	plans, err := o.s.st.ServerTrafficPlanMap(ctx)
	if err != nil {
		return
	}
	plan, ok := plans[serverID]
	if !ok || plan.QuotaBytes == nil || *plan.QuotaBytes <= 0 {
		return
	}
	loc := o.s.inspectionLocation(ctx)
	usage, err := o.s.serverTrafficUsageAt(ctx, serverID, plan, loc, time.Now().In(loc))
	if err != nil {
		return
	}
	exhausted := usage.Used >= *plan.QuotaBytes
	o.mu.Lock()
	prev := o.quotaExhausted[serverID]
	o.quotaExhausted[serverID] = exhausted
	o.mu.Unlock()
	if exhausted || prev != exhausted {
		o.emit(HealthEvent{Kind: HealthEventQuotaCheck, ServerID: serverID})
	}
}

// Run 启动单协程防抖协调 Worker：合并短时间内的突发事件并定期巡检离线超时与冷却期回切。
func (o *AutoOrchestrator) Run(ctx context.Context) {
	if o.workers != nil {
		o.workers.Start(ctx)
	}
	o.EvaluateAll(ctx)
	ticker := time.NewTicker(o.sweepInterval)
	defer ticker.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	scheduleDebounce := func() {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(o.coalesceWindow)
			debounceC = debounceTimer.C
		}
	}

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		case <-o.events:
			scheduleDebounce()
		case <-debounceC:
			debounceTimer = nil
			debounceC = nil
			o.EvaluateAll(ctx)
		case <-ticker.C:
			o.EvaluateAll(ctx)
		}
	}
}

// EvaluateAll 扫描所有启用了自动编排策略的链路并按需执行跳位切换。
func (o *AutoOrchestrator) EvaluateAll(ctx context.Context) {
	if o == nil || o.s == nil {
		return
	}
	graphChains, err := o.s.st.ChainIDsWithHopGraphs(ctx)
	if err != nil {
		log.Printf("auto_orchestrator: list graph chains: %v", err)
		return
	}
	graphSet := make(map[int64]bool, len(graphChains))
	for _, chainID := range graphChains {
		graphSet[chainID] = true
		if o.workers != nil && o.workers.Ensure(chainID) {
			o.workers.Wake(chainID)
		}
	}

}

// 供保存/删除等路径协调 worker 生命周期。
func (o *AutoOrchestrator) EnsureChainWorker(chainID int64) {
	if o == nil || o.workers == nil {
		return
	}
	o.workers.Ensure(chainID)
}

func (o *AutoOrchestrator) ResumeChain(chainID int64) {
	if o == nil || o.workers == nil {
		return
	}
	o.workers.Resume(chainID)
}

func (o *AutoOrchestrator) PauseChain(chainID int64) {
	if o == nil || o.workers == nil {
		return
	}
	o.workers.Pause(chainID)
}

func (o *AutoOrchestrator) StopChain(chainID int64) {
	if o == nil || o.workers == nil {
		return
	}
	o.workers.Stop(chainID)
}

func (o *AutoOrchestrator) WaitChainSettled(ctx context.Context, chainID int64, timeout time.Duration) error {
	if o == nil || o.workers == nil {
		return nil
	}
	return o.workers.WaitSettled(ctx, chainID, timeout)
}

// orchestratorSnapshot 聚合单次评估所需的全局服务器健康上下文。
type orchestratorSnapshot struct {
	servers        map[int64]*store.Server
	banWatches     map[int64]store.ServerBanWatch
	pairWatches    map[[2]int64]store.ServerPairWatch
	quotaExhausted map[int64]bool
	quotaUsed      map[int64]int64
	quotaLimit     map[int64]int64
	now            time.Time
}

func (o *AutoOrchestrator) loadSnapshot(ctx context.Context) (*orchestratorSnapshot, error) {
	servers, err := o.s.st.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	srvMap := make(map[int64]*store.Server, len(servers))
	for i := range servers {
		srvMap[servers[i].ID] = &servers[i]
	}
	banWatches, err := o.s.st.ServerBanWatchMap(ctx)
	if err != nil {
		return nil, err
	}
	pairWatches, err := o.s.st.ServerPairWatchMap(ctx)
	if err != nil {
		return nil, err
	}
	plans, err := o.s.st.ServerTrafficPlanMap(ctx)
	if err != nil {
		return nil, err
	}
	loc := o.s.inspectionLocation(ctx)
	today := time.Now().In(loc)
	quotaExhausted := make(map[int64]bool)
	quotaUsed := make(map[int64]int64)
	quotaLimit := make(map[int64]int64)
	for srvID, plan := range plans {
		if plan.QuotaBytes == nil || *plan.QuotaBytes <= 0 {
			continue
		}
		usage, uerr := o.s.serverTrafficUsageAt(ctx, srvID, plan, loc, today)
		if uerr != nil {
			continue
		}
		quotaUsed[srvID] = usage.Used
		quotaLimit[srvID] = *plan.QuotaBytes
		if usage.Used >= *plan.QuotaBytes {
			quotaExhausted[srvID] = true
		}
	}
	return &orchestratorSnapshot{
		servers:        srvMap,
		banWatches:     banWatches,
		pairWatches:    pairWatches,
		quotaExhausted: quotaExhausted,
		quotaUsed:      quotaUsed,
		quotaLimit:     quotaLimit,
		now:            time.Now().UTC(),
	}, nil
}

func (o *AutoOrchestrator) serverOfflineDuration(srv *store.Server, now time.Time) time.Duration {
	o.mu.Lock()
	since, ok := o.offlineSince[srv.ID]
	o.mu.Unlock()
	if ok {
		return now.Sub(since)
	}
	if srv.LastDisconnectedAt != nil {
		return now.Sub(*srv.LastDisconnectedAt)
	}
	return 0
}

// isCandidateHealthy 判断一个候选服务器在指定跳位上是否满足健康与拓扑约束。
func (o *AutoOrchestrator) isCandidateHealthy(
	snap *orchestratorSnapshot,
	cand store.HopSwitchTarget,
	seq int,
	totalHops int,
	exitIsExternal bool,
	usedServers map[int64]bool,
	upServerID int64,
	downServerID int64,
	policy store.HopSwitchPolicy,
) (bool, string) {
	if usedServers[cand.ServerID] {
		return false, "已在同链路其他跳位使用"
	}
	srv := snap.servers[cand.ServerID]
	if srv == nil {
		return false, "服务器不存在"
	}
	isLast := seq == totalHops-1
	if isLast {
		if srv.IsExternal() != exitIsExternal {
			return false, "出口节点类别不匹配"
		}
		if srv.IsExternal() && srv.ParseExternalExit() == nil {
			return false, "外部直链配置无效"
		}
	} else {
		if srv.IsExternal() || !inboundCapable(srv) {
			return false, "无入站能力"
		}
	}
	if !srv.IsExternal() && !o.s.req.IsOnline(srv.ID) {
		return false, "Agent 离线"
	}
	if snap.quotaExhausted[srv.ID] {
		return false, "当期流量已耗尽"
	}
	if !srv.IsExternal() && !srv.NonDirect {
		bw := snap.banWatches[srv.ID]
		if bw.State == store.BanStateBanned || bw.State == store.BanStateSuspected {
			return false, "三网测速处于封禁/疑似封禁状态"
		}
	}
	streakLimit := int64(policy.UnreachableStreak)
	if streakLimit < 1 {
		streakLimit = defaultPairUnreachableStreak
	}
	if upServerID != 0 {
		if snap.pairWatches[[2]int64{upServerID, srv.ID}].LossStreak >= streakLimit ||
			snap.pairWatches[[2]int64{srv.ID, upServerID}].LossStreak >= streakLimit {
			return false, "与上游跳探测不可达"
		}
	}
	if downServerID != 0 {
		if snap.pairWatches[[2]int64{srv.ID, downServerID}].LossStreak >= streakLimit ||
			snap.pairWatches[[2]int64{downServerID, srv.ID}].LossStreak >= streakLimit {
			return false, "与下游跳探测不可达"
		}
	}
	return true, "健康"
}

// EvaluateChain 评估单条链路的健康状态与回切条件，并在需要时自动切换到最佳候选节点。
// EvaluateChain 对有编排图的链走图求值；没有图的链保留旧线性逻辑（Task 6 移除）。
func (o *AutoOrchestrator) EvaluateChain(ctx context.Context, chainID int64) error {
	chain, err := o.s.st.ChainByID(ctx, chainID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if (chain.Status != store.ChainStatusActive && chain.Status != store.ChainStatusDegraded) ||
		chain.DesiredRevisionID != 0 {
		return nil
	}
	graphs, err := o.s.st.ChainHopGraphs(ctx, chainID)
	if err != nil {
		return err
	}
	if len(graphs) == 0 {
		return nil
	}
	hops, err := o.s.st.ChainHops(ctx, chainID)
	if err != nil || len(hops) == 0 {
		return err
	}
	published, err := o.s.st.PublishedChainRevision(ctx, chainID)
	if err != nil || published == nil {
		return nil
	}
	snap, err := o.loadSnapshot(ctx)
	if err != nil {
		return err
	}
	return o.evaluateChainGraphs(ctx, chain, hops, *published, snap, graphs)
}

func (o *AutoOrchestrator) reportFailoverExhausted(ctx context.Context, chain *store.Chain, seq int, serverID int64, reason string) {
	key := fmt.Sprintf("%d:%d", chain.ID, seq)
	o.mu.Lock()
	last, exists := o.exhaustAlertAt[key]
	if exists && time.Since(last) < 10*time.Minute {
		o.mu.Unlock()
		return
	}
	o.exhaustAlertAt[key] = time.Now()
	o.mu.Unlock()

	msg := fmt.Sprintf("链路「%s」第 %d 跳触发自动切换（%s），但备选池中无可用健康服务器", chain.Name, seq+1, reason)
	o.s.recordOperation(ctx, logging.OperationEvent{
		Severity: logging.SeverityWarning,
		Category: logging.CategoryChain,
		Action:   "chain.failover_exhausted",
		ServerID: &serverID,
		Detail: map[string]any{
			"chain_id": chain.ID,
			"seq":      seq,
			"reason":   reason,
		},
	})
	o.s.notifyAlert(serverID, alert.EventChainFailoverExhausted, msg)
}

// switchChainHop 执行单跳替换并生成新 Revision（自动编排与管理员快捷切换共用）。
// 完全复用现有的差分 Planner（PlanRevision）、共享端点 EnsureSharedEndpoint 与 Make-Before-Break 状态机。
func (s *Server) switchChainHop(
	ctx context.Context,
	chainID int64,
	seq int,
	target store.HopSwitchTarget,
	reasonCode string,
	reasonDetail string,
) (*store.ChainRevision, error) {
	chain, err := s.st.ChainByID(ctx, chainID)
	if err != nil {
		return nil, err
	}
	if chain.DesiredRevisionID != 0 &&
		chain.Status != store.ChainStatusFailed &&
		chain.Status != store.ChainStatusActiveFailed &&
		chain.Status != store.ChainStatusWaitingForAgent {
		return nil, errors.New("链路已有部署中的修订，暂无法切换")
	}
	current, err := s.st.PublishedChainRevision(ctx, chain.ID)
	if err != nil || current == nil {
		return nil, errors.New("链路尚无已发布修订")
	}
	if seq < 0 || seq >= len(current.Snapshot.Hops) {
		return nil, fmt.Errorf("跳位序号 %d 越界", seq)
	}
	fromServerID := current.Snapshot.Hops[seq].ServerID
	if fromServerID == target.ServerID {
		return current, nil
	}

	// 构造替换后的跳序列并加载服务器。
	hopCount := len(current.Snapshot.Hops)
	servers := make([]*store.Server, 0, hopCount)
	addresses := make([]string, 0, hopCount)
	seen := make(map[int64]bool, hopCount)
	for i, hop := range current.Snapshot.Hops {
		srvID := hop.ServerID
		addr := hop.Address
		if i == seq {
			srvID = target.ServerID
			addr = target.Address
		}
		if seen[srvID] {
			return nil, errors.New("目标服务器已在该链路其他跳位中使用")
		}
		seen[srvID] = true
		srv, err := s.st.ServerByID(ctx, srvID)
		if err != nil {
			return nil, fmt.Errorf("服务器 %d 不存在", srvID)
		}
		if addr != "" && !store.ServerAddressSet(srv)[addr] {
			addr = "" // 所选地址失效时安全回退默认地址
		}
		if srv.IsExternal() && (i < hopCount-1 || hopCount == 1) {
			return nil, fmt.Errorf("未托管服务器 %s 只能作为链路最后一跳", srv.Alias)
		}
		if i < hopCount-1 && !inboundCapable(srv) {
			return nil, fmt.Errorf("服务器 %s 无入站能力", srv.Alias)
		}
		servers = append(servers, srv)
		addresses = append(addresses, addr)
	}

	exitSrv := servers[hopCount-1]
	externalExit := exitSrv.ParseExternalExit()
	if (externalExit != nil) != (current.Snapshot.ServiceExternal != nil) {
		return nil, errors.New("不支持在托管出口与外部直链出口之间切换")
	}
	if externalExit != nil {
		if err := externalExit.Validate(); err != nil {
			return nil, fmt.Errorf("外部直链出口配置无效: %w", err)
		}
	}

	// 解析现有出口虚拟配置。
	var vc shared.VirtualConfig
	if externalExit == nil {
		if err := json.Unmarshal(current.Snapshot.ServiceConfig, &vc); err != nil {
			return nil, fmt.Errorf("解析出口配置失败: %w", err)
		}
		// 如果替换的是末跳（出口）且候选指定了监听端口，更新出口端口。
		if seq == hopCount-1 && target.ListenPort > 0 && current.Snapshot.ServiceEndpointID == 0 &&
			!(hopCount == 1 && current.Snapshot.EndpointID != 0) {
			vc.Port = target.ListenPort
		}
	}

	// 处理入口共享端点（VLESS 链或带入口协议区块的链）。
	endpointID := int64(0)
	serviceUUID := current.Snapshot.ServiceUUID
	if externalExit != nil {
		serviceUUID = ""
	}
	entryPort := 0
	if seq == 0 && target.ListenPort > 0 {
		entryPort = target.ListenPort
	} else if current.Snapshot.EndpointID != 0 {
		if ep, err := s.st.SharedEndpointByID(ctx, current.Snapshot.EndpointID); err == nil {
			// 若入口服务器未变则沿用原端口，若入口服务器改变且候选未指定端口则 0 自动挑选（防 NAT 越界）。
			if seq != 0 {
				entryPort = ep.Port
			} else if checkPortInRanges(servers[0], ep.Port) == nil {
				entryPort = ep.Port
			}
		}
	} else if len(current.Snapshot.Hops) > 0 {
		if seq != 0 {
			entryPort = current.Snapshot.Hops[0].ForwardPort
		} else if checkPortInRanges(servers[0], current.Snapshot.Hops[0].ForwardPort) == nil {
			entryPort = current.Snapshot.Hops[0].ForwardPort
		}
	}

	if current.Snapshot.EndpointID != 0 {
		if serviceUUID == "" && externalExit == nil {
			serviceUUID = uuid.NewString()
		}
		endpointSource := vc
		if currentEndpoint, err := s.st.SharedEndpointByID(ctx, current.Snapshot.EndpointID); err == nil {
			var prevEntry shared.VirtualConfig
			if json.Unmarshal(currentEndpoint.ConfigTemplate, &prevEntry) == nil {
				endpointSource = prevEntry
			}
		}
		endpointConfig := endpointSource
		endpointConfig.Port = entryPort
		endpointConfig.StaticClients = nil
		endpointJSON, _ := json.Marshal(endpointConfig)
		profile := endpointConfig
		profile.Port = 0
		profileJSON, _ := json.Marshal(profile)
		profileHash := fmt.Sprintf("%x", sha256.Sum256(profileJSON))

		if seq != 0 && current.Snapshot.Hops[0].ServerID == servers[0].ID {
			endpointID = current.Snapshot.EndpointID
		} else {
			ep, _, err := s.st.EnsureSharedEndpoint(ctx, servers[0].ID, shared.ProtocolVLESS,
				entryPort, profileHash, endpointJSON)
			if err != nil {
				return nil, err
			}
			endpointID = ep.ID
		}
		if externalExit == nil {
			vc.StaticClients = []shared.ClientCredential{{ID: serviceUUID, Email: "tunnel:" + serviceUUID}}
		}
		if hopCount == 1 {
			vc.Port = 0
		}
	}

	// 处理 hy2 出口共享端点。
	serviceEndpointID := current.Snapshot.ServiceEndpointID
	if externalExit == nil && vc.Protocol == shared.ProtocolHysteria2 && current.Snapshot.ServiceEndpointID != 0 {
		if seq == hopCount-1 {
			endpointJSON, _ := json.Marshal(vc)
			svcEp, _, err := s.st.EnsureProtocolSharedEndpoint(ctx, exitSrv.ID,
				shared.ProtocolHysteria2, target.ListenPort, endpointJSON)
			if err != nil {
				return nil, err
			}
			serviceEndpointID = svcEp.ID
			vc.Port = 0
		}
	}

	serviceConfig := json.RawMessage(`{}`)
	if externalExit == nil {
		raw, err := json.Marshal(vc)
		if err != nil {
			return nil, err
		}
		serviceConfig = raw
	}

	oldByServer := make(map[int64]store.ChainRevisionHop, hopCount)
	for _, h := range current.Snapshot.Hops {
		oldByServer[h.ServerID] = h
	}
	desiredHops := make([]store.ChainRevisionHop, 0, hopCount)
	for i, srv := range servers {
		hop, ok := oldByServer[srv.ID]
		if !ok {
			hopID, err := s.st.NextChainHopID(ctx, chain.ID, srv.ID)
			if err != nil {
				return nil, err
			}
			hop = store.ChainRevisionHop{HopID: hopID, ServerID: srv.ID}
		}
		hop.Role = store.HopRoleMiddle
		if hopCount == 1 || i == hopCount-1 {
			hop.Role = store.HopRoleExit
		} else if i == 0 {
			hop.Role = store.HopRoleEntry
		}
		if i == 0 {
			if endpointID != 0 {
				hop.ForwardPort = 0
			} else {
				hop.ForwardPort = entryPort
			}
		}
		hop.Address = addresses[i]
		desiredHops = append(desiredHops, hop)
	}

	plaintext := vc.Protocol == shared.ProtocolSocks || vc.Protocol == shared.ProtocolHTTP
	for i := 0; i+1 < len(desiredHops); i++ {
		transport := "direct"
		if servers[i+1].IsExternal() {
			transport = "direct"
		} else if !inboundCapable(servers[i+1]) {
			transport = "reverse"
		} else if plaintext {
			transport = "encrypted"
		}
		if externalExit == nil && i == len(desiredHops)-2 && endpointID != 0 &&
			vc.Protocol == shared.ProtocolHysteria2 && inboundCapable(servers[i+1]) {
			transport = "hy2"
		}
		old := desiredHops[i]
		if old.Transport != transport || (old.TunnelUUID == "" && transport != "direct" && transport != "hy2") {
			desiredHops[i].PortalPort = 0
			desiredHops[i].PortalPublicKey = ""
			desiredHops[i].PortalServerName = ""
		}
		desiredHops[i].Transport = transport
		if transport == "direct" || transport == "hy2" {
			desiredHops[i].TunnelUUID = ""
		} else if desiredHops[i].TunnelUUID == "" {
			desiredHops[i].TunnelUUID = uuid.NewString()
		}
	}

	desired := store.ChainRevisionSnapshot{
		Name:                   chain.Name,
		ServiceNodeID:          current.Snapshot.ServiceNodeID,
		ServiceServerID:        exitSrv.ID,
		ServiceConfig:          serviceConfig,
		EndpointID:             endpointID,
		ServiceEndpointID:      serviceEndpointID,
		ServiceUUID:            serviceUUID,
		TrafficMultiplierMilli: current.Snapshot.TrafficMultiplierMilli,
		Hops:                   desiredHops,
		ServiceExternal:        externalExit,
	}
	if externalExit != nil {
		desired.ServiceNodeID = 0
		desired.ServiceConfig = json.RawMessage(`{}`)
		desired.ServiceUUID = ""
	}

	plan, err := dispatch.PlanRevision(revisionTopology(1, current.Snapshot), revisionTopology(2, desired))
	if err != nil {
		return nil, err
	}
	for _, piece := range plan.Apply {
		desired.ApplyKeys = append(desired.ApplyKeys, piece.Key)
	}
	serviceChanged := false
	for _, key := range desired.ApplyKeys {
		if key == fmt.Sprintf("service/%d", desired.ServiceNodeID) {
			serviceChanged = true
		}
	}
	if !serviceChanged {
		desired.ServiceRealized = append(json.RawMessage(nil), current.Snapshot.ServiceRealized...)
	}

	revision, err := s.st.CreateChainRevision(ctx, chain.ID, desired)
	if err != nil {
		return nil, err
	}
	for _, piece := range plan.Apply {
		_, _ = s.st.AddRevisionTask(ctx, store.ChainRevisionTask{
			RevisionID: revision.ID, TaskKey: piece.Key, Phase: "apply", Action: "apply",
			Kind: piece.Kind, HopID: piece.HopID, ServerID: piece.ServerID,
		})
	}
	for _, piece := range plan.Cleanup {
		_, _ = s.st.AddRevisionTask(ctx, store.ChainRevisionTask{
			RevisionID: revision.ID, TaskKey: "cleanup/" + piece.Key, Phase: "cleanup", Action: "remove",
			Kind: piece.Kind, HopID: piece.HopID, ServerID: piece.ServerID,
		})
	}
	if externalExit == nil && current.Snapshot.ServiceServerID != desired.ServiceServerID {
		_, _ = s.st.AddRevisionTask(ctx, store.ChainRevisionTask{
			RevisionID: revision.ID,
			TaskKey:    fmt.Sprintf("cleanup/service/%d", desired.ServiceNodeID),
			Phase:      "cleanup", Action: "remove", Kind: dispatch.RevisionPieceService,
			HopID: desired.ServiceNodeID, ServerID: current.Snapshot.ServiceServerID,
		})
	}

	var nodePort *int
	if vc.Port != 0 {
		val := vc.Port
		nodePort = &val
	}
	if err := s.disp.EditChainTopology(ctx, revision, vc.Protocol, nodePort, serviceChanged); err != nil {
		return nil, err
	}

	offline := false
	for _, piece := range plan.Apply {
		if !s.req.IsOnline(piece.ServerID) {
			offline = true
			break
		}
	}
	if offline {
		_ = s.st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusWaitingForAgent, "")
	}
	if err := s.disp.StartChainOrWait(ctx, chain.ID, offline, "自动编排所需 Agent 离线，等待上线后恢复"); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_ = s.st.TouchChainHopGraphSwitch(ctx, chain.ID, seq, now)
	_, _ = s.st.RecordChainFailoverEvent(ctx, store.ChainFailoverEvent{
		ChainID:      chain.ID,
		Seq:          seq,
		FromServerID: fromServerID,
		ToServerID:   target.ServerID,
		ReasonCode:   reasonCode,
		ReasonDetail: reasonDetail,
		RevisionID:   revision.ID,
	})

	fromAlias := fmt.Sprintf("#%d", fromServerID)
	if srv, err := s.st.ServerByID(ctx, fromServerID); err == nil && srv.Alias != "" {
		fromAlias = srv.Alias
	}
	toAlias := fmt.Sprintf("#%d", target.ServerID)
	if srv, err := s.st.ServerByID(ctx, target.ServerID); err == nil && srv.Alias != "" {
		toAlias = srv.Alias
	}
	alertMsg := fmt.Sprintf("链路「%s」第 %d 跳已由 %s 切换至 %s（原因：%s）",
		chain.Name, seq+1, fromAlias, toAlias, reasonDetail)
	s.recordOperation(ctx, logging.OperationEvent{
		Severity: logging.SeverityWarning,
		Category: logging.CategoryChain,
		Action:   "chain.auto_failover",
		ServerID: &target.ServerID,
		Detail: map[string]any{
			"chain_id":       chain.ID,
			"seq":            seq,
			"from_server_id": fromServerID,
			"to_server_id":   target.ServerID,
			"reason_code":    reasonCode,
			"reason_detail":  reasonDetail,
			"revision_id":    revision.ID,
		},
	})
	s.notifyAlert(fromServerID, alert.EventChainAutoFailover, alertMsg)
	return revision, nil
}

// chainSwitchHopRequest 是手动跳位切换的请求体。
type chainSwitchHopRequest struct {
	ChainID  int64  `json:"chain_id"`
	Seq      int    `json:"seq"`
	ServerID int64  `json:"server_id"`
	Reason   string `json:"reason,omitempty"`
}

// handleSwitchChainHop 处理 POST /api/chain/switch-hop：管理员手动从某跳的候选池中
// 指定一台服务器立即切换（或切回主节点）。
func (s *Server) handleSwitchChainHop(w http.ResponseWriter, r *http.Request) {
	var req chainSwitchHopRequest
	if err := readJSON(r, &req); err != nil || req.ChainID <= 0 || req.ServerID <= 0 || req.Seq < 0 {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var matched *store.HopSwitchTarget
	if graph, err := s.st.ChainHopGraph(r.Context(), req.ChainID, req.Seq); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if graph != nil {
		for i := range graph.Nodes {
			node := graph.Nodes[i]
			if node.Kind == store.HopGraphKindServer && node.ServerID == req.ServerID {
				matched = &store.HopSwitchTarget{
					ChainID: req.ChainID, Seq: req.Seq, ServerID: node.ServerID,
					Address: node.Address, ListenPort: node.ListenPort,
				}
				break
			}
		}
		if matched == nil {
			writeError(w, http.StatusBadRequest, "目标服务器不在该跳的编排图中")
			return
		}
	}
	reasonDetail := req.Reason
	if reasonDetail == "" {
		reasonDetail = "管理员手动触发跳位候选节点切换"
	}
	o := s.observeStart(r, "chain.switch_hop", "切换链路跳位节点", chainNodeObserveStages)
	defer o.Close()
	if _, err := s.switchChainHop(r.Context(), req.ChainID, req.Seq, *matched, store.FailoverReasonManualSwitch, reasonDetail); err != nil {
		o.Fail(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o.Report("db", 100, "切换 revision 已生成")
	o.Report("publish", 100, "切换编排已启动")
	chain, err := s.st.ChainByID(r.Context(), req.ChainID)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "chain.switch_hop", nil, nil, map[string]any{
		"chain_id":  req.ChainID,
		"seq":       req.Seq,
		"server_id": req.ServerID,
	})
	writeJSON(w, http.StatusAccepted, dto)
}
