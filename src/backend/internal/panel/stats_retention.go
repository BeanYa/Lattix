package panel

import "context"

// cleanupTrafficStats 每小时清理保留期外的流量统计分钟桶；保留天数读自
// SettingTrafficStatsRetentionDays（缺省/非法回落默认 60 天），日汇总表永不清理。
func (s *Server) cleanupTrafficStats(ctx context.Context) error {
	days, err := s.st.TrafficStatsRetentionDays(ctx)
	if err != nil {
		return err
	}
	_, err = s.st.DeleteExpiredTrafficStatBuckets(ctx, days)
	return err
}
