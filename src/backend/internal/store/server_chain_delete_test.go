package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

func TestInvalidateChainAbandonsTasksAndPreservesServiceIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "entry-token", MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "exit-token", MachineTypeDirect, "", "", "US", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "service", exitID, shared.ProtocolVLESS, nil, config)
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	_, _, _ = st.SetUserNodes(ctx, userID, []int64{nodeID})
	chainID, _ := st.InsertChain(ctx, "chain")
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, HopRoleEntry, 0, 1443, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, HopRoleExit, nodeID, 2443, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: exitID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, Hops: []ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: HopRoleEntry, Transport: "direct", ForwardPort: 1443},
			{HopID: exitHopID, ServerID: exitID, Role: HopRoleExit, ForwardPort: 2443},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	desired, _ := st.CreateChainRevision(ctx, chainID, revision.Snapshot)
	taskID, _ := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: desired.ID, TaskKey: "forward/1",
		Phase: "apply", Action: "apply", Kind: "forward", HopID: entryHopID, ServerID: entryID})
	commandID, err := st.EnqueueRevisionTaskCommand(ctx, "request", "trace", entryID, shared.TypeApplyChainHop,
		json.RawMessage(`{}`), desired.ID, "forward/1")
	if err != nil {
		t.Fatal(err)
	}

	affected, err := st.ChainsReferencingServer(ctx, exitID)
	if err != nil || len(affected) != 1 || affected[0].ID != chainID {
		t.Fatalf("affected chains = %+v err=%v", affected, err)
	}
	if err := st.InvalidateChainForServerDeletion(ctx, chainID, exitID, "server deleted"); err != nil {
		t.Fatal(err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != ChainStatusInvalid || chain.DesiredRevisionID != 0 {
		t.Fatalf("invalid chain = %+v", chain)
	}
	task, _ := st.RevisionTaskByCommandID(ctx, commandID)
	if task.ID != taskID || task.Status != RevisionTaskAbandoned {
		t.Fatalf("abandoned task = %+v", task)
	}
	command, _ := st.CommandByRequestID(ctx, "request")
	if command.Status != CommandStatusAbandoned {
		t.Fatalf("abandoned command = %+v", command)
	}
	if chain.ServiceNodeID != nodeID {
		t.Fatalf("service identity before server deletion = %d, want %d", chain.ServiceNodeID, nodeID)
	}
	var identities, archived int
	if err := st.db.QueryRow(`SELECT COUNT(*), COUNT(archived_at) FROM chain_hop_identities WHERE chain_id=?`, chainID).
		Scan(&identities, &archived); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || archived != 2 {
		t.Fatalf("hop identities after invalidation = %d total, %d archived; want 2, 2", identities, archived)
	}
	newHopID, err := st.NextChainHopID(ctx, chainID, entryID)
	if err != nil {
		t.Fatal(err)
	}
	if newHopID <= exitHopID {
		t.Fatalf("new hop id = %d, must not reuse historical id %d", newHopID, exitHopID)
	}
	var deletable int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE server_id = ?
		AND id NOT IN (SELECT service_node_id FROM chains WHERE deleted_at IS NULL)`, exitID).Scan(&deletable); err != nil {
		t.Fatal(err)
	}
	if deletable != 0 {
		t.Fatalf("stable service node matched cascade delete query")
	}
	if err := st.DeleteServerCascade(ctx, exitID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM chain_hop_identities WHERE id=?`, exitHopID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("deleted server hop identity remaining=%d err=%v", remaining, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id=?`, nodeID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("stable service node row remaining=%d err=%v", remaining, err)
	}
	assigned, _ := st.UserNodeIDs(ctx, userID)
	if len(assigned) != 1 || assigned[0] != nodeID {
		t.Fatalf("service assignment = %v", assigned)
	}
}
