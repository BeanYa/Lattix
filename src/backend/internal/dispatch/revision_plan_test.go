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
