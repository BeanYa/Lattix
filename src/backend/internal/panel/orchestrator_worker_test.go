package panel

import (
	"context"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func waitUntil(t *testing.T, timeout time.Duration, check func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestChainWorkerOnlyForGraphChains(t *testing.T) {
	srv, st, _, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := srv.Orchestrator()
	o.workerTick = 20 * time.Millisecond
	o.workers.Start(ctx)

	// setupAutoTestChain 自带编排图：先清空，验证无图链不建 worker。
	for seq := 0; seq < 4; seq++ {
		if err := st.DeleteChainHopGraph(context.Background(), chainID, seq); err != nil {
			t.Fatal(err)
		}
	}
	if o.workers.Ensure(chainID) {
		t.Fatal("graph-less chain must not get a worker")
	}
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, false, false)
	if !o.workers.Ensure(chainID) {
		t.Fatal("chain with graph should get a worker")
	}
	if !o.workers.Has(chainID) {
		t.Fatal("worker missing")
	}
	if _, err := st.ReplaceChainHopGraph(context.Background(), chainID, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	if o.workers.Ensure(chainID) {
		t.Fatal("deleting the last graph should stop the worker")
	}
	if o.workers.Has(chainID) {
		t.Fatal("worker not reclaimed")
	}
}

func TestChainWorkerFailsAndPreservesScene(t *testing.T) {
	srv, st, req, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := srv.Orchestrator()
	o.workerTick = 20 * time.Millisecond
	o.workers.Start(ctx)
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, false, false)
	req.online[a2] = false
	if err := st.UpsertServerBanWatch(context.Background(), store.ServerBanWatch{
		ServerID: a1, LossStreak: 6, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if !o.workers.Ensure(chainID) {
		t.Fatal("worker not created")
	}
	o.workers.Wake(chainID)
	if !waitUntil(t, 2*time.Second, func() bool { return o.workers.State(chainID) == workerFailed }) {
		t.Fatalf("worker should enter failed, state=%s", o.workers.State(chainID))
	}
	message, err := st.ChainOrchestrationError(context.Background(), chainID)
	if err != nil || message == "" {
		t.Fatalf("orchestration error not persisted: %q %v", message, err)
	}
	hops, _ := st.ChainHops(context.Background(), chainID)
	if len(hops) == 0 || hops[0].ServerID != a1 {
		t.Fatalf("failed orchestration must preserve the scene, got %+v", hops)
	}

	// 修改并保存编排（这里用 Resume 模拟）后恢复。
	req.online[a2] = true
	o.ResumeChain(chainID)
	if message, _ := st.ChainOrchestrationError(context.Background(), chainID); message != "" {
		t.Fatalf("resume should clear error, got %q", message)
	}
	if !waitUntil(t, 2*time.Second, func() bool { return o.workers.State(chainID) != workerFailed }) {
		t.Fatal("worker should leave failed after resume")
	}
}

func TestChainWorkerPausedDoesNotSwitch(t *testing.T) {
	srv, st, _, chainID, a1, a2, _, b2 := setupAutoTestChain(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := srv.Orchestrator()
	o.workerTick = 20 * time.Millisecond
	o.workers.Start(ctx)
	replaceEntryGraph(t, st, chainID, a1, a2, b2, false, false, false)
	if err := st.UpsertServerBanWatch(context.Background(), store.ServerBanWatch{
		ServerID: a1, LossStreak: 6, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if !o.workers.Ensure(chainID) {
		t.Fatal("worker not created")
	}
	o.workers.Pause(chainID)
	o.workers.Wake(chainID)
	time.Sleep(150 * time.Millisecond)
	hops, _ := st.ChainHops(context.Background(), chainID)
	if len(hops) == 0 || hops[0].ServerID != a1 {
		t.Fatalf("paused worker must not switch, got %+v", hops)
	}
}
