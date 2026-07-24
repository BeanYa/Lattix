package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User 是 users 表的一行（§4）：独立 UUID（跨服务器同一 UUID，§7）与独立 sub_token（§8）。
type User struct {
	ID        int64
	Name      string
	UUID      string
	SubToken  string
	CreatedAt time.Time
}

// InsertUser 插入一个用户。
func (s *Store) InsertUser(ctx context.Context, name, uuid, subToken string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, uuid, sub_token) VALUES (?, ?, ?)`, name, uuid, subToken)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return res.LastInsertId()
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Name, &u.UUID, &u.SubToken, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

const userCols = `id, name, uuid, sub_token, created_at`

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
		`DELETE FROM user_nodes WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
	}
	return tx.Commit()
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
func (s *Store) NodeUserUUIDs(ctx context.Context, nodeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.uuid FROM user_nodes un JOIN users u ON u.id = un.user_id
		 WHERE un.node_id = ? ORDER BY u.id`, nodeID)
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
