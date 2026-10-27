package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"lattix/backend/internal/store"
)

// chainStatsFixture 建一台直连两跳链（entry→exit，realized 端口 30000）并发布，
// 返回独立 sqlite 连接供测试落流量日桶（面板侧无 dispatcher 依赖；
// handleGetChainStats 只读 store）。
func chainStatsFixture(t *testing.T) (*store.Store, *sql.DB, *Server) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel-stats.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	raw, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: "token-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, cID := server("entry"), server("exit")
	chainID, err := st.InsertChain(ctx, "链A")
	if err != nil {
		t.Fatal(err)
	}
	entryHop, err := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	exitHop, err := st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "链A", ServiceServerID: cID,
		ServiceConfig: []byte(`{"protocol":"vless"}`), ServiceRealized: []byte(`{"port":30000}`),
		Hops: []store.ChainRevisionHop{
			{HopID: entryHop, ServerID: aID, Transport: "direct", ForwardPort: 10000},
			{HopID: exitHop, ServerID: cID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false, store.ChainStatusPending); err != nil {
		t.Fatal(err)
	}
	return st, raw, &Server{st: st}
}

// TestGetChainStats 验证 get-stats 的服务器对测速聚合（§链路统计）：
// tcp 首选 / icmp 回退、新鲜度过期、失败样本与今日负载口径。
func TestGetChainStats(t *testing.T) {
	ctx := context.Background()
	st, raw, serverAPI := chainStatsFixture(t)
	chains, err := st.ListChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(chains))
	}
	chain := chains[0]
	edges, err := st.PublishedLatencyEdges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 { // tcp（复用出口 realized 端口）+ icmp
		t.Fatalf("edges = %+v, want tcp+icmp", edges)
	}
	fromServer, toServer := edges[0].FromServer, edges[0].ToServer

	// tcp（复用出口 realized 端口 30000）与 icmp 同段各有新鲜样本：首选 tcp。
	latency := 12.25
	now := time.Now().UTC()
	if err := st.SaveServerLatencyProbes(ctx, fromServer, []store.ServerLatencySample{
		{ToServer: toServer, Kind: "tcp", Port: 30000, LatencyMS: &latency},
		{ToServer: toServer, Kind: "icmp", Port: 0, LatencyMS: &latency},
	}, now); err != nil {
		t.Fatal(err)
	}
	// 今日负载：直接落链级日桶（折算口径 1.5 倍）。
	_, location := st.TrafficLocation(ctx)
	today := time.Now().In(location).Format("2006-01-02")
	if _, err := raw.Exec(`INSERT INTO chain_traffic_daily
		(chain_id, hop_id, revision_id, usage_date, timezone, raw_up, raw_down, effective_up, effective_down)
		VALUES (?, 0, 1, ?, 'Asia/Shanghai', 100, 200, 150, 300)`, chain.ID, today); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	serverAPI.handleGetChainStats(recorder, httptest.NewRequest("GET", "/api/chain/get-stats", nil))
	if recorder.Code != 200 {
		t.Fatalf("get-stats = %d: %s", recorder.Code, recorder.Body.String())
	}
	env := decodeRPC(t, recorder)
	var response chainStatsResponse
	if err := json.Unmarshal(env.Data, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Chains) != 1 {
		t.Fatalf("got %d chains: %+v", len(response.Chains), response.Chains)
	}
	dto := response.Chains[0]
	if dto.ChainID != chain.ID {
		t.Fatalf("chain_id = %d, want %d", dto.ChainID, chain.ID)
	}
	if dto.LatencyMS == nil || *dto.LatencyMS != 12.25 {
		t.Fatalf("chain latency = %+v, want 12.25", dto.LatencyMS)
	}
	if len(dto.Segments) != 2 {
		t.Fatalf("segments = %+v, want tcp+icmp", dto.Segments)
	}
	if dto.Today == nil || dto.Today.EffectiveUp != 150 || dto.Today.EffectiveDown != 300 {
		t.Fatalf("today mismatch: %+v", dto.Today)
	}
	if response.TodayUp != 150 || response.TodayDown != 300 {
		t.Fatalf("today totals = %d/%d, want 150/300", response.TodayUp, response.TodayDown)
	}
	var tcpSegment, icmpSegment *chainSegmentStatDTO
	for i := range dto.Segments {
		switch dto.Segments[i].Kind {
		case "tcp":
			tcpSegment = &dto.Segments[i]
		case "icmp":
			icmpSegment = &dto.Segments[i]
		}
	}
	if tcpSegment == nil || tcpSegment.TargetPort != 30000 || tcpSegment.LatencyMS == nil {
		t.Fatalf("tcp segment mismatch: %+v", tcpSegment)
	}
	if icmpSegment == nil || icmpSegment.LatencyMS == nil {
		t.Fatalf("icmp segment mismatch: %+v", icmpSegment)
	}

	// tcp 样本过期（写回过期时间戳）：段回退到 icmp，链路延迟取 icmp 值。
	if err := st.SaveServerLatencyProbes(ctx, fromServer, []store.ServerLatencySample{
		{ToServer: toServer, Kind: "tcp", Port: 30000, LatencyMS: &latency},
	}, now.Add(-16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	serverAPI.handleGetChainStats(recorder, httptest.NewRequest("GET", "/api/chain/get-stats", nil))
	env = decodeRPC(t, recorder)
	if err := json.Unmarshal(env.Data, &response); err != nil {
		t.Fatal(err)
	}
	dto = response.Chains[0]
	if len(dto.Segments) != 1 || dto.Segments[0].Kind != "icmp" {
		t.Fatalf("segments after tcp expiry = %+v, want icmp only", dto.Segments)
	}
	if dto.LatencyMS == nil || *dto.LatencyMS != 12.25 {
		t.Fatalf("chain latency after fallback = %+v", dto.LatencyMS)
	}

	// 失败样本（tcp 在、latency 缺省）：段保留为连通性参考，链路延迟转为 null。
	if err := st.SaveServerLatencyProbes(ctx, fromServer, []store.ServerLatencySample{
		{ToServer: toServer, Kind: "tcp", Port: 30000},
		{ToServer: toServer, Kind: "icmp", Port: 0, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	serverAPI.handleGetChainStats(recorder, httptest.NewRequest("GET", "/api/chain/get-stats", nil))
	env = decodeRPC(t, recorder)
	if err := json.Unmarshal(env.Data, &response); err != nil {
		t.Fatal(err)
	}
	dto = response.Chains[0]
	if dto.LatencyMS != nil {
		t.Fatalf("failed tcp sample must null chain latency: %+v", dto)
	}
	if len(dto.Segments) != 2 {
		t.Fatalf("segments after failure = %+v, want tcp+icmp", dto.Segments)
	}
}
