# 服务器设置（ServerSettings）默认 + 逐服务器覆盖 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 面板级服务器设置默认值（defaultsetting）+ 每台服务器字段级覆盖（customsetting），合并后的生效设置经平行 sync 通道下发 agent，agent 自动对齐 xray 版本。

**Architecture:** 照抄现有 AgentSettings 同步机制（拉取→比对 revision→下发文档→落盘→回执），但逐服务器计算生效值。面板默认存 `settings` 表（revisioned JSON），服务器覆盖存 `servers.custom_settings` 列（JSON 内带 revision），effective revision = default.revision + custom.revision。Agent 收到固定版本且与当前 xray 不一致时自动 `UpgradeXray`，latest/nil 不动作，失败冷却重试。

**Tech Stack:** Go 1.26（shared/backend/agent 三 module，go.work）、SQLite（modernc）、React 19 + TypeScript + Vite（frontend）。

## Global Constraints

- 版本号合法值：`""`、`"latest"`、`vX.Y.Z`（正则 `^v[0-9]+\.[0-9]+\.[0-9]+$`）。
- effective revision = default.revision + custom.revision，必须单调递增。
- 字段级合并：custom 中 JSON 出现的字段覆盖 default；custom `{}` = 清除覆盖。
- 消息类型遵循 `domain.action` 命名：`server.settings.sync` / `server.settings.changed`。
- 不破坏现有 AgentSettings 机制；两套 sync 通道平行共存。
- 旧 agent 兼容：未知消息类型走现有 `recv unknown type` 兜底。
- 状态列命名沿用 `agent_settings_*` 模式：`server_settings_revision/_error/_reported_at`。
- 测试命令：backend/shared/agent 各 module 内 `go test ./...`；frontend `npm test`（vitest）+ `npm run check:api` + `npx tsc -b`。

---

### Task 1: shared 协议类型与校验

**Files:**
- Create: `src/shared/server_settings.go`
- Create: `src/shared/server_settings_test.go`
- Modify: `src/shared/messages.go:73-94`（消息类型常量区）

**Interfaces:**
- Produces（后续所有任务依赖）:
  - `shared.ServerSettingsSchemaVersion = 1`
  - `shared.ServerSettings struct { XrayVersion *string }`（JSON tag `xray_version,omitempty`）
  - `shared.ServerSettingsDocument struct { SchemaVersion int; Revision int64; Server ServerSettings }`（JSON tag `revision`、`server`）
  - `shared.ServerSettingsSyncPayload { PanelInstanceID, AppliedRevision int64, LastApplyError }`
  - `shared.ServerSettingsSyncResult { Changed bool; Settings *ServerSettingsDocument }`
  - `shared.ServerSettingsChangedPayload { Revision int64 }`
  - `shared.TypeServerSettingsSync = "server.settings.sync"`、`shared.TypeServerSettingsChanged = "server.settings.changed"`
  - `shared.DefaultServerSettings() ServerSettings`（xray_version="latest"）
  - `shared.ValidateXrayVersion(string) error`、`(ServerSettings).Validate() error`、`(ServerSettingsDocument).Validate() error`

**说明（相对 spec 的细化）**：`ServerSettingsDocument` 增加 `Revision int64` 字段——agent 需要知道自己已应用的 effective revision 才能回执（与 `AgentSettings.Revision` 语义一致）。

- [ ] **Step 1: 写失败测试**

创建 `src/shared/server_settings_test.go`：

```go
package shared

import (
	"encoding/json"
	"testing"
)

func TestValidateXrayVersion(t *testing.T) {
	for _, ok := range []string{"", "latest", "v1.8.24", "v0.0.1"} {
		if err := ValidateXrayVersion(ok); err != nil {
			t.Fatalf("ValidateXrayVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"1.8.24", "latest ", "v1.8", "xray-core", "v1.8.24.1"} {
		if err := ValidateXrayVersion(bad); err == nil {
			t.Fatalf("ValidateXrayVersion(%q) = nil, want error", bad)
		}
	}
}

func TestDefaultServerSettings(t *testing.T) {
	settings := DefaultServerSettings()
	if settings.XrayVersion == nil || *settings.XrayVersion != "latest" {
		t.Fatalf("default xray_version = %v, want latest", settings.XrayVersion)
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("default validate: %v", err)
	}
}

func TestServerSettingsDocumentValidate(t *testing.T) {
	good := ServerSettingsDocument{SchemaVersion: ServerSettingsSchemaVersion, Revision: 3, Server: DefaultServerSettings()}
	if err := good.Validate(); err != nil {
		t.Fatalf("good doc validate: %v", err)
	}
	bad := []ServerSettingsDocument{
		{SchemaVersion: 0, Revision: 1, Server: DefaultServerSettings()},
		{SchemaVersion: ServerSettingsSchemaVersion, Revision: 0, Server: DefaultServerSettings()},
		{SchemaVersion: ServerSettingsSchemaVersion, Revision: 1, Server: ServerSettings{}},
	}
	for i, doc := range bad {
		if err := doc.Validate(); err == nil {
			t.Fatalf("bad doc %d validate = nil, want error", i)
		}
	}
}

func TestServerSettingsDocumentJSONRoundTrip(t *testing.T) {
	version := "v1.8.24"
	doc := ServerSettingsDocument{
		SchemaVersion: ServerSettingsSchemaVersion,
		Revision:      5,
		Server:        ServerSettings{XrayVersion: &version},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ServerSettingsDocument
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Revision != 5 || decoded.Server.XrayVersion == nil || *decoded.Server.XrayVersion != "v1.8.24" {
		t.Fatalf("round trip = %+v", decoded)
	}
	// nil 指针 + omitempty：未设置字段序列化后不出现。
	var empty ServerSettings
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("empty settings = %s, want {}", raw)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/shared`）：`go test ./... -run TestValidateXrayVersion`
Expected: 编译失败 `undefined: ValidateXrayVersion`。

- [ ] **Step 3: 实现**

创建 `src/shared/server_settings.go`：

```go
package shared

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	ServerSettingsSchemaVersion = 1
)

var xrayVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// ServerSettings 是面板默认（defaultsetting）与服务器覆盖（customsetting）共用的
// 服务器设置模型（字段级覆盖，可扩展）。XrayVersion 为 nil = 未设置：
// default 中未设置 = 不自动对齐；custom 中未设置 = 继承 default。
type ServerSettings struct {
	XrayVersion *string `json:"xray_version,omitempty"`
}

// ServerSettingsDocument 是面板下发生效文档（agent 落盘，Task 6 使用）。
// Revision 是面板计算的 effective revision，agent 原样回执。
type ServerSettingsDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Revision      int64          `json:"revision"`
	Server        ServerSettings `json:"server"`
}

// ServerSettingsSyncPayload 是 agent 上报已应用 revision 的载荷（照抄 AgentSettingsSyncPayload）。
type ServerSettingsSyncPayload struct {
	PanelInstanceID string `json:"panel_instance_id"`
	AppliedRevision int64  `json:"applied_revision"`
	LastApplyError  string `json:"last_apply_error,omitempty"`
}

// ServerSettingsSyncResult 是面板对 sync 请求的响应；Changed 时携带完整文档。
type ServerSettingsSyncResult struct {
	Changed  bool                     `json:"changed"`
	Settings *ServerSettingsDocument `json:"settings,omitempty"`
}

// ServerSettingsChangedPayload 是面板变更通知事件的载荷。
type ServerSettingsChangedPayload struct {
	Revision int64 `json:"revision"`
}

// ValidateXrayVersion 校验 xray 版本取值：空 | latest | vX.Y.Z。
func ValidateXrayVersion(version string) error {
	if version == "" || version == "latest" || xrayVersionPattern.MatchString(version) {
		return nil
	}
	return fmt.Errorf("xray 版本须为空、latest 或 vX.Y.Z: %s", version)
}

func (s ServerSettings) Validate() error {
	if s.XrayVersion != nil {
		return ValidateXrayVersion(*s.XrayVersion)
	}
	return nil
}

func (d ServerSettingsDocument) Validate() error {
	if d.SchemaVersion != ServerSettingsSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", d.SchemaVersion)
	}
	if d.Revision < 1 {
		return errors.New("server settings revision must be at least 1")
	}
	return d.Server.Validate()
}

// DefaultServerSettings 返回面板默认值：latest（保持现状行为，不自动对齐）。
func DefaultServerSettings() ServerSettings {
	version := "latest"
	return ServerSettings{XrayVersion: &version}
}
```

