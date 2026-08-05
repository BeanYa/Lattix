package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestCleanupResponseOnlyCompletesRevisionTask(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	serverID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "token", store.MachineTypeDirect, "", "", "US", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "service", serverID, shared.ProtocolVLESS, nil, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 443, PublicKey: "keep-me"})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	chainID, _ := st.InsertChain(ctx, "chain")
	hopID, _ := st.InsertChainHop(ctx, chainID, 0, serverID, store.HopRoleExit, nodeID, 443, "")
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: serverID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{{HopID: hopID, ServerID: serverID, Role: store.HopRoleExit, ForwardPort: 443}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskKey := "cleanup/service/" + jsonNumber(nodeID)
	if _, err := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID, TaskKey: taskKey,
		Phase: "cleanup", Action: "remove", Kind: RevisionPieceService, HopID: nodeID, ServerID: serverID}); err != nil {
		t.Fatal(err)
	}

	requester := &fakeRequester{online: map[int64]bool{serverID: true}}
	d := New(st, requester)
	commandID, err := d.enqueueRevisionTask(ctx, serverID, shared.TypeRemoveNode,
		shared.RemoveNodePayload{NodeID: nodeID}, revision.ID, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _ := st.CommandsByType(ctx, shared.TypeRemoveNode)
	if len(cmds) != 1 || cmds[0].ID != commandID || cmds[0].Status != store.CommandStatusSent {
		t.Fatalf("cleanup command = %+v", cmds)
	}
	result, _ := json.Marshal(shared.ApplyResultPayload{NodeID: nodeID})
	d.handleCommandResponse(serverID, shared.Envelope{Kind: shared.KindResponse, Type: shared.TypeRemoveNode,
		RequestID: cmds[0].RequestID, TraceID: cmds[0].TraceID, Code: shared.CodeOK, Data: result})

	task, err := st.RevisionTaskByCommandID(ctx, commandID)
	if err != nil || task.Status != store.RevisionTaskAcked {
		t.Fatalf("revision task = %+v err=%v", task, err)
	}
	node, err := st.NodeByID(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != store.NodeStatusActive || string(node.RealizedConfig) != string(realized) {
		t.Fatalf("cleanup response changed current node: status=%s realized=%s", node.Status, node.RealizedConfig)
	}
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestResumeChainsEnqueuesNextPersistedTask(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "entry-token", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "exit-token", store.MachineTypeDirect, "", "", "US", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "service", exitID, shared.ProtocolVLESS, nil, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 2443})
	_ = st.SetNodeActive(ctx, nodeID, realized)
	chainID, _ := st.InsertChain(ctx, "chain")
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 1443, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 2443, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	key := revisionPieceKey(RevisionPieceForward, entryHopID)
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: exitID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, ApplyKeys: []string{key}, Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1443},
			{HopID: exitHopID, ServerID: exitID, Role: store.HopRoleExit, ForwardPort: 2443},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID, TaskKey: key,
		Phase: "apply", Action: "apply", Kind: RevisionPieceForward, HopID: entryHopID, ServerID: entryID})
	_ = st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, "")

	requester := &fakeRequester{online: map[int64]bool{entryID: false, exitID: false}}
	d := New(st, requester)
	if err := d.ResumeChains(ctx); err != nil {
		t.Fatal(err)
	}
	commands, _ := st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if len(commands) != 1 || commands[0].Status != store.CommandStatusQueued {
		t.Fatalf("resumed commands = %+v", commands)
	}
	task, err := st.RevisionTaskByCommandID(ctx, commands[0].ID)
	if err != nil || task.Status != store.RevisionTaskQueued {
		t.Fatalf("resumed task = %+v err=%v", task, err)
	}
}

func TestVersionMismatchOnlyDeliversExactUpgrade(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "server", "server.example.com", "token", store.MachineTypeDirect, "", "", "US", "")
	_ = st.TouchServer(ctx, serverID, "", "v1.0.0", "server.example.com", "server.example.com", "")
	requester := &fakeRequester{online: map[int64]bool{serverID: true}}
	d := New(st, requester)
	d.PanelVersion = "v2.0.0"
	if _, err := d.Enqueue(ctx, serverID, shared.TypeApplyNode, shared.ApplyNodePayload{NodeID: 42}); err != nil {
		t.Fatal(err)
	}
	if len(requester.sent) != 0 {
		t.Fatalf("revision command crossed version gate: %+v", requester.sent)
	}
	if _, err := d.Enqueue(ctx, serverID, shared.TypeUpgradeAgent,
		shared.UpgradeAgentPayload{Version: "v2.0.0"}); err != nil {
		t.Fatal(err)
	}
	if len(requester.sent) != 1 || requester.sent[0].Type != shared.TypeUpgradeAgent {
		t.Fatalf("sent before sync = %+v", requester.sent)
	}
	_ = st.TouchServer(ctx, serverID, "", "v2.0.0", "server.example.com", "server.example.com", "")
	d.Flush(ctx, serverID)
	if len(requester.sent) != 2 || requester.sent[1].Type != shared.TypeApplyNode {
		t.Fatalf("queued revision was not released after sync: %+v", requester.sent)
	}
}

