package sub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestSubStatusUsesEffectiveGroupAssignments(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, _ := newTestEndpointChain(t, st, "group-a")
	chainB, _ := newTestEndpointChain(t, st, "group-b")
	outsideChain, _ := newTestEndpointChain(t, st, "outside")
	groupA, err := st.CreateLinkGroup(ctx, "group-a", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	groupAB, err := st.CreateLinkGroup(ctx, "group-ab", []int64{chainA, chainB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "status-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "members-a", []int64{userID}, []int64{groupA}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "members-ab", []int64{userID}, []int64{groupAB}); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	for _, tc := range []struct {
		name   string
		direct []int64
	}{
		{name: "no direct assignments"},
		{name: "partial direct assignments", direct: []int64{chainA}},
		{name: "unrelated direct assignment", direct: []int64{outsideChain}},
		{name: "all direct assignments", direct: []int64{chainA, chainB, outsideChain}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := st.SetUserChains(ctx, userID, tc.direct); err != nil {
				t.Fatal(err)
			}
			items, warnings, err := server.itemsForUser(ctx, user)
			if err != nil || len(warnings) != 0 || len(items) != 2 {
				t.Fatalf("subscription items=%+v warnings=%v err=%v", items, warnings, err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/sub/status-token/status", nil)
			req.SetPathValue("token", "status-token")
			server.HandleSubStatus(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status code=%d body=%s", rec.Code, rec.Body.String())
			}
			var status SubLinkStatusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			var labels []string
			for _, link := range status.Links {
				labels = append(labels, link.Label)
				if len(link.Hops) != 2 {
					t.Fatalf("hops=%+v, want entry and exit even without samples", link.Hops)
				}
				for _, hop := range link.Hops {
					if hop.Samples == nil || len(hop.Samples) != 0 {
						t.Fatalf("samples=%+v, want empty array", hop.Samples)
					}
				}
			}
			slices.Sort(labels)
			if !slices.Equal(labels, []string{"group-a", "group-b"}) {
				t.Fatalf("status links=%v, want group-a and group-b exactly once", labels)
			}
		})
	}

	// Direct nodes retained on a grouped user must be hidden just as in the subscription.
	directServer, err := st.CreateServer(ctx, store.ServerDraft{Alias: "direct", Address: "direct.example.com", BootstrapToken: "tok-direct", MachineType: store.MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	directNode, err := st.InsertNode(ctx, "direct-node", directServer, shared.ProtocolVLESS, nil, json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeActive(ctx, directNode, []byte(`{"port":443}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{directNode}); err != nil {
		t.Fatal(err)
	}
	links, err := server.subscriptionLinkStatus(httptest.NewRequest("GET", "/", nil), user)
	if err != nil || len(links) != 2 {
		t.Fatalf("grouped user with direct node: links=%+v err=%v, want only 2 grouped chains", links, err)
	}

	// Users outside groups retain their directly assigned nodes and chains.
	outsideUserID, err := st.InsertUser(ctx, "outside", "00000000-0000-0000-0000-0000000000bb", "outside-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, outsideUserID, []int64{outsideChain}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, outsideUserID, []int64{directNode}); err != nil {
		t.Fatal(err)
	}
	outsideUser, err := st.UserByID(ctx, outsideUserID)
	if err != nil {
		t.Fatal(err)
	}
	links, err = server.subscriptionLinkStatus(httptest.NewRequest("GET", "/", nil), outsideUser)
	if err != nil || len(links) != 2 {
		t.Fatalf("ungrouped user: links=%+v err=%v, want direct node and assigned chain", links, err)
	}
	labels := []string{links[0].Label, links[1].Label}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"direct-node", "outside"}) {
		t.Fatalf("ungrouped user links=%v, want direct-node and outside", labels)
	}
}
