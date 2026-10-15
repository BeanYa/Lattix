package store

import "database/sql"

// addTrafficStatTx 将一笔遥测增量同时累加进分钟明细桶与日汇总（同一事务）。
// 记录原始字节，不乘链路倍率（计费口径归 chain_traffic_daily）。
// dimType: "user" | "node" | "endpoint" | "chain_hop"；dimID: 用户UUID或十进制ID字符串。
func (s *Store) addTrafficStatTx(tx *sql.Tx, serverID int64, bucketTS int64, usageDate, dimType, dimID string, up, down int64) error {
	if _, err := tx.Exec(`INSERT INTO traffic_stats_bucket (server_id, dim_type, dim_id, bucket_ts, up, down)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id, dim_type, dim_id, bucket_ts) DO UPDATE SET
		up=up+excluded.up, down=down+excluded.down`,
		serverID, dimType, dimID, bucketTS, up, down); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO traffic_stats_daily (server_id, dim_type, dim_id, usage_date, up, down)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id, dim_type, dim_id, usage_date) DO UPDATE SET
		up=up+excluded.up, down=down+excluded.down`,
		serverID, dimType, dimID, usageDate, up, down)
	return err
}
