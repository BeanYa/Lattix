package panel

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

type fakeAutoRequester struct {
	online map[int64]bool
	sent   []shared.Envelope
}

func (f *fakeAutoRequester) Send(_ context.Context, _ int64, env shared.Envelope) error {
	f.sent = append(f.sent, env)
	return nil
}

func (f *fakeAutoRequester) IsOnline(serverID int64) bool {
	return f.online[serverID]
}

// setupAutoTestChain 创建一条 2 跳已发布 active 链路（Entry: A1 [备选 A2], Exit: B1 [备选 B2]）。
func setupAutoTestChain(t *testing.T) (*Server, *store.Store, *fakeAutoRequester, int64, int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel-auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	req := &fakeAutoRequester{online: make(map[int64]bool)}
	srv, err := New(st, req, Config{AdminUser: "admin", AdminPass: "test-password-123"})
	if err != nil {
		t.Fatal(err)
	}

	createSrv := func(alias, addr string) int64 {
		id, cerr := st.CreateServer(ctx, store.ServerDraft{
			Alias:          alias,
			Address:        addr,
			BootstrapToken: "tok-" + alias,
			MachineType:    store.MachineTypeDirect,
			CountryCode:    "CN",
			Location:       alias,
		})
		if cerr != nil {
			t.Fatal(cerr)
		}
		req.online[id] = true
		return id
	}

	a1 := createSrv("Entry-A1", "203.0.113.1")
	a2 := createSrv("Entry-A2", "203.0.113.2")
	b1 := createSrv("Exit-B1", "198.51.100.1")
	b2 := createSrv("Exit-B2", "198.51.100.2")

	vc := buildVirtualConfig(createNodeRequest{
		Name:     "AutoChain",
		ServerID: b1,
		Protocol: shared.ProtocolVLESS,
		Network:  "tcp",
		Security: shared.SecurityReality,
	})
	vcJSON, _ := json.Marshal(vc)
	ep, _, err := st.EnsureSharedEndpoint(ctx, a1, shared.ProtocolVLESS, 10443, "hash-a1", vcJSON)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetSharedEndpointStatus(ctx, ep.ID, store.EndpointStatusApplying, "", store.EndpointStatusPending)
	_ = st.SetSharedEndpointStatus(ctx, ep.ID, store.EndpointStatusActive, "", store.EndpointStatusApplying)

	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name:                   "AutoChain",
		ServiceServerID:        b1,
		ServiceProtocol:        shared.ProtocolVLESS,
		ServiceConfig:          vcJSON,
		EndpointID:             ep.ID,
		ServiceUUID:            "11111111-1111-1111-1111-111111111111",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: a1, Role: store.HopRoleEntry, Transport: "direct"},
			{ServerID: b1, Role: store.HopRoleExit, Transport: ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, dep.ChainID, store.ChainStatusApplying, "", store.ChainStatusPending); err != nil {
		t.Fatal(err)
	}
	for _, h := range dep.Hops {
		_ = st.SetChainHopStatus(ctx, h.HopID, store.HopStatusActive, "")
	}
	if err := st.PublishChainRevision(ctx, dep.RevisionID, false, store.ChainStatusApplying); err != nil {
		t.Fatal(err)
	}

	candidates := []store.ChainHopCandidate{
		{ChainID: dep.ChainID, Seq: 0, Priority: 0, ServerID: a1, ListenPort: 10443},
		{ChainID: dep.ChainID, Seq: 0, Priority: 1, ServerID: a2, ListenPort: 10444},
		{ChainID: dep.ChainID, Seq: 1, Priority: 0, ServerID: b1, ListenPort: 20443},
		{ChainID: dep.ChainID, Seq: 1, Priority: 1, ServerID: b2, ListenPort: 20444},
	}
	if err := st.ReplaceChainHopCandidates(ctx, dep.ChainID, candidates); err != nil {
		t.Fatal(err)
	}
	policy := store.DefaultChainAutoPolicy(dep.ChainID)
	policy.Enabled = true
	policy.CooldownSec = 0
	if err := st.UpsertChainAutoPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	return srv, st, req, dep.ChainID, a1, a2, b1, b2
}

