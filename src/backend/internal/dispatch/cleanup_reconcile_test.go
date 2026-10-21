package dispatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

const cleanupForwardKey = "cleanup/forward/7"

// cleanupFixture 构造“已发布新 revision + 旧服务器清理任务”的链路：
// 当前拓扑只有 newServer 上一跳，新 revision 携带一条目标为 oldServer 的 cleanup 任务，
// 发布后链处于 active 且 desired_revision_id=0（正常发布后的状态）。
type cleanupFixture struct {
	st       *store.Store
	d        *Dispatcher
	req      *fakeRequester
	chainID  int64
	revision *store.ChainRevision
	nodeID   int64
	hopID    int64
	oldID    int64
	newID    int64
	taskID   int64
}

func newCleanupFixture(t *testing.T, oldOnline, withCleanupTask bool) *cleanupFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	oldID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "old", Address: "old.example.com",
		BootstrapToken: "old-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	newID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "new", Address: "new.example.com",
		BootstrapToken: "new-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "service", newID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "chain")
	if err != nil {
		t.Fatal(err)
	}
	hopID, err := st.InsertChainHop(ctx, chainID, 0, newID, store.HopRoleExit, nodeID, 2443, "")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: newID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.ChainRevisionHop{{HopID: hopID, ServerID: newID, Role: store.HopRoleExit, ForwardPort: 2443}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := int64(0)
	if withCleanupTask {
		taskID, err = st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID,
			TaskKey: cleanupForwardKey, Phase: "cleanup", Action: "remove",
			Kind: store.RevisionPieceForward, HopID: 7, ServerID: oldID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{newID: true, oldID: oldOnline}}
	d := New(st, req, Options{}, Events{})
	return &cleanupFixture{st: st, d: d, req: req, chainID: chainID, revision: revision,
		nodeID: nodeID, hopID: hopID, oldID: oldID, newID: newID, taskID: taskID}
}

func (f *cleanupFixture) chainStatus(t *testing.T) string {
	t.Helper()
	chain, err := f.st.ChainByID(context.Background(), f.chainID)
	if err != nil {
		t.Fatal(err)
	}
	return chain.Status
}

func (f *cleanupFixture) cleanupTask(t *testing.T) store.ChainRevisionTask {
	t.Helper()
	tasks, err := f.st.RevisionTasks(context.Background(), f.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.TaskKey == cleanupForwardKey {
			return task
		}
	}
	t.Fatalf("cleanup task %s not found", cleanupForwardKey)
	return store.ChainRevisionTask{}
}

func (f *cleanupFixture) removeCommands(t *testing.T) []store.Command {
	t.Helper()
	cmds, err := f.st.CommandsByType(context.Background(), shared.TypeRemoveChainHop)
	if err != nil {
		t.Fatal(err)
	}
	return cmds
}

// TestCleanupOfflineTargetDoesNotBlockChain 覆盖用户上报场景：旧 revision 清理任务的
// 目标服务器离线（已替换下线）时，清理任务不得把链卡在 cleanup_pending；命令留在
// commands 队列，Agent 重连后照常下发，回执后链保持 active。
func TestCleanupOfflineTargetDoesNotBlockChain(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("offline cleanup target must not block chain, status = %s", got)
	}
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued || task.CommandID == 0 {
		t.Fatalf("cleanup task = %+v, want queued with command", task)
	}
	cmds := f.removeCommands(t)
	if len(cmds) != 1 || cmds[0].Status != store.CommandStatusQueued {
		t.Fatalf("cleanup commands = %+v, want single queued command", cmds)
	}
	// Agent 重连：滞留命令照常下发（离线语义）。
	f.req.setOnline(f.oldID, true)
	f.d.OnAgentConnect(ctx, f.oldID)
	cmds = f.removeCommands(t)
	if len(cmds) != 1 || cmds[0].Status != store.CommandStatusSent {
		t.Fatalf("cleanup command after reconnect = %+v, want sent", cmds)
	}
	// 目标重新可达后清理任务参与展示（cleanup_pending），回执后回 active。
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status after reconnect = %s, want cleanup_pending", got)
	}
	f.d.setRevisionTaskResult(ctx, cmds[0].ID, true, "")
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status after ack = %s, want active", got)
	}
}

