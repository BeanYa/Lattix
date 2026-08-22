package dispatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

type uninstallRequester struct {
	mu   sync.Mutex
	sent []shared.Envelope
	wake chan struct{}
}

func (r *uninstallRequester) Send(_ context.Context, _ int64, envelope shared.Envelope) error {
	r.mu.Lock()
	r.sent = append(r.sent, envelope)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return nil
}

func (r *uninstallRequester) IsOnline(int64) bool { return true }

func TestUninstallRetryDelay(t *testing.T) {
	wants := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3200 * time.Millisecond,
		6400 * time.Millisecond,
		10 * time.Second,
		10 * time.Second,
		10 * time.Second,
	}
	for index, want := range wants {
		if got := uninstallRetryDelay(index + 1); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", index+1, got, want)
		}
	}
}

func TestUninstallRetriesSameRequestUntilACK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "agent", Address: "agent.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	go func() {
		for deliveries := 1; deliveries <= 2; deliveries++ {
			select {
			case <-requester.wake:
			case <-ctx.Done():
				return
			}
		}
		requester.mu.Lock()
		request := requester.sent[len(requester.sent)-1]
		requester.mu.Unlock()
		dispatcher.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: request.Type,
			RequestID: request.RequestID, TraceID: request.TraceID,
			Code: shared.CodeOK,
			Data: json.RawMessage(`{"node_id":0}`),
		})
	}()

	acked, attempts, err := dispatcher.UninstallWithRetry(ctx, serverID, shared.UninstallPayload{PurgeXray: true})
	if err != nil {
		t.Fatal(err)
	}
	if !acked || attempts != 2 {
		t.Fatalf("acked=%v attempts=%d, want true/2", acked, attempts)
	}
	requester.mu.Lock()
	defer requester.mu.Unlock()
	if len(requester.sent) != 2 || requester.sent[0].RequestID != requester.sent[1].RequestID {
		t.Fatalf("deliveries must reuse one request ID: %#v", requester.sent)
	}
}