func TestAutoOrchestratorFailoverOnEntryBannedAndPreemptiveFailback(t *testing.T) {
	ctx := context.Background()
	srv, st, _, chainID, a1, a2, b1, _ := setupAutoTestChain(t)

	// 模拟入口 A1 三网连续 10 轮超时被判定为 banned。
	now := time.Now().UTC()
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID:   a1,
		LossStreak: 10,
		State:      store.BanStateBanned,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatalf("EvaluateChain on ban: %v", err)
	}

	hops, err := st.ChainHops(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 || hops[0].ServerID != a2 || hops[1].ServerID != b1 {
		t.Fatalf("expected entry hop switched to A2 (%d), got hops=%+v", a2, hops)
	}
	events, err := st.ChainFailoverEvents(ctx, chainID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ReasonCode != store.FailoverReasonBanned ||
		events[0].FromServerID != a1 || events[0].ToServerID != a2 {
		t.Fatalf("unexpected failover event: %+v", events)
	}

	// 将新建的 desired revision 推向已发布 active，随后测试主节点 A1 恢复 normal 时的抢占式回切。
	chain, _ := st.ChainByID(ctx, chainID)
	for _, h := range hops {
		_ = st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, "")
	}
	if err := st.PublishChainRevision(ctx, chain.DesiredRevisionID, false); err != nil {
		t.Fatal(err)
	}

	policy, _ := st.ChainAutoPolicyByID(ctx, chainID)
	policy.FailbackMode = store.FailbackPreemptive
	policy.CooldownSec = 0
	if err := st.UpsertChainAutoPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID:   a1,
		LossStreak: 0,
		State:      store.BanStateNormal,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatalf("EvaluateChain on failback: %v", err)
	}
	hopsAfter, _ := st.ChainHops(ctx, chainID)
	if len(hopsAfter) != 2 || hopsAfter[0].ServerID != a1 {
		t.Fatalf("expected entry hop failed back to A1 (%d), got %+v", a1, hopsAfter)
	}
}

func TestAutoOrchestratorFailoverOnSegmentUnreachableAndQuotaExhausted(t *testing.T) {
	ctx := context.Background()
	srv, st, _, chainID, a1, a2, b1, b2 := setupAutoTestChain(t)

	// 1. 模拟 A1 -> B1 连续 3 轮探测超时（nil LatencyMS），触发下游 B1 替换为 B2。
	for round := 0; round < 3; round++ {
		srv.Orchestrator().OnLatencyReport(ctx, a1, []store.ServerLatencySample{
			{ToServer: b1, Kind: "tcp", Port: 20443, LatencyMS: nil},
		})
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatalf("EvaluateChain on segment unreachable: %v", err)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) != 2 || hops[1].ServerID != b2 {
		t.Fatalf("expected exit hop switched to B2 (%d), got %+v", b2, hops)
	}

	// 发布该 revision 后，模拟入口 A1 当期流量耗尽。
	chain, _ := st.ChainByID(ctx, chainID)
	for _, h := range hops {
		_ = st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, "")
	}
	if err := st.PublishChainRevision(ctx, chain.DesiredRevisionID, false); err != nil {
		t.Fatal(err)
	}

	quota := int64(1000)
	today := time.Now().Format("2006-01-02")
	if err := st.UpsertServerTrafficPlan(ctx, store.ServerTrafficPlan{
		ServerID:          a1,
		QuotaBytes:        &quota,
		AccountingMode:    "outbound",
		ResetAnchorOn:     today,
		ResetCount:        1,
		ResetUnit:         "month",
		TrackingStartedOn: today,
	}); err != nil {
		t.Fatal(err)
	}
	// 两次 SaveServerMetrics 写入累计差值 1500 字节（超过 1000 字节配额）。
	if err := st.SaveServerMetrics(ctx, a1, store.ServerMetrics{
		NetworkInterface: "eth0",
		NetworkTXBytes:   100,
		UptimeSeconds:    100,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveServerMetrics(ctx, a1, store.ServerMetrics{
		NetworkInterface: "eth0",
		NetworkTXBytes:   1600,
		UptimeSeconds:    160,
	}, true); err != nil {
		t.Fatal(err)
	}

	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatalf("EvaluateChain on quota exhausted: %v", err)
	}
	hopsAfterQuota, _ := st.ChainHops(ctx, chainID)
	if len(hopsAfterQuota) != 2 || hopsAfterQuota[0].ServerID != a2 {
		t.Fatalf("expected entry hop switched to A2 (%d) on quota exhaustion, got %+v", a2, hopsAfterQuota)
	}
}
