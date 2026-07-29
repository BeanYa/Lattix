package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/shared"
)

func TestChainTrafficDailyReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	buckets, err := st.ChainTrafficDaily(ctx, 1, 0, "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(buckets)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("empty traffic history = %s, want []", encoded)
	}
}

func TestApplyTrafficSnapshotIsRecoverableAndMultiplied(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	entryID, _ := st.CreateServer(ctx, "entry", "entry.test", "entry-token", MachineTypeDirect, "", "", "US", "Entry")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.test", "exit-token", MachineTypeDirect, "", "", "US", "Exit")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "chain", exitID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "chain")
	if err != nil {
		t.Fatal(err)
	}
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, HopRoleEntry, 0, 10000, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, HopRoleExit, nodeID, 0, "")
	if _, err := st.db.Exec(`UPDATE chains SET service_node_id=?, traffic_multiplier_milli=1500 WHERE id=?`, nodeID, chainID); err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20000})
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1500,
		Hops: []ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: HopRoleEntry, Transport: "direct", ForwardPort: 10000},
			{HopID: exitHopID, ServerID: exitID, Role: HopRoleExit, ForwardPort: 20000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	first := []TrafficCounterSnapshot{
		{NodeID: nodeID, Up: 100, Down: 200},
		{HopID: entryHopID, Up: 110, Down: 210},
	}
	if err := st.ApplyTrafficSnapshot(ctx, exitID, "exit:1", first[:1], now); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, entryID, "entry:1", first[1:], now); err != nil {
		t.Fatal(err)
	}
	// Duplicate absolute snapshots must not add traffic.
	if err := st.ApplyTrafficSnapshot(ctx, exitID, "exit:1", first[:1], now); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, exitID, "exit:1", []TrafficCounterSnapshot{{NodeID: nodeID, Up: 120, Down: 240}}, now); err != nil {
		t.Fatal(err)
	}
	// The same stable service ID reported by a non-published server is ignored.
	futureExitID, _ := st.CreateServer(ctx, "future-exit", "future.test", "future-token", MachineTypeDirect, "", "", "US", "Future")
	if err := st.ApplyTrafficSnapshot(ctx, futureExitID, "future:1", []TrafficCounterSnapshot{{NodeID: nodeID, Up: 999, Down: 999}}, now); err != nil {
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
	if got := byHop[0]; got.RawUp != 120 || got.RawDown != 240 || got.EffectiveUp != 180 || got.EffectiveDown != 360 {
		t.Fatalf("chain total = %+v", got)
	}
	if got := byHop[exitHopID]; got.RawUp != 120 || got.EffectiveUp != 180 {
		t.Fatalf("exit total = %+v", got)
	}
	if got := byHop[entryHopID]; got.RawUp != 110 || got.EffectiveUp != 165 {
		t.Fatalf("entry total = %+v", got)
	}
	if err := st.ResetChainTraffic(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	totals, err = st.ChainTrafficTotals(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range totals {
		if total.RawUp != 0 || total.RawDown != 0 || total.EffectiveUp != 0 || total.EffectiveDown != 0 {
			t.Fatalf("total after reset = %+v", total)
		}
	}
}

func TestTrafficSnapshotNewInstanceCountsFromZero(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, _ := st.CreateServer(ctx, "server", "server.test", "token", MachineTypeDirect, "", "", "US", "Test")
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1", []TrafficCounterSnapshot{{User: "u", Up: 100}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-2", []TrafficCounterSnapshot{{User: "u", Up: 25}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	total, err := st.UserTraffic(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if total.Up != 125 {
		t.Fatalf("user up = %d, want 125", total.Up)
	}
}
