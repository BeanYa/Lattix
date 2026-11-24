package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type orchestrationDraft struct {
	store.OrchestrationGraph
	Seq              int                   `json:"seq"`
	TemplateID       int64                 `json:"template_id,omitempty"`
	TemplateRevision int                   `json:"template_revision,omitempty"`
	Overrides        []store.ParamOverride `json:"overrides,omitempty"`
}
type templateRequest struct {
	store.OrchestrationGraph
	ID           int64  `json:"id,omitempty"`
	Revision     int    `json:"revision,omitempty"`
	Name         string `json:"name"`
	Operation    string `json:"operation,omitempty"`
	PreviewProof string `json:"preview_proof,omitempty"`
}
type orchestrationImpact struct {
	ChainID   int64  `json:"chain_id"`
	ChainName string `json:"chain_name"`
	Seq       int    `json:"seq"`
	Redeploy  bool   `json:"redeploy"`
}

func resolvedGraph(chainID int64, seq int, input store.OrchestrationGraph, overrides, previousOverrides []store.ParamOverride) (store.ChainHopGraph, error) {
	graph, err := buildGraphFromRequest(hopOrchestrationSaveRequest{ChainID: chainID, Seq: seq, Nodes: input.Nodes, Edges: input.Edges})
	if err != nil {
		return graph, err
	}
	servers := map[int64]*store.ChainHopGraphNode{}
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.Kind == store.HopGraphKindServer {
			servers[n.ServerID] = n
		}
	}
	seen := map[int64]bool{}
	previousServers := make(map[int64]bool, len(previousOverrides))
	for _, v := range previousOverrides {
		previousServers[v.ServerID] = true
	}
	for _, v := range overrides {
		if v.ServerID <= 0 || seen[v.ServerID] {
			return graph, fmt.Errorf("参数覆盖的服务器无效或重复：#%d", v.ServerID)
		}
		if v.ListenPort != nil && (*v.ListenPort < 0 || *v.ListenPort > 65535) {
			return graph, fmt.Errorf("服务器 #%d 的覆盖端口须为 0-65535", v.ServerID)
		}
		seen[v.ServerID] = true
		n := servers[v.ServerID]
		if n == nil {
			// Retain saved overrides for candidates removed from a template, so
			// an edit can round-trip them and a later restore can use them again.
			if previousServers[v.ServerID] {
				continue
			}
			return graph, fmt.Errorf("参数覆盖的服务器 #%d 不在模板中", v.ServerID)
		}
		if v.Address != nil {
			n.Address = strings.TrimSpace(*v.Address)
		}
		if v.ListenPort != nil {
			n.ListenPort = *v.ListenPort
		}
	}
	return graph, nil
}
func activeGraphNode(g store.ChainHopGraph, id int64) (store.ChainHopGraphNode, bool) {
	for _, n := range g.Nodes {
		if n.Kind == store.HopGraphKindServer && n.ServerID == id {
			return n, true
		}
	}
	return store.ChainHopGraphNode{}, false
}

