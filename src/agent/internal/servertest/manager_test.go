package servertest

import (
	"compress/gzip"
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

func TestManagerTurnsRunningJournalIntoAgentRestartFailure(t *testing.T) {
	directory := t.TempDir()
	payload := testPayload()
	journal := taskJournal{
		Version: 1, State: "running", BootID: currentBootID(), Payload: payload,
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
	if stored.State != "result_pending" || stored.Manifest == nil || stored.Manifest.Status != shared.ServerTestFailed {
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
