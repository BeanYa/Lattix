package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound 表示查询的行不存在。
var ErrNotFound = errors.New("store: not found")

// Server 是 servers 表的一行（§4）。
type Server struct {
	ID          int64
	Alias       string
	Token       string // 长期凭证；创建时先存 bootstrap token，hello 认证后换发（§11）
	LastSeenAt  *time.Time
	XrayVersion string
	Address     string // 公网地址（hello 时按 WS RemoteAddr 记录，订阅用，§9）
	ConfigDrift bool   // 配置漂移标志（§17，agent drift_report 驱动）
	CreatedAt   time.Time
}

// serverCols 是 Server 各字段对应的列清单。
const serverCols = `id, alias, token, last_seen_at, xray_version, address, config_drift, created_at`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	var srv Server
	var lastSeen sql.NullTime
	var xrayVer sql.NullString
	err := row.Scan(&srv.ID, &srv.Alias, &srv.Token, &lastSeen, &xrayVer, &srv.Address, &srv.ConfigDrift, &srv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		srv.LastSeenAt = &t
	}
	srv.XrayVersion = xrayVer.String
	return &srv, nil
}

// CreateServer 插入一台服务器，token 为一次性 bootstrap token（§11），返回服务器 id。
// address 为管理员指定的公网地址（§4）；空串表示留待 hello 时按 RemoteAddr 自动学习。
func (s *Store) CreateServer(ctx context.Context, alias, address, bootstrapToken string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (alias, address, token) VALUES (?, ?, ?)`, alias, address, bootstrapToken)
	if err != nil {
		return 0, fmt.Errorf("insert server: %w", err)
	}
	return res.LastInsertId()
}

// ServerByToken 按 token（bootstrap 或长期）查找服务器。
func (s *Store) ServerByToken(ctx context.Context, token string) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE token = ?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by token: %w", err)
	}
	return srv, nil
}

// ServerByID 按 id 查找服务器。
func (s *Store) ServerByID(ctx context.Context, id int64) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by id: %w", err)
	}
	return srv, nil
}

// ListServers 列出全部服务器（按 id 升序）。
func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		out = append(out, *srv)
	}
	return out, rows.Err()
}

// RotateServerToken 重写服务器 token：hello 认证成功后 bootstrap token 换发为长期凭证（§11）。
func (s *Store) RotateServerToken(ctx context.Context, id int64, newToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET token = ? WHERE id = ?`, newToken, id)
	return err
}

// UpdateServerAddress 由管理员修改服务器公网地址（§4"地址变更由管理员修改"）；
// 置空则下次 hello 时按 RemoteAddr 重新自动学习。
func (s *Store) UpdateServerAddress(ctx context.Context, id int64, address string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET address = ? WHERE id = ?`, address, id)
	return err
}

// TouchServer 更新 last_seen_at、xray 版本（hello 携带，§13）与公网地址（RemoteAddr，§9）。
func (s *Store) TouchServer(ctx context.Context, id int64, xrayVersion, address string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET last_seen_at = CURRENT_TIMESTAMP, xray_version = ?, address = ? WHERE id = ?`,
		xrayVersion, address, id)
	return err
}

// UpdateServerVersion 仅更新 xray 版本（telemetry 周期携带，升级后据此刷新展示，§13）。
func (s *Store) UpdateServerVersion(ctx context.Context, id int64, xrayVersion string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET xray_version = ? WHERE id = ?`, xrayVersion, id)
	return err
}

// SetServerDrift 设置配置漂移标志（§17，agent drift_report 驱动）。
func (s *Store) SetServerDrift(ctx context.Context, id int64, drifted bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET config_drift = ? WHERE id = ?`, drifted, id)
	return err
}

// ResetServerBootstrap 换发 bootstrap token 并将服务器重置回 bootstrap 状态
// （last_seen_at 置空，下次 hello 重新换发长期凭证，§5/§11）。
func (s *Store) ResetServerBootstrap(ctx context.Context, id int64, newBootstrapToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET token = ?, last_seen_at = NULL WHERE id = ?`, newBootstrapToken, id)
	return err
}

// DeleteServerCascade 删除服务器及其节点与命令记录（§10 删除服务器）。
func (s *Store) DeleteServerCascade(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_nodes WHERE node_id IN (SELECT id FROM nodes WHERE server_id = ?)`,
		`DELETE FROM commands WHERE server_id = ?`,
		`DELETE FROM nodes WHERE server_id = ?`,
		`DELETE FROM servers WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete server cascade: %w", err)
		}
	}
	return tx.Commit()
}
