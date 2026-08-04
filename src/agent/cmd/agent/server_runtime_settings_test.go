package main

import (
	"errors"
	"testing"
	"time"

	"lattix/shared"
)

func ptr(v string) *string { return &v }

func TestServerRuntimeSettingsApplyAndSnapshot(t *testing.T) {
	runtime := newServerRuntimeSettings(shared.ServerSettingsDocument{})
	if _, _, revision, _ := runtime.snapshot(); revision != 0 {
		t.Fatalf("initial revision = %d, want 0", revision)
	}
	doc := shared.ServerSettingsDocument{
		SchemaVersion: shared.ServerSettingsSchemaVersion,
		Revision:      7,
		Server:        shared.ServerSettings{XrayVersion: ptr("v1.8.24")},
	}
	previous := runtime.apply(doc)
	if previous.XrayVersion != nil {
		t.Fatalf("previous = %v, want nil", previous.XrayVersion)
	}
	settings, _, revision, _ := runtime.snapshot()
	if revision != 7 || settings.XrayVersion == nil || *settings.XrayVersion != "v1.8.24" {
		t.Fatalf("snapshot = (%+v, %d)", settings, revision)
	}
}

func TestShouldReconcileGates(t *testing.T) {
	runtime := newServerRuntimeSettings(shared.ServerSettingsDocument{})
	// 未设置 / latest：不动作。
	runtime.apply(shared.ServerSettingsDocument{SchemaVersion: shared.ServerSettingsSchemaVersion, Revision: 1, Server: shared.ServerSettings{}})
	if version, ok := runtime.shouldReconcile("v1.8.20"); ok {
		t.Fatalf("empty should not reconcile, got %s", version)
	}
	runtime.apply(shared.ServerSettingsDocument{SchemaVersion: shared.ServerSettingsSchemaVersion, Revision: 2, Server: shared.ServerSettings{XrayVersion: ptr("latest")}})
	if version, ok := runtime.shouldReconcile("v1.8.20"); ok {
		t.Fatalf("latest should not reconcile, got %s", version)
	}
	// 固定版本且不一致：触发。
	runtime.apply(shared.ServerSettingsDocument{SchemaVersion: shared.ServerSettingsSchemaVersion, Revision: 3, Server: shared.ServerSettings{XrayVersion: ptr("v1.8.24")}})
	version, ok := runtime.shouldReconcile("v1.8.20")
	if !ok || version != "v1.8.24" {
		t.Fatalf("should reconcile = (%s, %v), want (v1.8.24, true)", version, ok)
	}
	// 版本已一致：跳过。
	if _, ok := runtime.shouldReconcile("v1.8.24"); ok {
		t.Fatal("matching version should not reconcile")
	}
	// Xray 自报版本无 v 前缀（"1.8.24"）：同样视为一致，跳过。
	if _, ok := runtime.shouldReconcile("1.8.24"); ok {
		t.Fatal("matching version without v prefix should not reconcile")
	}
	// 失败冷却：同版本 30 分钟内不重试。
	runtime.markAttempt("v1.8.24", errors.New("boom"))
	if _, ok := runtime.shouldReconcile("v1.8.20"); ok {
		t.Fatal("cooldown should suppress retry")
	}
	runtime.lastAttemptAt = time.Now().Add(-31 * time.Minute)
	if _, ok := runtime.shouldReconcile("v1.8.20"); !ok {
		t.Fatal("cooldown expired should retry")
	}
	// 版本变更：无视冷却立即重试。
	runtime.markAttempt("v1.8.24", errors.New("boom"))
	runtime.apply(shared.ServerSettingsDocument{SchemaVersion: shared.ServerSettingsSchemaVersion, Revision: 4, Server: shared.ServerSettings{XrayVersion: ptr("v1.8.25")}})
	if _, ok := runtime.shouldReconcile("v1.8.20"); !ok {
		t.Fatal("version change should retry immediately")
	}
}

func TestMarkAttemptClearsStaleErrorOnSuccess(t *testing.T) {
	runtime := newServerRuntimeSettings(shared.ServerSettingsDocument{})
	runtime.markAttempt("v1.8.24", errors.New("boom"))
	if _, _, _, err := runtime.snapshot(); err != "boom" {
		t.Fatalf("lastApplyError = %q after failure, want boom", err)
	}
	runtime.markAttempt("v1.8.24", nil)
	if _, _, _, err := runtime.snapshot(); err != "" {
		t.Fatalf("lastApplyError = %q after success, want empty", err)
	}
}

func TestServerRuntimeSettingsBeginReconcileExcludesConcurrent(t *testing.T) {
	runtime := newServerRuntimeSettings(shared.ServerSettingsDocument{})
	if !runtime.beginReconcile() {
		t.Fatal("first begin should succeed")
	}
	if runtime.beginReconcile() {
		t.Fatal("second begin should fail while reconciling")
	}
	runtime.endReconcile()
	if !runtime.beginReconcile() {
		t.Fatal("begin after end should succeed")
	}
	runtime.endReconcile()
}

func TestServerRuntimeSettingsFail(t *testing.T) {
	runtime := newServerRuntimeSettings(shared.ServerSettingsDocument{})
	runtime.fail("apply failed")
	_, _, _, err := runtime.snapshot()
	if err != "apply failed" {
		t.Fatalf("lastApplyError = %q", err)
	}
	runtime.resetForPanelRebind()
	_, _, revision, lastErr := runtime.snapshot()
	if revision != 0 || lastErr != "" {
		t.Fatalf("after reset = (%d, %q)", revision, lastErr)
	}
}