// TestCleanupPendingHealsWhenTargetGoesOffline 覆盖“链已卡在 cleanup_pending，目标服务器随后离线”：
// reconcile 自愈后链回 active，未完成的任务/命令保留（重连后继续清理）。
func TestCleanupPendingHealsWhenTargetGoesOffline(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	f.req.setOnline(f.oldID, false)
	f.d.ReconcileChainCleanups(ctx)
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status after target offline = %s, want active", got)
	}
	if task := f.cleanupTask(t); task.Status != store.RevisionTaskQueued {
		t.Fatalf("cleanup task = %+v, want queued preserved", task)
	}
}

// TestFailedCleanupTaskDoesNotBlockChain 验证 failed 清理任务是终态：清理命令死信/失败后
// 链不得继续停在 cleanup_pending。
func TestFailedCleanupTaskDoesNotBlockChain(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	cmds := f.removeCommands(t)
	if len(cmds) != 1 {
		t.Fatalf("cleanup commands = %+v", cmds)
	}
	f.d.setRevisionTaskResult(ctx, cmds[0].ID, false, "remove failed")
	if task := f.cleanupTask(t); task.Status != store.RevisionTaskFailed {
		t.Fatalf("cleanup task = %+v, want failed", task)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active after failed cleanup", got)
	}
}

// TestReconcileEnqueuesPendingCleanupTask 验证 reconcile 补入未入队的 pending 清理任务，
// 且重复运行幂等（不重复入队）。
func TestReconcileEnqueuesPendingCleanupTask(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, true)
	f.d.ReconcileChainCleanups(ctx)
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued || task.CommandID == 0 {
		t.Fatalf("cleanup task = %+v, want queued with command", task)
	}
	if cmds := f.removeCommands(t); len(cmds) != 1 || cmds[0].Status != store.CommandStatusQueued {
		t.Fatalf("cleanup commands = %+v, want single queued command", cmds)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active (offline cleanup target)", got)
	}
	f.d.ReconcileChainCleanups(ctx)
	if cmds := f.removeCommands(t); len(cmds) != 1 {
		t.Fatalf("reconcile must be idempotent, commands = %+v", cmds)
	}
}

// TestReconcileReenqueuesCleanupTaskWithTerminalCommand 验证 queued 清理任务关联命令已终态
// （回执先到、任务回写丢失的崩溃窗口）时，CAS 复位后重新入队。
func TestReconcileReenqueuesCleanupTaskWithTerminalCommand(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, true)
	oldCommandID, err := f.st.EnqueueRevisionTaskCommand(ctx, "req-old", "trace", f.oldID,
		shared.TypeRemoveChainHop, json.RawMessage(`{}`), f.revision.ID, cleanupForwardKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.MarkCommandSent(ctx, oldCommandID); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.st.MarkCommandAcked(ctx, oldCommandID); err != nil || !ok {
		t.Fatalf("ack old command: %v ok=%v", err, ok)
	}

	f.d.ReconcileChainCleanups(ctx)
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued || task.CommandID == 0 || task.CommandID == oldCommandID {
		t.Fatalf("cleanup task = %+v, want re-enqueued with new command", task)
	}
	command, err := f.st.CommandByID(ctx, task.CommandID)
	if err != nil || command.Status != store.CommandStatusQueued {
		t.Fatalf("re-enqueued command = %+v err=%v", command, err)
	}
	if cmds := f.removeCommands(t); len(cmds) != 2 {
		t.Fatalf("cleanup commands = %d, want 2 (acked + re-enqueued)", len(cmds))
	}
}

