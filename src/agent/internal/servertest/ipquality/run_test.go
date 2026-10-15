package ipquality

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fileFetcher struct{ dir string }

func (f fileFetcher) GetText(_ context.Context, _ string, _ int64) (string, error) {
	content, err := os.ReadFile(filepath.Join(f.dir, "ip.sh"))
	return string(content), err
}

func fakeScriptContent() string {
	content, err := os.ReadFile(filepath.Join("testdata", "fake_ip.sh"))
	if err != nil {
		panic(err)
	}
	return "script_version=\"v-fake\"\n" + string(content)
}

func TestRunnerRun(t *testing.T) {
	dir := t.TempDir()
	content := fakeScriptContent()
	stubScriptHash(t, content)
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(content), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	var stages []string
	result, err := runner.Run(context.Background(), func(stage string) { stages = append(stages, stage) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ScriptVersion != "v-fake" || result.ScriptStale {
		t.Errorf("version=%q stale=%v", result.ScriptVersion, result.ScriptStale)
	}
	if !strings.Contains(result.Output, "\"IP\": \"203.0.113.9\"") || !strings.Contains(result.Output, "\"IP\": \"240e:390::1\"") {
		t.Errorf("output does not contain both family documents")
	}
	families, err := ParseScriptOutput(result.Output)
	if err != nil || len(families) != 2 {
		t.Fatalf("parse output: %v, families=%d", err, len(families))
	}
	if len(stages) < 3 || stages[0] != "下载脚本" {
		t.Errorf("stages = %v", stages)
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "ip.sh")); err != nil {
		t.Errorf("cached script missing: %v", err)
	}
}

// TestRunnerRunAcceptsExitOneWithCompletedReport mirrors NAT-tier machines:
// the script prints the IPv4 family document to stdout and exits 1 (upstream
// ip.sh does this when its trailing IPv6 gate fails on hosts without public
// IPv6); the completed report must be accepted instead of reported as failure.
func TestRunnerRunAcceptsExitOneWithCompletedReport(t *testing.T) {
	dir := t.TempDir()
	content, err := os.ReadFile(filepath.Join("testdata", "fake_ip_v4only.sh"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	script := "script_version=\"v-fake\"\n" + string(content)
	stubScriptHash(t, script)
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(script), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	result, err := runner.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Output, "\"IP\": \"203.0.113.9\"") {
		t.Errorf("output does not contain the IPv4 family document")
	}
	families, err := ParseScriptOutput(result.Output)
	if err != nil || len(families) != 1 {
		t.Fatalf("parse output: %v, families=%d", err, len(families))
	}
}

// TestRunnerRunExitOneWithoutReport guards real failures: a non-zero exit
// without a parseable report on stdout must still surface as an error.
func TestRunnerRunExitOneWithoutReport(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/bash\necho 'boom'\nexit 1\n"
	content := "script_version=\"v-boom\"\n" + script
	stubScriptHash(t, content)
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(content), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	_, err := runner.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "ip.sh failed") {
		t.Fatalf("err = %v, want ip.sh failed", err)
	}
}

func TestRunnerRunNoPublicAddress(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/bash\nexit 0\n"
	content := "script_version=\"v-empty\"\n" + script
	stubScriptHash(t, content)
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(content), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	_, err := runner.Run(context.Background(), nil)
	if !errors.Is(err, ErrNoFamily) {
		t.Fatalf("err = %v, want ErrNoFamily", err)
	}
}

func TestRunnerRunTimeout(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/bash\nsleep 30\n"
	content := "script_version=\"v-slow\"\n" + script
	stubScriptHash(t, content)
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(content), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: 200 * time.Millisecond,
		Missing: func() []string { return nil },
	}
	_, err := runner.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}
