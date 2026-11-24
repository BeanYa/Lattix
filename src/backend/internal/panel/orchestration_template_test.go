package panel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestCreateChainWithOrchestration(t *testing.T) {
	srv, st, _, ids := setupOrchestrationHandler(t)
	var graph map[string]any
	if err := json.Unmarshal([]byte(hopGraphBody(0, 0, ids["A1"], ids["A2"], store.TriggerSubjectSelf, store.TriggerActionBanBanned, "", 4141)), &graph); err != nil {
		t.Fatal(err)
	}
	delete(graph, "chain_id")
	body := map[string]any{"name": "new graph", "hops": []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}}, "node": map[string]any{"protocol": "socks"}, "orchestrations": []any{graph}}
	_, env := postCreateChain(t, srv, body)
	if env.Code != "OK" {
		t.Fatalf("create with graph: %+v", env)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	stored, err := st.ChainHopGraph(t.Context(), dto.ID, 0)
	if err != nil || stored == nil || stored.Nodes[0].ListenPort != 4141 {
		t.Fatalf("initial graph = %+v, %v", stored, err)
	}
}

func callTemplateHandler(t *testing.T, handler http.HandlerFunc, body any) handlerEnvelope {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/api/chain/orchestration-template/test", bytes.NewReader(raw)))
	return decodeHandlerEnvelope(t, rec)
}
func saveTemplateForTest(t *testing.T, s *Server, req templateRequest) store.OrchestrationTemplate {
	t.Helper()
	env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, req)
	if env.Code != "OK" {
		t.Fatalf("save template: %+v", env)
	}
	var tpl store.OrchestrationTemplate
	if err := json.Unmarshal(env.Data, &tpl); err != nil {
		t.Fatal(err)
	}
	return tpl
}
func templateForTest(root, backup int64) templateRequest {
	return templateRequest{Name: "Shared", OrchestrationGraph: store.OrchestrationGraph{
		Nodes: []store.OrchestrationNodeInput{{Kind: "server", IsRoot: true, ServerID: root, ListenPort: 4421}, {Kind: "trigger", Subject: "self", Action: "ban_banned"}, {Kind: "server", ServerID: backup, ListenPort: 7000}},
		Edges: []store.OrchestrationEdgeInput{{From: 0, To: 1}, {From: 1, To: 2}}}}
}
func createBoundChain(t *testing.T, s *Server, ids map[string]int64, tpl store.OrchestrationTemplate, overrides ...store.ParamOverride) int64 {
	t.Helper()
	return createBoundChainWithProtocol(t, s, ids, tpl, shared.ProtocolTrojan, overrides...)
}

