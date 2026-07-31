package dispatch

// 链路状态机（§21.1）：集中管理链状态转换合法性、副作用分派与条件驱动评估。
//
// 外部事件输入：
//   - Agent 上线/离线（hub.OnConnect / hub.OnDisconnect → Evaluate）
//   - 共享端点 ack（handleCommandResponse → Evaluate）
//   - 编排推进（advanceChain → Transition）
//   - 管理员操作（重试/删除 → Transition）
//   - 周期自愈（ReconcileStaleEndpoints → 间接触发 Evaluate）
//
// 设计原则：
//   - 所有链状态变更经 Transition 或 Evaluate 入口，不直接调用 store.SetChainStatus。
//   - 副作用（告警、端点 reconcile、订阅重建）声明式绑定到状态进入，与转换逻辑解耦。
//   - Evaluate 是幂等的：相同条件重复调用不产生多余转换或副作用。

import (
	"context"
	"fmt"
	"log"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
)

// chainTransitions 定义合法的链状态转换集合。键为当前状态，值为允许的目标状态集合。
// 未列出的转换为非法，Transition 将拒绝并记录日志。
var chainTransitions = map[string]map[string]bool{
	store.ChainStatusPending: {
		store.ChainStatusApplying: true,
		store.ChainStatusInvalid:  true,
		store.ChainStatusDeleted:  true,
	},
	store.ChainStatusApplying: {
		store.ChainStatusActive:          true,
		store.ChainStatusActiveUnconfirmed: true,
		store.ChainStatusFailed:          true,
		store.ChainStatusWaitingForAgent: true,
		store.ChainStatusInvalid:         true,
		store.ChainStatusDeleted:         true,
	},
	store.ChainStatusActive: {
		store.ChainStatusDegraded:       true,
		store.ChainStatusActiveFailed:   true,
		store.ChainStatusCleanupPending: true,
		store.ChainStatusInvalid:        true,
		store.ChainStatusDeleted:        true,
	},
	store.ChainStatusDegraded: {
		store.ChainStatusActive:         true,
		store.ChainStatusActiveFailed:   true,
		store.ChainStatusCleanupPending: true,
		store.ChainStatusInvalid:        true,
		store.ChainStatusDeleted:        true,
	},
	store.ChainStatusFailed: {
		store.ChainStatusApplying: true, // 重试
		store.ChainStatusInvalid:  true,
		store.ChainStatusDeleted:  true,
	},
	store.ChainStatusActiveUnconfirmed: {
		store.ChainStatusActive:   true, // agent 确认
		store.ChainStatusApplying: true, // 重试
		store.ChainStatusFailed:   true,
		store.ChainStatusInvalid:  true,
		store.ChainStatusDeleted:  true,
	},
	store.ChainStatusActiveFailed: {
		store.ChainStatusApplying: true, // 重试
		store.ChainStatusActive:   true, // 重新评估恢复
		store.ChainStatusInvalid:  true,
		store.ChainStatusDeleted:  true,
	},
	store.ChainStatusWaitingForAgent: {
		store.ChainStatusApplying: true,
		store.ChainStatusFailed:   true,
		store.ChainStatusInvalid:  true,
		store.ChainStatusDeleted:  true,
	},
	store.ChainStatusCleanupPending: {
		store.ChainStatusActive:  true, // 清理完成恢复
		store.ChainStatusDeleted: true,
		store.ChainStatusInvalid: true,
	},
	store.ChainStatusInvalid: {
		store.ChainStatusDeleted: true,
	},
}

