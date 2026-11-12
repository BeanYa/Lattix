package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// 命令状态（§4）：queued → sent → acked | failed。
//
// 回执可来自 queued：Flush 的投递顺序是 send → MarkCommandSent（dispatcher.Flush），
// 两者之间存在落库窗口；Agent 回执可能先于 MarkCommandSent 到达，此时命令仍为 queued。
// 这类回执必须照常落终态（否则命令永久停留 sent、任务永久 queued，重连前不再补发），
// MarkCommandSent 的 queued → sent CAS 随后自然失效。终态到终态的翻写仍被禁止
// （迟到/重复回执不改写已 acked/failed）。
const (
	CommandStatusQueued    = "queued"
	CommandStatusSent      = "sent"
	CommandStatusAcked     = "acked"
	CommandStatusFailed    = "failed"
	CommandStatusAbandoned = "abandoned"
)

// Command 是 commands 表的一行；同时充当离线命令队列与操作日志（§4）。
// CreatedAt/UpdatedAt 仅在 RecentCommands（命令日志）场景填充，其余查询不要求。
type Command struct {
	ID        int64
	RequestID string
	TraceID   string
	ServerID  int64
	Type      string
	Data      json.RawMessage
	Status    string
	Error     string
	Attempts  int
	CreatedAt string
	UpdatedAt string
}

// EnqueueCommand 入队一条命令（queued），返回命令 id。
func (s *Store) EnqueueCommand(
	ctx context.Context,
	requestID, traceID string,
	serverID int64,
	typ string,
	data json.RawMessage,
) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, ?, ?, ?)`,
		requestID, traceID, serverID, typ, string(data))
	if err != nil {
		return 0, fmt.Errorf("enqueue command: %w", err)
	}
	return res.LastInsertId()
}

// QueuedCommands 取一台服务器全部待发命令（按 id 升序）。
func (s *Store) QueuedCommands(ctx context.Context, serverID int64) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, request_id, trace_id, server_id, type, data, status, attempts FROM commands
		 WHERE server_id = ? AND status = ? ORDER BY id`, serverID, CommandStatusQueued)
	if err != nil {
		return nil, fmt.Errorf("query queued commands: %w", err)
	}
	defer rows.Close()
	var cmds []Command
	for rows.Next() {
		var c Command
		var data string
		if err := rows.Scan(
			&c.ID, &c.RequestID, &c.TraceID, &c.ServerID, &c.Type, &data, &c.Status, &c.Attempts,
		); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		c.Data = json.RawMessage(data)
		cmds = append(cmds, c)
	}
	return cmds, rows.Err()
}

// PendingCommandsByType lists commands still awaiting delivery or a receipt.
func (s *Store) PendingCommandsByType(ctx context.Context, serverID int64, typ string) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, request_id, trace_id, data, status, error, attempts, updated_at FROM commands
		 WHERE server_id = ? AND type = ? AND status IN (?, ?) ORDER BY status, id`,
		serverID, typ, CommandStatusQueued, CommandStatusSent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var commands []Command
	for rows.Next() {
		command := Command{ServerID: serverID, Type: typ}
		var data string
		if err := rows.Scan(
			&command.ID, &command.RequestID, &command.TraceID, &data,
			&command.Status, &command.Error, &command.Attempts, &command.UpdatedAt,
		); err != nil {
			return nil, err
		}
		command.Data = json.RawMessage(data)
		commands = append(commands, command)
	}
	return commands, rows.Err()
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
		`UPDATE commands SET status = ?, attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status IN (?, ?)`,
		CommandStatusSent, id, CommandStatusQueued, CommandStatusSent)
	return err
}

// CommandByRequestID 按 WS request_id 取命令（响应归属校验用）。
func (s *Store) CommandByRequestID(ctx context.Context, requestID string) (*Command, error) {
	var c Command
	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, request_id, trace_id, server_id, type, data, status, error, attempts, updated_at
		 FROM commands WHERE request_id = ?`, requestID).
		Scan(
			&c.ID, &c.RequestID, &c.TraceID, &c.ServerID, &c.Type, &data,
			&c.Status, &c.Error, &c.Attempts, &c.UpdatedAt,
		)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query command: %w", err)
	}
	c.Data = json.RawMessage(data)
	return &c, nil
}

