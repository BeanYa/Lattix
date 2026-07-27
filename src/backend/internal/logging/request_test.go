package logging

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestLogAppendsAndReadsNewestWindow(t *testing.T) {
	log, err := OpenRequestLog(filepath.Join(t.TempDir(), "requests"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close(context.Background())

	for i := 0; i < 35; i++ {
		log.Append(RequestEntry{
			Timestamp:  time.Unix(int64(i), 0),
			RequestID:  newID(),
			Severity:   SeverityInfo,
			Method:     "GET",
			Path:       "/api/test",
			HTTPStatus: 200,
		})
	}
	items, _, err := log.Tail(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 30 {
		t.Fatalf("tail returned %d entries, want 30", len(items))
	}
	if got := items[0].Timestamp.Unix(); got != 34 {
		t.Fatalf("newest timestamp = %d, want 34", got)
	}
	if got := items[29].Timestamp.Unix(); got != 5 {
		t.Fatalf("oldest timestamp = %d, want 5", got)
	}
}

func TestRequestLogClear(t *testing.T) {
	log, err := OpenRequestLog(filepath.Join(t.TempDir(), "requests"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close(context.Background())
	log.Append(RequestEntry{Timestamp: time.Now(), RequestID: newID(), Method: "GET", HTTPStatus: 200})
	if _, _, err := log.Tail(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if err := log.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _, err := log.Tail(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("tail after clear returned %d entries", len(items))
	}
}

func TestRequestLogRotatesWithinConfiguredLimit(t *testing.T) {
	log, err := OpenRequestLog(filepath.Join(t.TempDir(), "requests"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close(context.Background())

	for i := 0; i < 3000; i++ {
		log.Append(RequestEntry{
			Timestamp:  time.Unix(int64(i), 0),
			RequestID:  newID(),
			Severity:   SeverityInfo,
			Method:     "GET",
			Path:       "/api/" + strings.Repeat("x", 480),
			HTTPStatus: 200,
		})
	}
	// Status is serialized behind all queued append commands, so it also waits
	// until the writer has completed rotation and retention enforcement.
	status, err := log.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.UsageBytes > status.MaxBytes {
		t.Fatalf("request logs use %d bytes, limit %d", status.UsageBytes, status.MaxBytes)
	}
	if status.SegmentCount < 2 {
		t.Fatalf("segment count = %d, want rotation", status.SegmentCount)
	}
}