在 `src/shared/messages.go` 的常量区（`TypeSettingsChanged` 行后）追加：

```go
	TypeServerSettingsSync    = "server.settings.sync"
	TypeServerSettingsChanged = "server.settings.changed"
```

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/shared`）：`go test ./...`
Expected: PASS（`TestValidateXrayVersion`、`TestDefaultServerSettings`、`TestServerSettingsDocumentValidate`、`TestServerSettingsDocumentJSONRoundTrip` 全绿）。

- [ ] **Step 5: 提交**

```bash
git add src/shared/server_settings.go src/shared/server_settings_test.go src/shared/messages.go
git commit -m "feat(shared): server settings model with per-server override protocol"
```

---

### Task 2: store 迁移与存取

**Files:**
- Modify: `src/backend/internal/store/store.go:19-49`（servers 表 Schema）
- Modify: `src/backend/internal/store/migrations.go:14`（schemaVersion）+ `:61-88`（servers 列迁移清单）
- Modify: `src/backend/internal/store/settings.go:14-52`（设置键常量）
- Modify: `src/backend/internal/store/servers.go`（Server struct、serverCols、scanServer、新方法）
- Create: `src/backend/internal/store/server_settings_test.go`

**Interfaces:**
- Consumes: Task 1 的 `shared.ServerSettings`、`shared.DefaultServerSettings()`
- Produces:
  - `store.SettingServerSettings = "server_settings"`
  - `store.serverSettingsValue struct { Revision int64; XrayVersion *string }`（JSON tag `revision`/`xray_version,omitempty`，settings 表与 custom_settings 列共用）
  - `(s *Store) DefaultServerSettings(ctx) (shared.ServerSettings, int64, error)` — 读默认 + revision，空则首建 `{"revision":1,"xray_version":"latest"}`
  - `(s *Store) UpdateDefaultServerSettings(ctx, desired shared.ServerSettings) (int64, error)` — 校验 + revision+1 + upsert（事务）
  - `(s *Store) ServerCustomSettings(ctx, id int64) (*serverSettingsValue, error)` — 无覆盖返回 (nil, nil)；服务器不存在返回 ErrNotFound
  - `(s *Store) UpdateServerCustomSettings(ctx, id int64, settings *shared.ServerSettings) error` — nil = 清除（写空串）；非空 = 校验 + revision+1 + 写入
  - `(s *Store) EffectiveServerSettings(ctx, id int64) (shared.ServerSettings, int64, error)` — 合并 + effective revision
  - `(s *Store) ReportServerSettings(ctx, id, revision int64, applyError string) error`

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/store/server_settings_test.go`：

```go
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
	if err := st.UpdateDefaultServerSettings(ctx, shared.ServerSettings{XrayVersion: ptr("bad")}); err == nil {
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
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/backend`）：`go test ./internal/store/ -run TestDefaultServerSettingsCreatesDefaults -v`
Expected: 编译失败（`DefaultServerSettings` 未定义）。

- [ ] **Step 3: Schema 迁移**

`src/backend/internal/store/store.go` servers 表 CREATE 语句末尾（`agent_settings_reported_at DATETIME,` 之后、`created_at` 之前）插入：

```sql
    custom_settings TEXT    NOT NULL DEFAULT '', -- 服务器级覆盖（JSON：{revision, xray_version, ...}，字段级覆盖面板默认）
    server_settings_revision INTEGER NOT NULL DEFAULT 0, -- agent 上报的已应用 effective revision
    server_settings_error TEXT   NOT NULL DEFAULT '',    -- agent 应用错误信息（透出给列表状态）
    server_settings_reported_at DATETIME,                -- 最近一次上报时间
```

`src/backend/internal/store/migrations.go`：
- `const schemaVersion = 10` → `const schemaVersion = 11`
- servers 列迁移清单（`agent_settings_reported_at` 行后）追加：

```go
		{"custom_settings", "TEXT NOT NULL DEFAULT ''"},
		{"server_settings_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"server_settings_error", "TEXT NOT NULL DEFAULT ''"},
		{"server_settings_reported_at", "DATETIME"},
```

- [ ] **Step 4: store/servers.go 扩展**

`Server` struct（`AgentSettingsReportedAt` 后）追加：

```go
	CustomSettings          string // 服务器级覆盖 JSON（空串 = 无覆盖）
	ServerSettingsRevision  int64
	ServerSettingsError     string
	ServerSettingsReportedAt *time.Time
```

`serverCols` 末尾（`agent_settings_reported_at, created_at` → 中间插入）：

```go
const serverCols = `id, alias, token, last_seen_at, xray_version, agent_version, address, address_mode, learned_addr, nic_addresses, config_drift, machine_type, allowed_ports, tags, country_code, location, credential_epoch, credential_committed, credential_pending_token, credential_exchange_id, last_connected_at, last_disconnected_at, last_reconnected_at, reconnect_count, last_disconnect_reason, agent_settings_revision, agent_settings_error, agent_settings_reported_at, custom_settings, server_settings_revision, server_settings_error, server_settings_reported_at, created_at`
```

`scanServer`：变量区加 `var serverSettingsReported sql.NullTime`；Scan 参数在 `&srv.AgentSettingsReportedAt` 后追加 `&srv.CustomSettings, &srv.ServerSettingsRevision, &srv.ServerSettingsError, &serverSettingsReported`；`settingsReported.Valid` 块后追加：

```go
	if serverSettingsReported.Valid {
		t := serverSettingsReported.Time
		srv.ServerSettingsReportedAt = &t
	}
```

文件末尾（`ReportAgentSettings` 之后）追加方法：

