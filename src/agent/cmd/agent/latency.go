package main

import (
	"encoding/binary"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval      = 30 * time.Second
	probeKindLiveness byte = 1
	probeKindLatency  byte = 2
)

type latencyTracker struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]time.Time
	samples []float64
	ready   chan struct{}
	once    sync.Once
	enabled bool
}

func newLatencyTracker() *latencyTracker {
	return &latencyTracker{
		pending: make(map[uint64]time.Time),
		ready:   make(chan struct{}),
		enabled: true,
	}
}

func (t *latencyTracker) sendProbe(conn *safeConn) error {
	t.mu.Lock()
	if !t.enabled {
		t.mu.Unlock()
		return nil
	}
	t.next++
	sequence := t.next
	t.pending[sequence] = time.Now()
	for old := range t.pending {
		if old+3 < sequence {
			delete(t.pending, old)
		}
	}
	t.mu.Unlock()

	var payload [9]byte
	payload[0] = probeKindLatency
	binary.BigEndian.PutUint64(payload[1:], sequence)
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
	if len(data) != 9 || data[0] != probeKindLatency {
		return nil
	}
	sequence := binary.BigEndian.Uint64(data[1:])
	t.mu.Lock()
	sentAt, ok := t.pending[sequence]
	if ok && t.enabled {
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
	if !t.enabled || len(t.samples) == 0 {
		return nil
	}
	values := append([]float64(nil), t.samples...)
	sort.Float64s(values)
	value := values[len(values)/2]
	return &value
}

func (t *latencyTracker) setEnabled(enabled bool) {
	t.mu.Lock()
	t.enabled = enabled
	if !enabled {
		clear(t.pending)
	}
	t.mu.Unlock()
}

func sendLiveness(conn *safeConn) error {
	var payload [9]byte
	payload[0] = probeKindLiveness
	binary.BigEndian.PutUint64(payload[1:], uint64(time.Now().UnixNano()))
	return conn.writeControl(websocket.PingMessage, payload[:])
}