func (s *Server) prepareChainGraphs(ctx context.Context, chainID int64, refs []chainHopRef, drafts []orchestrationDraft) ([]store.HopGraphWrite, map[int]store.ChainHopGraphNode, error) {
	if chainID == 0 && len(drafts) == 0 {
		return nil, nil, nil
	}
	existing, err := s.st.ChainHopGraphs(ctx, chainID)
	if err != nil {
		return nil, nil, err
	}
	// Moving a persisted hop must retain its active candidate and original root.
	// Only an actual previous hop can establish this identity; create requests
	// and unrelated graphs cannot adopt arbitrary candidate/root pairs.
	originalHops := map[int]store.ChainHop{}
	if len(existing) > 0 {
		previous, err := s.st.ChainHops(ctx, chainID)
		if err != nil {
			return nil, nil, err
		}
		for _, hop := range previous {
			originalHops[hop.Seq] = hop
		}
	}
	effective := make(map[int]store.ChainHopGraph, len(existing))
	hops := make([]store.ChainHop, len(refs))
	for seq, ref := range refs {
		role := store.HopRoleMiddle
		if seq == len(refs)-1 {
			role = store.HopRoleExit
		} else if seq == 0 {
			role = store.HopRoleEntry
		}
		hops[seq] = store.ChainHop{ServerID: ref.ServerID, Role: role}
		if g, ok := existing[seq]; ok {
			if _, present := activeGraphNode(g, ref.ServerID); present {
				effective[seq] = g
			}
		}
	}
	if drafts != nil && len(drafts) == 0 {
		for seq := range existing {
			drafts = append(drafts, orchestrationDraft{Seq: seq})
		}
	}
	writes := make([]store.HopGraphWrite, 0, len(drafts))
	active := map[int]store.ChainHopGraphNode{}
	seen := map[int]bool{}
	for _, d := range drafts {
		_, existed := existing[d.Seq]
		clear := len(d.Nodes) == 0 && len(d.Edges) == 0 && d.TemplateID == 0 && d.TemplateRevision == 0 && len(d.Overrides) == 0
		if d.Seq < 0 || seen[d.Seq] || (d.Seq >= len(refs) && !(existed && clear)) {
			return nil, nil, fmt.Errorf("编排跳位无效或重复：%d", d.Seq+1)
		}
		seen[d.Seq] = true
		input := d.OrchestrationGraph
		var binding *store.OrchestrationBinding
		var previousOverrides []store.ParamOverride
		if d.TemplateID != 0 {
			t, err := s.st.OrchestrationTemplate(ctx, d.TemplateID)
			if err != nil {
				return nil, nil, err
			}
			if d.TemplateRevision != 0 && d.TemplateRevision != t.Revision {
				return nil, nil, store.ErrTemplateConflict
			}
			input = t.OrchestrationGraph
			if d.Overrides == nil {
				d.Overrides = []store.ParamOverride{}
			}
			binding = &store.OrchestrationBinding{TemplateID: t.ID, Revision: t.Revision, Overrides: d.Overrides}
			for _, g := range existing {
				if g.Binding != nil && g.Binding.TemplateID == t.ID {
					previousOverrides = g.Binding.Overrides
					break
				}
			}
		} else if len(d.Overrides) != 0 || d.TemplateRevision != 0 {
			return nil, nil, errors.New("独立编排不能携带模板参数覆盖")
		}
		g, err := resolvedGraph(chainID, d.Seq, input, d.Overrides, previousOverrides)
		if err != nil {
			return nil, nil, err
		}
		effective[d.Seq] = g
		writes = append(writes, store.HopGraphWrite{Graph: g, Binding: binding})
	}
	if drafts != nil {
		for seq, g := range existing {
			if seen[seq] || g.Binding != nil || g.SyncPending {
				continue
			}
			// Preserve independent graphs on compatible hops, including when a
			// candidate is active. Only clear slots invalidated by the topology.
			if err := validateOrchestrationGraph(ctx, s.st, hops, seq, g, graphRoot(g), nil); err != nil {
				delete(effective, seq)
				writes = append(writes, store.HopGraphWrite{Graph: store.ChainHopGraph{ChainID: chainID, Seq: seq}})
			}
		}
	}
	for _, w := range writes {
		g := w.Graph
		seq := g.Seq
		if seq >= len(refs) {
			continue // Explicitly clearing a graph whose hop was removed.
		}
		expected := refs[seq].ServerID
		// A chain running on a candidate retains its original root.
		if expected != graphRoot(g) {
			for originalSeq, old := range existing {
				if graphRoot(old) != graphRoot(g) {
					continue
				}
				if originalSeq != seq && originalHops[originalSeq].ServerID != expected {
					continue
				}
				if _, ok := activeGraphNode(old, expected); ok {
					expected = graphRoot(old)
					break
				}
			}
		}
		if err := validateOrchestrationGraph(ctx, s.st, hops, seq, g, expected, effective); err != nil {
			return nil, nil, err
		}
		if n, ok := activeGraphNode(g, refs[seq].ServerID); ok {
			active[seq] = n
			refs[seq].Address = n.Address
		}
	}
	for seq, g := range existing {
		if seen[seq] || (g.Binding == nil && !g.SyncPending) {
			continue
		}
		if seq >= len(hops) {
			return nil, nil, fmt.Errorf("第 %d 跳：请先明确清除该跳的关联编排", seq+1)
		}
		if err := validateOrchestrationGraph(ctx, s.st, hops, seq, g, graphRoot(g), effective); err != nil {
			return nil, nil, err
		}
		if !g.SyncPending {
			n, _ := activeGraphNode(g, refs[seq].ServerID)
			active[seq] = n
			refs[seq].Address = n.Address
		}
	}
	return writes, active, nil
}

