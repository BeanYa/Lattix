package panel

import (
	"context"
	"log"
	"time"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
)

// expirySweepIntervalDefault 是用户生命周期扫描（激活/到期/流量配额）的默认周期（§9）。
const expirySweepIntervalDefault = time.Minute

// sweepUserLifecycles 用户生命周期扫描（§9）：试用到期删除、生效时刻到点激活、
// 普通用户到期停权、流量配额用尽停权。四段互不重叠，同一周期内按序执行：
// 试用删除先行，避免给已过期的试用用户补发 add_user；激活先于停权，使
// "生效与到期同时越过"的普通用户直接走停权路径而不产生 add→remove 抖动。
func (s *Server) sweepUserLifecycles(ctx context.Context) {
	now := time.Now()
	s.sweepTrialExpiries(ctx, now)
	s.sweepActivations(ctx, now)
	s.sweepExpiries(ctx, now)
	s.sweepQuotaExhaustions(ctx, now)
}

// sweepTrialExpiries 试用用户到期自动删除（§9）：删除用户记录、订阅快照以及
// 节点/共享端点上的用户凭证。流量用尽只置 quota_exhausted 停权、不删除
// （见 sweepQuotaExhaustions）。
func (s *Server) sweepTrialExpiries(ctx context.Context, now time.Time) {
	due, err := s.st.ListTrialExpiryDue(ctx, now)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list trial expiry due users: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list nodes: %v", err)
		return
	}
	for _, u := range due {
		// 重读后再删：管理员可能在扫描与删除之间延长有效期或执行了重新激活。
		current, err := s.st.UserByID(ctx, u.ID)
		if err != nil {
			continue // 已被删除
		}
		if !current.IsTrial || current.ExpiresAt == nil || current.ExpiresAt.After(now) {
			continue
		}
		assigned, err := s.st.UserNodeIDs(ctx, current.ID)
		if err != nil {
			log.Printf("panel: trial expiry: user %d list nodes: %v", current.ID, err)
			continue
		}
		assignments, _ := s.st.UserChainAssignments(ctx, current.ID)
		// 先摘除节点与共享端点上的用户凭证，再删用户记录（订阅快照随 DeleteUser 级联删除）。
		s.fanoutUserDiff(ctx, current.UUID, nodes, nil, assigned)
		s.reconcileUserEndpoints(ctx, current.ID, assignments)
		if err := s.st.DeleteUser(ctx, current.ID); err != nil {
			log.Printf("panel: trial expiry: delete user %d: %v", current.ID, err)
			continue
		}
		if err := s.recordOperation(ctx, logging.OperationEvent{
			Severity: logging.SeverityInfo,
			Category: logging.CategoryUser,
			Action:   "user.trial_expired_deleted",
			Detail: map[string]any{
				"user_id": current.ID, "name": current.Name, "expires_at": current.ExpiresAt,
			},
			Operator: "system",
		}); err != nil {
			log.Printf("panel: audit user.trial_expired_deleted: %v", err)
		}
		log.Printf("panel: trial user %d (%s) 已到期，自动删除（节点 %d）", current.ID, current.Name, len(assigned))
	}
}

// sweepActivations 生效时刻已到但尚未激活的用户：置位激活标记后按有效停权态扇出 add_user。
// 已停用/已到期/配额停权/本轮同时到期的用户只补记标记（时间已越过的事实），摘除由停权路径负责。
func (s *Server) sweepActivations(ctx context.Context, now time.Time) {
	due, err := s.st.ListActivationDue(ctx, now)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list activation due users: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list nodes: %v", err)
		return
	}
	for _, u := range due {
		if err := s.st.MarkUserStarted(ctx, u.ID, now); err != nil {
			log.Printf("panel: lifecycle sweep: mark user %d started: %v", u.ID, err)
			continue
		}
		if u.Disabled || u.Expired || u.QuotaExhausted || (u.ExpiresAt != nil && !u.ExpiresAt.After(now)) {
			log.Printf("panel: user %d (%s) 已到生效时刻，置 started_at（停权/将到期，跳过扇出）", u.ID, u.Name)
			continue
		}
		assigned, err := s.st.UserNodeIDs(ctx, u.ID)
		if err != nil {
			log.Printf("panel: lifecycle sweep: user %d list nodes: %v", u.ID, err)
			continue
		}
		s.fanoutUserDiff(ctx, u.UUID, nodes, assigned, nil)
		if assignments, err := s.st.UserChainAssignments(ctx, u.ID); err == nil {
			s.reconcileUserEndpoints(ctx, u.ID, assignments)
		}
		if s.subscriptions != nil {
			s.subscriptions.EnqueueUsers([]int64{u.ID}, "")
		}
		log.Printf("panel: user %d (%s) 已到生效时刻，扇出 add_user（%d 节点）", u.ID, u.Name, len(assigned))
	}
}

