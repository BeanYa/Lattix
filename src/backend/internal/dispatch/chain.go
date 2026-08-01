package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"

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
	if err := d.fsm.Transition(ctx, chainID, store.ChainStatusApplying, "建链编排启动"); err != nil {
		return err
	}
	d.advanceChain(context.Background(), chainID)
	return nil
}

// ResumeChains reconstructs orchestration progress from persisted revisions,
// tasks, and commands. It closes the crash window between acknowledging one
// piece and enqueueing the next, and resumes cleanup not queued before exit.
func (d *Dispatcher) ResumeChains(ctx context.Context) error {
	chains, err := d.st.ListChains(ctx)
	if err != nil {
		return err
	}
	for _, chain := range chains {
		if chain.PublishedRevisionID != 0 {
			if revision, err := d.st.PublishedChainRevision(ctx, chain.ID); err == nil {
				d.enqueueCleanupTasks(ctx, revision)
			}
		}
		if chain.DesiredRevisionID == 0 {
			continue
		}
		switch chain.Status {
		case store.ChainStatusApplying, store.ChainStatusWaitingForAgent, store.ChainStatusActiveUnconfirmed:
			d.advanceChain(ctx, chain.ID)
		}
	}
	return nil
}

// RetryChain 重试失败链（§21 只重放失败 piece）：失败跳复位 pending，链回到 applying 后继续编排。
// 已完成的 piece（portal 回执列 / acked 命令）不会重发。
func (d *Dispatcher) RetryChain(ctx context.Context, chainID int64) error {
	chain, err := d.st.ChainByID(ctx, chainID)
	if err != nil {
		return err
	}
	if chain.Status != store.ChainStatusFailed && chain.Status != store.ChainStatusActiveFailed {
		return fmt.Errorf("仅 failed 状态的链可重试（当前 %s）", chain.Status)
	}
	if revision, err := d.st.DesiredChainRevision(ctx, chainID); err == nil {
		if err := d.st.ResetFailedRevisionTasks(ctx, revision.ID); err != nil {
			return err
		}
		if err := d.st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusApplying, ""); err != nil {
			return err
		}
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
	if err := d.fsm.Transition(ctx, chainID, store.ChainStatusApplying, "重试编排"); err != nil {
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
	if chain.Status != store.ChainStatusApplying && chain.Status != store.ChainStatusActiveUnconfirmed {
		return // 仅编排中的链可推进（failed 等重试，active/degraded 已完成编排）
	}
	hops, err := d.st.ChainHops(ctx, chainID)
	if err != nil || len(hops) < 1 {
		log.Printf("dispatch: chain %d hops: %v", chainID, err)
		return
	}
	revisionID := int64(0)
	endpointID := chain.EndpointID
	if revision, revisionErr := d.st.DesiredChainRevision(ctx, chainID); revisionErr == nil {
		revisionID = revision.ID
		endpointID = revision.Snapshot.EndpointID
	}
	pieces, err := d.chainPieces(ctx, chainID, revisionID)
	if err != nil {
		log.Printf("dispatch: chain %d pieces: %v", chainID, err)
		return
	}
	if revisionID != 0 {
		if revision, revisionErr := d.st.ChainRevisionByID(ctx, revisionID); revisionErr == nil {
			applyKeys := map[string]bool{}
			for _, key := range revision.Snapshot.ApplyKeys {
				applyKeys[key] = true
			}
			for _, hop := range revision.Snapshot.Hops {
				for _, kind := range []string{RevisionPieceForward, RevisionPiecePortal, RevisionPieceBridge} {
					key := revisionPieceKey(kind, hop.HopID)
					if !applyKeys[key] {
						pieces[key] = store.CommandStatusAcked
					}
				}
			}
		}
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
	if endpointID != 0 && len(hops) == 1 {
		if hops[0].Status != store.HopStatusActive {
			if err := d.st.SetChainHopStatus(ctx, hops[0].ID, store.HopStatusActive, ""); err != nil {
				return
			}
		}
		if err := d.fsm.Transition(ctx, chainID, store.ChainStatusActive, "单跳共享端点编排完成"); err != nil {
			return
		}
		d.publishDesiredRevision(ctx, chainID, hops, *node)
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
			RevisionID:     revisionID,
			NodeID:         node.ID,
			Config:         vc,
			UserUUIDs:      uuids,
			DestCandidates: d.DestCandidates,
		}
		// NAT 受限直连机总是携带监听侧候选（自动挑选 + 手动端口段内校验，§21）。
		payload.PortCandidates = listenCandidatesOf(servers[exit.ServerID])
		if err := d.st.SetNodeApplying(ctx, node.ID); err != nil {
			log.Printf("dispatch: chain %d node %d applying: %v", chainID, node.ID, err)
			return
		}
		var commandID int64
		if revisionID != 0 {
			commandID, err = d.enqueueRevisionTask(ctx, exit.ServerID, shared.TypeApplyNode, payload,
				revisionID, revisionPieceKey(RevisionPieceService, node.ID))
		} else {
			commandID, err = d.Enqueue(ctx, exit.ServerID, shared.TypeApplyNode, payload)
		}
		if err != nil {
			log.Printf("dispatch: chain %d enqueue apply_node: %v", chainID, err)
		}
		_ = commandID
		return
	case store.NodeStatusApplying:
		return // 等待回执
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(node.RealizedConfig, &rc); err != nil || rc.Port == 0 {
		log.Printf("dispatch: chain %d: 出口节点 %d realized_config 不可用: %v", chainID, node.ID, err)
		return
	}
	if len(hops) == 1 {
		if hops[0].ForwardPort != rc.Port {
			if err := d.st.SetChainHopForwardPort(ctx, hops[0].ID, rc.Port); err != nil {
				log.Printf("dispatch: chain %d single-hop port: %v", chainID, err)
				return
			}
			hops[0].ForwardPort = rc.Port
		}
		if hops[0].Status != store.HopStatusActive {
			if err := d.st.SetChainHopStatus(ctx, hops[0].ID, store.HopStatusActive, ""); err != nil {
				return
			}
		}
		if err := d.fsm.Transition(ctx, chainID, store.ChainStatusActive, "单跳编排完成"); err != nil {
			log.Printf("dispatch: chain %d active: %v", chainID, err)
		}
		d.publishDesiredRevision(ctx, chainID, hops, *node)
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
		d.enqueueHop(ctx, chainID, revisionID, up, shared.HopKindPortal, shared.ApplyChainHopPayload{
			ChainID:        chainID,
			RevisionID:     revisionID,
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
		d.enqueueHop(ctx, chainID, revisionID, down, shared.HopKindBridge, shared.ApplyChainHopPayload{
			ChainID:    chainID,
			RevisionID: revisionID,
			HopID:      down.ID,
			Kind:       shared.HopKindBridge,
			Bridge:     spec,
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
		if i == 0 && endpointID != 0 {
			spec.LocalOnly = true
		}
		// NAT 受限直连机非回环监听（手动/自动端口）总是携带监听侧候选（§21）：
		// 自动 → Agent 段内挑空闲；手动 → Agent 校验段内归属（面板已校验，双保险）。
		if !spec.LocalOnly {
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
		d.enqueueHop(ctx, chainID, revisionID, hop, shared.HopKindForward, shared.ApplyChainHopPayload{
			ChainID:    chainID,
			RevisionID: revisionID,
			HopID:      hop.ID,
			Kind:       shared.HopKindForward,
			Forward:    spec,
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
	if err := d.fsm.Transition(ctx, chainID, store.ChainStatusActive, "编排完成"); err != nil {
		log.Printf("dispatch: chain %d active: %v", chainID, err)
		return
	}
	log.Printf("dispatch: chain %d active（%d 跳）", chainID, len(hops))
	d.publishDesiredRevision(ctx, chainID, hops, *node)
	d.recomputeChain(ctx, chainID)
}

func (d *Dispatcher) publishDesiredRevision(ctx context.Context, chainID int64, hops []store.ChainHop, node store.Node) {
	revision, err := d.st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		return
	}
	revision.Snapshot.ServiceRealized = append(json.RawMessage(nil), node.RealizedConfig...)
	byID := make(map[int64]store.ChainHop, len(hops))
	for _, hop := range hops {
		byID[hop.ID] = hop
	}
	for i := range revision.Snapshot.Hops {
		if hop, ok := byID[revision.Snapshot.Hops[i].HopID]; ok {
			revision.Snapshot.Hops[i].ForwardPort = hop.ForwardPort
			revision.Snapshot.Hops[i].PortalPort = hop.PortalPort
			revision.Snapshot.Hops[i].PortalPublicKey = hop.PortalPublicKey
			revision.Snapshot.Hops[i].PortalServerName = hop.PortalServerName
		}
	}
	if err := d.st.UpdateChainRevision(ctx, revision.ID, store.RevisionStatusActive, "", revision.Snapshot); err != nil {
		log.Printf("dispatch: chain %d revision snapshot: %v", chainID, err)
		return
	}
	previous, _ := d.st.PublishedChainRevision(ctx, chainID)
	if err := d.st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		log.Printf("dispatch: chain %d publish revision: %v", chainID, err)
		return
	}
	if d.OnChainPublished != nil {
		if err := d.OnChainPublished(ctx, chainID); err != nil {
			log.Printf("dispatch: enqueue subscriptions for chain %d: %v", chainID, err)
		}
	}
	d.cleanupPublishedRevision(ctx, previous, revision)
	if previous != nil && previous.Snapshot.EndpointID != 0 &&
		previous.Snapshot.EndpointID != revision.Snapshot.EndpointID {
		if err := d.ReconcileSharedEndpoint(ctx, previous.Snapshot.EndpointID); err != nil {
			log.Printf("dispatch: chain %d previous shared endpoint %d: %v",
				chainID, previous.Snapshot.EndpointID, err)
		}
	}
	if revision.Snapshot.EndpointID != 0 {
		if err := d.ReconcileSharedEndpoint(ctx, revision.Snapshot.EndpointID); err != nil {
			log.Printf("dispatch: chain %d shared endpoint %d: %v", chainID, revision.Snapshot.EndpointID, err)
		}
		// 端点刚置 applying，立即重算链状态（若端点未 active 则链进入 degraded，待 ack 后恢复）。
		d.recomputeChain(ctx, chainID)
	}
}

func (d *Dispatcher) ForcePublishRevision(ctx context.Context, chainID int64) error {
	revision, err := d.st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		return err
	}
	if target := strings.TrimSpace(d.PanelVersion); target != "" && target != "dev" {
		tasks, err := d.st.RevisionTasks(ctx, revision.ID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.Phase != "apply" || task.Status == store.RevisionTaskAcked {
				continue
			}
			server, err := d.st.ServerByID(ctx, task.ServerID)
			if err != nil {
				return fmt.Errorf("强制发布失败：任务服务器 %d 不存在", task.ServerID)
			}
			if server.AgentVersion != target {
				return fmt.Errorf("强制发布失败：服务器 %s 的 Agent 版本为 %q，须先同步到 Panel 版本 %s",
					server.Alias, server.AgentVersion, target)
			}
		}
	}
	previous, _ := d.st.PublishedChainRevision(ctx, chainID)
	directShared := revision.Snapshot.EndpointID != 0 && len(revision.Snapshot.Hops) == 1
	if len(revision.Snapshot.ServiceRealized) == 0 && !directShared {
		var source json.RawMessage
		if node, err := d.st.NodeByID(ctx, revision.Snapshot.ServiceNodeID); err == nil && len(node.RealizedConfig) > 0 {
			source = node.RealizedConfig
		} else if previous != nil {
			source = previous.Snapshot.ServiceRealized
		}
		if len(source) == 0 {
			return fmt.Errorf("强制发布失败：出口 Agent 尚未产生可用于订阅的生效参数")
		}
		realized, err := forcedServiceRealized(revision.Snapshot.ServiceConfig, source)
		if err != nil {
			return err
		}
		revision.Snapshot.ServiceRealized = realized
	}
	if hops, err := d.st.ChainHops(ctx, chainID); err == nil {
		byID := make(map[int64]store.ChainHop, len(hops))
		for _, hop := range hops {
			byID[hop.ID] = hop
		}
		for i := range revision.Snapshot.Hops {
			if hop, ok := byID[revision.Snapshot.Hops[i].HopID]; ok {
				if hop.ForwardPort != 0 {
					revision.Snapshot.Hops[i].ForwardPort = hop.ForwardPort
				}
				revision.Snapshot.Hops[i].PortalPort = hop.PortalPort
				revision.Snapshot.Hops[i].PortalPublicKey = hop.PortalPublicKey
				revision.Snapshot.Hops[i].PortalServerName = hop.PortalServerName
			}
		}
	}
	if len(revision.Snapshot.Hops) == 1 && revision.Snapshot.Hops[0].ForwardPort == 0 {
		var realized shared.RealizedConfig
		if json.Unmarshal(revision.Snapshot.ServiceRealized, &realized) == nil {
			revision.Snapshot.Hops[0].ForwardPort = realized.Port
		}
	}
	if len(revision.Snapshot.Hops) == 0 || revision.Snapshot.Hops[0].ForwardPort == 0 && !directShared {
		return fmt.Errorf("强制发布失败：入口 Agent 尚未确认可用于订阅的监听端口；请指定固定入口端口或等待 Agent 在线")
	}
	if err := d.st.UpdateChainRevision(ctx, revision.ID, store.RevisionStatusActiveUnconfirmed, "", revision.Snapshot); err != nil {
		return err
	}
	if err := d.st.PublishChainRevision(ctx, revision.ID, true); err != nil {
		return err
	}
	if d.OnChainPublished != nil {
		if err := d.OnChainPublished(ctx, chainID); err != nil {
			log.Printf("dispatch: enqueue subscriptions for chain %d: %v", chainID, err)
		}
	}
	d.cleanupPublishedRevision(ctx, previous, revision)
	if previous != nil && previous.Snapshot.EndpointID != 0 &&
		previous.Snapshot.EndpointID != revision.Snapshot.EndpointID {
		if err := d.ReconcileSharedEndpoint(ctx, previous.Snapshot.EndpointID); err != nil {
			return err
		}
	}
	if revision.Snapshot.EndpointID != 0 {
		if err := d.ReconcileSharedEndpoint(ctx, revision.Snapshot.EndpointID); err != nil {
			return err
		}
	}
	d.advanceChain(context.Background(), chainID)
	return nil
}

func forcedServiceRealized(serviceConfig, previousRealized json.RawMessage) (json.RawMessage, error) {
	var virtual shared.VirtualConfig
	if err := json.Unmarshal(serviceConfig, &virtual); err != nil {
		return nil, fmt.Errorf("强制发布失败：出口协议配置损坏")
	}
	var realized shared.RealizedConfig
	if err := json.Unmarshal(previousRealized, &realized); err != nil || realized.Port == 0 {
		return nil, fmt.Errorf("强制发布失败：没有可复用的出口生效端口")
	}
	if virtual.Port != 0 {
		realized.Port = virtual.Port
	}
	realized.Flow = virtual.Flow
	realized.Network = virtual.Network
	realized.ServiceName = virtual.ServiceName
	realized.Path = virtual.Path
	realized.Mode = virtual.Mode
	realized.Host = virtual.Host
	realized.Method = virtual.Method
	realized.Fingerprint = virtual.Fingerprint
	if shared.IsRealityProtocol(virtual.Protocol) && realized.PublicKey == "" {
		return nil, fmt.Errorf("强制发布失败：新 Reality 配置尚无 Agent 生成的 public_key，请等待出口 Agent 在线")
	}
	if virtual.Encryption != "" && realized.Encryption == "" {
		return nil, fmt.Errorf("强制发布失败：VLESS Encryption 尚无 Agent 生成的客户端参数，请等待出口 Agent 在线")
	}
	if virtual.Protocol == shared.ProtocolShadowsocks && shared.Is2022Method(virtual.Method) && realized.PSK == "" {
		return nil, fmt.Errorf("强制发布失败：Shadowsocks 2022 尚无 Agent 生成的 PSK，请等待出口 Agent 在线")
	}
	raw, err := json.Marshal(realized)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (d *Dispatcher) cleanupPublishedRevision(ctx context.Context, previous, current *store.ChainRevision) {
	if previous == nil || current == nil || previous.ID == current.ID {
		return
	}
	d.enqueueCleanupTasks(ctx, current)
}

func (d *Dispatcher) enqueueCleanupTasks(ctx context.Context, current *store.ChainRevision) {
	tasks, err := d.st.RevisionTasks(ctx, current.ID)
	if err != nil {
		log.Printf("dispatch: revision %d cleanup tasks: %v", current.ID, err)
		return
	}
	for _, task := range tasks {
		if task.Phase != "cleanup" || task.Status != store.RevisionTaskPending {
			continue
		}
		var commandID int64
		if task.Kind == RevisionPieceService {
			commandID, err = d.enqueueRevisionTask(ctx, task.ServerID, shared.TypeRemoveNode,
				shared.RemoveNodePayload{NodeID: task.HopID}, current.ID, task.TaskKey)
		} else {
			commandID, err = d.enqueueRevisionTask(ctx, task.ServerID, shared.TypeRemoveChainHop,
				shared.RemoveChainHopPayload{HopID: task.HopID, Kind: task.Kind}, current.ID, task.TaskKey)
		}
		if err != nil {
			log.Printf("dispatch: revision %d enqueue cleanup %s: %v", current.ID, task.TaskKey, err)
			continue
		}
		_ = commandID
	}
	d.refreshCleanupStatus(ctx, current.ID)
}

func (d *Dispatcher) refreshCleanupStatus(ctx context.Context, revisionID int64) {
	revision, err := d.st.ChainRevisionByID(ctx, revisionID)
	if err != nil {
		return
	}
	tasks, err := d.st.RevisionTasks(ctx, revisionID)
	if err != nil {
		return
	}
	hasCleanup, complete := false, true
	for _, task := range tasks {
		if task.Phase != "cleanup" {
			continue
		}
		hasCleanup = true
		if task.Status != store.RevisionTaskAcked && task.Status != store.RevisionTaskAbandoned {
			complete = false
		}
	}
	if !hasCleanup {
		return
	}
	chain, err := d.st.ChainByID(ctx, revision.ChainID)
	if err != nil || chain.DesiredRevisionID != 0 {
		return
	}
	if !complete {
		if chain.Status == store.ChainStatusActive || chain.Status == store.ChainStatusDegraded {
			_ = d.fsm.Transition(ctx, chain.ID, store.ChainStatusCleanupPending, "旧 revision 配置等待清理")
		}
		return
	}
	if chain.Status == store.ChainStatusCleanupPending {
		_ = d.fsm.Transition(ctx, chain.ID, store.ChainStatusActive, "清理完成")
		d.recomputeChain(ctx, chain.ID)
	}
}

// enqueueHop 跳进入 applying 并下发 apply_chain_hop（离线留 commands 队列补发，§2）。
func (d *Dispatcher) enqueueHop(ctx context.Context, chainID, revisionID int64, hop store.ChainHop, kind string, payload shared.ApplyChainHopPayload) {
	if err := d.st.SetChainHopStatus(ctx, hop.ID, store.HopStatusApplying, ""); err != nil {
		log.Printf("dispatch: chain %d hop %d applying: %v", chainID, hop.ID, err)
		return
	}
	var err error
	if revisionID != 0 {
		_, err = d.enqueueRevisionTask(ctx, hop.ServerID, shared.TypeApplyChainHop, payload,
			revisionID, revisionPieceKey(kind, hop.ID))
	} else {
		_, err = d.Enqueue(ctx, hop.ServerID, shared.TypeApplyChainHop, payload)
	}
	if err != nil {
		log.Printf("dispatch: chain %d hop %d enqueue %s: %v", chainID, hop.ID, kind, err)
	}
}

// handleChainHopResult 处理链跳配置件回执（§21.1，apply_result 带 hop_id 时由 handleApplyResult 路由至此）：
// portal/forward 回执的 realized port/public_key 写回 chain_hops 并推进编排；
// 失败定位到跳并置链 failed。remove_chain_hop 的回执到达时跳行已删（删链流程），仅记日志。
func (d *Dispatcher) handleChainHopResult(serverID int64, p shared.ApplyResultPayload, responseError string) {
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
	if responseError != "" {
		d.failHop(ctx, hop, fmt.Sprintf("%s: %s", p.Kind, responseError))
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
	status := store.ChainStatusFailed
	if chain, err := d.st.ChainByID(ctx, chainID); err == nil && chain.PublishedRevisionID != 0 {
		status = store.ChainStatusActiveFailed
	}
	if revision, err := d.st.DesiredChainRevision(ctx, chainID); err == nil {
		revisionStatus := store.RevisionStatusFailed
		if revision.Forced {
			revisionStatus = store.RevisionStatusActiveFailed
		}
		_ = d.st.SetChainRevisionStatus(ctx, revision.ID, revisionStatus, locate)
	}
	if err := d.fsm.Transition(ctx, chainID, status, locate); err != nil {
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
	if err != nil || (chain.Status != store.ChainStatusApplying && chain.Status != store.ChainStatusPending &&
		chain.Status != store.ChainStatusActiveUnconfirmed) {
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
		d.fsm.Evaluate(ctx, h.ChainID)
	}
}

// recomputeChain 委托给链路状态机的条件评估（保留方法签名兼容内部调用点）。
func (d *Dispatcher) recomputeChain(ctx context.Context, chainID int64) {
	d.fsm.Evaluate(ctx, chainID)
}

// ChainHopPieces 返回一个跳的配置件 kind 列表（panel 删链逐跳反向下发 remove_chain_hop 用，§21.1），
// 实现下沉 store.ChainHopPieces（与 xray.cleanup 期望集合计算同源）。
func ChainHopPieces(hops []store.ChainHop, i int) []string {
	return store.ChainHopPieces(hops, i)
}

// pieceKey 是 piece 进度表的键（"<hopID>|<kind>"）。
func pieceKey(hopID int64, kind string) string { return fmt.Sprintf("%d|%s", hopID, kind) }

// pieceInFlight 报告该 piece 是否有在途命令（queued/sent）。
func pieceInFlight(pieces map[string]string, hopID int64, kind string) bool {
	st := pieces[pieceKey(hopID, kind)]
	return st == store.CommandStatusQueued || st == store.CommandStatusSent
}

// chainPieces 从 commands 表推导该链各 piece 最新命令状态（按 id 升序后者覆盖前者）。
func (d *Dispatcher) chainPieces(ctx context.Context, chainID, revisionID int64) (map[string]string, error) {
	cmds, err := d.st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, c := range cmds {
		var p shared.ApplyChainHopPayload
		if err := json.Unmarshal(c.Data, &p); err != nil || p.ChainID != chainID || p.HopID == 0 || p.RevisionID != revisionID {
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
