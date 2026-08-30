package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCreateInitialChainDeploymentIsAtomic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "broken", ServiceServerID: serverID, ServiceProtocol: "vless",
		ServiceConfig: json.RawMessage(`{"incomplete":`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err == nil {
		t.Fatal("expected invalid revision snapshot to fail")
	}
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chains, err := st.ListChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 || len(chains) != 0 {
		t.Fatalf("failed transaction left partial state: %d nodes, %d chains", len(nodes), len(chains))
	}
}

func TestCreateInitialChainDeploymentStoresCompletePlan(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, err := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.example.com", BootstrapToken: "entry-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "exit-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "relay", ServiceServerID: exitID, ServiceProtocol: "vless",
		ServiceConfig: json.RawMessage(`{"protocol":"vless"}`), TrafficMultiplierMilli: 1250,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 443, TunnelUUID: "tunnel"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChainID == 0 || result.NodeID == 0 || result.RevisionID == 0 {
		t.Fatalf("missing aggregate ids: %+v", result)
	}
	if len(result.Hops) != 2 || len(result.ApplyKeys) != 4 {
		t.Fatalf("unexpected deployment plan: %d hops, %v", len(result.Hops), result.ApplyKeys)
	}
	tasks, err := st.RevisionTasks(ctx, result.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != len(result.ApplyKeys) {
		t.Fatalf("stored %d tasks for %d apply keys", len(tasks), len(result.ApplyKeys))
	}
	tasksByKey := make(map[string]ChainRevisionTask, len(tasks))
	for _, task := range tasks {
		tasksByKey[task.TaskKey] = task
	}
	wantServers := map[string]int64{
		fmt.Sprintf("%s/%d", RevisionPieceService, result.NodeID):        exitID,
		fmt.Sprintf("%s/%d", RevisionPieceForward, result.Hops[0].HopID): entryID,
		fmt.Sprintf("%s/%d", RevisionPiecePortal, result.Hops[0].HopID):  entryID,
		fmt.Sprintf("%s/%d", RevisionPieceBridge, result.Hops[1].HopID):  exitID,
	}
	for key, wantServer := range wantServers {
		if got := tasksByKey[key].ServerID; got != wantServer {
			t.Fatalf("task %s server = %d, want %d", key, got, wantServer)
		}
	}
}

func TestCreateInitialChainDeploymentRejectsTerminalTunnel(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.CreateInitialChainDeployment(context.Background(), InitialChainDeployment{
		Name: "broken", ServiceServerID: 1, ServiceProtocol: "vless",
		ServiceConfig: json.RawMessage(`{"protocol":"vless"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{{ServerID: 1, Role: HopRoleExit, TunnelUUID: "dangling"}},
	})
	if err == nil {
		t.Fatal("terminal tunnel was accepted")
	}
}
