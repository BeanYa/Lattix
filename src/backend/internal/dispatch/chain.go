package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// 链编排状态机（§21.1 五阶段）：出口业务 apply_node → 反向链 portal（出口→入口方向）
// → 反向链 bridge（携带对应 portal 回执的 pubkey/port/uuid/shortID/sni）
// → forward（出口→入口方向）→ 全部跳 active 则链 active。
// 每步经 commands 表下发 apply_chain_hop（离线留队列，复用 §2 重发/死信语义）；
// 任一失败/死信 → 链 failed 且 error 定位到跳，重试只重放失败 piece。
//
// 编排器无内存状态：piece 进度完全由 commands 表（queued/sent=在途，acked=完成，
// failed=待重试重放）与 chain_hops 回执列（portal_port/forward_port 等）推导，重启安全。
// 反向链标记：跳 i → i+1 为反向链当且仅当 hops[i].tunnel_uuid 非空（建链时按下游入站能力快照，
// portal 在跳 i 所在上游机，bridge 在跳 i+1 所在下游机；tunnel_domain 取上游跳 id）。

// StartChain 启动建链编排（panel 建链落库后调用）：链置 applying 并推进第一步。
func (d *Dispatcher) StartChain(ctx context.Context, chainID int64) error {
	if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		return err
	}
	d.advanceChain(context.Background(), chainID)
	return nil
}

// RetryChain 重试失败链（§21 只重放失败 piece）：失败跳复位 pending，链回到 applying 后继续编排。
// 已完成的 piece（portal 回执列 / acked 命令）不会重发。
func (d *Dispatcher) RetryChain(ctx context.Context, chainID int64) error {
	chain, err := d.st.ChainByID(ctx, chainID)
	if err != nil {
		return err
	}
	if chain.Status != store.ChainStatusFailed {
		return fmt.Errorf("仅 failed 状态的链可重试（当前 %s）", chain.Status)
	}
	hops, err := d.st.ChainHops(ctx, chainID)
	if err != nil {
		return err
	}
	for _, h := range hops {
		if h.Status == store.HopStatusFailed {
			if err := d.st.SetChainHopStatus(ctx, h.ID, store.HopStatusPending, ""); err != nil {
				return err
			}
		}
	}
	if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		return err
	}
	d.advanceChain(context.Background(), chainID)
	return nil
}

