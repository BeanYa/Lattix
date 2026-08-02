package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User 是 users 表的一行（§4）：独立 UUID（跨服务器同一 UUID，§7）与独立 sub_token（§8）。
// ExpiresAt 为到期时刻（NULL=长期）；Expired 是到期停权标记（sweeper 置位后已扇出 remove_user，§9）。
// Disabled 是管理员显式停用标记（§16），与 Expired 正交：任一成立即为有效停权态。
type User struct {
	ID              int64
	Name            string
	UUID            string
	SubToken        string
	ExpiresAt       *time.Time
	Expired         bool
	Disabled        bool
	TrafficLimit    int64  // 流量配额（字节），0=不限
	TrafficResetDay int    // 每月重置日（day-of-month，0=创建日，1-31）
	SubTitle        string // 订阅落地页标题覆盖
	SubAnnouncement string // 订阅公告覆盖（Markdown）
	PlanName        string // 套餐名（subscription-userinfo plan_name，空=用全局）
	AppURL          string // 客户端跳转链接（subscription-userinfo app_url，空=用全局）
	CreatedAt       time.Time
}

// TrafficResetAt returns this user's reset boundary for a calendar month.
// A day absent from that month resolves to its last day (for example, 31 -> February 28/29).
func (u User) TrafficResetAt(year int, month time.Month, loc *time.Location) time.Time {
	day := u.TrafficResetDay
	if day == 0 {
		day = u.CreatedAt.Day()
	}
	if day < 1 {
		day = 1
	} else if day > 31 {
		day = 31
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

// InsertUser 插入一个用户；expiresAt 为 nil 表示长期有效。
func (s *Store) InsertUser(ctx context.Context, name, uuid, subToken string, expiresAt *time.Time) (int64, error) {
	var exp *int64
	if expiresAt != nil {
		v := expiresAt.Unix()
		exp = &v
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, uuid, sub_token, expires_at) VALUES (?, ?, ?, ?)`, name, uuid, subToken, exp)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return res.LastInsertId()
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var exp sql.NullInt64
	var expired, disabled int
	if err := row.Scan(&u.ID, &u.Name, &u.UUID, &u.SubToken, &exp, &expired, &disabled,
		&u.TrafficLimit, &u.TrafficResetDay, &u.SubTitle, &u.SubAnnouncement, &u.PlanName, &u.AppURL, &u.CreatedAt); err != nil {
		return nil, err
	}
	if exp.Valid {
		t := time.Unix(exp.Int64, 0)
		u.ExpiresAt = &t
	}
	u.Expired = expired != 0
	u.Disabled = disabled != 0
	return &u, nil
}

const userCols = `id, name, uuid, sub_token, expires_at, expired, disabled, traffic_limit, traffic_reset_day, sub_title, sub_announcement, plan_name, app_url, created_at`

// ListUsers 列出全部用户（按 id 升序）。
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UserByID 按 id 查找用户。
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}

// UserBySubToken 按 sub_token 查找用户（订阅端点，§9）。
func (s *Store) UserBySubToken(ctx context.Context, subToken string) (*User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE sub_token = ?`, subToken))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query user by sub_token: %w", err)
	}
	return u, nil
}

// DeleteUser 删除一个用户（含其节点关联，§16）。
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_chain_assignments WHERE user_id = ?`,
		`DELETE FROM subscription_rule_files WHERE snapshot_id IN (SELECT id FROM subscription_snapshots WHERE user_id = ?)`,
		`DELETE FROM subscription_files WHERE snapshot_id IN (SELECT id FROM subscription_snapshots WHERE user_id = ?)`,
		`DELETE FROM published_subscription_snapshots WHERE user_id = ?`,
		`DELETE FROM subscription_snapshots WHERE user_id = ?`,
		`DELETE FROM user_subscription_profiles WHERE user_id = ?`,
		`DELETE FROM user_nodes WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
	}
	return tx.Commit()
}

