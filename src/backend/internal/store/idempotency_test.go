package store

import (
	"context"
	"errors"
	"testing"
)

func TestIdempotencyReservationLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.ReserveIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "hash"); err != nil {
		t.Fatal(err)
	}
	record, err := st.IdempotencyRecord(ctx, "admin", "/api/test", "request-key")
	if err != nil {
		t.Fatal(err)
	}
	if record.RequestHash != "hash" || record.ResponseJSON != "" {
		t.Fatalf("unexpected reservation: %+v", record)
	}
	if err := st.ReserveIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "other-hash"); !errors.Is(err, ErrIdempotencyReservationExists) {
		t.Fatalf("duplicate reservation error = %v", err)
	}
	response := `{"code":"OK","message":"","data":null}`
	if err := st.CompleteIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "hash", response); err != nil {
		t.Fatal(err)
	}
	record, err = st.IdempotencyRecord(ctx, "admin", "/api/test", "request-key")
	if err != nil {
		t.Fatal(err)
	}
	if record.ResponseJSON != response {
		t.Fatalf("response = %q, want %q", record.ResponseJSON, response)
	}
	if err := st.CompleteIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "hash", response); err == nil {
		t.Fatal("expected a completed reservation to reject a second completion")
	}
}

func TestDeleteIdempotencyReservationDoesNotDeleteCompletedRecord(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReserveIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteIdempotencyRecord(ctx, "admin", "/api/test", "request-key", "hash", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteIdempotencyReservation(ctx, "admin", "/api/test", "request-key", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IdempotencyRecord(ctx, "admin", "/api/test", "request-key"); err != nil {
		t.Fatalf("completed record was deleted: %v", err)
	}
}
