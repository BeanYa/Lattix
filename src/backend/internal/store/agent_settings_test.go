package store

import (
	"context"
	"testing"

	"lattix/shared"
)

func TestPanelIdentityStableAndAgentRevisionIncrements(t *testing.T) {
	st, err := Open(t.TempDir() + "/settings.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	firstID, err := st.PanelInstanceID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := st.PanelInstanceID(ctx)
	if err != nil || secondID != firstID {
		t.Fatalf("panel ids = %q, %q, err=%v", firstID, secondID, err)
	}
	current, err := st.AgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desired := shared.DefaultAgentSettings()
	desired.Reconnect.Mode = shared.ReconnectModeLimited
	updated, err := st.UpdateAgentSettings(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != current.Revision+1 {
		t.Fatalf("revision = %d, want %d", updated.Revision, current.Revision+1)
	}
}