func applyGraphPorts(active map[int]store.ChainHopGraphNode, count int, entry **int, node *createNodeRequest) {
	if n, ok := active[0]; ok {
		*entry = nil
		if n.ListenPort > 0 {
			*entry = &n.ListenPort
		}
	}
	if n, ok := active[count-1]; ok {
		node.Port = nil
		if n.ListenPort > 0 {
			node.Port = &n.ListenPort
		}
	}
}

func (s *Server) templatePlan(ctx context.Context, r templateRequest) (store.OrchestrationTemplate, []store.TemplatePropagation, []orchestrationImpact, error) {
	t := store.OrchestrationTemplate{ID: r.ID, Name: strings.TrimSpace(r.Name), Revision: r.Revision, OrchestrationGraph: r.OrchestrationGraph}
	if t.ID < 0 || (t.ID == 0 && t.Revision != 0) {
		return t, nil, nil, errors.New("模板标识或版本无效")
	}
	if t.Edges == nil {
		t.Edges = []store.OrchestrationEdgeInput{}
	}
	if t.Name == "" || len(t.Nodes) == 0 {
		return t, nil, nil, errors.New("模板需要名称和根服务器")
	}
	for _, n := range t.Nodes {
		if n.Address != "" {
			return t, nil, nil, errors.New("模板地址须留空，在链路实例中填写")
		}
	}
	g, err := resolvedGraph(0, 0, t.OrchestrationGraph, nil, nil)
	if err != nil {
		return t, nil, nil, err
	}
	expected := int64(0)
	if t.ID != 0 {
		old, err := s.st.OrchestrationTemplate(ctx, t.ID)
		if err != nil {
			return t, nil, nil, err
		}
		if old.Revision != t.Revision {
			return t, nil, nil, store.ErrTemplateConflict
		}
		for _, n := range old.Nodes {
			if n.IsRoot {
				expected = n.ServerID
				break
			}
		}
	}
	if err := validateOrchestrationGraph(ctx, s.st, nil, 0, g, expected, nil); err != nil {
		return t, nil, nil, err
	}
	uses, err := s.st.TemplateUses(ctx, t.ID)
	if err != nil {
		return t, nil, nil, err
	}
	impacts := []orchestrationImpact{}
	var updates []store.TemplatePropagation
	var problems []string
	plannedPorts := make(map[int64][]store.PortOccupant)
	for _, use := range uses {
		chain, err := s.st.ChainByID(ctx, use.ChainID)
		if err != nil {
			return t, nil, nil, err
		}
		update, err := s.planTemplateUse(ctx, chain, use, t.OrchestrationGraph, plannedPorts)
		if err != nil {
			problems = append(problems, fmt.Sprintf("链路「%s」第 %d 跳：%v", chain.Name, use.Seq+1, err))
			continue
		}
		updates = append(updates, update)
		impacts = append(impacts, orchestrationImpact{ChainID: chain.ID, ChainName: chain.Name, Seq: use.Seq, Redeploy: update.Redeploy})
	}
	if len(problems) > 0 {
		return t, nil, nil, errors.New(strings.Join(problems, "；"))
	}
	return t, updates, impacts, nil
}
func (s *Server) planTemplateUse(ctx context.Context, c *store.Chain, use store.TemplateUse, input store.OrchestrationGraph, plannedPorts map[int64][]store.PortOccupant) (store.TemplatePropagation, error) {
	var u store.TemplatePropagation
	if c.DesiredRevisionID != 0 || (c.Status != store.ChainStatusActive && c.Status != store.ChainStatusDegraded) {
		return u, errors.New("链路有在途修订或尚未就绪")
	}
	hops, err := s.st.ChainHops(ctx, c.ID)
	if err != nil {
		return u, err
	}
	graphs, err := s.st.ChainHopGraphs(ctx, c.ID)
	if err != nil {
		return u, err
	}
	old := graphs[use.Seq]
	g, err := resolvedGraph(c.ID, use.Seq, input, use.Overrides, use.Overrides)
	if err != nil {
		return u, err
	}
	if err := validateOrchestrationGraph(ctx, s.st, hops, use.Seq, g, graphRoot(old), graphs); err != nil {
		return u, err
	}
	id := hops[use.Seq].ServerID
	before, _ := activeGraphNode(old, id)
	after, _ := activeGraphNode(g, id)
	rev, err := s.st.PublishedChainRevision(ctx, c.ID)
	if err != nil {
		return u, err
	}
	var vc shared.VirtualConfig
	if err := json.Unmarshal(rev.Snapshot.ServiceConfig, &vc); err != nil {
		return u, err
	}
	portHop := vc.PortHop
	if use.Seq == len(hops)-1 || (use.Seq == 0 && rev.Snapshot.EndpointID != 0) || rev.Snapshot.Hops[use.Seq].Transport == "hy2" {
		portHop = ""
	}
	if err := s.checkCandidateForwardSpans(ctx, g, id, portHop); err != nil {
		return u, err
	}
	if use.BasePort != nil && use.BaseAddress != nil {
		before.ListenPort = *use.BasePort
		before.Address = *use.BaseAddress
	}
	if before.ListenPort != after.ListenPort {
		if err := s.reserveTemplatePort(ctx, c, rev.Snapshot, use.Seq, vc.Protocol, portHop, after, plannedPorts); err != nil {
			return u, err
		}
	}
	return store.TemplatePropagation{Graph: g, BaseAddress: before.Address, BasePort: before.ListenPort, Redeploy: before.ListenPort != after.ListenPort || before.Address != after.Address, PublishedID: c.PublishedRevisionID}, nil
}

