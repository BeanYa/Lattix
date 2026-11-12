package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCleanupAckBeforeMarkSentCompletesTask 覆盖用户上报路径：Flush 的 send →
// MarkCommandSent 落库窗口内回执先到（命令仍 queued），回执必须完成清理任务，
// 否则命令永久 sent、任务永久 queued，链永久停在 cleanup_pending。
func TestCleanupAckBeforeMarkSentCompletesTask(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, false, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	cmds := f.removeCommands(t)
	if len(cmds) != 1 || cmds[0].Status != store.CommandStatusQueued {
		t.Fatalf("cleanup commands = %+v, want single queued command", cmds)
	}
	f.req.setOnline(f.oldID, true)
	// 命令仍是 queued 时投递 Agent 成功回执（回执先于 MarkCommandSent）。
	d := f.d
	d.handleCommandResponse(f.oldID, shared.Envelope{Kind: shared.KindResponse,
		Type: shared.TypeRemoveChainHop, RequestID: cmds[0].RequestID, TraceID: cmds[0].TraceID,
		Code: shared.CodeOK, Data: json.RawMessage(`{}`)})
	command, err := f.st.CommandByID(ctx, cmds[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusAcked {
		t.Fatalf("command %d status = %s, want acked (queued response must complete)", command.ID, command.Status)
	}
	if task := f.cleanupTask(t); task.Status != store.RevisionTaskAcked {
		t.Fatalf("cleanup task = %+v, want acked", task)
	}
	// Flush 随后的 MarkCommandSent 不得把 acked 翻回 sent。
	if err := f.st.MarkCommandSent(ctx, cmds[0].ID); err != nil {
		t.Fatal(err)
	}
	if command, _ := f.st.CommandByID(ctx, cmds[0].ID); command.Status != store.CommandStatusAcked {
		t.Fatalf("command flipped by late MarkCommandSent: %s", command.Status)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status = %s, want active", got)
	}
}

// TestCleanupSentTimeoutRequeuesTask 覆盖回执丢失且连接不重建：清理命令 sent 超过
// cleanupCommandSentTimeout 后由 reconcile 复位同一条命令重发（attempts 递增），
// 达到投递上限后走既有死信路径，任务 failed 终态、链不再被 cleanup_pending 阻塞。
func TestCleanupSentTimeoutRequeuesTask(t *testing.T) {
	ctx := context.Background()
	previous := cleanupCommandSentTimeout
	cleanupCommandSentTimeout = -time.Second // 立即超时（时间戳为 now）
	defer func() { cleanupCommandSentTimeout = previous }()

	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	original := f.removeCommands(t)
	if len(original) != 1 || original[0].Status != store.CommandStatusSent {
		t.Fatalf("cleanup commands = %+v, want single sent command", original)
	}
	// 第一次自愈：同一命令复位重发，attempts 递增，任务仍 queued 并绑定同一命令。
	f.d.ReconcileChainCleanups(ctx)
	command, err := f.st.CommandByID(ctx, original[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusSent || command.Attempts != 2 {
		t.Fatalf("command = %+v, want resent sent with attempts=2", command)
	}
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued || task.CommandID != original[0].ID {
		t.Fatalf("cleanup task = %+v, want still queued on the same command", task)
	}
	if cmds := f.removeCommands(t); len(cmds) != 1 {
		t.Fatalf("cleanup commands = %+v, want no duplicate command", cmds)
	}
	// 持续无回执：重发至投递上限后死信，任务 failed 终态并释放链状态。
	for i := 0; i < maxCommandAttempts+2; i++ {
		f.d.ReconcileChainCleanups(ctx)
		if f.cleanupTask(t).Status == store.RevisionTaskFailed {
			break
		}
	}
	if task := f.cleanupTask(t); task.Status != store.RevisionTaskFailed {
		t.Fatalf("cleanup task = %+v, want failed after delivery cap", task)
	}
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("chain status after dead-letter = %s, want active", got)
	}
}

// TestCleanupSentTimeoutUsesRealTimestamp 覆盖真实驱动时间戳格式（RFC3339）：用极小阈值
// 验证 sent 命令按实际写入时间超时。
func TestCleanupSentTimeoutUsesRealTimestamp(t *testing.T) {
	ctx := context.Background()
	previous := cleanupCommandSentTimeout
	cleanupCommandSentTimeout = time.Millisecond
	defer func() { cleanupCommandSentTimeout = previous }()

	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	cmd := f.removeCommands(t)
	if len(cmd) != 1 || cmd[0].Status != store.CommandStatusSent {
		t.Fatalf("cleanup commands = %+v, want single sent command", cmd)
	}
	time.Sleep(10 * time.Millisecond)
	f.d.ReconcileChainCleanups(ctx)
	command, err := f.st.CommandByID(ctx, cmd[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Attempts < 2 || command.Status != store.CommandStatusSent {
		t.Fatalf("command = %+v, want resent with attempts>=2", command)
	}
}

// TestParseCommandTimestamp 覆盖 commands.updated_at 的驱动扫描格式（RFC3339）与
// SQLite CURRENT_TIMESTAMP 文本格式。
func TestParseCommandTimestamp(t *testing.T) {
	cases := []struct {
		value string
		ok    bool
	}{
		{"2026-11-10T03:32:35Z", true},
		{"2026-11-10T03:32:35.123456+08:00", true},
		{"2026-11-10 03:32:35", true},
		{"", false},
		{"not-a-time", false},
	}
	for _, c := range cases {
		if _, ok := parseCommandTimestamp(c.value); ok != c.ok {
			t.Errorf("parseCommandTimestamp(%q) ok = %v, want %v", c.value, ok, c.ok)
		}
	}
}

// TestVersionGatedCleanupDoesNotBlockChain 覆盖「目标在线但版本门控导致清理命令永远
// 投不出去」：清理任务不得维持 cleanup_pending；版本同步完成后清理进度恢复展示。
func TestVersionGatedCleanupDoesNotBlockChain(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.opts.PanelVersion = "v2.0.0"
	for _, serverID := range []int64{f.oldID, f.newID} {
		if err := f.st.TouchServer(ctx, serverID, "", "v1.0.0", "x.example.com", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusActive {
		t.Fatalf("version-gated cleanup must not block chain, status = %s", got)
	}
	task := f.cleanupTask(t)
	if task.Status != store.RevisionTaskQueued {
		t.Fatalf("cleanup task = %+v, want queued preserved", task)
	}
	cmds := f.removeCommands(t)
	if len(cmds) != 1 || cmds[0].Status != store.CommandStatusQueued || cmds[0].Attempts != 0 {
		t.Fatalf("cleanup commands = %+v, want queued attempts=0 while gated", cmds)
	}
	// Agent 同步到面板版本后，命令可投递，清理任务重新参与展示。
	for _, serverID := range []int64{f.oldID, f.newID} {
		if err := f.st.TouchServer(ctx, serverID, "", "v2.0.0", "x.example.com", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	f.d.ReconcileChainCleanups(ctx)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status after version sync = %s, want cleanup_pending", got)
	}
}

// TestReconcileAgentVersionsEnqueuesUpgradeOnce 覆盖版本门控的周期补偿：在线且版本
// 不一致的服务器补发 Agent 升级命令，且重复运行不重复入队。
func TestReconcileAgentVersionsEnqueuesUpgradeOnce(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, false)
	f.d.opts.PanelVersion = "v2.0.0"
	for _, serverID := range []int64{f.oldID, f.newID} {
		if err := f.st.TouchServer(ctx, serverID, "", "v1.0.0", "x.example.com", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	f.d.ReconcileAgentVersions(ctx)
	first, err := f.st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("upgrade commands = %d, want 2 (one per mismatched online server)", len(first))
	}
	// 模拟升级失败（终态化），退避窗口内不得重复入队。
	for _, command := range first {
		if _, err := f.st.MarkCommandFailedWithError(ctx, command.ID, "download failed"); err != nil {
			t.Fatal(err)
		}
	}
	f.d.ReconcileAgentVersions(ctx)
	second, err := f.st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Fatalf("upgrade commands within backoff = %d, want %d (no duplicate)", len(second), len(first))
	}
	// 退避窗口过后才允许再次补偿（无法升级的服务器不得每 60s 堆积升级命令）。
	f.d.versionRetryMu.Lock()
	for id, state := range f.d.versionRetry {
		state.at = time.Now().Add(-2 * versionUpgradeRetryInterval)
		f.d.versionRetry[id] = state
	}
	f.d.versionRetryMu.Unlock()
	f.d.ReconcileAgentVersions(ctx)
	third, err := f.st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 2*len(first) {
		t.Fatalf("upgrade commands after backoff = %d, want %d (retry once per window)",
			len(third), 2*len(first))
	}
	// 目标版本变化立即重置退避（面板升级后需要尽快重试）。
	for _, command := range third[len(first):] {
		if _, err := f.st.MarkCommandFailedWithError(ctx, command.ID, "download failed"); err != nil {
			t.Fatal(err)
		}
	}
	f.d.versionRetryMu.Lock()
	for id := range f.d.versionRetry {
		f.d.versionRetry[id] = versionRetryState{version: "v1.0.0", at: time.Now()}
	}
	f.d.versionRetryMu.Unlock()
	f.d.opts.PanelVersion = "v3.0.0"
	f.d.ReconcileAgentVersions(ctx)
	afterVersionBump, err := f.st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterVersionBump) <= len(third) {
		t.Fatalf("upgrade commands after version bump = %d, want more than %d", len(afterVersionBump), len(third))
	}
}

func TestReconcileAgentVersionsDeliversExistingUpgrade(t *testing.T) {
	for _, status := range []string{store.CommandStatusQueued, store.CommandStatusSent} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			f := newCleanupFixture(t, false, false)
			f.d.opts.PanelVersion = "v2.0.0"
			if err := f.st.TouchServer(ctx, f.newID, "", "v1.0.0", "new.example.com", "", ""); err != nil {
				t.Fatal(err)
			}
			id, err := f.st.EnqueueCommand(ctx, "req-upgrade", "trace", f.newID, shared.TypeUpgradeAgent,
				json.RawMessage(`{"version":"v2.0.0"}`))
			if err != nil {
				t.Fatal(err)
			}
			wantAttempts := 1
			if status == store.CommandStatusSent {
				if err := f.st.MarkCommandSent(ctx, id); err != nil {
					t.Fatal(err)
				}
				previous := versionUpgradeRetryInterval
				versionUpgradeRetryInterval = -time.Second
				t.Cleanup(func() { versionUpgradeRetryInterval = previous })
				wantAttempts++
			}
			f.d.ReconcileAgentVersions(ctx)
			command, err := f.st.CommandByID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if command.Status != store.CommandStatusSent || command.Attempts != wantAttempts {
				t.Fatalf("command status=%s attempts=%d, want sent attempts=%d", command.Status, command.Attempts, wantAttempts)
			}
			if commands, err := f.st.CommandsByType(ctx, shared.TypeUpgradeAgent); err != nil || len(commands) != 1 {
				t.Fatalf("upgrade commands=%d err=%v, want one", len(commands), err)
			}
		})
	}
}

// TestCleanupPendingDegradesWhenHopOffline 覆盖「cleanup_pending 掩盖运行时不健康」：
// 清理任务阻塞期间某跳离线时，链必须转 degraded（可告警、原因可见），且周期 refresh
// 不得把 degraded 翻回 cleanup_pending（防抖动）。
func TestCleanupPendingDegradesWhenHopOffline(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	if got := f.chainStatus(t); got != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending", got)
	}
	f.req.setOnline(f.newID, false)
	f.d.RecomputeChainsByServer(f.newID)
	chain, err := f.st.ChainByID(ctx, f.chainID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Status != store.ChainStatusDegraded {
		t.Fatalf("chain status = %s, want degraded (cleanup must not mask runtime failure)", chain.Status)
	}
	if !strings.Contains(chain.Error, "离线") {
		t.Fatalf("chain error = %q, want offline reason", chain.Error)
	}
	// 周期 refresh/重入队不得把 degraded 翻回 cleanup_pending。
	f.d.enqueueCleanupTasks(ctx, f.revision)
	f.d.ReconcileChainCleanups(ctx)
	if got := f.chainStatus(t); got != store.ChainStatusDegraded {
		t.Fatalf("chain status after refresh = %s, want degraded (no flap)", got)
	}
}

// sharedCleanupFixture 构造「已发布共享入口链 + 清理任务」夹具：端点部署在 entry 上，
// 清理任务目标服务器由 cleanupID 指定（可与 entry 相同，覆盖 Flush 自重入路径）。
type sharedCleanupFixture struct {
	st         *store.Store
	d          *Dispatcher
	req        *fakeRequester
	chainID    int64
	revisionID int64
	endpointID int64
	entryID    int64
	cleanupID  int64
}

func newSharedCleanupFixture(t *testing.T, cleanupOnEntry bool) *sharedCleanupFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.example.com",
		BootstrapToken: "entry-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "old", Address: "old.example.com",
		BootstrapToken: "old-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if cleanupOnEntry {
		cleanupID = entryID
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "service", entryID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 2443, "profile-1", config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	hopID, err := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleExit, nodeID, 2443, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainHopStatus(ctx, hopID, store.HopStatusActive, ""); err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "shared", ServiceNodeID: nodeID, ServiceServerID: entryID, ServiceConfig: config,
		EndpointID: endpoint.ID, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{{HopID: hopID, ServerID: entryID, Role: store.HopRoleExit, ForwardPort: 2443}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID,
		TaskKey: cleanupKeyForTest, Phase: "cleanup", Action: "remove",
		Kind: store.RevisionPieceForward, HopID: 9, ServerID: cleanupID}); err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{entryID: true, cleanupID: true}}
	return &sharedCleanupFixture{
		st: st, d: New(st, req, Options{}, Events{}), req: req, chainID: chainID,
		revisionID: revision.ID, endpointID: endpoint.ID, entryID: entryID, cleanupID: cleanupID,
	}
}

