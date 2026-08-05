package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type chainEditRequester struct {
	online map[int64]bool
}

func (f *chainEditRequester) Send(context.Context, int64, shared.Envelope) error { return nil }
func (f *chainEditRequester) IsOnline(serverID int64) bool                       { return f.online[serverID] }

func TestEditChainRemovesMiddleAndPlansOnlyAffectedPieces(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := func(alias, token string) int64 {
		id, err := st.CreateServer(ctx, alias, alias+".example.com", token, store.MachineTypeDirect, "", "", "US", "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, bID, cID := server("a", "token-a"), server("b", "token-b"), server("c", "token-c")
	port := 3000
	nodeRequest := createNodeRequest{Name: "chain", ServerID: cID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeRequest.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(nodeRequest))
	nodeID, _ := st.InsertNode(ctx, "chain", cID, shared.ProtocolVLESS, &port, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: port, PublicKey: "public-key",
		ShortID: nodeRequest.ShortID, ServerName: "dl.google.com", Network: shared.NetworkTCP,
		Flow: shared.FlowVision, Fingerprint: shared.FingerprintChrome})
	_ = st.SetNodeActive(ctx, nodeID, realized)
	chainID, _ := st.InsertChain(ctx, "chain")
	aHop, _ := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 1000, "")
	bHop, _ := st.InsertChainHop(ctx, chainID, 1, bID, store.HopRoleMiddle, 0, 2000, "")
	cHop, _ := st.InsertChainHop(ctx, chainID, 2, cID, store.HopRoleExit, nodeID, port, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	for _, hopID := range []int64{aHop, bHop, cHop} {
		_ = st.SetChainHopStatus(ctx, hopID, store.HopStatusActive, "")
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
			{HopID: bHop, ServerID: bID, Role: store.HopRoleMiddle, Transport: "direct", ForwardPort: 2000},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: port},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}

	requester := &chainEditRequester{online: map[int64]bool{aID: true, bID: true, cID: true}}
	dispatcher := dispatch.New(st, requester)
	serverAPI := &Server{st: st, disp: dispatcher, req: requester}
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "{{ENTRY.COUNTRY_FLAG}}-{{EXIT.COUNTRY_CODE}}",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: func() *int { value := 1000; return &value }(),
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("edit response = %d %s", recorder.Code, recorder.Body.String())
	}

	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Snapshot.Name != "🇺🇸-US" {
		t.Fatalf("revision name = %q, want %q", desired.Snapshot.Name, "🇺🇸-US")
	}
	tasks, _ := st.RevisionTasks(ctx, desired.ID)
	byKey := map[string]store.ChainRevisionTask{}
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if len(tasks) != 2 {
		t.Fatalf("revision tasks = %+v", tasks)
	}
	if task, ok := byKey["forward/"+formatID(aHop)]; !ok || task.Phase != "apply" || task.ServerID != aID {
		t.Fatalf("entry apply task = %+v present=%v", task, ok)
	}
	if task, ok := byKey["cleanup/forward/"+formatID(bHop)]; !ok || task.Phase != "cleanup" || task.ServerID != bID {
		t.Fatalf("middle cleanup task = %+v present=%v", task, ok)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) != 2 || hops[0].ID != aHop || hops[1].ID != cHop {
		t.Fatalf("desired hops = %+v", hops)
	}
}

