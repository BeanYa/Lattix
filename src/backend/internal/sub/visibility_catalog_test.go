package sub

import (
	"context"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/store"
)

func TestPublishedChainIsAvailableWithoutAssignment(t *testing.T) {
	st, _ := setupChainFixture(t, true)
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "new user", "new-user-uuid", "new-user-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil)
	items, _, err := s.itemsForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].node.Name != "JP直连Test" || items[0].credential == "" {
		t.Fatalf("unassigned user should receive the published chain with a credential: %+v", items)
	}
	data, err := s.linkStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	links, err := s.subscriptionLinkStatus(httptest.NewRequest("GET", "/", nil), user, data)
	if err != nil || len(links) != 1 || links[0].Label != items[0].node.Name {
		t.Fatalf("subscription status = %+v, %v", links, err)
	}
	chain, err := st.ChainByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].credential != store.GroupAccessUUID(user.UUID, chain.ID) {
		t.Fatal("published credential must match the derived endpoint credential")
	}
}
