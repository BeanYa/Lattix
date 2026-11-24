package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestPartialEditKeepsIndependentCandidateGraph(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	graph := templateForTest(ids["A1"], ids["A2"]).OrchestrationGraph
	id := createReviewChain(t, s, createChainRequest{
		Name: "independent", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}},
		Node: createNodeRequest{Protocol: shared.ProtocolTrojan}, Orchestrations: []orchestrationDraft{{Seq: 0, OrchestrationGraph: graph}},
	})
	publishTestEdit(t, s, id)
	if _, err := s.switchChainHop(t.Context(), id, 0, store.HopSwitchTarget{ServerID: ids["A2"], ListenPort: 7000}, store.FailoverReasonManualSwitch, "test"); err != nil {
		t.Fatal(err)
	}
	publishTestEdit(t, s, id)
	exit := rootOnlyGraph(ids["B1"], 0)
	active := map[string]int64{"A1": ids["A2"], "B1": ids["B1"]}
	if env := editDraftForTest(t, s, id, active, []orchestrationDraft{{Seq: 1, OrchestrationGraph: exit}}); env.Code != shared.CodeAccepted {
		t.Fatalf("partial edit: %+v", env)
	}
	kept, err := st.ChainHopGraph(t.Context(), id, 0)
	if err != nil || kept == nil || kept.Binding != nil || len(kept.Nodes) != 3 || kept.Nodes[0].ListenPort != 4421 || kept.Nodes[2].ListenPort != 7000 {
		t.Fatalf("partial edit changed independent candidate graph: %+v, %v", kept, err)
	}
}

func rootOnlyGraph(id int64, port int) store.OrchestrationGraph {
	return store.OrchestrationGraph{Nodes: []store.OrchestrationNodeInput{{Kind: store.HopGraphKindServer, IsRoot: true, ServerID: id, ListenPort: port}}}
}

func createReviewChain(t *testing.T, s *Server, req createChainRequest) int64 {
	t.Helper()
	_, env := postCreateChain(t, s, req)
	if env.Code != shared.CodeOK {
		t.Fatalf("create %s: %s %s", req.Name, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	return dto.ID
}

func respondReviewEndpoint(t *testing.T, s *Server, endpointID int64, port int, failure string) {
	t.Helper()
	commands, err := s.st.CommandsByType(t.Context(), shared.TypeApplySharedEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(commands) - 1; i >= 0; i-- {
		c := commands[i]
		var apply shared.ApplySharedEndpointPayload
		if err := json.Unmarshal(c.Data, &apply); err != nil {
			t.Fatal(err)
		}
		if apply.EndpointID != endpointID || (c.Status != store.CommandStatusQueued && c.Status != store.CommandStatusSent) {
			continue
		}
		code := shared.CodeOK
		response := shared.ApplyResultPayload{EndpointID: endpointID, RealizedConfig: &shared.RealizedConfig{Port: port}}
		if failure != "" {
			code = shared.CodeInternalError
			response.RealizedConfig = nil
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		s.disp.HandleMessage(c.ServerID, shared.Envelope{Kind: shared.KindResponse, Type: c.Type, RequestID: c.RequestID, TraceID: c.TraceID, Code: code, Message: failure, Data: data})
		return
	}
	t.Fatalf("no outstanding apply command for endpoint %d", endpointID)
}

func TestTemplateSyncWaitsForSharedEntryRealization(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	id := createReviewChain(t, s, createChainRequest{
		Name: "vless", Hops: []chainHopRef{{ServerID: ids["A1"]}}, Node: createNodeRequest{Protocol: shared.ProtocolVLESS},
		Orchestrations: []orchestrationDraft{{TemplateID: tpl.ID}},
	})
	chain, _ := st.ChainByID(ctx, id)
	respondReviewEndpoint(t, s, chain.EndpointID, 4421, "")
	r.ID, r.Revision, r.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 4433
	changed := saveTemplateForTest(t, s, r)
	w := chainWorker{chainID: id, o: newAutoOrchestrator(s), state: workerIdle}
	w.step(ctx)
	readGraph := func() *store.ChainHopGraph {
		t.Helper()
		g, err := st.ChainHopGraph(ctx, id, 0)
		if err != nil || g == nil || g.Binding == nil {
			t.Fatalf("binding: %+v, %v", g, err)
		}
		return g
	}
	if g := readGraph(); !g.SyncPending || g.Binding.Revision != tpl.Revision {
		t.Fatalf("acknowledged before entry realization: %+v", g)
	}
	desired, err := st.DesiredChainRevision(ctx, id)
	if err != nil || desired.Snapshot.EndpointID == chain.EndpointID {
		t.Fatalf("missing planned entry listener: %+v, %v", desired, err)
	}
	respondReviewEndpoint(t, s, desired.Snapshot.EndpointID, 4433, "entry bind failed")
	w.step(ctx)
	message, err := st.ChainOrchestrationError(ctx, id)
	if g := readGraph(); !g.SyncPending || g.Binding.Revision != tpl.Revision || w.stateOf() != workerFailed || err != nil || !strings.Contains(message, "entry bind failed") {
		t.Fatalf("failed endpoint state: %+v worker=%s error=%q, %v", g, w.stateOf(), message, err)
	}
	if err := s.disp.RetryChain(ctx, id); err != nil {
		t.Fatal(err)
	}
	respondReviewEndpoint(t, s, desired.Snapshot.EndpointID, 4433, "")
	if g := readGraph(); g.SyncPending || g.Binding.Revision != changed.Revision {
		t.Fatalf("matching deployment did not acknowledge endpoint: %+v", g)
	}
	chain, _ = st.ChainByID(ctx, id)
	if chain.PublishedRevisionID != desired.ID {
		t.Fatalf("endpoint acknowledged without matching publication: %+v", chain)
	}
}

func TestChainOrchestrationRejectsIncompatibleExternalListener(t *testing.T) {
	st, s, entry, exit := externalChainFixture(t)
	tpl := saveTemplateForTest(t, s, templateRequest{Name: "external", OrchestrationGraph: rootOnlyGraph(exit, 45000)})
	req := createChainRequest{Name: "external listener", Hops: []chainHopRef{{ServerID: entry}, {ServerID: exit}},
		Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}}
	_, env := postCreateChain(t, s, req)
	if env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "外部直链") {
		t.Fatalf("accepted unowned external listener: %s %s", env.Code, env.Message)
	}
	if endpoints, err := st.SharedEndpointsByServer(t.Context(), entry); err != nil || len(endpoints) != 0 {
		t.Fatalf("rejected create provisioned endpoints: %+v, %v", endpoints, err)
	}
	req.Orchestrations = nil
	id := createReviewChain(t, s, req)
	publishTestEdit(t, s, id)
	edit := editChainRequest{ChainID: id, Name: req.Name, Hops: req.Hops, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}}
	if env := callTemplateHandler(t, s.handleEditChain, edit); env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "外部直链") {
		t.Fatalf("edit accepted unowned external listener: %s %s", env.Code, env.Message)
	}
}

