package main

import "testing"

func TestIgnoredNetworkInterface(t *testing.T) {
	for _, name := range []string{"", "lo", "docker0", "veth123", "br-abcd", "virbr0", "tun0", "tap1"} {
		if !ignoredNetworkInterface(name) {
			t.Errorf("%q must be ignored", name)
		}
	}
	for _, name := range []string{"eth0", "ens3", "enp1s0", "wlan0"} {
		if ignoredNetworkInterface(name) {
			t.Errorf("%q must be eligible", name)
		}
	}
}

func TestLinuxHostSourcesAvailable(t *testing.T) {
	if total, used, ok := rootDiskUsage(); !ok || total == 0 || used > total {
		t.Fatalf("root disk = total %d used %d ok %v", total, used, ok)
	}
	if uptime, ok := systemUptime(); !ok || uptime == 0 {
		t.Fatalf("uptime = %d ok %v", uptime, ok)
	}
}

func TestHostMetricsReportsLatencyProbeState(t *testing.T) {
	latency := 42.0
	active := (&telemetry{latency: func() (*float64, bool) {
		return &latency, true
	}}).hostMetrics()
	if active.LatencyProbeActive == nil || !*active.LatencyProbeActive ||
		active.LatencyMS == nil || *active.LatencyMS != latency {
		t.Fatalf("active latency state = value %v, active %v", active.LatencyMS, active.LatencyProbeActive)
	}

	paused := (&telemetry{latency: func() (*float64, bool) {
		return nil, false
	}}).hostMetrics()
	if paused.LatencyProbeActive == nil || *paused.LatencyProbeActive || paused.LatencyMS != nil {
		t.Fatalf("paused latency state = value %v, active %v", paused.LatencyMS, paused.LatencyProbeActive)
	}
}
