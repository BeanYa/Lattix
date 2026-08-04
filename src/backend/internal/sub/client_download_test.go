package sub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestClientDownloadSpecsMatchCurrentReleaseAssets(t *testing.T) {
	tests := map[string]string{
		"clash-verge-windows-x64":           "Clash.Verge_2.5.2_x64-setup.exe",
		"clash-verge-macos-arm64":           "Clash.Verge_2.5.2_aarch64.dmg",
		"mihomo-party-windows-x64":          "mihomo-party-windows-2.0.0-x64-setup.exe",
		"mihomo-party-windows-x64-portable": "mihomo-party-windows-2.0.0-x64-portable.7z",
		"flclash-android-armv7":             "FlClash-0.8.94-android-armeabi-v7a.apk",
		"surfboard-android-x86":             "mobile-x86-release.apk",
		"singbox-android-x86":               "SFA-1.13.16-x86.apk",
		"v2rayng-android-x86":               "v2rayNG_2.2.6_x86.apk",
		"nekobox-android-x86":               "NekoBox-1.4.2-x86.apk",
	}
	for id, filename := range tests {
		spec, ok := clientDownloadSpecs[id]
		if !ok {
			t.Fatalf("missing client download spec %q", id)
		}
		if !spec.Pattern.MatchString(filename) {
			t.Errorf("spec %q does not match %q", id, filename)
		}
	}
}

func TestSafeClientFilename(t *testing.T) {
	if got := safeClientFilename(`../../client".zip`); got != "client_.zip" {
		t.Fatalf("safeClientFilename() = %q", got)
	}
	if got := safeClientFilename(""); got != "client-package.bin" {
		t.Fatalf("empty filename = %q", got)
	}
}

type cacheProbeRequester struct {
	releaseJSON     string
	downloadContent []byte
	metadataCalls   int
	downloadCalls   int
}

func (f *cacheProbeRequester) GetText(context.Context, string, int64) (string, error) {
	f.metadataCalls++
	if f.releaseJSON == "" {
		return "", errors.New("missing release fixture")
	}
	return f.releaseJSON, nil
}

func (f *cacheProbeRequester) Download(_ context.Context, _ string, path string, onProgress func(float64)) error {
	f.downloadCalls++
	if f.downloadContent == nil {
		return errors.New("download fixture is not configured")
	}
	if err := os.WriteFile(path, f.downloadContent, 0o600); err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(1)
	}
	return nil
}

func TestClientDownloadReusesCacheWhenReleaseAssetIsUnchanged(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cacheDir := t.TempDir()
	server := NewWithCacheDir(st, nil, nil, cacheDir)
	spec := clientDownloadSpecs["flclash-android-arm64"]
	probe := &cacheProbeRequester{releaseJSON: `{"tag_name":"v0.8.94","assets":[{"name":"FlClash-0.8.94-android-arm64-v8a.apk","browser_download_url":"https://example.invalid/cached.apk","size":14}]}`}
	server.downloadFiles = probe
	task := &clientDownloadTask{ID: "cache-hit-task", VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	server.downloadTasks[task.ID] = task
	server.activeDownloads[spec.ID] = task.ID

	targetDir := filepath.Join(cacheDir, spec.ID)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("cached package")
	if err := os.WriteFile(filepath.Join(targetDir, "latest.bin"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(clientCacheMetadata{
		VariantID: spec.ID, ReleaseTag: "v0.8.94", AssetURL: "https://example.invalid/cached.apk", Filename: "cached.apk",
		Size: int64(len(content)), DownloadedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "latest.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	server.runClientDownload(task.ID, spec)
	if task.Status != "done" || task.Progress != 1 || task.Filename != "cached.apk" {
		t.Fatalf("cache task = %+v", task)
	}
	if probe.metadataCalls != 1 || probe.downloadCalls != 0 {
		t.Fatalf("upstream calls on cache hit: metadata=%d download=%d", probe.metadataCalls, probe.downloadCalls)
	}
}

func TestClientDownloadReplacesCacheWhenReleaseAssetChanges(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cacheDir := t.TempDir()
	server := NewWithCacheDir(st, nil, nil, cacheDir)
	spec := clientDownloadSpecs["flclash-android-arm64"]
	newContent := []byte("new package")
	probe := &cacheProbeRequester{
		releaseJSON:     `{"tag_name":"v0.8.95","assets":[{"name":"FlClash-0.8.95-android-arm64-v8a.apk","browser_download_url":"https://example.invalid/new.apk","size":11}]}`,
		downloadContent: newContent,
	}
	server.downloadFiles = probe
	task := &clientDownloadTask{ID: "cache-replace-task", VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	server.downloadTasks[task.ID] = task
	server.activeDownloads[spec.ID] = task.ID

	targetDir := filepath.Join(cacheDir, spec.ID)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldContent := []byte("old package")
	if err := os.WriteFile(filepath.Join(targetDir, "latest.bin"), oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMetadata, err := json.Marshal(clientCacheMetadata{
		VariantID: spec.ID, ReleaseTag: "v0.8.94", AssetURL: "https://example.invalid/old.apk", Filename: "old.apk",
		Size: int64(len(oldContent)), DownloadedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "latest.json"), oldMetadata, 0o600); err != nil {
		t.Fatal(err)
	}

	server.runClientDownload(task.ID, spec)
	gotContent, err := os.ReadFile(filepath.Join(targetDir, "latest.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(newContent) || task.Status != "done" || probe.downloadCalls != 1 {
		t.Fatalf("replacement task=%+v content=%q downloads=%d", task, gotContent, probe.downloadCalls)
	}
	metadata, ok := readClientCacheMetadata(filepath.Join(targetDir, "latest.json"))
	if !ok || metadata.AssetURL != "https://example.invalid/new.apk" || metadata.Filename != "FlClash-0.8.95-android-arm64-v8a.apk" {
		t.Fatalf("replacement metadata=%+v ok=%v", metadata, ok)
	}
}
