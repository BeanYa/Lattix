package store

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestStaleOrchestrationBindingRollsBackChainDeployment(t *testing.T) {
	ctx := t.Context()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "server", Address: "s.example.com", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := st.SaveOrchestrationTemplate(ctx, OrchestrationTemplate{Name: "shared"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph := ChainHopGraph{Seq: 0, Nodes: []ChainHopGraphNode{{Kind: HopGraphKindServer, ServerID: serverID, IsRoot: true, ListenPort: 443}}}
	valid := HopGraphWrite{Graph: graph, Binding: &OrchestrationBinding{TemplateID: tpl.ID, Revision: tpl.Revision}}
	stale := HopGraphWrite{Graph: graph, Binding: &OrchestrationBinding{TemplateID: tpl.ID, Revision: tpl.Revision + 1}}
	input := InitialChainDeployment{
		Name: "bound", ServiceServerID: serverID, ServiceProtocol: "trojan", ServiceConfig: json.RawMessage(`{"protocol":"trojan"}`),
		Hops: []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}}, GraphWrites: []HopGraphWrite{stale},
	}
	failed, err := st.CreateInitialChainDeployment(ctx, input)
	if !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("initial stale binding: %v", err)
	}
	for _, table := range []string{"chains", "nodes", "chain_hops", "chain_hop_identities", "chain_revisions", "chain_revision_tasks", "chain_hop_graphs", "orchestration_bindings"} {
		var count int
		if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stale create left rows in %s for chain %d: %d, %v", table, failed.ChainID, count, err)
		}
	}
	input.GraphWrites = []HopGraphWrite{valid}
	created, err := st.CreateInitialChainDeployment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, created.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	published, err := st.PublishedChainRevision(ctx, created.ChainID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := published.Snapshot
	snapshot.Name = "must not be written"
	snapshot.Hops[0].ForwardPort = 1443
	stale.Graph.Nodes = []ChainHopGraphNode{{Kind: HopGraphKindServer, ServerID: serverID, IsRoot: true, ListenPort: 1443}}
	edit := &ChainRevision{ChainID: created.ChainID, Snapshot: snapshot, Edit: &RevisionEdit{
		ExpectedPublishedID: published.ID, GraphWrites: []HopGraphWrite{stale},
		Tasks: []ChainRevisionTask{{TaskKey: "service/1", Phase: "apply", Action: "apply", Kind: RevisionPieceService, HopID: created.NodeID, ServerID: serverID}},
	}}
	if err := st.ReplaceWorkingChainTopology(ctx, edit, input.ServiceProtocol, nil, false, ChainStatusActive); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("edit stale binding: %v", err)
	}
	chain, err := st.ChainByID(ctx, created.ChainID)
	if err != nil || chain.DesiredRevisionID != 0 || chain.PublishedRevisionID != published.ID || chain.Name != input.Name {
		t.Fatalf("stale binding changed chain: %+v, %v", chain, err)
	}
	if _, err := st.ChainRevisionByID(ctx, edit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed revision remained: %v", err)
	}
	hops, err := st.ChainHops(ctx, created.ChainID)
	if err != nil || len(hops) != 1 || hops[0].ForwardPort != 0 {
		t.Fatalf("stale binding changed topology: %+v, %v", hops, err)
	}
	stored, err := st.ChainHopGraph(ctx, created.ChainID, 0)
	if err != nil || stored == nil || stored.Binding == nil || stored.Binding.Revision != tpl.Revision || stored.Nodes[0].ListenPort != 443 {
		t.Fatalf("stale edit replaced graph or binding: %+v, %v", stored, err)
	}
}