```go
// ServerCustomSettings 读取服务器覆盖；无覆盖返回 (nil, nil)。
func (s *Store) ServerCustomSettings(ctx context.Context, id int64) (*serverSettingsValue, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT custom_settings FROM servers WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get server custom settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var value serverSettingsValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode server custom settings: %w", err)
	}
	return &value, nil
}

// UpdateServerCustomSettings 写入服务器覆盖；settings 为 nil 时清除覆盖。
// 每次写入 revision+1，保证 effective revision 单调递增（清除也递增语义由 EffectiveServerSettings 处理）。
func (s *Store) UpdateServerCustomSettings(ctx context.Context, id int64, settings *shared.ServerSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT custom_settings FROM servers WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get server custom settings: %w", err)
	}
	revision := int64(0)
	if strings.TrimSpace(raw) != "" {
		var current serverSettingsValue
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return fmt.Errorf("decode server custom settings: %w", err)
		}
		revision = current.Revision
	}
	if settings == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE servers SET custom_settings = '' WHERE id = ?`, id); err != nil {
			return fmt.Errorf("clear server custom settings: %w", err)
		}
		return tx.Commit()
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	value := serverSettingsValue{Revision: revision + 1, XrayVersion: settings.XrayVersion}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE servers SET custom_settings = ? WHERE id = ?`, string(encoded), id); err != nil {
		return fmt.Errorf("save server custom settings: %w", err)
	}
	return tx.Commit()
}

// EffectiveServerSettings 返回服务器生效设置 = 面板默认 + 字段级覆盖；
// effective revision = default.revision + custom.revision（单调递增）。
func (s *Store) EffectiveServerSettings(ctx context.Context, id int64) (shared.ServerSettings, int64, error) {
	settings, revision, err := s.DefaultServerSettings(ctx)
	if err != nil {
		return shared.ServerSettings{}, 0, err
	}
	custom, err := s.ServerCustomSettings(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return shared.ServerSettings{}, 0, err
	}
	if err != nil {
		return shared.ServerSettings{}, 0, err
	}
	if custom != nil {
		if custom.XrayVersion != nil {
			settings.XrayVersion = custom.XrayVersion
		}
		revision += custom.Revision
	}
	return settings, revision, nil
}

// ReportServerSettings records the last effective revision the Agent applied.
func (s *Store) ReportServerSettings(ctx context.Context, id, revision int64, applyError string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers
		 SET server_settings_revision = ?, server_settings_error = ?, server_settings_reported_at = CURRENT_TIMESTAMP
		 WHERE id = ?`, revision, applyError, id)
	return err
}
```

（`servers.go` 需补充 import：`encoding/json`、`strings`。）

- [ ] **Step 5: store/settings.go 扩展**

设置键常量区（`SettingAgentSettings` 行后）追加：

```go
	SettingServerSettings         = "server_settings"          // 面板级服务器设置默认值（JSON：{revision, xray_version}）
```

文件末尾追加：

```go
// serverSettingsValue 是 settings 表默认值与 servers.custom_settings 共用的存储结构。
type serverSettingsValue struct {
	Revision    int64   `json:"revision"`
	XrayVersion *string `json:"xray_version,omitempty"`
}

// DefaultServerSettings 返回面板级默认服务器设置与当前 revision，首次读取时建默认
// （xray_version=latest，保持现状行为）。
func (s *Store) DefaultServerSettings(ctx context.Context) (shared.ServerSettings, int64, error) {
	raw, err := s.GetSetting(ctx, SettingServerSettings)
	if err != nil {
		return shared.ServerSettings{}, 0, err
	}
	if raw == "" {
		value := serverSettingsValue{Revision: 1, XrayVersion: shared.DefaultServerSettings().XrayVersion}
		encoded, err := json.Marshal(value)
		if err != nil {
			return shared.ServerSettings{}, 0, err
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE settings.value = ''`,
			SettingServerSettings, string(encoded)); err != nil {
			return shared.ServerSettings{}, 0, err
		}
		raw = string(encoded)
	}
	var value serverSettingsValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return shared.ServerSettings{}, 0, fmt.Errorf("decode server settings: %w", err)
	}
	if err := shared.ValidateXrayVersion(xrayVersionOrEmpty(value.XrayVersion)); err != nil {
		return shared.ServerSettings{}, 0, fmt.Errorf("validate stored server settings: %w", err)
	}
	settings := shared.ServerSettings{XrayVersion: value.XrayVersion}
	return settings, value.Revision, nil
}

// UpdateDefaultServerSettings 替换面板默认并在同一事务内递增 revision。
func (s *Store) UpdateDefaultServerSettings(ctx context.Context, desired shared.ServerSettings) (int64, error) {
	if err := desired.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	revision := int64(0)
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, SettingServerSettings).Scan(&raw)
	if err == nil {
		var current serverSettingsValue
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return 0, fmt.Errorf("decode server settings: %w", err)
		}
		revision = current.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("get server settings: %w", err)
	}
	value := serverSettingsValue{Revision: revision + 1, XrayVersion: desired.XrayVersion}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		SettingServerSettings, string(encoded)); err != nil {
		return 0, fmt.Errorf("save server settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return value.Revision, nil
}

func xrayVersionOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
```

- [ ] **Step 6: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/store/...`
Expected: PASS（含既有测试——确认迁移没破坏老库）。

- [ ] **Step 7: 提交**

```bash
git add src/backend/internal/store/
git commit -m "feat(store): server settings default/custom storage with effective revision"
```

---

### Task 3: dispatcher 平行 sync 通道

**Files:**
- Modify: `src/backend/internal/dispatch/dispatcher.go`
- Create: `src/backend/internal/dispatch/server_settings_test.go`

**Interfaces:**
- Consumes: Task 1 类型、Task 2 store 方法
- Produces:
  - `(d *Dispatcher) handleServerSettingsSync(serverID int64, env shared.Envelope)`
  - `(d *Dispatcher) NotifyServerSettingsChanged(ctx context.Context, serverID int64, revision int64)` — serverID=0 通知全部在线
  - `HandleMessage` 新增 `TypeServerSettingsSync` 分支

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/dispatch/server_settings_test.go`：

```go
package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func ptr(v string) *string { return &v }

func TestServerSettingsSyncDeliversChangedDocument(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, "s1", "", "tok", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)

	// 期望版本：默认 latest（revision 1）→ 无变化时不回文档。
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"","applied_revision":1}`),
	})
	// 先触发面板默认变更 → 通知全部。
	if _, err := st.UpdateDefaultServerSettings(ctx, shared.ServerSettings{XrayVersion: ptr("v1.8.24")}); err != nil {
		t.Fatal(err)
	}
	dispatcher.NotifyServerSettingsChanged(ctx, 0, 2)
	// 服务器覆盖 → 仅通知该服务器。
	if err := st.UpdateServerCustomSettings(ctx, serverID, &shared.ServerSettings{XrayVersion: ptr("v1.8.10")}); err != nil {
		t.Fatal(err)
	}
	dispatcher.NotifyServerSettingsChanged(ctx, serverID, 3)

	// 模拟 agent 拉取：applied_revision=1 < effective 3 → 回文档。
	var response shared.Envelope
	select {
	case envelope := <-requester.wake:
		_ = envelope
	case <-time.After(time.Second):
		t.Fatal("notify not delivered")
	}
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"x","applied_revision":1}`),
	})
	requester.mu.Lock()
	sent := append([]shared.Envelope(nil), requester.sent...)
	requester.mu.Unlock()
	var last shared.Envelope
	for _, envelope := range sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync {
			last = envelope
		}
	}
	if last.Type != shared.TypeServerSettingsSync || last.Code != shared.CodeOK {
		t.Fatalf("no sync response, sent=%+v", sent)
	}
	var result shared.ServerSettingsSyncResult
	if err := json.Unmarshal(last.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Settings == nil {
		t.Fatalf("result = %+v, want changed document", result)
	}
	if result.Settings.Revision != 3 {
		t.Fatalf("revision = %d, want 3", result.Settings.Revision)
	}
	if result.Settings.Server.XrayVersion == nil || *result.Settings.Server.XrayVersion != "v1.8.10" {
		t.Fatalf("effective version = %v, want v1.8.10", result.Settings.Server.XrayVersion)
	}
	srv, err := st.ServerByID(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if srv.ServerSettingsRevision != 1 {
		t.Fatalf("reported revision = %d, want 1", srv.ServerSettingsRevision)
	}
}

func TestServerSettingsSyncNoChangeWhenUpToDate(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(context.Background(), "s1", "", "tok", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"","applied_revision":1}`),
	})
	requester.mu.Lock()
	var result shared.ServerSettingsSyncResult
	for _, envelope := range requester.sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync {
			_ = json.Unmarshal(envelope.Data, &result)
		}
	}
	requester.mu.Unlock()
	if result.Changed {
		t.Fatalf("expected no change, got %+v", result)
	}
}