// Validate both existing listeners and ports claimed by other instances in
// this proposed update. Compatible entry endpoints may share their listener.
func (s *Server) reserveTemplatePort(ctx context.Context, c *store.Chain, snapshot store.ChainRevisionSnapshot, seq int, protocol, portHop string, node store.ChainHopGraphNode, planned map[int64][]store.PortOccupant) error {
	sharedEntry := seq == 0 && snapshot.EndpointID != 0
	if !sharedEntry && seq == len(snapshot.Hops)-1 && snapshot.ServiceEndpointID != 0 {
		ep, err := s.st.SharedEndpointByID(ctx, snapshot.ServiceEndpointID)
		if err != nil {
			return err
		}
		return matchGraphSharedExitPort(ep, node.ListenPort)
	}
	srv, err := s.st.ServerByID(ctx, node.ServerID)
	if err != nil {
		return err
	}
	port := node.ListenPort
	if port == 0 && snapshot.EndpointID == 0 && portHop != "" {
		port, _, _ = shared.ParsePortHop(portHop)
	}
	if port == 0 {
		return nil
	}
	claim, err := orchestrationListener(srv, protocol, portHop, port, sharedEntry)
	if err != nil {
		return err
	}
	if err := s.checkOrchestrationListener(ctx, node.ServerID, c.ID, claim, planned[node.ServerID]); err != nil {
		return err
	}
	claim.ChainID, claim.RefName = c.ID, c.Name
	planned[node.ServerID] = append(planned[node.ServerID], claim)
	return nil
}

func templateError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrTemplateConflict) {
		status = http.StatusConflict
	} else if errors.Is(err, store.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}
func (s *Server) handleListOrchestrationTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.st.OrchestrationTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

