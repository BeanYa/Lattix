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

func TestApplySettingsRollsBackAllMutations(t *testing.T) {
	st, err := Open(t.TempDir() + "/settings-atomic.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.db.Exec(`CREATE TRIGGER fail_setting
		BEFORE INSERT ON settings WHEN NEW.key = 'forced_failure'
		BEGIN SELECT RAISE(ABORT, 'forced settings failure'); END`); err != nil {
		t.Fatal(err)
	}

	_, err = st.ApplySettings(ctx, []SettingMutation{
		{Key: "first", Value: "written-before-failure"},
		{Key: "forced_failure", Value: "fail"},
	}, nil)
	if err == nil {
		t.Fatal("expected settings transaction to fail")
	}
	value, err := st.GetSetting(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	if value != "" {
		t.Fatalf("failed transaction retained first mutation: %q", value)
	}
}
