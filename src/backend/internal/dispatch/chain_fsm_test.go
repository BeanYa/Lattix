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
	serverID, _ := st.CreateServer(ctx, "direct", "direct.test", "token", store.MachineTypeDirect, "", "", "US", "Test")
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
}
