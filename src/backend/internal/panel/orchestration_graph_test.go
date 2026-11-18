package panel

import (
	"context"
	"path/filepath"
	"testing"

	"lattix/backend/internal/store"
)

type graphEnv struct {
	st    *store.Store
	chain *store.Chain
	hops  []store.ChainHop
	ids   map[string]int64
}

func newGraphEnv(t *testing.T) *graphEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-val.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ids := map[string]int64{}
	for _, alias := range []string{"A1", "A2", "M1", "M2", "B1", "B2", "X1"} {
		id, err := st.CreateServer(ctx, store.ServerDraft{
			Alias: alias, Address: "203.0.113." + alias[1:2],
			BootstrapToken: "tok-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "JP",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[alias] = id
	}
	chainID, err := st.InsertChain(ctx, "GraphChain")
	if err != nil {
		t.Fatal(err)
	}
	for seq, spec := range []struct {
		alias string
		role  string
		port  int
	}{
		{"A1", store.HopRoleEntry, 443},
		{"M1", store.HopRoleMiddle, 0},
		{"B1", store.HopRoleExit, 8443},
	} {
		if _, err := st.InsertChainHop(ctx, chainID, seq, ids[spec.alias], spec.role, 0, spec.port, ""); err != nil {
			t.Fatal(err)
		}
	}
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	hops, err := st.ChainHops(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	return &graphEnv{st: st, chain: chain, hops: hops, ids: ids}
}

func gn(serverID int64, root bool) store.ChainHopGraphNode {
	return store.ChainHopGraphNode{Kind: store.HopGraphKindServer, ServerID: serverID, IsRoot: root}
}

func gt(subject, action string, priority int) store.ChainHopGraphNode {
	return store.ChainHopGraphNode{Kind: store.HopGraphKindTrigger, Subject: subject, Action: action, Priority: priority}
}

func ge(from, to int) store.ChainHopGraphEdge {
	return store.ChainHopGraphEdge{FromNode: int64(from), ToNode: int64(to)}
}

func (e *graphEnv) validate(t *testing.T, seq int, nodes []store.ChainHopGraphNode, edges []store.ChainHopGraphEdge, hops ...[]store.ChainHop) error {
	t.Helper()
	useHops := e.hops
	if len(hops) > 0 {
		useHops = hops[0]
	}
	return validateHopOrchestration(context.Background(), e.st, e.chain, useHops, seq,
		store.ChainHopGraph{ChainID: e.chain.ID, Seq: seq, Nodes: nodes, Edges: edges})
}

func TestValidateHopOrchestrationAccepts(t *testing.T) {
	env := newGraphEnv(t)
	a1, a2, m1, m2, b1 := env.ids["A1"], env.ids["A2"], env.ids["M1"], env.ids["M2"], env.ids["B1"]

	t.Run("basic failover", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{gn(a1, true), gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0), gn(a2, false)}
		if err := env.validate(t, 0, nodes, []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("suspected and banned share target", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanSuspected, 0),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gn(a2, false),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(0, 2), ge(1, 3), ge(2, 3)}
		if err := env.validate(t, 0, nodes, edges); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("recovery trigger before root", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gn(a2, false),
			gt(store.TriggerSubjectRoot, store.TriggerActionRecovered, 1),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2), ge(3, 0)}
		if err := env.validate(t, 0, nodes, edges); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("middle prev and next timeout", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(m1, true),
			gt(store.TriggerSubjectPrev, store.TriggerActionSegmentTimeout, 0),
			gt(store.TriggerSubjectNext, store.TriggerActionSegmentTimeout, 0),
			gn(m2, false),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(0, 2), ge(1, 3), ge(2, 3)}
		if err := env.validate(t, 1, nodes, edges); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exit hop", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{gn(b1, true), gt(store.TriggerSubjectPrev, store.TriggerActionSegmentTimeout, 0), gn(env.ids["B2"], false)}
		if err := env.validate(t, 2, nodes, []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("empty deletes", func(t *testing.T) {
		if err := env.validate(t, 0, nil, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestValidateHopOrchestrationRejects(t *testing.T) {
	env := newGraphEnv(t)
	a1, a2, m1, b1, x1 := env.ids["A1"], env.ids["A2"], env.ids["M1"], env.ids["B1"], env.ids["X1"]

	base := func() ([]store.ChainHopGraphNode, []store.ChainHopGraphEdge) {
		return []store.ChainHopGraphNode{gn(a1, true), gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0), gn(a2, false)},
			[]store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}
	}

	t.Run("root mismatch", func(t *testing.T) {
		nodes, edges := base()
		nodes[0].ServerID = a2
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected root mismatch")
		}
	})

	t.Run("recovery with owner", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{gn(a1, true), gt(store.TriggerSubjectRoot, store.TriggerActionRecovered, 1), gn(a2, false)}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(1, 0), ge(0, 2)}
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected recovery owner rejection")
		}
	})

	t.Run("recovery target not root", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectRoot, store.TriggerActionRecovered, 1),
			gn(a2, false),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
		}
		edges := []store.ChainHopGraphEdge{ge(1, 2), ge(0, 3), ge(3, 2)}
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected recovery target rejection")
		}
	})

	t.Run("non-root cycle", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gn(a2, false),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanSuspected, 0),
			gn(x1, false),
			gt(store.TriggerSubjectRoot, store.TriggerActionRecovered, 1),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2), ge(2, 3), ge(3, 4), ge(4, 3)}
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected cycle rejection")
		}
	})

	t.Run("trigger with two targets", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gn(a2, false),
			gn(x1, false),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanSuspected, 0),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2), ge(1, 3), ge(0, 4), ge(4, 3)}
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected multi-target rejection")
		}
	})

	t.Run("duplicate condition", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{
			gn(a1, true),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0),
			gn(a2, false),
		}
		edges := []store.ChainHopGraphEdge{ge(0, 1), ge(0, 2), ge(1, 3), ge(2, 3)}
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected duplicate condition rejection")
		}
	})

	t.Run("priority out of range", func(t *testing.T) {
		nodes, edges := base()
		nodes[1].Priority = 10000
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected priority rejection")
		}
	})

	t.Run("cross hop reuse", func(t *testing.T) {
		nodes := []store.ChainHopGraphNode{gn(a1, true), gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0), gn(m1, false)}
		if err := env.validate(t, 0, nodes, []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}); err == nil {
			t.Fatal("expected cross-hop rejection")
		}
	})

	t.Run("current server missing after failover", func(t *testing.T) {
		ctx := context.Background()
		if _, err := env.st.ReplaceChainHopGraph(ctx, env.chain.ID, 0, []store.ChainHopGraphNode{
			gn(a1, true), gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0), gn(a2, false),
		}, []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}); err != nil {
			t.Fatal(err)
		}
		hops := append([]store.ChainHop(nil), env.hops...)
		hops[0].ServerID = a2
		nodes := []store.ChainHopGraphNode{gn(a1, true), gt(store.TriggerSubjectSelf, store.TriggerActionBanBanned, 0), gn(x1, false)}
		if err := env.validate(t, 0, nodes, []store.ChainHopGraphEdge{ge(0, 1), ge(1, 2)}, hops); err == nil {
			t.Fatal("expected current-server-missing rejection")
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		nodes, edges := base()
		nodes[0].Address = "198.51.100.99"
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected invalid address rejection")
		}
	})

	t.Run("bad trigger combo", func(t *testing.T) {
		nodes, edges := base()
		nodes[1].Action = store.TriggerActionRecovered
		if err := env.validate(t, 0, nodes, edges); err == nil {
			t.Fatal("expected trigger combo rejection")
		}
	})

	_ = b1
}
