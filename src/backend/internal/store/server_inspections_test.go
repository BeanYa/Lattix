package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestServerInspectionRunLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "direct", BootstrapToken: "token", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.LatestServerInspectionRun(ctx, serverID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("latest empty error = %v, want not found", err)
	}
	run, err := st.CreateServerInspectionRun(ctx, serverID, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.TaskID != "task-1" {
		t.Fatalf("created run = %+v", run)
	}
	if _, err := st.LatestCompletedServerInspectionRun(ctx, serverID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("latest completed error = %v, want not found", err)
	}

	if err := st.CompleteServerInspectionRun(ctx, serverID, "task-1", "succeeded", `{"status":"succeeded"}`, "", ""); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestCompletedServerInspectionRun(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "succeeded" || latest.SummaryJSON != `{"status":"succeeded"}` || latest.CompletedAt == nil {
		t.Fatalf("completed run = %+v", latest)
	}

	// A queued run is superseded when its task is replaced before completion.
	if _, err := st.CreateServerInspectionRun(ctx, serverID, "task-2"); err != nil {
		t.Fatal(err)
	}
	if err := st.SupersedeServerInspectionRun(ctx, serverID, "task-2", "replaced"); err != nil {
		t.Fatal(err)
	}
	pending, err := st.PendingServerInspectionRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}

	runs, err := st.ListServerInspectionRuns(ctx, serverID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].TaskID != "task-2" || runs[1].TaskID != "task-1" {
		t.Fatalf("history = %+v", runs)
	}

	if err := st.DeleteServerCascade(ctx, serverID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LatestServerInspectionRun(ctx, serverID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("run after server delete = %+v", err)
	}
}

func TestServerInspectionRunPrunesHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "direct", BootstrapToken: "token", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 30; index++ {
		taskID := "task-" + string(rune('a'+index%26)) + "-" + string(rune('a'+index/26))
		if _, err := st.CreateServerInspectionRun(ctx, serverID, taskID); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := st.ListServerInspectionRuns(ctx, serverID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > 26 {
		t.Fatalf("history retained %d rows, want <= 26", len(runs))
	}
}
