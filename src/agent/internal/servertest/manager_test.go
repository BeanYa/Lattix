package servertest

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lattix/shared"
)

func testPayload() shared.ServerTestRunPayload {
	return shared.ServerTestRunPayload{
		SchemaVersion: shared.ServerTestSchemaVersion,
		TaskID:        shared.NewMessageID(), Generation: 1,
		Categories: []shared.ServerTestCategory{shared.ServerTestIPQuality},
		Catalog: shared.ServerTestCatalogSnapshot{
			Version: "test-v1",
			Hashes:  map[string]string{"zstatic": "0000000000000000000000000000000000000000000000000000000000000000"},
		},
	}
}

func TestManagerCompletedMarkerPreventsRecoveredCommandRerun(t *testing.T) {
	directory := t.TempDir()
	payload := testPayload()
	manager, err := NewManager(directory, "v0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.idle = make(chan struct{})
	manager.journal = &taskJournal{
		Version: 1, State: taskStateResultPending, Payload: payload,
		Manifest: &shared.ServerTestResultManifest{}, UpdatedAt: time.Now().UTC(),
	}
	if err := manager.saveJournalLocked(); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	manager.mu.Unlock()
	if err := manager.completeLocal(payload.TaskID, payload.Generation); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewManager(directory, "v0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Busy() {
		t.Fatal("completed marker reported a busy manager")
	}
	if err := recovered.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Accept(payload); err != nil {
		t.Fatalf("recovered duplicate command was not accepted idempotently: %v", err)
	}
	recovered.mu.Lock()
	defer recovered.mu.Unlock()
	if recovered.journal == nil || recovered.journal.State != taskStateCompleted {
		t.Fatalf("duplicate command changed completed marker: %+v", recovered.journal)
	}
}

func TestManagerTurnsRunningJournalIntoAgentRestartFailure(t *testing.T) {
	directory := t.TempDir()
	payload := testPayload()
	journal := taskJournal{
		Version: 1, State: taskStateRunning, BootID: currentBootID(), Payload: payload,
		UpdatedAt: time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server-test-task.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory, "v0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	stored := *manager.journal
	manager.mu.Unlock()
	if stored.State != taskStateResultPending || stored.Manifest == nil || stored.Manifest.Status != shared.ServerTestFailed {
		t.Fatalf("unexpected recovered journal: %+v", stored)
	}
	compressed, err := os.Open(filepath.Join(directory, "server-test-result.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	reportJSON, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	_ = compressed.Close()
	var report shared.ServerTestReport
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}
	wantCode := "execution_interrupted"
	if currentBootID() != "" {
		wantCode = "agent_restarted"
	}
	if report.ErrorCode != wantCode || report.TaskID != payload.TaskID || report.Generation != payload.Generation {
		t.Fatalf("unexpected recovered report: %+v", report)
	}
}

func TestPublicAddressPolicyRejectsLocalRanges(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1", "fe80::1"} {
		address, err := netip.ParseAddr(value)
		if err != nil {
			t.Fatal(err)
		}
		if publicAddress(address) {
			t.Fatalf("%s unexpectedly allowed", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		address, _ := netip.ParseAddr(value)
		if !publicAddress(address) {
			t.Fatalf("%s unexpectedly rejected", value)
		}
	}
}

func TestTaskJournalTransitions(t *testing.T) {
	legal := []struct{ from, to taskJournalState }{
		{taskStateRunning, taskStateResultPending},
		{taskStateResultPending, taskStateCompleted},
	}
	for _, c := range legal {
		if !validTaskJournalTransition(c.from, c.to) {
			t.Errorf("missing transition edge %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to taskJournalState }{
		{taskStateRunning, taskStateCompleted},     // result must be persisted first
		{taskStateResultPending, taskStateRunning}, // no re-run while awaiting delivery
		{taskStateCompleted, taskStateRunning},
		{taskStateCompleted, taskStateResultPending},
		{taskStateRunning, "unknown"},
		{"", taskStateResultPending},
	}
	for _, c := range illegal {
		if validTaskJournalTransition(c.from, c.to) {
			t.Errorf("illegal transition not rejected %s → %s", c.from, c.to)
		}
	}
	// Same-state transitions stay idempotent.
	for _, s := range []taskJournalState{taskStateRunning, taskStateResultPending, taskStateCompleted} {
		if !validTaskJournalTransition(s, s) {
			t.Errorf("same-state transition %s should be idempotent", s)
		}
	}
}

func TestManagerCompleteLocalRejectsRunningTransition(t *testing.T) {
	directory := t.TempDir()
	payload := testPayload()
	manager, err := NewManager(directory, "v0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.journal = &taskJournal{
		Version: 1, State: taskStateRunning, Payload: payload, UpdatedAt: time.Now().UTC(),
	}
	manager.mu.Unlock()
	if err := manager.completeLocal(payload.TaskID, payload.Generation); err == nil {
		t.Fatal("completeLocal accepted a running → completed transition")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.journal == nil || manager.journal.State != taskStateRunning {
		t.Fatalf("rejected transition changed the journal: %+v", manager.journal)
	}
}
