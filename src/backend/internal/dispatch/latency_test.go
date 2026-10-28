package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// latencyProbeTargetsOf 从 fakeRequester 捕获的信封里取某服务器的最新探测目标集。
func latencyProbeTargetsOf(t *testing.T, requester *fakeRequester, serverID int64) []shared.LatencyProbeTarget {
	t.Helper()
	var targets []shared.LatencyProbeTarget
	for _, env := range requester.sent {
		if env.Type != shared.TypeLatencyProbe {
			continue
		}
		var p shared.LatencyProbePayload
		if err := json.Unmarshal(env.Data, &p); err != nil {
			t.Fatalf("decode probe payload: %v", err)
		}
		targets = p.Targets
	}
	return targets
}

// TestHandleLatencyReportRoutesCatalogTargets 验证一轮回报按目标类型分流落库：
// Key 非空 → 目录测速表（§三网测速），Key 空 → 服务器对延迟表（§链路统计）。
func TestHandleLatencyReportRoutesCatalogTargets(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{7: true}}, Options{}, Events{})

	const roundID = "0123456789abcdef0123456789abcdef"
	d.latencyMu.Lock()
	d.latencyRounds[7] = latencyRound{roundID: roundID, sentAt: time.Now()}
	d.latencyMu.Unlock()

	latency := 18.0
	env := shared.Envelope{
		Kind: shared.KindEvent, Type: shared.TypeLatencyReport,
		Data: marshalMessageData(shared.LatencyProbeReportPayload{
			RoundID: roundID,
			Results: []shared.LatencyProbeResult{
				{ServerID: 9, Kind: shared.LatencyKindTCP, Port: 9101, LatencyMS: &latency},
				{Key: "catalog:广东:*:telecom", Kind: shared.LatencyKindTCP, LatencyMS: &latency},
				{Key: "catalog:广东:*:unicom", Kind: shared.LatencyKindTCP},
			},
		}),
	}
	d.handleLatencyReport(7, env)

	pairs, err := st.FreshServerLatencyProbes(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].FromServer != 7 || pairs[0].ToServer != 9 {
		t.Fatalf("pair rows = %+v, want single 7→9 row", pairs)
	}
	catalogs, err := st.FreshServerCatalogProbes(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs) != 2 {
		t.Fatalf("catalog rows = %d, want 2", len(catalogs))
	}
	byKey := map[string]store.CatalogProbeRow{}
	for _, row := range catalogs {
		byKey[row.TargetKey] = row
	}
	if row := byKey["catalog:广东:*:telecom"]; row.FromServer != 7 || row.LatencyMS == nil {
		t.Fatalf("catalog telecom row mismatch: %+v", row)
	}
	if row := byKey["catalog:广东:*:unicom"]; row.LatencyMS != nil {
		t.Fatalf("catalog unicom row should be failed sample: %+v", row)
	}

	// 回报已消费：再次回报同轮次应被丢弃（无在途轮次归属）。
	d.handleLatencyReport(7, env)
	catalogsAgain, err := st.FreshServerCatalogProbes(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogsAgain) != 2 {
		t.Fatalf("duplicate report accepted: rows = %d, want 2", len(catalogsAgain))
	}
}

// TestRunLatencyProbeRoundIncludesCatalogTargets 验证轮次目标合并：目录目标
// （ToServer=0 + Key）对所有在线服务器生效，与链路目标同轮下发。
func TestRunLatencyProbeRoundIncludesCatalogTargets(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingCDNNodeCatalog, `{"version":1,"provinces":[`+
		`{"name":"广东","carriers":{"telecom":{"ipv4":{"host":"gd-ct.ip.zstaticcdn.com","port":80}},`+
		`"unicom":{"ipv4":{"host":"gd-cu.ip.zstaticcdn.com","port":80}},`+
		`"mobile":{"ipv4":{"host":"gd-cm.ip.zstaticcdn.com","port":80}}}}]}`); err != nil {
		t.Fatal(err)
	}
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "edge-1", Address: "edge-1.test",
		BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &fakeRequester{online: map[int64]bool{serverID: true}}
	d := New(st, requester, Options{}, Events{})

	d.RunLatencyProbeRound(ctx)

	targets := latencyProbeTargetsOf(t, requester, serverID)
	if len(targets) == 0 {
		t.Fatalf("no probe enqueued for server %d", serverID)
	}
	catalogTargets := make([]shared.LatencyProbeTarget, 0, 3)
	for _, target := range targets {
		if target.Key == "" {
			continue
		}
		if target.ServerID != 0 || target.Kind != shared.LatencyKindTCP || target.Address == "" || target.Port != 80 {
			t.Fatalf("catalog target malformed: %+v", target)
		}
		catalogTargets = append(catalogTargets, target)
	}
	if len(catalogTargets) != 3 {
		t.Fatalf("catalog targets = %d, want 3: %+v", len(catalogTargets), targets)
	}
}
