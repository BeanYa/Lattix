package panel

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

// TestCleanupCommandsTieredRetention 验证两级保留期：探测类命令 24h、其余终态命令
// 14 天，而 queued/sent 属于离线补发队列，永不清理。
func TestCleanupCommandsTieredRetention(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "commands.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverAPI := &Server{st: st}

	raw, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	now := time.Now().UTC()
	insert := func(id int64, typ, status string, age time.Duration) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, `INSERT INTO commands (request_id, trace_id, server_id, type, data, status, created_at)
			VALUES (?, 'trace', 1, ?, '{}', ?, ?)`,
			fmt.Sprintf("req-%d", id), typ, status, now.Add(-age).Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, "latency.probe", "acked", 2*time.Hour)      // 保留：24h 窗口内
	insert(2, "latency.probe", "acked", 30*time.Hour)     // 删除：探测类超 24h
	insert(3, "node.apply", "acked", 2*time.Hour)         // 保留：14 天窗口内
	insert(4, "node.apply", "acked", 10*24*time.Hour)     // 保留：14 天窗口内
	insert(5, "node.apply", "failed", 20*24*time.Hour)    // 删除：终态超 14 天
	insert(6, "latency.probe", "queued", 40*24*time.Hour) // 保留：离线队列永不清理
	insert(7, "latency.probe", "sent", 40*24*time.Hour)   // 保留：离线队列永不清理

	if err := serverAPI.cleanupCommands(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := raw.QueryContext(ctx, `SELECT id FROM commands ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	want := []int64{1, 3, 4, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("remaining ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining ids = %v, want %v", got, want)
		}
	}
}
