package store

import (
	"context"
	"encoding/json"
	"strconv"
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

// trafficStatSeedPoint 描述一笔测试种子增量及其采样时刻；
// overview 同进度测试用它按窗口/日期谓词在 Go 侧重算期望值
//（Store 无法注入时钟，窗口锚点只能相对真实 now 构造）。
type trafficStatSeedPoint struct {
	at   time.Time
	up   int64
	down int64
}

// trafficStatSeedWindowSum 汇总分钟桶落在 [from,to) 内的种子增量。
func trafficStatSeedWindowSum(seeds []trafficStatSeedPoint, from, to time.Time) TrafficVolume {
	var volume TrafficVolume
	for _, seed := range seeds {
		ts := seed.at.UTC().Truncate(time.Minute).Unix()
		if ts >= from.Unix() && ts < to.Unix() {
			volume.Up += seed.up
			volume.Down += seed.down
		}
	}
	return volume
}

// trafficStatSeedDateSum 汇总本地格式化日期（"2006-01-02" 或 "2006-01"）恰等于
// prefix 的种子增量，对应日汇总表的分桶口径。
func trafficStatSeedDateSum(seeds []trafficStatSeedPoint, location *time.Location, dateLayout, prefix string) TrafficVolume {
	var volume TrafficVolume
	for _, seed := range seeds {
		if seed.at.In(location).Format(dateLayout) == prefix {
			volume.Up += seed.up
			volume.Down += seed.down
		}
	}
	return volume
}

// TestTrafficStatSeries5mAlignment 验证 5m 粒度按 (bucket_ts/300)*300 对齐：
// 同一 5 分钟窗内的两分钟数据聚合为一桶，TS 为窗口起点。
func TestTrafficStatSeries5mAlignment(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	// 计数器为累计值：第二次快照 400/700 → 增量 300/500，分钟桶合计 400/700。
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 100, Down: 200}}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 400, Down: 700}}, base.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	points, err := st.TrafficStatSeries(ctx, "fleet", 0, "", "5m", base, base.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %+v, want 1 bucket", points)
	}
	if points[0].TS != base.Unix() {
		t.Fatalf("bucket ts = %d, want %d (window start)", points[0].TS, base.Unix())
	}
	if points[0].Up != 400 || points[0].Down != 700 {
		t.Fatalf("bucket up/down = %d/%d, want 400/700", points[0].Up, points[0].Down)
	}
}

