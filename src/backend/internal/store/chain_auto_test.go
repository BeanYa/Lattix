package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// 保留段间连通性观察与切换审计的存储契约（备选池/策略表已在 v34 移除）。
func TestServerPairWatchAndFailoverEvents(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)
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
