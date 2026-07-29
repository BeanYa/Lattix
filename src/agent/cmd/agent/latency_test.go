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
	var payload [9]byte
	payload[0] = probeKindLatency
	binary.BigEndian.PutUint64(payload[1:], 7)
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

func TestLatencyTrackerPauseRetainsSamplesAndIgnoresLatePong(t *testing.T) {
	tracker := newLatencyTracker()
	tracker.samples = []float64{12, 20, 30}
	tracker.pending[9] = time.Now().Add(-40 * time.Millisecond)
	tracker.setEnabled(false)
	if len(tracker.pending) != 0 {
		t.Fatal("pause must clear pending probes")
	}
	if got := tracker.medianMS(); got != nil {
		t.Fatalf("paused median = %v, want nil", got)
	}
	var payload [9]byte
	payload[0] = probeKindLatency
	binary.BigEndian.PutUint64(payload[1:], 9)
	if err := tracker.handlePong(string(payload[:])); err != nil {
		t.Fatal(err)
	}
	tracker.setEnabled(true)
	if got := tracker.medianMS(); got == nil || *got != 20 {
		t.Fatalf("resumed median = %v, want retained median 20", got)
	}
}
