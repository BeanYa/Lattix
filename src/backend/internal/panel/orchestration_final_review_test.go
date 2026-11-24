package panel

import (
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func previewProofForTest(t *testing.T, s *Server, body any) string {
	t.Helper()
	env := callTemplateHandler(t, s.handlePreviewOrchestrationTemplate, body)
	if env.Code != shared.CodeOK {
		t.Fatalf("preview: %+v", env)
	}
	var data struct {
		Proof string `json:"preview_proof"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.Proof
}

func TestFinalReviewPendingRenamePreservesEntrySync(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "offline", true: "failed"}[failure], func(t *testing.T) {
			s, st, _, ids := setupOrchestrationHandler(t)
			req := templateForTest(ids["A1"], ids["A2"])
			tpl := saveTemplateForTest(t, s, req)
			id := createReviewChain(t, s, createChainRequest{Name: "original", Hops: []chainHopRef{{ServerID: ids["A1"]}}, Node: createNodeRequest{Protocol: shared.ProtocolVLESS}, Orchestrations: []orchestrationDraft{{TemplateID: tpl.ID}}})
			original, _ := st.ChainByID(t.Context(), id)
			respondReviewEndpoint(t, s, original.EndpointID, 4421, "")
			req.ID, req.Revision, req.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 4433
			changed := saveTemplateForTest(t, s, req)
			requester := s.req.(*fakeAutoRequester)
			if !failure {
				requester.online[ids["A1"]] = false
			}
			w := chainWorker{chainID: id, o: newAutoOrchestrator(s), state: workerIdle}
			w.step(t.Context())
			desired, _ := st.DesiredChainRevision(t.Context(), id)
			if failure {
				respondReviewEndpoint(t, s, desired.Snapshot.EndpointID, 4433, "entry bind failed")
				w.step(t.Context())
			}
			before, _ := st.TemplateUses(t.Context(), tpl.ID)
			env := callTemplateHandler(t, s.handleEditChain, editChainRequest{ChainID: id, Name: "renamed", Hops: []chainHopRef{{ServerID: ids["A1"]}}, Node: createNodeRequest{Protocol: shared.ProtocolVLESS}, Orchestrations: []orchestrationDraft{{TemplateID: tpl.ID, TemplateRevision: changed.Revision}}})
			if env.Code != shared.CodeConflict || !strings.Contains(env.Message, "同步") {
				t.Fatalf("pending rename accepted or not actionable: %s %s", env.Code, env.Message)
			}
			after, _ := st.TemplateUses(t.Context(), tpl.ID)
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			graph, _ := st.ChainHopGraph(t.Context(), id, 0)
			chain, _ := st.ChainByID(t.Context(), id)
			still, _ := st.DesiredChainRevision(t.Context(), id)
			previousSnapshot, _ := json.Marshal(desired.Snapshot)
			currentSnapshot, _ := json.Marshal(still.Snapshot)
			if string(previousSnapshot) != string(currentSnapshot) || string(a) != string(b) || !graph.SyncPending || graph.Binding.Revision != 1 || chain.Name != "original" || still.ID != desired.ID {
				t.Fatalf("rejected edit mutated pending state: %+v %+v", graph, chain)
			}
			if failure {
				if err := s.disp.RetryChain(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			if !failure {
				requester.online[ids["A1"]] = true
				s.disp.OnAgentConnect(t.Context(), ids["A1"])
			}
			respondReviewEndpoint(t, s, desired.Snapshot.EndpointID, 4433, "")
			graph, _ = st.ChainHopGraph(t.Context(), id, 0)
			if graph.SyncPending || graph.Binding.Revision != changed.Revision {
				t.Fatalf("matching receipt did not acknowledge: %+v", graph)
			}
		})
	}
}

func TestFinalReviewPreviewConstrainsSaveAndDelete(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "delete"}[deleting], func(t *testing.T) {
			s, st, _, ids := setupOrchestrationHandler(t)
			req := templateForTest(ids["A1"], ids["A2"])
			tpl := saveTemplateForTest(t, s, req)
			first := createBoundChain(t, s, ids, tpl)
			body := map[string]any{"id": tpl.ID, "revision": tpl.Revision, "name": tpl.Name, "nodes": tpl.Nodes, "edges": tpl.Edges}
			if deleting {
				body["operation"] = "delete"
			} else {
				nodes := append([]store.OrchestrationNodeInput(nil), tpl.Nodes...)
				nodes[0].ListenPort = 4433
				body["nodes"] = nodes
			}
			body["preview_proof"] = previewProofForTest(t, s, body)
			second := createBoundChain(t, s, map[string]int64{"A1": ids["A1"], "B1": ids["B2"]}, tpl, store.ParamOverride{ServerID: ids["A1"], ListenPort: new(int)})
			handler := s.handleSaveOrchestrationTemplate
			if deleting {
				handler = s.handleDeleteOrchestrationTemplate
			}
			env := callTemplateHandler(t, handler, body)
			if env.Code != shared.CodeConflict {
				t.Fatalf("confirmation affected unpreviewed binding: %+v", env)
			}
			saved, err := st.OrchestrationTemplate(t.Context(), tpl.ID)
			if err != nil || saved.Revision != 1 {
				t.Fatalf("partial template mutation: %+v %v", saved, err)
			}
			for _, id := range []int64{first, second} {
				g, _ := st.ChainHopGraph(t.Context(), id, 0)
				if g.Binding == nil || g.Binding.Revision != 1 || g.SyncPending || g.Nodes[0].ListenPort != map[int64]int{first: 4421, second: 0}[id] {
					t.Fatalf("partial graph mutation: %+v", g)
				}
			}
			body["preview_proof"] = previewProofForTest(t, s, body)
			if env := callTemplateHandler(t, handler, body); env.Code != shared.CodeOK {
				t.Fatalf("refreshed confirmation: %+v", env)
			}
		})
	}
}

func TestFinalReviewMaterializedRevisionDTO(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	req := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, req)
	zero := 0
	blank := ""
	id := createBoundChain(t, s, ids, tpl, store.ParamOverride{ServerID: ids["A2"], ListenPort: &zero, Address: &blank})
	req.ID, req.Revision, req.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 4433
	saveTemplateForTest(t, s, req)
	g, _ := st.ChainHopGraph(t.Context(), id, 0)
	raw, _ := json.Marshal(g.Binding)
	var binding map[string]any
	_ = json.Unmarshal(raw, &binding)
	if binding["template_revision"] != float64(1) || binding["template_source_revision"] != float64(2) {
		t.Fatalf("pending materialization mislabeled: %s", raw)
	}
}

func TestFinalReviewDeletionPreviewBypassesUpdateValidation(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	tpl := saveTemplateForTest(t, s, templateForTest(ids["A1"], ids["A2"]))
	id := createBoundChain(t, s, ids, tpl)
	graph, _ := st.ChainHopGraph(t.Context(), id, 0)
	chain, _ := st.ChainByID(t.Context(), id)
	// A damaged legacy source must not prevent detaching its intact materialization.
	input, err := resolvedGraph(id, 0, tpl.OrchestrationGraph, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl.OrchestrationGraph = store.OrchestrationGraph{}
	invalid, err := st.SaveOrchestrationTemplate(t.Context(), tpl, []store.TemplatePropagation{{Graph: input, PublishedID: chain.PublishedRevisionID}})
	if err != nil {
		t.Fatal(err)
	}
	graph, _ = st.ChainHopGraph(t.Context(), id, 0)
	if err := st.SetChainStatus(t.Context(), id, store.ChainStatusApplying, ""); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"id": tpl.ID, "operation": "delete", "revision": invalid.Revision}
	body["preview_proof"] = previewProofForTest(t, s, body)
	if env := callTemplateHandler(t, s.handleDeleteOrchestrationTemplate, body); env.Code != shared.CodeOK {
		t.Fatalf("delete invalid/applying: %s %s", env.Code, env.Message)
	}
	after, _ := st.ChainHopGraph(t.Context(), id, 0)
	beforeNodes, _ := json.Marshal(graph.Nodes)
	afterNodes, _ := json.Marshal(after.Nodes)
	if after.Binding != nil || string(beforeNodes) != string(afterNodes) {
		t.Fatalf("detach changed materialization: %+v", after)
	}
}

func TestFinalReviewConfirmationProofRequiredAndOperationBound(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	tpl := saveTemplateForTest(t, s, templateForTest(ids["A1"], ids["A2"]))
	req := templateRequest{ID: tpl.ID, Revision: tpl.Revision, Name: tpl.Name, OrchestrationGraph: tpl.OrchestrationGraph}
	saveProof := previewProofForTest(t, s, req)
	deletion := map[string]any{"id": tpl.ID, "operation": "delete", "revision": tpl.Revision}
	deleteProof := previewProofForTest(t, s, deletion)
	for _, proof := range []string{"", deleteProof} {
		req.PreviewProof = proof
		if env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, req); env.Code != shared.CodeConflict {
			t.Fatalf("save accepted missing/delete proof: %s %s", env.Code, env.Message)
		}
	}
	for _, proof := range []string{"", saveProof} {
		deletion["preview_proof"] = proof
		if env := callTemplateHandler(t, s.handleDeleteOrchestrationTemplate, deletion); env.Code != shared.CodeConflict {
			t.Fatalf("delete accepted missing/save proof: %s %s", env.Code, env.Message)
		}
	}
	current, _ := st.OrchestrationTemplate(t.Context(), tpl.ID)
	if current.Revision != tpl.Revision {
		t.Fatal("invalid proof changed template")
	}
	req.PreviewProof = saveProof
	changed := saveTemplateForTest(t, s, req)
	deletion["preview_proof"] = deleteProof
	if env := callTemplateHandler(t, s.handleDeleteOrchestrationTemplate, deletion); env.Code != shared.CodeConflict {
		t.Fatalf("stale revision deleted v%d: %s", changed.Revision, env.Code)
	}
}

func TestFinalReviewPreviewConstrainsRedeployOperation(t *testing.T) {
	s, st, _, ids := setupOrchestrationHandler(t)
	req := templateForTest(ids["A1"], ids["A2"])
	tpl := saveTemplateForTest(t, s, req)
	id := createBoundChain(t, s, ids, tpl)
	req.ID, req.Revision, req.Nodes[0].ListenPort = tpl.ID, tpl.Revision, 4433
	req.PreviewProof = previewProofForTest(t, s, req)
	zero := 0
	if env := editDraftForTest(t, s, id, ids, []orchestrationDraft{{TemplateID: tpl.ID, TemplateRevision: tpl.Revision, Overrides: []store.ParamOverride{{ServerID: ids["A1"], ListenPort: &zero}}}}); env.Code != shared.CodeAccepted {
		t.Fatalf("override edit: %s %s", env.Code, env.Message)
	}
	publishTestEdit(t, s, id)
	if env := callTemplateHandler(t, s.handleSaveOrchestrationTemplate, req); env.Code != shared.CodeConflict {
		t.Fatalf("changed hop operation accepted: %s %s", env.Code, env.Message)
	}
	current, _ := st.OrchestrationTemplate(t.Context(), tpl.ID)
	if current.Revision != tpl.Revision {
		t.Fatal("operation conflict changed template")
	}
}
