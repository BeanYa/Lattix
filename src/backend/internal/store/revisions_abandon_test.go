package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

// TestAbandonChainRevisionAbandonsQueuedWork 验证被新编辑取代的失败 revision：
// pending/queued 任务与 queued/sent 命令被置 abandoned，revision 置 cancelled，
// 已 acked/failed 的终态不受影响。
func TestAbandonChainRevisionAbandonsQueuedWork(t *testing.T) {
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
	if err := st.SetChainRevisionStatus(ctx, revision.ID, RevisionStatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	pendingTaskID, _ := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revision.ID, TaskKey: "forward/1",
		Phase: "apply", Action: "apply", Kind: "forward", HopID: entryHopID, ServerID: entryID})
	queuedTaskID, _ := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revision.ID, TaskKey: "forward/2",
		Phase: "apply", Action: "apply", Kind: "forward", HopID: exitHopID, ServerID: entryID})
	_, err = st.EnqueueRevisionTaskCommand(ctx, "request", "trace", entryID, shared.TypeApplyChainHop,
		json.RawMessage(`{}`), revision.ID, "forward/2")
	if err != nil {
		t.Fatal(err)
	}
	ackedTaskID, _ := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revision.ID, TaskKey: "service/3",
		Phase: "apply", Action: "apply", Kind: "service", HopID: nodeID, ServerID: exitID})
	if err := st.SetRevisionTaskResult(ctx, ackedTaskID, true, ""); err != nil {
		t.Fatal(err)
	}
	failedTaskID, _ := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revision.ID, TaskKey: "cleanup/forward/1",
		Phase: "cleanup", Action: "remove", Kind: "forward", HopID: entryHopID, ServerID: entryID})
	if err := st.SetRevisionTaskResult(ctx, failedTaskID, false, "boom"); err != nil {
		t.Fatal(err)
	}

	if err := st.AbandonChainRevision(ctx, revision.ID, "superseded by edit"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.ChainRevisionByID(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != RevisionStatusCancelled || reloaded.Error != "superseded by edit" {
		t.Fatalf("superseded revision = %+v", reloaded)
	}
	tasks, _ := st.RevisionTasks(ctx, revision.ID)
	byID := map[int64]ChainRevisionTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	if task := byID[pendingTaskID]; task.Status != RevisionTaskAbandoned || task.Error != "superseded by edit" {
		t.Fatalf("pending task = %+v", task)
	}
	if task := byID[queuedTaskID]; task.Status != RevisionTaskAbandoned || task.Error != "superseded by edit" {
		t.Fatalf("queued task = %+v", task)
	}
	if task := byID[ackedTaskID]; task.Status != RevisionTaskAcked {
		t.Fatalf("acked task must be preserved, got %+v", task)
	}
	if task := byID[failedTaskID]; task.Status != RevisionTaskFailed {
		t.Fatalf("failed task must be preserved, got %+v", task)
	}
	command, _ := st.CommandByRequestID(ctx, "request")
	if command.Status != CommandStatusAbandoned || command.Error != "superseded by edit" {
		t.Fatalf("abandoned command = %+v", command)
	}
}
