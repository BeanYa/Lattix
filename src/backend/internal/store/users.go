package store

import (
	"context"
	"fmt"
)

// InsertUser 插入一个用户：独立 UUID（跨服务器同一 UUID，§7）与独立 sub_token（§8）。
func (s *Store) InsertUser(ctx context.Context, name, uuid, subToken string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, uuid, sub_token) VALUES (?, ?, ?)`, name, uuid, subToken)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return res.LastInsertId()
}
