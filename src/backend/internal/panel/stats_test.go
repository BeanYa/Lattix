package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
)

// statsFixture 构建内存 store + 两台服务器 + 一个用户的面板测试桩。
func statsFixture(t *testing.T) (*store.Store, *Server, int64, int64, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	createServer := func(alias string) int64 {
		t.Helper()
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: "token-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, bID := createServer("stats-a"), createServer("stats-b")
	userUUID := "10000000-0000-0000-0000-000000000001"
	if _, err := st.InsertUser(ctx, "alice", userUUID, "tok-stats", nil); err != nil {
		t.Fatal(err)
	}
	requester := &settingsRequester{online: map[int64]bool{aID: true}}
	serverAPI := &Server{st: st, req: requester}
	return st, serverAPI, aID, bID, userUUID
}

// seedStatsTraffic 在 t0（整分钟）落桶：
//   - 服务器 a：用户维度 1000/2000 + endpoint 实体维度 300/600
//   - 服务器 b：用户维度 500/900
//
// 返回 t0。overview 的 today/month 走日汇总（usage_date=t0 本地日）。
func seedStatsTraffic(t *testing.T, st *store.Store, aID, bID int64, userUUID string) time.Time {
	t.Helper()
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := st.ApplyTrafficSnapshot(ctx, aID, "instance-stats-a", []store.TrafficCounterSnapshot{
		{User: userUUID, Up: 1000, Down: 2000},
		{EndpointID: 7, Up: 300, Down: 600},
	}, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, bID, "instance-stats-b", []store.TrafficCounterSnapshot{
		{User: userUUID, Up: 500, Down: 900},
	}, t0); err != nil {
		t.Fatal(err)
	}
	return t0
}

func callStatsHandler(t *testing.T, serverAPI *Server, handler http.HandlerFunc, rawQuery string) rpcEnvelope {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil))
	return decodeRPC(t, recorder)
}