// advanceChain 按当前 DB 状态推进链编排一步（每次至多一个在途 piece，回执到达后再推进）。
// 触发点：建链/重试、apply_chain_hop 回执、出口节点 apply_result。
func (d *Dispatcher) advanceChain(ctx context.Context, chainID int64) {
	chain, err := d.st.ChainByID(ctx, chainID)
	if err != nil {
		log.Printf("dispatch: chain %d: %v", chainID, err)
		return
	}
	if chain.Status != store.ChainStatusApplying {
		return // 仅编排中的链可推进（failed 等重试，active/degraded 已完成编排）
	}
	hops, err := d.st.ChainHops(ctx, chainID)
	if err != nil || len(hops) < 2 {
		log.Printf("dispatch: chain %d hops: %v", chainID, err)
		return
	}
	pieces, err := d.chainPieces(ctx, chainID)
	if err != nil {
		log.Printf("dispatch: chain %d pieces: %v", chainID, err)
		return
	}
	servers := map[int64]*store.Server{}
	for i := range hops {
		srv, err := d.st.ServerByID(ctx, hops[i].ServerID)
		if err != nil {
			log.Printf("dispatch: chain %d hop %d server %d: %v", chainID, hops[i].ID, hops[i].ServerID, err)
			return
		}
		servers[hops[i].ServerID] = srv
	}
	exit := hops[len(hops)-1]

	// 阶段 1：出口业务 inbound（apply_node；NAT 受限直连机自动端口时携带 port_candidates）。
	node, err := d.st.NodeByID(ctx, exit.NodeID)
	if err != nil {
		log.Printf("dispatch: chain %d exit node %d: %v", chainID, exit.NodeID, err)
		return
	}
	switch node.Status {
	case store.NodeStatusPending, store.NodeStatusFailed:
		// pending = 首次下发；failed = 链重试只重放失败 piece（§21）。
		var vc shared.VirtualConfig
		if err := json.Unmarshal(node.ConfigTemplate, &vc); err != nil {
			log.Printf("dispatch: chain %d node %d 虚拟配置损坏: %v", chainID, node.ID, err)
			return
		}
		uuids, err := d.st.NodeUserUUIDs(ctx, node.ID)
		if err != nil {
			log.Printf("dispatch: chain %d node %d users: %v", chainID, node.ID, err)
			return
		}
		payload := shared.ApplyNodePayload{
			NodeID:         node.ID,
			Config:         vc,
			UserUUIDs:      uuids,
			DestCandidates: d.DestCandidates,
		}
		if vc.Port == 0 {
			payload.PortCandidates = listenCandidatesOf(servers[exit.ServerID])
		}
		if err := d.st.SetNodeApplying(ctx, node.ID); err != nil {
			log.Printf("dispatch: chain %d node %d applying: %v", chainID, node.ID, err)
			return
		}
		if _, err := d.Enqueue(ctx, exit.ServerID, shared.TypeApplyNode, payload); err != nil {
			log.Printf("dispatch: chain %d enqueue apply_node: %v", chainID, err)
		}
		return
	case store.NodeStatusApplying:
		return // 等待回执
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(node.RealizedConfig, &rc); err != nil || rc.Port == 0 {
		log.Printf("dispatch: chain %d: 出口节点 %d realized_config 不可用: %v", chainID, node.ID, err)
		return
	}

	// 阶段 2：反向链 portal（出口→入口方向逐个；portal 在反向链上游机）。
	for i := len(hops) - 2; i >= 0; i-- {
		up := hops[i]
		if up.TunnelUUID == "" || (up.PortalPort != 0 && up.PortalPublicKey != "") {
			continue // 直连段无 portal / 已完成
		}
		if up.Status == store.HopStatusFailed || pieceInFlight(pieces, up.ID, shared.HopKindPortal) {
			return
		}
		spec := &shared.PortalSpec{
			Tag:            shared.ChainPortalTag(up.ID),
			TunnelDomain:   shared.TunnelDomain(chainID, up.ID),
			Port:           0, // 自动（NAT 受限直连机从可用段分配，§21）
			PortCandidates: listenCandidatesOf(servers[up.ServerID]),
			TunnelUUID:     up.TunnelUUID,
			ShortID:        tunnelShortID(up.TunnelUUID),
			Dest:           d.portalDest(),
			ServerNames:    []string{destHost(d.portalDest())},
		}
		d.enqueueHop(ctx, chainID, up, shared.HopKindPortal, shared.ApplyChainHopPayload{
			ChainID:        chainID,
			HopID:          up.ID,
			Kind:           shared.HopKindPortal,
			Portal:         spec,
			DestCandidates: d.DestCandidates,
		})
		return
	}

	// 阶段 3：反向链 bridge（下游机；携带对应 portal 回执的 pubkey/port 与下发值 uuid/shortID/sni）。
	for i := len(hops) - 2; i >= 0; i-- {
		up, down := hops[i], hops[i+1]
		if up.TunnelUUID == "" || pieces[pieceKey(down.ID, shared.HopKindBridge)] == store.CommandStatusAcked {
			continue
		}
		if down.Status == store.HopStatusFailed || pieceInFlight(pieces, down.ID, shared.HopKindBridge) {
			return
		}
		upSrv := servers[up.ServerID]
		serverName := up.PortalServerName
		if serverName == "" {
			serverName = destHost(d.portalDest()) // 回退白名单首位（旧回执无 server_name）
		}
		spec := &shared.BridgeSpec{
			TunnelDomain:  shared.TunnelDomain(chainID, up.ID),
			PortalAddress: upSrv.Address,
			PortalPort:    publicPortOf(upSrv, up.PortalPort), // bridge 外拨，取公网侧端口
			TunnelUUID:    up.TunnelUUID,
			PublicKey:     up.PortalPublicKey,
			ShortID:       tunnelShortID(up.TunnelUUID),
			ServerName:    serverName, // portal 回执的实际 SNI（dest 预检可能 fallback）
		}
		d.enqueueHop(ctx, chainID, down, shared.HopKindBridge, shared.ApplyChainHopPayload{
			ChainID: chainID,
			HopID:   down.ID,
			Kind:    shared.HopKindBridge,
			Bridge:  spec,
		})
		return
	}

	// 阶段 4：forward（出口→入口方向：中间跳先，入口最后——客户端永不见到半成品入口）。
	// 目标：反向链 → 127.0.0.1:下一跳监听端口 + via_tunnel_domain；
	// 直连 → 下一跳 server.address:公网端口（出口跳 = 出口业务 realized 端口）。
	for i := len(hops) - 2; i >= 0; i-- {
		hop := hops[i]
		if hop.ForwardPort != 0 && pieces[pieceKey(hop.ID, shared.HopKindForward)] == store.CommandStatusAcked {
			continue
		}
		if hop.Status == store.HopStatusFailed || pieceInFlight(pieces, hop.ID, shared.HopKindForward) {
			return
		}
		spec := &shared.ForwardSpec{
			Tag:  shared.ChainForwardTag(hop.ID),
			Port: hop.ForwardPort, // 0 = 自动（用户未指定的入口/中间跳）
		}
		if spec.Port == 0 {
			spec.PortCandidates = listenCandidatesOf(servers[hop.ServerID])
		}
		reverse := hop.TunnelUUID != "" // 本跳 → 下一跳为反向链
		next := hops[i+1]
		if i == len(hops)-2 {
			// 下一跳为出口：目标 = 出口业务 realized 端口。
			if reverse {
				spec.TargetAddress = "127.0.0.1"
				spec.TargetPort = rc.Port
				spec.ViaTunnelDomain = shared.TunnelDomain(chainID, hop.ID)
			} else {
				spec.TargetAddress = servers[next.ServerID].Address
				spec.TargetPort = publicPortOf(servers[next.ServerID], rc.Port)
			}
		} else {
			// 下一跳为中间跳：目标 = 其 forward 端口（反向取回环监听侧，直连取公网侧）。
			if reverse {
				spec.TargetAddress = "127.0.0.1"
				spec.TargetPort = next.ForwardPort
				spec.ViaTunnelDomain = shared.TunnelDomain(chainID, hop.ID)
			} else {
				spec.TargetAddress = servers[next.ServerID].Address
				spec.TargetPort = publicPortOf(servers[next.ServerID], next.ForwardPort)
			}
		}
		d.enqueueHop(ctx, chainID, hop, shared.HopKindForward, shared.ApplyChainHopPayload{
			ChainID: chainID,
			HopID:   hop.ID,
			Kind:    shared.HopKindForward,
			Forward: spec,
		})
		return
	}

	// 阶段 5：全部 piece 完成 → 跳 active → 链 active（随后按在线性推导是否 degraded）。
	for i := range hops {
		if hops[i].Status != store.HopStatusActive {
			if err := d.st.SetChainHopStatus(ctx, hops[i].ID, store.HopStatusActive, ""); err != nil {
				log.Printf("dispatch: chain %d hop %d active: %v", chainID, hops[i].ID, err)
				return
			}
		}
	}
	if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
		log.Printf("dispatch: chain %d active: %v", chainID, err)
		return
	}
	log.Printf("dispatch: chain %d active（%d 跳）", chainID, len(hops))
	d.recomputeChain(ctx, chainID)
}

