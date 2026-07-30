package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"lattix/shared"
)

func TestServerTestLatestGenerationAndValidatedChunkPublish(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, "test", "", "token", MachineTypeDirect, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	catalog := shared.ServerTestCatalogSnapshot{
		Version: "test-v1", Hashes: map[string]string{"zstatic": string(make([]byte, 64))},
	}
	// Hex zeroes are valid; NUL bytes are not.
	catalog.Hashes["zstatic"] = "0000000000000000000000000000000000000000000000000000000000000000"
	task, _, err := st.EnqueueServerTest(ctx, serverID, "", []shared.ServerTestCategory{shared.ServerTestIPQuality}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if task.Generation != 1 || task.Status != shared.ServerTestQueued {
		t.Fatalf("unexpected first task: %+v", task)
	}
	if _, _, err := st.EnqueueServerTest(ctx, serverID, "", []shared.ServerTestCategory{shared.ServerTestIPQuality}, catalog); !errors.Is(err, ErrServerTestInProgress) {
		t.Fatalf("second enqueue error = %v, want in progress", err)
	}

	report := shared.ServerTestReport{
		SchemaVersion: shared.ServerTestSchemaVersion, TaskID: task.TaskID, Generation: task.Generation,
		Status: shared.ServerTestSucceeded, StartedAt: "2026-07-31T00:00:00Z", CompletedAt: "2026-07-31T00:01:00Z",
		AgentVersion: "v0.0.9", CatalogVersion: catalog.Version,
		Environment: shared.ServerTestEnvironment{ProbeMethod: "tcp_connect", Degraded: true, DegradedReason: "cap_net_raw_unavailable"},
		Categories:  []shared.ServerTestCategoryResult{{Category: shared.ServerTestIPQuality, Status: "available"}},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	manifest := shared.ServerTestResultManifest{
		SchemaVersion: shared.ServerTestSchemaVersion, TaskID: task.TaskID, Generation: task.Generation,
		Status: shared.ServerTestSucceeded, AgentVersion: report.AgentVersion, CatalogVersion: catalog.Version,
		UncompressedSize: len(raw), CompressedSize: compressed.Len(), SHA256: hex.EncodeToString(sum[:]), ChunkCount: 1,
	}
	outcome, err := st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &manifest, TaskID: task.TaskID, Generation: task.Generation, Index: 0, Data: compressed.Bytes(),
	})
	if err != nil || outcome != ServerTestChunkComplete {
		t.Fatalf("publish outcome=%q err=%v", outcome, err)
	}
	stored, err := st.ServerTestByServerID(ctx, serverID)
	if err != nil || stored.Result == nil || stored.Status != shared.ServerTestSucceeded || stored.ResultSHA256 != manifest.SHA256 {
		t.Fatalf("stored result=%+v err=%v", stored, err)
	}

	next, _, err := st.EnqueueServerTest(ctx, serverID, "", []shared.ServerTestCategory{shared.ServerTestTCPIPv4}, catalog)
	if err != nil || next.Generation != 2 || next.Result != nil {
		t.Fatalf("replacement=%+v err=%v", next, err)
	}
	outcome, err = st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &manifest, TaskID: task.TaskID, Generation: task.Generation, Index: 0, Data: compressed.Bytes(),
	})
	if err != nil || outcome != ServerTestChunkSuperseded {
		t.Fatalf("stale outcome=%q err=%v", outcome, err)
	}
}
