package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogMigrationPreservesPreviouslyIssuedCredentialsAndModes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	chainID, endpointID := newTestEndpointChain(t, st, "legacy-chain")
	sourceID, err := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: "legacy source", URL: "https://source.example/sub", UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	groupID, err := st.CreateLinkGroup(ctx, "legacy", []int64{chainID},
		[]LinkGroupExternalSubscription{{SubscriptionID: sourceID, Mode: ExtSubModeMerge}})
	if err != nil {
		t.Fatal(err)
	}
	const userUUID = "00000000-0000-0000-0000-000000000011"
	userID, err := st.InsertUser(ctx, "legacy user", userUUID, "legacy-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "legacy users", []int64{userID}, []int64{groupID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "PRAGMA user_version = 29"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateLinkGroup(ctx, groupID, "legacy", nil, nil); err != nil {
		t.Fatal(err)
	}
	assignments, err := st.CatalogUserChainAssignments(ctx, userID)
	if err != nil || len(assignments) != 1 || assignments[0].AccessUUID != GroupAccessUUID(userUUID, chainID) {
		t.Fatalf("catalog credentials after migration = %+v, %v", assignments, err)
	}
	sources, err := st.CatalogExternalSubscriptions(ctx, userID)
	if err != nil || len(sources) != 1 || sources[0].Mode != ExtSubModeMerge {
		t.Fatalf("catalog traffic modes after migration = %+v, %v", sources, err)
	}

	now := time.Now()
	expired := now.Add(-time.Hour)
	if _, err := st.InsertUser(ctx, "expired user", "expired-uuid", "expired-token", &expired); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE chains SET endpoint_id=0 WHERE id=?", chainID); err != nil {
		t.Fatal(err)
	}
	active, err := st.CatalogEndpointAssignments(ctx, endpointID, now)
	if err != nil || len(active) != 1 || active[0].UserID != userID {
		t.Fatalf("active users of published endpoint = %+v, %v", active, err)
	}
}
