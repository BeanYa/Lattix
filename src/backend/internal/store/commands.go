package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// CommandByID 按 id 取命令（apply_result 归属校验用，§5）。
func (s *Store) CommandByID(ctx context.Context, id int64) (*Command, error) {
	var c Command
	var payload string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, type, payload, status, attempts FROM commands WHERE id = ?`, id).
		Scan(&c.ID, &c.ServerID, &c.Type, &payload, &c.Status, &c.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query command: %w", err)
	}
	c.Payload = json.RawMessage(payload)
	return &c, nil
}

// MarkCommandAcked 标记命令已被对端确认执行成功（仅 sent → acked，§4）。
// 返回 false 表示命令不在 sent 状态（如已死信），迟到的回执不得翻回终态。
func (s *Store) MarkCommandAcked(ctx context.Context, id int64) (bool, error) {
	return s.transitionFromSent(ctx, id, CommandStatusAcked)
}

// MarkCommandFailed 标记命令执行失败（仅 sent → failed，§4）；返回值语义同 MarkCommandAcked。
func (s *Store) MarkCommandFailed(ctx context.Context, id int64) (bool, error) {
	return s.transitionFromSent(ctx, id, CommandStatusFailed)
}

// transitionFromSent 仅在 sent 状态下迁移命令至终态，返回是否实际迁移。
func (s *Store) transitionFromSent(ctx context.Context, id int64, to string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
		to, id, CommandStatusSent)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeadLetterCommand 将超过投递上限的命令终态化（queued/sent → failed，§2 死信）。
func (s *Store) DeadLetterCommand(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status IN (?, ?)`,
		CommandStatusFailed, id, CommandStatusQueued, CommandStatusSent)
	return err
}
