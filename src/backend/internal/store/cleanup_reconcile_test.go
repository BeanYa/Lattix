package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"lattix/shared"
)

// newCleanupTaskRevision 构造一条链与一个 revision（快照无跳），供清理任务级联/自愈测试使用。
func newCleanupTaskRevision(t *testing.T, st *Store) (chainID, revisionID int64) {
	t.Helper()
	ctx := context.Background()
	chainID, err := st.InsertChain(ctx, "cleanup-chain")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: "cleanup-chain", TrafficMultiplierMilli: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return chainID, revision.ID
}

// TestDeleteServerCascadeAbandonsPendingRevisionTasks 验证删除服务器时，该服务器上
// pending/queued 的 revision 任务在同一级联事务内被置 abandoned（避免悬挂任务
// 永久阻塞链 cleanup_pending），已终结任务与其他服务器任务不受影响。
func TestDeleteServerCascadeAbandonsPendingRevisionTasks(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	deletedID, _ := st.CreateServer(ctx, ServerDraft{Alias: "old", Address: "old.example.com", BootstrapToken: "old-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	keptID, _ := st.CreateServer(ctx, ServerDraft{Alias: "kept", Address: "kept.example.com", BootstrapToken: "kept-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	_, revisionID := newCleanupTaskRevision(t, st)

	pendingTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/1", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 1, ServerID: deletedID})
	if err != nil {
		t.Fatal(err)
	}
	queuedTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/2", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 2, ServerID: deletedID})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := st.EnqueueRevisionTaskCommand(ctx, "req-cleanup", "trace", deletedID,
		shared.TypeRemoveChainHop, json.RawMessage(`{}`), revisionID, "cleanup/forward/2")
	if err != nil {
		t.Fatal(err)
	}
	keptTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/3", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 3, ServerID: keptID})
	if err != nil {
		t.Fatal(err)
	}
	ackedTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/service/4", Phase: "cleanup", Action: "remove", Kind: RevisionPieceService,
		HopID: 4, ServerID: deletedID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRevisionTaskResult(ctx, ackedTaskID, true, ""); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteServerCascade(ctx, deletedID); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.RevisionTasks(ctx, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]ChainRevisionTask, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	if task := byID[pendingTaskID]; task.Status != RevisionTaskAbandoned || task.Error == "" {
		t.Fatalf("pending cleanup task = %+v, want abandoned with reason", task)
	}
	if task := byID[queuedTaskID]; task.Status != RevisionTaskAbandoned || task.Error == "" {
		t.Fatalf("queued cleanup task = %+v, want abandoned with reason", task)
	}
	if task := byID[keptTaskID]; task.Status != RevisionTaskPending {
		t.Fatalf("other server task = %+v, want pending unchanged", task)
	}
	if task := byID[ackedTaskID]; task.Status != RevisionTaskAcked {
		t.Fatalf("acked cleanup task = %+v, want acked preserved", task)
	}
	if _, err := st.CommandByID(ctx, commandID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted server command lookup error = %v, want ErrNotFound", err)
	}
}

// TestResetQueuedRevisionTaskCAS 验证带 CAS 的 queued 清理任务复位：command_id
// 不匹配时不生效（并发已变化），匹配时复位为 pending 并解绑命令。
func TestResetQueuedRevisionTaskCAS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "srv", Address: "srv.example.com", BootstrapToken: "srv-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	_, revisionID := newCleanupTaskRevision(t, st)
	taskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/1", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 1, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := st.EnqueueRevisionTaskCommand(ctx, "req-cleanup", "trace", serverID,
		shared.TypeRemoveChainHop, json.RawMessage(`{}`), revisionID, "cleanup/forward/1")
	if err != nil {
		t.Fatal(err)
	}

	reset, err := st.ResetQueuedRevisionTask(ctx, taskID, commandID+100)
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Fatal("reset with mismatched command id must not take effect")
	}
	task, err := st.RevisionTaskByCommandID(ctx, commandID)
	if err != nil || task.Status != RevisionTaskQueued || task.CommandID != commandID {
		t.Fatalf("task after mismatched reset = %+v err=%v", task, err)
	}

	reset, err = st.ResetQueuedRevisionTask(ctx, taskID, commandID)
	if err != nil || !reset {
		t.Fatalf("reset queued task = %v err=%v", reset, err)
	}
	tasks, err := st.RevisionTasks(ctx, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != RevisionTaskPending || tasks[0].CommandID != 0 || tasks[0].Error != "" {
		t.Fatalf("reset task = %+v", tasks)
	}
	reset, err = st.ResetQueuedRevisionTask(ctx, taskID, commandID)
	if err != nil || reset {
		t.Fatalf("second reset of pending task = %v err=%v, want false", reset, err)
	}
}

// TestAbandonRevisionTaskCAS 验证未终结任务可废弃、终态任务不可翻写。
func TestAbandonRevisionTaskCAS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "srv", Address: "srv.example.com", BootstrapToken: "srv-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	_, revisionID := newCleanupTaskRevision(t, st)
	pendingTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/1", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 1, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	ackedTaskID, err := st.AddRevisionTask(ctx, ChainRevisionTask{RevisionID: revisionID,
		TaskKey: "cleanup/forward/2", Phase: "cleanup", Action: "remove", Kind: RevisionPieceForward,
		HopID: 2, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRevisionTaskResult(ctx, ackedTaskID, true, ""); err != nil {
		t.Fatal(err)
	}

	abandoned, err := st.AbandonRevisionTask(ctx, pendingTaskID, "服务器已删除")
	if err != nil || !abandoned {
		t.Fatalf("abandon pending task = %v err=%v", abandoned, err)
	}
	abandoned, err = st.AbandonRevisionTask(ctx, ackedTaskID, "服务器已删除")
	if err != nil || abandoned {
		t.Fatalf("abandon acked task = %v err=%v, want false", abandoned, err)
	}
	tasks, err := st.RevisionTasks(ctx, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]ChainRevisionTask, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	if task := byID[pendingTaskID]; task.Status != RevisionTaskAbandoned || task.Error != "服务器已删除" {
		t.Fatalf("abandoned task = %+v", task)
	}
	if task := byID[ackedTaskID]; task.Status != RevisionTaskAcked {
		t.Fatalf("acked task must be preserved, got %+v", task)
	}
}

// TestCommandByID 验证按主键取命令与缺失语义（清理任务判定悬空命令用）。
func TestCommandByID(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "srv", Address: "srv.example.com", BootstrapToken: "srv-token", MachineType: MachineTypeDirect, CountryCode: "US"})
	commandID, err := st.EnqueueCommand(ctx, "req", "trace", serverID, shared.TypeRemoveChainHop, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	command, err := st.CommandByID(ctx, commandID)
	if err != nil || command.ID != commandID || command.Status != CommandStatusQueued || command.ServerID != serverID {
		t.Fatalf("command by id = %+v err=%v", command, err)
	}
	if _, err := st.CommandByID(ctx, commandID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing command error = %v, want ErrNotFound", err)
	}
}
