package store

import (
	"context"
	"errors"
	"testing"

	"lattix/shared"
)

func ptr(v string) *string { return &v }

func TestDefaultServerSettingsCreatesDefaults(t *testing.T) {
	st, err := Open(t.TempDir() + "/server-settings.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	settings, revision, err := st.DefaultServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1", revision)
	}
	if settings.XrayVersion == nil || *settings.XrayVersion != "latest" {
		t.Fatalf("xray_version = %v, want latest", settings.XrayVersion)
	}
	// 再次读取不重复建默认、revision 不漂移。
	_, again, err := st.DefaultServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != 1 {
		t.Fatalf("second read revision = %d, want 1", again)
	}
}

func TestUpdateDefaultServerSettingsIncrementsRevision(t *testing.T) {
	st, err := Open(t.TempDir() + "/server-settings-update.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, before, err := st.DefaultServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desired := shared.ServerSettings{XrayVersion: ptr("v1.8.24")}
	revision, err := st.UpdateDefaultServerSettings(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	if revision != before+1 {
		t.Fatalf("revision = %d, want %d", revision, before+1)
	}
	settings, gotRevision, err := st.DefaultServerSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotRevision != revision || settings.XrayVersion == nil || *settings.XrayVersion != "v1.8.24" {
		t.Fatalf("stored = (%+v, %d)", settings, gotRevision)
	}
	if _, err := st.UpdateDefaultServerSettings(ctx, shared.ServerSettings{XrayVersion: ptr("bad")}); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestServerCustomSettingsOverrideAndClear(t *testing.T) {
	st, err := Open(t.TempDir() + "/server-custom.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, "s1", "", "tok", MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	// 默认无覆盖。
	custom, err := st.ServerCustomSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if custom != nil {
		t.Fatalf("custom = %+v, want nil", custom)
	}
	// 字段级覆盖。
	override := shared.ServerSettings{XrayVersion: ptr("v1.8.10")}
	if err := st.UpdateServerCustomSettings(ctx, serverID, &override); err != nil {
		t.Fatal(err)
	}
	custom, err = st.ServerCustomSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if custom == nil || custom.Revision != 1 || custom.XrayVersion == nil || *custom.XrayVersion != "v1.8.10" {
		t.Fatalf("custom = %+v", custom)
	}
	// 再次写入 revision+1。
	if err := st.UpdateServerCustomSettings(ctx, serverID, &shared.ServerSettings{XrayVersion: ptr("latest")}); err != nil {
		t.Fatal(err)
	}
	custom, _ = st.ServerCustomSettings(ctx, serverID)
	if custom.Revision != 2 {
		t.Fatalf("custom revision = %d, want 2", custom.Revision)
	}
	// 清除覆盖。
	if err := st.UpdateServerCustomSettings(ctx, serverID, nil); err != nil {
		t.Fatal(err)
	}
	custom, err = st.ServerCustomSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if custom != nil {
		t.Fatalf("custom after clear = %+v, want nil", custom)
	}
}

func TestEffectiveServerSettingsFieldMergeAndRevision(t *testing.T) {
	st, err := Open(t.TempDir() + "/server-effective.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, "s1", "", "tok", MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	// 无覆盖：effective = default。
	settings, revision, err := st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.XrayVersion == nil || *settings.XrayVersion != "latest" || revision != 1 {
		t.Fatalf("effective = (%+v, %d)", settings, revision)
	}
	// 改默认到 v1.8.24 → effective revision = 2。
	if _, err := st.UpdateDefaultServerSettings(ctx, shared.ServerSettings{XrayVersion: ptr("v1.8.24")}); err != nil {
		t.Fatal(err)
	}
	settings, revision, err = st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if *settings.XrayVersion != "v1.8.24" || revision != 2 {
		t.Fatalf("effective = (%+v, %d)", settings, revision)
	}
	// 服务器覆盖 v1.8.10 → effective = custom，revision = 2 + 1 = 3。
	if err := st.UpdateServerCustomSettings(ctx, serverID, &shared.ServerSettings{XrayVersion: ptr("v1.8.10")}); err != nil {
		t.Fatal(err)
	}
	settings, revision, err = st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if *settings.XrayVersion != "v1.8.10" || revision != 3 {
		t.Fatalf("effective = (%+v, %d)", settings, revision)
	}
	// 清除覆盖 → 回到 default，revision = 2 + 0 = 2（单调：不因清除而回退）。
	if err := st.UpdateServerCustomSettings(ctx, serverID, nil); err != nil {
		t.Fatal(err)
	}
	settings, revision, err = st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if *settings.XrayVersion != "v1.8.24" || revision != 2 {
		t.Fatalf("effective after clear = (%+v, %d)", settings, revision)
	}
}

func TestReportServerSettings(t *testing.T) {
	st, err := Open(t.TempDir() + "/server-report.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, "s1", "", "tok", MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReportServerSettings(ctx, serverID, 3, "boom"); err != nil {
		t.Fatal(err)
	}
	srv, err := st.ServerByID(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if srv.ServerSettingsRevision != 3 || srv.ServerSettingsError != "boom" || srv.ServerSettingsReportedAt == nil {
		t.Fatalf("server = %+v", srv)
	}
	// 服务器不存在不报错（与 ReportAgentSettings 一致）。
	if err := st.ReportServerSettings(ctx, 9999, 1, ""); err != nil {
		t.Fatalf("report missing server: %v", err)
	}
	// Effective 对不存在服务器返回 ErrNotFound。
	_, _, err = st.EffectiveServerSettings(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("effective missing server err = %v, want ErrNotFound", err)
	}
}