// enqueueHop 跳进入 applying 并下发 apply_chain_hop（离线留 commands 队列补发，§2）。
func (d *Dispatcher) enqueueHop(ctx context.Context, chainID int64, hop store.ChainHop, kind string, payload shared.ApplyChainHopPayload) {
	if err := d.st.SetChainHopStatus(ctx, hop.ID, store.HopStatusApplying, ""); err != nil {
		log.Printf("dispatch: chain %d hop %d applying: %v", chainID, hop.ID, err)
		return
	}
	if _, err := d.Enqueue(ctx, hop.ServerID, shared.TypeApplyChainHop, payload); err != nil {
		log.Printf("dispatch: chain %d hop %d enqueue %s: %v", chainID, hop.ID, kind, err)
	}
}

// handleChainHopResult 处理链跳配置件回执（§21.1，apply_result 带 hop_id 时由 handleApplyResult 路由至此）：
// portal/forward 回执的 realized port/public_key 写回 chain_hops 并推进编排；
// 失败定位到跳并置链 failed。remove_chain_hop 的回执到达时跳行已删（删链流程），仅记日志。
func (d *Dispatcher) handleChainHopResult(serverID int64, p shared.ApplyResultPayload) {
	ctx := context.Background()
	hop, err := d.st.ChainHopByID(ctx, p.HopID)
	if err != nil {
		log.Printf("dispatch: server %d: apply_result hop %d 不存在（可能为删链回执）: %v", serverID, p.HopID, err)
		return
	}
	if hop.ServerID != serverID {
		log.Printf("dispatch: server %d: apply_result hop %d 属于 server %d，忽略", serverID, p.HopID, hop.ServerID)
		return
	}
	if !p.OK {
		d.failHop(ctx, hop, fmt.Sprintf("%s: %s", p.Kind, p.Error))
		return
	}
	switch p.Kind {
	case shared.HopKindPortal:
		if p.RealizedConfig == nil || p.RealizedConfig.Port == 0 || p.RealizedConfig.PublicKey == "" {
			d.failHop(ctx, hop, "portal 回执缺少 realized port/public_key")
			return
		}
		if err := d.st.SetChainHopPortalRealized(ctx, hop.ID, p.RealizedConfig.Port, p.RealizedConfig.PublicKey, p.RealizedConfig.ServerName); err != nil {
			log.Printf("dispatch: hop %d portal realized: %v", hop.ID, err)
			return
		}
	case shared.HopKindForward:
		if p.RealizedConfig == nil || p.RealizedConfig.Port == 0 {
			d.failHop(ctx, hop, "forward 回执缺少 realized port")
			return
		}
		if err := d.st.SetChainHopForwardPort(ctx, hop.ID, p.RealizedConfig.Port); err != nil {
			log.Printf("dispatch: hop %d forward realized: %v", hop.ID, err)
			return
		}
	case shared.HopKindBridge:
		// 无回执字段：commands 表 acked 即完成记录。
	default:
		log.Printf("dispatch: server %d: apply_result hop %d 未知 kind %q，忽略", serverID, p.HopID, p.Kind)
		return
	}
	log.Printf("dispatch: server %d: chain %d hop %d %s acked", serverID, hop.ChainID, hop.ID, p.Kind)
	d.advanceChain(ctx, hop.ChainID)
}