const cleanupKeyForTest = "cleanup/forward/9"

func (f *sharedCleanupFixture) chain(t *testing.T) *store.Chain {
	t.Helper()
	chain, err := f.st.ChainByID(context.Background(), f.chainID)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

// TestCleanupPendingDegradesWhenSharedEndpointNotActive 覆盖共享入口未生效时的可见性：
// 清理阻塞期间端点未 active，链转 degraded 并在端点就绪后恢复 active。
func TestCleanupPendingDegradesWhenSharedEndpointNotActive(t *testing.T) {
	ctx := context.Background()
	f := newSharedCleanupFixture(t, false)
	revision, err := f.st.ChainRevisionByID(ctx, f.revisionID)
	if err != nil {
		t.Fatal(err)
	}
	f.d.enqueueCleanupTasks(ctx, revision)
	chain := f.chain(t)
	if chain.Status != store.ChainStatusDegraded {
		t.Fatalf("chain status = %s, want degraded while shared endpoint not active (error=%q)",
			chain.Status, chain.Error)
	}
	if !strings.Contains(chain.Error, "共享入口") {
		t.Fatalf("chain error = %q, want shared endpoint reason", chain.Error)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 2443, PublicKey: "pub"})
	if err := f.st.SetSharedEndpointActive(ctx, f.endpointID, realized); err != nil {
		t.Fatal(err)
	}
	f.d.fsm.Evaluate(ctx, f.chainID)
	if chain := f.chain(t); chain.Status != store.ChainStatusActive {
		t.Fatalf("chain status after endpoint active = %s, want active", chain.Status)
	}
}