// The proof is a deterministic concurrency checksum, not an authorization token.
// The existing authenticated RPC and idempotency middleware still own authorization.
func templatePreviewProof(t store.OrchestrationTemplate, operation string, impacts []orchestrationImpact) string {
	raw, _ := json.Marshal(struct {
		ID        int64
		Revision  int
		Name      string
		Graph     store.OrchestrationGraph
		Operation string
		Impacts   []orchestrationImpact
	}{t.ID, t.Revision, t.Name, t.OrchestrationGraph, operation, impacts})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Deletion only detaches the already materialized graphs. It deliberately does
// not run update validation: applying chains and invalid source graphs may detach.
func (s *Server) templateDeletePlan(ctx context.Context, id int64) (store.OrchestrationTemplate, []store.TemplateUse, []orchestrationImpact, error) {
	t, err := s.st.OrchestrationTemplate(ctx, id)
	if err != nil {
		return t, nil, nil, err
	}
	uses, err := s.st.TemplateUses(ctx, id)
	if err != nil {
		return t, nil, nil, err
	}
	impacts := []orchestrationImpact{}
	for _, use := range uses {
		chain, err := s.st.ChainByID(ctx, use.ChainID)
		if err != nil {
			return t, nil, nil, err
		}
		impacts = append(impacts, orchestrationImpact{ChainID: use.ChainID, ChainName: chain.Name, Seq: use.Seq})
	}
	return t, uses, impacts, nil
}

func (s *Server) handlePreviewOrchestrationTemplate(w http.ResponseWriter, r *http.Request) {
	s.templateChange(w, r, true)
}
func (s *Server) handleSaveOrchestrationTemplate(w http.ResponseWriter, r *http.Request) {
	s.templateChange(w, r, false)
}
func (s *Server) templateChange(w http.ResponseWriter, r *http.Request, preview bool) {
	var req templateRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.orchestrationMu.Lock()
	defer s.orchestrationMu.Unlock()
	if req.Operation != "" && req.Operation != "delete" {
		writeError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	if req.Operation == "delete" {
		if !preview {
			writeError(w, http.StatusBadRequest, "use delete endpoint")
			return
		}
		t, _, impacts, err := s.templateDeletePlan(r.Context(), req.ID)
		if err != nil {
			templateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revision": t.Revision, "affected_chains": impacts, "preview_proof": templatePreviewProof(t, "delete", impacts)})
		return
	}
	t, updates, impacts, err := s.templatePlan(r.Context(), req)
	if err != nil {
		templateError(w, err)
		return
	}
	if preview {
		writeJSON(w, http.StatusOK, map[string]any{"revision": t.Revision, "affected_chains": impacts, "preview_proof": templatePreviewProof(t, "save", impacts)})
		return
	}
	if t.ID != 0 && req.PreviewProof != templatePreviewProof(t, "save", impacts) {
		writeError(w, http.StatusConflict, "模板影响已变化或尚未确认，请重新预览受影响链路后确认")
		return
	}
	t, err = s.st.SaveOrchestrationTemplate(r.Context(), t, updates)
	if err != nil {
		templateError(w, err)
		return
	}
	for _, u := range updates {
		if s.orchestrator != nil {
			s.orchestrator.EnsureChainWorker(u.Graph.ChainID)
			s.orchestrator.ResumeChain(u.Graph.ChainID)
		}
	}
	writeJSON(w, http.StatusOK, t)
}
func (s *Server) handleDeleteOrchestrationTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateRequest
	if err := readJSON(r, &req); err != nil || req.ID <= 0 {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.orchestrationMu.Lock()
	defer s.orchestrationMu.Unlock()
	t, uses, impacts, err := s.templateDeletePlan(r.Context(), req.ID)
	if err != nil {
		templateError(w, err)
		return
	}
	if req.Revision != t.Revision || req.PreviewProof != templatePreviewProof(t, "delete", impacts) {
		writeError(w, http.StatusConflict, "模板或分离范围已变化或尚未确认，请重新预览关联链路后确认")
		return
	}
	if err := s.st.DeleteOrchestrationTemplate(r.Context(), req.ID, store.TemplateDeleteExpectation{Revision: t.Revision, Uses: uses}); err != nil {
		templateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

func plannedChainRevision(chain *store.Chain, current *store.ChainRevision, desired store.ChainRevisionSnapshot, plan dispatch.RevisionPlan) *store.ChainRevision {
	edit := &store.RevisionEdit{ExpectedDesiredID: chain.DesiredRevisionID, ExpectedPublishedID: chain.PublishedRevisionID}
	for _, piece := range plan.Apply {
		edit.Tasks = append(edit.Tasks, store.ChainRevisionTask{TaskKey: piece.Key, Phase: "apply", Action: "apply", Kind: piece.Kind, HopID: piece.HopID, ServerID: piece.ServerID})
	}
	for _, piece := range plan.Cleanup {
		edit.Tasks = append(edit.Tasks, store.ChainRevisionTask{TaskKey: "cleanup/" + piece.Key, Phase: "cleanup", Action: "remove", Kind: piece.Kind, HopID: piece.HopID, ServerID: piece.ServerID})
	}
	if desired.ServiceExternal == nil && current.Snapshot.ServiceServerID != desired.ServiceServerID {
		edit.Tasks = append(edit.Tasks, store.ChainRevisionTask{TaskKey: fmt.Sprintf("cleanup/service/%d", desired.ServiceNodeID), Phase: "cleanup", Action: "remove", Kind: dispatch.RevisionPieceService, HopID: desired.ServiceNodeID, ServerID: current.Snapshot.ServiceServerID})
	}
	return &store.ChainRevision{ChainID: chain.ID, Snapshot: desired, Edit: edit}
}
