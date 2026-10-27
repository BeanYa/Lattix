package store

import (
	"context"
	"testing"
	"time"
)

func TestSaveChainHopProbesUpsertsLatestAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 12.5
	if err := st.SaveChainHopProbes(ctx, 7, []ChainProbeSample{
		{HopID: 3, Kind: "forward", TargetAddress: "203.0.113.2", TargetPort: 9101, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// 同段覆盖写（端口变更后新值取代旧值），历史保留每次样本；bridge 段失败样本为 0 值。
	failed := 0.0
	if err := st.SaveChainHopProbes(ctx, 7, []ChainProbeSample{
		{HopID: 3, Kind: "forward", TargetAddress: "203.0.113.9", TargetPort: 9109, LatencyMS: nil},
		{HopID: 3, Kind: "bridge", TargetAddress: "203.0.113.1", TargetPort: 9201, LatencyMS: &failed},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshChainHopProbes(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 2 {
		t.Fatalf("got %d fresh probes: %+v", len(fresh), fresh)
	}
	byKind := map[string]ChainHopProbe{}
	for _, probe := range fresh {
		byKind[probe.Kind] = probe
	}
	forward := byKind["forward"]
	if forward.TargetPort != 9109 || forward.LatencyMS != nil || forward.ServerID != 7 {
		t.Fatalf("forward latest mismatch: %+v", forward)
	}
	bridge := byKind["bridge"]
	if bridge.LatencyMS == nil || *bridge.LatencyMS != 0 {
		t.Fatalf("bridge latest mismatch: %+v", bridge)
	}
	var historyCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM chain_hop_probe_history WHERE hop_id=3`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 3 {
		t.Fatalf("history rows = %d, want 3", historyCount)
	}
}

func TestFreshChainHopProbesFiltersStaleSamples(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 9.0
	if err := st.SaveChainHopProbes(ctx, 1, []ChainProbeSample{
		{HopID: 5, Kind: "forward", TargetAddress: "10.0.0.2", TargetPort: 8000, LatencyMS: &latency},
	}, time.Now().UTC().Add(-15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshChainHopProbes(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 0 {
		t.Fatalf("stale sample leaked: %+v", fresh)
	}
}

func TestDeleteExpiredChainHopProbeHistoryKeepsFresh(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 4.0
	if err := st.SaveChainHopProbes(ctx, 1, []ChainProbeSample{
		{HopID: 2, Kind: "forward", TargetAddress: "10.0.0.2", TargetPort: 8000, LatencyMS: &latency},
	}, time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChainHopProbes(ctx, 1, []ChainProbeSample{
		{HopID: 2, Kind: "forward", TargetAddress: "10.0.0.2", TargetPort: 8000, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.DeleteExpiredChainHopProbeHistory(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1", deleted)
	}
	var kept int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM chain_hop_probe_history`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("kept %d rows, want 1", kept)
	}
}

func TestDeleteChainRemovesChainHopProbes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, err := st.InsertChain(ctx, "chain-a")
	if err != nil {
		t.Fatal(err)
	}
	chainB, err := st.InsertChain(ctx, "chain-b")
	if err != nil {
		t.Fatal(err)
	}
	hopA, err := st.InsertChainHop(ctx, chainA, 0, 1, HopRoleEntry, 0, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	hopB, err := st.InsertChainHop(ctx, chainB, 0, 2, HopRoleEntry, 0, 11000, "")
	if err != nil {
		t.Fatal(err)
	}
	latency := 1.0
	if err := st.SaveChainHopProbes(ctx, 1, []ChainProbeSample{
		{HopID: hopA, Kind: "forward", TargetAddress: "10.0.0.2", TargetPort: 9101, LatencyMS: &latency},
		{HopID: hopB, Kind: "forward", TargetAddress: "10.0.0.3", TargetPort: 9102, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteChain(ctx, chainA); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshChainHopProbes(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].HopID != hopB {
		t.Fatalf("expected only chain-b probe, got %+v", fresh)
	}
}

func TestChainDailyTrafficByDateGroupsByChainAndHop(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows := []struct{ chainID, hopID, up, down int64 }{
		{1, 0, 100, 200},
		{1, 3, 60, 80},
		{1, 3, 40, 20}, // 同日多 revision 行合并
		{2, 0, 5, 6},
	}
	for i, row := range rows {
		if _, err := st.db.Exec(`INSERT INTO chain_traffic_daily
			(chain_id, hop_id, revision_id, usage_date, timezone, raw_up, raw_down, effective_up, effective_down)
			VALUES (?, ?, ?, '2026-10-28', 'Asia/Shanghai', ?, ?, ?, ?)`,
			row.chainID, row.hopID, 9+i, row.up, row.down, row.up*2, row.down*2); err != nil {
			t.Fatal(err)
		}
	}
	buckets, err := st.ChainDailyTrafficByDate(ctx, "2026-10-28")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 3 {
		t.Fatalf("got %d buckets: %+v", len(buckets), buckets)
	}
	if buckets[0].ChainID != 1 || buckets[0].HopID != 0 || buckets[0].RawUp != 100 || buckets[0].EffectiveDown != 400 {
		t.Fatalf("chain total bucket mismatch: %+v", buckets[0])
	}
	if buckets[1].ChainID != 1 || buckets[1].HopID != 3 || buckets[1].RawUp != 100 || buckets[1].RawDown != 100 {
		t.Fatalf("hop bucket not merged: %+v", buckets[1])
	}
	if buckets[2].ChainID != 2 || buckets[2].RawUp != 5 {
		t.Fatalf("second chain bucket mismatch: %+v", buckets[2])
	}
	if buckets, err = st.ChainDailyTrafficByDate(ctx, "2026-10-27"); err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 0 {
		t.Fatalf("other date leaked: %+v", buckets)
	}
}
