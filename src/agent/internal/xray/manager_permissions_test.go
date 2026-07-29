package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitConfigUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config", "xray.json")
	mgr := NewManager(bin, configPath, "127.0.0.1:10085", nil)

	for _, level := range []string{"warning", "error"} {
		cfg := fullConfig{"log": json.RawMessage(`{"loglevel":"` + level + `"}`)}
		if err := mgr.commitConfig(cfg); err != nil {
			t.Fatal(err)
		}
		assertPrivateMode(t, configPath)
	}
	assertPrivateMode(t, configPath+".prev")
}

func assertPrivateMode(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s mode = %04o, want 0600", path, got)
	}
}