// TestCleanupPendingIgnoresApplyingEndpoint 覆盖发布窗口：cleanup_pending 期间端点处于
// applying（重部署中）不据此降级，避免每发布/每 60s reconcile 的 cleanup_pending↔degraded
// 状态与日志抖动；端点失败（failed）仍应降级。
func TestCleanupPendingIgnoresApplyingEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newSharedCleanupFixture(t, false)
	if err := f.st.SetSharedEndpointActive(ctx, f.endpointID, json.RawMessage(`{"port":2443}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetSharedEndpointApplying(ctx, f.endpointID); err != nil {
		t.Fatal(err)
	}
	revision, err := f.st.ChainRevisionByID(ctx, f.revisionID)
	if err != nil {
		t.Fatal(err)
	}
	f.d.enqueueCleanupTasks(ctx, revision)
	if chain := f.chain(t); chain.Status != store.ChainStatusCleanupPending {
		t.Fatalf("chain status = %s, want cleanup_pending while endpoint applying (error=%q)",
			chain.Status, chain.Error)
	}
	id, err := f.d.Enqueue(ctx, f.entryID, shared.TypeApplySharedEndpoint,
		shared.ApplySharedEndpointPayload{EndpointID: f.endpointID})
	if err != nil {
		t.Fatal(err)
	}
	command, err := f.st.CommandByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.d.handleCommandResponse(f.entryID, shared.Envelope{
		Kind: shared.KindResponse, Type: command.Type, RequestID: command.RequestID, TraceID: command.TraceID,
		Code: shared.CodeInternalError, Message: "deploy failed",
		Data: json.RawMessage(fmt.Sprintf(`{"endpoint_id":%d}`, f.endpointID)),
	})
	if chain := f.chain(t); chain.Status != store.ChainStatusDegraded {
		t.Fatalf("chain status after endpoint failed = %s, want degraded", chain.Status)
	}
}

// TestCleanupDeadLetterNoSelfDeadlock 覆盖评审阻断项：清理命令达到投递上限 → 死信收尾
// 触发链评估 → 端点自动重试 → Enqueue → Flush 同一服务器。Flush 若在持 serverLock 时
// 执行死信副作用会自重入死锁；此处断言清理自愈能在超时内返回。
func TestCleanupDeadLetterNoSelfDeadlock(t *testing.T) {
	ctx := context.Background()
	previous := cleanupCommandSentTimeout
	cleanupCommandSentTimeout = -time.Second
	defer func() { cleanupCommandSentTimeout = previous }()

	f := newSharedCleanupFixture(t, true) // 清理命令与端点同一服务器（entry）
	const taskKey = "cleanup/forward/10"
	if _, err := f.st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: f.revisionID,
		TaskKey: taskKey, Phase: "cleanup", Action: "remove",
		Kind: store.RevisionPieceForward, HopID: 10, ServerID: f.entryID}); err != nil {
		t.Fatal(err)
	}
	commandID, err := f.d.enqueueRevisionTask(ctx, f.entryID, shared.TypeRemoveChainHop,
		shared.RemoveChainHopPayload{HopID: 10, Kind: "forward"}, f.revisionID, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxCommandAttempts; i++ {
		if err := f.st.MarkCommandSent(ctx, commandID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.SetSharedEndpointFailed(ctx, f.endpointID, "deploy failed"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		f.d.ReconcileChainCleanups(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: ReconcileChainCleanups did not return within 5s")
	}
	command, err := f.st.CommandByID(ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusFailed {
		t.Fatalf("command status = %s, want failed (dead-lettered)", command.Status)
	}
	tasks, err := f.st.RevisionTasks(ctx, f.revisionID)
	if err != nil {
		t.Fatal(err)
	}
	foundFailed := false
	for _, task := range tasks {
		if task.TaskKey == taskKey && task.Status == store.RevisionTaskFailed {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("cleanup tasks = %+v, want %s failed", tasks, taskKey)
	}
	if chain := f.chain(t); chain.Status != store.ChainStatusDegraded {
		t.Fatalf("chain status = %s, want degraded after endpoint failed", chain.Status)
	}
}

type cleanupObservingRequester struct {
	*fakeRequester
	beforeSend func()
}

func (r cleanupObservingRequester) Send(ctx context.Context, serverID int64, env shared.Envelope) error {
	r.beforeSend()
	return r.fakeRequester.Send(ctx, serverID, env)
}

func TestFlushCompletesDeadLetterBeforeNextSend(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, true)
	f.d.enqueueCleanupTasks(ctx, f.revision)
	original := f.cleanupTask(t)
	for i := 1; i < maxCommandAttempts; i++ {
		if err := f.st.MarkCommandSent(ctx, original.CommandID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.ResetSentCommands(ctx, f.oldID); err != nil {
		t.Fatal(err)
	}
	nextID, err := f.st.EnqueueCommand(ctx, "next", "trace", f.oldID, shared.TypeRemoveChainHop, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	var statusAtSend string
	f.d.req = cleanupObservingRequester{fakeRequester: f.req, beforeSend: func() {
		statusAtSend = f.cleanupTask(t).Status
	}}
	f.d.Flush(ctx, f.oldID)
	next, err := f.st.CommandByID(ctx, nextID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != store.CommandStatusSent || statusAtSend != store.RevisionTaskFailed {
		t.Fatalf("next command=%s, old cleanup task when sent=%s; want sent after old task failed", next.Status, statusAtSend)
	}
}

func TestFlushDeliversConcurrentEnqueueAfterRequestEnds(t *testing.T) {
	ctx := context.Background()
	f := newCleanupFixture(t, true, false)
	if _, err := f.st.EnqueueCommand(ctx, "first", "trace", f.oldID, shared.TypeRemoveChainHop, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	f.d.req = cleanupObservingRequester{fakeRequester: f.req, beforeSend: sync.OnceFunc(func() {
		close(started)
		<-release
	})}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		f.d.Flush(ctx, f.oldID)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("initial send did not start")
	}

	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	id, err := f.d.Enqueue(requestCtx, f.oldID, shared.TypeRemoveChainHop, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	unblock()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not finish")
	}
	command, err := f.st.CommandByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusSent {
		t.Fatalf("concurrent enqueue status=%s, want sent after request ends", command.Status)
	}
}