// validChainTransition 检查从 from 到 to 的状态转换是否合法。
func validChainTransition(from, to string) bool {
	if from == to {
		return true // 幂等
	}
	targets, ok := chainTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// chainFSM 是链路状态机，嵌入 Dispatcher 使用。所有链状态变更的唯一入口。
type chainFSM struct {
	d *Dispatcher // 反向引用，用于访问 st/req/Alerter 等基础设施
}

// Transition 执行一次显式链状态转换：校验合法性 → 持久化 → 分派副作用。
// 非法转换返回 error 并记录日志（不 panic，保持系统韧性）。
func (f *chainFSM) Transition(ctx context.Context, chainID int64, to, detail string) error {
	chain, err := f.d.st.ChainByID(ctx, chainID)
	if err != nil {
		return fmt.Errorf("chain_fsm: chain %d: %w", chainID, err)
	}
	from := chain.Status
	if from == to {
		return nil // 幂等，不触发副作用
	}
	if !validChainTransition(from, to) {
		log.Printf("chain_fsm: chain %d: illegal transition %s → %s (detail: %s)", chainID, from, to, detail)
		return fmt.Errorf("chain_fsm: illegal transition %s → %s", from, to)
	}
	if err := f.d.st.SetChainStatus(ctx, chainID, to, detail); err != nil {
		return fmt.Errorf("chain_fsm: chain %d persist %s: %w", chainID, to, err)
	}
	log.Printf("chain_fsm: chain %d: %s → %s (%s)", chainID, from, to, detail)
	f.onEnter(ctx, chain, to, detail)
	return nil
}

// Evaluate 条件驱动评估：根据当前运行时条件（服务器在线性 + 端点就绪性 + 跳状态）
// 推导链应处于 active 还是 degraded，并在需要时执行转换与副作用。
// 仅对 active/degraded 状态的链生效（其他状态由编排/管理员显式驱动）。
//
// 触发时机：
//   - Agent 上线/离线（RecomputeChainsByServer）
//   - 端点 ack 后
//   - 链路发布后（publishDesiredRevision）
//   - 周期自愈后
func (f *chainFSM) Evaluate(ctx context.Context, chainID int64) {
	chain, err := f.d.st.ChainByID(ctx, chainID)
	if err != nil {
		return
	}
	if chain.Status != store.ChainStatusActive && chain.Status != store.ChainStatusDegraded {
		return // 非运行时状态不参与条件评估
	}
	hops, err := f.d.st.ChainHops(ctx, chainID)
	if err != nil {
		return
	}

	// 条件 1：全部跳 server 在线。
	for i := range hops {
		if !f.d.req.IsOnline(hops[i].ServerID) {
			detail := fmt.Sprintf("跳 %d（%s，server %d）离线", hops[i].ID, hops[i].Role, hops[i].ServerID)
			if chain.Status == store.ChainStatusActive {
				f.degrade(ctx, chain, hops[i].ServerID, detail)
			}
			return
		}
	}

	// 条件 2：共享端点（若有）已 active。
	if chain.EndpointID != 0 {
		endpoint, err := f.d.st.SharedEndpointByID(ctx, chain.EndpointID)
		if err != nil || endpoint.Status != store.EndpointStatusActive {
			if chain.Status == store.ChainStatusActive {
				detail := fmt.Sprintf("共享入口（endpoint %d）尚未生效", chain.EndpointID)
				if endpoint != nil && endpoint.Status == store.EndpointStatusFailed {
					detail = fmt.Sprintf("共享入口（endpoint %d）部署失败: %s", chain.EndpointID, endpoint.Error)
				}
				f.degrade(ctx, chain, 0, detail)
			}
			// 自动重试：端点服务器在线时立即重新下发部署命令。
			if endpoint != nil && f.d.req.IsOnline(endpoint.ServerID) {
				if err := f.d.ReconcileSharedEndpoint(ctx, chain.EndpointID); err != nil {
					log.Printf("chain_fsm: chain %d auto-reconcile endpoint %d: %v", chainID, chain.EndpointID, err)
				}
			}
			return
		}
	}

	// 全部条件满足：若当前 degraded 则恢复 active。
	if chain.Status == store.ChainStatusDegraded {
		for i := range hops {
			if hops[i].Status != store.HopStatusActive {
				return // 跳未就绪，不恢复
			}
		}
		_ = f.Transition(ctx, chainID, store.ChainStatusActive, "")
		f.d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryChain, Action: "chain.recovered",
			Detail: map[string]any{"chain_id": chainID},
		})
	}
}

