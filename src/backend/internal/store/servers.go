package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound 表示查询的行不存在。
var ErrNotFound = errors.New("store: not found")

// Server 是 servers 表的一行（§4）。
type Server struct {
	ID          int64
	Alias       string
	Token       string // 长期凭证；创建时先存 bootstrap token，hello 认证后换发（§11）
	XrayVersion string
}

// CreateServer 插入一台服务器，token 为一次性 bootstrap token（§11），返回服务器 id。
func (s *Store) CreateServer(ctx context.Context, alias, bootstrapToken string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (alias, token) VALUES (?, ?)`, alias, bootstrapToken)
	if err != nil {
		return 0, fmt.Errorf("insert server: %w", err)
	}
	return res.LastInsertId()
}

// ServerByToken 按 token（bootstrap 或长期）查找服务器。
func (s *Store) ServerByToken(ctx context.Context, token string) (*Server, error) {
	var srv Server
	err := s.db.QueryRowContext(ctx,
		`SELECT id, alias, token, COALESCE(xray_version, '') FROM servers WHERE token = ?`, token).
		Scan(&srv.ID, &srv.Alias, &srv.Token, &srv.XrayVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by token: %w", err)
	}
	return &srv, nil
}

// RotateServerToken 重写服务器 token：hello 认证成功后 bootstrap token 换发为长期凭证（§11）。
func (s *Store) RotateServerToken(ctx context.Context, id int64, newToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET token = ? WHERE id = ?`, newToken, id)
	return err
}

// TouchServer 更新 last_seen_at 与 xray 版本（hello 携带，§13）。
func (s *Store) TouchServer(ctx context.Context, id int64, xrayVersion string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET last_seen_at = CURRENT_TIMESTAMP, xray_version = ? WHERE id = ?`,
		xrayVersion, id)
	return err
}
