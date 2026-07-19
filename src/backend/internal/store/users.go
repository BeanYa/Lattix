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

// AllUserUUIDs 返回全量用户 UUID 列表（apply_node 一次性下发，§8）。
func (s *Store) AllUserUUIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uuid FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list user uuids: %w", err)
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

// DeleteUser 删除一个用户。
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}