func TestServerSettingsSyncInvalidPayload(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	dispatcher.HandleMessage(1, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`not-json`),
	})
	requester.mu.Lock()
	defer requester.mu.Unlock()
	found := false
	for _, envelope := range requester.sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync && envelope.Code == shared.CodeInvalidArgument {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected INVALID_ARGUMENT response, sent=%+v", requester.sent)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/backend`）：`go test ./internal/dispatch/ -run TestServerSettingsSync -v`
Expected: 编译失败（`handleServerSettingsSync` 不存在）。

- [ ] **Step 3: 实现**

`HandleMessage` 的 `case shared.TypeSettingsSync:` 行后追加：

```go
		case shared.TypeServerSettingsSync:
			d.handleServerSettingsSync(serverID, env)
```

`handleAgentSettingsSync` 函数之后追加（照抄其结构）：

```go
// handleServerSettingsSync 处理 agent.settings.sync 的平行通道：agent 上报已应用
// effective revision，面板比对后返回逐服务器合并的 ServerSettingsDocument。
func (d *Dispatcher) handleServerSettingsSync(serverID int64, env shared.Envelope) {
	ctx := context.Background()
	var payload shared.ServerSettingsSyncPayload
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInvalidArgument, "invalid server settings sync payload", nil)
		return
	}
	if len(payload.LastApplyError) > 512 {
		payload.LastApplyError = payload.LastApplyError[:512]
	}
	if err := d.st.ReportServerSettings(ctx, serverID, payload.AppliedRevision, payload.LastApplyError); err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to record server settings status", nil)
		return
	}
	settings, revision, err := d.st.EffectiveServerSettings(ctx, serverID)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load server settings", nil)
		return
	}
	panelID, err := d.st.PanelInstanceID(ctx)
	if err != nil {
		d.replyAgentRequest(ctx, serverID, env, shared.CodeInternalError, "failed to load panel identity", nil)
		return
	}
	changed := (payload.PanelInstanceID != "" && payload.PanelInstanceID != panelID) ||
		payload.AppliedRevision != revision ||
		payload.LastApplyError != ""
	result := shared.ServerSettingsSyncResult{Changed: changed}
	if changed {
		result.Settings = &shared.ServerSettingsDocument{
			SchemaVersion: shared.ServerSettingsSchemaVersion,
			Revision:      revision,
			Server:        settings,
		}
	}
	d.replyAgentRequest(ctx, serverID, env, shared.CodeOK, "", result)
}
```

`NotifyAgentSettingsChanged` 之后追加：

```go
// NotifyServerSettingsChanged is best effort; agents also pull after session.open
// and periodically. serverID=0 notifies every online server (default changed),
// otherwise only that server (custom override changed).
func (d *Dispatcher) NotifyServerSettingsChanged(ctx context.Context, serverID int64, revision int64) {
	servers, err := d.st.ListServers(ctx)
	if err != nil {
		log.Printf("dispatch: list agents for server settings notification: %v", err)
		return
	}
	for _, server := range servers {
		if serverID != 0 && server.ID != serverID {
			continue
		}
		if !d.req.IsOnline(server.ID) {
			continue
		}
		id := shared.NewMessageID()
		env := shared.Envelope{
			Kind: shared.KindEvent, Type: shared.TypeServerSettingsChanged,
			RequestID: id, TraceID: id,
			Data: marshalMessageData(shared.ServerSettingsChangedPayload{Revision: revision}),
		}
		if err := d.req.Send(ctx, server.ID, env); err != nil {
			log.Printf("dispatch: notify server %d server settings revision %d: %v", server.ID, revision, err)
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/dispatch/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/dispatch/
git commit -m "feat(dispatch): server settings sync channel with per-server effective document"
```

---

### Task 4: 面板设置页 API（defaultsetting）

**Files:**
- Modify: `src/backend/internal/panel/settings.go`
- Modify: `src/backend/internal/panel/settings_test.go`（如存在）——先用 `server_tests.go` 既有测试框架验证编译

**Interfaces:**
- Consumes: Task 1/2/3
- Produces:
  - `settingsDTO.ServerSettings shared.ServerSettings`（JSON `server_settings`）、`settingsDTO.ServerSettingsRevision int64`（JSON `server_settings_revision`）
  - `updateSettingsRequest.ServerSettings *shared.ServerSettings`（JSON `server_settings`，nil = 不变）
  - PUT 保存后调用 `s.disp.NotifyServerSettingsChanged(ctx, 0, revision)`；audit `settings.updated` 记录 `server_settings` 变更

- [ ] **Step 1: 写失败测试**

在 `src/backend/internal/panel/server_tests.go` 末尾（或新建 `settings_server_test.go`）追加：

```go
func TestSettingsServerSettingsRoundTrip(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	// 初始默认 latest。
	body := `{}`
	s.patchJSON(t, "/api/setting/update", body)
	// GET 默认值。
	resp := s.getJSON(t, "/api/setting/get")
	var dto struct {
		ServerSettings         shared.ServerSettings `json:"server_settings"`
		ServerSettingsRevision int64                 `json:"server_settings_revision"`
	}
	if err := json.Unmarshal(resp, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ServerSettingsRevision != 1 || dto.ServerSettings.XrayVersion == nil || *dto.ServerSettings.XrayVersion != "latest" {
		t.Fatalf("default = %+v rev=%d", dto.ServerSettings, dto.ServerSettingsRevision)
	}
	// 保存覆盖值。
	s.patchJSON(t, "/api/setting/update", `{"server_settings":{"xray_version":"v1.8.24"}}`)
	resp = s.getJSON(t, "/api/setting/get")
	if err := json.Unmarshal(resp, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ServerSettingsRevision != 2 || dto.ServerSettings.XrayVersion == nil || *dto.ServerSettings.XrayVersion != "v1.8.24" {
		t.Fatalf("after update = %+v rev=%d", dto.ServerSettings, dto.ServerSettingsRevision)
	}
	// 非法版本被拒绝。
	s.patchError(t, "/api/setting/update", `{"server_settings":{"xray_version":"nope"}}`, http.StatusBadRequest)
}
```

（先确认 `newTestServer`/`getJSON`/`patchJSON`/`patchError` 在 `server_tests.go` 中的确切签名再写，见 Step 2 检查步骤。）

- [ ] **Step 2: 检查测试框架签名**

Read `src/backend/internal/panel/server_tests.go` 找到 `newTestServer`、`getJSON`、`patchJSON`、`patchError` 的签名与用法（它们可能命名为 `doJSON` 等）。若签名不同，按实际签名改写 Step 1 的测试。然后运行确认失败：

Run（workdir `src/backend`）：`go test ./internal/panel/ -run TestSettingsServerSettingsRoundTrip -v`
Expected: 编译失败或断言失败（DTO 字段不存在）。

- [ ] **Step 3: 实现**

`settingsDTO` 结构体（`Agent shared.AgentSettings` 行后）追加：

```go
	ServerSettings         shared.ServerSettings `json:"server_settings"`          // 面板级默认（defaultsetting）
	ServerSettingsRevision int64                 `json:"server_settings_revision"` // 默认值当前 revision
```

`handleGetSettings` 中 `dto.Agent = agentSettings` 之后追加：

```go
	dto.ServerSettings, dto.ServerSettingsRevision, err = s.st.DefaultServerSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
```

`updateSettingsRequest` 结构体（`Agent *shared.AgentSettings` 行后）追加：

```go
	ServerSettings *shared.ServerSettings `json:"server_settings"` // nil = 不变
```

`handleUpdateSettings` 校验区（`req.Agent` 校验块之后）追加：

```go
	if req.ServerSettings != nil {
		if err := req.ServerSettings.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
```

`before` map（`"reporting_currency"` 行前）追加：

```go
		"server_settings":                func() shared.ServerSettings { v, _, _ := s.st.DefaultServerSettings(ctx); return v }(),
```

保存区（`set(store.SettingReportingCurrency, req.ReportingCurrency)` 行后）追加：

```go
	var serverSettingsRevision int64
	if req.ServerSettings != nil {
		serverSettingsRevision, err = s.st.UpdateDefaultServerSettings(ctx, *req.ServerSettings)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		after["server_settings"] = *req.ServerSettings
	}
```

通知区（`if updatedAgent != nil { s.disp.NotifyAgentSettingsChanged(...) }` 之后）追加：

```go
	if req.ServerSettings != nil {
		s.disp.NotifyServerSettingsChanged(ctx, 0, serverSettingsRevision)
	}
```

`after` map 初始化（`"reporting_currency": req.ReportingCurrency,` 行前）追加：

```go
		"server_settings": before["server_settings"],
```

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/panel/...`
Expected: PASS（若 server_tests.go 无对应辅助函数，按 Step 2 实际框架实现）。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/panel/settings.go src/backend/internal/panel/settings_server_test.go
git commit -m "feat(panel): default server settings in settings API"
```

---

### Task 5: 面板服务器 API（customsetting + 生效值）

**Files:**
- Modify: `src/backend/internal/panel/servers.go`
- Modify: `src/backend/internal/panel/server_tests.go`（追加测试）

**Interfaces:**
- Consumes: Task 2 store 方法
- Produces:
  - `serverDTO.CustomSettings *shared.ServerSettings`（JSON `custom_settings`，nil = 无覆盖）
  - `serverDTO.EffectiveXrayVersion string`（JSON `effective_xray_version`，空 = 未设置）
  - `PATCH /api/server/update` 请求新增 `CustomSettings *shared.ServerSettings`：省略 = 不变；`{}`（无字段）或 null = 清除覆盖；非空 = 覆盖。保存后仅通知该服务器 + audit `server.settings.updated`

- [ ] **Step 1: 写失败测试**

`src/backend/internal/panel/server_tests.go` 追加：

```go
func TestServerCustomSettingsOverrideLifecycle(t *testing.T) {
	s := newTestServer(t)
	serverID := s.createTestServer(t, "s1", "")
	// 无覆盖：effective = 默认 latest。
	body := fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":""}`, serverID)
	s.patchJSON(t, "/api/server/update", body)
	resp := s.getJSON(t, "/api/server/list")
	var list []map[string]any
	if err := json.Unmarshal(resp, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if int64(item["id"].(float64)) == serverID {
			found = true
			if item["custom_settings"] != nil {
				t.Fatalf("custom_settings = %v, want nil", item["custom_settings"])
			}
			if item["effective_xray_version"] != "latest" {
				t.Fatalf("effective_xray_version = %v, want latest", item["effective_xray_version"])
			}
		}
	}
	if !found {
		t.Fatal("server not in list")
	}
	// 设置覆盖 v1.8.10。
	s.patchJSON(t, "/api/server/update", fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{"xray_version":"v1.8.10"}}`, serverID))
	resp = s.getJSON(t, "/api/server/list")
	if err := json.Unmarshal(resp, &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if int64(item["id"].(float64)) == serverID {
			custom := item["custom_settings"].(map[string]any)
			if custom["xray_version"] != "v1.8.10" {
				t.Fatalf("custom_settings = %v", item["custom_settings"])
			}
			if item["effective_xray_version"] != "v1.8.10" {
				t.Fatalf("effective_xray_version = %v, want v1.8.10", item["effective_xray_version"])
			}
		}
	}
	// 清除覆盖回退默认。
	s.patchJSON(t, "/api/server/update", fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{}}`, serverID))
	resp = s.getJSON(t, "/api/server/list")
	if err := json.Unmarshal(resp, &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if int64(item["id"].(float64)) == serverID {
			if item["custom_settings"] != nil {
				t.Fatalf("custom_settings after clear = %v, want nil", item["custom_settings"])
			}
			if item["effective_xray_version"] != "latest" {
				t.Fatalf("effective_xray_version after clear = %v, want latest", item["effective_xray_version"])
			}
		}
	}
	// 非法版本拒绝。
	s.patchError(t, "/api/server/update", fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{"xray_version":"bad"}}`, serverID), http.StatusBadRequest)
}
```

