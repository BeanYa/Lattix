package panel

import (
	"context"
	"fmt"
	"sort"
	"time"

	"lattix/backend/internal/store"
)

type graphTriggerHit struct {
	seq       int
	trigger   store.ChainHopGraphNode
	target    store.ChainHopGraphNode
	class     int
	priority  int
	typeRank  int
	ordinal   int
}

func graphTriggerTypeRank(t store.ChainHopGraphNode) int {
	switch {
	case t.Subject == store.TriggerSubjectRoot:
		return 0
	case t.Subject == store.TriggerSubjectSelf && t.Action == store.TriggerActionBanSuspected:
		return 1
	case t.Subject == store.TriggerSubjectSelf && t.Action == store.TriggerActionBanBanned:
		return 2
	case t.Subject == store.TriggerSubjectPrev:
		return 3
	case t.Subject == store.TriggerSubjectNext:
		return 4
	}
	return 9
}

func graphTriggerLabel(t store.ChainHopGraphNode) string {
	switch {
	case t.Subject == store.TriggerSubjectRoot:
		return "原节点恢复"
	case t.Subject == store.TriggerSubjectSelf && t.Action == store.TriggerActionBanSuspected:
		return "疑似被 ban（本机）"
	case t.Subject == store.TriggerSubjectSelf && t.Action == store.TriggerActionBanBanned:
		return "被 ban（本机）"
	case t.Subject == store.TriggerSubjectPrev:
		return "上一跳超时"
	case t.Subject == store.TriggerSubjectNext:
		return "下一跳超时"
	}
	return t.Subject + "/" + t.Action
}

func graphSegmentPair(published store.ChainRevision, upIdx int, upID, downID int64) (int64, int64) {
	transport := "direct"
	if upIdx >= 0 && upIdx < len(published.Snapshot.Hops) {
		if published.Snapshot.Hops[upIdx].Transport != "" {
			transport = published.Snapshot.Hops[upIdx].Transport
		}
	}
	if transport == "reverse" || transport == "encrypted" {
		return downID, upID
	}
	return upID, downID
}

func (o *AutoOrchestrator) graphTriggerFires(snap *orchestratorSnapshot, trigger store.ChainHopGraphNode,
	current *store.Server, seq int, hops []store.ChainHop, published store.ChainRevision) (bool, string) {
	switch {
	case trigger.Subject == store.TriggerSubjectSelf:
		if current == nil || current.IsExternal() || current.NonDirect {
			return false, ""
		}
		state := snap.banWatches[current.ID].State
		if trigger.Action == store.TriggerActionBanSuspected {
			if state == store.BanStateSuspected || state == store.BanStateBanned {
				return true, fmt.Sprintf("服务器 %s 三网测速处于 %s", current.Alias, state)
			}
			return false, ""
		}
		if trigger.Action == store.TriggerActionBanBanned && state == store.BanStateBanned {
			return true, fmt.Sprintf("服务器 %s 三网测速确认被封禁", current.Alias)
		}
		return false, ""
	case trigger.Subject == store.TriggerSubjectPrev:
		if seq <= 0 {
			return false, ""
		}
		from, to := graphSegmentPair(published, seq-1, hops[seq-1].ServerID, hops[seq].ServerID)
		if !o.s.req.IsOnline(from) {
			return false, ""
		}
		pw := snap.pairWatches[[2]int64{from, to}]
		if pw.LossStreak >= defaultPairUnreachableStreak {
			return true, fmt.Sprintf("段间探测 %d → %d 连续 %d 轮超时", from, to, pw.LossStreak)
		}
		return false, ""
	case trigger.Subject == store.TriggerSubjectNext:
		if seq+1 >= len(hops) {
			return false, ""
		}
		from, to := graphSegmentPair(published, seq, hops[seq].ServerID, hops[seq+1].ServerID)
		if !o.s.req.IsOnline(from) {
			return false, ""
		}
		pw := snap.pairWatches[[2]int64{from, to}]
		if pw.LossStreak >= defaultPairUnreachableStreak {
			return true, fmt.Sprintf("段间探测 %d → %d 连续 %d 轮超时", from, to, pw.LossStreak)
		}
		return false, ""
	}
	return false, ""
}