// failHop 置跳 failed 并定位链 failed（回执失败与回执缺字段两条路径共用）。
func (d *Dispatcher) failHop(ctx context.Context, hop *store.ChainHop, errMsg string) {
	if err := d.st.SetChainHopStatus(ctx, hop.ID, store.HopStatusFailed, errMsg); err != nil {
		log.Printf("dispatch: hop %d failed: %v", hop.ID, err)
	}
	d.failChain(ctx, hop.ChainID, hop, errMsg)
}

// failChain 置链 failed 并把错误定位到跳（§21：跨机部分失败 → 链 failed 定位到跳）。
func (d *Dispatcher) failChain(ctx context.Context, chainID int64, hop *store.ChainHop, errMsg string) {
	locate := fmt.Sprintf("跳 %d（%s，server %d）: %s", hop.ID, hop.Role, hop.ServerID, errMsg)
	if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusFailed, locate); err != nil {
		log.Printf("dispatch: chain %d failed: %v", chainID, err)
	}
	log.Printf("dispatch: chain %d failed: %s", chainID, locate)
	d.recordOperation(logging.OperationEvent{
		Severity: logging.SeverityError, Category: logging.CategoryChain, Action: "chain.failed",
		ServerID: &hop.ServerID, Detail: map[string]any{"chain_id": chainID, "hop_id": hop.ID, "error": locate},
	})
}