- [ ] **Step 2: 运行确认失败**

Run（workdir `src/backend`）：`go test ./internal/panel/ -run TestServerCustomSettingsOverrideLifecycle -v`
Expected: 编译失败或断言失败（DTO 字段不存在）。若 `newTestServer`/`createTestServer`/`patchError` 签名不同，按 `server_tests.go` 实际框架改写。

- [ ] **Step 3: 实现**

`serverDTO`（`AgentSettingsStatus` 行前）追加：

```go
	CustomSettings        *shared.ServerSettings `json:"custom_settings"`          // 服务器覆盖（customsetting），nil = 跟随面板默认
	EffectiveXrayVersion  string                 `json:"effective_xray_version"`   // 合并后的生效 xray 版本，空 = 未设置
```

`toServerDTO` 返回值构造（`AgentSettingsError: srv.AgentSettingsError,` 行前）追加：

```go
		EffectiveXrayVersion:  effectiveXrayVersion(s, srv.ID),
		CustomSettings:        customServerSettings(s, srv.ID),
```

`toServerDTO` 函数末尾（`return serverDTO{...}` 之前）追加辅助函数：

```go
// effectiveXrayVersion 读取合并生效值（读失败按未设置处理，不阻断列表）。
func effectiveXrayVersion(s *Server, id int64) string {
	settings, _, err := s.st.EffectiveServerSettings(context.Background(), id)
	if err != nil || settings.XrayVersion == nil {
		return ""
	}
	return *settings.XrayVersion
}

// customServerSettings 读取服务器覆盖（读失败按无覆盖处理，不阻断列表）。
func customServerSettings(s *Server, id int64) *shared.ServerSettings {
	custom, err := s.st.ServerCustomSettings(context.Background(), id)
	if err != nil || custom == nil {
		return nil
	}
	return &shared.ServerSettings{XrayVersion: custom.XrayVersion}
}
```