// graphTargetCandidate 校验图节点作为替换目标是否健康/兼容，返回可交给 switchChainHop 的目标。
func (o *AutoOrchestrator) graphTargetCandidate(snap *orchestratorSnapshot, chainID int64, target store.ChainHopGraphNode,
	seq int, hops []store.ChainHop, published store.ChainRevision, used map[int64]bool) (store.ChainHopCandidate, bool) {
	srv := snap.servers[target.ServerID]
	if srv == nil {
		return store.ChainHopCandidate{}, false
	}
	address := target.Address
	if address != "" && !store.ServerAddressSet(srv)[address] {
		address = ""
	}
	port := target.ListenPort
	if port > 0 {
		if err := checkPortInRanges(srv, port); err != nil {
			return store.ChainHopCandidate{}, false
		}
	}
	var upID, downID int64
	if seq > 0 {
		upID = hops[seq-1].ServerID
	}
	if seq+1 < len(hops) {
		downID = hops[seq+1].ServerID
	}
	policy := store.DefaultChainAutoPolicy(chainID)
	policy.UnreachableStreak = defaultPairUnreachableStreak
	candidate := store.ChainHopCandidate{
		ChainID: chainID, Seq: seq, ServerID: target.ServerID,
		Address: address, ListenPort: port,
	}
	ok, _ := o.isCandidateHealthy(snap, candidate, seq, len(hops),
		published.Snapshot.ServiceExternal != nil, used, upID, downID, policy)
	return candidate, ok
}

func graphTriggerReason(t store.ChainHopGraphNode) string {
	switch t.Action {
	case store.TriggerActionBanSuspected, store.TriggerActionBanBanned:
		return store.FailoverReasonBanned
	case store.TriggerActionSegmentTimeout:
		return store.FailoverReasonUnreachable
	case store.TriggerActionRecovered:
		return store.FailoverReasonFailback
	}
	return store.FailoverReasonManualSwitch
}

