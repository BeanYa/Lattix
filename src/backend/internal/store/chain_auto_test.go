package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestChainHopCandidatesAndAutoPolicyCRUD(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s1, err := st.CreateServer(ctx, ServerDraft{Alias: "A1", Address: "203.0.113.1", BootstrapToken: "tok1", MachineType: MachineTypeDirect, CountryCode: "CN"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := st.CreateServer(ctx, ServerDraft{Alias: "A2", Address: "203.0.113.2", BootstrapToken: "tok2", MachineType: MachineTypeDirect, CountryCode: "CN"})
	if err != nil {
		t.Fatal(err)
	}
	s3, err := st.CreateServer(ctx, ServerDraft{Alias: "B1", Address: "198.51.100.1", BootstrapToken: "tok3", MachineType: MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "TestChain")
	if err != nil {
		t.Fatal(err)
	}

	candidates := []ChainHopCandidate{
		{ChainID: chainID, Seq: 0, Priority: 0, ServerID: s1, Address: "203.0.113.1", ListenPort: 10080},
		{ChainID: chainID, Seq: 0, Priority: 1, ServerID: s2, Address: "203.0.113.2", ListenPort: 10081},
		{ChainID: chainID, Seq: 1, Priority: 0, ServerID: s3, Address: "", ListenPort: 20080},
	}
	if err := st.ReplaceChainHopCandidates(ctx, chainID, candidates); err != nil {
		t.Fatalf("ReplaceChainHopCandidates: %v", err)
	}
	got, err := st.ChainHopCandidates(ctx, chainID)
	if err != nil {
		t.Fatalf("ChainHopCandidates: %v", err)
	}
	if len(got) != 3 || got[0].ServerID != s1 || got[1].ServerID != s2 || got[1].Priority != 1 || got[2].ServerID != s3 {
		t.Fatalf("unexpected candidates: %+v", got)
	}

	policy := ChainAutoPolicy{
		ChainID:           chainID,
		Enabled:           true,
		OnBan:             true,
		BanTriggerLevel:   BanTriggerSuspected,
		OnUnreachable:     true,
		UnreachableStreak: 2,
		OnQuotaExhausted:  true,
		OnAgentOffline:    true,
		OfflineTimeoutSec: 300,
		FailbackMode:      FailbackPreemptive,
		CooldownSec:       600,
	}
	if err := st.UpsertChainAutoPolicy(ctx, policy); err != nil {
		t.Fatalf("UpsertChainAutoPolicy: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.TouchChainFailoverAt(ctx, chainID, now); err != nil {
		t.Fatalf("TouchChainFailoverAt: %v", err)
	}
	gotPolicy, err := st.ChainAutoPolicyByID(ctx, chainID)
	if err != nil {
		t.Fatalf("ChainAutoPolicyByID: %v", err)
	}
	if !gotPolicy.Enabled || gotPolicy.BanTriggerLevel != BanTriggerSuspected ||
		gotPolicy.FailbackMode != FailbackPreemptive || gotPolicy.LastFailoverAt == nil {
		t.Fatalf("unexpected policy: %+v", gotPolicy)
	}

	if err := st.UpsertServerPairWatch(ctx, ServerPairWatch{
		FromServer: s1,
		ToServer:   s3,
		LossStreak: 3,
		State:      PairStateUnreachable,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("UpsertServerPairWatch: %v", err)
	}
	watches, err := st.ServerPairWatchMap(ctx)
	if err != nil {
		t.Fatalf("ServerPairWatchMap: %v", err)
	}
	if watches[[2]int64{s1, s3}].State != PairStateUnreachable || watches[[2]int64{s1, s3}].LossStreak != 3 {
		t.Fatalf("unexpected pair watch: %+v", watches[[2]int64{s1, s3}])
	}

	if _, err := st.RecordChainFailoverEvent(ctx, ChainFailoverEvent{
		ChainID:      chainID,
		Seq:          0,
		FromServerID: s1,
		ToServerID:   s2,
		ReasonCode:   FailoverReasonBanned,
		ReasonDetail: "三网连续全丢包",
		RevisionID:   2,
	}); err != nil {
		t.Fatalf("RecordChainFailoverEvent: %v", err)
	}
	events, err := st.ChainFailoverEvents(ctx, chainID, 10)
	if err != nil {
		t.Fatalf("ChainFailoverEvents: %v", err)
	}
	if len(events) != 1 || events[0].FromServerID != s1 || events[0].ToServerID != s2 {
		t.Fatalf("unexpected failover events: %+v", events)
	}
}