// chainEditFixture 构建一条已发布的双跳直连链（a 入口 → c 出口）并返回基础设施。
func chainEditFixture(t *testing.T, ctx context.Context, st *store.Store) (
	aID, cID, nodeID, chainID, aHop, cHop int64, nodeRequest createNodeRequest, config []byte) {
	t.Helper()
	server := func(alias, token string) int64 {
		id, err := st.CreateServer(ctx, alias, alias+".example.com", token, store.MachineTypeDirect, "", "", "US", "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, cID = server("a", "token-a"), server("c", "token-c")
	port := 3000
	nodeRequest = createNodeRequest{Name: "chain", ServerID: cID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeRequest.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ = json.Marshal(buildVirtualConfig(nodeRequest))
	nodeID, _ = st.InsertNode(ctx, "chain", cID, shared.ProtocolVLESS, &port, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: port, PublicKey: "public-key",
		ShortID: nodeRequest.ShortID, ServerName: "dl.google.com", Network: shared.NetworkTCP,
		Flow: shared.FlowVision, Fingerprint: shared.FingerprintChrome})
	_ = st.SetNodeActive(ctx, nodeID, realized)
	chainID, _ = st.InsertChain(ctx, "chain")
	aHop, _ = st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 1000, "")
	cHop, _ = st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, nodeID, port, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	for _, hopID := range []int64{aHop, cHop} {
		_ = st.SetChainHopStatus(ctx, hopID, store.HopStatusActive, "")
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: port},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	return aID, cID, nodeID, chainID, aHop, cHop, nodeRequest, config
}

// failChainAfterEdit 模拟一次失败的编辑 revision：创建 rev2（入口 forward 端口改
// 为 entryPort），service 任务 acked、forward 任务 failed，链与 revision 置 failed。
func failChainAfterEdit(t *testing.T, ctx context.Context, st *store.Store, chainID, nodeID, aID, cID, aHop int64, entryPort int, config []byte) *store.ChainRevision {
	t.Helper()
	hops, _ := st.ChainHops(ctx, chainID)
	var cHop int64
	for _, h := range hops {
		if h.Role == store.HopRoleExit {
			cHop = h.ID
		}
	}
	desired, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: entryPort},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceTask, _ := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: desired.ID,
		TaskKey: fmt.Sprintf("service/%d", nodeID), Phase: "apply", Action: "apply",
		Kind: store.RevisionPieceService, HopID: nodeID, ServerID: cID})
	_ = st.SetRevisionTaskResult(ctx, serviceTask, true, "")
	forwardTask, _ := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: desired.ID,
		TaskKey: fmt.Sprintf("forward/%d", aHop), Phase: "apply", Action: "apply",
		Kind: store.RevisionPieceForward, HopID: aHop, ServerID: aID})
	_ = st.SetRevisionTaskResult(ctx, forwardTask, false, "agent boom")
	if err := st.SetChainRevisionStatus(ctx, desired.ID, store.RevisionStatusFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusActiveFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainHopStatus(ctx, aHop, store.HopStatusFailed, "agent boom"); err != nil {
		t.Fatal(err)
	}
	return desired
}

// TestEditChainAfterFailedDeploymentReappliesUnackedPiecesOnly 覆盖 active_failed 链编辑：
// 允许编辑（不再 409）；以失败 revision 为基线规划，仅 acked piece 复用（service），
// 未落地的 piece（forward）重发；被取代的失败 revision 置 cancelled。
func TestEditChainAfterFailedDeploymentReappliesUnackedPiecesOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, nodeID, chainID, aHop, _, nodeRequest, config := chainEditFixture(t, ctx, st)
	failed := failChainAfterEdit(t, ctx, st, chainID, nodeID, aID, cID, aHop, 2000, config)

	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}
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

	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.ID == failed.ID {
		t.Fatalf("desired revision unchanged: %d", desired.ID)
	}
	superseded, _ := st.ChainRevisionByID(ctx, failed.ID)
	if superseded.Status != store.RevisionStatusCancelled {
		t.Fatalf("superseded revision status = %s, want cancelled", superseded.Status)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("chain status = %s, want applying", chain.Status)
	}
	tasks, _ := st.RevisionTasks(ctx, desired.ID)
	byKey := map[string]store.ChainRevisionTask{}
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if _, ok := byKey[fmt.Sprintf("forward/%d", aHop)]; !ok {
		t.Fatalf("failed forward piece must be re-applied, tasks = %+v", tasks)
	}
	if _, ok := byKey[fmt.Sprintf("service/%d", nodeID)]; ok {
		t.Fatalf("acked service piece must be reused, tasks = %+v", tasks)
	}
	if desired.Snapshot.Hops[0].ForwardPort != 2000 {
		t.Fatalf("desired entry forward port = %d, want 2000", desired.Snapshot.Hops[0].ForwardPort)
	}
}