// ListExpiryDue 返回已到期但尚未停权（expired=0）的用户（§9 sweeper 扫描）。
func (s *Store) ListExpiryDue(ctx context.Context, now time.Time) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userCols+` FROM users
		 WHERE expired = 0 AND expires_at IS NOT NULL AND expires_at <= ? ORDER BY id`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list expiry due users: %w", err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expiry due user: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SetUserExpired 更新用户的到期停权标记（§9）。
func (s *Store) SetUserExpired(ctx context.Context, id int64, expired bool) error {
	v := 0
	if expired {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET expired = ? WHERE id = ?`, v, id)
	if err != nil {
		return fmt.Errorf("set user expired: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserDisabled 更新用户的显式停用标记（§16）；扇出由调用方按有效停权态跃迁负责。
func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, v, id)
	if err != nil {
		return fmt.Errorf("set user disabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserExpiry 修改/清除用户有效期（§9）：expiresAt 为 nil 表示长期。
// 有效期被清除或延长到未来时清除停权标记（由调用方负责扇出 add_user 恢复）。
func (s *Store) SetUserExpiry(ctx context.Context, id int64, expiresAt *time.Time, now time.Time) error {
	var exp *int64
	if expiresAt != nil {
		v := expiresAt.Unix()
		exp = &v
	}
	restore := 0
	if expiresAt == nil || expiresAt.After(now) {
		restore = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET expires_at = ?, expired = CASE WHEN ? = 1 THEN 0 ELSE expired END WHERE id = ?`,
		exp, restore, id)
	if err != nil {
		return fmt.Errorf("set user expiry: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserSubSettings 更新用户级订阅设置（流量配额/重置日/落地页覆盖/套餐名/跳转链接）。
func (s *Store) SetUserSubSettings(ctx context.Context, id int64, trafficLimit int64, resetDay int, subTitle, subAnnouncement, planName, appURL string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET traffic_limit = ?, traffic_reset_day = ?, sub_title = ?, sub_announcement = ?, plan_name = ?, app_url = ? WHERE id = ?`,
		trafficLimit, resetDay, subTitle, subAnnouncement, planName, appURL, id)
	if err != nil {
		return fmt.Errorf("set user sub settings: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserSubToken 更换用户的订阅 token（§8）。
func (s *Store) SetUserSubToken(ctx context.Context, id int64, token string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET sub_token = ? WHERE id = ?`, token, id)
	if err != nil {
		return fmt.Errorf("set user sub token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserNodeIDs 返回用户分配到的节点 id 列表（§16）。
func (s *Store) UserNodeIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT node_id FROM user_nodes WHERE user_id = ? ORDER BY node_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user nodes: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan user node: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetUserNodes 整体替换用户的节点分配（§16），返回新增与移除的节点 id（供增量扇出）。
func (s *Store) SetUserNodes(ctx context.Context, userID int64, nodeIDs []int64) (added, removed []int64, err error) {
	cur, err := s.UserNodeIDs(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	want := map[int64]bool{}
	for _, id := range nodeIDs {
		want[id] = true
	}
	have := map[int64]bool{}
	for _, id := range cur {
		have[id] = true
		if !want[id] {
			removed = append(removed, id)
		}
	}
	for _, id := range nodeIDs {
		if !have[id] {
			added = append(added, id)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_nodes WHERE user_id = ?`, userID); err != nil {
		return nil, nil, fmt.Errorf("reset user nodes: %w", err)
	}
	for _, id := range nodeIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO user_nodes (user_id, node_id) VALUES (?, ?)`, userID, id); err != nil {
			return nil, nil, fmt.Errorf("insert user node: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return added, removed, nil
}

// NodeUserUUIDs 返回分配到该节点的用户 UUID 列表（apply_node 下发，§16）。
// 已到期停权（expired=1）或显式停用（disabled=1）的用户不下发（§9/§16）：
// 恢复时由有效期/停用开关扇出 add_user 补回。
func (s *Store) NodeUserUUIDs(ctx context.Context, nodeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.uuid FROM user_nodes un JOIN users u ON u.id = un.user_id
		 WHERE un.node_id = ? AND u.expired = 0 AND u.disabled = 0 ORDER BY u.id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node user uuids: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan uuid: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
