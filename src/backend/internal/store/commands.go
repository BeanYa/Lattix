package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// 命令状态（§4）：queued → sent → acked | failed。
const (
	CommandStatusQueued = "queued"
	CommandStatusSent   = "sent"
	CommandStatusAcked  = "acked"
	CommandStatusFailed = "failed"
)

// Command 是 commands 表的一行；同时充当离线命令队列与操作日志（§4）。
type Command struct {
	ID       int64
	ServerID int64
	Type     string
	Payload  json.RawMessage
	Status   string
	Attempts int
}

// EnqueueCommand 入队一条命令（queued），返回命令 id。
func (s *Store) EnqueueCommand(ctx context.Context, serverID int64, typ string, payload json.RawMessage) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO commands (server_id, type, payload) VALUES (?, ?, ?)`,
		serverID, typ, string(payload))
	if err != nil {
		return 0, fmt.Errorf("enqueue command: %w", err)
	}
	return res.LastInsertId()
}

// QueuedCommands 取一台服务器全部待发命令（按 id 升序）。
func (s *Store) QueuedCommands(ctx context.Context, serverID int64) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, type, payload, status, attempts FROM commands
		 WHERE server_id = ? AND status = ? ORDER BY id`, serverID, CommandStatusQueued)
	if err != nil {
		return nil, fmt.Errorf("query queued commands: %w", err)
	}
	defer rows.Close()
	var cmds []Command
	for rows.Next() {
		var c Command
		var payload string
		if err := rows.Scan(&c.ID, &c.ServerID, &c.Type, &payload, &c.Status, &c.Attempts); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		c.Payload = json.RawMessage(payload)
		cmds = append(cmds, c)
	}
	return cmds, rows.Err()
}

// ResetSentCommands 重连时将 sent 未终态的命令重置为 queued 重新补发（§2 重发语义）。
func (s *Store) ResetSentCommands(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE server_id = ? AND status = ?`,
		CommandStatusQueued, serverID, CommandStatusSent)
	return err
}

// MarkCommandSent 标记命令已投递到连接（attempts +1）。
func (s *Store) MarkCommandSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		CommandStatusSent, id)
	return err
}

// MarkCommandAcked 标记命令已被对端确认执行成功。
func (s *Store) MarkCommandAcked(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		CommandStatusAcked, id)
	return err
}

// MarkCommandFailed 标记命令执行失败。
func (s *Store) MarkCommandFailed(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		CommandStatusFailed, id)
	return err
}
