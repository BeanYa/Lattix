package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type serverExchangeFailureDTO struct {
	ChainID int64  `json:"chain_id"`
	Name    string `json:"name"`
	Error   string `json:"error"`
}

type serverExchangeDTO struct {
	Total        int                        `json:"total"`
	Exchanged    int                        `json:"exchanged"`
	Waiting      int                        `json:"waiting"`
	Failed       int                        `json:"failed"`
	FailedChains []serverExchangeFailureDTO `json:"failed_chains"`
}

// tryExchangeChainsForServer 在服务器类型或 NAT 可用端口段发生变化后，对所有受影响的链路
// 执行自动转换（TryExchange）：可平滑重编排的链路自动切换端口/反向隧道并发布新修订；
// 无法自动适配的链路通过链路状态机标记为 exchange_failed（「转换失败」），由管理员在链路页处理。
func (s *Server) tryExchangeChainsForServer(ctx context.Context, before, after *store.Server) serverExchangeDTO {
	summary := serverExchangeDTO{FailedChains: []serverExchangeFailureDTO{}}
	if s.disp == nil || after == nil {
		return summary
	}
	affected, err := s.st.ChainsReferencingServer(ctx, after.ID)
	if err != nil {
		log.Printf("panel: try-exchange list referencing chains for server %d: %v", after.ID, err)
		return summary
	}
	working, err := s.st.ChainsWithWorkingHopOnServer(ctx, after.ID)
	if err != nil {
		log.Printf("panel: try-exchange list working chains for server %d: %v", after.ID, err)
		return summary
	}
	byID := make(map[int64]store.Chain, len(affected)+len(working))
	for _, c := range affected {
		if c.Status == store.ChainStatusDeleted || c.Status == store.ChainStatusInvalid {
			continue
		}
		byID[c.ID] = c
	}
	for _, c := range working {
		if c.Status == store.ChainStatusDeleted || c.Status == store.ChainStatusInvalid {
			continue
		}
		byID[c.ID] = c
	}
	chainIDs := make([]int64, 0, len(byID))
	for id := range byID {
		chainIDs = append(chainIDs, id)
	}
	sort.Slice(chainIDs, func(i, j int) bool { return chainIDs[i] < chainIDs[j] })

	graphRefs, err := s.st.ChainsWithGraphNodeOnServer(ctx, after.ID)
	if err != nil {
		log.Printf("panel: try-exchange list hop graph refs for server %d: %v", after.ID, err)
	}
	pausedIDs := make(map[int64]bool, len(chainIDs)+len(graphRefs))
	if s.orchestrator != nil {
		for _, cid := range chainIDs {
			pausedIDs[cid] = true
			s.orchestrator.PauseChain(cid)
			_ = s.orchestrator.WaitChainSettled(ctx, cid, 2*time.Second)
		}
		for _, ref := range graphRefs {
			if !pausedIDs[ref.ChainID] {
				pausedIDs[ref.ChainID] = true
				s.orchestrator.PauseChain(ref.ChainID)
				_ = s.orchestrator.WaitChainSettled(ctx, ref.ChainID, 2*time.Second)
			}
		}
	}

	failedChainSet := make(map[int64]bool)
	for _, cid := range chainIDs {
		chain := byID[cid]
		outcome, reason := s.tryExchangeSingleChain(ctx, chain, before, after)
		switch outcome {
		case "skipped":
			continue
		case "exchanged":
			summary.Total++
			summary.Exchanged++
		case "waiting":
			summary.Total++
			summary.Waiting++
		case "failed":
			summary.Total++
			summary.Failed++
			failedChainSet[cid] = true
			if markErr := s.disp.ChainFSM().MarkExchangeFailed(ctx, cid, after.ID, reason); markErr != nil {
				log.Printf("panel: try-exchange mark chain %d exchange_failed: %v", cid, markErr)
			}
			summary.FailedChains = append(summary.FailedChains, serverExchangeFailureDTO{
				ChainID: cid,
				Name:    chain.Name,
				Error:   reason,
			})
		}
	}

	s.reconcileHopGraphsForServerExchange(ctx, after, graphRefs, failedChainSet)

	if s.orchestrator != nil {
		for cid := range pausedIDs {
			if failedChainSet[cid] {
				s.orchestrator.StopChain(cid)
			} else {
				s.orchestrator.NotifyChainUpdated(cid)
				s.orchestrator.ResumeChain(cid)
			}
		}
	}
	return summary
}

