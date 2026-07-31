package main

import (
	"encoding/binary"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval        = 30 * time.Second
	latencyProbeTimeout      = 10 * time.Second
	probeKindLiveness   byte = 1
	probeKindLatency    byte = 2
)

type latencyTracker struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]time.Time
	samples []float64
	ready   chan struct{}
	once    sync.Once
	enabled bool

	lastOutcomeSequence uint64
	lastOutcomeTimedOut bool
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
	now := time.Now()
	t.expirePendingLocked(now)
	t.next++
	sequence := t.next
	t.pending[sequence] = now
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
	now := time.Now()
	t.mu.Lock()
	sentAt, ok := t.pending[sequence]
	recorded := ok && t.enabled && now.Sub(sentAt) < latencyProbeTimeout
	if ok {
		delete(t.pending, sequence)
	}
	if recorded {
		value := float64(now.Sub(sentAt).Microseconds()) / 1000
		t.samples = append(t.samples, value)
		if len(t.samples) > 3 {
			t.samples = append([]float64(nil), t.samples[len(t.samples)-3:]...)
		}
		t.recordOutcomeLocked(sequence, false)
	} else if ok && t.enabled {
		t.recordOutcomeLocked(sequence, true)
	}
	t.mu.Unlock()
	if recorded {
		t.once.Do(func() { close(t.ready) })
	}
	return nil
}

func (t *latencyTracker) medianMS() *float64 {
	value, _ := t.snapshot()
	return value
}

func (t *latencyTracker) snapshot() (*float64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.enabled {
		return nil, false
	}
	t.expirePendingLocked(time.Now())
	// A fresh session reports telemetry before its first staggered probe. Keep
	// that warm-up packet out of latency history; only an observed timeout is a
	// meaningful active probe with no value.
	if t.lastOutcomeSequence == 0 && len(t.samples) == 0 {
		return nil, false
	}
	if t.lastOutcomeTimedOut {
		return nil, true
	}
	if len(t.samples) == 0 {
		return nil, true
	}
	values := append([]float64(nil), t.samples...)
	sort.Float64s(values)
	value := values[len(values)/2]
	return &value, true
}

func (t *latencyTracker) expirePendingLocked(now time.Time) {
	for sequence, sentAt := range t.pending {
		if now.Sub(sentAt) < latencyProbeTimeout {
			continue
		}
		delete(t.pending, sequence)
		t.recordOutcomeLocked(sequence, true)
	}
}

func (t *latencyTracker) recordOutcomeLocked(sequence uint64, timedOut bool) {
	if sequence < t.lastOutcomeSequence {
		return
	}
	t.lastOutcomeSequence = sequence
	t.lastOutcomeTimedOut = timedOut
}

func (t *latencyTracker) setEnabled(enabled bool) {
	t.mu.Lock()
	if enabled && !t.enabled {
		t.lastOutcomeTimedOut = false
	}
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
