package store

import (
	"context"
	"path/filepath"
	"testing"
)

func graphFixture(t *testing.T) (*Store, int64, []int64) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var ids []int64
	for i, alias := range []string{"A1", "A2", "B1"} {
		id, err := st.CreateServer(ctx, ServerDraft{
			Alias: alias, Address: "203.0.113." + string(rune('1'+i)),
			BootstrapToken: "tok-" + alias, MachineType: MachineTypeDirect, CountryCode: "JP",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	chainID, err := st.InsertChain(ctx, "GraphChain")
	if err != nil {
		t.Fatal(err)
	}
	return st, chainID, ids
}

func TestChainHopGraphReplaceReadAndDelete(t *testing.T) {
	ctx := context.Background()
	st, chainID, ids := graphFixture(t)
	a1, a2, b1 := ids[0], ids[1], ids[2]
	_ = b1

	nodes := []ChainHopGraphNode{
		{Kind: HopGraphKindServer, ServerID: a1, Address: "203.0.113.1", ListenPort: 443, IsRoot: true},
		{Kind: HopGraphKindTrigger, Subject: TriggerSubjectSelf, Action: TriggerActionBanSuspected, Priority: 0},
		{Kind: HopGraphKindServer, ServerID: a2, Address: "203.0.113.2", ListenPort: 8443},
		{Kind: HopGraphKindTrigger, Subject: TriggerSubjectRoot, Action: TriggerActionRecovered, Priority: 1},
	}
	edges := []ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}, {FromNode: 3, ToNode: 0}}
	graph, err := st.ReplaceChainHopGraph(ctx, chainID, 0, nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if graph == nil || len(graph.Nodes) != 4 || len(graph.Edges) != 3 {
		t.Fatalf("unexpected graph: %+v", graph)
	}
	if !graph.Nodes[0].IsRoot || graph.Nodes[0].ServerID != a1 || graph.Nodes[0].ListenPort != 443 {
		t.Fatalf("unexpected root node: %+v", graph.Nodes[0])
	}
	if graph.Nodes[3].Kind != HopGraphKindTrigger || graph.Nodes[3].Subject != TriggerSubjectRoot ||
		graph.Nodes[3].Action != TriggerActionRecovered || graph.Nodes[3].Priority != 1 {
		t.Fatalf("unexpected recovery trigger: %+v", graph.Nodes[3])
	}
	for i, edge := range graph.Edges {
		if edge.FromNode != graph.Nodes[edges[i].FromNode].ID || edge.ToNode != graph.Nodes[edges[i].ToNode].ID {
			t.Fatalf("edge %d not mapped to node ids: %+v", i, edge)
		}
	}

	graphs, err := st.ChainHopGraphs(ctx, chainID)
	if err != nil || len(graphs) != 1 {
		t.Fatalf("ChainHopGraphs: %v %+v", err, graphs)
	}

	// 整图替换后旧节点/边不残留。
	replaced := []ChainHopGraphNode{{Kind: HopGraphKindServer, ServerID: a1, IsRoot: true}}
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 0, replaced, nil); err != nil {
		t.Fatal(err)
	}
	graphs, _ = st.ChainHopGraphs(ctx, chainID)
	if len(graphs[0].Nodes) != 1 || len(graphs[0].Edges) != 0 {
		t.Fatalf("replace left rows: %+v", graphs[0])
	}

	if err := st.DeleteChainHopGraph(ctx, chainID, 0); err != nil {
		t.Fatal(err)
	}
	if g, err := st.ChainHopGraph(ctx, chainID, 0); err != nil || g != nil {
		t.Fatalf("graph not deleted: %+v %v", g, err)
	}

	// 空节点 = 删除。
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 1, nodes, edges); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.ChainHopGraph(ctx, chainID, 1); g != nil {
		t.Fatalf("empty replace should delete graph: %+v", g)
	}
}

func TestChainHopGraphCascades(t *testing.T) {
	ctx := context.Background()
	st, chainID, ids := graphFixture(t)
	a1, a2 := ids[0], ids[1]
	nodes := []ChainHopGraphNode{
		{Kind: HopGraphKindServer, ServerID: a1, IsRoot: true},
		{Kind: HopGraphKindTrigger, Subject: TriggerSubjectSelf, Action: TriggerActionBanBanned},
		{Kind: HopGraphKindServer, ServerID: a2},
	}
	edges := []ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 0, nodes, edges); err != nil {
		t.Fatal(err)
	}

	refs, err := st.ClearChainHopGraphsForServer(ctx, a2)
	if err != nil || len(refs) != 1 || refs[0].ChainID != chainID || refs[0].Seq != 0 || refs[0].RootServerID != a1 {
		t.Fatalf("ClearChainHopGraphsForServer: %v %+v", err, refs)
	}
	if g, _ := st.ChainHopGraph(ctx, chainID, 0); g != nil {
		t.Fatalf("graph should be cleared: %+v", g)
	}

	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 0, nodes, edges); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteChain(ctx, chainID); err != nil {
		t.Fatal(err)
	}
	graphs, err := st.ChainHopGraphs(ctx, chainID)
	if err != nil || len(graphs) != 0 {
		t.Fatalf("chain delete left graphs: %v %+v", err, graphs)
	}
}