// TestTrafficStatSeriesFleetUserDimOnly 验证 fleet 口径只计用户维度（Review Focus #4）：
// user 行 + node 行各 1GB 时仅返回 1GB；附带验证 day/month 粒度走日汇总且 TS 为本地零点。
func TestTrafficStatSeriesFleetUserDimOnly(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	gig := int64(1) << 30
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: gig, Down: gig}}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{NodeID: 42, Up: gig, Down: gig}}, base); err != nil {
		t.Fatal(err)
	}
	points, err := st.TrafficStatSeries(ctx, "fleet", 0, "", "1h", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Up != gig || points[0].Down != gig {
		t.Fatalf("fleet points = %+v, want single 1GiB bucket (user dim only)", points)
	}
	// day 粒度：查日汇总，TS 为本地（默认 Asia/Shanghai）当日零点；仅存在数据的日子有桶。
	_, location := st.TrafficLocation(ctx)
	dayPoints, err := st.TrafficStatSeries(ctx, "fleet", 0, "", "day", base.Add(-24*time.Hour), base.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	localDay := time.Date(2026, 10, 15, 0, 0, 0, 0, location)
	if len(dayPoints) != 1 {
		t.Fatalf("day points = %+v, want 1 bucket (local 2026-10-15 only)", dayPoints)
	}
	if dayPoints[0].TS != localDay.Unix() {
		t.Fatalf("day bucket ts = %d, want %d (local 2026-10-15 midnight)", dayPoints[0].TS, localDay.Unix())
	}
	if dayPoints[0].Up != gig || dayPoints[0].Down != gig {
		t.Fatalf("day bucket up/down = %d/%d, want 1GiB (user dim only)", dayPoints[0].Up, dayPoints[0].Down)
	}
	// month 粒度：按 substr(usage_date,1,7) 分组，TS 为本地当月一日零点。
	monthPoints, err := st.TrafficStatSeries(ctx, "fleet", 0, "", "month", base.Add(-24*time.Hour), base.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	localMonth := time.Date(2026, 10, 1, 0, 0, 0, 0, location)
	if len(monthPoints) != 1 || monthPoints[0].TS != localMonth.Unix() {
		t.Fatalf("month points = %+v, want single bucket ts %d", monthPoints, localMonth.Unix())
	}
	if monthPoints[0].Up != gig || monthPoints[0].Down != gig {
		t.Fatalf("month bucket up/down = %d/%d, want 1GiB", monthPoints[0].Up, monthPoints[0].Down)
	}
}

// TestTrafficStatSeriesServerEntityDim 验证 server 口径为实体维度合计（node/chain_hop/endpoint），
// user 行不计入。
func TestTrafficStatSeriesServerEntityDim(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	gig := int64(1) << 30
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: gig, Down: gig}}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{
			{NodeID: 42, Up: gig, Down: gig},
			{EndpointID: 7, Up: gig, Down: gig},
		}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	points, err := st.TrafficStatSeries(ctx, "server", serverID, "", "1h", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Up != 2*gig || points[0].Down != 2*gig {
		t.Fatalf("server points = %+v, want single bucket 2GiB (node+endpoint, user row excluded)", points)
	}
	// 逐分钟明细：两条实体行同分钟（base+1m）合计为一桶，user 行不计入。
	buckets, err := st.ServerTrafficBuckets(ctx, serverID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].TS != base.Add(time.Minute).Unix() ||
		buckets[0].Up != 2*gig || buckets[0].Down != 2*gig {
		t.Fatalf("server buckets = %+v, want single minute bucket 2GiB at base+1m", buckets)
	}
}

// TestTrafficStatOverviewSameProgress 验证同进度环比窗口（Review Focus #3）：
// TodayPrev 恰为昨日 [startToday-24h, now-24h) 窗口——昨日同时刻之后的数据不得计入；
// MonthPrev = 上月全月 - 上月同进度之后的尾部。Store 无法注入时钟，窗口随真实 now
// 变化，故期望值按相同谓词在种子集合上重算，并以锚点保证两个分支均被覆盖：
// startToday-24h 必在 TodayPrev 窗口内，now-24h+2m 必在其外。
func TestTrafficStatOverviewSameProgress(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	_, location := st.TrafficLocation(ctx)
	now := time.Now().In(location)
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
	startLastMonth := startMonth.AddDate(0, -1, 0)
	elapsed := now.Sub(startMonth)
	seeds := []trafficStatSeedPoint{
		{at: startToday.Add(-24 * time.Hour), up: 100, down: 40},               // 昨日 00:00：必在 TodayPrev 窗口
		{at: now.Add(-24*time.Hour + 2*time.Minute), up: 999, down: 998},        // 昨日同时刻之后：必在 TodayPrev 窗口外
		{at: startToday, up: 7, down: 3},                                       // 今日 00:00：计入 Today
		{at: startLastMonth, up: 500, down: 250},                               // 上月 1 日：计入上月全月，不在同进度尾部窗口
		{at: startMonth.Add(-2 * time.Hour), up: 300, down: 150},               // 上月末尾：落在尾部窗口内被扣除
	}
	for i, seed := range seeds {
		if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
			[]TrafficCounterSnapshot{{User: "seed-" + strconv.Itoa(i), Up: seed.up, Down: seed.down}}, seed.at); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := st.TrafficStatsOverview(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Today != trafficStatSeedDateSum(seeds, location, "2006-01-02", now.Format("2006-01-02")) {
		t.Fatalf("Today = %+v, want %+v", overview.Today, trafficStatSeedDateSum(seeds, location, "2006-01-02", now.Format("2006-01-02")))
	}
	wantTodayPrev := trafficStatSeedWindowSum(seeds, startToday.Add(-24*time.Hour), now.Add(-24*time.Hour))
	if wantTodayPrev.Up != 100 || wantTodayPrev.Down != 40 {
		t.Fatalf("test setup: expected TodayPrev anchors changed, got %+v (window start must be in, now-24h+2m out)", wantTodayPrev)
	}
	if overview.TodayPrev != wantTodayPrev {
		t.Fatalf("TodayPrev = %+v, want %+v (yesterday same-progress window)", overview.TodayPrev, wantTodayPrev)
	}
	if overview.Month != trafficStatSeedDateSum(seeds, location, "2006-01", startMonth.Format("2006-01")) {
		t.Fatalf("Month = %+v, want %+v", overview.Month, trafficStatSeedDateSum(seeds, location, "2006-01", startMonth.Format("2006-01")))
	}
	wantLastMonth := trafficStatSeedDateSum(seeds, location, "2006-01", startLastMonth.Format("2006-01"))
	wantMonthPrev := TrafficVolume{
		Up:   wantLastMonth.Up - trafficStatSeedWindowSum(seeds, startLastMonth.Add(elapsed), startMonth).Up,
		Down: wantLastMonth.Down - trafficStatSeedWindowSum(seeds, startLastMonth.Add(elapsed), startMonth).Down,
	}
	if overview.MonthPrev != wantMonthPrev {
		t.Fatalf("MonthPrev = %+v, want %+v (last month same progress)", overview.MonthPrev, wantMonthPrev)
	}
	if len(overview.TopServers) != 0 || len(overview.TopUsers) != 0 {
		t.Fatalf("top lists = %+v / %+v, want empty (no entity rows / no resolvable users)", overview.TopServers, overview.TopUsers)
	}
}

// TestTrafficStatOverviewTopJoinsLabels 验证 Top 榜 label 联表：
// top_servers → servers.alias，top_users → users.name。
func TestTrafficStatOverviewTopJoinsLabels(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	alpha, _ := st.CreateServer(ctx, ServerDraft{Alias: "alpha", Address: "a.test", BootstrapToken: "tok-a", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	beta, _ := st.CreateServer(ctx, ServerDraft{Alias: "beta", Address: "b.test", BootstrapToken: "tok-b", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if _, err := st.InsertUser(ctx, "alice", "uuid-alice", "sub-alice", nil); err != nil {
		t.Fatal(err)
	}
	_, location := st.TrafficLocation(ctx)
	now := time.Now().In(location)
	seedAt := time.Date(now.Year(), now.Month(), 1, 1, 0, 0, 0, location)
	if err := st.ApplyTrafficSnapshot(ctx, alpha, "inst-a",
		[]TrafficCounterSnapshot{{User: "uuid-alice", Up: 200, Down: 100}}, seedAt); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, beta, "inst-b",
		[]TrafficCounterSnapshot{{User: "uuid-alice", Up: 50, Down: 25}}, seedAt); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, alpha, "inst-a",
		[]TrafficCounterSnapshot{{NodeID: 9, Up: 700, Down: 300}}, seedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	overview, err := st.TrafficStatsOverview(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	wantServers := []TrafficStatTopItem{{ServerID: alpha, UserUUID: "", Label: "alpha", Up: 700, Down: 300}}
	if len(overview.TopServers) != len(wantServers) {
		t.Fatalf("top_servers = %+v, want %+v", overview.TopServers, wantServers)
	}
	for i, want := range wantServers {
		if overview.TopServers[i] != want {
			t.Fatalf("top_servers[%d] = %+v, want %+v", i, overview.TopServers[i], want)
		}
	}
	wantUsers := []TrafficStatTopItem{{ServerID: 0, UserUUID: "uuid-alice", Label: "alice", Up: 250, Down: 125}}
	if len(overview.TopUsers) != len(wantUsers) {
		t.Fatalf("top_users = %+v, want %+v", overview.TopUsers, wantUsers)
	}
	for i, want := range wantUsers {
		if overview.TopUsers[i] != want {
			t.Fatalf("top_users[%d] = %+v, want %+v", i, overview.TopUsers[i], want)
		}
	}
}

// TestTrafficStatBreakdownByServerAndUser 验证下钻双向分组与 label：
// 按服务器 → user/node/endpoint 各维度行（users.name / nodes.name / 空串）；
// 按用户 → GROUP BY server_id（dim_type="server"，servers.alias）。
func TestTrafficStatBreakdownByServerAndUser(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	alpha, _ := st.CreateServer(ctx, ServerDraft{Alias: "alpha", Address: "a.test", BootstrapToken: "tok-a", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	beta, _ := st.CreateServer(ctx, ServerDraft{Alias: "beta", Address: "b.test", BootstrapToken: "tok-b", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if _, err := st.InsertUser(ctx, "alice", "uuid-alice", "sub-alice", nil); err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "node-1", alpha, "vless", nil, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, alpha, "inst-a",
		[]TrafficCounterSnapshot{{User: "uuid-alice", Up: 100, Down: 50}}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, alpha, "inst-a",
		[]TrafficCounterSnapshot{{NodeID: nodeID, Up: 800, Down: 400}}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, alpha, "inst-a",
		[]TrafficCounterSnapshot{{EndpointID: 55, Up: 10, Down: 5}}, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, beta, "inst-b",
		[]TrafficCounterSnapshot{{User: "uuid-alice", Up: 300, Down: 150}}, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	serverRows, err := st.TrafficStatBreakdown(ctx, alpha, "", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantServerRows := []TrafficStatBreakdownRow{
		{DimType: "node", DimID: strconv.FormatInt(nodeID, 10), Label: "node-1", Up: 800, Down: 400},
		{DimType: "user", DimID: "uuid-alice", Label: "alice", Up: 100, Down: 50},
		{DimType: "endpoint", DimID: "55", Label: "", Up: 10, Down: 5},
	}
	if len(serverRows) != len(wantServerRows) {
		t.Fatalf("server breakdown = %+v, want %+v", serverRows, wantServerRows)
	}
	for i, want := range wantServerRows {
		if serverRows[i] != want {
			t.Fatalf("server breakdown[%d] = %+v, want %+v", i, serverRows[i], want)
		}
	}
	userRows, err := st.TrafficStatBreakdown(ctx, 0, "uuid-alice", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantUserRows := []TrafficStatBreakdownRow{
		{DimType: "server", DimID: strconv.FormatInt(beta, 10), Label: "beta", Up: 300, Down: 150},
		{DimType: "server", DimID: strconv.FormatInt(alpha, 10), Label: "alpha", Up: 100, Down: 50},
	}
	if len(userRows) != len(wantUserRows) {
		t.Fatalf("user breakdown = %+v, want %+v", userRows, wantUserRows)
	}
	for i, want := range wantUserRows {
		if userRows[i] != want {
			t.Fatalf("user breakdown[%d] = %+v, want %+v", i, userRows[i], want)
		}
	}
}

// TestDeleteExpiredTrafficStatBucketsKeepsDaily 验证保留清理只删分钟桶（Review Focus #5
// 后半）：61 天前的旧桶在 delete(60) 后消失，同笔数据的 daily 行仍在（日汇总永不清理），
// 当前新桶不受影响。Store 无法注入时钟，截止线相对真实 now 构造（61 天 vs 60 天，留足余量）。
func TestDeleteExpiredTrafficStatBucketsKeepsDaily(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	now := time.Now()
	old := now.Add(-61 * 24 * time.Hour)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "old-u", Up: 100, Down: 50}}, old); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "new-u", Up: 30, Down: 20}}, now); err != nil {
		t.Fatal(err)
	}
	removed, err := st.DeleteExpiredTrafficStatBuckets(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed rows = %d, want 1 (only the 61-day-old bucket)", removed)
	}
	var expiredBuckets int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_stats_bucket WHERE dim_id='old-u'`).Scan(&expiredBuckets); err != nil {
		t.Fatal(err)
	}
	if expiredBuckets != 0 {
		t.Fatalf("expired bucket rows = %d, want 0", expiredBuckets)
	}
	var oldDaily int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_stats_daily WHERE dim_id='old-u'`).Scan(&oldDaily); err != nil {
		t.Fatal(err)
	}
	if oldDaily != 1 {
		t.Fatalf("old daily rows = %d, want 1 (daily rows are never pruned)", oldDaily)
	}
	var freshUp, freshDown int64
	if err := st.db.QueryRow(`SELECT up, down FROM traffic_stats_bucket WHERE dim_id='new-u'`).Scan(&freshUp, &freshDown); err != nil {
		t.Fatal(err)
	}
	if freshUp != 30 || freshDown != 20 {
		t.Fatalf("fresh bucket up/down = %d/%d, want 30/20 (untouched)", freshUp, freshDown)
	}
}

// TestTrafficStatsRetentionDaysFallback 验证保留天数设置读取：未设置、非数字与非正值
// 均回落 DefaultTrafficStatsRetentionDays（60），合法数字原样返回。
func TestTrafficStatsRetentionDaysFallback(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, raw := range []string{"", "junk", "-5", "0"} {
		if raw != "" {
			if err := st.SetSetting(ctx, SettingTrafficStatsRetentionDays, raw); err != nil {
				t.Fatal(err)
			}
		}
		days, err := st.TrafficStatsRetentionDays(ctx)
		if err != nil {
			t.Fatalf("retention days (setting %q) err = %v", raw, err)
		}
		if days != DefaultTrafficStatsRetentionDays {
			t.Fatalf("retention days (setting %q) = %d, want default %d", raw, days, DefaultTrafficStatsRetentionDays)
		}
	}
	if err := st.SetSetting(ctx, SettingTrafficStatsRetentionDays, "90"); err != nil {
		t.Fatal(err)
	}
	days, err := st.TrafficStatsRetentionDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if days != 90 {
		t.Fatalf("retention days = %d, want 90", days)
	}
}

// TestTrafficStatEmptyRangeReturnsEmpty 验证空范围（from>=to 或无数据）返回空切片而非
// nil/error（Review Focus #5 前半）。
func TestTrafficStatEmptyRangeReturnsEmpty(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "server.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US", Location: "Test"})
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1",
		[]TrafficCounterSnapshot{{User: "u", Up: 100, Down: 200}}, base); err != nil {
		t.Fatal(err)
	}
	for _, granularity := range []TrafficStatGranularity{"5m", "day"} {
		points, err := st.TrafficStatSeries(ctx, "fleet", 0, "", granularity, base, base)
		if err != nil {
			t.Fatalf("series %s zero-width range err = %v", granularity, err)
		}
		if points == nil || len(points) != 0 {
			t.Fatalf("series %s zero-width range = %+v, want empty non-nil slice", granularity, points)
		}
		// 无数据窗口：5m 取数据前的同日窗口；day 取完全不重叠的本地日期
		//（day 粒度按本地日含端点匹配，与窗口重叠的本地日整日计入是既定语义）。
		noFrom, noTo := base.Add(-2*time.Hour), base.Add(-time.Hour)
		if granularity == "day" {
			noFrom, noTo = base.Add(-72*time.Hour), base.Add(-48*time.Hour)
		}
		points, err = st.TrafficStatSeries(ctx, "fleet", 0, "", granularity, noFrom, noTo)
		if err != nil {
			t.Fatalf("series %s no-data range err = %v", granularity, err)
		}
		if points == nil || len(points) != 0 {
			t.Fatalf("series %s no-data range = %+v, want empty non-nil slice", granularity, points)
		}
	}
	buckets, err := st.ServerTrafficBuckets(ctx, serverID, base, base)
	if err != nil {
		t.Fatal(err)
	}
	if buckets == nil || len(buckets) != 0 {
		t.Fatalf("server buckets zero-width range = %+v, want empty non-nil slice", buckets)
	}
}