// TestEditChainAfterInitialDeploymentFailure 覆盖从未发布（初始部署即失败）的链编辑：
// 无 published revision 时以失败 desired revision 为基线；未落地 piece 全部重发，
// 链回到 applying 重新编排。
func TestEditChainAfterInitialDeploymentFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := func(alias, token string) int64 {
		id, err := st.CreateServer(ctx, alias, alias+".example.com", token, store.MachineTypeDirect, "", "", "US", "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, cID := server("a", "token-a"), server("c", "token-c")
	port := 3000
	nodeRequest := createNodeRequest{Name: "chain", ServerID: cID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeRequest.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(nodeRequest))
	nodeID, _ := st.InsertNode(ctx, "chain", cID, shared.ProtocolVLESS, &port, config)
	chainID, _ := st.InsertChain(ctx, "chain")
	aHop, _ := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 1000, "")
	cHop, _ := st.InsertChainHop(ctx, chainID, 1, cID, store.HopRoleExit, nodeID, 0, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	initial, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: initial.ID,
		TaskKey: fmt.Sprintf("service/%d", nodeID), Phase: "apply", Action: "apply",
		Kind: store.RevisionPieceService, HopID: nodeID, ServerID: cID})
	forwardTask, _ := st.AddRevisionTask(ctx, store.ChainRevisionTask{RevisionID: initial.ID,
		TaskKey: fmt.Sprintf("forward/%d", aHop), Phase: "apply", Action: "apply",
		Kind: store.RevisionPieceForward, HopID: aHop, ServerID: aID})
	_ = st.SetRevisionTaskResult(ctx, forwardTask, false, "agent boom")
	if err := st.SetChainRevisionStatus(ctx, initial.ID, store.RevisionStatusFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusFailed, "跳失败"); err != nil {
		t.Fatal(err)
	}

	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("edit response = %d %s", recorder.Code, recorder.Body.String())
	}

	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if desired.ID == initial.ID {
		t.Fatalf("desired revision unchanged: %d", desired.ID)
	}
	superseded, _ := st.ChainRevisionByID(ctx, initial.ID)
	if superseded.Status != store.RevisionStatusCancelled {
		t.Fatalf("superseded revision status = %s, want cancelled", superseded.Status)
	}
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusApplying {
		t.Fatalf("chain status = %s, want applying", chain.Status)
	}
	tasks, _ := st.RevisionTasks(ctx, desired.ID)
	byKey := map[string]store.ChainRevisionTask{}
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if _, ok := byKey[fmt.Sprintf("service/%d", nodeID)]; !ok {
		t.Fatalf("initial service piece must be re-applied, tasks = %+v", tasks)
	}
	if _, ok := byKey[fmt.Sprintf("forward/%d", aHop)]; !ok {
		t.Fatalf("initial forward piece must be re-applied, tasks = %+v", tasks)
	}
}

// TestEditChainStillBlockedWhileApplying 覆盖编排中的链编辑仍被拒绝（409）。
func TestEditChainStillBlockedWhileApplying(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aID, cID, nodeID, chainID, aHop, cHop, nodeRequest, config := chainEditFixture(t, ctx, st)
	if _, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID, ServiceConfig: config,
		TrafficMultiplierMilli: 1000, Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: 0},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainStatus(ctx, chainID, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{aID: true, cID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}},
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "链路已有部署中的编辑") {
		t.Fatalf("edit response = %d %s, want CONFLICT", recorder.Code, recorder.Body.String())
	}
}

func formatID(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
