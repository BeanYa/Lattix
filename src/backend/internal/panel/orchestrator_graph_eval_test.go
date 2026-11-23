package panel

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func replaceEntryGraph(t *testing.T, st *store.Store, chainID, a1, a2, b2 int64, withBanned, withRecovery, withNext bool) {
	t.Helper()
	nodes := []store.ChainHopGraphNode{
		{Kind: store.HopGraphKindServer, ServerID: a1, IsRoot: true},
		{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectSelf, Action: store.TriggerActionBanSuspected, Priority: 0},
		{Kind: store.HopGraphKindServer, ServerID: a2},
	}
	edges := []store.ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}
	if withBanned {
		nodes = append(nodes, store.ChainHopGraphNode{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectSelf, Action: store.TriggerActionBanBanned, Priority: 0})
		nodes = append(nodes, store.ChainHopGraphNode{Kind: store.HopGraphKindServer, ServerID: b2})
		edges = append(edges, store.ChainHopGraphEdge{FromNode: 0, ToNode: 3}, store.ChainHopGraphEdge{FromNode: 3, ToNode: 4})
	}
	if withNext {
		nodes = append(nodes, store.ChainHopGraphNode{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectNext, Action: store.TriggerActionSegmentTimeout, Priority: 0})
		edges = append(edges, store.ChainHopGraphEdge{FromNode: 0, ToNode: int64(len(nodes) - 1)},
			store.ChainHopGraphEdge{FromNode: int64(len(nodes) - 1), ToNode: 2})
	}
	if withRecovery {
		nodes = append(nodes, store.ChainHopGraphNode{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectRoot, Action: store.TriggerActionRecovered, Priority: 1})
		edges = append(edges, store.ChainHopGraphEdge{FromNode: int64(len(nodes) - 1), ToNode: 0})
	}
	if _, err := st.ReplaceChainHopGraph(context.Background(), chainID, 0, nodes, edges); err != nil {
		t.Fatal(err)
	}
}

func publishDesired(t *testing.T, st *store.Store, chainID int64) {
	t.Helper()
	ctx := context.Background()
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	hops, err := st.ChainHops(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	for _, hop := range hops {
		_ = st.SetChainHopStatus(ctx, hop.ID, store.HopStatusActive, "")
	}
	if err := st.PublishChainRevision(ctx, chain.DesiredRevisionID, false); err != nil {
		t.Fatal(err)
	}
}

func TestGraphOrchestratorSuspectedPrecedesBanned(t *testing.T) {
	srv, st, req, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	_ = req
	replaceEntryGraph(t, st, chainID, a1, a2, b2, true, false, false)
	if err := st.UpsertServerBanWatch(context.Background(), store.ServerBanWatch{
		ServerID: a1, LossStreak: 10, State: store.BanStateBanned, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(context.Background(), chainID); err != nil {
		t.Fatal(err)
	}
	hops, _ := st.ChainHops(context.Background(), chainID)
	if len(hops) == 0 || hops[0].ServerID != a2 {
		t.Fatalf("expected suspected trigger to win (a2), got %+v", hops)
	}
}

func TestGraphOrchestratorRecoveryFailback(t *testing.T) {
	srv, st, _, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx := context.Background()
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, true, false)
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID: a1, LossStreak: 6, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) == 0 || hops[0].ServerID != a2 {
		t.Fatalf("expected failover to a2, got %+v", hops)
	}
	publishDesired(t, st, chainID)
	if err := st.ResetChainHopGraphLastSwitch(ctx, chainID, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID: a1, LossStreak: 0, State: store.BanStateNormal, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	hops, _ = st.ChainHops(ctx, chainID)
	if len(hops) == 0 || hops[0].ServerID != a1 {
		t.Fatalf("expected recovery failback to a1, got %+v", hops)
	}
	events, _ := st.ChainFailoverEvents(ctx, chainID, 10)
	found := false
	for _, ev := range events {
		if ev.ReasonCode == store.FailoverReasonFailback {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected failback audit event, got %+v", events)
	}
}

func TestGraphOrchestratorNoViableTarget(t *testing.T) {
	srv, st, req, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx := context.Background()
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, false, false)
	req.online[a2] = false
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID: a1, LossStreak: 6, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err == nil {
		t.Fatal("expected exhausted error")
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) == 0 || hops[0].ServerID != a1 {
		t.Fatalf("hop should stay on a1, got %+v", hops)
	}
}

func TestGraphOrchestratorCooldownSkips(t *testing.T) {
	srv, st, _, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx := context.Background()
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, false, false)
	if err := st.TouchChainHopGraphSwitch(ctx, chainID, 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID: a1, LossStreak: 6, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) == 0 || hops[0].ServerID != a1 {
		t.Fatalf("cooldown should skip, got %+v", hops)
	}
}

func TestGraphOrchestratorBannedSwitchDTO(t *testing.T) {
	srv, st, _, chainID, a1, a2, _, _ := setupAutoTestChain(t)
	ctx := context.Background()
	_, err := st.ReplaceChainHopGraph(ctx, chainID, 0, []store.ChainHopGraphNode{
		{Kind: store.HopGraphKindServer, ServerID: a1, IsRoot: true, ListenPort: 10443},
		{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectSelf, Action: store.TriggerActionBanBanned},
		{Kind: store.HopGraphKindServer, ServerID: a2, ListenPort: 443},
		{Kind: store.HopGraphKindTrigger, Subject: store.TriggerSubjectRoot, Action: store.TriggerActionRecovered, Priority: 1},
	}, []store.ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}, {FromNode: 3, ToNode: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{ServerID: a1, LossStreak: 12, State: store.BanStateBanned, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orchestrator().EvaluateChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	check := func() {
		chain, err := st.ChainByID(ctx, chainID)
		if err != nil {
			t.Fatal(err)
		}
		dto, err := srv.toChainDTO(httptest.NewRequest("GET", "/api/chain/list", nil), *chain)
		if err != nil {
			t.Fatal(err)
		}
		orch := dto.Hops[0].Orchestration
		if dto.Hops[0].ServerID != a2 || orch.ActiveServerID != a2 || !orch.Triggered {
			t.Fatalf("hop=%+v orchestration=%+v", dto.Hops[0], orch)
		}
		for _, n := range orch.Nodes {
			if n.Kind == store.HopGraphKindServer && n.Active != (n.ServerID == a2) {
				t.Fatalf("wrong active node: %+v", n)
			}
		}
		if len(dto.FailoverEvents) != 1 || dto.FailoverEvents[0].ToServerID != a2 {
			t.Fatalf("events: %+v", dto.FailoverEvents)
		}
	}
	check()
	publishDesired(t, st, chainID)
	check()
}
