package main

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestLatencyTrackerMedian(t *testing.T) {
	tracker := newLatencyTracker()
	tracker.samples = []float64{80, 12, 45}
	value := tracker.medianMS()
	if value == nil || *value != 45 {
		t.Fatalf("median = %v, want 45", value)
	}
}

func TestLatencyTrackerPongInitializesAndRecords(t *testing.T) {
	tracker := newLatencyTracker()
	tracker.pending[7] = time.Now().Add(-40 * time.Millisecond)
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], 7)
	if err := tracker.handlePong(string(payload[:])); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tracker.ready:
	default:
		t.Fatal("first valid pong must initialize latency")
	}
	value := tracker.medianMS()
	if value == nil || *value < 30 || *value > 200 {
		t.Fatalf("recorded latency = %v, want a value near 40ms", value)
	}
}
