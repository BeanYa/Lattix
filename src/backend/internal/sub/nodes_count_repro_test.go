package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

func TestSubInfoNodesCountTracksHiddenItemsAndNewChains(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// 面板链路池：5 条共享入口链路。
	chainIDs := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		chainID, _ := newTestEndpointChain(t, st, fmt.Sprintf("pool-%d", i))
		chainIDs = append(chainIDs, chainID)
	}
	// 外部订阅：3 个节点，直接分配给 u1。
	subID, _ := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场Y", URL: "https://sub.example.com/y", UpdateIntervalHours: 24,
	})
	extChains := make([]store.ExternalChain, 0, 3)
	for i := 0; i < 3; i++ {
		cfg, _ := json.Marshal(extsub.Node{
			Name: fmt.Sprintf("ext-%d", i), Type: "vless",
			Server: fmt.Sprintf("ext-%d.example.com", i), Port: 443,
			Extra: map[string]any{"id": fmt.Sprintf("x-uuid-%d", i)},
		})
		extChains = append(extChains, store.ExternalChain{
			SubscriptionID: subID, Name: fmt.Sprintf("ext-%d", i), Protocol: "vless",
			Server: fmt.Sprintf("ext-%d.example.com", i), Port: 443,
			Config: cfg, ConfigSHA256: fmt.Sprintf("sha-%d", i),
		})
	}
	if _, err := st.ReplaceExternalChains(ctx, subID, extChains); err != nil {
		t.Fatal(err)
	}

	userID, err := st.InsertUser(ctx, "user", "catalog-user-uuid", "catalog-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil)
	req := httptest.NewRequest("GET", "https://panel.example/api/sub/catalog-token/info", nil)
	req.SetPathValue("token", "catalog-token")
	checkCount := func(want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.HandleSubInfo(rec, req)
		var response SubInfoResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.NodesCount != want {
			t.Fatalf("nodes_count = %d, want %d", response.NodesCount, want)
		}
	}
	checkCount(8)
	hidden := []string{subscriptionItemID("chain", chainIDs[0]), subscriptionItemID("external", subID)}
	if _, err := s.UpdateSubscriptionVisibility(req, user, hidden); err != nil {
		t.Fatal(err)
	}
	if !s.processQueuedSubscriptionJobs(ctx) {
		t.Fatal("visibility publication did not run")
	}
	checkCount(4)
	newTestEndpointChain(t, st, "new chain")
	checkCount(5)
}
