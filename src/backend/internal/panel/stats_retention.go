package panel

import "context"

// cleanupTrafficStats 每小时清理保留期外的流量统计分钟桶；保留天数读自
// SettingTrafficStatsRetentionDays（缺省/非法回落默认 60 天），日汇总表永不清理。
func (s *Server) cleanupTrafficStats(ctx context.Context) error {
	days, err := s.st.TrafficStatsRetentionDays(ctx)
	if err != nil {
		return err
	}
	deleted, err := s.st.DeleteExpiredTrafficStatBuckets(ctx, days)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return nil
	}
	// 大量删除会让 ANALYZE 统计失真（桶表按 bucket_ts 的分布变了），
	// 计划一旦退回全维度扫描，概览查询会慢两个数量级。
	return s.st.AnalyzeTables(ctx, "traffic_stats_bucket")
}
