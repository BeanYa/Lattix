package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// 事件日志分类（§log）：统一汇聚四类信号成时间线。
const (
	EventCategoryCommand = "command" // 命令 ack/failed（dispatcher 在 commands 表状态机外复写一份）
	EventCategoryNode    = "node"    // 节点状态机流转（保留历史，补 nodes.error 只存当前值的缺口）
	EventCategoryAgent   = "agent"   // agent 连接/离线
	EventCategoryAdmin   = "admin"   // 管理员操作（13 个 handler 文件的全量写操作）
)

// EventLog 是 event_log 表的一行。
type EventLog struct {
	ID       int64
	Ts       string
	Category string
	Action   string
	ServerID sql.NullInt64 // admin 类登录/改密等无关联服务器
	NodeID   sql.NullInt64
	Detail   string // JSON 串
	Operator string
	IP       string
}

// EventFilter 是 ListEvents 的查询过滤条件；指针字段 nil = 不过滤。
type EventFilter struct {
	Category string // 精确匹配；空 = 不过滤
	ServerID *int64 // 精确匹配
	Operator string // 精确匹配
	Q        string // action/detail 模糊匹配（LIKE %q%）
}

// RecordEvent 追加一条事件日志。serverID/nodeID 为 nil 表示无关联（写入 NULL）。
// detail 为任意值，会被 json.Marshal；若已是 string/[]byte 则原样使用。
func (s *Store) RecordEvent(ctx context.Context, category, action string, serverID, nodeID *int64, detail any, operator, ip string) error {
	var detailStr string
	switch d := detail.(type) {
	case nil:
	case string:
		detailStr = d
	case []byte:
		detailStr = string(d)
	case json.RawMessage:
		detailStr = string(d)
	default:
		b, err := json.Marshal(d)
		if err != nil {
			return fmt.Errorf("marshal event detail: %w", err)
		}
		detailStr = string(b)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO event_log (category, action, server_id, node_id, detail, operator, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		category, action, serverID, nodeID, detailStr, operator, ip); err != nil {
		return fmt.Errorf("insert event_log: %w", err)
	}
	return nil
}

// ListEvents 按过滤条件倒序（最新在前）分页查询事件日志，返回当前页与总数。
func (s *Store) ListEvents(ctx context.Context, f EventFilter, limit, offset int) ([]EventLog, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	// 动态拼 WHERE；参数顺序与查询/计数两处一致。
	where := ""
	args := []any{}
	if f.Category != "" {
		where += " AND category = ?"
		args = append(args, f.Category)
	}
	if f.ServerID != nil {
		where += " AND server_id = ?"
		args = append(args, *f.ServerID)
	}
	if f.Operator != "" {
		where += " AND operator = ?"
		args = append(args, f.Operator)
	}
	if f.Q != "" {
		where += " AND (action LIKE ? OR detail LIKE ?)"
		args = append(args, "%"+f.Q+"%", "%"+f.Q+"%")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM event_log WHERE 1=1`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count event_log: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, category, action, server_id, node_id, detail, operator, ip
		 FROM event_log WHERE 1=1`+where+`
		 ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("query event_log: %w", err)
	}
	defer rows.Close()

	var items []EventLog
	for rows.Next() {
		var e EventLog
		if err := rows.Scan(&e.ID, &e.Ts, &e.Category, &e.Action, &e.ServerID, &e.NodeID, &e.Detail, &e.Operator, &e.IP); err != nil {
			return nil, 0, fmt.Errorf("scan event_log: %w", err)
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}
