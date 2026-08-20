package ipquality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubFetcher struct {
	content string
	err     error
}

func (f stubFetcher) GetText(_ context.Context, _ string, _ int64) (string, error) {
	return f.content, f.err
}

func scriptContent(version string) string {
	return "#!/bin/bash\nscript_version=\"" + version + "\"\n# body\n"
}

// stubScriptHash points the pinned hash at content for the duration of the
// test, so EnsureScript's SHA256 check accepts the fake script.
func stubScriptHash(t *testing.T, content string) {
	t.Helper()
	old := scriptSHA256
	sum := sha256.Sum256([]byte(content))
	scriptSHA256 = hex.EncodeToString(sum[:])
	t.Cleanup(func() { scriptSHA256 = old })
}

func TestExtractScriptVersion(t *testing.T) {
	if version, ok := ExtractScriptVersion(scriptContent("v2026-03-29")); !ok || version != "v2026-03-29" {
		t.Errorf("version = %q, %v", version, ok)
	}
	if _, ok := ExtractScriptVersion("no version here"); ok {
		t.Error("expected no version")
	}
}

func TestEnsureScriptFreshCache(t *testing.T) {
	dir := t.TempDir()
	stubScriptHash(t, scriptContent("v1"))
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if path != filepath.Join(dir, "ip.sh") || version != "v1" || stale {
		t.Errorf("path=%q version=%q stale=%v", path, version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v1") {
		t.Errorf("cached content mismatch")
	}

	// Same version reuses the cache without a rewrite.
	info, _ := os.Stat(path)
	modTime := info.ModTime()
	_, version, stale, err = EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir)
	if err != nil || version != "v1" || stale {
		t.Fatalf("recheck: %v %q %v", err, version, stale)
	}
	info, _ = os.Stat(path)
	if !info.ModTime().Equal(modTime) {
		t.Error("cache was rewritten despite same version")
	}
}

func TestEnsureScriptUpdatesVersion(t *testing.T) {
	dir := t.TempDir()
	stubScriptHash(t, scriptContent("v1"))
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	stubScriptHash(t, scriptContent("v2"))
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v2")}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if version != "v2" || stale {
		t.Errorf("version=%q stale=%v, want v2/false", version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v2") {
		t.Error("cache not replaced")
	}
}

func TestEnsureScriptFallbackToCache(t *testing.T) {
	dir := t.TempDir()
	stubScriptHash(t, scriptContent("v1"))
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{err: os.ErrNotExist}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if version != "v1" || !stale {
		t.Errorf("version=%q stale=%v, want v1/true", version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v1") {
		t.Error("cache content lost")
	}
}

func TestEnsureScriptNoCacheAndFetchFails(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{err: os.ErrNotExist}, dir); err == nil {
		t.Fatal("expected error when no cache and fetch fails")
	}
}

func TestEnsureScriptRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	content := scriptContent("v1")
	_, _, _, err := EnsureScript(context.Background(), stubFetcher{content: content}, dir)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	sum := sha256.Sum256([]byte(content))
	if got := hex.EncodeToString(sum[:]); !strings.Contains(err.Error(), got) || !strings.Contains(err.Error(), scriptSHA256) {
		t.Errorf("error should carry both actual and pinned hashes: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ip.sh")); !os.IsNotExist(statErr) {
		t.Error("cache must not be written on checksum mismatch")
	}
}

func TestEnsureScriptRejectsTamperedCacheOnFallback(t *testing.T) {
	dir := t.TempDir()
	// Seed a cache whose version line parses but whose body does not match
	// the pinned hash.
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(scriptContent("v1")), 0o700); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	path, _, _, err := EnsureScript(context.Background(), stubFetcher{err: os.ErrNotExist}, dir)
	if err == nil {
		t.Fatal("expected cache checksum rejection")
	}
	if path != "" {
		t.Errorf("tampered cache must not be returned for execution, got %q", path)
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("err = %v, want checksum mismatch", err)
	}
}

func TestEnsureScriptRepairsTamperedCache(t *testing.T) {
	dir := t.TempDir()
	stubScriptHash(t, scriptContent("v1"))
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Tamper with the cached body while keeping the version line intact.
	path := filepath.Join(dir, "ip.sh")
	if err := os.WriteFile(path, []byte(strings.Replace(scriptContent("v1"), "# body", "# injected", 1)), 0o700); err != nil {
		t.Fatalf("tamper cache: %v", err)
	}
	_, version, stale, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if version != "v1" || stale {
		t.Errorf("version=%q stale=%v, want v1/false", version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v1") {
		t.Error("tampered cache was not repaired from the verified download")
	}
}
