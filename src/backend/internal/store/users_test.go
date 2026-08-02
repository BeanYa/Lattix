package store

import (
	"context"
	"errors"
	"testing"
)

func TestSetUserSubToken(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	userID, err := st.InsertUser(ctx, "user", "user-uuid", "old-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubToken(ctx, userID, "new-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserBySubToken(ctx, "old-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token lookup err = %v, want ErrNotFound", err)
	}
	u, err := st.UserBySubToken(ctx, "new-token")
	if err != nil {
		t.Fatal(err)
	}
	if u.SubToken != "new-token" {
		t.Fatalf("sub token = %q, want new-token", u.SubToken)
	}
	if err := st.SetUserSubToken(ctx, 9999, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}
