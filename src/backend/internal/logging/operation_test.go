package logging

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOperationStoreRetainsNewestEntries(t *testing.T) {
	store, err := OpenOperationStore(filepath.Join(t.TempDir(), "operation.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := 0; i < 105; i++ {
		if err := store.Record(context.Background(), OperationEvent{
			Category: CategoryServer,
			Action:   "server.updated",
			Detail:   map[string]int{"index": i},
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := store.List(context.Background(), OperationFilter{}, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 || len(items) != 100 {
		t.Fatalf("retained %d/%d entries, want 100/100", len(items), total)
	}
	if items[0].Detail != `{"index":104}` {
		t.Fatalf("newest detail = %s", items[0].Detail)
	}
}

func TestOperationStoreDetectsUncleanRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.db")
	first, err := OpenOperationStore(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.StartRun(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenOperationStore(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.StartRun(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	items, _, err := second.List(context.Background(), OperationFilter{Category: CategoryPanel}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Action == "panel.unclean_shutdown" && item.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("unclean shutdown event not found in %#v", items)
	}
}

func TestOperationStoreClearLeavesAuditEntry(t *testing.T) {
	store, err := OpenOperationStore(filepath.Join(t.TempDir(), "operation.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Record(context.Background(), OperationEvent{Category: CategoryServer, Action: "server.created"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(context.Background(), OperationEvent{Operator: "admin", Detail: map[string]int{"removed": 1}}); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.List(context.Background(), OperationFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Action != "operation_log.cleared" {
		t.Fatalf("items after clear = %#v, total %d", items, total)
	}
}

func TestOperationStoreCleanStopDoesNotReportUncleanShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.db")
	first, err := OpenOperationStore(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.StartRun(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	if err := first.StopRun(context.Background(), "signal:terminated"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenOperationStore(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.StartRun(context.Background(), "v2"); err != nil {
		t.Fatal(err)
	}
	items, _, err := second.List(context.Background(), OperationFilter{Category: CategoryPanel}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Action == "panel.unclean_shutdown" {
			t.Fatalf("clean stop was reported as unclean: %#v", items)
		}
	}
}