// advanceChainByNode 出口业务节点 apply 成功后推进所属链编排（阶段 2 起）；非链出口节点直接返回。
func (d *Dispatcher) advanceChainByNode(ctx context.Context, nodeID int64) {
	hop, err := d.st.ChainHopByNodeID(ctx, nodeID)
	if err != nil {
		return
	}
	d.advanceChain(ctx, hop.ChainID)
}

// failChainByNode 出口业务节点失败/死信时定位所属链 failed（仅编排中的链；
// 已 active 链的节点独立重试失败不翻转链状态）。
func (d *Dispatcher) failChainByNode(ctx context.Context, nodeID int64, reason string) {
	hop, err := d.st.ChainHopByNodeID(ctx, nodeID)
	if err != nil {
		return
	}
	chain, err := d.st.ChainByID(ctx, hop.ChainID)
	if err != nil || (chain.Status != store.ChainStatusApplying && chain.Status != store.ChainStatusPending) {
		return
	}
	if err := d.st.SetChainHopStatus(ctx, hop.ID, store.HopStatusFailed, reason); err != nil {
		log.Printf("dispatch: hop %d failed: %v", hop.ID, err)
	}
	d.failChain(ctx, hop.ChainID, hop, reason)
}

// RecomputeChainsByServer 重算受影响链的 degraded 状态（§21.1，hub 注册/注销连接时调用）：
// 链任一跳 server 离线 → degraded + chain_degraded 告警（§19 防抖沿用）；
// 全部跳 server 在线且跳均 active → 恢复 active。
func (d *Dispatcher) RecomputeChainsByServer(serverID int64) {
	ctx := context.Background()
	hops, err := d.st.ChainHopsByServerID(ctx, serverID)
	if err != nil {
		log.Printf("dispatch: recompute chains by server %d: %v", serverID, err)
		return
	}
	seen := map[int64]bool{}
	for _, h := range hops {
		if seen[h.ChainID] {
			continue
		}
		seen[h.ChainID] = true
		d.recomputeChain(ctx, h.ChainID)
	}
}

// recomputeChain 推导单条链 active ↔ degraded（pending/applying/failed 不参与）。
func (d *Dispatcher) recomputeChain(ctx context.Context, chainID int64) {
	chain, err := d.st.ChainByID(ctx, chainID)
	if err != nil {
		return
	}
	if chain.Status != store.ChainStatusActive && chain.Status != store.ChainStatusDegraded {
		return
	}
	hops, err := d.st.ChainHops(ctx, chainID)
	if err != nil {
		return
	}
	for i := range hops {
		if !d.req.IsOnline(hops[i].ServerID) {
			if chain.Status == store.ChainStatusActive {
				detail := fmt.Sprintf("跳 %d（%s，server %d）离线", hops[i].ID, hops[i].Role, hops[i].ServerID)
				if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusDegraded, detail); err != nil {
					log.Printf("dispatch: chain %d degraded: %v", chainID, err)
				}
				log.Printf("dispatch: chain %d degraded: %s", chainID, detail)
				d.recordOperation(logging.OperationEvent{
					Severity: logging.SeverityWarning, Category: logging.CategoryChain, Action: "chain.degraded",
					ServerID: &hops[i].ServerID, Detail: map[string]any{"chain_id": chainID, "hop_id": hops[i].ID, "reason": detail},
				})
				if d.Alerter != nil {
					d.Alerter.Notify(hops[i].ServerID, alert.EventChainDegraded, fmt.Sprintf("chain_%d", chainID), detail)
				}
			}
			return
		}
	}
	if chain.Status == store.ChainStatusDegraded {
		for i := range hops {
			if hops[i].Status != store.HopStatusActive {
				return
			}
		}
		if err := d.st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
			log.Printf("dispatch: chain %d 恢复 active: %v", chainID, err)
		}
		log.Printf("dispatch: chain %d 恢复 active", chainID)
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryChain, Action: "chain.recovered",
			Detail: map[string]any{"chain_id": chainID},
		})
	}
}