func newForcePublishFixture(t *testing.T, entryPort int, serviceRealized shared.RealizedConfig) (*store.Store, *Dispatcher, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "entry-token", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "exit-token", store.MachineTypeDirect, "", "", "US", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "service", exitID, shared.ProtocolVLESS, nil, config)
	realized, _ := json.Marshal(serviceRealized)
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	chainID, _ := st.InsertChain(ctx, "chain")
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, entryPort, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, serviceRealized.Port, "")
	if err := st.SetChainServiceNode(ctx, chainID, nodeID); err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		ApplyKeys: []string{revisionPieceKey(RevisionPieceForward, entryHopID)},
		Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: entryPort},
			{HopID: exitHopID, ServerID: exitID, Role: store.HopRoleExit, ForwardPort: serviceRealized.Port},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID,
		TaskKey: revisionPieceKey(RevisionPieceForward, entryHopID), Phase: "apply", Action: "apply",
		Kind: RevisionPieceForward, HopID: entryHopID, ServerID: entryID}); err != nil {
		t.Fatal(err)
	}
	requester := &fakeRequester{online: map[int64]bool{entryID: false, exitID: true}}
	return st, New(st, requester), chainID, entryID, revision.ID
}

func TestForcePublishAllowsConfirmedExitAndFixedOfflineEntry(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _, revisionID := newForcePublishFixture(t, 1443,
		shared.RealizedConfig{Port: 2443, PublicKey: "public-key"})
	defer st.Close()

	if err := d.ForcePublishRevision(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.PublishedRevisionID != revisionID || chain.DesiredRevisionID != revisionID ||
		chain.Status != store.ChainStatusActiveUnconfirmed {
		t.Fatalf("forced chain = %+v", chain)
	}
}

func TestForcePublishRejectsUnconfirmedAutomaticEntryPort(t *testing.T) {
	st, d, chainID, _, _ := newForcePublishFixture(t, 0,
		shared.RealizedConfig{Port: 2443, PublicKey: "public-key"})
	defer st.Close()

	err := d.ForcePublishRevision(context.Background(), chainID)
	if err == nil || !strings.Contains(err.Error(), "入口 Agent 尚未确认") {
		t.Fatalf("force publish error = %v", err)
	}
}

func TestForcePublishRejectsVersionMismatch(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, entryID, _ := newForcePublishFixture(t, 1443,
		shared.RealizedConfig{Port: 2443, PublicKey: "public-key"})
	defer st.Close()
	if err := st.TouchServer(ctx, entryID, "", "v1.0.0", "entry.example.com", "entry.example.com", ""); err != nil {
		t.Fatal(err)
	}
	d.PanelVersion = "v2.0.0"

	err := d.ForcePublishRevision(ctx, chainID)
	if err == nil || !strings.Contains(err.Error(), "须先同步到 Panel 版本 v2.0.0") {
		t.Fatalf("force publish error = %v", err)
	}
}

func TestForcedServiceRealizedRejectsMissingRealityPublicKey(t *testing.T) {
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS})
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 2443})
	_, err := forcedServiceRealized(config, realized)
	if err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Fatalf("forced realized error = %v", err)
	}
}

func TestCleanupPendingReturnsActiveAfterAllCleanupTasksAck(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "server", "server.example.com", "token", store.MachineTypeDirect, "", "", "US", "")
	chainID, _ := st.InsertChain(ctx, "chain")
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", TrafficMultiplierMilli: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID,
		TaskKey: "cleanup/forward/7", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 7, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	d := New(st, &fakeRequester{online: map[int64]bool{serverID: false}})
	d.refreshCleanupStatus(ctx, revision.ID)
	if chain, _ := st.ChainByID(ctx, chainID); chain.Status != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", chain.Status)
	}
	if err := st.SetRevisionTaskResult(ctx, taskID, true, ""); err != nil {
		t.Fatal(err)
	}
	d.refreshCleanupStatus(ctx, revision.ID)
	if chain, _ := st.ChainByID(ctx, chainID); chain.Status != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active", chain.Status)
	}
}
