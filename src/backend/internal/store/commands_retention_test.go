package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteTerminalCommandsKeepsQueueAndFreshRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	insert := func(id int64, status, createdAt string) {
		t.Helper()
		if _, err := st.db.Exec(`INSERT INTO commands (request_id, trace_id, server_id, type, data, status, created_at)
			VALUES (?, 'trace', 1, 'latency.probe', '{}', ?, ?)`, id, status, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, CommandStatusAcked, "2026-01-01 00:00:00")
	insert(2, CommandStatusFailed, "2026-01-02 00:00:00")
	insert(3, CommandStatusAbandoned, "2026-01-03 00:00:00")
	insert(4, CommandStatusAcked, "2026-06-01 00:00:00")
	insert(5, CommandStatusQueued, "2026-01-01 00:00:00")
	insert(6, CommandStatusSent, "2026-01-01 00:00:00")

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	pending, err := st.CountTerminalCommands(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 3 {
		t.Fatalf("CountTerminalCommands = %d, want 3", pending)
	}

	deleted, err := st.DeleteTerminalCommands(ctx, cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}

	rows, err := st.db.Query(`SELECT status FROM commands ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		got = append(got, status)
	}
	want := []string{CommandStatusAcked, CommandStatusQueued, CommandStatusSent}
	if len(got) != len(want) {
		t.Fatalf("remaining rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining rows = %v, want %v", got, want)
		}
	}
}

func TestDeleteTerminalCommandsRespectsBatchLimit(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 1; i <= 5; i++ {
		if _, err := st.db.Exec(`INSERT INTO commands (request_id, trace_id, server_id, type, data, status, created_at)
			VALUES (?, 'trace', 1, 'ping', '{}', 'acked', '2026-01-01 00:00:00')`, i); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := st.DeleteTerminalCommands(ctx, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("first batch deleted = %d, want 2", deleted)
	}
	var left int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 3 {
		t.Fatalf("rows left = %d, want 3", left)
	}
}

func TestOpenUsesWALAndSingleConnectionForMemory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var mode string
	if err := st.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if got := st.db.Stats().MaxOpenConnections; got != sqliteReadPoolSize {
		t.Fatalf("max open connections = %d, want %d", got, sqliteReadPoolSize)
	}

	mem, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	if got := mem.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("in-memory max open connections = %d, want 1", got)
	}
}

func TestOpenAnalyzesPlannerStats(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stats.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := st.db.Exec(`INSERT INTO traffic_stats_bucket (server_id, dim_type, dim_id, bucket_ts, up, down)
			VALUES (1, 'user', ?, ?, 1, 2)`, "uuid", 1760000000+i); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 清掉统计后重开：应自动 ANALYZE 并把统计写回。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM sqlite_stat1"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var rows int
	if err := st2.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl = 'traffic_stats_bucket'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows == 0 {
		t.Fatal("expected planner stats for traffic_stats_bucket after reopen")
	}
}
