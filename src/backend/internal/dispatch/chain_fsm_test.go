package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestChainTransitionTableCoversRealFlows 校验转换表覆盖评审补齐的实际边：
// 编辑、强制发布、等待恢复、强制发布后失败定位。
func TestChainTransitionTableCoversRealFlows(t *testing.T) {
	legal := []struct{ from, to string }{
		{store.ChainStatusPending, store.ChainStatusApplying},
		{store.ChainStatusApplying, store.ChainStatusWaitingForAgent},          // 编辑所需 Agent 离线
		{store.ChainStatusApplying, store.ChainStatusActiveUnconfirmed},        // 强制发布
		{store.ChainStatusActive, store.ChainStatusApplying},                   // 编辑
		{store.ChainStatusDegraded, store.ChainStatusApplying},                 // 编辑
		{store.ChainStatusCleanupPending, store.ChainStatusApplying},           // 编辑
		{store.ChainStatusInvalid, store.ChainStatusApplying},                  // 替换缺失 hop 重新发布
		{store.ChainStatusApplying, store.ChainStatusActiveFailed},             // 已发布链编辑失败
		{store.ChainStatusWaitingForAgent, store.ChainStatusActiveFailed},      // 等待期间在途命令失败
		{store.ChainStatusActiveUnconfirmed, store.ChainStatusActiveFailed},    // 强制发布后任务失败
		{store.ChainStatusWaitingForAgent, store.ChainStatusActiveUnconfirmed}, // 强制发布
		{store.ChainStatusWaitingForAgent, store.ChainStatusApplying},          // Agent 上线恢复
		{store.ChainStatusFailed, store.ChainStatusActiveUnconfirmed},          // 失败编辑强制发布
		{store.ChainStatusActiveFailed, store.ChainStatusActiveUnconfirmed},    // 强制发布后失败再发布
	}
	for _, c := range legal {
		if !validChainTransition(c.from, c.to) {
			t.Errorf("缺少转换边 %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to string }{
		{store.ChainStatusActive, store.ChainStatusFailed},
		{store.ChainStatusActive, store.ChainStatusWaitingForAgent},
		{store.ChainStatusActiveUnconfirmed, store.ChainStatusDegraded},
		{store.ChainStatusDeleted, store.ChainStatusApplying},
		{store.ChainStatusInvalid, store.ChainStatusActive},
	}
	for _, c := range illegal {
		if validChainTransition(c.from, c.to) {
			t.Errorf("非法转换未被拒绝 %s → %s", c.from, c.to)
		}
	}
}

func newFSMFixture(t *testing.T) (*store.Store, *Dispatcher, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "direct", Address: "direct.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, _ := st.InsertNode(ctx, "direct", serverID, shared.ProtocolVLESS, nil, config)
	chainID, _ := st.InsertChain(ctx, "direct")
	_, _ = st.InsertChainHop(ctx, chainID, 0, serverID, store.HopRoleExit, nodeID, 0, "")
	return st, New(st, &fakeRequester{online: map[int64]bool{serverID: false}}), chainID, serverID
}

// TestFSMTransitionRejectsIllegalEdge 校验 Transition 拒绝非法转换且不落库。
func TestFSMTransitionRejectsIllegalEdge(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
		t.Fatal(err)
	}
	err := d.fsm.Transition(ctx, chainID, store.ChainStatusWaitingForAgent, "x")
	if err == nil || !strings.Contains(err.Error(), "illegal transition") {
		t.Fatalf("Transition error = %v, want illegal transition", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("非法转换后链状态 = %s，期望保持 active", chain.Status)
	}
}

// TestSetChainStatusCASRejectsStaleFrom 校验 CAS 守卫：以过期 from 写入被拒绝。
func TestSetChainStatusCASRejectsStaleFrom(t *testing.T) {
	ctx := context.Background()
	st, _, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	err := st.SetChainStatus(ctx, chainID, store.ChainStatusActive, "x", store.ChainStatusPending)
	if !errors.Is(err, store.ErrChainStatusChanged) {
		t.Fatalf("CAS error = %v, want ErrChainStatusChanged", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("CAS 失败后链状态 = %s，期望保持 applying", chain.Status)
	}
}

// TestActiveUnconfirmedToActiveFailed 修复评审 P0-1：强制发布后残余任务失败
// 必须允许 active_unconfirmed → active_failed，否则链卡死且重试/编辑均被拒绝。
func TestActiveUnconfirmedToActiveFailed(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActiveUnconfirmed, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.fsm.Transition(ctx, chainID, store.ChainStatusActiveFailed, "跳 1 失败"); err != nil {
		t.Fatalf("active_unconfirmed → active_failed 被拒绝: %v", err)
	}
	// 失败后重试路径恢复：active_failed → applying
	if err := d.fsm.Transition(ctx, chainID, store.ChainStatusApplying, "重试"); err != nil {
		t.Fatalf("active_failed → applying 被拒绝: %v", err)
	}
}

// TestPublishedChainEditFailureReachesActiveFailed 修复评审 P1：已发布链编辑
// （status=applying，published_revision_id 仍在）期间跳失败必须允许
// applying → active_failed，否则链卡在 applying（重试/编辑均被拒绝）。
func TestPublishedChainEditFailureReachesActiveFailed(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	// 发布 revision：链 active 且 published_revision_id 置位。
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false, store.ChainStatusActive); err != nil {
		t.Fatal(err)
	}
	// 编辑已发布链：desired revision 就位，链进入 applying（published_revision_id 保留，
	// 与 ReplaceWorkingChainTopology 一致）。
	revision2, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	hops, err := st.ChainHops(ctx, chainID)
	if err != nil || len(hops) == 0 {
		t.Fatalf("chain hops: %d, %v", len(hops), err)
	}
	if err := st.SetChainHopStatus(ctx, hops[0].ID, store.HopStatusFailed, "跳部署失败"); err != nil {
		t.Fatal(err)
	}
	// 跳失败 → failChain 按 published_revision_id 选择 active_failed，不得被转换表拒绝。
	d.failChain(ctx, chainID, &hops[0], "跳部署失败")
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActiveFailed {
		t.Fatalf("失败后链状态 = %s，期望 active_failed（已发布链编辑失败不得卡 applying）", chain.Status)
	}
	if chain.DesiredRevisionID != revision2.ID {
		t.Fatalf("desired_revision_id = %d，期望 %d", chain.DesiredRevisionID, revision2.ID)
	}
	// 恢复路径：RetryChain 接受 active_failed → applying 继续编排，失败跳复位 pending。
	if err := d.RetryChain(ctx, chainID); err != nil {
		t.Fatalf("active_failed 链重试被拒绝: %v", err)
	}
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("重试后链状态 = %s，期望 applying", chain.Status)
	}
	hops, _ = st.ChainHops(ctx, chainID)
	if len(hops) == 0 || hops[0].Status != store.HopStatusPending {
		t.Fatalf("重试后跳状态 = %v，期望 pending", hops)
	}
}

// TestWaitForAgentAndResume 覆盖 §21.1 离线语义：编辑所需 Agent 离线 →
// applying → waiting_for_agent；Agent 上线（ResumeChainsByServer）→ applying 并推进编排。
func TestWaitForAgentAndResume(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, serverID := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.fsm.WaitForAgent(ctx, chainID, "编辑所需 Agent 离线"); err != nil {
		t.Fatalf("WaitForAgent: %v", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusWaitingForAgent {
		t.Fatalf("链状态 = %s，期望 waiting_for_agent", chain.Status)
	}
	// 等待期间不推进编排（无命令入队）
	if cmds, err := st.CommandsByType(ctx, shared.TypeApplyNode); err != nil || len(cmds) != 0 {
		t.Fatalf("waiting_for_agent 期间不应下发命令: %d, %v", len(cmds), err)
	}
	// Agent 上线：ResumeChainsByServer → applying 并推进编排
	d.fsm.ResumeChainsByServer(ctx, serverID)
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("Agent 上线后链状态 = %s，期望 applying", chain.Status)
	}
	cmds, err := st.CommandsByType(ctx, shared.TypeApplyNode)
	if err != nil || len(cmds) != 1 || cmds[0].Status != store.CommandStatusQueued {
		t.Fatalf("恢复后应入队 1 条 queued apply_node: %d, %v", len(cmds), err)
	}
}

// TestMaybePublishReadyRevisionRepublishesRacedRevision 修复评审 P2-8：发布窗口
// 竞态遗留（链 active、desired revision 已置 active 却未发布）时，恢复路径补发发布。
func TestMaybePublishReadyRevisionRepublishesRacedRevision(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
		t.Fatal(err)
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false, store.ChainStatusActive); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.ListNodes(ctx)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %d, %v", len(nodes), err)
	}
	revision2, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "c2", ServiceNodeID: nodes[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟竞态遗留：revision 已置 active，但 published_revision_id 未推进（发布 CAS 被中断）。
	if err := st.UpdateChainRevision(ctx, revision2.ID, store.RevisionStatusActive, "", revision2.Snapshot); err != nil {
		t.Fatal(err)
	}
	d.maybePublishReadyRevision(ctx, chainID)
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.PublishedRevisionID != revision2.ID {
		t.Fatalf("published_revision_id = %d, want %d（竞态遗留未补发发布）", chain.PublishedRevisionID, revision2.ID)
	}
	if chain.DesiredRevisionID != 0 {
		t.Fatalf("补发后 desired_revision_id = %d, want 0", chain.DesiredRevisionID)
	}
}

// TestForcePublishRejectsActiveChain 已上线链不允许强制发布（先编辑）。
func TestForcePublishRejectsActiveChain(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c"}); err != nil {
		t.Fatal(err)
	}
	err := d.ForcePublishRevision(ctx, chainID)
	if err == nil || !strings.Contains(err.Error(), "不允许强制发布") {
		t.Fatalf("ForcePublishRevision error = %v, want 不允许强制发布", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("拒绝后链状态 = %s，期望保持 active", chain.Status)
	}
}

// TestEditChainTopologyValidatesAndCAS 校验编辑入口：FSM 校验 + CAS 并发保护。
func TestEditChainTopologyValidatesAndCAS(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	// pending → applying 合法
	if err := d.EditChainTopology(ctx, revision, shared.ProtocolVLESS, nil, false); err != nil {
		t.Fatalf("EditChainTopology: %v", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("编辑后链状态 = %s，期望 applying", chain.Status)
	}
	// deleted → applying 非法
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusDeleted, ""); err != nil {
		t.Fatal(err)
	}
	revision2, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	err = d.EditChainTopology(ctx, revision2, shared.ProtocolVLESS, nil, false)
	if err == nil || !strings.Contains(err.Error(), "illegal transition") {
		t.Fatalf("EditChainTopology error = %v, want illegal transition", err)
	}
	// 并发保护：以过期 from 调 store CAS 被拒绝
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	err = st.ReplaceWorkingChainTopology(ctx, revision2, shared.ProtocolVLESS, nil, false, store.ChainStatusFailed)
	if !errors.Is(err, store.ErrChainStatusChanged) {
		t.Fatalf("ReplaceWorkingChainTopology CAS error = %v, want ErrChainStatusChanged", err)
	}
}

// TestDeleteChainFSMWrapperIdempotent 删除入口经 FSM，重复删除幂等。
func TestDeleteChainFSMWrapperIdempotent(t *testing.T) {
	ctx := context.Background()
	st, d, chainID, _ := newFSMFixture(t)
	defer st.Close()
	if err := d.DeleteChain(ctx, chainID); err != nil {
		t.Fatalf("DeleteChain: %v", err)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusDeleted {
		t.Fatalf("删除后链状态 = %s", chain.Status)
	}
	if err := d.DeleteChain(ctx, chainID); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
}

// TestEntityStateWriteGuards 覆盖节点/端点/跳/任务写入的前置状态守卫。
func TestEntityStateWriteGuards(t *testing.T) {
	ctx := context.Background()
	st, _, chainID, serverID := newFSMFixture(t)
	defer st.Close()
	hopID, _ := st.InsertChainHop(ctx, chainID, 0, serverID, store.HopRoleEntry, 0, 0, "")

	// 跳：pending → active 合法；active → pending 拒绝
	if err := st.SetChainHopStatus(ctx, hopID, store.HopStatusActive, ""); err != nil {
		t.Fatalf("hop pending→active: %v", err)
	}
	if err := st.SetChainHopStatus(ctx, hopID, store.HopStatusPending, ""); !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("hop active→pending error = %v, want ErrStateTransition", err)
	}

	// 节点：pending → active 合法；active → active 重复写入拒绝（回执唯一性守卫）
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS})
	nodeID, _ := st.InsertNode(ctx, "n", serverID, shared.ProtocolVLESS, nil, config)
	realized := json.RawMessage(`{"port":443}`)
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatalf("node pending→active: %v", err)
	}
	if err := st.SetNodeActive(ctx, nodeID, realized); !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("node active→active error = %v, want ErrStateTransition", err)
	}
	// repair：active → applying 合法
	if err := st.SetNodeApplying(ctx, nodeID); err != nil {
		t.Fatalf("node active→applying (repair): %v", err)
	}

	// 端点：pending → active 合法；active → pending 合法（复用重置）；pending → failed 合法
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 8443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, json.RawMessage(`{"port":8443}`)); err != nil {
		t.Fatalf("endpoint pending→active: %v", err)
	}
	if err := st.SetSharedEndpointPending(ctx, endpoint.ID); err != nil {
		t.Fatalf("endpoint active→pending: %v", err)
	}
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatalf("endpoint pending→failed: %v", err)
	}

	// 任务：pending → acked 合法；acked → failed 拒绝（终态不可翻写）
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: revision.ID,
		TaskKey: "forward/1", Phase: "apply", Action: "apply", Kind: RevisionPieceForward,
		HopID: hopID, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRevisionTaskResult(ctx, taskID, true, ""); err != nil {
		t.Fatalf("task pending→acked: %v", err)
	}
	if err := st.SetRevisionTaskResult(ctx, taskID, false, "late"); !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("task acked→failed error = %v, want ErrStateTransition", err)
	}

	// revision（评审 P2-7）：applying → failed 合法；failed → active 拒绝（迟到失败
	// 不得覆盖已发布）；failed → applying（重试复位）与 applying → waiting_for_agent
	// 合法；waiting_for_agent → active 拒绝（需先复位 applying）。
	if err := st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusFailed, "boom"); err != nil {
		t.Fatalf("revision applying→failed: %v", err)
	}
	if err := st.UpdateChainRevision(ctx, revision.ID, store.RevisionStatusActive, "", store.ChainRevisionSnapshot{Name: "c"}); !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("revision failed→active error = %v, want ErrStateTransition", err)
	}
	if err := st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusApplying, ""); err != nil {
		t.Fatalf("revision failed→applying (retry): %v", err)
	}
	if err := st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusWaitingForAgent, ""); err != nil {
		t.Fatalf("revision applying→waiting_for_agent: %v", err)
	}
	if err := st.SetChainRevisionStatus(ctx, revision.ID, store.RevisionStatusActive, ""); !errors.Is(err, store.ErrStateTransition) {
		t.Fatalf("revision waiting_for_agent→active error = %v, want ErrStateTransition", err)
	}
}