func (s *Server) tryExchangeSingleChain(ctx context.Context, chain store.Chain, before, after *store.Server) (string, string) {
	if chain.Status == store.ChainStatusDeleted || chain.Status == store.ChainStatusInvalid {
		return "skipped", ""
	}
	failed := chain.Status == store.ChainStatusFailed ||
		chain.Status == store.ChainStatusActiveFailed ||
		chain.Status == store.ChainStatusExchangeFailed
	waitingForAgent := chain.Status == store.ChainStatusWaitingForAgent
	if chain.DesiredRevisionID != 0 && !failed && !waitingForAgent {
		return "failed", "服务器类型变更时链路正在部署中，无法自动转换，请待部署完成后手动编辑或删除"
	}

	var waitingRevisionID int64
	if waitingForAgent {
		waitingRevisionID = chain.DesiredRevisionID
	}
	current, err := s.st.PublishedChainRevision(ctx, chain.ID)
	var failedRevision *store.ChainRevision
	if chain.Status == store.ChainStatusFailed || chain.Status == store.ChainStatusActiveFailed {
		if revision, desiredErr := s.st.DesiredChainRevision(ctx, chain.ID); desiredErr == nil {
			failedRevision = revision
			current = revision
		}
	}
	if current == nil && waitingForAgent {
		if revision, desiredErr := s.st.DesiredChainRevision(ctx, chain.ID); desiredErr == nil {
			current = revision
		}
	}
	if current == nil && chain.Status == store.ChainStatusExchangeFailed {
		if revision, latestErr := s.st.LatestChainRevision(ctx, chain.ID); latestErr == nil {
			current = revision
		}
	}
	if current == nil {
		return "failed", "链路尚无可用的配置快照，请手动编辑或删除该链路"
	}
	if hops, hopErr := s.st.ChainHops(ctx, chain.ID); hopErr == nil && len(hops) > 0 {
		byID := make(map[int64]store.ChainHop, len(hops))
		for _, hop := range hops {
			byID[hop.ID] = hop
		}
		for i := range current.Snapshot.Hops {
			if hop, ok := byID[current.Snapshot.Hops[i].HopID]; ok {
				if hop.ForwardPort != 0 {
					current.Snapshot.Hops[i].ForwardPort = hop.ForwardPort
				}
				if hop.PortalPort != 0 {
					current.Snapshot.Hops[i].PortalPort = hop.PortalPort
				}
				if hop.PortalPublicKey != "" {
					current.Snapshot.Hops[i].PortalPublicKey = hop.PortalPublicKey
				}
				if hop.PortalServerName != "" {
					current.Snapshot.Hops[i].PortalServerName = hop.PortalServerName
				}
			}
		}
	}

	srvIdx := -1
	for i, hop := range current.Snapshot.Hops {
		if hop.ServerID == after.ID {
			srvIdx = i
			break
		}
	}
	if srvIdx < 0 {
		return "skipped", ""
	}

	hopCount := len(current.Snapshot.Hops)
	servers := make([]*store.Server, 0, hopCount)
	for i, hop := range current.Snapshot.Hops {
		if i == srvIdx {
			servers = append(servers, after)
			continue
		}
		srv, loadErr := s.st.ServerByID(ctx, hop.ServerID)
		if loadErr != nil {
			return "failed", fmt.Sprintf("链路关联服务器 #%d 不存在，请手动编辑或删除该链路", hop.ServerID)
		}
		servers = append(servers, srv)
	}

	snapshotExitIsExternal := current.Snapshot.ServiceExternal != nil
	if after.IsExternal() {
		if hopCount == 1 || srvIdx < hopCount-1 {
			return "failed", fmt.Sprintf("服务器 %s 已转为外部直链节点，无法继续担任%s，请编辑或删除该链路",
				after.Alias, hopRoleLabel(hopCount, srvIdx))
		}
		if !snapshotExitIsExternal {
			return "failed", fmt.Sprintf("服务器 %s 已转为外部直链节点，既有托管出口链路无法自动切换为外部出口，请编辑或删除该链路",
				after.Alias)
		}
	} else if srvIdx == hopCount-1 && snapshotExitIsExternal {
		return "failed", fmt.Sprintf("服务器 %s 已转为托管服务器，既有外部直链出口链路无法自动切换为托管出口，请编辑或删除该链路",
			after.Alias)
	}

	if hopCount == 1 && !inboundCapable(after) {
		return "failed", fmt.Sprintf("服务器 %s 已转为仅出口 NAT（无可用端口），无法继续担任直连入口，请编辑调整链路或补充可用端口",
			after.Alias)
	}
	for i, srv := range servers {
		if i < hopCount-1 && !inboundCapable(srv) {
			return "failed", fmt.Sprintf("服务器 %s 已转为仅出口 NAT（无可用端口），无法继续担任%s，请编辑调整链路或补充可用端口",
				srv.Alias, hopRoleLabel(hopCount, i))
		}
	}
	if snapshotExitIsExternal {
		if exit := servers[hopCount-1].ParseExternalExit(); exit == nil || exit.Validate() != nil {
			return "failed", fmt.Sprintf("外部直链服务器 %s 的出口配置不可用，请编辑链路或补全服务器参数", servers[hopCount-1].Alias)
		}
	}

	newRanges, err := shared.ParsePortRanges(after.AllowedPorts)
	if err != nil {
		return "failed", fmt.Sprintf("解析服务器 %s 可用端口段失败：%v", after.Alias, err)
	}

	externalExit := current.Snapshot.ServiceExternal
	var vc shared.VirtualConfig
	if externalExit == nil {
		if err := json.Unmarshal(current.Snapshot.ServiceConfig, &vc); err != nil {
			return "failed", "链路出口配置无法解析，请手动编辑或删除该链路"
		}
	}

	endpointID := current.Snapshot.EndpointID
	serviceEndpointID := current.Snapshot.ServiceEndpointID
	serviceUUID := current.Snapshot.ServiceUUID
	needsRevision := chain.Status == store.ChainStatusExchangeFailed
	endpointChanged := false
	serviceEndpointChanged := false
	forceServiceReapply := false
	forceUpstreamForwardIdx := -1
	forceDownstreamBridgeIdx := -1

	// 1) 入口共享端点：若端口已越过新 NAT 段，新建或复用段内新端点（同 ID 重发会保留旧端口）。
	if srvIdx == 0 && endpointID != 0 {
		ep, epErr := s.st.SharedEndpointByID(ctx, endpointID)
		if epErr != nil {
			return "failed", fmt.Sprintf("读取入口共享端点失败：%v", epErr)
		}
		epPort := ep.Port
		if epPort == 0 && len(ep.RealizedConfig) > 0 {
			var rc shared.RealizedConfig
			if json.Unmarshal(ep.RealizedConfig, &rc) == nil {
				epPort = rc.Port
			}
		}
		if len(newRanges) > 0 && epPort > 0 && !shared.InListenRanges(newRanges, epPort) {
			var epCfg shared.VirtualConfig
			if err := json.Unmarshal(ep.ConfigTemplate, &epCfg); err != nil {
				return "failed", fmt.Sprintf("解析入口共享端点配置失败：%v", err)
			}
			epCfg.Port = 0
			epCfg.StaticClients = nil
			endpointJSON, _ := json.Marshal(epCfg)
			profile := epCfg
			profile.Port = 0
			profileJSON, _ := json.Marshal(profile)
			profileHash := fmt.Sprintf("%x", sha256.Sum256(profileJSON))
			newEp, _, epAllocErr := s.st.EnsureSharedEndpointInRanges(ctx, after.ID, ep.Protocol,
				profileHash, endpointJSON, newRanges)
			if epAllocErr != nil {
				return "failed", fmt.Sprintf("重新分配入口共享端点失败：%v", epAllocErr)
			}
			endpointID = newEp.ID
			endpointChanged = true
			needsRevision = true
		}
	}

	// 2) hy2 出口共享端点（与端到端 port_hop 段治理）。
	oldPortHop := vc.PortHop
	if externalExit == nil && vc.Protocol == shared.ProtocolHysteria2 && serviceEndpointID != 0 {
		if srvIdx == hopCount-1 {
			svcEp, svcErr := s.st.SharedEndpointByID(ctx, serviceEndpointID)
			if svcErr != nil {
				return "failed", fmt.Sprintf("读取出口共享端点失败：%v", svcErr)
			}
			svcPort := svcEp.Port
			if svcPort == 0 && len(svcEp.RealizedConfig) > 0 {
				var rc shared.RealizedConfig
				if json.Unmarshal(svcEp.RealizedConfig, &rc) == nil {
					svcPort = rc.Port
				}
			}
			portOutOfRange := len(newRanges) > 0 && svcPort > 0 && !shared.InListenRanges(newRanges, svcPort)
			portHopNeedsUpdate := false
			if vc.PortHop != "" {
				if !inboundCapable(after) {
					portHopNeedsUpdate = true
				} else if len(newRanges) > 0 {
					start, end, parseErr := shared.ParsePortHop(vc.PortHop)
					if parseErr != nil || !shared.SpanInListenRanges(newRanges, start, end) {
						portHopNeedsUpdate = true
					}
				}
			}
			if portOutOfRange || portHopNeedsUpdate {
				if !inboundCapable(after) {
					vc.PortHop = ""
				} else if portHopNeedsUpdate {
					tmpNode := createNodeRequest{Protocol: shared.ProtocolHysteria2}
					if resolveErr := s.resolveHy2PortHop(ctx, &tmpNode, after, chain.ID, 0); resolveErr != nil {
						vc.PortHop = ""
					} else {
						vc.PortHop = tmpNode.PortHop
					}
					if vc.PortHop != "" && endpointID == 0 {
						if s0, e0, parseErr := shared.ParsePortHop(vc.PortHop); parseErr == nil {
							for _, hopSrv := range servers[:hopCount-1] {
								hopRanges, _ := shared.ParsePortRanges(hopSrv.AllowedPorts)
								if len(hopRanges) > 0 && !shared.SpanInListenRanges(hopRanges, s0, e0) {
									vc.PortHop = ""
									break
								}
							}
						}
					}
				}
				vcCopy := vc
				vcCopy.Port = 0
				endpointJSON, _ := json.Marshal(vcCopy)
				newSvcEp, created, svcAllocErr := s.st.EnsureProtocolSharedEndpointInRanges(ctx, after.ID,
					shared.ProtocolHysteria2, endpointJSON, newRanges, inboundCapable(after))
				if svcAllocErr != nil {
					return "failed", fmt.Sprintf("重新分配 hy2 出口共享端点失败：%v", svcAllocErr)
				}
				if !created {
					var dummy createNodeRequest
					adoptHy2EndpointParams(&dummy, &vc, newSvcEp)
				}
				vc.Port = 0
				serviceEndpointID = newSvcEp.ID
				serviceEndpointChanged = true
				forceServiceReapply = true
				needsRevision = true
			} else if svcPort > 0 && publicPortOfServer(before, svcPort) != publicPortOfServer(after, svcPort) {
				if endpointID != 0 && hopCount == 2 && current.Snapshot.Hops[0].Transport == "hy2" {
					endpointChanged = true
				} else if srvIdx > 0 {
					forceUpstreamForwardIdx = srvIdx - 1
				}
				needsRevision = true
			}
		} else if srvIdx < hopCount-1 && endpointID == 0 && vc.PortHop != "" && len(newRanges) > 0 {
			start, end, parseErr := shared.ParsePortHop(vc.PortHop)
			if parseErr != nil || !shared.SpanInListenRanges(newRanges, start, end) {
				return "failed", fmt.Sprintf("服务器 %s 的新可用端口段无法覆盖 Hysteria2 端到端跳跃段 %s，请编辑调整链路或扩大可用端口段", after.Alias, vc.PortHop)
			}
		}
	}

	// 3) 普通出口业务节点（非 hy2 共享监听且非单跳共享端点）。
	directShared := hopCount == 1 && endpointID != 0
	if srvIdx == hopCount-1 && externalExit == nil && serviceEndpointID == 0 && !directShared {
		realizedPort := 0
		if len(current.Snapshot.ServiceRealized) > 0 {
			var rc shared.RealizedConfig
			if json.Unmarshal(current.Snapshot.ServiceRealized, &rc) == nil {
				realizedPort = rc.Port
			}
		}
		if realizedPort == 0 && current.Snapshot.ServiceNodeID != 0 {
			if node, nErr := s.st.NodeByID(ctx, current.Snapshot.ServiceNodeID); nErr == nil && len(node.RealizedConfig) > 0 {
				var rc shared.RealizedConfig
				if json.Unmarshal(node.RealizedConfig, &rc) == nil {
					realizedPort = rc.Port
				}
			}
		}
		outOfRange := len(newRanges) > 0 && ((vc.Port > 0 && !shared.InListenRanges(newRanges, vc.Port)) ||
			(realizedPort > 0 && !shared.InListenRanges(newRanges, realizedPort)))
		if outOfRange {
			vc.Port = 0
			forceServiceReapply = true
			needsRevision = true
		} else if srvIdx > 0 && realizedPort > 0 && publicPortOfServer(before, realizedPort) != publicPortOfServer(after, realizedPort) {
			forceUpstreamForwardIdx = srvIdx - 1
			needsRevision = true
		}
	}

	serviceConfig := json.RawMessage(`{}`)
	if externalExit == nil {
		raw, marshalErr := json.Marshal(vc)
		if marshalErr != nil {
			return "failed", fmt.Sprintf("序列化出口配置失败：%v", marshalErr)
		}
		serviceConfig = raw
	}

	// 4) 逐跳端口与 transport 重算。
	desiredHops := make([]store.ChainRevisionHop, len(current.Snapshot.Hops))
	copy(desiredHops, current.Snapshot.Hops)

	// 检查目标服务器所在跳的监听端口是否需要重分配（必须分配新 HopID 避免 agent 复用旧端口）。
	if srvIdx < hopCount-1 {
		hop := &desiredHops[srvIdx]
		localOnlyEntry := srvIdx == 0 && endpointID != 0
		forwardOutOfRange := !localOnlyEntry && len(newRanges) > 0 && hop.ForwardPort > 0 && !shared.InListenRanges(newRanges, hop.ForwardPort)
		portalOutOfRange := len(newRanges) > 0 && hop.PortalPort > 0 && !shared.InListenRanges(newRanges, hop.PortalPort)
		if forwardOutOfRange || portalOutOfRange {
			newHopID, idErr := s.st.NextChainHopID(ctx, chain.ID, after.ID)
			if idErr != nil {
				return "failed", fmt.Sprintf("分配新跳 ID 失败：%v", idErr)
			}
			hop.HopID = newHopID
			if !localOnlyEntry {
				hop.ForwardPort = 0
			}
			hop.PortalPort = 0
			hop.PortalPublicKey = ""
			hop.PortalServerName = ""
			needsRevision = true
		} else {
			if !localOnlyEntry && srvIdx > 0 && hop.ForwardPort > 0 &&
				publicPortOfServer(before, hop.ForwardPort) != publicPortOfServer(after, hop.ForwardPort) {
				forceUpstreamForwardIdx = srvIdx - 1
				needsRevision = true
			}
			if hop.PortalPort > 0 &&
				publicPortOfServer(before, hop.PortalPort) != publicPortOfServer(after, hop.PortalPort) {
				forceDownstreamBridgeIdx = srvIdx + 1
				needsRevision = true
			}
		}
	} else if hopCount == 1 && endpointID == 0 && forceServiceReapply {
		desiredHops[0].ForwardPort = 0
	}

	plaintext := externalExit == nil && (vc.Protocol == shared.ProtocolSocks || vc.Protocol == shared.ProtocolHTTP)
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
			needsRevision = true
		}
		desiredHops[i].Transport = transport
		if transport == "direct" || transport == "hy2" {
			desiredHops[i].TunnelUUID = ""
		} else if desiredHops[i].TunnelUUID == "" {
			desiredHops[i].TunnelUUID = uuid.NewString()
		}
	}

	// 若端到端 hy2 跳跃段发生变化且仍开启跳跃，同步重算入口/中间跳的 forward 绑定端口（需新 HopID 重新监听）。
	if externalExit == nil && endpointID == 0 && vc.Protocol == shared.ProtocolHysteria2 && oldPortHop != vc.PortHop && vc.PortHop != "" {
		if s0, _, pErr := shared.ParsePortHop(vc.PortHop); pErr == nil && s0 > 0 {
			for i := 0; i+1 < len(desiredHops); i++ {
				if desiredHops[i].ForwardPort != s0 {
					if newHopID, idErr := s.st.NextChainHopID(ctx, chain.ID, desiredHops[i].ServerID); idErr == nil {
						desiredHops[i].HopID = newHopID
						desiredHops[i].PortalPort = 0
						desiredHops[i].PortalPublicKey = ""
						desiredHops[i].PortalServerName = ""
					}
					desiredHops[i].ForwardPort = s0
					needsRevision = true
				}
			}
		}
	}

	if !needsRevision {
		return "skipped", ""
	}

	desired := store.ChainRevisionSnapshot{
		Name:                   current.Snapshot.Name,
		ServiceNodeID:          current.Snapshot.ServiceNodeID,
		ServiceServerID:        servers[hopCount-1].ID,
		ServiceConfig:          serviceConfig,
		EndpointID:             endpointID,
		ServiceEndpointID:      serviceEndpointID,
		ServiceUUID:            serviceUUID,
		TrafficMultiplierMilli: current.Snapshot.TrafficMultiplierMilli,
		Hops:                   desiredHops,
		ServiceExternal:        externalExit,
	}
	if desired.Name == "" {
		desired.Name = chain.Name
	}

	plan, err := dispatch.PlanRevision(revisionTopology(1, current.Snapshot), revisionTopology(2, desired))
	if err != nil {
		return "failed", fmt.Sprintf("生成链路转换计划失败：%v", err)
	}

	promoteKey := make(map[string]bool)
	if forceServiceReapply && desired.ServiceNodeID != 0 && !directShared {
		promoteKey[fmt.Sprintf("%s/%d", dispatch.RevisionPieceService, desired.ServiceNodeID)] = true
		if hopCount >= 2 && !(hopCount == 2 && desiredHops[0].Transport == "hy2") {
			promoteKey[fmt.Sprintf("%s/%d", dispatch.RevisionPieceForward, desiredHops[hopCount-2].HopID)] = true
		}
	}
	if forceUpstreamForwardIdx >= 0 && forceUpstreamForwardIdx < len(desiredHops) {
		promoteKey[fmt.Sprintf("%s/%d", dispatch.RevisionPieceForward, desiredHops[forceUpstreamForwardIdx].HopID)] = true
	}
	if forceDownstreamBridgeIdx > 0 && forceDownstreamBridgeIdx < len(desiredHops) {
		promoteKey[fmt.Sprintf("%s/%d", dispatch.RevisionPieceBridge, desiredHops[forceDownstreamBridgeIdx].HopID)] = true
	}
	if failedRevision != nil {
		tasks, taskErr := s.st.RevisionTasks(ctx, failedRevision.ID)
		if taskErr != nil {
			return "failed", fmt.Sprintf("读取失败修订任务失败：%v", taskErr)
		}
		acked := make(map[string]bool, len(tasks))
		for _, task := range tasks {
			if task.Phase == "apply" && task.Status == store.RevisionTaskAcked {
				acked[task.TaskKey] = true
			}
		}
		for _, piece := range plan.Reuse {
			if !acked[piece.Key] {
				promoteKey[piece.Key] = true
			}
		}
	} else if chain.Status == store.ChainStatusExchangeFailed && chain.PublishedRevisionID == 0 {
		for _, piece := range plan.Reuse {
			promoteKey[piece.Key] = true
		}
	}
	if len(promoteKey) > 0 {
		remaining := make([]dispatch.RevisionPiece, 0, len(plan.Reuse))
		for _, piece := range plan.Reuse {
			if promoteKey[piece.Key] {
				plan.Apply = append(plan.Apply, piece)
			} else {
				remaining = append(remaining, piece)
			}
		}
		plan.Reuse = remaining
	}

	for _, piece := range plan.Apply {
		desired.ApplyKeys = append(desired.ApplyKeys, piece.Key)
	}
	serviceChanged := false
	for _, key := range desired.ApplyKeys {
		if key == fmt.Sprintf("%s/%d", dispatch.RevisionPieceService, desired.ServiceNodeID) {
			serviceChanged = true
		}
	}
	if !serviceChanged {
		desired.ServiceRealized = append(json.RawMessage(nil), current.Snapshot.ServiceRealized...)
	}

	revision, err := s.st.CreateChainRevision(ctx, chain.ID, desired)
	if err != nil {
		return "failed", fmt.Sprintf("创建链路修订失败：%v", err)
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

	var nodePort *int
	if vc.Port != 0 {
		value := vc.Port
		nodePort = &value
	}
	if err := s.disp.EditChainTopology(ctx, revision, vc.Protocol, nodePort, serviceChanged); err != nil {
		return "failed", fmt.Sprintf("更新链路拓扑失败：%v", err)
	}
	if failedRevision != nil {
		if err := s.st.AbandonChainRevision(ctx, failedRevision.ID, "被服务器类型转换取代"); err != nil {
			log.Printf("panel: abandon failed revision %d on server exchange: %v", failedRevision.ID, err)
		}
	}
	if waitingRevisionID != 0 {
		if err := s.st.AbandonChainRevision(ctx, waitingRevisionID, "被服务器类型转换取代"); err != nil {
			log.Printf("panel: abandon waiting revision %d on server exchange: %v", waitingRevisionID, err)
		}
	}

	offline := false
	if s.req != nil {
		for _, piece := range plan.Apply {
			if !s.req.IsOnline(piece.ServerID) {
				offline = true
				break
			}
		}
		if !offline && endpointChanged && !s.req.IsOnline(servers[0].ID) {
			offline = true
		}
		if !offline && serviceEndpointChanged && !s.req.IsOnline(servers[hopCount-1].ID) {
			offline = true
		}
	}
	if offline {
		_ = s.st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusWaitingForAgent, "")
	}
	if err := s.disp.StartChainOrWait(ctx, chain.ID, offline, "服务器类型转换后等待 Agent 在线完成编排"); err != nil {
		return "failed", fmt.Sprintf("启动链路转换编排失败：%v", err)
	}
	if offline {
		return "waiting", ""
	}
	return "exchanged", ""
}