`handleUpdateServer` 请求结构体（`TrafficPlan *trafficPlanInput` 行后）追加：

```go
		CustomSettings *shared.ServerSettings `json:"custom_settings"` // 省略 = 不变；{} 或 null = 清除覆盖；非空 = 覆盖
```

`handleUpdateServer` 校验区（`req.TrafficPlan` 校验块之后）追加：

```go
	if req.CustomSettings != nil && req.CustomSettings.XrayVersion != nil {
		if err := req.CustomSettings.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
```

保存区（`UpdateServerGeography` 调用之后）追加：

```go
	settingsChanged := false
	if req.CustomSettings != nil {
		var custom *shared.ServerSettings
		if req.CustomSettings.XrayVersion != nil {
			custom = req.CustomSettings
		}
		if err := s.st.UpdateServerCustomSettings(r.Context(), id, custom); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		settingsChanged = true
	}
```

通知与审计（`handleUpdateServer` 结尾 `s.toServerDTOWithPlans` 返回之前）追加：

```go
	if settingsChanged {
		if _, revision, err := s.st.EffectiveServerSettings(r.Context(), id); err == nil {
			s.disp.NotifyServerSettingsChanged(r.Context(), id, revision)
		}
		sid := id
		after := map[string]any{}
		if req.CustomSettings != nil && req.CustomSettings.XrayVersion != nil {
			after["custom_settings"] = map[string]any{"xray_version": *req.CustomSettings.XrayVersion}
		} else {
			after["custom_settings"] = "已清除"
		}
		s.audit(r, "server.settings.updated", &sid, nil, after)
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/panel/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/panel/servers.go src/backend/internal/panel/server_tests.go
git commit -m "feat(panel): per-server custom settings with effective version in API"
```

---

### Task 6: agent 侧同步与 xray 版本自动对齐

**Files:**
- Modify: `src/agent/internal/state/state.go`（Load/SaveServerSettings）
- Create: `src/agent/cmd/agent/server_runtime_settings.go`
- Modify: `src/agent/cmd/agent/main.go`
- Create: `src/agent/cmd/agent/server_runtime_settings_test.go`

**Interfaces:**
- Consumes: Task 1 类型、`mgr.Version() (string, bool)`、`mgr.UpgradeXray(string) error`（upgrade.go:28，已存在）
- Produces:
  - `state.LoadServerSettings(path) (shared.ServerSettingsDocument, error)`、`state.SaveServerSettings(path, doc) error`
  - `newServerRuntimeSettings(doc) *serverRuntimeSettings`
  - `(r *serverRuntimeSettings) snapshot() (shared.ServerSettings, string, int64, string)`
  - `(r *serverRuntimeSettings) apply(doc shared.ServerSettingsDocument) shared.ServerSettings`（返回旧值）
  - `(r *serverRuntimeSettings) fail(string)`、`resetForPanelRebind()`
  - `(r *serverRuntimeSettings) shouldReconcile(current string) (string, bool)`、`(r *serverRuntimeSettings) markAttempt(version string, err error)`、`(r *serverRuntimeSettings) beginReconcile() bool`、`endReconcile()`
  - `sendServerSettingsSync(sc *safeConn, r *serverRuntimeSettings) error`
  - `handleServerSettingsSyncResponse(sc, env, path, r, mgr)` — 应用 + 落盘 + 回执 + 触发对齐
  - `maybeReconcileXray(mgr, r)` — 冷却期 30 分钟防重试风暴

- [ ] **Step 1: 写失败测试（对齐逻辑）**

创建 `src/agent/cmd/agent/server_runtime_settings_test.go`：

```go
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
```

- [ ] **Step 2: 运行确认失败**

Run（workdir `src/agent`）：`go test ./cmd/agent/ -run TestShouldReconcileGates -v`
Expected: 编译失败（`serverRuntimeSettings` 未定义）。

- [ ] **Step 3: state.go 追加落盘方法**

`src/agent/internal/state/state.go` 末尾追加：

```go
func LoadServerSettings(path string) (shared.ServerSettingsDocument, error) {
	var settings shared.ServerSettingsDocument
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return settings, err
	}
	if err := settings.Validate(); err != nil {
		return shared.ServerSettingsDocument{}, err
	}
	return settings, nil
}

func SaveServerSettings(path string, settings shared.ServerSettingsDocument) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: server_runtime_settings.go**

创建 `src/agent/cmd/agent/server_runtime_settings.go`：

```go
package main

import (
	"sync"
	"time"

	"lattix/shared"
)

// reconcileCooldown 防止固定版本对齐失败后每个 sync 周期无限重试：
// 同版本失败后冷却期内不再尝试，面板持续显示 failed 状态。
const reconcileCooldown = 30 * time.Minute

// serverRuntimeSettings 持有面板下发的服务器生效设置（照抄 runtimeSettings 模式）。
type serverRuntimeSettings struct {
	mu               sync.RWMutex
	value            shared.ServerSettings
	panelInstanceID  string
	appliedRevision  int64
	lastApplyError   string
	changed          chan struct{}
	reconciling      bool
	lastAttemptVersion string
	lastAttemptAt    time.Time
}

func newServerRuntimeSettings(document shared.ServerSettingsDocument) *serverRuntimeSettings {
	r := &serverRuntimeSettings{changed: make(chan struct{})}
	if document.Validate() == nil {
		r.value = document.Server
		r.panelInstanceID = ""
		r.appliedRevision = document.Revision
	}
	return r
}

func (r *serverRuntimeSettings) snapshot() (shared.ServerSettings, string, int64, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value, r.panelInstanceID, r.appliedRevision, r.lastApplyError
}

// apply 应用新文档并返回旧值（供对齐逻辑判断版本变化）。
func (r *serverRuntimeSettings) apply(document shared.ServerSettingsDocument) shared.ServerSettings {
	r.mu.Lock()
	previous := r.value
	r.value = document.Server
	r.panelInstanceID = ""
	r.appliedRevision = document.Revision
	r.lastApplyError = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
	return previous
}

func (r *serverRuntimeSettings) fail(message string) {
	r.mu.Lock()
	r.lastApplyError = message
	r.mu.Unlock()
}

func (r *serverRuntimeSettings) resetForPanelRebind() {
	r.mu.Lock()
	r.value = shared.ServerSettings{}
	r.panelInstanceID = ""
	r.appliedRevision = 0
	r.lastApplyError = ""
	r.lastAttemptVersion = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
}

// shouldReconcile 决定是否触发版本对齐：期望为固定版本、与当前不一致、
// 且（版本与上次尝试不同 或 已过冷却期）。
func (r *serverRuntimeSettings) shouldReconcile(current string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v := r.value.XrayVersion
	if v == nil || *v == "" || *v == "latest" {
		return "", false
	}
	if *v == current {
		return "", false
	}
	if *v == r.lastAttemptVersion && time.Since(r.lastAttemptAt) < reconcileCooldown {
		return "", false
	}
	return *v, true
}

// markAttempt 记录一次对齐尝试；失败时写入 lastApplyError 供回执展示。
func (r *serverRuntimeSettings) markAttempt(version string, err error) {
	r.mu.Lock()
	r.lastAttemptVersion = version
	r.lastAttemptAt = time.Now()
	if err != nil {
		r.lastApplyError = err.Error()
	}
	r.mu.Unlock()
}

