package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSaveServerLatencyProbesUpsertsLatestAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 12.5
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 9101, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// 端口入键（spec：TCP 需包含端口）：复用端口变更 = 新 (对,kind,port) 行，
	// 旧行待新鲜度窗口过期；历史保留每次样本。
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 9109},
		{ToServer: 2, Kind: "icmp", Port: 0, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshServerLatencyProbes(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 3 {
		t.Fatalf("got %d fresh rows: %+v", len(fresh), fresh)
	}
	byKindPort := map[string]ServerLatencyRow{}
	for _, row := range fresh {
		byKindPort[row.Kind+"/"+fmt.Sprintf("%d", row.Port)] = row
	}
	tcpNew := byKindPort["tcp/9109"]
	if tcpNew.ToServer != 2 || tcpNew.LatencyMS != nil || tcpNew.FromServer != 1 {
		t.Fatalf("tcp new-port row mismatch: %+v", tcpNew)
	}
	icmp := byKindPort["icmp/0"]
	if icmp.LatencyMS == nil || *icmp.LatencyMS != 12.5 {
		t.Fatalf("icmp latest mismatch: %+v", icmp)
	}
	var historyCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM server_latency_history`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 3 {
		t.Fatalf("history rows = %d, want 3", historyCount)
	}
}

func TestFreshServerLatencyProbesFiltersStaleSamples(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 9.0
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 8000, LatencyMS: &latency},
	}, time.Now().UTC().Add(-16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshServerLatencyProbes(ctx, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 0 {
		t.Fatalf("stale sample leaked: %+v", fresh)
	}
}

func TestDeleteExpiredServerLatencyHistoryKeepsFresh(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	latency := 4.0
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 8000, LatencyMS: &latency},
	}, time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 8000, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	deleted, err := st.DeleteExpiredServerLatencyHistory(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1", deleted)
	}
	var kept int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM server_latency_history`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("kept %d rows, want 1", kept)
	}
}