func (s *Server) reconcileHopGraphsForServerExchange(ctx context.Context, after *store.Server, refs []store.ChainHopGraphRef, failedSet map[int64]bool) {
	if len(refs) == 0 {
		return
	}
	addrSet := store.ServerAddressSet(after)
	for _, ref := range refs {
		if failedSet[ref.ChainID] {
			continue
		}
		chain, err := s.st.ChainByID(ctx, ref.ChainID)
		if err != nil || chain.Status == store.ChainStatusDeleted || chain.Status == store.ChainStatusInvalid ||
			chain.Status == store.ChainStatusExchangeFailed {
			continue
		}
		graph, err := s.st.ChainHopGraph(ctx, ref.ChainID, ref.Seq)
		if err != nil || graph == nil || len(graph.Nodes) == 0 {
			continue
		}
		hops, err := s.st.ChainHops(ctx, ref.ChainID)
		if err != nil || len(hops) == 0 {
			continue
		}
		idToIdx := make(map[int64]int, len(graph.Nodes))
		modified := false
		for i := range graph.Nodes {
			idToIdx[graph.Nodes[i].ID] = i
			n := &graph.Nodes[i]
			if n.Kind == store.HopGraphKindServer && n.ServerID == after.ID {
				if n.Address != "" && !addrSet[n.Address] {
					n.Address = ""
					modified = true
				}
				if n.ListenPort > 0 && (after.IsExternal() || checkPortInRanges(after, n.ListenPort) != nil) {
					n.ListenPort = 0
					modified = true
				}
			}
		}
		normalizedEdges := make([]store.ChainHopGraphEdge, 0, len(graph.Edges))
		for _, e := range graph.Edges {
			fromIdx, okFrom := idToIdx[e.FromNode]
			toIdx, okTo := idToIdx[e.ToNode]
			if !okFrom || !okTo {
				continue
			}
			normalizedEdges = append(normalizedEdges, store.ChainHopGraphEdge{
				FromNode: int64(fromIdx), ToNode: int64(toIdx),
			})
		}
		graph.Edges = normalizedEdges
		if valErr := validateHopOrchestration(ctx, s.st, chain, hops, ref.Seq, *graph); valErr != nil {
			_ = s.st.DeleteChainHopGraph(ctx, ref.ChainID, ref.Seq)
			_ = s.st.SetChainOrchestrationError(ctx, ref.ChainID,
				fmt.Sprintf("服务器 %s 类型转换后第 %d 跳编排图不兼容已清除：%v", after.Alias, ref.Seq+1, valErr))
			continue
		}
		if modified {
			if _, err := s.st.ReplaceChainHopGraph(ctx, ref.ChainID, ref.Seq, graph.Nodes, graph.Edges); err != nil {
				log.Printf("panel: update hop graph chain=%d seq=%d after server exchange: %v", ref.ChainID, ref.Seq, err)
			}
		}
	}
}

func publicPortOfServer(srv *store.Server, listen int) int {
	if srv == nil || listen <= 0 {
		return listen
	}
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil || len(ranges) == 0 {
		return listen
	}
	if pub, ok := shared.PublicPort(ranges, listen); ok {
		return pub
	}
	return listen
}

func hopRoleLabel(total, idx int) string {
	if total == 1 {
		return "直连入口"
	}
	if idx == 0 {
		return "入口跳"
	}
	if idx == total-1 {
		return "出口跳"
	}
	return "中转跳"
}
