package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// ServerMetrics 是 server_metrics 表的一行（§13 主机遥测最新值）。
type ServerMetrics struct {
	ServerID         int64
	Load1            float64
	Load5            float64
	Load15           float64
	CPUPercent       *float64
	MemTotal         uint64
	MemUsed          uint64
	DiskTotal        uint64
	DiskUsed         uint64
	NetworkInterface string
	NetworkTXBytes   uint64
	NetworkRXBytes   uint64
	NetworkTXBPS     *float64
	NetworkRXBPS     *float64
	UptimeSeconds    uint64
	LatencyMS        *float64
	UpdatedAt        string
}

const metricColumns = `server_id, load1, load5, load15, cpu_percent,
	mem_total, mem_used, disk_total, disk_used, network_interface,
	network_tx_bytes, network_rx_bytes, network_tx_bps, network_rx_bps,
	uptime_seconds, latency_ms`

func metricArgs(serverID int64, m ServerMetrics) []any {
	return []any{
		serverID, m.Load1, m.Load5, m.Load15, m.CPUPercent,
		m.MemTotal, m.MemUsed, m.DiskTotal, m.DiskUsed, m.NetworkInterface,
		m.NetworkTXBytes, m.NetworkRXBytes, m.NetworkTXBPS, m.NetworkRXBPS,
		m.UptimeSeconds, m.LatencyMS,
	}
}

// SaveServerMetrics 原子更新最新值并插入历史样本（telemetry 上报驱动）。
func (s *Store) SaveServerMetrics(ctx context.Context, serverID int64, m ServerMetrics, latencyProbeActive bool) error {
	loc := time.Local
	if name, err := s.GetSetting(ctx, SettingTimezone); err == nil && name != "" {
		if configured, loadErr := time.LoadLocation(name); loadErr == nil {
			loc = configured
		}
	}
	usageDate := time.Now().In(loc).Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server metrics: %w", err)
	}
	defer tx.Rollback()
	var previous ServerMetrics
	previousErr := scanServerMetrics(tx.QueryRowContext(ctx,
		`SELECT `+metricColumns+`, updated_at FROM server_metrics WHERE server_id = ?`, serverID), &previous)
	args := metricArgs(serverID, m)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO server_metrics (`+metricColumns+`, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (server_id) DO UPDATE SET
		   load1 = excluded.load1, load5 = excluded.load5, load15 = excluded.load15,
		   cpu_percent = excluded.cpu_percent, mem_total = excluded.mem_total,
		   mem_used = excluded.mem_used, disk_total = excluded.disk_total,
		   disk_used = excluded.disk_used, network_interface = excluded.network_interface,
		   network_tx_bytes = excluded.network_tx_bytes,
		   network_rx_bytes = excluded.network_rx_bytes,
		   network_tx_bps = excluded.network_tx_bps,
		   network_rx_bps = excluded.network_rx_bps,
		   uptime_seconds = excluded.uptime_seconds, latency_ms = excluded.latency_ms,
		   updated_at = excluded.updated_at`,
		args...)
	if err != nil {
		return fmt.Errorf("upsert server metrics: %w", err)
	}
	historyArgs := append(args, latencyProbeActive)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO server_metric_history (`+metricColumns+`, latency_probe_active, sampled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		historyArgs...)
	if err != nil {
		return fmt.Errorf("insert server metric history: %w", err)
	}
	if previousErr == nil && previous.NetworkInterface != "" && previous.NetworkInterface == m.NetworkInterface &&
		m.UptimeSeconds >= previous.UptimeSeconds && m.NetworkTXBytes >= previous.NetworkTXBytes && m.NetworkRXBytes >= previous.NetworkRXBytes {
		txDelta, rxDelta := m.NetworkTXBytes-previous.NetworkTXBytes, m.NetworkRXBytes-previous.NetworkRXBytes
		if txDelta <= math.MaxInt64 && rxDelta <= math.MaxInt64 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO server_network_usage_daily (server_id, usage_date, tx_bytes, rx_bytes)
				VALUES (?, ?, ?, ?) ON CONFLICT(server_id, usage_date) DO UPDATE SET
				tx_bytes=tx_bytes+excluded.tx_bytes, rx_bytes=rx_bytes+excluded.rx_bytes`, serverID, usageDate, int64(txDelta), int64(rxDelta)); err != nil {
				return fmt.Errorf("record server network usage: %w", err)
			}
		}
	} else if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return fmt.Errorf("read previous server metrics: %w", previousErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server metrics: %w", err)
	}
	return nil
}

