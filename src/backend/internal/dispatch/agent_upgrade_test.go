package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestEnqueueAgentUpgradeAllIncludesOfflineServersAndForce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	firstID, _ := st.CreateServer(ctx, "first", "first.example.com", "first-token", store.MachineTypeDirect, "", "", "US", "")
	secondID, _ := st.CreateServer(ctx, "second", "second.example.com", "second-token", store.MachineTypeDirect, "", "", "US", "")
	requester := &fakeRequester{online: map[int64]bool{firstID: true, secondID: false}}
	d := New(st, requester)

	queued, err := d.EnqueueAgentUpgradeAll(ctx, "v1.2.3", "https://releases.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 2 {
		t.Fatalf("queued = %d, want 2", queued)
	}
	commands, err := st.CommandsByType(ctx, shared.TypeUpgradeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(commands))
	}
	for _, command := range commands {
		var payload shared.UpgradeAgentPayload
		if err := json.Unmarshal(command.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Version != "v1.2.3" || payload.ReleaseBase != "https://releases.example.com" || !payload.Force {
			t.Fatalf("payload = %+v", payload)
		}
		wantStatus := store.CommandStatusQueued
		if command.ServerID == firstID {
			wantStatus = store.CommandStatusSent
		}
		if command.Status != wantStatus {
			t.Fatalf("server %d status = %s, want %s", command.ServerID, command.Status, wantStatus)
		}
	}
}