// sweepExpiries 已到期但未停权的用户：置 expired 并扇出 remove_user（§9）。
func (s *Server) sweepExpiries(ctx context.Context, now time.Time) {
	due, err := s.st.ListExpiryDue(ctx, now)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list due users: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list nodes: %v", err)
		return
	}
	for _, u := range due {
		s.stopUser(ctx, u, nodes, "已到期", false)
	}
}

// sweepQuotaExhaustions 流量配额强制执行（§9）：对所有设置 traffic_limit 的用户
// （试用与普通一致）检查当期用量（traffic 表 node_id=0 行，与订阅 userinfo 同口径），
// 达配额即置 quota_exhausted 停权（节点摘除 + 订阅 403）。
// 恢复路径：周期流量重置（sweepTrafficReset，按 traffic_reset_day 边界）或管理员手动重置。
func (s *Server) sweepQuotaExhaustions(ctx context.Context, _ time.Time) {
	users, err := s.st.ListUsers(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list users: %v", err)
		return
	}
	traffic, err := s.st.TrafficByUser(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: traffic by user: %v", err)
		return
	}
	var due []store.User
	for _, u := range users {
		if u.TrafficLimit <= 0 || u.Expired || u.QuotaExhausted || u.Disabled {
			continue
		}
		t := traffic[u.UUID]
		if t.Up+t.Down < u.TrafficLimit {
			continue
		}
		due = append(due, u)
	}
	if len(due) == 0 {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		log.Printf("panel: lifecycle sweep: list nodes: %v", err)
		return
	}
	for _, u := range due {
		s.stopUser(ctx, u, nodes, "流量配额已用尽", true)
	}
}

// stopUser 置停权标记（expired=到期 / quota_exhausted=配额用尽）并扇出 remove_user +
// 重发布订阅（节点清空，§9）。已显式停用的用户本就不在线，只补记标记不重复扇出。
func (s *Server) stopUser(ctx context.Context, u store.User, nodes []store.Node, reason string, quota bool) {
	var err error
	if quota {
		err = s.st.SetUserQuotaExhausted(ctx, u.ID, true)
	} else {
		err = s.st.SetUserExpired(ctx, u.ID, true)
	}
	if err != nil {
		log.Printf("panel: lifecycle sweep: mark user %d stopped: %v", u.ID, err)
		return
	}
	if u.Disabled {
		log.Printf("panel: user %d (%s) %s，置停权标记（已停用，跳过扇出）", u.ID, u.Name, reason)
		return
	}
	assigned, err := s.st.UserNodeIDs(ctx, u.ID)
	if err != nil {
		log.Printf("panel: lifecycle sweep: user %d list nodes: %v", u.ID, err)
		return
	}
	s.fanoutUserDiff(ctx, u.UUID, nodes, nil, assigned)
	if assignments, err := s.st.UserChainAssignments(ctx, u.ID); err == nil {
		s.reconcileUserEndpoints(ctx, u.ID, assignments)
	}
	// 停权后重发布订阅（节点清空，§9）。此前该路径缺失重发布触发，
	// 已到期的用户订阅文件仍残留旧节点（评审发现，links.sh e2e 复现）。
	if s.subscriptions != nil {
		s.subscriptions.EnqueueUsers([]int64{u.ID}, "")
	}
	log.Printf("panel: user %d (%s) %s，置停权标记并扇出 remove_user（%d 节点）", u.ID, u.Name, reason, len(assigned))
}
