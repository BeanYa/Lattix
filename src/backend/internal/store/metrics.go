package store

import (
	"context"
	"fmt"
)

// ServerMetrics 是 server_metrics 表的一行（§13 主机遥测最新值）。
type ServerMetrics struct {
	Load1      float64
	CPUPercent float64
	MemTotal   uint64
	MemUsed    uint64
	UpdatedAt  string
}

// UpsertServerMetrics 写入服务器最新主机指标（telemetry 上报驱动）。
func (s *Store) UpsertServerMetrics(ctx context.Context, serverID int64, load1, cpuPercent float64, memTotal, memUsed uint64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO server_metrics (server_id, load1, cpu_percent, mem_total, mem_used, updated_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (server_id) DO UPDATE SET
		   load1 = excluded.load1, cpu_percent = excluded.cpu_percent,
		   mem_total = excluded.mem_total, mem_used = excluded.mem_used,
		   updated_at = excluded.updated_at`,
		serverID, load1, cpuPercent, memTotal, memUsed)
	return err
}

// ServerMetricsMap 返回 server_id → 主机指标（面板列表联查）。
func (s *Store) ServerMetricsMap(ctx context.Context) (map[int64]ServerMetrics, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT server_id, load1, cpu_percent, mem_total, mem_used, updated_at FROM server_metrics`)
	if err != nil {
		return nil, fmt.Errorf("list server metrics: %w", err)
	}
	defer rows.Close()
	out := map[int64]ServerMetrics{}
	for rows.Next() {
		var id int64
		var m ServerMetrics
		if err := rows.Scan(&id, &m.Load1, &m.CPUPercent, &m.MemTotal, &m.MemUsed, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan server metrics: %w", err)
		}
		out[id] = m
	}
	return out, rows.Err()
}

// AddTraffic 累加流量（telemetry 增量上报驱动，§13 仅统计）。
// 节点维度 user_uuid 传空串，用户维度 node_id 传 0。
func (s *Store) AddTraffic(ctx context.Context, nodeID int64, userUUID string, up, down int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO traffic (node_id, user_uuid, up, down, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (node_id, user_uuid) DO UPDATE SET
		   up = up + excluded.up, down = down + excluded.down,
		   updated_at = excluded.updated_at`,
		nodeID, userUUID, up, down)
	return err
}

// TrafficTotals 是流量合计（字节）。
type TrafficTotals struct {
	Up   int64
	Down int64
}

// TrafficByNode 返回 node_id → 节点流量合计（面板节点列表）。
func (s *Store) TrafficByNode(ctx context.Context) (map[int64]TrafficTotals, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT node_id, up, down FROM traffic WHERE user_uuid = ''`)
	if err != nil {
		return nil, fmt.Errorf("traffic by node: %w", err)
	}
	defer rows.Close()
	out := map[int64]TrafficTotals{}
	for rows.Next() {
		var id int64
		var t TrafficTotals
		if err := rows.Scan(&id, &t.Up, &t.Down); err != nil {
			return nil, fmt.Errorf("scan node traffic: %w", err)
		}
		out[id] = t
	}
	return out, rows.Err()
}

// TrafficByUser 返回 user_uuid → 用户流量合计（面板用户列表）。
func (s *Store) TrafficByUser(ctx context.Context) (map[string]TrafficTotals, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_uuid, up, down FROM traffic WHERE node_id = 0`)
	if err != nil {
		return nil, fmt.Errorf("traffic by user: %w", err)
	}
	defer rows.Close()
	out := map[string]TrafficTotals{}
	for rows.Next() {
		var uuid string
		var t TrafficTotals
		if err := rows.Scan(&uuid, &t.Up, &t.Down); err != nil {
			return nil, fmt.Errorf("scan user traffic: %w", err)
		}
		out[uuid] = t
	}
	return out, rows.Err()
}
