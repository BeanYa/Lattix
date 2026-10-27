package store

import (
	"context"
	"fmt"
	"time"
)

// ChainProbeSample 是一帧遥测里的一条链跳段探测结果（agent TCP 握手 RTT）。
type ChainProbeSample struct {
	HopID         int64
	Kind          string // forward|bridge
	TargetAddress string
	TargetPort    int
	LatencyMS     *float64 // nil = 失败/超时样本（连通性参考）
}

// ChainHopProbe 是 chain_hop_probes 最新值行。
type ChainHopProbe struct {
	HopID         int64
	Kind          string
	ServerID      int64
	TargetAddress string
	TargetPort    int
	LatencyMS     *float64
	SampledAt     time.Time
}

// SaveChainHopProbes 覆盖写段探测最新值并插入历史（遥测上报驱动）。
// 未知 hop 的样本照常保留历史（join chain_hops 过滤），面板侧按新鲜度窗口取用。
func (s *Store) SaveChainHopProbes(ctx context.Context, serverID int64, probes []ChainProbeSample, sampledAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chain hop probes: %w", err)
	}
	defer tx.Rollback()
	for _, probe := range probes {
		if probe.HopID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_probes
			(hop_id, kind, server_id, target_address, target_port, latency_ms, sampled_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(hop_id, kind) DO UPDATE SET
			server_id=excluded.server_id, target_address=excluded.target_address,
			target_port=excluded.target_port, latency_ms=excluded.latency_ms,
			sampled_at=excluded.sampled_at`,
			probe.HopID, probe.Kind, serverID, probe.TargetAddress, probe.TargetPort,
			probe.LatencyMS, sampledAt.UTC()); err != nil {
			return fmt.Errorf("upsert chain hop probe: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_probe_history
			(hop_id, kind, server_id, latency_ms, sampled_at) VALUES (?, ?, ?, ?, ?)`,
			probe.HopID, probe.Kind, serverID, probe.LatencyMS, sampledAt.UTC()); err != nil {
			return fmt.Errorf("insert chain hop probe history: %w", err)
		}
	}
	return tx.Commit()
}

// FreshChainHopProbes 返回新鲜度窗口内的段探测最新值（最新值表只保留每段一条，
// 窗口外样本视为失效：Agent 离线或拓扑变更后旧段不再计入链路延迟）。
func (s *Store) FreshChainHopProbes(ctx context.Context, maxAge time.Duration) ([]ChainHopProbe, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	rows, err := s.db.QueryContext(ctx, `SELECT hop_id, kind, server_id, target_address,
		target_port, latency_ms, sampled_at FROM chain_hop_probes WHERE sampled_at >= ?
		ORDER BY hop_id, kind`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("fresh chain hop probes: %w", err)
	}
	defer rows.Close()
	out := make([]ChainHopProbe, 0)
	for rows.Next() {
		var probe ChainHopProbe
		if err := rows.Scan(&probe.HopID, &probe.Kind, &probe.ServerID,
			&probe.TargetAddress, &probe.TargetPort, &probe.LatencyMS, &probe.SampledAt); err != nil {
			return nil, fmt.Errorf("scan chain hop probe: %w", err)
		}
		out = append(out, probe)
	}
	return out, rows.Err()
}

// DeleteExpiredChainHopProbeHistory 清理保留窗口外的探测历史（后台小时任务）。
func (s *Store) DeleteExpiredChainHopProbeHistory(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM chain_hop_probe_history WHERE sampled_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired chain hop probe history: %w", err)
	}
	return result.RowsAffected()
}

// ChainDailyTraffic 是某日一条链（或其单跳）的流量桶（原始与折算字节）。
type ChainDailyTraffic struct {
	ChainID       int64 `json:"chain_id"`
	HopID         int64 `json:"hop_id"`
	RawUp         int64 `json:"raw_up"`
	RawDown       int64 `json:"raw_down"`
	EffectiveUp   int64 `json:"effective_up"`
	EffectiveDown int64 `json:"effective_down"`
}

// ChainDailyTrafficByDate 返回指定日期（流量统计时区口径）的逐链逐跳流量桶，
// hop_id=0 为链合计（入口用户口径），hop_id>0 为各跳转发口径（链路一览负载列）。
func (s *Store) ChainDailyTrafficByDate(ctx context.Context, usageDate string) ([]ChainDailyTraffic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chain_id, hop_id, SUM(raw_up), SUM(raw_down),
		SUM(effective_up), SUM(effective_down) FROM chain_traffic_daily
		WHERE usage_date=? GROUP BY chain_id, hop_id ORDER BY chain_id, hop_id`, usageDate)
	if err != nil {
		return nil, fmt.Errorf("chain daily traffic: %w", err)
	}
	defer rows.Close()
	out := make([]ChainDailyTraffic, 0)
	for rows.Next() {
		var item ChainDailyTraffic
		if err := rows.Scan(&item.ChainID, &item.HopID, &item.RawUp, &item.RawDown,
			&item.EffectiveUp, &item.EffectiveDown); err != nil {
			return nil, fmt.Errorf("scan chain daily traffic: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
