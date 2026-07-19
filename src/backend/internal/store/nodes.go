package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// 节点状态机（§6）：pending → applying → active | failed。
const (
	NodeStatusPending  = "pending"
	NodeStatusApplying = "applying"
	NodeStatusActive   = "active"
	NodeStatusFailed   = "failed"
)

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