func TestChainOrchestrationRejectsDifferentSharedHy2Listener(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	_, _, err := st.EnsureProtocolSharedEndpoint(ctx, ids["B1"], shared.ProtocolHysteria2, 44000,
		json.RawMessage(`{"protocol":"hysteria","port":44000}`))
	if err != nil {
		t.Fatal(err)
	}
	tpl := saveTemplateForTest(t, s, templateRequest{Name: "different shared exit", OrchestrationGraph: rootOnlyGraph(ids["B1"], 45000)})
	req := createChainRequest{Name: "hy2 listener", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: ids["B1"]}},
		Node: createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "off"}, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}}
	_, env := postCreateChain(t, s, req)
	if env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "44000") {
		t.Fatalf("accepted a different shared exit port: %s %s", env.Code, env.Message)
	}
	req.Orchestrations = nil
	id := createReviewChain(t, s, req)
	publishTestEdit(t, s, id)
	edit := editChainRequest{ChainID: id, Name: req.Name, Hops: req.Hops, Node: req.Node, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}}
	if env := callTemplateHandler(t, s.handleEditChain, edit); env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "44000") {
		t.Fatalf("edit accepted a different shared exit port: %s %s", env.Code, env.Message)
	}
}

func TestChainOrchestrationChecksActualHy2NATSpan(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	middle, err := st.CreateServer(t.Context(), store.ServerDraft{
		Alias: "NAT middle", Address: "nat.example.com", BootstrapToken: "middle-span", CountryCode: "JP",
		MachineType: store.MachineTypeNAT, AllowedPorts: `[{"pub_start":44000,"pub_end":44007},{"pub_start":45000,"pub_end":45002}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tpl := saveTemplateForTest(t, s, templateRequest{Name: "middle", OrchestrationGraph: rootOnlyGraph(middle, 45000)})
	req := createChainRequest{Name: "hy2 NAT span", Hops: []chainHopRef{{ServerID: ids["A1"]}, {ServerID: middle}, {ServerID: ids["B1"]}},
		Node: createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "44000-44007"}, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}}
	_, env := postCreateChain(t, s, req)
	if env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "45007") {
		t.Fatalf("accepted truncated NAT span: %s %s", env.Code, env.Message)
	}
	if endpoints, err := st.SharedEndpointsByServer(t.Context(), ids["B1"]); err != nil || len(endpoints) != 0 {
		t.Fatalf("rejected span provisioned endpoints: %+v, %v", endpoints, err)
	}
	withCandidate := templateForTest(ids["A2"], middle)
	withCandidate.Nodes[2].ListenPort = 45000
	tpl = saveTemplateForTest(t, s, withCandidate)
	req.Hops[1].ServerID = ids["A2"]
	req.Orchestrations[0].TemplateID = tpl.ID
	_, env = postCreateChain(t, s, req)
	if env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "45007") {
		t.Fatalf("accepted candidate with truncated NAT span: %s %s", env.Code, env.Message)
	}
}

func TestTemplatePendingHy2PortChecksWholeSpan(t *testing.T) {
	t.Run("fixed target", func(t *testing.T) {
		assertPendingHy2Span(t, 0, 45000, 45003, "45007")
	})
	t.Run("automatic target uses service span", func(t *testing.T) {
		assertPendingHy2Span(t, 45100, 0, 44003, "44007")
	})
}

func assertPendingHy2Span(t *testing.T, initial, target, overlap int, end string) {
	t.Helper()
	s, st, _, ids := setupOrchestrationHandler(t)
	ctx := t.Context()
	bind := func(name, portHop string, exit int64, port int) (templateRequest, int64) {
		t.Helper()
		r := templateRequest{Name: name, OrchestrationGraph: rootOnlyGraph(ids["A1"], port)}
		tpl := saveTemplateForTest(t, s, r)
		id := createReviewChain(t, s, createChainRequest{Name: name, Hops: []chainHopRef{{ServerID: ids["A2"]}, {ServerID: ids["A1"]}, {ServerID: exit}},
			Node: createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: portHop}, Orchestrations: []orchestrationDraft{{Seq: 1, TemplateID: tpl.ID}}})
		publishTestEdit(t, s, id)
		r.ID, r.Revision = tpl.ID, tpl.Revision
		return r, id
	}
	first, a := bind("first span", "44000-44007", ids["B1"], initial)
	// Reusing the same host for the original span would conflict during chain creation.
	// Give the second chain a distinct original span before applying new targets.
	second, b := bind("second span", "44100-44107", ids["B2"], 0)
	first.Nodes[0].ListenPort = target
	saveTemplateForTest(t, s, first)
	second.Nodes[0].ListenPort = overlap
	if env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, second); env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, end) {
		t.Fatalf("pending span did not reserve its interior: %s %s", env.Code, env.Message)
	}
	for id, pending := range map[int64]bool{a: true, b: false} {
		g, err := st.ChainHopGraph(ctx, id, 1)
		if err != nil || g == nil || g.SyncPending != pending {
			t.Fatalf("chain %d reservation state: %+v, %v", id, g, err)
		}
	}
}

func TestTemplatePendingPortBlocksCandidateActivation(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	first := createBoundChain(t, s, ids, tpl)
	r.ID, r.Revision, r.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 45000
	saveTemplateForTest(t, s, r)
	local := templateForTest(ids["A2"], ids["A1"]).OrchestrationGraph
	local.Nodes[0].ListenPort, local.Nodes[2].ListenPort = 45001, 45000
	other := createReviewChain(t, s, createChainRequest{Name: "candidate", Hops: []chainHopRef{{ServerID: ids["A2"]}, {ServerID: ids["B2"]}},
		Node: createNodeRequest{Protocol: shared.ProtocolTrojan}, Orchestrations: []orchestrationDraft{{Seq: 0, OrchestrationGraph: local}}})
	publishTestEdit(t, s, other)
	if env := callTemplateHandler(t, s.handleSwitchChainHop, chainSwitchHopRequest{ChainID: other, Seq: 0, ServerID: ids["A1"]}); env.Code == shared.CodeOK || !strings.Contains(env.Message, "45000") {
		t.Fatalf("candidate switch claimed pending listener: %+v", env)
	}
	g, err := st.ChainHopGraph(t.Context(), first, 0)
	if err != nil || g == nil || !g.SyncPending {
		t.Fatalf("pending target changed: %+v, %v", g, err)
	}
}

func TestNodeCreateWaitsForTemplatePortClaim(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	r := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, r)
	createBoundChain(t, s, ids, tpl)
	r.ID, r.Revision, r.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 45000
	request, err := json.Marshal(createNodeRequest{Name: "competing node", ServerID: ids["A1"], Protocol: shared.ProtocolTrojan, Port: &r.Nodes[0].ListenPort})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	s.orchestrationMu.Lock()
	go func() {
		s.handleCreateNode(rec, httptest.NewRequest(http.MethodPost, "/api/node/create", strings.NewReader(string(request))))
		close(done)
	}()
	select {
	case <-done:
		s.orchestrationMu.Unlock()
		t.Fatalf("node create completed during the template claim: %s", rec.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	plan, updates, _, err := s.templatePlan(t.Context(), r)
	if err == nil {
		_, err = st.SaveOrchestrationTemplate(t.Context(), plan, updates)
	}
	s.orchestrationMu.Unlock()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeHandlerEnvelope(t, rec); env.Code != shared.CodeInvalidArgument || !strings.Contains(env.Message, "45000") {
		t.Fatalf("node creation missed persisted claim: %+v", env)
	}
}
