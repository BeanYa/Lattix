package store

import (
	"context"
	"testing"
	"time"
)

// TestTrafficStatsBucketAccumulatesSameMinute 验证同一计数器同一分钟内的两次快照
// 增量在分钟桶与日汇总中均累加为一行。
func TestTrafficStatsBucketAccumulatesSameMinute(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	base := time.Date(2026, 10, 15, 10, 0, 10, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 100, Down: 200}}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 160, Down: 350}}, base.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	var bucketTS, up, down int64
	if err := st.db.QueryRow(`SELECT bucket_ts, up, down FROM traffic_stats_bucket
		WHERE server_id=? AND dim_type='user' AND dim_id='u'`, serverID).Scan(&bucketTS, &up, &down); err != nil {
		t.Fatal(err)
	}
	if want := base.Truncate(time.Minute).Unix(); bucketTS != want {
		t.Fatalf("bucket_ts = %d, want %d", bucketTS, want)
	}
	if up != 160 || down != 350 {
		t.Fatalf("bucket up/down = %d/%d, want 160/350", up, down)
	}
	if err := st.db.QueryRow(`SELECT up, down FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='user' AND dim_id='u'`, serverID).Scan(&up, &down); err != nil {
		t.Fatal(err)
	}
	if up != 160 || down != 350 {
		t.Fatalf("daily up/down = %d/%d, want 160/350", up, down)
	}
}

// TestTrafficStatsUserDimUsesResolvedOwner 验证分组派生身份（group:<uuid>:<chain_id>）
// 落桶后的 user 维度 dim_id 为归属用户 UUID，而非原始身份字符串。
func TestTrafficStatsUserDimUsesResolvedOwner(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, endpointID := newTestEndpointChain(t, st, "stats-group")
	member, _ := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "tok-s", nil)
	lgID, err := st.CreateLinkGroup(ctx, "lg", []int64{chainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "ug", []int64{member}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	assignments, err := st.ActiveEndpointAssignments(ctx, endpointID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignments = %+v err %v", assignments, err)
	}
	var serverID int64
	if err := st.db.QueryRow(`SELECT server_id FROM shared_endpoints WHERE id=?`, endpointID).Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "group:1",
		[]TrafficCounterSnapshot{{User: assignments[0].Identity(), Up: 100, Down: 200}}, now); err != nil {
		t.Fatal(err)
	}
	var dimID string
	var up, down int64
	if err := st.db.QueryRow(`SELECT dim_id, up, down FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='user'`, serverID).Scan(&dimID, &up, &down); err != nil {
		t.Fatal(err)
	}
	if dimID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("daily dim_id = %q, want member user uuid", dimID)
	}
	if up != 100 || down != 200 {
		t.Fatalf("daily up/down = %d/%d, want 100/200", up, down)
	}
	if err := st.db.QueryRow(`SELECT dim_id FROM traffic_stats_bucket
		WHERE server_id=? AND dim_type='user'`, serverID).Scan(&dimID); err != nil {
		t.Fatal(err)
	}
	if dimID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("bucket dim_id = %q, want member user uuid", dimID)
	}
}

// TestTrafficStatsInstanceResetMatchesTrafficTable 验证实例重启（换 instanceID）
// 从零计数时，user 维度桶累计与 traffic 表口径一致。
func TestTrafficStatsInstanceResetMatchesTrafficTable(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1", []TrafficCounterSnapshot{{User: "u", Up: 100}}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-2", []TrafficCounterSnapshot{{User: "u", Up: 25}}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var statsUp int64
	if err := st.db.QueryRow(`SELECT SUM(up) FROM traffic_stats_bucket
		WHERE server_id=? AND dim_type='user' AND dim_id='u'`, serverID).Scan(&statsUp); err != nil {
		t.Fatal(err)
	}
	if statsUp != 125 {
		t.Fatalf("stats bucket up = %d, want 125", statsUp)
	}
	if err := st.db.QueryRow(`SELECT up FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='user' AND dim_id='u'`, serverID).Scan(&statsUp); err != nil {
		t.Fatal(err)
	}
	if statsUp != 125 {
		t.Fatalf("stats daily up = %d, want 125", statsUp)
	}
	total, err := st.UserTraffic(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if total.Up != 125 {
		t.Fatalf("traffic table up = %d, want 125", total.Up)
	}
	if statsUp != total.Up {
		t.Fatalf("stats daily up = %d, want %d (match traffic table)", statsUp, total.Up)
	}
}

// TestTrafficStatsDailyTimezoneBoundary 验证日汇总按 SettingTrafficTimezone 本地日期
// 分桶：UTC 15:59 与 16:00（Asia/Shanghai 23:59/次日 00:00）落不同 usage_date。
func TestTrafficStatsDailyTimezoneBoundary(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.SetSetting(ctx, SettingTrafficTimezone, "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	before := time.Date(2026, 10, 15, 15, 59, 30, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 10}}, before); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 30}}, before.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	rows, err := st.db.Query(`SELECT usage_date, up FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='user' AND dim_id='u' ORDER BY usage_date`, serverID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type dailyRow struct {
		date string
		up   int64
	}
	var got []dailyRow
	for rows.Next() {
		var row dailyRow
		if err := rows.Scan(&row.date, &row.up); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("daily rows = %+v, want 2", got)
	}
	if got[0].date != "2026-10-15" || got[0].up != 10 {
		t.Fatalf("first daily row = %+v, want {2026-10-15 10}", got[0])
	}
	if got[1].date != "2026-10-16" || got[1].up != 20 {
		t.Fatalf("second daily row = %+v, want {2026-10-16 20}", got[1])
	}
}
