package panel

import (
	"context"
	"log"
	"time"
)

// expirySweepIntervalDefault 是到期用户扫描的默认周期（§9）。
const expirySweepIntervalDefault = time.Minute

func (s *Server) sweepExpiredUsers(ctx context.Context) {
	due, err := s.st.ListExpiryDue(ctx, time.Now())
	if err != nil {
		log.Printf("panel: expiry sweep: list due users: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		log.Printf("panel: expiry sweep: list nodes: %v", err)
		return
	}
	for _, u := range due {
		if err := s.st.SetUserExpired(ctx, u.ID, true); err != nil {
			log.Printf("panel: expiry sweep: mark user %d expired: %v", u.ID, err)
			continue
		}
		// 有效停权态（disabled OR expired，§16）：已显式停用的用户本就不在线，
		// 到期只补记 expired 标记，不重复扇出 remove_user。
		if u.Disabled {
			log.Printf("panel: user %d (%s) 已到期，置 expired（已停用，跳过扇出）", u.ID, u.Name)
			continue
		}
		assigned, err := s.st.UserNodeIDs(ctx, u.ID)
		if err != nil {
			log.Printf("panel: expiry sweep: user %d list nodes: %v", u.ID, err)
			continue
		}
		s.fanoutUserDiff(ctx, u.UUID, nodes, nil, assigned)
		if assignments, err := s.st.UserChainAssignments(ctx, u.ID); err == nil {
			s.reconcileAssignmentEndpoints(ctx, assignments, nil)
		}
		// 到期停权后重发布订阅（节点清空，§9）。此前该路径缺失重发布触发，
		// 已到期的用户订阅文件仍残留旧节点（评审发现，links.sh e2e 复现）。
		if s.subscriptions != nil {
			s.subscriptions.EnqueueUsers([]int64{u.ID}, "")
		}
		log.Printf("panel: user %d (%s) 已到期，置 expired 并扇出 remove_user（%d 节点）", u.ID, u.Name, len(assigned))
	}
}