// ServerMetricsMap 返回 server_id → 主机指标（面板列表联查）。
func (s *Store) ServerMetricsMap(ctx context.Context) (map[int64]ServerMetrics, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+metricColumns+`, updated_at FROM server_metrics`)
	if err != nil {
		return nil, fmt.Errorf("list server metrics: %w", err)
	}
	defer rows.Close()
	out := map[int64]ServerMetrics{}
	for rows.Next() {
		var m ServerMetrics
		if err := scanServerMetrics(rows, &m); err != nil {
			return nil, fmt.Errorf("scan server metrics: %w", err)
		}
		out[m.ServerID] = m
	}
	return out, rows.Err()
}

type metricScanner interface {
	Scan(dest ...any) error
}

func scanServerMetrics(scanner metricScanner, m *ServerMetrics) error {
	return scanner.Scan(
		&m.ServerID, &m.Load1, &m.Load5, &m.Load15, &m.CPUPercent,
		&m.MemTotal, &m.MemUsed, &m.DiskTotal, &m.DiskUsed, &m.NetworkInterface,
		&m.NetworkTXBytes, &m.NetworkRXBytes, &m.NetworkTXBPS, &m.NetworkRXBPS,
		&m.UptimeSeconds, &m.LatencyMS, &m.UpdatedAt,
	)
}

// RecentServerMetricSamples 返回每台服务器最近 limit 个已接受的延迟探测包，按时间升序。
// 生命周期暂停的包不占用趋势图名额；已接受但超时的 NULL 样本保留为连通性参考。
func (s *Store) RecentServerMetricSamples(ctx context.Context, limit int) (map[int64][]ServerMetrics, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+metricColumns+`, sampled_at
		FROM (
			SELECT `+metricColumns+`, sampled_at,
			       ROW_NUMBER() OVER (
			         PARTITION BY server_id ORDER BY sampled_at DESC, id DESC
			       ) AS sample_rank
			FROM server_metric_history
			WHERE latency_probe_active = 1
		)
		WHERE sample_rank <= ?
		ORDER BY server_id, sampled_at`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent server metric samples: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]ServerMetrics)
	for rows.Next() {
		var m ServerMetrics
		if err := scanServerMetrics(rows, &m); err != nil {
			return nil, fmt.Errorf("scan recent server metric sample: %w", err)
		}
		out[m.ServerID] = append(out[m.ServerID], m)
	}
	return out, rows.Err()
}

// ServerMetricHistory 返回指定服务器最近 hours 小时的样本，按时间升序。
func (s *Store) ServerMetricHistory(ctx context.Context, serverID int64, hours int) ([]ServerMetrics, error) {
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+metricColumns+`, sampled_at
		 FROM server_metric_history
		 WHERE server_id = ? AND sampled_at >= ?
		 ORDER BY sampled_at, id`, serverID, since)
	if err != nil {
		return nil, fmt.Errorf("server metric history: %w", err)
	}
	defer rows.Close()
	var out []ServerMetrics
	for rows.Next() {
		var m ServerMetrics
		if err := scanServerMetrics(rows, &m); err != nil {
			return nil, fmt.Errorf("scan server metric history: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) DeleteExpiredServerMetricHistory(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM server_metric_history WHERE sampled_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired server metric history: %w", err)
	}
	return result.RowsAffected()
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

// UserTraffic 返回单个用户的流量合计（用户维度 node_id=0，各服务器上报已按 (0,uuid) 累计求和）；
// 无数据时返回零值（订阅 subscription-userinfo 头，§9）。
func (s *Store) UserTraffic(ctx context.Context, userUUID string) (TrafficTotals, error) {
	var t TrafficTotals
	err := s.db.QueryRowContext(ctx,
		`SELECT up, down FROM traffic WHERE node_id = 0 AND user_uuid = ?`, userUUID).
		Scan(&t.Up, &t.Down)
	if errors.Is(err, sql.ErrNoRows) {
		return TrafficTotals{}, nil
	}
	if err != nil {
		return TrafficTotals{}, fmt.Errorf("query user traffic: %w", err)
	}
	return t, nil
}
