package dispatch

import (
	"encoding/json"
	"testing"
)

func topology(revision int64, service string, hops ...RevisionHopSpec) RevisionTopology {
	return RevisionTopology{RevisionID: revision, ServiceID: 9, Service: json.RawMessage(service), Hops: hops}
}

func hop(id, server int64) RevisionHopSpec {
	return RevisionHopSpec{HopID: id, ServerID: server, Transport: "direct"}
}

func pieceKeys(pieces []RevisionPiece) []string {
	out := make([]string, len(pieces))
	for i := range pieces {
		out[i] = pieces[i].Key
	}
	return out
}

func TestPlanRevisionRemovesMiddleWithoutBreakingOrder(t *testing.T) {
	current := topology(1, `{"protocol":"vless"}`, hop(1, 101), hop(2, 102), hop(3, 103))
	desired := topology(2, `{"protocol":"vless"}`, hop(1, 101), hop(3, 103))
	plan, err := PlanRevision(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pieceKeys(plan.Apply), []string{"forward/1"}; !equalStrings(got, want) {
		t.Fatalf("apply = %v, want %v", got, want)
	}
	if got, want := pieceKeys(plan.Cleanup), []string{"forward/2"}; !equalStrings(got, want) {
		t.Fatalf("cleanup = %v, want %v", got, want)
	}
}

func TestPlanRevisionPropagatesExitChangeToEntry(t *testing.T) {
	current := topology(1, `{"protocol":"vless"}`, hop(1, 101), hop(2, 102), hop(3, 103))
	desired := topology(2, `{"protocol":"socks"}`, hop(1, 101), hop(2, 102), hop(3, 103))
	plan, err := PlanRevision(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pieceKeys(plan.Apply), []string{"service/9", "forward/2", "forward/1"}; !equalStrings(got, want) {
		t.Fatalf("apply = %v, want %v", got, want)
	}
}

func TestPlanRevisionReusesUnchangedTopology(t *testing.T) {
	current := topology(1, `{"protocol":"vless"}`, hop(1, 101), hop(2, 102))
	desired := topology(2, `{"protocol":"vless"}`, hop(1, 101), hop(2, 102))
	plan, err := PlanRevision(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Apply) != 0 || len(plan.Cleanup) != 0 || len(plan.Reuse) != 2 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestPlanRevisionValidatesTopology(t *testing.T) {
	_, err := PlanRevision(RevisionTopology{}, topology(1, `{}`, hop(1, 101), hop(2, 101)))
	if err == nil {
		t.Fatal("expected duplicate server error")
	}
}

func TestPlanRevisionDirectSharedOmitsService(t *testing.T) {
	desired := topology(2, `{"protocol":"vless"}`, hop(10, 20))
	desired.DirectShared = true
	plan, err := PlanRevision(RevisionTopology{}, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Apply) != 0 || len(plan.Reuse) != 0 || len(plan.Cleanup) != 0 {
		t.Fatalf("direct shared plan contains service work: %+v", plan)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestValidateTopologyHy2Transport 验证末段 hy2 transport 白名单（P4 入口终结）。
func TestValidateTopologyHy2Transport(t *testing.T) {
	topo := RevisionTopology{RevisionID: 1, ServiceID: 9, Hops: []RevisionHopSpec{
		{HopID: 1, ServerID: 1, Transport: "hy2"},
		{HopID: 2, ServerID: 2},
	}}
	if err := validateTopology(topo); err != nil {
		t.Fatalf("hy2 transport 应合法: %v", err)
	}
	topo.Hops[0].Transport = "bogus"
	if err := validateTopology(topo); err == nil {
		t.Fatal("未知 transport 应拒绝")
	}
}
