package panel

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestOnServerTestCompletedWritesInspectionSummary(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "direct", BootstrapToken: "token", MachineType: store.MachineTypeDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := shared.ServerTestCatalogSnapshot{
		Version: "test-v1",
		Hashes:  map[string]string{"zstatic": strings.Repeat("0", 64)},
	}
	task, _, err := st.EnqueueServerInspection(ctx, serverID, "", serverInspectionCategories(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if task.Source != store.ServerTestSourceInspection {
		t.Fatalf("source = %q", task.Source)
	}
	if _, err := st.CreateServerInspectionRun(ctx, serverID, task.TaskID); err != nil {
		t.Fatal(err)
	}

	report := shared.ServerTestReport{
		SchemaVersion: shared.ServerTestSchemaVersion, TaskID: task.TaskID, Generation: task.Generation,
		Status: shared.ServerTestSucceeded, StartedAt: time.Now().UTC().Format(time.RFC3339),
		CompletedAt: time.Now().UTC().Format(time.RFC3339), AgentVersion: "v-test", CatalogVersion: catalog.Version,
		Categories: []shared.ServerTestCategoryResult{
			{Category: shared.ServerTestIPQuality, Status: "available"},
			{Category: shared.ServerTestTCPIPv4, Status: "available",
				Summary: map[string]any{"targets": float64(1), "failed_targets": float64(0)},
				Items:   []map[string]any{{"label": "广东电信", "rtt_avg_ms": 42.0, "loss_percent": 0.0}}},
			{Category: shared.ServerTestConnectivity, Status: "available",
				Summary: map[string]any{"targets": float64(2), "failed_targets": float64(1)},
				Items: []map[string]any{
					{"rtt_median_ms": 80.0},
					{"error_code": "timeout"},
				}},
		},
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
		Status: shared.ServerTestSucceeded, AgentVersion: "v-test", CatalogVersion: catalog.Version,
		UncompressedSize: len(raw), CompressedSize: compressed.Len(),
		SHA256: hex.EncodeToString(sum[:]), ChunkCount: 1,
	}
	if _, err := st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &manifest, TaskID: task.TaskID, Generation: task.Generation, Index: 0,
		Data: compressed.Bytes(),
	}); err != nil {
		t.Fatal(err)
	}

	server := &Server{st: st}
	server.onServerTestCompleted(ctx, serverID, task.TaskID)
	latest, err := st.LatestServerInspectionRun(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != string(shared.ServerTestSucceeded) || latest.SummaryJSON == "" {
		t.Fatalf("run = %+v", latest)
	}
	var summary serverInspectionSummary
	if err := json.Unmarshal([]byte(latest.SummaryJSON), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TCPIPv4 == nil || summary.TCPIPv4.Targets != 1 || summary.TCPIPv4.AvgRTTMS == nil {
		t.Fatalf("summary tcp = %+v", summary.TCPIPv4)
	}
	if summary.Connectivity == nil || summary.Connectivity.Failed != 1 {
		t.Fatalf("summary connectivity = %+v", summary.Connectivity)
	}
}