func TestDeleteServerCascadeRemovesLatencyRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, alias := range []string{"a", "b", "c"} {
		if _, err := st.CreateServer(ctx, ServerDraft{Alias: alias, Address: alias + ".test",
			BootstrapToken: alias, MachineType: MachineTypeDirect, CountryCode: "US"}); err != nil {
			t.Fatal(err)
		}
	}
	latency := 1.0
	if err := st.SaveServerLatencyProbes(ctx, 1, []ServerLatencySample{
		{ToServer: 2, Kind: "tcp", Port: 9000, LatencyMS: &latency},
		{ToServer: 3, Kind: "icmp", LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// 服务器 2 测 1 的反向样本也随其删除。
	if err := st.SaveServerLatencyProbes(ctx, 2, []ServerLatencySample{
		{ToServer: 1, Kind: "tcp", Port: 8000, LatencyMS: &latency},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteServerCascade(ctx, 2); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.FreshServerLatencyProbes(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].FromServer != 1 || fresh[0].ToServer != 3 {
		t.Fatalf("expected only 1->3 to survive, got %+v", fresh)
	}
}

// latencyTargetsFixture 建两跳链 A->B（含 PortalPort 的反向段复用）并发布。
func latencyTargetsFixture(t *testing.T, st *Store, reverse bool) (fromServer, midServer, toServer int64) {
	t.Helper()
	ctx := context.Background()
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, ServerDraft{Alias: alias, Address: alias + ".test",
			BootstrapToken: "token-" + alias, MachineType: MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c := server("a"), server("b"), server("c")
	config := marshalLatencyTestConfig()
	nodeID, err := st.InsertNode(ctx, "chain-exit", c, "vless", nil, config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "chain")
	if err != nil {
		t.Fatal(err)
	}
	entryHop, err := st.InsertChainHop(ctx, chainID, 0, a, HopRoleEntry, 0, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	exitHop, err := st.InsertChainHop(ctx, chainID, 1, c, HopRoleExit, nodeID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	hops := []ChainRevisionHop{
		{HopID: entryHop, ServerID: a, Transport: "direct", ForwardPort: 10000},
		{HopID: exitHop, ServerID: c, Role: HopRoleExit},
	}
	if reverse {
		// entry->exit 反向段：exit（下游）持有 bridge，拨 entry 机的 portal。
		hops[0].Transport = "reverse"
		hops[0].PortalPort = 20000
	}
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: c,
		ServiceConfig: config, ServiceRealized: []byte(`{"port":30000}`), Hops: hops,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false, ChainStatusPending); err != nil {
		t.Fatal(err)
	}
	return a, b, c
}

func marshalLatencyTestConfig() []byte {
	return []byte(`{"protocol":"vless"}`)
}

func TestLatencyProbeTargetsDirectChain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _, c := latencyTargetsFixture(t, st, false)
	targets, err := st.LatencyProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected targets only for entry server, got %d servers", len(targets))
	}
	list := targets[a]
	if len(list) != 2 { // tcp(直连段) + icmp
		t.Fatalf("entry targets = %+v", list)
	}
	byKind := map[string]ServerLatencyTarget{}
	for _, target := range list {
		byKind[target.Kind] = target
	}
	// tcp 复用出口业务 realized 端口（30000），icmp 同向。
	if len(list) != 2 {
		t.Fatalf("entry targets = %+v, want tcp+icmp", list)
	}
	tcp := byKind["tcp"]
	if tcp.ToServer != c || tcp.Port != 30000 || tcp.Address != "c.test" {
		t.Fatalf("tcp target mismatch: %+v", tcp)
	}
	icmp := byKind["icmp"]
	if icmp.ToServer != c || icmp.Port != 0 || icmp.Address != "c.test" {
		t.Fatalf("icmp target mismatch: %+v", icmp)
	}
}

func TestLatencyProbeTargetsReverseChainReversesDirection(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _, c := latencyTargetsFixture(t, st, true)
	targets, err := st.LatencyProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 反向段：出口 c（bridge 侧）测入口 a 的 portal 端口。
	list := targets[c]
	if len(list) == 0 {
		t.Fatalf("exit server has no targets: %+v", targets)
	}
	foundTCP := false
	for _, target := range list {
		if target.Kind == "tcp" {
			foundTCP = true
			if target.ToServer != a || target.Port != 20000 || target.Address != "a.test" {
				t.Fatalf("reverse tcp target mismatch: %+v", target)
			}
		}
	}
	if !foundTCP {
		t.Fatalf("missing reverse tcp target: %+v", list)
	}
	if _, ok := targets[a]; ok {
		t.Fatalf("entry server must not probe reverse segment: %+v", targets[a])
	}
}

func TestLatencyProbeTargetsDeduplicate(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, ServerDraft{Alias: alias, Address: alias + ".test",
			BootstrapToken: alias, MachineType: MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := server("a"), server("b")
	config := marshalLatencyTestConfig()
	// 两条链都是 A->B，同一段（同目标同端口）应去重。
	for _, name := range []string{"chain-1", "chain-2"} {
		chainID, err := st.InsertChain(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		entryHop, err := st.InsertChainHop(ctx, chainID, 0, a, HopRoleEntry, 0, 10000, "")
		if err != nil {
			t.Fatal(err)
		}
		exitHop, err := st.InsertChainHop(ctx, chainID, 1, b, HopRoleExit, 0, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
			Name: name, ServiceServerID: b, ServiceConfig: config,
			ServiceRealized: []byte(`{"port":30000}`), Hops: []ChainRevisionHop{
				{HopID: entryHop, ServerID: a, Transport: "direct", ForwardPort: 10000},
				{HopID: exitHop, ServerID: b, Role: HopRoleExit},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, revision.ID, false, ChainStatusPending); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := st.LatencyProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	list := targets[a]
	if len(list) != 2 { // tcp + icmp 各一条
		t.Fatalf("targets not deduplicated: %+v", list)
	}
	var tcpTargets int
	for _, target := range list {
		if target.Kind == "tcp" {
			tcpTargets++
			if target.Port != 30000 || target.ToServer != b || target.Address != "b.test" {
				t.Fatalf("tcp target mismatch: %+v", target)
			}
		}
	}
	if tcpTargets != 1 {
		t.Fatalf("tcp targets = %d, want 1", tcpTargets)
	}
}