func TestReconcileChainHopGraphsTx(t *testing.T) {
	ctx := context.Background()
	st, chainID, ids := graphFixture(t)
	a1, a2, b1 := ids[0], ids[1], ids[2]

	// seq0 根 A1，包含 A2；seq1 根 B1。
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 0, []ChainHopGraphNode{
		{Kind: HopGraphKindServer, ServerID: a1, Address: "", ListenPort: 443, IsRoot: true},
		{Kind: HopGraphKindTrigger, Subject: TriggerSubjectSelf, Action: TriggerActionBanBanned},
		{Kind: HopGraphKindServer, ServerID: a2},
	}, []ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 1, []ChainHopGraphNode{
		{Kind: HopGraphKindServer, ServerID: b1, IsRoot: true},
	}, nil); err != nil {
		t.Fatal(err)
	}

	// 根与跳服务器一致：保留并同步地址/端口。
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileChainHopGraphsTx(tx, chainID, []ChainRevisionHop{
		{ServerID: a1, Address: "203.0.113.9", ForwardPort: 8443},
		{ServerID: b1, ForwardPort: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	g, _ := st.ChainHopGraph(ctx, chainID, 0)
	if g == nil || g.Nodes[0].Address != "203.0.113.9" || g.Nodes[0].ListenPort != 8443 {
		t.Fatalf("root not synced: %+v", g)
	}

	// 跳服务器变更：清图。
	tx, _ = st.db.Begin()
	if err := reconcileChainHopGraphsTx(tx, chainID, []ChainRevisionHop{
		{ServerID: a2},
		{ServerID: b1},
	}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	if g, _ := st.ChainHopGraph(ctx, chainID, 0); g != nil {
		t.Fatalf("changed hop should clear graph: %+v", g)
	}

	// 跳数减少：seq1 图删除。
	tx, _ = st.db.Begin()
	if err := reconcileChainHopGraphsTx(tx, chainID, []ChainRevisionHop{{ServerID: a1}}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	if g, _ := st.ChainHopGraph(ctx, chainID, 1); g != nil {
		t.Fatalf("removed hop should clear graph: %+v", g)
	}
}

func TestChainDeletionReferenceQueries(t *testing.T) {
	ctx := context.Background()
	st, chainID, ids := graphFixture(t)
	a1, a2 := ids[0], ids[1]
	if _, err := st.InsertChainHop(ctx, chainID, 0, a1, HopRoleEntry, 0, 443, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceChainHopGraph(ctx, chainID, 0, []ChainHopGraphNode{
		{Kind: HopGraphKindServer, ServerID: a1, IsRoot: true},
		{Kind: HopGraphKindTrigger, Subject: TriggerSubjectSelf, Action: TriggerActionBanBanned},
		{Kind: HopGraphKindServer, ServerID: a2},
	}, []ChainHopGraphEdge{{FromNode: 0, ToNode: 1}, {FromNode: 1, ToNode: 2}}); err != nil {
		t.Fatal(err)
	}
	working, err := st.ChainsWithWorkingHopOnServer(ctx, a1)
	if err != nil || len(working) != 1 || working[0].ID != chainID {
		t.Fatalf("working refs: %v %+v", err, working)
	}
	graphOnly, err := st.ChainsWithWorkingHopOnServer(ctx, a2)
	if err != nil || len(graphOnly) != 0 {
		t.Fatalf("graph-only server must not be a working hop: %v %+v", err, graphOnly)
	}
	refs, err := st.ChainsWithGraphNodeOnServer(ctx, a2)
	if err != nil || len(refs) != 1 || refs[0].ChainID != chainID || refs[0].Seq != 0 || refs[0].RootServerID != a1 {
		t.Fatalf("graph refs: %v %+v", err, refs)
	}
	if _, err := st.ClearChainHopGraphsForServer(ctx, a2); err != nil {
		t.Fatal(err)
	}
	refs, _ = st.ChainsWithGraphNodeOnServer(ctx, a2)
	if len(refs) != 0 {
		t.Fatalf("graph should be cleared: %+v", refs)
	}
}

