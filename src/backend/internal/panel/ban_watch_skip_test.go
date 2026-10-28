package panel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// banWatchRequester 是 Ban 巡检测试的最小 AgentRequester 桩（仅在线表）。
type banWatchRequester struct{ online map[int64]bool }

func (r *banWatchRequester) Send(context.Context, int64, shared.Envelope) error { return nil }
func (r *banWatchRequester) IsOnline(serverID int64) bool                       { return r.online[serverID] }

// TestRunBanWatchSkipsNonDirect 校验非直连服务器（大陆不可直连）被巡检跳过：
// 不计数、既有观察状态被清除；直连服务器照常推进。
func TestRunBanWatchSkipsNonDirect(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	normalID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "hk-1", BootstrapToken: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	nonDirectID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "ir-1", BootstrapToken: "t2", NonDirect: true})
	if err != nil {
		t.Fatal(err)
	}

	// 目录 + 区域设置：广东省三网目标。
	doc := &cdncatalog.Document{Version: cdncatalog.SchemaVersion, Provinces: []cdncatalog.Province{{
		Name: "广东",
		Carriers: cdncatalog.CarrierEndpoints{
			Telecom: cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-ct.example.com", Port: 80}},
			Unicom:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-cu.example.com", Port: 80}},
			Mobile:  cdncatalog.ProtocolEndpoints{IPv4: cdncatalog.Endpoint{Host: "gd-cm.example.com", Port: 80}},
		},
	}}}
	catalogRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(catalogRaw)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingCatalogProbeRegion, `{"province":"广东"}`); err != nil {
		t.Fatal(err)
	}

	// 两台服务器本轮三网全失败样本。
	targets, region, err := st.CatalogProbeTargets(ctx)
	if err != nil || len(targets) != 3 {
		t.Fatalf("targets = %d err = %v, want 3", len(targets), err)
	}
	samples := make([]store.CatalogProbeSample, 0, len(targets))
	for _, target := range targets {
		samples = append(samples, store.CatalogProbeSample{Key: target.Key, Kind: "tcp"})
	}
	now := time.Now()
	for _, id := range []int64{normalID, nonDirectID} {
		if err := st.SaveServerCatalogProbes(ctx, id, samples, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	_ = region

	// 非直连服务器预置一个 banned 观察行：应被巡检清除。
	seeded := time.Now().UTC()
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{ServerID: nonDirectID, LossStreak: 12,
		State: store.BanStateBanned, BannedAt: &seeded, UpdatedAt: seeded}); err != nil {
		t.Fatal(err)
	}

	serverAPI := &Server{st: st, req: &banWatchRequester{online: map[int64]bool{normalID: true, nonDirectID: true}}}
	if err := serverAPI.runBanWatch(ctx); err != nil {
		t.Fatal(err)
	}

	watches, err := st.ServerBanWatchMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, watched := watches[nonDirectID]; watched {
		t.Fatalf("non-direct server watch row should be cleared, got %+v", watches[nonDirectID])
	}
	normal, watched := watches[normalID]
	if !watched {
		t.Fatal("direct server should enter watch after all-loss round")
	}
	if normal.LossStreak != 1 || banState(normal) != store.BanStateNormal {
		t.Fatalf("direct server watch = %+v, want streak 1 normal", normal)
	}

	// 标记直连服务器为非直连后，下一轮巡检同样清场。
	if err := st.UpdateServerNonDirect(ctx, normalID, true); err != nil {
		t.Fatal(err)
	}
	if err := serverAPI.runBanWatch(ctx); err != nil {
		t.Fatal(err)
	}
	watches, err = st.ServerBanWatchMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 0 {
		t.Fatalf("watches after non-direct flag = %d, want 0: %+v", len(watches), watches)
	}
}
