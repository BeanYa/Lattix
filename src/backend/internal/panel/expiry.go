package panel

import (
	"context"
	"log"
	"time"
)

// expirySweepIntervalDefault 是到期用户扫描的默认周期（§9）。
const expirySweepIntervalDefault = time.Minute

// RunExpirySweeper 后台扫描到期用户：expires_at 已过且 expired=0 → 置 expired=1
// 并对其已分配节点所在服务器扇出 remove_user（显式 nodes 载荷，§9）。
// interval 传 0 用默认值；启动即先扫一次。阻塞运行，由调用方 go 启动。
func (s *Server) RunExpirySweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = expirySweepIntervalDefault
	}
	s.sweepExpiredUsers(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepExpiredUsers(ctx)
		}
	}
}

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
		assigned, err := s.st.UserNodeIDs(ctx, u.ID)
		if err != nil {
			log.Printf("panel: expiry sweep: user %d list nodes: %v", u.ID, err)
			continue
		}
		s.fanoutUserDiff(ctx, u.UUID, nodes, nil, assigned)
		log.Printf("panel: user %d (%s) 已到期，置 expired 并扇出 remove_user（%d 节点）", u.ID, u.Name, len(assigned))
	}
}