func createBoundChainWithProtocol(t *testing.T, s *Server, ids map[string]int64, tpl store.OrchestrationTemplate, protocol string, overrides ...store.ParamOverride) int64 {
	t.Helper()
	_, env := postCreateChain(t, s, createChainRequest{Name: "bound", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}}, Node: createNodeRequest{Protocol: protocol}, Orchestrations: []orchestrationDraft{{Seq: 0, TemplateID: tpl.ID, TemplateRevision: tpl.Revision, Overrides: overrides}}})
	if env.Code != "OK" {
		t.Fatalf("bind: %+v", env)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	r, err := s.st.DesiredChainRevision(t.Context(), dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the realized service result used by subsequent incremental deployments.
	if err := s.st.SetNodeActive(t.Context(), r.Snapshot.ServiceNodeID, json.RawMessage(`{"port":33441}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.st.PublishChainRevision(t.Context(), r.ID, false); err != nil {
		t.Fatal(err)
	}
	return dto.ID
}

type templateUpgradeRequester struct {
	*fakeAutoRequester
	updating int64
}

func (r *templateUpgradeRequester) IsUpdating(id int64) bool { return id == r.updating }
func TestTemplatePropagationAndDurableWorker(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	a := createBoundChain(t, s, ids, tpl)
	zero, empty := 0, ""
	b := createBoundChain(t, s, ids, tpl, store.ParamOverride{ServerID: ids["A1"], ListenPort: &zero, Address: &empty})
	r.ID, r.Revision = tpl.ID, 1
	r.Nodes[0].ListenPort = 4412
	r.Nodes[2].ListenPort = 7001
	preview := callTemplateHandler(t, s.handlePreviewOrchestrationTemplate, r)
	var impact struct {
		Affected []orchestrationImpact `json:"affected_chains"`
	}
	if err := json.Unmarshal(preview.Data, &impact); err != nil || len(impact.Affected) != 2 || !impact.Affected[0].Redeploy || impact.Affected[1].Redeploy {
		t.Fatalf("impact: %s %v", preview.Data, err)
	}
	tpl = saveTemplateForTest(t, s, r)
	if tpl.Revision != 2 || tpl.UsageCount != 2 {
		t.Fatalf("saved: %+v", tpl)
	}
	if env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, r); env.Code != "CONFLICT" {
		t.Fatalf("stale edit: %+v", env)
	}
	ga, err := st.ChainHopGraph(ctx, a, 0)
	if err != nil || !ga.SyncPending || ga.Binding.Revision != 1 || ga.Nodes[0].ListenPort != 4412 {
		t.Fatalf("pending: %+v %v", ga, err)
	}
	gb, err := st.ChainHopGraph(ctx, b, 0)
	if err != nil || gb.SyncPending || gb.Binding.Revision != 2 || gb.Binding.Overrides[0].Address == nil || *gb.Binding.Overrides[0].Address != "" || gb.Nodes[0].ListenPort != 0 || gb.Nodes[2].ListenPort != 7001 {
		t.Fatalf("explicit override: %+v %v", gb, err)
	}
	// A fresh orchestrator recovers the queue, including after a planned Agent reconnect.
	u := &templateUpgradeRequester{fakeAutoRequester: s.req.(*fakeAutoRequester), updating: ids["A1"]}
	s.req = u
	restarted := newAutoOrchestrator(s)
	w := chainWorker{chainID: a, o: restarted, state: workerIdle}
	w.step(ctx)
	chain, _ := st.ChainByID(ctx, a)
	if chain.DesiredRevisionID != 0 || w.stateOf() == workerFailed {
		t.Fatalf("upgrading: %+v %s", chain, w.stateOf())
	}
	u.updating = 0
	w.step(ctx)
	desired, err := st.DesiredChainRevision(ctx, a)
	if err != nil || desired == nil || desired.Snapshot.Hops[0].ForwardPort != 4412 || desired.Snapshot.Hops[0].ServerID != ids["A1"] {
		t.Fatalf("same-server deployment: %+v %v", desired, err)
	}
	tasks, err := st.RevisionTasks(ctx, desired.ID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("missing plan tasks: %+v %v", tasks, err)
	}
	ga, _ = st.ChainHopGraph(ctx, a, 0)
	if !ga.SyncPending || ga.Binding.Revision != 1 {
		t.Fatalf("premature acknowledgement: %+v", ga)
	}
	events, err := st.ChainFailoverEvents(ctx, a, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("same-server failover events: %+v %v", events, err)
	}
	r.Revision = 2
	r.Nodes[2].ListenPort = 7002
	if env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, r); env.Code == "OK" || !strings.Contains(env.Message, "bound") {
		t.Fatalf("updating linked chain must block: %+v", env)
	}
	gb, _ = st.ChainHopGraph(ctx, b, 0)
	if gb.Nodes[2].ListenPort != 7001 {
		t.Fatalf("partial update: %+v", gb)
	}
	// Deliver the real task's response through the dispatcher's normal receipt
	// handler. Publishing the revision must acknowledge the pending template.
	for _, task := range tasks {
		if task.Kind != store.RevisionPieceForward || task.CommandID == 0 {
			t.Fatalf("expected queued same-server forward: %+v", task)
		}
		cmd, err := st.CommandByID(ctx, task.CommandID)
		if err != nil {
			t.Fatal(err)
		}
		var payload shared.ApplyChainHopPayload
		if err := json.Unmarshal(cmd.Data, &payload); err != nil || payload.Forward == nil || payload.Forward.Port != 4412 {
			t.Fatalf("forward command: %s %v", cmd.Data, err)
		}
		data, err := json.Marshal(shared.ApplyResultPayload{HopID: task.HopID, Kind: shared.HopKindForward, RealizedConfig: &shared.RealizedConfig{Port: 4412}})
		if err != nil {
			t.Fatal(err)
		}
		s.disp.HandleMessage(cmd.ServerID, shared.Envelope{Kind: shared.KindResponse, Type: cmd.Type, RequestID: cmd.RequestID, TraceID: cmd.TraceID, Code: shared.CodeOK, Data: data})
	}
	ga, _ = st.ChainHopGraph(ctx, a, 0)
	if ga.SyncPending || ga.Binding.Revision != 2 {
		t.Fatalf("publish did not acknowledge: %+v", ga)
	}
	tpl = saveTemplateForTest(t, s, r)
	ga, _ = st.ChainHopGraph(ctx, a, 0)
	if ga.SyncPending || ga.Binding.Revision != tpl.Revision {
		t.Fatalf("candidate-only deployment: %+v", ga)
	}
	if env := callTemplateHandler(t, s.handleDeleteOrchestrationTemplate, map[string]any{"id": tpl.ID}); env.Code != "OK" {
		t.Fatalf("delete: %+v", env)
	}
	gb, _ = st.ChainHopGraph(ctx, b, 0)
	if gb.Binding != nil || gb.Nodes[0].ListenPort != 0 || gb.Nodes[2].ListenPort != 7002 {
		t.Fatalf("delete lost materialization: %+v", gb)
	}
}

func editDraftForTest(t *testing.T, s *Server, id int64, ids map[string]int64, drafts []orchestrationDraft) handlerEnvelope {
	t.Helper()
	return callTemplateHandler(t, s.handleEditChain, editChainRequest{ChainID: id, Name: "edited", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}}, Node: createNodeRequest{Protocol: "trojan"}, Orchestrations: drafts})
}
func publishTestEdit(t *testing.T, s *Server, id int64) {
	t.Helper()
	c, err := s.st.ChainByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if c.DesiredRevisionID != 0 {
		if err := s.st.PublishChainRevision(t.Context(), c.DesiredRevisionID, false); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTemplateDetachRestoreAndCandidateRemoval(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	zero := 0
	a := createBoundChain(t, s, ids, tpl, store.ParamOverride{ServerID: ids["A1"], ListenPort: &zero})
	// Restore inheritance on edit.
	env := editDraftForTest(t, s, a, ids, []orchestrationDraft{{Seq: 0, TemplateID: tpl.ID, TemplateRevision: 1}})
	if env.Code != "ACCEPTED" {
		t.Fatalf("restore: %+v", env)
	}
	publishTestEdit(t, s, a)
	g, _ := st.ChainHopGraph(ctx, a, 0)
	if g.Nodes[0].ListenPort != 4421 || len(g.Binding.Overrides) != 0 {
		t.Fatalf("restore: %+v", g)
	}
	r.ID, r.Revision = tpl.ID, 1
	r.Nodes[2].ListenPort = 7001
	tpl = saveTemplateForTest(t, s, r)
	// The topology can be running on the backup at template-edit time.
	if _, err := s.switchChainHop(ctx, a, 0, store.HopSwitchTarget{ServerID: ids["A2"], ListenPort: 7001}, store.FailoverReasonManualSwitch, "test"); err != nil {
		t.Fatal(err)
	}
	publishTestEdit(t, s, a)
	bad := templateRequest{ID: tpl.ID, Revision: tpl.Revision, Name: tpl.Name, OrchestrationGraph: store.OrchestrationGraph{Nodes: []store.OrchestrationNodeInput{r.Nodes[0]}}}
	env = callTemplateHandler(t, s.handleSaveOrchestrationTemplate, bad)
	if env.Code == "OK" || !strings.Contains(env.Message, "当前运行") {
		t.Fatalf("removed active candidate: %+v", env)
	}
	// Detach from the currently resolved graph and preserve its values.
	activeIDs := map[string]int64{"A1": ids["A2"], "B1": ids["B1"]}
	env = editDraftForTest(t, s, a, activeIDs, []orchestrationDraft{{Seq: 0, OrchestrationGraph: tpl.OrchestrationGraph}})
	if env.Code != "ACCEPTED" {
		t.Fatalf("detach: %+v", env)
	}
	publishTestEdit(t, s, a)
	r.Revision = tpl.Revision
	r.Nodes[2].ListenPort = 7002
	saveTemplateForTest(t, s, r)
	g, _ = st.ChainHopGraph(ctx, a, 0)
	if g.Binding != nil || g.Nodes[2].ListenPort != 7001 {
		t.Fatalf("detached changed: %+v", g)
	}
	env = editDraftForTest(t, s, a, activeIDs, []orchestrationDraft{{Seq: 0, OrchestrationGraph: store.OrchestrationGraph{Nodes: []store.OrchestrationNodeInput{}, Edges: []store.OrchestrationEdgeInput{}}}})
	if env.Code != "ACCEPTED" {
		t.Fatalf("clear: %+v", env)
	}
	g, _ = st.ChainHopGraph(ctx, a, 0)
	if g != nil {
		t.Fatalf("explicit empty graph retained: %+v", g)
	}
}
func TestTemplatePortCollisionRejectsAllBindings(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	r.Nodes[0].ListenPort = 0
	tpl := saveTemplateForTest(t, s, r)
	a := createBoundChain(t, s, ids, tpl)
	b := createBoundChain(t, s, ids, tpl)
	r.ID, r.Revision = tpl.ID, 1
	r.Nodes[0].ListenPort = 4433
	env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, r)
	if env.Code == "OK" || !strings.Contains(env.Message, "4433") {
		t.Fatalf("conflicting shared defaults: %+v", env)
	}
	for _, id := range []int64{a, b} {
		g, _ := st.ChainHopGraph(t.Context(), id, 0)
		if g.Nodes[0].ListenPort != 0 || g.SyncPending {
			t.Fatalf("partial propagation: %+v", g)
		}
	}
}

func TestTemplatePortCollisionWithPendingSync(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	first := templateForTest(ids["A1"], ids["A2"])
	first.Name, first.Nodes[0].ListenPort = "First template", 0
	a := saveTemplateForTest(t, s, first)
	firstChain := createBoundChain(t, s, ids, a)
	second := templateForTest(ids["A1"], ids["A2"])
	second.Name, second.Nodes[0].ListenPort = "Second template", 0
	b := saveTemplateForTest(t, s, second)
	secondChain := createBoundChain(t, s, ids, b)

	s.req = &templateUpgradeRequester{fakeAutoRequester: s.req.(*fakeAutoRequester), updating: ids["A1"]}
	first.ID, first.Revision, first.Nodes[0].ListenPort = a.ID, a.Revision, 4433
	updated := saveTemplateForTest(t, s, first)
	w := chainWorker{chainID: firstChain, o: newAutoOrchestrator(s), state: workerIdle}
	w.step(ctx)
	chain, err := st.ChainByID(ctx, firstChain)
	if err != nil || chain.DesiredRevisionID != 0 {
		t.Fatalf("planned Agent update did not defer deployment: %+v, %v", chain, err)
	}
	second.ID, second.Revision, second.Nodes[0].ListenPort = b.ID, b.Revision, 4433
	if rejected := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, second); rejected.Code != "INVALID_ARGUMENT" || !strings.Contains(rejected.Message, "4433") {
		t.Fatalf("second template claimed a pending listener: %+v", rejected)
	}
	firstGraph, err := st.ChainHopGraph(ctx, firstChain, 0)
	if err != nil || firstGraph == nil || !firstGraph.SyncPending || firstGraph.Nodes[0].ListenPort != 4433 || firstGraph.Binding == nil || firstGraph.Binding.Revision != updated.Revision-1 {
		t.Fatalf("first reservation changed: %+v, %v", firstGraph, err)
	}
	secondGraph, err := st.ChainHopGraph(ctx, secondChain, 0)
	if err != nil || secondGraph == nil || secondGraph.SyncPending || secondGraph.Nodes[0].ListenPort != 0 || secondGraph.Binding == nil || secondGraph.Binding.Revision != b.Revision {
		t.Fatalf("rejected save changed its binding: %+v, %v", secondGraph, err)
	}
	stored, err := st.OrchestrationTemplate(ctx, b.ID)
	if err != nil || stored.Revision != b.Revision {
		t.Fatalf("rejected save changed its template revision: %+v, %v", stored, err)
	}
}

func TestTemplatePortSharingWithPendingEntrySync(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	r.Nodes[0].ListenPort = 0
	first := saveTemplateForTest(t, s, r)
	r.Name = "Second template"
	second := saveTemplateForTest(t, s, r)
	a := createBoundChainWithProtocol(t, s, ids, first, shared.ProtocolVLESS)
	b := createBoundChainWithProtocol(t, s, ids, second, shared.ProtocolVLESS)
	s.req = &templateUpgradeRequester{fakeAutoRequester: s.req.(*fakeAutoRequester), updating: ids["A1"]}
	for _, tpl := range []store.OrchestrationTemplate{first, second} {
		r.ID, r.Revision, r.Name = tpl.ID, tpl.Revision, tpl.Name
		r.Nodes[0].ListenPort = 4433
		saveTemplateForTest(t, s, r)
	}
	for _, id := range []int64{a, b} {
		g, err := st.ChainHopGraph(t.Context(), id, 0)
		if err != nil || g == nil || !g.SyncPending || g.Nodes[0].ListenPort != 4433 {
			t.Fatalf("compatible entry reservation: %+v, %v", g, err)
		}
	}
}

func TestChainEditPreservesUnmentionedTemplateBinding(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	r := templateForTest(ids["B1"], ids["B2"])
	tpl := saveTemplateForTest(t, s, r)
	_, env := postCreateChain(t, s, createChainRequest{
		Name: "bound exit", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}},
		Node: createNodeRequest{Protocol: "trojan"}, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}},
	})
	if env.Code != "OK" {
		t.Fatalf("create: %+v", env)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	publishTestEdit(t, s, dto.ID)
	entry := store.OrchestrationGraph{Nodes: []store.OrchestrationNodeInput{{Kind: store.HopGraphKindServer, ServerID: ids["A1"], IsRoot: true}}}
	edit := editDraftForTest(t, s, dto.ID, ids, []orchestrationDraft{{Seq: 0, OrchestrationGraph: entry}})
	if edit.Code != "ACCEPTED" {
		t.Fatalf("unrelated edit: %+v", edit)
	}
	publishTestEdit(t, s, dto.ID)
	g, err := st.ChainHopGraph(ctx, dto.ID, 1)
	if err != nil || g == nil || g.Binding == nil || g.Nodes[0].ListenPort != r.Nodes[0].ListenPort {
		t.Fatalf("edited an unmentioned template graph: %+v, %v", g, err)
	}
	remove := editChainRequest{ChainID: dto.ID, Name: "remove exit", Hops: []chainHopRef{{ServerID: ids["A1"]}}, Node: createNodeRequest{Protocol: "trojan"}}
	if rejected := callTemplateHandler(t, s.handleEditChain, remove); rejected.Code == "ACCEPTED" {
		t.Fatalf("omission removed a template hop: %+v", rejected)
	}
	remove.Orchestrations = []orchestrationDraft{{Seq: 1}}
	if saved := callTemplateHandler(t, s.handleEditChain, remove); saved.Code != "ACCEPTED" {
		t.Fatalf("explicit removal: %+v", saved)
	}
	g, err = st.ChainHopGraph(ctx, dto.ID, 1)
	if err != nil || g != nil {
		t.Fatalf("removed template graph: %+v, %v", g, err)
	}
}

func TestTemplateBindingValidatesBoundaryBeforeCreate(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	r.Nodes[1].Subject = store.TriggerSubjectPrev
	r.Nodes[1].Action = store.TriggerActionSegmentTimeout
	tpl := saveTemplateForTest(t, s, r)
	ctx := t.Context()
	before, err := st.ListChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, env := postCreateChain(t, s, createChainRequest{
		Name: "invalid entrance", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}},
		Node: createNodeRequest{Protocol: "trojan"}, Orchestrations: []orchestrationDraft{{Seq: 0, TemplateID: tpl.ID}},
	})
	if env.Code != "INVALID_ARGUMENT" || !strings.Contains(env.Message, "上一跳") {
		t.Fatalf("unusable entrance template was accepted: %+v", env)
	}
	after, err := st.ListChains(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("invalid binding wrote a chain: %+v, %v", after, err)
	}
	uses, err := st.TemplateUses(ctx, tpl.ID)
	if err != nil || len(uses) != 0 {
		t.Fatalf("invalid binding was stored: %+v, %v", uses, err)
	}
}

func TestTemplateBindingKeepsOverridesForRemovedCandidate(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	zero := 0
	backup := store.ParamOverride{ServerID: ids["A2"], ListenPort: &zero}
	id := createBoundChain(t, s, ids, tpl, backup)
	r.ID, r.Revision = tpl.ID, tpl.Revision
	removed := r
	removed.Nodes, removed.Edges = r.Nodes[:1], nil
	tpl = saveTemplateForTest(t, s, removed)
	if edited := editDraftForTest(t, s, id, ids, []orchestrationDraft{{Seq: 0, TemplateID: tpl.ID, TemplateRevision: tpl.Revision, Overrides: []store.ParamOverride{backup}}}); edited.Code != "ACCEPTED" {
		t.Fatalf("edit with previously saved candidate override: %+v", edited)
	}
	publishTestEdit(t, s, id)
	r.Revision = tpl.Revision
	saveTemplateForTest(t, s, r)
	g, err := st.ChainHopGraph(t.Context(), id, 0)
	if err != nil || g == nil || len(g.Nodes) != 3 || g.Nodes[2].ListenPort != 0 || g.Binding == nil || len(g.Binding.Overrides) != 1 {
		t.Fatalf("restored candidate lost its override: %+v, %v", g, err)
	}
}

func TestTemplateSyncFailureKeepsBindingThroughRetry(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	zero := 0
	id := createBoundChain(t, s, ids, tpl, store.ParamOverride{ServerID: ids["A2"], ListenPort: &zero})
	r.ID, r.Revision = tpl.ID, tpl.Revision
	r.Nodes[0].ListenPort = 4412
	updated := saveTemplateForTest(t, s, r)
	w := chainWorker{chainID: id, o: newAutoOrchestrator(s), state: workerIdle}
	w.step(ctx)
	desired, err := st.DesiredChainRevision(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	forward := func() store.Command {
		t.Helper()
		tasks, err := st.RevisionTasks(ctx, desired.ID)
		if err != nil || len(tasks) != 1 || tasks[0].Kind != store.RevisionPieceForward || tasks[0].CommandID == 0 {
			t.Fatalf("unexpected revision tasks: %+v, %v", tasks, err)
		}
		cmd, err := st.CommandByID(ctx, tasks[0].CommandID)
		if err != nil {
			t.Fatal(err)
		}
		return *cmd
	}
	cmd := forward()
	failure, err := json.Marshal(shared.ApplyResultPayload{HopID: desired.Snapshot.Hops[0].HopID, Kind: shared.HopKindForward})
	if err != nil {
		t.Fatal(err)
	}
	s.disp.HandleMessage(cmd.ServerID, shared.Envelope{
		Kind: shared.KindResponse, Type: cmd.Type, RequestID: cmd.RequestID, TraceID: cmd.TraceID,
		Code: shared.CodeInternalError, Message: "listener unavailable", Data: failure,
	})
	w.step(ctx)
	message, err := st.ChainOrchestrationError(ctx, id)
	if err != nil || w.stateOf() != workerFailed || !strings.Contains(message, "listener unavailable") {
		t.Fatalf("failed deploy: worker=%s, persisted=%q, err=%v", w.stateOf(), message, err)
	}
	g, err := st.ChainHopGraph(ctx, id, 0)
	if err != nil || g == nil || !g.SyncPending || g.Binding == nil || g.Binding.Revision != tpl.Revision || len(g.Binding.Overrides) != 1 || g.Nodes[2].ListenPort != 0 {
		t.Fatalf("failed sync lost its binding or override: %+v, %v", g, err)
	}
	if err := s.disp.RetryChain(ctx, id); err != nil {
		t.Fatal(err)
	}
	w.resume(ctx)
	retry := forward()
	if retry.ID == cmd.ID {
		t.Fatalf("retry reused failed command %d", retry.ID)
	}
	payload, err := json.Marshal(shared.ApplyResultPayload{HopID: desired.Snapshot.Hops[0].HopID, Kind: shared.HopKindForward, RealizedConfig: &shared.RealizedConfig{Port: 4412}})
	if err != nil {
		t.Fatal(err)
	}
	s.disp.HandleMessage(retry.ServerID, shared.Envelope{
		Kind: shared.KindResponse, Type: retry.Type, RequestID: retry.RequestID, TraceID: retry.TraceID, Code: shared.CodeOK, Data: payload,
	})
	g, err = st.ChainHopGraph(ctx, id, 0)
	if err != nil || g == nil || g.SyncPending || g.Binding == nil || g.Binding.Revision != updated.Revision || g.Nodes[2].ListenPort != 0 {
		t.Fatalf("retry failed to acknowledge the published template: %+v, %v", g, err)
	}
}
