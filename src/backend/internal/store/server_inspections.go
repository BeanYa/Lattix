package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ServerInspectionRun is the panel-side history record of one automatic server
// inspection. The full report still follows server_test_tasks (latest only);
// summary_json is the compact, long-lived view used by the runtime monitor.
type ServerInspectionRun struct {
	ID           int64
	ServerID     int64
	TaskID       string
	Status       string // queued|accepted|running|succeeded|completed_with_errors|failed|superseded
	SummaryJSON  string
	ErrorCode    string
	ErrorMessage string
	StartedAt    string
	CompletedAt  *string
	UpdatedAt    string
}

func (s *Store) CreateServerInspectionRun(ctx context.Context, serverID int64, taskID string) (*ServerInspectionRun, error) {
	if serverID < 1 || taskID == "" {
		return nil, errors.New("invalid server inspection run")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO server_inspection_runs (server_id, task_id, status)
		VALUES (?, ?, 'queued')`, serverID, taskID); err != nil {
		return nil, fmt.Errorf("create server inspection run: %w", err)
	}
	if err := s.pruneServerInspectionRuns(ctx, serverID); err != nil {
		return nil, err
	}
	return s.ServerInspectionRunByTaskID(ctx, taskID)
}

// CompleteServerInspectionRun records a terminal state and the compact summary.
// It is idempotent for a repeated delivery of the same result.
func (s *Store) CompleteServerInspectionRun(
	ctx context.Context,
	serverID int64,
	taskID string,
	status string,
	summaryJSON string,
	errorCode string,
	errorMessage string,
) error {
	if serverID < 1 || taskID == "" || status == "" {
		return errors.New("invalid server inspection completion")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE server_inspection_runs SET status = ?,
		summary_json = ?, error_code = ?, error_message = ?,
		completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE server_id = ? AND task_id = ? AND status IN ('queued','accepted','running')`,
		status, summaryJSON, errorCode, errorMessage, serverID, taskID)
	if err != nil {
		return fmt.Errorf("complete server inspection run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return s.pruneServerInspectionRuns(ctx, serverID)
}

// SupersedeServerInspectionRun closes a queued/running run whose task was
// replaced or abandoned before a terminal result arrived.
func (s *Store) SupersedeServerInspectionRun(ctx context.Context, serverID int64, taskID, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE server_inspection_runs SET status = 'superseded',
		error_code = 'superseded', error_message = ?, completed_at = CURRENT_TIMESTAMP,
		updated_at = CURRENT_TIMESTAMP
		WHERE server_id = ? AND task_id = ? AND status IN ('queued','accepted','running')`,
		reason, serverID, taskID)
	if err != nil {
		return fmt.Errorf("supersede server inspection run: %w", err)
	}
	return nil
}

func (s *Store) ServerInspectionRunByTaskID(ctx context.Context, taskID string) (*ServerInspectionRun, error) {
	if taskID == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, server_id, task_id, status, summary_json,
		error_code, error_message, started_at, completed_at, updated_at
		FROM server_inspection_runs WHERE task_id = ?`, taskID)
	return scanServerInspectionRun(row)
}

// LatestServerInspectionRun returns the newest run (including queued/failed)
// for a server. The scheduler uses it for the weekly due check; the runtime
// monitor uses the compact summary from the newest completed run.
func (s *Store) LatestServerInspectionRun(ctx context.Context, serverID int64) (*ServerInspectionRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, server_id, task_id, status, summary_json,
		error_code, error_message, started_at, completed_at, updated_at
		FROM server_inspection_runs WHERE server_id = ?
		ORDER BY started_at DESC, id DESC LIMIT 1`, serverID)
	return scanServerInspectionRun(row)
}

// LatestCompletedServerInspectionRun returns the newest terminal run, which is
// what the runtime monitor summary should display.
func (s *Store) LatestCompletedServerInspectionRun(ctx context.Context, serverID int64) (*ServerInspectionRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, server_id, task_id, status, summary_json,
		error_code, error_message, started_at, completed_at, updated_at
		FROM server_inspection_runs WHERE server_id = ?
			AND status IN ('succeeded','completed_with_errors','failed')
		ORDER BY completed_at DESC, id DESC LIMIT 1`, serverID)
	return scanServerInspectionRun(row)
}

func (s *Store) ListServerInspectionRuns(ctx context.Context, serverID int64, limit int) ([]ServerInspectionRun, error) {
	if limit < 1 || limit > 100 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, server_id, task_id, status, summary_json,
		error_code, error_message, started_at, completed_at, updated_at
		FROM server_inspection_runs WHERE server_id = ?
		ORDER BY started_at DESC, id DESC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("list server inspection runs: %w", err)
	}
	defer rows.Close()
	out := make([]ServerInspectionRun, 0, limit)
	for rows.Next() {
		run, err := scanServerInspectionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// PendingServerInspectionRuns returns queued/running runs for startup and
// scheduler reconciliation (the panel may have restarted after the result was
// persisted but before the callback mirrored it).
func (s *Store) PendingServerInspectionRuns(ctx context.Context) ([]ServerInspectionRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, server_id, task_id, status, summary_json,
		error_code, error_message, started_at, completed_at, updated_at
		FROM server_inspection_runs WHERE status IN ('queued','accepted','running')
		ORDER BY server_id, id`)
	if err != nil {
		return nil, fmt.Errorf("list pending server inspection runs: %w", err)
	}
	defer rows.Close()
	out := make([]ServerInspectionRun, 0)
	for rows.Next() {
		run, err := scanServerInspectionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) pruneServerInspectionRuns(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM server_inspection_runs
		WHERE server_id = ? AND id NOT IN (
			SELECT id FROM server_inspection_runs WHERE server_id = ?
			ORDER BY started_at DESC, id DESC LIMIT 26
		)`, serverID, serverID)
	if err != nil {
		return fmt.Errorf("prune server inspection runs: %w", err)
	}
	return nil
}

func scanServerInspectionRun(row interface {
	Scan(...any) error
}) (*ServerInspectionRun, error) {
	var run ServerInspectionRun
	var completedAt, updatedAt sql.NullString
	if err := row.Scan(&run.ID, &run.ServerID, &run.TaskID, &run.Status, &run.SummaryJSON,
		&run.ErrorCode, &run.ErrorMessage, &run.StartedAt, &completedAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan server inspection run: %w", err)
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.String
	}
	if updatedAt.Valid {
		run.UpdatedAt = updatedAt.String
	}
	return &run, nil
}
