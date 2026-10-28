package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 服务器 Ban 观察（§告警分类 Ban告警）：状态机推进逻辑在 panel（ban-watch 巡检），
// 本文件只提供快照持久化与三网测速窗口聚合（逐运营商成败计数），供推进时判定
// 「本轮三网是否全丢包 / 是否恢复在线」。

// Ban 观察状态（server_ban_watch.state 取值）。
const (
	BanStateNormal     = "normal"     // 无告警（含丢包 1-4 轮的静默观察）
	BanStateSuspected  = "suspected"  // 连续 5-9 轮三网全丢包：疑似被 Ban
	BanStateBanned     = "banned"     // 连续 10 轮以上：判定已被 Ban
	BanStateRecovering = "recovering" // banned 后半小时确认在线，待下一轮评估取消告警
)

// ServerBanWatch 是 server_ban_watch 表行（单服务器 Ban 观察快照）。
type ServerBanWatch struct {
	ServerID        int64
	LossStreak      int64
	State           string
	StreakStartedAt *time.Time
	SuspectedAt     *time.Time
	BannedAt        *time.Time
	LastConfirmAt   *time.Time
	UpdatedAt       time.Time
}

// ServerBanWatchMap 返回全部服务器的 Ban 观察快照（按 server_id 索引；无行 = 从未进入观察）。
func (s *Store) ServerBanWatchMap(ctx context.Context) (map[int64]ServerBanWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, loss_streak, state,
		streak_started_at, suspected_at, banned_at, last_confirm_at, updated_at
		FROM server_ban_watch`)
	if err != nil {
		return nil, fmt.Errorf("list server ban watch: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]ServerBanWatch)
	for rows.Next() {
		var row ServerBanWatch
		if err := rows.Scan(&row.ServerID, &row.LossStreak, &row.State,
			&row.StreakStartedAt, &row.SuspectedAt, &row.BannedAt, &row.LastConfirmAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan server ban watch: %w", err)
		}
		out[row.ServerID] = row
	}
	return out, rows.Err()
}

// UpsertServerBanWatch 保存一台服务器的 Ban 观察快照（ban-watch 每轮评估后落库）。
func (s *Store) UpsertServerBanWatch(ctx context.Context, row ServerBanWatch) error {
	if row.State == "" {
		row.State = BanStateNormal
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO server_ban_watch
		(server_id, loss_streak, state, streak_started_at, suspected_at, banned_at, last_confirm_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
		loss_streak=excluded.loss_streak, state=excluded.state,
		streak_started_at=excluded.streak_started_at, suspected_at=excluded.suspected_at,
		banned_at=excluded.banned_at, last_confirm_at=excluded.last_confirm_at,
		updated_at=excluded.updated_at`,
		row.ServerID, row.LossStreak, row.State, row.StreakStartedAt, row.SuspectedAt,
		row.BannedAt, row.LastConfirmAt, row.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("upsert server ban watch: %w", err)
	}
	return nil
}

// DeleteServerBanWatch 清除一台服务器的 Ban 观察快照（服务器标记非直连后巡检
// 跳过并清场；行不存在时静默成功）。
func (s *Store) DeleteServerBanWatch(ctx context.Context, serverID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM server_ban_watch WHERE server_id = ?`, serverID); err != nil {
		return fmt.Errorf("delete server ban watch: %w", err)
	}
	return nil
}

// CatalogCarrierOutcome 是一个目录目标（运营商）在统计窗口内的成败样本计数。
type CatalogCarrierOutcome struct {
	Samples int64
	Failed  int64 // latency_ms IS NULL 的失败/超时样本
}

// AllLost 报告该运营商在窗口内是否全为失败样本（无样本不算）。
func (o CatalogCarrierOutcome) AllLost() bool { return o.Samples > 0 && o.Failed == o.Samples }

// CatalogCarrierOutcomes 返回窗口内 (from_server, 目标 key) 的成败计数（按运营商归组）。
// targetKeys 为当前测速区域的目标全集：窗口内可能残留换省/市之前的旧 key 样本，
// 不在集合内的一律忽略。carrier 通过目标 key 尾段（telecom|unicom|mobile）解析。
func (s *Store) CatalogCarrierOutcomes(ctx context.Context, fromServer int64, targetKeys []string, from, to time.Time) (map[string]CatalogCarrierOutcome, error) {
	if len(targetKeys) == 0 {
		return map[string]CatalogCarrierOutcome{}, nil
	}
	args := make([]any, 0, len(targetKeys)+3)
	query := `SELECT target_key, COUNT(*) AS samples,
			SUM(CASE WHEN latency_ms IS NULL THEN 1 ELSE 0 END) AS failed
		FROM server_catalog_probe_history
		WHERE from_server = ? AND measured_at > ? AND measured_at <= ?
		AND target_key IN (`
	args = append(args, fromServer, from.UTC(), to.UTC())
	for i, key := range targetKeys {
		if i > 0 {
			query += ", "
		}
		query += "?"
		args = append(args, key)
	}
	query += `) GROUP BY target_key`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog carrier outcomes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]CatalogCarrierOutcome)
	for rows.Next() {
		var key string
		var outcome CatalogCarrierOutcome
		if err := rows.Scan(&key, &outcome.Samples, &outcome.Failed); err != nil {
			return nil, fmt.Errorf("scan catalog carrier outcomes: %w", err)
		}
		if idx := strings.LastIndex(key, ":"); idx >= 0 {
			key = key[idx+1:]
		}
		merged := out[key]
		merged.Samples += outcome.Samples
		merged.Failed += outcome.Failed
		out[key] = merged
	}
	return out, rows.Err()
}