// ChainHopPieces 返回一个跳的配置件 kind 列表（panel 删链逐跳反向下发 remove_chain_hop 用，§21.1）：
// forward（入口/中间跳）、bridge（反向链下游机：上一跳 tunnel_uuid 非空）、
// portal（反向链上游机：本跳 tunnel_uuid 非空）。返回顺序即拆除顺序。
func ChainHopPieces(hops []store.ChainHop, i int) []string {
	kinds := []string{}
	if hops[i].Role != store.HopRoleExit {
		kinds = append(kinds, shared.HopKindForward)
	}
	if i > 0 && hops[i-1].TunnelUUID != "" {
		kinds = append(kinds, shared.HopKindBridge)
	}
	if hops[i].TunnelUUID != "" {
		kinds = append(kinds, shared.HopKindPortal)
	}
	return kinds
}

// pieceKey 是 piece 进度表的键（"<hopID>|<kind>"）。
func pieceKey(hopID int64, kind string) string { return fmt.Sprintf("%d|%s", hopID, kind) }

// pieceInFlight 报告该 piece 是否有在途命令（queued/sent）。
func pieceInFlight(pieces map[string]string, hopID int64, kind string) bool {
	st := pieces[pieceKey(hopID, kind)]
	return st == store.CommandStatusQueued || st == store.CommandStatusSent
}

// chainPieces 从 commands 表推导该链各 piece 最新命令状态（按 id 升序后者覆盖前者）。
func (d *Dispatcher) chainPieces(ctx context.Context, chainID int64) (map[string]string, error) {
	cmds, err := d.st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, c := range cmds {
		var p shared.ApplyChainHopPayload
		if err := json.Unmarshal(c.Payload, &p); err != nil || p.ChainID != chainID || p.HopID == 0 {
			continue
		}
		m[pieceKey(p.HopID, p.Kind)] = c.Status
	}
	return m, nil
}

// portalDest 返回 portal 的 Reality dest（白名单首位，隧道 inbound 复用 §6 同一白名单，§21 PoC）。
func (d *Dispatcher) portalDest() string {
	if len(d.DestCandidates) > 0 {
		return d.DestCandidates[0]
	}
	return "dl.google.com:443"
}

// tunnelShortID 由 tunnel_uuid 确定性派生 portal 的 short_id（bridge 侧需同一值，
// chain_hops 不另存列；取 sha256 前 8 字节 hex）。
func tunnelShortID(tunnelUUID string) string {
	sum := sha256.Sum256([]byte(tunnelUUID))
	return hex.EncodeToString(sum[:8])
}

// destHost 取 dest（host:port）的 host 部分（portal serverNames / bridge SNI）。
func destHost(dest string) string {
	if h, _, err := net.SplitHostPort(dest); err == nil {
		return h
	}
	return dest
}

// listenCandidatesOf 返回服务器 NAT 端口段的监听侧候选端口（无段/非 NAT 返回 nil）。
func listenCandidatesOf(srv *store.Server) []int {
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil || len(ranges) == 0 {
		return nil
	}
	return shared.ListenCandidates(ranges)
}

// publicPortOf 按服务器端口段把监听端口换算为公网端口（非 1:1 映射，§21）；
// 无段或不在段内时按 1:1 返回监听端口本身。
func publicPortOf(srv *store.Server, listen int) int {
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil || len(ranges) == 0 {
		return listen
	}
	if pub, ok := shared.PublicPort(ranges, listen); ok {
		return pub
	}
	return listen
}
