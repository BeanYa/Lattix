package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// 用户引入外部订阅的模式。
const (
	ExtSubModeStack = "stack" // 叠加：独立配额池，额度与已用相加
	ExtSubModeMerge = "merge" // 并入：已用并入面板配额池，总额度不变
	ExtSubModeNodes = "nodes" // 附加：仅引入节点，不参与流量合并
)

// UserExternalSubscription 是用户与外部订阅的一对关联行。
type UserExternalSubscription struct {
	ID             int64
	UserID         int64
	SubscriptionID int64
	Mode           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserExternalSubscriptionJoined 是关联行 + 外部订阅统计字段的 join 结果。
type UserExternalSubscriptionJoined struct {
	UserID         int64
	SubscriptionID int64
	Mode           string
	Name           string
	Upload         int64
	Download       int64
	Total          int64
	Expire         *int64
	NodeCount      int
}

// SetUserExternalSubscriptions 整表替换用户的关联外部订阅（含模式）。
func (s *Store) SetUserExternalSubscriptions(ctx context.Context, userID int64, items []UserExternalSubscription) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set user external subscriptions: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_external_subscriptions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear user external subscriptions: %w", err)
	}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_external_subscriptions
			(user_id, subscription_id, mode) VALUES (?, ?, ?)
			ON CONFLICT(user_id, subscription_id)
			DO UPDATE SET mode = excluded.mode, updated_at = CURRENT_TIMESTAMP`,
			userID, item.SubscriptionID, item.Mode); err != nil {
			return fmt.Errorf("insert user external subscription: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user external subscriptions: %w", err)
	}
	return nil
}

// ListUserExternalSubscriptions 返回用户关联的外部订阅（含流量统计 join）。
func (s *Store) ListUserExternalSubscriptions(ctx context.Context, userID int64) ([]UserExternalSubscriptionJoined, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ues.user_id, ues.subscription_id, ues.mode,
		es.name, es.upload, es.download, es.total, es.expire, es.node_count
		FROM user_external_subscriptions ues
		JOIN external_subscriptions es ON es.id = ues.subscription_id
		WHERE ues.user_id = ?
		ORDER BY ues.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user external subscriptions: %w", err)
	}
	defer rows.Close()
	var out []UserExternalSubscriptionJoined
	for rows.Next() {
		var joined UserExternalSubscriptionJoined
		var expire sql.NullInt64
		if err := rows.Scan(&joined.UserID, &joined.SubscriptionID, &joined.Mode,
			&joined.Name, &joined.Upload, &joined.Download, &joined.Total,
			&expire, &joined.NodeCount); err != nil {
			return nil, fmt.Errorf("scan user external subscription: %w", err)
		}
		if expire.Valid {
			joined.Expire = &expire.Int64
		}
		out = append(out, joined)
	}
	return out, rows.Err()
}

// UsersByExternalSubscriptionID 返回关联了指定外部订阅的用户 ID 列表。
func (s *Store) UsersByExternalSubscriptionID(ctx context.Context, subscriptionID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id FROM user_external_subscriptions
		WHERE subscription_id = ? ORDER BY user_id`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("users by external subscription: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan user id: %w", err)
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}
