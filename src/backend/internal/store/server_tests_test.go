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
	"strings"
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
	outcome, err = st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &manifest, TaskID: task.TaskID, Generation: task.Generation, Index: 0, Data: compressed.Bytes(),
	})
	if err != nil || outcome != ServerTestChunkComplete {
		t.Fatalf("idempotent terminal publish outcome=%q err=%v", outcome, err)
	}
	conflicting := manifest
	conflicting.SHA256 = strings.Repeat("1", 64)
	if _, err := st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &conflicting, TaskID: task.TaskID, Generation: task.Generation, Index: 0, Data: compressed.Bytes(),
	}); err == nil {
		t.Fatal("conflicting terminal report replaced the authoritative result")
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

func TestServerTestLegacyReportPreservesUnknownIPv6Availability(t *testing.T) {
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
		Version: "test-v1",
		Hashes:  map[string]string{"zstatic": "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	if _, _, err := st.EnqueueServerTest(ctx, serverID, "", []shared.ServerTestCategory{shared.ServerTestIPQuality}, catalog); err != nil {
		t.Fatal(err)
	}
	storeResult := func(resultJSON string) {
		if _, err := st.db.ExecContext(ctx, `UPDATE server_test_tasks SET status = ?, result_json = ?,
			result_sha256 = '', error_code = '', error_message = '', agent_version = 'v0.0.9',
			completed_at = CURRENT_TIMESTAMP WHERE server_id = ?`,
			shared.ServerTestSucceeded, resultJSON, serverID); err != nil {
			t.Fatal(err)
		}
	}
	readResult := func() *shared.ServerTestReport {
		t.Helper()
		stored, err := st.ServerTestByServerID(ctx, serverID)
		if err != nil || stored.Result == nil {
			t.Fatalf("read stored result=%+v err=%v", stored, err)
		}
		return stored.Result
	}

	t.Run("legacy report without ipv6_available stays unknown", func(t *testing.T) {
		storeResult(`{"environment": {"probe_method":"raw_syn","degraded":false,"sandbox":"none","privileges":"root"}}`)
		report := readResult()
		if report.Environment.IPv6Available != nil {
			t.Fatalf("legacy report ipv6_available = %v, want nil", *report.Environment.IPv6Available)
		}
		roundTripped, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(roundTripped, []byte("ipv6_available")) {
			t.Fatalf("re-marshaled legacy report fabricates ipv6_available: %s", roundTripped)
		}
	})
	for _, tc := range []struct {
		name    string
		value   bool
		literal string
	}{
		{name: "fresh report with ipv6_available true", value: true, literal: "true"},
		{name: "fresh report with ipv6_available false", value: false, literal: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeResult(`{"schema_version":1,"status":"succeeded","agent_version":"v0.0.9","catalog_version":"test-v1",` +
				`"environment": {"probe_method":"raw_syn","degraded":false,"sandbox":"none","privileges":"root",` +
				`"ipv6_available":` + tc.literal + `}}`)
			report := readResult()
			if report.Environment.IPv6Available == nil || *report.Environment.IPv6Available != tc.value {
				t.Fatalf("fresh report ipv6_available = %v, want %v", report.Environment.IPv6Available, tc.value)
			}
			roundTripped, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(roundTripped, []byte(`"ipv6_available":`+tc.literal)) {
				t.Fatalf("re-marshaled fresh report lost ipv6_available: %s", roundTripped)
			}
		})
	}
}

func TestDeleteServerCascadeRemovesServerTestState(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, "test", "", "token", MachineTypeDirect, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	catalog := shared.ServerTestCatalogSnapshot{
		Version: "test-v1",
		Hashes:  map[string]string{"zstatic": "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	task, _, err := st.EnqueueServerTest(ctx, serverID, "", []shared.ServerTestCategory{shared.ServerTestIPQuality}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	manifest := shared.ServerTestResultManifest{
		SchemaVersion: shared.ServerTestSchemaVersion, TaskID: task.TaskID, Generation: task.Generation,
		Status: shared.ServerTestSucceeded, AgentVersion: "v0.0.9", CatalogVersion: catalog.Version,
		UncompressedSize: 1, CompressedSize: 2, SHA256: strings.Repeat("0", 64), ChunkCount: 2,
	}
	if outcome, err := st.SaveServerTestResultChunk(ctx, serverID, shared.ServerTestResultChunkPayload{
		Manifest: &manifest, TaskID: task.TaskID, Generation: task.Generation, Index: 0, Data: []byte{0},
	}); err != nil || outcome != ServerTestChunkAccepted {
		t.Fatalf("save partial result outcome=%q err=%v", outcome, err)
	}
	if err := st.DeleteServerCascade(ctx, serverID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"server_test_tasks", "server_test_result_chunks"} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE server_id = ?`, serverID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows", table, count)
		}
	}
}

func TestServerTestTransitionTable(t *testing.T) {
	// queued 为初始态（无入边）；其余非终态与终态均可由三个非终态转入。
	if got := serverTestFromSet(shared.ServerTestQueued); len(got) != 0 {
		t.Fatalf("queued 是初始态不应有入边，实际 %v", got)
	}
	for _, target := range []shared.ServerTestTaskStatus{
		shared.ServerTestAccepted, shared.ServerTestRunning,
		shared.ServerTestSucceeded, shared.ServerTestCompletedWithErrors, shared.ServerTestFailed,
	} {
		got := serverTestFromSet(target)
		if len(got) != 3 {
			t.Fatalf("serverTestFromSet(%s) = %v，期望 3 个非终态前置", target, got)
		}
		for _, from := range got {
			if !validServerTestTransition(from, target) {
				t.Errorf("from-set 与转换表不一致: %s → %s", from, target)
			}
		}
	}
	// 终态无出边。
	for _, terminal := range []shared.ServerTestTaskStatus{
		shared.ServerTestSucceeded, shared.ServerTestCompletedWithErrors, shared.ServerTestFailed,
	} {
		if targets := serverTestTransitions[terminal]; len(targets) != 0 {
			t.Fatalf("%s 终态出边 = %v，期望空", terminal, targets)
		}
	}
}

func TestServerTestTransitionsMatchOrderedStatuses(t *testing.T) {
	// SQL IN 参数顺序稳定（表键固定顺序），构建出的 placeholders 数量一致。
	for _, target := range serverTestStatusOrder {
		set := serverTestFromSet(target)
		if len(set) == 0 && !target.Terminal() && target != shared.ServerTestQueued {
			// 仅初始态 queued 允许空入边。
			t.Fatalf("非终态 %s 的 from 集合为空", target)
		}
		if target.Terminal() {
			// 终态只能由非终态转入。
			for _, from := range set {
				if from.Terminal() {
					t.Errorf("终态 → 终态转换 %s → %s 不应存在", from, target)
				}
			}
		}
	}
}