// degrade 执行 active → degraded 转换并分派副作用（告警 + 日志）。
func (f *chainFSM) degrade(ctx context.Context, chain *store.Chain, serverID int64, detail string) {
	if err := f.Transition(ctx, chain.ID, store.ChainStatusDegraded, detail); err != nil {
		return
	}
	f.d.recordOperation(logging.OperationEvent{
		Severity: logging.SeverityWarning, Category: logging.CategoryChain, Action: "chain.degraded",
		Detail: map[string]any{"chain_id": chain.ID, "endpoint_id": chain.EndpointID, "reason": detail},
	})
	if serverID != 0 && f.d.Alerter != nil {
		f.d.Alerter.Notify(serverID, alert.EventChainDegraded, fmt.Sprintf("chain_%d", chain.ID), detail)
	}
}

// InvalidateForServerDeletion 服务器删除时级联失效链（§10）：
// 校验转换合法性 → 事务性废弃命令/修订/跳 → 记录操作日志。
// 由 panel.handleDeleteServer 调用，替代直接调用 store.InvalidateChainForServerDeletion。
func (f *chainFSM) InvalidateForServerDeletion(ctx context.Context, chainID, serverID int64, reason string) error {
	chain, err := f.d.st.ChainByID(ctx, chainID)
	if err != nil {
		return fmt.Errorf("chain_fsm: chain %d: %w", chainID, err)
	}
	if !validChainTransition(chain.Status, store.ChainStatusInvalid) {
		log.Printf("chain_fsm: chain %d: illegal invalidation from %s", chainID, chain.Status)
		return fmt.Errorf("chain_fsm: illegal invalidation from %s", chain.Status)
	}
	if err := f.d.st.InvalidateChainForServerDeletion(ctx, chainID, serverID, reason); err != nil {
		return fmt.Errorf("chain_fsm: chain %d invalidate: %w", chainID, err)
	}
	log.Printf("chain_fsm: chain %d: %s → invalid (server %d deleted: %s)", chainID, chain.Status, serverID, reason)
	f.d.recordOperation(logging.OperationEvent{
		Severity: logging.SeverityWarning, Category: logging.CategoryChain, Action: "chain.invalidated",
		ServerID: &serverID, Detail: map[string]any{"chain_id": chainID, "reason": reason},
	})
	return nil
}

// ResumeChainsByServer 恢复指定服务器上处于编排中的链（Agent 重连后调用）：
// applying/waiting_for_agent/active_unconfirmed 状态的链重新推进编排。
// 与 ResumeChains（启动时全量恢复）互补，覆盖运行时 Agent 重连场景。
func (f *chainFSM) ResumeChainsByServer(ctx context.Context, serverID int64) {
	hops, err := f.d.st.ChainHopsByServerID(ctx, serverID)
	if err != nil {
		log.Printf("chain_fsm: resume chains by server %d: %v", serverID, err)
		return
	}
	seen := map[int64]bool{}
	for _, h := range hops {
		if seen[h.ChainID] {
			continue
		}
		seen[h.ChainID] = true
		chain, err := f.d.st.ChainByID(ctx, h.ChainID)
		if err != nil {
			continue
		}
		switch chain.Status {
		case store.ChainStatusApplying, store.ChainStatusWaitingForAgent, store.ChainStatusActiveUnconfirmed:
			log.Printf("chain_fsm: chain %d: resuming orchestration (agent reconnected, server %d)", chain.ID, serverID)
			f.d.advanceChain(ctx, chain.ID)
		}
	}
}

// onEnter 状态进入副作用分派。仅在 Transition 成功持久化后调用。
// 注意：详细的失败日志/告警由调用方（failChain）处理，此处仅做轻量级通用副作用。
func (f *chainFSM) onEnter(ctx context.Context, chain *store.Chain, state, detail string) {
	switch state {
	case store.ChainStatusApplying:
		// 重试进入 applying：记录操作日志。
		if chain.Status == store.ChainStatusFailed || chain.Status == store.ChainStatusActiveFailed {
			f.d.recordOperation(logging.OperationEvent{
				Severity: logging.SeverityInfo, Category: logging.CategoryChain, Action: "chain.retry",
				Detail: map[string]any{"chain_id": chain.ID},
			})
		}
	}
}

