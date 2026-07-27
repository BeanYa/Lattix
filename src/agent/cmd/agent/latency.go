package main

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval   = 30 * time.Second
	initialProbeTimeout = 10 * time.Second
)

type latencyTracker struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]time.Time
	samples []float64
	ready   chan struct{}
	once    sync.Once
}

func newLatencyTracker() *latencyTracker {
	return &latencyTracker{
		pending: make(map[uint64]time.Time),
		ready:   make(chan struct{}),
	}
}

func (t *latencyTracker) sendProbe(conn *safeConn) error {
	t.mu.Lock()
	t.next++
	sequence := t.next
	t.pending[sequence] = time.Now()
	for old := range t.pending {
		if old+3 < sequence {
			delete(t.pending, old)
		}
	}
	t.mu.Unlock()

	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], sequence)
	if err := conn.writeControl(websocket.PingMessage, payload[:]); err != nil {
		t.mu.Lock()
		delete(t.pending, sequence)
		t.mu.Unlock()
		return err
	}
	return nil
}

func (t *latencyTracker) handlePong(payload string) error {
	data := []byte(payload)
	if len(data) != 8 {
		return nil
	}
	sequence := binary.BigEndian.Uint64(data)
	t.mu.Lock()
	sentAt, ok := t.pending[sequence]
	if ok {
		delete(t.pending, sequence)
		value := float64(time.Since(sentAt).Microseconds()) / 1000
		t.samples = append(t.samples, value)
		if len(t.samples) > 3 {
			t.samples = append([]float64(nil), t.samples[len(t.samples)-3:]...)
		}
	}
	t.mu.Unlock()
	if ok {
		t.once.Do(func() { close(t.ready) })
	}
	return nil
}

func (t *latencyTracker) medianMS() *float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.samples) == 0 {
		return nil
	}
	values := append([]float64(nil), t.samples...)
	sort.Float64s(values)
	value := values[len(values)/2]
	return &value
}

func (t *latencyTracker) waitInitial(done <-chan struct{}) error {
	timer := time.NewTimer(initialProbeTimeout)
	defer timer.Stop()
	select {
	case <-t.ready:
		return nil
	case <-timer.C:
		return errors.New("initial latency probe timed out")
	case <-done:
		return errors.New("connection closed during initial latency probe")
	}
}
