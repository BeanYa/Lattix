package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestEditChainOfflineAgentThenReconnect 覆盖 §21.1 离线编辑全路径：
// Agent 离线时编辑链路 → 链与 revision 进入 waiting_for_agent；
// Agent 重连（Dispatcher.OnAgentConnect）→ 恢复编排并下发待应用 piece。
func TestEditChainOfflineAgentThenReconnect(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, _, chainID, _, _, nodeRequest, _ := chainEditFixture(t, ctx, st)

	requester := &chainEditRequester{online: map[int64]bool{aID: false, cID: true}}
	dispatcher := dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{})
	serverAPI := &Server{st: st, disp: dispatcher, req: requester}

	entryPort := 2000
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: &entryPort,
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("edit response = %d %s", recorder.Code, recorder.Body.String())
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusWaitingForAgent {
		t.Fatalf("离线编辑后链状态 = %s，期望 waiting_for_agent", chain.Status)
	}
	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Status != store.RevisionStatusWaitingForAgent {
		t.Fatalf("离线编辑后 revision 状态 = %s，期望 waiting_for_agent", desired.Status)
	}

	// Agent 重连：panel hub.OnConnect → Dispatcher.OnAgentConnect。
	requester.online[aID] = true
	dispatcher.OnAgentConnect(ctx, aID)

	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("Agent 重连后链状态 = %s，期望 applying（恢复编排）", chain.Status)
	}
	commands, err := st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) == 0 {
		t.Fatalf("Agent 重连后应恢复编排（入口 forward piece 入队），实际无命令")
	}
}

// TestEditChainSupersedesWaitingForAgentEdit 覆盖等待中链的再编辑：Agent 仍离线时
// 提交第二次编辑应取代等待中的 revision（而非 409 拒绝），链保持 waiting_for_agent；
// Agent 上线后按新 revision 恢复编排。
func TestEditChainSupersedesWaitingForAgentEdit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, _, chainID, _, _, nodeRequest, _ := chainEditFixture(t, ctx, st)

	requester := &chainEditRequester{online: map[int64]bool{aID: false, cID: true}}
	dispatcher := dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{})
	serverAPI := &Server{st: st, disp: dispatcher, req: requester}

	edit := func(entryPort int) {
		t.Helper()
		body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
			Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: &entryPort,
			Node: nodeRequest, TrafficMultiplier: "1.000"})
		req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		serverAPI.handleEditChain(recorder, req)
		// 面板错误响应也是 HTTP 200（RPC envelope），须按 envelope code 判定成败。
		var envelope struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil ||
			(envelope.Code != "OK" && envelope.Code != "ACCEPTED") {
			t.Fatalf("edit(%d) response = %d %s", entryPort, recorder.Code, recorder.Body.String())
		}
	}

	edit(2000)
	superseded, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Status != store.RevisionStatusWaitingForAgent {
		t.Fatalf("第一次编辑后 revision 状态 = %s，期望 waiting_for_agent", superseded.Status)
	}

	// Agent 仍离线：第二次编辑应取代等待中的 revision。
	edit(3000)
	reloaded, err := st.ChainRevisionByID(ctx, superseded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != store.RevisionStatusCancelled {
		t.Fatalf("被取代的等待中 revision 状态 = %s，期望 cancelled", reloaded.Status)
	}
	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.ID == superseded.ID || desired.Snapshot.Hops[0].ForwardPort != 3000 {
		t.Fatalf("新 desired revision = %+v，期望入口端口 3000", desired.Snapshot.Hops)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusWaitingForAgent {
		t.Fatalf("再编辑后链状态 = %s，期望保持 waiting_for_agent", chain.Status)
	}

	// Agent 上线：按新 revision 恢复编排。
	requester.online[aID] = true
	dispatcher.OnAgentConnect(ctx, aID)
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("Agent 重连后链状态 = %s，期望 applying", chain.Status)
	}
	commands, err := st.CommandsByType(ctx, shared.TypeApplyChainHop)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 {
		t.Fatalf("恢复后 apply_chain_hop 命令数 = %d，期望 1（仅新 revision 的 forward）", len(commands))
	}
	var payload shared.ApplyChainHopPayload
	if err := json.Unmarshal(commands[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Forward == nil || payload.Forward.Port != 3000 {
		t.Fatalf("恢复下发的 forward spec = %+v，期望端口 3000", payload.Forward)
	}
}