func (r *serverRuntimeSettings) beginReconcile() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reconciling {
		return false
	}
	r.reconciling = true
	return true
}

func (r *serverRuntimeSettings) endReconcile() {
	r.mu.Lock()
	r.reconciling = false
	r.mu.Unlock()
}
```

- [ ] **Step 5: main.go 接线**

`main()` flag 区（`settingsPath` flag 后）追加：

```go
	serverSettingsPath := flag.String("server-settings", "/opt/lattix-agent/data/server-settings.json", "面板同步服务器设置文件路径")
```

`main()` 中 `runtime := newRuntimeSettings(document)` 之后追加：

```go
	serverDocument, err := state.LoadServerSettings(*serverSettingsPath)
	if err != nil {
		log.Printf("load server settings: %v (using defaults)", err)
	}
	serverRuntime := newServerRuntimeSettings(serverDocument)
```

`run()` 调用处（`runtime` 参数前）追加 `serverRuntime` 与 `*serverSettingsPath` 实参；`run()` 签名追加：

```go
func run(panel, token, statePath, settingsPath, serverSettingsPath, connectionPath string, mgr *xray.Manager, st *state.State, runtime *runtimeSettings, serverRuntime *serverRuntimeSettings, panelRuntime *panelStateTracker, latency *latencyTracker, testManager *servertest.Manager, commandQueue *persistentCommandQueue) (string, error) {
```

`run()` 内 `sendSettingsSync(sc, runtime)` 之后追加：

```go
	sendServerSettingsSync(sc, serverRuntime)
	maybeReconcileXray(mgr, serverRuntime)
```

周期拉取 goroutine（`sendSettingsSync` 周期循环后）追加一个平行循环：

```go
	go func() {
		for {
			timer := time.NewTimer(time.Duration(48+time.Now().UnixNano()%25) * time.Second)
			select {
			case <-timer.C:
			case <-done:
				if !timer.Stop() {
					<-timer.C
				}
				return
			}
			if err := sendServerSettingsSync(sc, serverRuntime); err != nil {
				return
			}
		}
	}()
```

`handle()` 签名追加 `serverSettingsPath string, serverRuntime *serverRuntimeSettings`（面板切换场景由 revision 不匹配天然覆盖，agent 不追踪 server settings 的面板身份，故 `PanelInstanceID` 恒为空串、面板侧跳过该比较）。更新**全部三处**调用点：
1. `main()` 中 `run()` 调用（约 :114）追加 `*serverSettingsPath, serverRuntime` 实参；
2. `run()` 主循环 `handle(sc, mgr, env, statePath, settingsPath, st, runtime, panelRuntime, latency, testManager, commandQueue)`（约 :440）追加 `serverSettingsPath, serverRuntime`；
3. `commandQueue.Attach(func(envelope) { handle(sc, mgr, envelope, statePath, settingsPath, st, runtime, panelRuntime, latency, testManager, nil) })`（约 :307-309）追加 `serverSettingsPath, serverRuntime`。

`TypeSettingsSync` 响应分支后追加：

```go
	if env.Kind == shared.KindResponse && env.Type == shared.TypeServerSettingsSync {
		handleServerSettingsSyncResponse(sc, env, serverSettingsPath, serverRuntime, mgr)
		return
	}
```

`TypeSettingsChanged` 事件分支后追加：

```go
	if env.Kind == shared.KindEvent && env.Type == shared.TypeServerSettingsChanged {
		if err := sendServerSettingsSync(sc, serverRuntime); err != nil {
			log.Printf("server settings changed pull: %v", err)
		}
		return
	}
```

`handleSettingsSyncResponse` 之后追加：

```go
func sendServerSettingsSync(sc *safeConn, runtime *serverRuntimeSettings) error {
	_, panelID, revision, applyError := runtime.snapshot()
	id := shared.NewMessageID()
	return sc.writeJSON(shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: id, TraceID: id,
		Data: mustJSON(shared.ServerSettingsSyncPayload{
			PanelInstanceID: panelID,
			AppliedRevision: revision,
			LastApplyError:  applyError,
		}),
	})
}

func handleServerSettingsSyncResponse(sc *safeConn, env shared.Envelope, path string, runtime *serverRuntimeSettings, mgr *xray.Manager) {
	if env.Code != shared.CodeOK {
		runtime.fail(env.Message)
		log.Printf("server settings sync failed: %s: %s", env.Code, env.Message)
		return
	}
	var result shared.ServerSettingsSyncResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		runtime.fail("invalid server settings sync response")
		return
	}
	if !result.Changed {
		return
	}
	if result.Settings == nil {
		runtime.fail("server settings sync response omitted settings")
		return
	}
	if err := result.Settings.Validate(); err != nil {
		runtime.fail(err.Error())
		log.Printf("reject panel server settings: %v", err)
		return
	}
	if err := state.SaveServerSettings(path, *result.Settings); err != nil {
		runtime.fail(err.Error())
		log.Printf("save panel server settings: %v", err)
		return
	}
	runtime.apply(*result.Settings)
	log.Printf("applied server settings revision=%d", result.Settings.Revision)
	if err := sendServerSettingsSync(sc, runtime); err != nil {
		log.Printf("confirm server settings revision: %v", err)
	}
	maybeReconcileXray(mgr, runtime)
}

// maybeReconcileXray 自动对齐 xray 版本：期望固定版本且与当前不一致时异步升级；
// 冷却期/升级中/已一致时不动作。latest 与未设置永不自动升级。
func maybeReconcileXray(mgr *xray.Manager, runtime *serverRuntimeSettings) {
	ver, running := mgr.Version()
	if !running {
		ver = ""
	}
	desired, ok := runtime.shouldReconcile(ver)
	if !ok || !runtime.beginReconcile() {
		return
	}
	go func() {
		defer runtime.endReconcile()
		log.Printf("server settings: 对齐 xray 版本 %s（当前 %s）", desired, ver)
		if err := mgr.UpgradeXray(desired); err != nil {
			runtime.markAttempt(desired, err)
			log.Printf("server settings: xray 对齐失败: %v", err)
			return
		}
		runtime.markAttempt(desired, nil)
	}()
}
```

- [ ] **Step 6: 运行测试确认通过**

Run（workdir `src/agent`）：`go test ./...`
Expected: PASS（含既有测试）。

- [ ] **Step 7: 提交**

```bash
git add src/agent/
git commit -m "feat(agent): server settings sync with xray version auto-reconcile"
```

---

### Task 7: 前端（设置页默认 + 服务器覆盖）

**Files:**
- Modify: `src/frontend/src/lib/types.ts`（`PanelSettings`、`UpdateSettingsRequest`、`Server`）
- Modify: `src/frontend/src/lib/api.ts`（`updateServerAddress`/`updateServerPorts` 加 `customSettings` 参数）
- Modify: `src/frontend/src/pages/Settings.tsx`
- Modify: `src/frontend/src/pages/Servers.tsx`

**Interfaces:**
- Consumes: 后端 API `server_settings` / `server_settings_revision` / `custom_settings` / `effective_xray_version`
- Produces: 设置页「服务器设置」区块（xray 版本默认 Select：latest + 具体版本）；服务器编辑对话框 xray 覆盖项（跟随默认 / 指定版本）；升级对话框默认选中 effective 版本

- [ ] **Step 1: types.ts**

`PanelSettings` 接口（`agent: AgentSettings` 行后）追加：

```ts
  server_settings: ServerSettings
  server_settings_revision: number
