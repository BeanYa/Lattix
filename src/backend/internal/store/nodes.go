package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 节点状态机（§6）：pending → applying → active | failed。
const (
	NodeStatusPending  = "pending"
	NodeStatusApplying = "applying"
	NodeStatusActive   = "active"
	NodeStatusFailed   = "failed"
)

// Node 是 nodes 表的一行（§4）；ServerAlias 来自 servers 联表。
type Node struct {
	ID             int64
	ServerID       int64
	ServerAlias    string
	Protocol       string
	Port           *int // nil = Agent 自动挑选空闲端口（§7）
	ConfigTemplate json.RawMessage
	RealizedConfig json.RawMessage // nil = 尚未生效
	Status         string
	Error          string
	CreatedAt      time.Time
}

const nodeCols = `n.id, n.server_id, s.alias, n.protocol, n.port, n.config_template, n.realized_config, n.status, n.error, n.created_at`

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var port sql.NullInt64
	var tmpl string
	var realized, nerr sql.NullString
	err := row.Scan(&n.ID, &n.ServerID, &n.ServerAlias, &n.Protocol, &port, &tmpl, &realized, &n.Status, &nerr, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	if port.Valid {
		p := int(port.Int64)
		n.Port = &p
	}
	n.ConfigTemplate = json.RawMessage(tmpl)
	if realized.Valid {
		n.RealizedConfig = json.RawMessage(realized.String)
	}
	n.Error = nerr.String
	return &n, nil
}

// InsertNode 插入一个节点（pending），port 为 nil 表示 Agent 自动挑选空闲端口（§7）。
func (s *Store) InsertNode(ctx context.Context, serverID int64, port *int, template json.RawMessage) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes (server_id, port, config_template) VALUES (?, ?, ?)`,
		serverID, port, string(template))
	if err != nil {
		return 0, fmt.Errorf("insert node: %w", err)
	}
	return res.LastInsertId()
}

// ListNodes 列出全部节点（联表带服务器别名，按 id 升序）。
func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeCols+` FROM nodes n JOIN servers s ON s.id = n.server_id ORDER BY n.id`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// NodeByID 按 id 查找节点。
func (s *Store) NodeByID(ctx context.Context, id int64) (*Node, error) {
	n, err := scanNode(s.db.QueryRowContext(ctx,
		`SELECT `+nodeCols+` FROM nodes n JOIN servers s ON s.id = n.server_id WHERE n.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query node: %w", err)
	}
	return n, nil
}

// SetNodeApplying 节点进入 applying（apply_node 已下发）。
func (s *Store) SetNodeApplying(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, error = NULL WHERE id = ?`, NodeStatusApplying, id)
	return err
}

// SetNodeActive 节点生效，记录 Agent 上报的实际生效值（§7）。
func (s *Store) SetNodeActive(ctx context.Context, id int64, realized json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, realized_config = ?, error = NULL WHERE id = ?`,
		NodeStatusActive, string(realized), id)
	return err
}

// SetNodeFailed 节点失败，携带错误详情（面板提供重试按钮，§6）。
func (s *Store) SetNodeFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, error = ? WHERE id = ?`, NodeStatusFailed, errMsg, id)
	return err
}
