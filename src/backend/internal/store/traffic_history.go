package store

import (
	"context"
	"fmt"
	"time"
)

// TrafficHistoryRow 是 traffic_history 表的一行（审计用）。
type TrafficHistoryRow struct {
	ID          int64
	NodeID      int64
	UserUUID    string
	Up          int64
	Down        int64
	PeriodStart string
	PeriodEnd   string
}

// ArchiveUserTraffic 将指定用户在所有节点上的当期流量归档到 traffic_history 并清零。
// periodEnd 为归档周期的结束时间（即新周期起始）。在单个事务内完成。
func (s *Store) ArchiveUserTraffic(ctx context.Context, userUUID string, periodEnd time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	endStr := periodEnd.Format(time.RFC3339)
	// 归档：将该用户所有有流量的行（up>0 或 down>0）写入 history。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO traffic_history (node_id, user_uuid, up, down, period_start, period_end)
		 SELECT node_id, user_uuid, up, down,
		        CASE WHEN period_start = '' THEN datetime('now', 'start of month') ELSE period_start END,
		        ?
		 FROM traffic WHERE user_uuid = ? AND (up > 0 OR down > 0)`,
		endStr, userUUID); err != nil {
		return fmt.Errorf("archive traffic: %w", err)
	}
	// 清零并更新 period_start。
	if _, err := tx.ExecContext(ctx,
		`UPDATE traffic SET up = 0, down = 0, period_start = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE user_uuid = ?`, endStr, userUUID); err != nil {
		return fmt.Errorf("reset traffic: %w", err)
	}
	return tx.Commit()
}

// ListUserTrafficHistory 返回指定用户的流量历史（按 period_start 降序）。
func (s *Store) ListUserTrafficHistory(ctx context.Context, userUUID string, limit int) ([]TrafficHistoryRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, user_uuid, up, down, period_start, period_end
		 FROM traffic_history WHERE user_uuid = ? ORDER BY period_start DESC LIMIT ?`,
		userUUID, limit)
	if err != nil {
		return nil, fmt.Errorf("list traffic history: %w", err)
	}
	defer rows.Close()
	var out []TrafficHistoryRow
	for rows.Next() {
		var r TrafficHistoryRow
		if err := rows.Scan(&r.ID, &r.NodeID, &r.UserUUID, &r.Up, &r.Down, &r.PeriodStart, &r.PeriodEnd); err != nil {
			return nil, fmt.Errorf("scan traffic history: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneTrafficHistory 删除指定用户超出保留周期数的历史记录。
func (s *Store) PruneTrafficHistory(ctx context.Context, userUUID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM traffic_history WHERE user_uuid = ? AND id NOT IN (
			SELECT id FROM traffic_history WHERE user_uuid = ? ORDER BY period_start DESC LIMIT ?
		)`, userUUID, userUUID, keep)
	if err != nil {
		return fmt.Errorf("prune traffic history: %w", err)
	}
	return nil
}

// UsersDueForTrafficReset 返回今天应执行流量重置的用户列表：
// 当期 period_start 的月份 ≠ 当前月 且 今天 >= reset_day。
// reset_day=0 时取用户创建日的 day-of-month。
func (s *Store) UsersDueForTrafficReset(ctx context.Context, now time.Time) ([]User, error) {
	// 查询所有有流量记录的用户，判断是否需要重置。
	// 简化实现：查所有用户，在 Go 侧判断。用户量小（自用/小团队），可接受。
	users, err := s.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var due []User
	for _, u := range users {
		resetDay := u.TrafficResetDay
		if resetDay == 0 {
			resetDay = u.CreatedAt.Day()
		}
		if resetDay > 28 {
			resetDay = 28
		}
		if now.Day() < resetDay {
			continue
		}
		// 检查该用户当期 period_start 是否在本月之前。
		needs, err := s.userNeedsReset(ctx, u.UUID, now)
		if err != nil {
			continue
		}
		if needs {
			due = append(due, u)
		}
	}
	return due, nil
}

// userNeedsReset 判断用户流量是否需要重置：当期 period_start 的年月 < 当前年月。
func (s *Store) userNeedsReset(ctx context.Context, userUUID string, now time.Time) (bool, error) {
	var periodStart string
	err := s.db.QueryRowContext(ctx,
		`SELECT period_start FROM traffic WHERE user_uuid = ? AND node_id = 0`, userUUID).
		Scan(&periodStart)
	if err != nil {
		// 无记录 → 不需要重置（还没有流量）
		return false, nil
	}
	if periodStart == "" {
		// 从未设置过 period_start（旧数据）→ 需要重置以初始化
		return true, nil
	}
	ps, err := time.Parse(time.RFC3339, periodStart)
	if err != nil {
		return true, nil // 解析失败 → 重置以修复
	}
	// 比较年月：period_start 的年月 < 当前年月 → 需要重置
	psYear, psMonth, _ := ps.Date()
	nowYear, nowMonth, _ := now.Date()
	if psYear < nowYear || (psYear == nowYear && psMonth < nowMonth) {
		return true, nil
	}
	return false, nil
}