// TestReconcileReenqueuesCleanupTaskWithDanglingCommand 验证 queued 清理任务的 command_id
// 悬空（命令行已被级联删除）时复位并重新入队。
func TestReconcileReenqueuesCleanupTaskWithDanglingCommand(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, true)
	if err := f.st.LinkRevisionTaskByKey(ctx, f.revision.ID, cleanupForwardKey, 987654); err != nil {
		t.Fatal(err)
	}
	f.d.ReconcileChainCleanups(ctx)
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued || task.CommandID == 0 || task.CommandID == 987654 {
		t.Fatalf("cleanup task = %+v, want re-enqueued command", task)
	}
	if command, err := f.st.CommandByID(ctx, task.CommandID); err != nil || command.Status != store.CommandStatusQueued {
		t.Fatalf("re-enqueued command = %+v err=%v", command, err)
	}
}

// TestReconcileAbandonsCleanupTaskForMissingServer 验证目标服务器已不存在时 reconcile
// 废弃任务，不产生新命令、不阻塞链。
func TestReconcileAbandonsCleanupTaskForMissingServer(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, false)
	taskID, err := f.st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: f.revision.ID,
		TaskKey: "cleanup/forward/8", Phase: "cleanup", Action: "remove",
		Kind: store.RevisionPieceForward, HopID: 8, ServerID: 987654})
	if err != nil {
		t.Fatal(err)
	}
	f.d.ReconcileChainCleanups(ctx)
	tasks, err := f.st.RevisionTasks(ctx, f.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var abandoned *store.ChainRevisionTask
	for i := range tasks {
		if tasks[i].ID == taskID {
			abandoned = &tasks[i]
		}
	}
	if abandoned == nil || abandoned.Status != store.RevisionTaskAbandoned {
		t.Fatalf("missing-server cleanup task = %+v, want abandoned", abandoned)
	}
	if cmds := f.removeCommands(t); len(cmds) != 0 {
		t.Fatalf("missing-server cleanup must not enqueue commands, got %+v", cmds)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active", got)
	}
}

// TestDeleteServerCascadeRestoresCleanupPendingChain 验证删除服务器：级联事务废弃该服务器
// 的清理任务，随后 reconcile 让卡住的 cleanup_pending 链回 active。
func TestDeleteServerCascadeRestoresCleanupPendingChain(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	if err := f.st.DeleteServerCascade(ctx, f.oldID); err != nil {
		t.Fatal(err)
	}
	if task := f.cleanupTask(t); task.Status != store.RevisionTaskAbandoned {
		t.Fatalf("cleanup task after server delete = %+v, want abandoned", task)
	}
	f.d.ReconcileChainCleanups(ctx)
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status after reconcile = %s, want active", got)
	}
}

// TestStaleCleanupPendingWithoutCleanupTasksRestores 验证 cleanup_pending 链已无清理任务时
// reconcile 自愈回 active（历史残留状态）。
func TestStaleCleanupPendingWithoutCleanupTasksRestores(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, false)
	if err := f.st.SetChainStatus(ctx, f.chainID, store.ChainStatusCleanupPending, "旧 revision 配置等待清理"); err != nil {
		t.Fatal(err)
	}
	f.d.ReconcileChainCleanups(ctx)
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active", got)
	}
}

// TestReconcileChainCleanupsConcurrentRunsEnqueueOnce 验证并发（60s ticker + Agent 重连）
// 触发时同一清理任务只入队一条命令。
func TestReconcileChainCleanupsConcurrentRunsEnqueueOnce(t *testing.T) {
	f := newCleanupFixture(t, false, true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.d.ReconcileChainCleanups(context.Background())
		}()
	}
	wg.Wait()
	if cmds := f.removeCommands(t); len(cmds) != 1 {
		t.Fatalf("cleanup commands = %d, want exactly 1 (concurrent reconcile guard)", len(cmds))
	}
}

// TestResumeChainsHealsStaleCleanupPending 验证启动入口（ResumeChains）自愈卡住的
// cleanup_pending：旧 revision 清理目标离线时，重启后链恢复 active（命令仍在队列）。
func TestResumeChainsHealsStaleCleanupPending(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	f.req.setOnline(f.oldID, false)
	if err := f.d.ResumeChains(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status after resume = %s, want active", got)
	}
}
