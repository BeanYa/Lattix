package xray

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type telemetryTestRunner struct {
	restarts int
}

func (r *telemetryTestRunner) Restart(context.Context) error {
	r.restarts++
	return nil
}

func (*telemetryTestRunner) IsRunning(context.Context) bool { return true }
func (*telemetryTestRunner) Stop(context.Context) error     { return nil }

func TestEnsureTelemetryFeaturesMigratesMinimalInstallConfig(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "xray.json")
	minimal := []byte(`{
  "inbounds": [{"tag":"node_7","protocol":"dokodemo-door"}],
  "outbounds": [{"protocol":"freedom","tag":"direct"}],
  "routing": {"domainStrategy":"AsIs"}
}`)
	if err := os.WriteFile(configPath, minimal, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &telemetryTestRunner{}
	mgr := NewManager(bin, configPath, "127.0.0.1:19085", runner)

	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", runner.restarts)
	}
	assertTelemetryConfig(t, configPath)

	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != 1 {
		t.Fatalf("idempotent ensure restarted Xray: restarts = %d", runner.restarts)
	}
}

func assertTelemetryConfig(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	api := config["api"].(map[string]any)
	if api["listen"] != "127.0.0.1:19085" {
		t.Fatalf("api.listen = %v", api["listen"])
	}
	services := api["services"].([]any)
	for _, required := range []string{"HandlerService", "StatsService"} {
		found := false
		for _, service := range services {
			found = found || service == required
		}
		if !found {
			t.Fatalf("api.services = %v, missing %s", services, required)
		}
	}
	policy := config["policy"].(map[string]any)
	level := policy["levels"].(map[string]any)["0"].(map[string]any)
	system := policy["system"].(map[string]any)
	for _, key := range []string{"statsUserUplink", "statsUserDownlink", "statsUserOnline"} {
		if level[key] != true {
			t.Fatalf("policy.levels.0.%s = %v", key, level[key])
		}
	}
	for _, key := range []string{"statsInboundUplink", "statsInboundDownlink"} {
		if system[key] != true {
			t.Fatalf("policy.system.%s = %v", key, system[key])
		}
	}
	if config["routing"] == nil || len(config["inbounds"].([]any)) != 1 {
		t.Fatal("migration discarded existing managed configuration")
	}
}

func TestEnsureTelemetryFeaturesEnablesUserOnlinePolicy(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "xray.json")
	runner := &telemetryTestRunner{}
	mgr := NewManager(bin, configPath, "127.0.0.1:19085", runner)

	if err := mgr.EnsureTelemetryFeatures(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	level := config["policy"].(map[string]any)["levels"].(map[string]any)["0"].(map[string]any)
	if level["statsUserOnline"] != true {
		t.Fatalf("policy.levels.0.statsUserOnline = %v, want true", level["statsUserOnline"])
	}
}
