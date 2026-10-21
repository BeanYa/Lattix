package panel

import (
	"context"
	"log"
	"strconv"
	"time"

	"lattix/backend/internal/store"
)

// sweepTrafficReset 周期性检查并执行用户流量重置：
// 归档当期流量到 traffic_history，清零后开始新周期。
// 因流量配额停权（quota_exhausted，§9）的用户在新周期恢复访问：
// 配额清零即解除配额停权并扇出 add_user、重发布订阅（到期/停用用户不受影响，
// 仍需管理员处理；多重停权时仅解除配额侧，其余标记继续生效）。
func (s *Server) sweepTrafficReset(ctx context.Context) error {
	now := time.Now()
	due, err := s.st.UsersDueForTrafficReset(ctx, now)
	if err != nil {
		log.Printf("panel: traffic reset: list due users: %v", err)
		return nil
	}
	if len(due) == 0 {
		return nil
	}
	// 读取历史保留周期数。
	keep := 12 // 默认保留 12 个周期
	if raw, _ := s.st.GetSetting(ctx, store.SettingTrafficHistoryKeep); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			keep = v
		}
	}
	var nodes []store.Node
	for _, u := range due {
		if err := s.st.ArchiveUserTraffic(ctx, u.UUID, now); err != nil {
			log.Printf("panel: traffic reset: archive user %d (%s): %v", u.ID, u.Name, err)
			continue
		}
		if err := s.st.PruneTrafficHistory(ctx, u.UUID, keep); err != nil {
			log.Printf("panel: traffic reset: prune history user %d: %v", u.ID, err)
		}
		log.Printf("panel: traffic reset: user %d (%s) 流量已归档并重置", u.ID, u.Name)
		if !u.QuotaExhausted {
			continue
		}
		if err := s.st.SetUserQuotaExhausted(ctx, u.ID, false); err != nil {
			log.Printf("panel: traffic reset: clear quota exhausted user %d: %v", u.ID, err)
			continue
		}
		// 多重停权（到期/停用/未生效）时仅解除配额侧，不扇出恢复。
		if u.Expired || u.Disabled || u.NotYetEffective(now) {
			log.Printf("panel: user %d (%s) 配额停权随周期重置解除（其余停权标记仍生效）", u.ID, u.Name)
			continue
		}
		if nodes == nil {
			if nodes, err = s.st.ListNodes(ctx); err != nil {
				log.Printf("panel: traffic reset: list nodes: %v", err)
				continue
			}
		}
		assigned, err := s.st.UserNodeIDs(ctx, u.ID)
		if err != nil {
			log.Printf("panel: traffic reset: user %d list nodes: %v", u.ID, err)
			continue
		}
		s.fanoutUserDiff(ctx, u.UUID, nodes, assigned, nil)
		if assignments, err := s.st.UserChainAssignments(ctx, u.ID); err == nil {
			s.reconcileUserEndpoints(ctx, u.ID, assignments)
		}
		if s.subscriptions != nil {
			s.subscriptions.EnqueueUsers([]int64{u.ID}, "")
		}
		log.Printf("panel: user %d (%s) 新周期配额恢复，扇出 add_user（%d 节点）", u.ID, u.Name, len(assigned))
	}
	return nil
}
