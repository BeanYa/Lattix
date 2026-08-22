package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log"

	"lattix/backend/internal/store"
)

// 共享端点状态机（§21 共享入口）：集中管理端点状态转换合法性、CAS 持久化与副作用分派。
// 与链状态机（chainFSM）同构：所有端点状态变更经 Transition 入口，store 层 WHERE 守卫
// 与转换表双重断言。
//
// 状态机：
//
//	pending → applying → active | failed
//	applying/active/failed → applying（重试 / 路由变化重部署 / 重连自愈幂等）
//	active → failed（新一轮部署失败，旧 realized 保留）
//	applying/active/failed → pending（重置复用，最后一条路由链删除场景）
//
// 外部事件输入：
//   - ReconcileSharedEndpoint（建链/发布/用户分配/自愈三层）→ applying
//   - apply_shared_endpoint 回执（handleCommandResponse）→ active | failed
//   - 命令死信（Flush）→ failed
//
// 副作用（onEnter）：
//   - active/failed → 重算使用该端点的链（degraded ↔ active）+ 触发受影响用户订阅重建。
var endpointTransitions = map[string]map[string]bool{
	store.EndpointStatusPending: {
		store.EndpointStatusApplying: true,
		store.EndpointStatusActive:   true, // 测试夹具直通确认（生产路径必经 applying）
		store.EndpointStatusFailed:   true, // 测试夹具
	},
	store.EndpointStatusApplying: {
		store.EndpointStatusApplying: true, // 幂等：重连自愈重复 reconcile 在途端点
		store.EndpointStatusActive:   true,
		store.EndpointStatusFailed:   true,
		store.EndpointStatusPending:  true, // 重置复用
	},
	store.EndpointStatusActive: {
		store.EndpointStatusApplying: true, // 路由变化重部署
		store.EndpointStatusFailed:   true, // 新一轮部署失败
		store.EndpointStatusPending:  true, // 重置复用
	},
	store.EndpointStatusFailed: {
		store.EndpointStatusApplying: true, // 重试
		store.EndpointStatusPending:  true, // 重置复用
	},
}

func validEndpointTransition(from, to string) bool {
	if from == to {
		return true // 幂等
	}
	targets, ok := endpointTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// endpointFSM 是共享端点状态机，嵌入 Dispatcher 使用。
type endpointFSM struct {
	d *Dispatcher // 反向引用，用于访问 st/回调等基础设施
}

// Transition 执行一次端点状态转换：校验合法性 → CAS 持久化 → 分派副作用。
// realized 仅在 to == active 时使用（生效配置回执）。非法转换返回 error 并记录日志。
func (f *endpointFSM) Transition(ctx context.Context, endpointID int64, to, detail string, realized []byte) error {
	ep, err := f.d.st.SharedEndpointByID(ctx, endpointID)
	if err != nil {
		return fmt.Errorf("endpoint_fsm: endpoint %d: %w", endpointID, err)
	}
	from := ep.Status
	if from == to {
		return nil // 幂等，不触发副作用
	}
	if !validEndpointTransition(from, to) {
		log.Printf("endpoint_fsm: endpoint %d: illegal transition %s → %s (detail: %s)", endpointID, from, to, detail)
		return fmt.Errorf("endpoint_fsm: illegal transition %s → %s", from, to)
	}
	switch to {
	case store.EndpointStatusApplying:
		err = f.d.st.SetSharedEndpointApplying(ctx, endpointID)
	case store.EndpointStatusActive:
		err = f.d.st.SetSharedEndpointActive(ctx, endpointID, realized)
	case store.EndpointStatusFailed:
		err = f.d.st.SetSharedEndpointFailed(ctx, endpointID, detail)
	case store.EndpointStatusPending:
		err = f.d.st.SetSharedEndpointPending(ctx, endpointID)
	default:
		return fmt.Errorf("endpoint_fsm: unknown endpoint status %q", to)
	}
	if err != nil {
		if errors.Is(err, store.ErrStateTransition) {
			return fmt.Errorf("endpoint_fsm: endpoint %d: %w (expected %s)", endpointID, err, from)
		}
		return fmt.Errorf("endpoint_fsm: endpoint %d persist %s: %w", endpointID, to, err)
	}
	log.Printf("endpoint_fsm: endpoint %d: %s → %s (%s)", endpointID, from, to, detail)
	f.onEnter(ctx, ep, to)
	return nil
}

// onEnter 状态进入副作用分派。仅在 Transition 成功持久化后调用。
func (f *endpointFSM) onEnter(ctx context.Context, ep *store.SharedEndpoint, to string) {
	switch to {
	case store.EndpointStatusActive, store.EndpointStatusFailed:
		// 端点生效/失败 → 重算使用该端点的链（degraded ↔ active，同步链路页状态）
		// 并触发受影响用户的订阅重建（同步订阅警告内容）。
		if chainIDs, err := f.d.st.ChainIDsByEndpoint(ctx, ep.ID); err != nil {
			log.Printf("endpoint_fsm: endpoint %d chain ids: %v", ep.ID, err)
		} else {
			for _, cid := range chainIDs {
				f.d.fsm.Evaluate(ctx, cid)
			}
		}
		if f.d.OnEndpointPublished != nil {
			if err := f.d.OnEndpointPublished(ctx, ep.ID); err != nil {
				log.Printf("endpoint_fsm: enqueue subscriptions for endpoint %d: %v", ep.ID, err)
			}
		}
	}
}
