package store

import (
	"context"
	"errors"
	"testing"

	"lattix/shared"
)

func TestPendingCredentialExchangeKeepsBootstrapUntilCommit(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	panelID, err := st.PanelInstanceID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, _ := shared.NewCredential(panelID, 1)
	pending, _ := shared.NewCredential(panelID, 1)
	serverID, err := st.CreateServer(ctx, "agent", "agent.test", bootstrap, MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	set, err := st.SetPendingCredential(ctx, serverID, pending, "exchange")
	if err != nil || !set {
		t.Fatalf("set pending: changed=%v err=%v", set, err)
	}
	if _, err := st.ServerByToken(ctx, bootstrap); err != nil {
		t.Fatalf("bootstrap must remain valid before commit: %v", err)
	}
	if _, err := st.ServerByToken(ctx, pending); err != nil {
		t.Fatalf("pending credential must support response-loss reconnect: %v", err)
	}
	if committed, err := st.CommitPendingCredential(ctx, serverID, "wrong"); err != nil || committed {
		t.Fatalf("wrong exchange committed=%v err=%v", committed, err)
	}
	if committed, err := st.CommitPendingCredential(ctx, serverID, "exchange"); err != nil || !committed {
		t.Fatalf("commit pending: committed=%v err=%v", committed, err)
	}
	if _, err := st.ServerByToken(ctx, bootstrap); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bootstrap lookup after commit = %v, want not found", err)
	}
	server, err := st.ServerByToken(ctx, pending)
	if err != nil || !server.CredentialCommitted || server.CredentialPendingToken != "" {
		t.Fatalf("committed server = %#v err=%v", server, err)
	}
}
