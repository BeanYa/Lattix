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

func TestLatencyTrackerTimeoutReportsActiveMissingSample(t *testing.T) {
	tracker := newLatencyTracker()
	tracker.samples = []float64{20}
	tracker.pending[7] = time.Now().Add(-latencyProbeTimeout - time.Millisecond)

	value, active := tracker.snapshot()
	if !active || value != nil {
		t.Fatalf("timed out snapshot = (%v, %v), want nil and active", value, active)
	}
	if len(tracker.pending) != 0 {
		t.Fatal("timed out probe must be removed from pending probes")
	}

	tracker.pending[8] = time.Now().Add(-40 * time.Millisecond)
	var payload [9]byte
	payload[0] = probeKindLatency
	binary.BigEndian.PutUint64(payload[1:], 8)
	if err := tracker.handlePong(string(payload[:])); err != nil {
		t.Fatal(err)
	}
	value, active = tracker.snapshot()
	if !active || value == nil {
		t.Fatalf("recovered snapshot = (%v, %v), want a value and active", value, active)
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
	if _, active := tracker.snapshot(); active {
		t.Fatal("paused tracker must report latency probes as inactive")
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
	got, active := tracker.snapshot()
	if !active || got == nil || *got != 20 {
		t.Fatalf("resumed snapshot = (%v, %v), want retained median 20 and active", got, active)
	}
}
