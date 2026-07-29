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
	for _, u := range due {
		if err := s.st.ArchiveUserTraffic(ctx, u.UUID, now); err != nil {
			log.Printf("panel: traffic reset: archive user %d (%s): %v", u.ID, u.Name, err)
			continue
		}
		if err := s.st.PruneTrafficHistory(ctx, u.UUID, keep); err != nil {
			log.Printf("panel: traffic reset: prune history user %d: %v", u.ID, err)
		}
		log.Printf("panel: traffic reset: user %d (%s) 流量已归档并重置", u.ID, u.Name)
	}
	return nil
}