```

`UpdateSettingsRequest`（`agent: AgentSettings` 行后）追加：

```ts
  server_settings?: ServerSettings
```

`Server` 接口（`agent_settings_reported_at` 行后）追加：

```ts
  custom_settings: ServerSettings | null
  effective_xray_version: string | null
```

`AgentSettings` 接口附近（types.ts 中 `agent: AgentSettings` 定义处上方）新增：

```ts
export interface ServerSettings {
  xray_version?: string | null
}
```

- [ ] **Step 2: api.ts**

`updateServerAddress` 与 `updateServerPorts` 签名追加第 9 个参数：

```ts
    customSettings?: ServerSettings,
```

两处 body 中 `traffic_plan: trafficPlan,` 后追加：

```ts
      ...(customSettings !== undefined ? { custom_settings: customSettings } : {}),
```

- [ ] **Step 3: Settings.tsx 状态与保存**

state 区（`reportingCurrency` 行后）追加：

```ts
  const [serverXrayVersion, setServerXrayVersion] = useState('latest')
  const [xrayVersions, setXrayVersions] = useState<string[]>(['latest'])
```

`useEffect` 加载（`setReportingCurrency(s.reporting_currency)` 行后）追加：

```ts
        setServerXrayVersion(s.server_settings.xray_version ?? 'latest')
```

`useEffect(() => { api.exchangeRates()... })` 后追加：

```ts
  useEffect(() => {
    api.releaseVersions('xray').then((versions) => {
      setXrayVersions(versions.versions)
    }).catch(() => {})
  }, [])
```

`onSave` 的 `updateSettings` body（`reporting_currency: reportingCurrency,` 行后）追加：

```ts
        server_settings: { xray_version: serverXrayVersion },
```

- [ ] **Step 4: Settings.tsx UI**

「巡检任务」Card 之前插入新 Card（参照既有 Card 结构，import 均已存在）：

```tsx
        <Card>
          <CardHeader>
            <CardTitle>服务器设置</CardTitle>
            <CardDescription>
              面板级默认设置（defaultsetting）。服务器未单独覆盖时采用该值；
              xray 版本为具体版本时，agent 收到后会自动对齐升级到该版本；
              latest 保持现状（仅手动升级）。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex flex-col gap-2">
              <Label htmlFor="serverXrayVersion">xray 版本（默认）</Label>
              <Select
                value={serverXrayVersion}
                onValueChange={(value) => value && setServerXrayVersion(value)}
                items={xrayVersions.map((version) => ({ value: version, label: version }))}
              >
                <SelectTrigger id="serverXrayVersion" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {xrayVersions.map((version) => (
                    <SelectItem key={version} value={version}>{version}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                版本列表来自 GitHub release 缓存；当前期望 revision：
                {settings?.server_settings_revision ?? 1}
              </p>
            </div>
          </CardContent>
        </Card>
```

- [ ] **Step 5: Servers.tsx 编辑对话框覆盖项**

state 区（`editTraffic` 相关 state 附近）追加：

```ts
  const [editXrayOverride, setEditXrayOverride] = useState('')
  const [editXrayVersions, setEditXrayVersions] = useState<string[]>(['latest'])
```

编辑对话框打开初始化（`openEditDialog`/`onOpenEdit` 中设置 `editTraffic` 处）追加：

```ts
    setEditXrayOverride(s.custom_settings?.xray_version ?? '')
    setEditXrayVersions(['latest'])
    api.releaseVersions('xray').then((versions) => setEditXrayVersions(versions.versions)).catch(() => {})
```

`onUpdateAddress` 中 `updateServerPorts(...)` 与 `updateServerAddress(...)` 调用追加末参：

```ts
          editXrayOverride ? { xray_version: editXrayOverride } : {},
```

编辑对话框表单内（地址选择区块附近，参照既有 Select 用法）追加：

```tsx
              <div className="space-y-2">
                <Label htmlFor="editXrayOverride">xray 版本（覆盖面板默认）</Label>
                <Select
                  value={editXrayOverride}
                  onValueChange={(value) => value !== undefined && setEditXrayOverride(value)}
                  items={[
                    { value: '', label: '跟随面板默认' },
                    ...editXrayVersions.filter((v) => v !== 'latest').map((version) => ({ value: version, label: version })),
                  ]}
                >
                  <SelectTrigger id="editXrayOverride" className="w-full">
                    <SelectValue placeholder="跟随面板默认" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="">跟随面板默认</SelectItem>
                    {editXrayVersions.filter((v) => v !== 'latest').map((version) => (
                      <SelectItem key={version} value={version}>{version}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
```

服务器行详情（`xray_version` 展示处）追加生效版本提示（找到展示 `s.xray_version` 的位置，在其附近）：

```tsx
            {s.effective_xray_version && (
              <span className="text-xs text-muted-foreground">
                期望版本：{s.effective_xray_version}
                {s.custom_settings ? '（服务器覆盖）' : '（面板默认）'}
              </span>
            )}
```

- [ ] **Step 6: 升级对话框默认选中 effective 版本**

`onOpenUpgrade` 中 `setUpgradeVersion('latest')` 改为：

```ts
    setUpgradeVersion(s.effective_xray_version ?? 'latest')
```

- [ ] **Step 7: 前端验证**

Run（workdir `src/frontend`）：
1. `npm run check:api` — Expected: PASS（未改 openapi.yaml）
2. `npx tsc -b` — Expected: 无类型错误
3. `npm test` — Expected: 既有测试全绿

- [ ] **Step 8: 提交**

```bash
git add src/frontend/src/
git commit -m "feat(frontend): server settings default and per-server xray version override"
```

---

### Task 8: openapi.yaml 文档同步

**Files:**
- Modify: `docs/openapi.yaml`（`SettingUpdateRequest` schema，:811-824）

**Interfaces:**
- 无代码接口；保持 API 契约文档与实现一致

- [ ] **Step 1: 更新 schema**

`SettingUpdateRequest` schema 的 `agent:` 行前追加：

```yaml
        server_settings:
          type: object
          additionalProperties: false
          required: [xray_version]
          properties:
            xray_version:
              type: string
              nullable: true
              default: latest
              description: 面板级默认 xray 版本（latest 或 vX.Y.Z）；服务器可单独覆盖。
```

- [ ] **Step 2: 验证契约生成**

Run（workdir `src/frontend`）：`npm run check:api`
Expected: PASS（生成产物与检查一致）。

- [ ] **Step 3: 提交**

```bash
git add docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts
git commit -m "docs(openapi): server settings default in settings contract"
```

（若 `check:api` 提示生成产物变更，先 `npm run generate:api` 再提交。）

---

## 收尾验证（全部任务完成后）

- [ ] 全量测试：workdir `src/backend`：`go test ./...`；`src/agent`：`go test ./...`；`src/shared`：`go test ./...`；`src/frontend`：`npm test && npm run check:api && npx tsc -b`
- [ ] `git status` 干净；分支合并回 main 前自查 spec 覆盖：shared 协议（Task 1）、存储与合并（Task 2）、sync 通道（Task 3）、设置页默认（Task 4）、服务器覆盖（Task 5）、agent 对齐（Task 6）、前端（Task 7）、文档（Task 8）。
