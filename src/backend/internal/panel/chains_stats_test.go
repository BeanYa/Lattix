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

// chainStatsFixture 在临时文件库上直建两跳链 A/B 与服务器，返回独立 sqlite 连接
// 供测试落流量日桶（面板侧无 dispatcher 依赖；handleGetChainStats 只读 store）。
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
	for _, name := range []string{"链A", "链B"} {
		chainID, err := st.InsertChain(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 10000, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, 0, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	return st, raw, &Server{st: st}
}

// TestGetChainStats 建两跳链 A/B，验证 get-stats 的段延迟聚合、失败样本
// 与今日负载口径（§链路统计）。
func TestGetChainStats(t *testing.T) {
	ctx := context.Background()
	st, raw, serverAPI := chainStatsFixture(t)
	chains, err := st.ListChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(chains))
	}
	// 链A 的入口跳上报一段有效 RTT；链B 不上报（段不完整 → 链路延迟 null）。
	latency := 12.25
	for _, chain := range chains {
		hops, err := st.ChainHops(ctx, chain.ID)
		if err != nil {
			t.Fatal(err)
		}
		if chain.Name != "链A" {
			continue
		}
		if err := st.SaveChainHopProbes(ctx, hops[0].ServerID, []store.ChainProbeSample{
			{HopID: hops[0].ID, Kind: "forward", TargetAddress: "203.0.113.9", TargetPort: 9109,
				LatencyMS: &latency},
		}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	// 今日负载：直接落链级日桶（折算口径 1.5 倍）。
	_, location := st.TrafficLocation(ctx)
	today := time.Now().In(location).Format("2006-01-02")
	for _, chain := range chains {
		if _, err := raw.Exec(`INSERT INTO chain_traffic_daily
			(chain_id, hop_id, revision_id, usage_date, timezone, raw_up, raw_down, effective_up, effective_down)
			VALUES (?, 0, 1, ?, 'Asia/Shanghai', 100, 200, 150, 300)`, chain.ID, today); err != nil {
			t.Fatal(err)
		}
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
	if len(response.Chains) != 2 {
		t.Fatalf("got %d chains: %+v", len(response.Chains), response.Chains)
	}
	measured, full := 0, 0
	for _, chain := range response.Chains {
		if chain.ChainID == 0 {
			t.Fatalf("chain_id missing in stats: %+v", chain)
		}
		if chain.Today == nil || chain.Today.EffectiveUp != 150 || chain.Today.EffectiveDown != 300 {
			t.Fatalf("today traffic mismatch: %+v", chain)
		}
		if chain.LatencyMS != nil {
			full++
			if *chain.LatencyMS != 12.25 {
				t.Fatalf("chain latency = %v, want 12.25", *chain.LatencyMS)
			}
			if len(chain.Segments) != 1 || chain.Segments[0].Kind != "forward" ||
				chain.Segments[0].TargetPort != 9109 || chain.Segments[0].LatencyMS == nil {
				t.Fatalf("segments mismatch: %+v", chain.Segments)
			}
			measured++
		} else if len(chain.Segments) != 0 {
			t.Fatalf("unmeasured chain must have no fresh segments: %+v", chain)
		}
	}
	if measured != 1 || full != 1 {
		t.Fatalf("expected exactly one fully measured chain, got measured=%d full=%d", measured, full)
	}
	if response.TodayUp != 300 || response.TodayDown != 600 {
		t.Fatalf("today totals = %d/%d, want 300/600", response.TodayUp, response.TodayDown)
	}

	// 失败样本（latency 缺省）：段保留在段列表中作连通性参考，链路延迟转为 null。
	for _, chain := range chains {
		if chain.Name != "链A" {
			continue
		}
		hops, err := st.ChainHops(ctx, chain.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveChainHopProbes(ctx, hops[0].ServerID, []store.ChainProbeSample{
			{HopID: hops[0].ID, Kind: "forward", TargetAddress: "203.0.113.9", TargetPort: 9109},
		}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	recorder = httptest.NewRecorder()
	serverAPI.handleGetChainStats(recorder, httptest.NewRequest("GET", "/api/chain/get-stats", nil))
	env = decodeRPC(t, recorder)
	if err := json.Unmarshal(env.Data, &response); err != nil {
		t.Fatal(err)
	}
	for _, chain := range response.Chains {
		if chain.LatencyMS != nil {
			t.Fatalf("failed probe must null chain latency: %+v", chain)
		}
	}
	for _, chain := range response.Chains {
		if len(chain.Segments) == 1 && chain.Segments[0].LatencyMS != nil {
			t.Fatalf("failed sample kept with latency: %+v", chain.Segments)
		}
	}
}
