package servertest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/shared"
)

const ooklaSampleJSON = `{
  "type": "result",
  "timestamp": "2026-08-13T08:00:00Z",
  "ping": {"jitter": 0.32, "latency": 12.34, "low": 11.9, "high": 13.1},
  "download": {"bandwidth": 12500000, "bytes": 187500000, "elapsed": 15015, "latency": {"iqm": 25.1}},
  "upload": {"bandwidth": 6250000, "bytes": 62500000, "elapsed": 15011, "latency": {"iqm": 30.2}},
  "server": {"id": 25858, "host": "speedtest.bmcc.com.cn", "port": 8080, "name": "China Mobile Group Beijing Co.Ltd", "location": "Beijing", "country": "China"},
  "result": {"id": "abc", "url": "https://www.speedtest.net/result/c/abc", "persisted": true}
}`

func TestParseOoklaOutput(t *testing.T) {
	result, err := parseOoklaOutput(ooklaSampleJSON)
	if err != nil {
		t.Fatalf("parseOoklaOutput: %v", err)
	}
	if result.DownloadMbps != 100 || result.UploadMbps != 50 {
		t.Fatalf("mbps = %v/%v, want 100/50", result.DownloadMbps, result.UploadMbps)
	}
	if result.DownloadBytes != 187500000 || result.UploadBytes != 62500000 ||
		result.DownloadMS != 15015 || result.UploadMS != 15011 {
		t.Fatalf("bytes/elapsed mismatch: %+v", result)
	}
	if result.LatencyMS != 12.34 || result.ResultURL == "" || !strings.Contains(result.ServerName, "China Mobile") {
		t.Fatalf("metadata mismatch: %+v", result)
	}
	if _, err := parseOoklaOutput(`{"download":{"bandwidth":0},"upload":{"bandwidth":0}}`); err == nil {
		t.Fatal("want error for empty bandwidth")
	}
	if _, err := parseOoklaOutput(`not json`); err == nil {
		t.Fatal("want error for invalid JSON")
	}
}

func writeFakeSpeedtest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "speedtest")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+body), 0o700); err != nil {
		t.Fatalf("write fake speedtest: %v", err)
	}
	return path
}

func TestRunOoklaServerSuccess(t *testing.T) {
	bin := writeFakeSpeedtest(t, "cat <<'EOF'\n"+ooklaSampleJSON+"\nEOF\n")
	result, err := runOoklaServer(context.Background(), bin, "25858")
	if err != nil {
		t.Fatalf("runOoklaServer: %v", err)
	}
	if result.DownloadMbps != 100 {
		t.Fatalf("download = %v Mbps", result.DownloadMbps)
	}
}

func TestRunOoklaServerFailure(t *testing.T) {
	bin := writeFakeSpeedtest(t, "echo 'server selection failed' >&2\nexit 1\n")
	_, err := runOoklaServer(context.Background(), bin, "1")
	if err == nil || !strings.Contains(err.Error(), "server selection failed") {
		t.Fatalf("err = %v, want stderr tail", err)
	}
}

func TestRunOoklaServerTimeout(t *testing.T) {
	bin := writeFakeSpeedtest(t, "sleep 120\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := runOoklaServer(ctx, bin, "1")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestRunOoklaSpeedTarget(t *testing.T) {
	bin := writeFakeSpeedtest(t, "cat <<'EOF'\n"+ooklaSampleJSON+"\nEOF\n")
	target := shared.ServerTestTarget{
		ID: "speed:ookla-beijing-mobile", Label: "北京移动",
		AddressFamily: shared.ServerTestIPv4, OoklaServerID: "25858",
	}
	result := runSpeedTarget(context.Background(), target, bin, nil)
	if result.Status != "available" || result.DownloadMbps != 100 || result.UploadMbps != 50 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.LatencyMS != 12.34 || result.ResultURL == "" {
		t.Fatalf("metadata not propagated: %#v", result)
	}

	failed := runSpeedTarget(context.Background(), target, "", os.ErrNotExist)
	if failed.Status != "failed" || failed.ErrorCode != "ookla_cli_unavailable" {
		t.Fatalf("unexpected cli-missing result: %#v", failed)
	}
}
