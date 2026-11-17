package store

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"lattix/shared"
)

// newTestExternalEndpointChain 创建一条「入口 A 共享端点 → 外部直链出口」的已发布链路：
// 入口 A 承担用户接入（EndpointID），末位跳是未托管外部服务器（node_id=0），
// 修订快照携带 ServiceExternal。返回入口服务器、外部服务器、链路与外部出口跳。
func newTestExternalEndpointChain(t *testing.T, st *Store, name string) (entryID, externalID, chainID, exitHopID int64) {
	t.Helper()
	ctx := context.Background()
	entryID, err := st.CreateServer(ctx, ServerDraft{Alias: name + "-entry", Address: "entry.example.com",
		BootstrapToken: "tok-" + name + "-e", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exit := shared.ExternalExit{Protocol: shared.ProtocolSocks, Address: "203.0.113.10", Port: 1080,
		Username: "user", Password: "pass"}
	rawExit, err := json.Marshal(exit)
	if err != nil {
		t.Fatal(err)
	}
	externalID, err = st.CreateServer(ctx, ServerDraft{Alias: name + "-external", Kind: ServerKindExternal,
		ExternalConfig: string(rawExit)})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP,
		Template: json.RawMessage(`{}`)})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 30000, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, []byte(`{"port":20001}`)); err != nil {
		t.Fatal(err)
	}
	chainID, err = st.InsertChain(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	entryHopID, err := st.InsertChainHop(ctx, chainID, 0, entryID, HopRoleEntry, 0, 10001, "")
	if err != nil {
		t.Fatal(err)
	}
	exitHopID, err = st.InsertChainHop(ctx, chainID, 1, externalID, HopRoleExit, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: name, ServiceNodeID: 0, ServiceServerID: externalID,
		ServiceConfig: json.RawMessage(`{}`), TrafficMultiplierMilli: 1500,
		EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
		Hops: []ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: HopRoleEntry, Transport: "direct", ForwardPort: 10001},
			{HopID: exitHopID, ServerID: externalID, Role: HopRoleExit, ForwardPort: 0},
		},
		ServiceExternal: &exit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	return entryID, externalID, chainID, exitHopID
}

// TestApplyTrafficSnapshotAttributesExternalExitTraffic 验证外部直链出口的流量归属：
// 入口 A 的 access 计数（链路权威总量）除计入链路总量与该跳外，还按出口服务器
// 记录一份 traffic_stats（dim_type=external，dim_id=chain_id），并写入外部出口跳的
// 链路流量（倍率折算），而入口 A 自身的统计口径不变。
func TestApplyTrafficSnapshotAttributesExternalExitTraffic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, externalID, chainID, exitHopID := newTestExternalEndpointChain(t, st, "ext-traffic")
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000bb", "tok-ext", nil)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := st.SetUserChains(ctx, userID, []int64{chainID})
	if err != nil || len(added) != 1 {
		t.Fatalf("assign chain = %+v err %v", added, err)
	}
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, entryID, "entry:1",
		[]TrafficCounterSnapshot{{User: added[0].Identity(), Up: 100, Down: 200}}, now); err != nil {
		t.Fatal(err)
	}

	totals, err := st.ChainTrafficTotals(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	byHop := map[int64]ChainTraffic{}
	for _, total := range totals {
		byHop[total.HopID] = total
	}
	if got := byHop[0]; got.RawUp != 100 || got.RawDown != 200 || got.EffectiveUp != 150 || got.EffectiveDown != 300 {
		t.Fatalf("chain total = %+v", got)
	}
	if got := byHop[exitHopID]; got.RawUp != 100 || got.RawDown != 200 || got.EffectiveUp != 150 || got.EffectiveDown != 300 {
		t.Fatalf("external exit hop = %+v", got)
	}

	var dimType, dimID string
	var up, down int64
	if err := st.db.QueryRow(`SELECT dim_type, dim_id, up, down FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='external'`, externalID).Scan(&dimType, &dimID, &up, &down); err != nil {
		t.Fatal(err)
	}
	if dimType != "external" || dimID != strconv.FormatInt(chainID, 10) || up != 100 || down != 200 {
		t.Fatalf("external traffic row = %s/%s %d/%d", dimType, dimID, up, down)
	}
	var entryExternal int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_stats_daily
		WHERE server_id=? AND dim_type='external'`, entryID).Scan(&entryExternal); err != nil {
		t.Fatal(err)
	}
	if entryExternal != 0 {
		t.Fatalf("entry server external rows = %d, want 0", entryExternal)
	}

	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	points, err := st.TrafficStatSeries(ctx, "server", externalID, "", "day", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Up != 100 || points[0].Down != 200 {
		t.Fatalf("external server series = %+v", points)
	}
	rows, err := st.TrafficStatBreakdown(ctx, externalID, "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].DimType != "external" || rows[0].DimID != strconv.FormatInt(chainID, 10) ||
		rows[0].Label != "ext-traffic" || rows[0].Up != 100 || rows[0].Down != 200 {
		t.Fatalf("external breakdown = %+v", rows)
	}
}

// TestApplyTrafficSnapshotManagedExitHasNoExternalAttribution 回归：托管出口的链路
// 只按既有口径入账（链路总量 + 各跳），不得产生 external 维度或外部出口跳行。
func TestApplyTrafficSnapshotManagedExitHasNoExternalAttribution(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, endpointID := newTestEndpointChain(t, st, "managed-traffic")
	var serverID int64
	if err := st.db.QueryRow(`SELECT server_id FROM shared_endpoints WHERE id=?`, endpointID).Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000cc", "tok-managed", nil)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := st.SetUserChains(ctx, userID, []int64{chainID})
	if err != nil || len(added) != 1 {
		t.Fatalf("assign chain = %+v err %v", added, err)
	}
	now := time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "entry:1",
		[]TrafficCounterSnapshot{{User: added[0].Identity(), Up: 500, Down: 700}}, now); err != nil {
		t.Fatal(err)
	}
	totals, err := st.ChainTrafficTotals(ctx, chainID)
	if err != nil || len(totals) != 1 || totals[0].HopID != 0 || totals[0].RawUp != 500 {
		t.Fatalf("managed chain traffic = %+v err %v", totals, err)
	}
	var externalRows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_stats_daily WHERE dim_type='external'`).Scan(&externalRows); err != nil {
		t.Fatal(err)
	}
	if externalRows != 0 {
		t.Fatalf("managed exit external rows = %d, want 0", externalRows)
	}
}