// evaluateChainGraphs 按跳序 A→B→C 扫描有图的跳；每轮最多切一跳。
func (o *AutoOrchestrator) evaluateChainGraphs(ctx context.Context, chain *store.Chain, hops []store.ChainHop,
	published store.ChainRevision, snap *orchestratorSnapshot, graphs map[int]store.ChainHopGraph) error {
	used := make(map[int64]bool, len(hops))
	for _, hop := range hops {
		used[hop.ServerID] = true
	}
	for seq, hop := range hops {
		graph, ok := graphs[seq]
		if !ok || len(graph.Nodes) == 0 {
			continue
		}
		byID := make(map[int64]store.ChainHopGraphNode, len(graph.Nodes))
		for _, node := range graph.Nodes {
			byID[node.ID] = node
		}
		var root store.ChainHopGraphNode
		var current *store.ChainHopGraphNode
		ownerTriggers := make(map[int64][]store.ChainHopGraphNode)
		targetByTrigger := make(map[int64]store.ChainHopGraphNode)
		for i := range graph.Nodes {
			node := graph.Nodes[i]
			if node.Kind == store.HopGraphKindServer && node.IsRoot {
				root = node
			}
			if node.Kind == store.HopGraphKindServer && node.ServerID == hop.ServerID {
				nodeCopy := node
				current = &nodeCopy
			}
		}
		for _, edge := range graph.Edges {
			from, okFrom := byID[edge.FromNode]
			to, okTo := byID[edge.ToNode]
			if !okFrom || !okTo {
				continue
			}
			if from.Kind == store.HopGraphKindServer && to.Kind == store.HopGraphKindTrigger {
				ownerTriggers[from.ServerID] = append(ownerTriggers[from.ServerID], to)
			}
			if from.Kind == store.HopGraphKindTrigger && to.Kind == store.HopGraphKindServer {
				targetByTrigger[from.ID] = to
			}
		}
		if current == nil {
			return fmt.Errorf("第 %d 跳：当前运行的服务器不在编排图中，暂停编排", seq+1)
		}
		if root.ServerID == 0 {
			return fmt.Errorf("第 %d 跳：编排图缺少根服务器，暂停编排", seq+1)
		}
		for _, node := range graph.Nodes {
			if node.Kind == store.HopGraphKindServer && node.ServerID != hop.ServerID && used[node.ServerID] {
				return fmt.Errorf("第 %d 跳：编排图中的服务器已被其他跳使用，暂停编排", seq+1)
			}
		}
		if graph.LastSwitchAt != nil && graph.CooldownSec > 0 &&
			time.Since(*graph.LastSwitchAt) < time.Duration(graph.CooldownSec)*time.Second {
			continue
		}
		otherUsed := make(map[int64]bool, len(used))
		for id := range used {
			if id != hop.ServerID {
				otherUsed[id] = true
			}
		}
		var hits []graphTriggerHit
		for _, trigger := range ownerTriggers[current.ServerID] {
			if fires, _ := o.graphTriggerFires(snap, trigger, snap.servers[current.ServerID], seq, hops, published); fires {
				target, okTarget := targetByTrigger[trigger.ID]
				if !okTarget {
					continue
				}
				hits = append(hits, graphTriggerHit{
					seq: seq, trigger: trigger, target: target,
					class: trigClass(trigger), priority: trigPriority(trigger),
					typeRank: graphTriggerTypeRank(trigger), ordinal: trigger.Ordinal,
				})
			}
		}
		// 原节点恢复：全局触发器，当前不在根且根健康时命中。
		for _, node := range graph.Nodes {
			if node.Kind != store.HopGraphKindTrigger || node.Subject != store.TriggerSubjectRoot {
				continue
			}
			if current.ServerID == root.ServerID {
				continue
			}
			candidate, healthy := o.graphTargetCandidate(snap, chain.ID, root, seq, hops, published, otherUsed)
			_ = candidate
			if !healthy {
				continue
			}
			hits = append(hits, graphTriggerHit{
				seq: seq, trigger: node, target: root,
				class: trigClass(node), priority: trigPriority(node),
				typeRank: graphTriggerTypeRank(node), ordinal: node.Ordinal,
			})
		}
		if len(hits) == 0 {
			continue
		}
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i], hits[j]
			if a.class != b.class {
				return a.class < b.class
			}
			if a.priority != b.priority {
				return a.priority < b.priority
			}
			if a.typeRank != b.typeRank {
				return a.typeRank < b.typeRank
			}
			return a.ordinal < b.ordinal
		})
		var lastDetail string
		for _, hit := range hits {
			candidate, healthy := o.graphTargetCandidate(snap, chain.ID, hit.target, seq, hops, published, otherUsed)
			if !healthy {
				lastDetail = fmt.Sprintf("触发器「%s」命中，但目标服务器不可用",
					graphTriggerLabel(hit.trigger))
				continue
			}
			fromAlias := fmt.Sprintf("#%d", current.ServerID)
			if srv := snap.servers[current.ServerID]; srv != nil {
				fromAlias = srv.Alias
			}
			toAlias := fmt.Sprintf("#%d", hit.target.ServerID)
			if srv := snap.servers[hit.target.ServerID]; srv != nil {
				toAlias = srv.Alias
			}
			detail := fmt.Sprintf("触发器「%s」命中，%s → %s", graphTriggerLabel(hit.trigger), fromAlias, toAlias)
			if _, err := o.s.switchChainHop(ctx, chain.ID, seq, candidate,
				graphTriggerReason(hit.trigger), detail); err != nil {
				return err
			}
			_ = o.s.st.TouchChainHopGraphSwitch(ctx, chain.ID, seq, time.Now().UTC())
			return nil
		}
		o.reportFailoverExhausted(ctx, chain, seq, hop.ServerID, lastDetail)
		return fmt.Errorf("第 %d 跳：编排触发器命中但没有可用目标", seq+1)
	}
	return nil
}

func trigClass(t store.ChainHopGraphNode) int {
	if t.Priority > 0 {
		return 0
	}
	return 1
}

func trigPriority(t store.ChainHopGraphNode) int {
	if t.Priority > 0 {
		return t.Priority
	}
	return 0
}