// CommandByID 按主键取命令（清理任务自愈判定命令悬空/终态用）。
func (s *Store) CommandByID(ctx context.Context, id int64) (*Command, error) {
	var c Command
	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, request_id, trace_id, server_id, type, data, status, error, attempts, updated_at
		 FROM commands WHERE id = ?`, id).
		Scan(&c.ID, &c.RequestID, &c.TraceID, &c.ServerID, &c.Type, &data, &c.Status, &c.Error, &c.Attempts, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query command by id: %w", err)
	}
	c.Data = json.RawMessage(data)
	return &c, nil
}

// MarkCommandAcked 标记命令已被对端确认执行成功（queued/sent → acked，§4）。
// 返回 false 表示命令已是终态（如已死信/已回执），迟到/重复回执不得翻回终态。
func (s *Store) MarkCommandAcked(ctx context.Context, id int64) (bool, error) {
	return s.transitionFromDeliverable(ctx, id, CommandStatusAcked)
}

// MarkCommandFailed 标记命令执行失败（queued/sent → failed，§4）；返回值语义同 MarkCommandAcked。
func (s *Store) MarkCommandFailed(ctx context.Context, id int64) (bool, error) {
	return s.transitionFromDeliverable(ctx, id, CommandStatusFailed)
}

// MarkCommandFailedWithError 标记命令失败并写入失败原因（queued/sent → failed）。
// 失败原因来自 apply_result.error（agent 回执）或死信说明，供命令日志 API 展示。
func (s *Store) MarkCommandFailedWithError(ctx context.Context, id int64, errMsg string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status IN (?, ?)`,
		CommandStatusFailed, errMsg, id, CommandStatusQueued, CommandStatusSent)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RecentCommands 取一台服务器最近 limit 条命令（操作日志，§4）。
// 不返回 payload（可能含 token 等敏感字段）；按 id 降序（最新在前）。
func (s *Store) RecentCommands(ctx context.Context, serverID int64, limit int) ([]Command, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, request_id, trace_id, server_id, type, status, error, attempts, created_at, updated_at
		 FROM commands WHERE server_id = ? ORDER BY id DESC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent commands: %w", err)
	}
	defer rows.Close()
	var cmds []Command
	for rows.Next() {
		var c Command
		var createdAt, updatedAt string
		if err := rows.Scan(
			&c.ID, &c.RequestID, &c.TraceID, &c.ServerID, &c.Type,
			&c.Status, &c.Error, &c.Attempts, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		c.CreatedAt = createdAt
		c.UpdatedAt = updatedAt
		cmds = append(cmds, c)
	}
	return cmds, rows.Err()
}

// transitionFromDeliverable 在命令仍可投递（queued/sent）时迁移至终态，返回是否实际迁移。
// queued 分支覆盖 Flush 的 send → MarkCommandSent 落库窗口：回执先到的命令仍是 queued。
func (s *Store) transitionFromDeliverable(ctx context.Context, id int64, to string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status IN (?, ?)`,
		to, id, CommandStatusQueued, CommandStatusSent)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RequeueSentCommand 把「已投递但回执丢失」的 sent 命令复位为 queued（§2 重发语义）：
// 清理任务自愈复用同一条命令重发（request_id 不变，remove_* 幂等），attempts 在下次
// MarkCommandSent 时递增，也用于防止旧超时检查复位一次更新的投递。
func (s *Store) RequeueSentCommand(ctx context.Context, id int64, attempts int) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ? AND attempts = ?`,
		CommandStatusQueued, id, CommandStatusSent, attempts)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeadLetterCommand 将超过投递上限的命令终态化（queued/sent → failed，§2 死信）。
// 返回是否实际迁移：并发回执可能已把命令落为 acked（queued 回执窗口），此时不得再按
// 死信做后续状态机收尾。
func (s *Store) DeadLetterCommand(ctx context.Context, id int64, reason string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status IN (?, ?)`,
		CommandStatusFailed, reason, id, CommandStatusQueued, CommandStatusSent)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
