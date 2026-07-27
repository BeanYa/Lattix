package logging

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Category string

const (
	CategoryServer   Category = "server"
	CategoryChain    Category = "chain"
	CategoryUser     Category = "user"
	CategorySettings Category = "settings"
	CategoryPanel    Category = "panel"
	CategoryAgent    Category = "agent"
	CategoryCommand  Category = "command"
	CategoryAuth     Category = "auth"
	CategoryLog      Category = "log"
)

const operationSchema = `
CREATE TABLE IF NOT EXISTS operation_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    TEXT NOT NULL UNIQUE,
    ts          TEXT NOT NULL,
    severity    TEXT NOT NULL,
    category    TEXT NOT NULL,
    action      TEXT NOT NULL,
    server_id   INTEGER,
    node_id     INTEGER,
    detail      TEXT NOT NULL DEFAULT '',
    operator    TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL DEFAULT '',
    trace_id    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_operation_log_ts ON operation_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_operation_log_severity ON operation_log(severity);
CREATE INDEX IF NOT EXISTS idx_operation_log_category ON operation_log(category);

CREATE TABLE IF NOT EXISTS runtime_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

const runMarkerKey = "active_run"

type OperationEntry struct {
	ID        int64
	EventID   string
	Timestamp time.Time
	Severity  Severity
	Category  Category
	Action    string
	ServerID  *int64
	NodeID    *int64
	Detail    string
	Operator  string
	IP        string
	RequestID string
	TraceID   string
}

type OperationEvent struct {
	Severity  Severity
	Category  Category
	Action    string
	ServerID  *int64
	NodeID    *int64
	Detail    any
	Operator  string
	IP        string
	RequestID string
	TraceID   string
}

type OperationFilter struct {
	Severity Severity
	Category Category
	ServerID *int64
	Operator string
	Query    string
	From     string
	To       string
}

type OperationStore struct {
	db *sql.DB

	mu         sync.Mutex
	maxEntries int
}

func OpenOperationStore(path string, maxEntries int) (*OperationStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create operation log directory: %w", err)
	}
	dsn := path + "?_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open operation log: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(operationSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init operation log: %w", err)
	}
	s := &OperationStore{db: db, maxEntries: normalizeOperationLimit(maxEntries)}
	if err := s.trim(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *OperationStore) Close() error {
	return s.db.Close()
}

func (s *OperationStore) Record(ctx context.Context, event OperationEvent) error {
	detail, err := marshalDetail(event.Detail)
	if err != nil {
		return err
	}
	entry := OperationEntry{
		EventID:   newID(),
		Timestamp: time.Now().UTC(),
		Severity:  event.Severity,
		Category:  event.Category,
		Action:    event.Action,
		ServerID:  event.ServerID,
		NodeID:    event.NodeID,
		Detail:    detail,
		Operator:  event.Operator,
		IP:        event.IP,
		RequestID: event.RequestID,
		TraceID:   event.TraceID,
	}
	if entry.Severity == "" {
		entry.Severity = SeverityInfo
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin operation log write: %w", err)
	}
	defer tx.Rollback()
	if err := insertOperation(ctx, tx, entry); err != nil {
		return err
	}
	if err := trimOperation(ctx, tx, s.maxEntries); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operation log write: %w", err)
	}
	return nil
}

func (s *OperationStore) List(ctx context.Context, filter OperationFilter, limit, offset int) ([]OperationEntry, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	where, args := operationWhere(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operation_log WHERE 1=1`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count operation log: %w", err)
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, ts, severity, category, action, server_id, node_id,
		       detail, operator, ip, request_id, trace_id
		FROM operation_log WHERE 1=1`+where+`
		ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query operation log: %w", err)
	}
	defer rows.Close()
	items := make([]OperationEntry, 0, limit)
	for rows.Next() {
		var item OperationEntry
		var ts string
		var serverID, nodeID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.EventID, &ts, &item.Severity, &item.Category,
			&item.Action, &serverID, &nodeID, &item.Detail, &item.Operator, &item.IP,
			&item.RequestID, &item.TraceID); err != nil {
			return nil, 0, fmt.Errorf("scan operation log: %w", err)
		}
		item.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		if serverID.Valid {
			value := serverID.Int64
			item.ServerID = &value
		}
		if nodeID.Valid {
			value := nodeID.Int64
			item.NodeID = &value
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *OperationStore) Clear(ctx context.Context, actor OperationEvent) error {
	detail, err := marshalDetail(actor.Detail)
	if err != nil {
		return err
	}
	entry := OperationEntry{
		EventID:   newID(),
		Timestamp: time.Now().UTC(),
		Severity:  SeverityInfo,
		Category:  CategoryLog,
		Action:    "operation_log.cleared",
		Detail:    detail,
		Operator:  actor.Operator,
		IP:        actor.IP,
		RequestID: actor.RequestID,
		TraceID:   actor.TraceID,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clearing operation log: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_log`); err != nil {
		return fmt.Errorf("clear operation log: %w", err)
	}
	if err := insertOperation(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OperationStore) SetMaxEntries(ctx context.Context, maxEntries int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxEntries = normalizeOperationLimit(maxEntries)
	return trimOperation(ctx, s.db, s.maxEntries)
}

func (s *OperationStore) MaxEntries() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxEntries
}

func (s *OperationStore) StartRun(ctx context.Context, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin panel start log: %w", err)
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT value FROM runtime_state WHERE key = ?`, runMarkerKey).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read panel run marker: %w", err)
	}
	if err == nil {
		if err := insertOperation(ctx, tx, OperationEntry{
			EventID:   newID(),
			Timestamp: time.Now().UTC(),
			Severity:  SeverityWarning,
			Category:  CategoryPanel,
			Action:    "panel.unclean_shutdown",
			Detail:    previous,
		}); err != nil {
			return err
		}
	}
	marker, _ := json.Marshal(map[string]any{
		"started_at": time.Now().UTC().Format(time.RFC3339Nano),
		"pid":        os.Getpid(),
		"version":    version,
	})
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, runMarkerKey, string(marker)); err != nil {
		return fmt.Errorf("write panel run marker: %w", err)
	}
	if err := insertOperation(ctx, tx, OperationEntry{
		EventID:   newID(),
		Timestamp: time.Now().UTC(),
		Severity:  SeverityInfo,
		Category:  CategoryPanel,
		Action:    "panel.started",
		Detail:    fmt.Sprintf(`{"version":%q,"pid":%d}`, version, os.Getpid()),
	}); err != nil {
		return err
	}
	if err := trimOperation(ctx, tx, s.maxEntries); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OperationStore) StopRun(ctx context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin panel stop log: %w", err)
	}
	defer tx.Rollback()
	detail, _ := json.Marshal(map[string]string{"reason": reason})
	if err := insertOperation(ctx, tx, OperationEntry{
		EventID:   newID(),
		Timestamp: time.Now().UTC(),
		Severity:  SeverityInfo,
		Category:  CategoryPanel,
		Action:    "panel.stopped",
		Detail:    string(detail),
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_state WHERE key = ?`, runMarkerKey); err != nil {
		return fmt.Errorf("clear panel run marker: %w", err)
	}
	if err := trimOperation(ctx, tx, s.maxEntries); err != nil {
		return err
	}
	return tx.Commit()
}

func insertOperation(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, entry OperationEntry) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO operation_log (
			event_id, ts, severity, category, action, server_id, node_id,
			detail, operator, ip, request_id, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.EventID, entry.Timestamp.Format(time.RFC3339Nano), entry.Severity,
		entry.Category, entry.Action, entry.ServerID, entry.NodeID, entry.Detail,
		entry.Operator, entry.IP, entry.RequestID, entry.TraceID)
	if err != nil {
		return fmt.Errorf("insert operation log: %w", err)
	}
	return nil
}

func (s *OperationStore) trim(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return trimOperation(ctx, s.db, s.maxEntries)
}

func trimOperation(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, maxEntries int) error {
	_, err := exec.ExecContext(ctx, `
		DELETE FROM operation_log
		WHERE id <= COALESCE((
			SELECT id FROM operation_log ORDER BY ts DESC, id DESC LIMIT 1 OFFSET ?
		), 0)`, maxEntries)
	if err != nil {
		return fmt.Errorf("trim operation log: %w", err)
	}
	return nil
}

func operationWhere(filter OperationFilter) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, 10)
	if filter.Severity != "" {
		b.WriteString(" AND severity = ?")
		args = append(args, filter.Severity)
	}
	if filter.Category != "" {
		b.WriteString(" AND category = ?")
		args = append(args, filter.Category)
	}
	if filter.ServerID != nil {
		b.WriteString(" AND server_id = ?")
		args = append(args, *filter.ServerID)
	}
	if filter.Operator != "" {
		b.WriteString(" AND operator = ?")
		args = append(args, filter.Operator)
	}
	if filter.Query != "" {
		b.WriteString(" AND (action LIKE ? OR detail LIKE ? OR request_id LIKE ?)")
		like := "%" + filter.Query + "%"
		args = append(args, like, like, like)
	}
	if filter.From != "" {
		b.WriteString(" AND ts >= ?")
		args = append(args, filter.From)
	}
	if filter.To != "" {
		b.WriteString(" AND ts <= ?")
		args = append(args, filter.To)
	}
	return b.String(), args
}

func marshalDetail(detail any) (string, error) {
	switch value := detail.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case json.RawMessage:
		return string(value), nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("marshal operation detail: %w", err)
		}
		return string(data), nil
	}
}

func normalizeOperationLimit(value int) int {
	if value < 100 {
		return 100
	}
	if value > 100000 {
		return 100000
	}
	return value
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
