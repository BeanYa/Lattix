package store

import (
	"context"
	"testing"
	"time"
)

func TestSaveAndReadServerMetrics(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "probe", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "HK", Location: "Hong Kong"})
	if err != nil {
		t.Fatal(err)
	}
	cpu, txRate, rxRate, latency := 12.5, 1024.0, 2048.0, 39.0
	metric := ServerMetrics{
		Load1: 0.1, Load5: 0.2, Load15: 0.3, CPUPercent: &cpu,
		MemTotal: 4 << 30, MemUsed: 1 << 30,
		DiskTotal: 40 << 30, DiskUsed: 5 << 30,
		NetworkInterface: "eth0", NetworkTXBytes: 100, NetworkRXBytes: 200,
		NetworkTXBPS: &txRate, NetworkRXBPS: &rxRate,
		UptimeSeconds: 3600, LatencyMS: &latency,
	}
	if err := st.SaveServerMetrics(ctx, serverID, metric, true); err != nil {
		t.Fatal(err)
	}
	latest, err := st.ServerMetricsMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := latest[serverID]
	if got.NetworkInterface != "eth0" || got.CPUPercent == nil || *got.CPUPercent != cpu ||
		got.LatencyMS == nil || *got.LatencyMS != latency {
		t.Fatalf("latest metrics = %#v", got)
	}
	recent, err := st.RecentServerMetricSamples(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent[serverID]) != 1 {
		t.Fatalf("recent sample count = %d, want 1", len(recent[serverID]))
	}
	history, err := st.ServerMetricHistory(ctx, serverID, 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history sample count = %d, want 1", len(history))
	}
}

func TestRecentServerMetricSamplesSkipsPausedProbeAndKeepsTimeout(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "probe", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "HK", Location: "Hong Kong"})
	if err != nil {
		t.Fatal(err)
	}

	packets := []struct {
		latency *float64
		active  bool
	}{
		{latency: float64Ptr(10), active: true},
		{latency: nil, active: false},
		{latency: nil, active: true},
		{latency: float64Ptr(20), active: true},
	}
	for _, packet := range packets {
		if err := st.SaveServerMetrics(ctx, serverID, ServerMetrics{LatencyMS: packet.latency}, packet.active); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := st.RecentServerMetricSamples(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	samples := recent[serverID]
	if len(samples) != 3 {
		t.Fatalf("recent sample count = %d, want 3", len(samples))
	}
	latencies := map[float64]bool{}
	timeouts := 0
	for _, sample := range samples {
		if sample.LatencyMS == nil {
			timeouts++
			continue
		}
		latencies[*sample.LatencyMS] = true
	}
	if timeouts != 1 || !latencies[10] || !latencies[20] {
		t.Fatalf("recent samples = latencies %v, timeouts %d; want 10, 20 and one timeout", latencies, timeouts)
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}

func TestDeleteExpiredServerMetricHistory(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "probe", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "HK", Location: "Hong Kong"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveServerMetrics(ctx, serverID, ServerMetrics{}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx,
		`UPDATE server_metric_history SET sampled_at = datetime('now', '-25 hours')`); err != nil {
		t.Fatal(err)
	}
	removed, err := st.DeleteExpiredServerMetricHistory(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}