func TestStatsTrafficOverviewReturnsVolumesTopsAndAgents(t *testing.T) {
	_, serverAPI, aID, bID, userUUID := statsFixture(t)
	seedStatsTraffic(t, serverAPI.st, aID, bID, userUUID)

	env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficOverview, "")
	if env.Code != "OK" {
		t.Fatalf("code = %q message = %q, want OK", env.Code, env.Message)
	}
	var data struct {
		Today        store.TrafficVolume        `json:"today"`
		Month        store.TrafficVolume        `json:"month"`
		TopServers   []store.TrafficStatTopItem `json:"top_servers"`
		TopUsers     []store.TrafficStatTopItem `json:"top_users"`
		AgentsOnline int                        `json:"agents_online"`
		AgentsTotal  int                        `json:"agents_total"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode overview data: %v, data = %s", err, env.Data)
	}
	// today/month 走用户维度日汇总：a(1000/2000) + b(500/900)。
	if data.Today.Up != 1500 || data.Today.Down != 2900 {
		t.Fatalf("today = %+v, want up 1500 down 2900", data.Today)
	}
	if data.Month.Up != 1500 || data.Month.Down != 2900 {
		t.Fatalf("month = %+v, want up 1500 down 2900", data.Month)
	}
	// agents_online/total 与 runtime.go 同源：ListServers + requester.IsOnline。
	if data.AgentsOnline != 1 || data.AgentsTotal != 2 {
		t.Fatalf("agents online/total = %d/%d, want 1/2", data.AgentsOnline, data.AgentsTotal)
	}
	// Top 服务器只计实体维度：a 的 endpoint 300/600；b 无实体维度流量不出榜。
	if len(data.TopServers) != 1 || data.TopServers[0].ServerID != aID ||
		data.TopServers[0].Label != "stats-a" || data.TopServers[0].Up != 300 || data.TopServers[0].Down != 600 {
		t.Fatalf("top_servers = %+v", data.TopServers)
	}
	if len(data.TopUsers) != 1 || data.TopUsers[0].UserUUID != userUUID ||
		data.TopUsers[0].Label != "alice" || data.TopUsers[0].Up != 1500 || data.TopUsers[0].Down != 2900 {
		t.Fatalf("top_users = %+v", data.TopUsers)
	}
	// 匿名嵌入打平：agents_online 必须出现在 data 顶层而非嵌套对象。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["agents_online"]; !ok {
		t.Fatalf("overview data 缺少顶层 agents_online 键: %s", env.Data)
	}
	if _, ok := raw["TrafficStatsOverview"]; ok {
		t.Fatalf("overview data 出现未打平的嵌套键: %s", env.Data)
	}
}

func TestStatsTrafficSeriesDimsAndGranularities(t *testing.T) {
	_, serverAPI, aID, bID, userUUID := statsFixture(t)
	t0 := seedStatsTraffic(t, serverAPI.st, aID, bID, userUUID)

	decodePoints := func(t *testing.T, env rpcEnvelope) []store.TrafficStatPoint {
		t.Helper()
		if env.Code != "OK" {
			t.Fatalf("code = %q message = %q, want OK", env.Code, env.Message)
		}
		var points []store.TrafficStatPoint
		if err := json.Unmarshal(env.Data, &points); err != nil {
			t.Fatalf("decode series points: %v, data = %s", err, env.Data)
		}
		return points
	}

	// fleet 默认窗（5m→6h，to=now）：用户维度两台服务器合计；ts 为 5 分钟窗起始。
	env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries, "dim=fleet&granularity=5m")
	points := decodePoints(t, env)
	windowTS := t0.Unix() - t0.Unix()%300
	if len(points) != 1 || points[0].TS != windowTS || points[0].Up != 1500 || points[0].Down != 2900 {
		t.Fatalf("fleet 5m points = %+v, want one point {%d 1500 2900}", points, windowTS)
	}

	// server dim：实体维度（endpoint）合计，用户维度不计入。
	env = callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries,
		"dim=server&server_id=1&granularity=5m&from="+itoa64(t0.Add(-time.Minute).Unix())+"&to="+itoa64(t0.Add(time.Minute).Unix()))
	points = decodePoints(t, env)
	if len(points) != 1 || points[0].Up != 300 || points[0].Down != 600 {
		t.Fatalf("server dim points = %+v, want {300 600}", points)
	}

	// user dim：跨服务器用户维度合计。
	env = callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries,
		"dim=user&user_uuid="+userUUID+"&granularity=15m&from="+itoa64(t0.Add(-time.Minute).Unix())+"&to="+itoa64(t0.Add(time.Minute).Unix()))
	points = decodePoints(t, env)
	if len(points) != 1 || points[0].Up != 1500 || points[0].Down != 2900 {
		t.Fatalf("user dim points = %+v, want {1500 2900}", points)
	}

	// day 粒度走日汇总：默认窗（30 天，to=now），ts 为本地当日零点。
	env = callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries, "dim=fleet&granularity=day")
	points = decodePoints(t, env)
	if len(points) != 1 || points[0].Up != 1500 || points[0].Down != 2900 {
		t.Fatalf("fleet day points = %+v, want one point {1500 2900}", points)
	}
	_, location := serverAPI.st.TrafficLocation(context.Background())
	local := t0.In(location)
	startOfDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	if points[0].TS != startOfDay.Unix() {
		t.Fatalf("day point ts = %d, want local midnight %d", points[0].TS, startOfDay.Unix())
	}
}

func itoa64(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestStatsTrafficSeriesValidation(t *testing.T) {
	_, serverAPI, aID, _, userUUID := statsFixture(t)
	validFrom := itoa64(time.Now().Add(-time.Hour).Unix())
	validTo := itoa64(time.Now().Unix())
	cases := []struct {
		name     string
		rawQuery string
	}{
		{"missing dim", "granularity=5m"},
		{"unknown dim", "dim=bogus&granularity=5m"},
		{"missing granularity", "dim=fleet"},
		{"unknown granularity", "dim=fleet&granularity=2m"},
		{"server dim without server_id", "dim=server&granularity=5m"},
		{"server dim with invalid server_id", "dim=server&server_id=abc&granularity=5m"},
		{"user dim without user_uuid", "dim=user&granularity=5m"},
		{"from after to", "dim=fleet&granularity=5m&from=" + validTo + "&to=" + validFrom},
		{"from equals to", "dim=fleet&granularity=5m&from=" + validTo + "&to=" + validTo},
		{"invalid from", "dim=fleet&granularity=5m&from=abc&to=" + validTo},
	}
	for _, testCase := range cases {
		env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries, testCase.rawQuery)
		if env.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s: code = %q, want INVALID_ARGUMENT", testCase.name, env.Code)
		}
	}
	// 合法请求不应被误伤。
	env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries,
		"dim=server&server_id="+itoa64(aID)+"&granularity=5m&from="+validFrom+"&to="+validTo)
	if env.Code != "OK" {
		t.Fatalf("valid server dim request: code = %q message = %q", env.Code, env.Message)
	}
	env = callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficSeries,
		"dim=user&user_uuid="+userUUID+"&granularity=month")
	if env.Code != "OK" {
		t.Fatalf("valid user dim request: code = %q message = %q", env.Code, env.Message)
	}
}

func TestStatsTrafficBreakdown(t *testing.T) {
	_, serverAPI, aID, bID, userUUID := statsFixture(t)
	t0 := seedStatsTraffic(t, serverAPI.st, aID, bID, userUUID)

	decodeRows := func(t *testing.T, env rpcEnvelope) []store.TrafficStatBreakdownRow {
		t.Helper()
		if env.Code != "OK" {
			t.Fatalf("code = %q message = %q, want OK", env.Code, env.Message)
		}
		var rows []store.TrafficStatBreakdownRow
		if err := json.Unmarshal(env.Data, &rows); err != nil {
			t.Fatalf("decode breakdown rows: %v, data = %s", err, env.Data)
		}
		return rows
	}

	// 按用户下钻：GROUP BY server_id，按 up+down 降序。
	rows := decodeRows(t, callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficBreakdown,
		"user_uuid="+userUUID+"&from="+itoa64(t0.Add(-time.Minute).Unix())+"&to="+itoa64(t0.Add(time.Minute).Unix())))
	if len(rows) != 2 {
		t.Fatalf("user breakdown rows = %+v, want 2", rows)
	}
	if rows[0].DimType != "server" || rows[0].DimID != itoa64(aID) || rows[0].Label != "stats-a" ||
		rows[0].Up != 1000 || rows[0].Down != 2000 {
		t.Fatalf("user breakdown first row = %+v", rows[0])
	}
	if rows[1].DimID != itoa64(bID) || rows[1].Up != 500 || rows[1].Down != 900 {
		t.Fatalf("user breakdown second row = %+v", rows[1])
	}

	// 按服务器下钻：该服务器全部维度行（用户行 JOIN users.name）。
	rows = decodeRows(t, callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficBreakdown,
		"server_id="+itoa64(aID)+"&from="+itoa64(t0.Add(-time.Minute).Unix())+"&to="+itoa64(t0.Add(time.Minute).Unix())))
	if len(rows) != 2 {
		t.Fatalf("server breakdown rows = %+v, want 2", rows)
	}
	if rows[0].DimType != "user" || rows[0].DimID != userUUID || rows[0].Label != "alice" ||
		rows[0].Up != 1000 || rows[0].Down != 2000 {
		t.Fatalf("server breakdown first row = %+v", rows[0])
	}
	if rows[1].DimType != "endpoint" || rows[1].DimID != "7" || rows[1].Label != "" ||
		rows[1].Up != 300 || rows[1].Down != 600 {
		t.Fatalf("server breakdown second row = %+v", rows[1])
	}

	// 默认窗（无 from/to）覆盖最近落桶数据。
	rows = decodeRows(t, callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficBreakdown, "user_uuid="+userUUID))
	if len(rows) != 2 {
		t.Fatalf("user breakdown default window rows = %+v, want 2", rows)
	}

	// 校验：缺 server_id/user_uuid、from>=to。
	for name, rawQuery := range map[string]string{
		"missing target":    "",
		"from equals to":    "server_id=" + itoa64(aID) + "&from=100&to=100",
		"invalid server_id": "server_id=abc",
		"invalid from":      "server_id=" + itoa64(aID) + "&from=abc&to=200",
	} {
		env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficBreakdown, rawQuery)
		if env.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s: code = %q, want INVALID_ARGUMENT", name, env.Code)
		}
	}
}

func TestStatsTrafficServerHistory(t *testing.T) {
	_, serverAPI, aID, bID, userUUID := statsFixture(t)
	t0 := seedStatsTraffic(t, serverAPI.st, aID, bID, userUUID)

	decodePoints := func(t *testing.T, env rpcEnvelope) []store.TrafficStatPoint {
		t.Helper()
		if env.Code != "OK" {
			t.Fatalf("code = %q message = %q, want OK", env.Code, env.Message)
		}
		var points []store.TrafficStatPoint
		if err := json.Unmarshal(env.Data, &points); err != nil {
			t.Fatalf("decode server history points: %v, data = %s", err, env.Data)
		}
		return points
	}

	// 默认 hours=24：逐分钟实体维度合计（用户维度不计入）。
	points := decodePoints(t, callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficServerHistory,
		"server_id="+itoa64(aID)))
	if len(points) != 1 || points[0].TS != t0.Unix() || points[0].Up != 300 || points[0].Down != 600 {
		t.Fatalf("server history points = %+v, want one point {t0 300 600}", points)
	}
	points = decodePoints(t, callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficServerHistory,
		"server_id="+itoa64(aID)+"&hours=168"))
	if len(points) != 1 {
		t.Fatalf("hours=168 points = %+v, want one point", points)
	}

	for name, rawQuery := range map[string]string{
		"missing server_id": "",
		"invalid server_id": "server_id=abc",
		"hours zero":        "server_id=1&hours=0",
		"hours too large":   "server_id=1&hours=169",
		"hours invalid":     "server_id=1&hours=abc",
	} {
		env := callStatsHandler(t, serverAPI, serverAPI.handleStatsTrafficServerHistory, rawQuery)
		if env.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s: code = %q, want INVALID_ARGUMENT", name, env.Code)
		}
	}
}

func TestStatsErrorsSummaryCountsTodayErrors(t *testing.T) {
	_, serverAPI, _, _, _ := statsFixture(t)
	opLog, err := logging.OpenOperationStore(filepath.Join(t.TempDir(), "oplog.db"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { opLog.Close() })
	serverAPI.opLog = opLog
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := opLog.Record(ctx, logging.OperationEvent{
			Severity: logging.SeverityError, Category: logging.CategoryServer, Action: "test.error"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := opLog.Record(ctx, logging.OperationEvent{
		Severity: logging.SeverityInfo, Category: logging.CategoryServer, Action: "test.info"}); err != nil {
		t.Fatal(err)
	}

	env := callStatsHandler(t, serverAPI, serverAPI.handleStatsErrorsSummary, "")
	if env.Code != "OK" {
		t.Fatalf("code = %q message = %q, want OK", env.Code, env.Message)
	}
	var data struct {
		TodayErrorCount int `json:"today_error_count"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode errors summary: %v, data = %s", err, env.Data)
	}
	if data.TodayErrorCount != 2 {
		t.Fatalf("today_error_count = %d, want 2（仅 severity=error）", data.TodayErrorCount)
	}
}

func TestStatsErrorsSummaryWithoutOperationLog(t *testing.T) {
	_, serverAPI, _, _, _ := statsFixture(t)
	env := callStatsHandler(t, serverAPI, serverAPI.handleStatsErrorsSummary, "")
	if env.Code != "SERVICE_UNAVAILABLE" {
		t.Fatalf("code = %q, want SERVICE_UNAVAILABLE", env.Code)
	}
}
