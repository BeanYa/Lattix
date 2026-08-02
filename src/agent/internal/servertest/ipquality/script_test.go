package ipquality

import (
	"context"
	"os"
	"path/filepath"
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
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
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
