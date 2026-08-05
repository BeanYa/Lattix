# 用户模板指派 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在用户页新增「模板指派」Tab，支持多选用户批量指派订阅模板（内置或自定义），保留用户自选优先，可强制覆盖，指派后重发订阅快照。

**Architecture:** `user_subscription_profiles` 新增 8 列（4 个 assigned 模板槽位 + 4 个 forced 标记）；`store.EffectiveProfile` 纯函数合并指派与自选（自选优先、强制覆盖）；`sub.PublishUser` 改用生效 profile；panel 新增 assign/unassign 端点并批量重发；前端 Users 页改双 Tab。

**Tech Stack:** Go 1.26（modernc.org/sqlite）、React + Vite + shadcn/ui（Radix Select/Dialog/Tabs）、vitest、oxlint。

**Worktree:** 在 `.worktree/user-template-assignment` 分支 `feat/user-template-assignment` 实施（AGENTS.md：手动管理 worktree 于 `<repo>/.worktree/<name>`，`/.worktree/` 已 gitignore）。

## Global Constraints

- 所有后端/前端命令在 WSL 内执行：`wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && <cmd>"`。
- 后端验证：`go test ./src/backend/...`、`go vet ./src/backend/...`（与 CI 一致）。
- 前端验证：`npm run check:api`、`npx tsc -b`、`npm run lint`、`npx vitest run`。
- 模板 kind 映射：`portable`/`acl4ssr` → 主策略槽位；`mihomo`/`singbox`/`quanx` → 对应原生槽位。
- 生效优先级：未指派 = 现状；指派且未强制 = 用户自选优先（用户未自选则指派生效）；强制 = 指派生效、自选在 UI 禁用。
- 指派/取消指派后必须逐个 `PublishUser` 刷新订阅快照。
- 不得引入新依赖；沿用现有 `requester`、`ensureColumns` 迁移、`writeJSON/writeError/audit` 模式。
- 新增提交信息用现有仓库风格（`feat(sub):`、`feat(panel):`、`feat(frontend):`、`test(store):` 等）。

---

### Task 0: 创建 Worktree

**Files:**
- 无代码文件（仓库管理操作）

**Interfaces:**
- Produces: worktree `.worktree/user-template-assignment`（分支 `feat/user-template-assignment`），后续所有任务在其中执行。

- [ ] **Step 1: 提交当前 main 上的计划文档并创建 worktree**

```bash
git add docs/superpowers/plans/2026-08-06-user-template-assignment.md
git commit -m "docs: user template assignment implementation plan"
git worktree add .worktree/user-template-assignment -b feat/user-template-assignment
```

- [ ] **Step 2: 验证 worktree 就绪**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && git branch --show-current && go test ./src/backend/internal/store/ -run TestOpenMigratesLegacySchemaAndPreservesData"
```
Expected: 分支 `feat/user-template-assignment`，迁移测试 PASS。

---

### Task 1: Store 层 — 指派槽位 schema + 持久化 + EffectiveProfile + 删除引用检查

**Files:**
- Modify: `src/backend/internal/store/store.go`（`Schema` 中 `user_subscription_profiles` 表定义）
- Modify: `src/backend/internal/store/migrations.go`（`schemaVersion` 12→13 + `ensureColumns` 新列）
- Modify: `src/backend/internal/store/subscriptions.go`（`SubscriptionProfile` 结构体、`UserSubscriptionProfile` 查询、`SaveUserSubscriptionProfile`、`EffectiveProfile`、`DeleteSubscriptionTemplate` 引用检查）
- Modify: `src/backend/internal/store/migrations_test.go:343`（硬编码版本断言 12 → 13）
- Test: `src/backend/internal/store/subscriptions_assignment_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `Store`、`ensureColumns`、`schemaVersion` 常量。
- Produces:
  - `store.SubscriptionProfile` 新增字段：`AssignedPortableTemplateID/AssignedMihomoTemplateID/AssignedSingboxTemplateID/AssignedQuanXTemplateID string`、`AssignForcedPortable/AssignForcedMihomo/AssignForcedSingbox/AssignForcedQuanX bool`。
  - `func EffectiveProfile(p SubscriptionProfile) SubscriptionProfile` — 合并指派与自选后的生效 profile。
  - `DeleteSubscriptionTemplate` 引用检查覆盖 assigned 列。
  - schemaVersion = 13。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/store/subscriptions_assignment_test.go`：

```go
package store

import (
	"context"
	"testing"
)

func TestEffectiveProfileUnassignedKeepsUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		MihomoTemplateID: "my-mihomo",
	}
	got := EffectiveProfile(profile)
	if got.PortableTemplateID != "mine" || got.MihomoTemplateID != "my-mihomo" {
		t.Fatalf("unassigned profile changed: %+v", got)
	}
	if got.Mode != SubscriptionModeTemplate {
		t.Fatalf("mode = %q", got.Mode)
	}
}

func TestEffectiveProfileAssignedAppliesWhenUserHasNoChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, // 默认建议模式 = 未自选
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
	}
	got := EffectiveProfile(profile)
	if got.Mode != SubscriptionModeTemplate || got.PortableTemplateID != "acl4ssr-standard" {
		t.Fatalf("assigned portable not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("assigned mihomo not applied: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileUserChoiceWinsWhenNotForced(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
		MihomoTemplateID:           "my-mihomo",
	}
	got := EffectiveProfile(profile)
	if got.PortableTemplateID != "mine" {
		t.Fatalf("user portable choice overridden: %q", got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "my-mihomo" {
		t.Fatalf("user mihomo choice overridden: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileForcedOverridesUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		MihomoTemplateID:           "my-mihomo",
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
		AssignedMihomoTemplateID: "builtin-mihomo", AssignForcedMihomo: true,
	}
	got := EffectiveProfile(profile)
	if got.PortableTemplateID != "acl4ssr-standard" || got.Mode != SubscriptionModeTemplate {
		t.Fatalf("forced portable not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("forced mihomo not applied: %q", got.MihomoTemplateID)
	}
}

func TestSubscriptionProfilePersistsAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000001", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
		AssignedMihomoTemplateID: "builtin-mihomo",
		GenerationStatus: SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedPortableTemplateID != "acl4ssr-standard" || !got.AssignForcedPortable {
		t.Fatalf("portable assignment lost: %+v", got)
	}
	if got.AssignedMihomoTemplateID != "builtin-mihomo" || got.AssignForcedMihomo {
		t.Fatalf("mihomo assignment wrong: %+v", got)
	}
	if got.AssignForcedSingbox || got.AssignForcedQuanX {
		t.Fatalf("unrelated forced flags set: %+v", got)
	}
}

func TestDeleteSubscriptionTemplateRejectsAssigned(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertSubscriptionTemplate(ctx, SubscriptionTemplate{
		ID: "tpl-1", Name: "Tpl", Kind: "portable", Origin: "local", Content: "x", ContentSHA256: "sha",
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000002", "tok2", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		AssignedPortableTemplateID: "tpl-1",
		GenerationStatus: SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSubscriptionTemplate(ctx, "tpl-1"); err == nil {
		t.Fatal("assigned template deletion unexpectedly succeeded")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/store/ -run 'TestEffectiveProfile|TestSubscriptionProfilePersistsAssignment|TestDeleteSubscriptionTemplateRejectsAssigned' -v"
```
Expected: FAIL（`EffectiveProfile` 未定义 / 结构体字段不存在 / 编译失败）。

- [ ] **Step 3: 实现 store 层**

修改 `src/backend/internal/store/store.go` 的 `user_subscription_profiles` 表定义，在 `quanx_template_id` 行后插入：

```sql
    assigned_portable_template_id TEXT NOT NULL DEFAULT '',
    assigned_mihomo_template_id  TEXT NOT NULL DEFAULT '',
    assigned_singbox_template_id TEXT NOT NULL DEFAULT '',
    assigned_quanx_template_id   TEXT NOT NULL DEFAULT '',
    assign_forced_portable INTEGER NOT NULL DEFAULT 0,
    assign_forced_mihomo  INTEGER NOT NULL DEFAULT 0,
    assign_forced_singbox INTEGER NOT NULL DEFAULT 0,
    assign_forced_quanx   INTEGER NOT NULL DEFAULT 0,
```

修改 `src/backend/internal/store/migrations.go`：

- `const schemaVersion = 12` → `const schemaVersion = 13`
- 在 `migrateSchema` 的 map 中新增一项：

```go
		"user_subscription_profiles": {
			{"assigned_portable_template_id", "TEXT NOT NULL DEFAULT ''"},
			{"assigned_mihomo_template_id", "TEXT NOT NULL DEFAULT ''"},
			{"assigned_singbox_template_id", "TEXT NOT NULL DEFAULT ''"},
			{"assigned_quanx_template_id", "TEXT NOT NULL DEFAULT ''"},
			{"assign_forced_portable", "INTEGER NOT NULL DEFAULT 0"},
			{"assign_forced_mihomo", "INTEGER NOT NULL DEFAULT 0"},
			{"assign_forced_singbox", "INTEGER NOT NULL DEFAULT 0"},
			{"assign_forced_quanx", "INTEGER NOT NULL DEFAULT 0"},
		},
```

修改 `src/backend/internal/store/subscriptions.go`：

`SubscriptionProfile` 结构体在 `QuanXTemplateID` 后插入：

```go
	AssignedPortableTemplateID string
	AssignedMihomoTemplateID   string
	AssignedSingboxTemplateID  string
	AssignedQuanXTemplateID    string
	AssignForcedPortable       bool
	AssignForcedMihomo         bool
	AssignForcedSingbox        bool
	AssignForcedQuanX          bool
```

`UserSubscriptionProfile` 的 SELECT 与 Scan 改为：

```go
	err := s.db.QueryRowContext(ctx, `SELECT user_id, mode, preset, categories, portable_template_id,
		mihomo_template_id, singbox_template_id, quanx_template_id,
		assigned_portable_template_id, assigned_mihomo_template_id, assigned_singbox_template_id, assigned_quanx_template_id,
		assign_forced_portable, assign_forced_mihomo, assign_forced_singbox, assign_forced_quanx,
		generation_status, generation_error, updated_at FROM user_subscription_profiles WHERE user_id = ?`, userID).Scan(
		&profile.UserID, &profile.Mode, &profile.Preset, &profile.CategoriesJSON,
		&profile.PortableTemplateID, &profile.MihomoTemplateID, &profile.SingboxTemplateID,
		&profile.QuanXTemplateID, &profile.AssignedPortableTemplateID, &profile.AssignedMihomoTemplateID,
		&profile.AssignedSingboxTemplateID, &profile.AssignedQuanXTemplateID,
		&forcedPortable, &forcedMihomo, &forcedSingbox, &forcedQuanx,
		&profile.GenerationStatus, &profile.GenerationError, &profile.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if _, userErr := s.UserByID(ctx, userID); userErr != nil {
			return SubscriptionProfile{}, userErr
		}
		return defaultSubscriptionProfile(userID), nil
	}
	if err != nil {
		return SubscriptionProfile{}, fmt.Errorf("query user subscription profile: %w", err)
	}
	profile.AssignForcedPortable = forcedPortable != 0
	profile.AssignForcedMihomo = forcedMihomo != 0
	profile.AssignForcedSingbox = forcedSingbox != 0
	profile.AssignForcedQuanX = forcedQuanx != 0
	return profile, nil
```

其中 `forcedPortable, forcedMihomo, forcedSingbox, forcedQuanx` 为函数内声明的 `int` 局部变量（对齐 `scanUser` 的 INTEGER→bool 模式）。

`SaveUserSubscriptionProfile` 整体替换为：

```go
func (s *Store) SaveUserSubscriptionProfile(ctx context.Context, profile SubscriptionProfile) error {
	forcedPortable, forcedMihomo := 0, 0
	forcedSingbox, forcedQuanx := 0, 0
	if profile.AssignForcedPortable {
		forcedPortable = 1
	}
	if profile.AssignForcedMihomo {
		forcedMihomo = 1
	}
	if profile.AssignForcedSingbox {
		forcedSingbox = 1
	}
	if profile.AssignForcedQuanX {
		forcedQuanx = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_subscription_profiles
		(user_id, mode, preset, categories, portable_template_id, mihomo_template_id,
		 singbox_template_id, quanx_template_id, assigned_portable_template_id, assigned_mihomo_template_id,
		 assigned_singbox_template_id, assigned_quanx_template_id, assign_forced_portable, assign_forced_mihomo,
		 assign_forced_singbox, assign_forced_quanx, generation_status, generation_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET mode=excluded.mode, preset=excluded.preset,
		categories=excluded.categories, portable_template_id=excluded.portable_template_id,
		mihomo_template_id=excluded.mihomo_template_id, singbox_template_id=excluded.singbox_template_id,
		quanx_template_id=excluded.quanx_template_id,
		assigned_portable_template_id=excluded.assigned_portable_template_id,
		assigned_mihomo_template_id=excluded.assigned_mihomo_template_id,
		assigned_singbox_template_id=excluded.assigned_singbox_template_id,
		assigned_quanx_template_id=excluded.assigned_quanx_template_id,
		assign_forced_portable=excluded.assign_forced_portable,
		assign_forced_mihomo=excluded.assign_forced_mihomo,
		assign_forced_singbox=excluded.assign_forced_singbox,
		assign_forced_quanx=excluded.assign_forced_quanx,
		generation_status=excluded.generation_status,
		generation_error='', updated_at=CURRENT_TIMESTAMP`,
		profile.UserID, profile.Mode, profile.Preset, profile.CategoriesJSON,
		profile.PortableTemplateID, profile.MihomoTemplateID, profile.SingboxTemplateID,
		profile.QuanXTemplateID, profile.AssignedPortableTemplateID, profile.AssignedMihomoTemplateID,
		profile.AssignedSingboxTemplateID, profile.AssignedQuanXTemplateID,
		forcedPortable, forcedMihomo, forcedSingbox, forcedQuanx, profile.GenerationStatus)
	if err != nil {
		return fmt.Errorf("save user subscription profile: %w", err)
	}
	return nil
}
```

在 `defaultSubscriptionProfile` 之后新增：

```go
// EffectiveProfile 合并管理员指派与用户自选，返回订阅构建实际使用的 profile：
// 指派槽位在用户未自选（或强制覆盖）时生效；用户自选优先于普通指派。
func EffectiveProfile(p SubscriptionProfile) SubscriptionProfile {
	if p.AssignedPortableTemplateID != "" && (p.AssignForcedPortable || p.Mode != SubscriptionModeTemplate || p.PortableTemplateID == "") {
		p.Mode = SubscriptionModeTemplate
		p.PortableTemplateID = p.AssignedPortableTemplateID
	}
	if p.AssignedMihomoTemplateID != "" && (p.AssignForcedMihomo || p.MihomoTemplateID == "") {
		p.MihomoTemplateID = p.AssignedMihomoTemplateID
	}
	if p.AssignedSingboxTemplateID != "" && (p.AssignForcedSingbox || p.SingboxTemplateID == "") {
		p.SingboxTemplateID = p.AssignedSingboxTemplateID
	}
	if p.AssignedQuanXTemplateID != "" && (p.AssignForcedQuanX || p.QuanXTemplateID == "") {
		p.QuanXTemplateID = p.AssignedQuanXTemplateID
	}
	return p
}
```

`DeleteSubscriptionTemplate` 的引用检查替换为：

```go
	var refs int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_subscription_profiles WHERE
		portable_template_id=? OR mihomo_template_id=? OR singbox_template_id=? OR quanx_template_id=?
		OR assigned_portable_template_id=? OR assigned_mihomo_template_id=? OR assigned_singbox_template_id=? OR assigned_quanx_template_id=?`,
		id, id, id, id, id, id, id, id).Scan(&refs); err != nil {
		return err
	}
```

- [ ] **Step 4: 更新迁移版本断言并运行测试**

修改 `src/backend/internal/store/migrations_test.go` 中 `TestOpenMigratesUserGroupsTables` 的断言 `if version != 12` → `if version != 13`（若该文件还有其他硬编码 12 的版本断言，一并改为 13）。

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/store/ -run 'TestEffectiveProfile|TestSubscriptionProfilePersistsAssignment|TestDeleteSubscriptionTemplateRejectsAssigned|TestOpenMigrates' -v"
```
Expected: 全部 PASS。

- [ ] **Step 5: 全量 store 测试 + vet + 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/store/ && go vet ./src/backend/internal/store/"
```
Expected: PASS，vet 无输出。

```bash
git add src/backend/internal/store/
git commit -m "feat(store): template assignment slots and effective profile resolution"
```

---

### Task 2: Sub 层 — PublishUser 使用生效 profile

**Files:**
- Modify: `src/backend/internal/sub/publisher.go:38-45`（`PublishUser` 内 profile 获取后合并指派）
- Test: `src/backend/internal/sub/subscription_routing_test.go`（新增 2 个测试）

**Interfaces:**
- Consumes: `store.EffectiveProfile`（Task 1）。
- Produces: `PublishUser` 的 `resolvePolicy` 与 native 覆盖循环均基于生效 profile（含指派槽位）。

- [ ] **Step 1: 写失败测试**

在 `src/backend/internal/sub/subscription_routing_test.go` 末尾追加（文件已 import `store`、`strings`、`context`、`testing`，无需新增 import）：

```go
func TestPublishUserAppliesAssignedPortableTemplate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertSubscriptionTemplate(ctx, store.SubscriptionTemplate{
		ID: "assigned-tpl", Name: "Assigned", Kind: "portable", Origin: "local",
		Content: "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n",
		ContentSHA256: "assigned-sha",
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000005", "assigned-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户未自选（默认建议模式），仅被指派模板。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "assigned-tpl",
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Files["clash"]), "Proxy") {
		t.Fatalf("assigned template group missing from clash output: %s", result.Files["clash"])
	}
}

func TestPublishUserForcedAssignmentOverridesUserChoice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	body := "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n"
	for _, template := range []store.SubscriptionTemplate{
		{ID: "assigned-tpl", Name: "Assigned", Kind: "portable", Origin: "local", Content: body, ContentSHA256: "assigned-sha"},
		{ID: "user-tpl", Name: "User", Kind: "portable", Origin: "local",
			Content: "name: User\ngroups:\n  - name: UserGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UserGroup\n",
			ContentSHA256: "user-sha"},
	} {
		if err := st.UpsertSubscriptionTemplate(ctx, template); err != nil {
			t.Fatal(err)
		}
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000006", "forced-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户自选 user-tpl，但被强制指派 assigned-tpl。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-tpl",
		GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "assigned-tpl", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "Proxy") || strings.Contains(clash, "UserGroup") {
		t.Fatalf("forced assignment not applied: %s", clash)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/sub/ -run 'TestPublishUserAppliesAssignedPortableTemplate|TestPublishUserForcedAssignmentOverridesUserChoice' -v"
```
Expected: FAIL（clash 输出中无 Proxy 组，仍走建议规则）。

- [ ] **Step 3: 实现**

修改 `src/backend/internal/sub/publisher.go` 的 `PublishUser`：

```go
	profile, err := s.st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		return PublishResult{}, err
	}
	profile = store.EffectiveProfile(profile)
	policy, sourceLabel, template, err := s.resolvePolicy(ctx, profile)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/sub/ -run 'TestPublishUserAppliesAssignedPortableTemplate|TestPublishUserForcedAssignmentOverridesUserChoice' -v"
```
Expected: PASS。

- [ ] **Step 5: 全量 sub 测试 + 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/sub/"
```
Expected: PASS。

```bash
git add src/backend/internal/sub/
git commit -m "feat(sub): publish subscriptions from effective profile with template assignment"
```

---

### Task 3: Panel 层 — DTO + assign/unassign 端点 + 路由 + openapi + 自选保存保留指派

**Files:**
- Modify: `src/backend/internal/panel/users.go`（`subscriptionProfileDTO` + `subscriptionProfileToDTO`；`handleUpdateUserSubSettings` 保存自选时保留指派字段）
- Create: `src/backend/internal/panel/template_assignment.go`（`applyTemplateAssignment`、`clearTemplateAssignment`、`handleAssignSubscriptionTemplate`、`handleUnassignSubscriptionTemplate`）
- Modify: `src/backend/internal/panel/panel.go`（注册 2 条路由，紧跟 `/api/subscription/template/refresh` 之后）
- Modify: `docs/openapi.yaml`（新增 2 个端点）
- Test: `src/backend/internal/panel/template_assignment_test.go`（新建）

**Interfaces:**
- Consumes: `store.SubscriptionTemplateByID`、`store.UserByID`、`store.UserSubscriptionProfile`、`store.SaveUserSubscriptionProfile`、`store.SubscriptionProfile` 新字段、`s.subscriptions.PublishUser`。
- Produces:
  - `POST /api/subscription/template/assign`：`{"user_ids": []int64, "template_id": string, "forced": bool}`
  - `POST /api/subscription/template/unassign`：`{"user_ids": []int64, "template_id": string}`
  - `subscriptionProfileDTO` 新增 8 个 JSON 字段：`assigned_portable_template_id`、`assign_forced_portable`、`assigned_mihomo_template_id`、`assign_forced_mihomo`、`assigned_singbox_template_id`、`assign_forced_singbox`、`assigned_quanx_template_id`、`assign_forced_quanx`。
  - 用户 `routing` 中这些字段为原始（未合并）值；生效值由前端/`EffectiveProfile` 各自计算。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/panel/template_assignment_test.go`：

```go
package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

const portableTemplateBody = "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n"

func upsertTestTemplate(t *testing.T, st *store.Store, template store.SubscriptionTemplate) {
	t.Helper()
	if err := st.UpsertSubscriptionTemplate(context.Background(), template); err != nil {
		t.Fatal(err)
	}
}

func assignRequest(t *testing.T, server *Server, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.handleAssignSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/assign", strings.NewReader(body)))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return rec.Code, resp.Code
}

func TestAssignSubscriptionTemplateWritesSlotAndPublishes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	userA, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000010", "tok-a", nil)
	userB, _ := st.InsertUser(ctx, "b", "00000000-0000-0000-0000-000000000011", "tok-b", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d,%d],"template_id":"tpl-portable","forced":true}`, userA, userB))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	for _, userID := range []int64{userA, userB} {
		profile, err := st.UserSubscriptionProfile(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if profile.AssignedPortableTemplateID != "tpl-portable" || !profile.AssignForcedPortable {
			t.Fatalf("user %d assignment = %+v", userID, profile)
		}
		snapshot, err := st.SubscriptionSnapshotStatus(ctx, userID)
		if err != nil || snapshot.Status != store.SubscriptionGenerationReady {
			t.Fatalf("user %d snapshot = %+v, err %v", userID, snapshot, err)
		}
	}
}

func TestAssignSubscriptionTemplateKindMapping(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-mihomo", Name: "Mihomo", Kind: "mihomo", Origin: "local",
		Content: "proxies: []\n", ContentSHA256: "sha-2",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000012", "tok-c", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-mihomo"}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedMihomoTemplateID != "tpl-mihomo" {
		t.Fatalf("mihomo slot = %q", profile.AssignedMihomoTemplateID)
	}
	if profile.AssignedPortableTemplateID != "" || profile.AssignedSingboxTemplateID != "" || profile.AssignedQuanXTemplateID != "" {
		t.Fatalf("unrelated slots written: %+v", profile)
	}
	if profile.AssignForcedMihomo {
		t.Fatalf("forced defaulted true: %+v", profile)
	}
}

func TestAssignSubscriptionTemplateValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	if status, code := assignRequest(t, server, `{"user_ids":[],"template_id":"tpl"}`); status != http.StatusBadRequest || code != shared.CodeInvalidArgument {
		t.Fatalf("empty user_ids: status=%d code=%q", status, code)
	}
	if status, code := assignRequest(t, server, `{"user_ids":[1],"template_id":""}`); status != http.StatusBadRequest || code != shared.CodeInvalidArgument {
		t.Fatalf("empty template_id: status=%d code=%q", status, code)
	}
	if status, code := assignRequest(t, server, `{"user_ids":[1],"template_id":"missing"}`); status != http.StatusNotFound || code != shared.CodeNotFound {
		t.Fatalf("missing template: status=%d code=%q", status, code)
	}
}

func TestAssignSubscriptionTemplateRejectsMissingUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	if status, code := assignRequest(t, server, `{"user_ids":[9999],"template_id":"tpl-portable"}`); status != http.StatusNotFound || code != shared.CodeNotFound {
		t.Fatalf("status=%d code=%q", status, code)
	}
}

func TestUnassignSubscriptionTemplateClearsSlotKeepsUserChoice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000013", "tok-d", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-own",
		GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	rec := httptest.NewRecorder()
	server.handleUnassignSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/unassign", strings.NewReader(fmt.Sprintf(
			`{"user_ids":[%d],"template_id":"tpl-portable"}`, userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "" || profile.AssignForcedPortable {
		t.Fatalf("assignment not cleared: %+v", profile)
	}
	if profile.PortableTemplateID != "user-own" || profile.Mode != store.SubscriptionModeTemplate {
		t.Fatalf("user choice lost: %+v", profile)
	}
	snapshot, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil || snapshot.Status != store.SubscriptionGenerationReady {
		t.Fatalf("snapshot = %+v, err %v", snapshot, err)
	}
}

func TestUpdateUserSubSettingsPreservesAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000014", "tok-e", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/sub-settings", strings.NewReader(fmt.Sprintf(
			`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"mode":"suggested","preset":"balanced","categories":["ai"],"portable_template_id":"","mihomo_template_id":"","singbox_template_id":"","quanx_template_id":""}}`,
			userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "acl4ssr-standard" || !profile.AssignForcedPortable {
		t.Fatalf("assignment lost after sub-settings save: %+v", profile)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/panel/ -run 'TestAssignSubscriptionTemplate|TestUnassignSubscriptionTemplate|TestUpdateUserSubSettingsPreservesAssignment' -v"
```
Expected: FAIL（处理器未定义 / DTO 字段缺失 / 订阅设置保存后指派丢失）。

- [ ] **Step 3: 实现 panel 层**

创建 `src/backend/internal/panel/template_assignment.go`：

```go
package panel

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
)

// applyTemplateAssignment 按模板 kind 把指派写入对应槽位；acl4ssr 归主策略槽位。
func applyTemplateAssignment(profile *store.SubscriptionProfile, kind, templateID string, forced bool) error {
	switch kind {
	case "portable", "acl4ssr":
		profile.AssignedPortableTemplateID, profile.AssignForcedPortable = templateID, forced
	case "mihomo":
		profile.AssignedMihomoTemplateID, profile.AssignForcedMihomo = templateID, forced
	case "singbox":
		profile.AssignedSingboxTemplateID, profile.AssignForcedSingbox = templateID, forced
	case "quanx":
		profile.AssignedQuanXTemplateID, profile.AssignForcedQuanX = templateID, forced
	default:
		return fmt.Errorf("不支持的模板类型 %q", kind)
	}
	return nil
}

// clearTemplateAssignment 清除模板 kind 对应槽位的指派与强制标记（用户自选不触碰）。
func clearTemplateAssignment(profile *store.SubscriptionProfile, kind string) error {
	switch kind {
	case "portable", "acl4ssr":
		profile.AssignedPortableTemplateID, profile.AssignForcedPortable = "", false
	case "mihomo":
		profile.AssignedMihomoTemplateID, profile.AssignForcedMihomo = "", false
	case "singbox":
		profile.AssignedSingboxTemplateID, profile.AssignForcedSingbox = "", false
	case "quanx":
		profile.AssignedQuanXTemplateID, profile.AssignForcedQuanX = "", false
	default:
		return fmt.Errorf("不支持的模板类型 %q", kind)
	}
	return nil
}

func dedupeUserIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// handleAssignSubscriptionTemplate 处理 POST /api/subscription/template/assign：
// 多选用户批量指派模板到对应 kind 槽位，可强制覆盖用户自选；指派后重发各用户订阅快照。
func (s *Server) handleAssignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs    []int64 `json:"user_ids"`
		TemplateID string  `json:"template_id"`
		Forced     bool    `json:"forced"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userIDs := dedupeUserIDs(req.UserIDs)
	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids 不能为空")
		return
	}
	templateID := strings.TrimSpace(req.TemplateID)
	if templateID == "" {
		writeError(w, http.StatusBadRequest, "template_id 不能为空")
		return
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(template.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("订阅模板 %q 尚无有效缓存", template.Name))
		return
	}
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := applyTemplateAssignment(&profile, template.Kind, template.ID, req.Forced); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if s.subscriptions != nil {
		for _, userID := range userIDs {
			if _, err := s.subscriptions.PublishUser(r.Context(), userID, s.panelBase(r)); err != nil {
				writeError(w, http.StatusBadRequest, "生成订阅失败: "+err.Error())
				return
			}
		}
	}
	s.audit(r, "subscription.template.assigned", nil, nil, map[string]any{
		"template_id": templateID, "user_ids": userIDs, "forced": req.Forced,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": templateID, "forced": req.Forced,
	})
}

// handleUnassignSubscriptionTemplate 处理 POST /api/subscription/template/unassign：
// 清除用户对应 kind 槽位的指派与强制标记（用户自选值保留），并重发订阅快照。
func (s *Server) handleUnassignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs    []int64 `json:"user_ids"`
		TemplateID string  `json:"template_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userIDs := dedupeUserIDs(req.UserIDs)
	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids 不能为空")
		return
	}
	templateID := strings.TrimSpace(req.TemplateID)
	if templateID == "" {
		writeError(w, http.StatusBadRequest, "template_id 不能为空")
		return
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := clearTemplateAssignment(&profile, template.Kind); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if s.subscriptions != nil {
		for _, userID := range userIDs {
			if _, err := s.subscriptions.PublishUser(r.Context(), userID, s.panelBase(r)); err != nil {
				writeError(w, http.StatusBadRequest, "生成订阅失败: "+err.Error())
				return
			}
		}
	}
	s.audit(r, "subscription.template.unassigned", nil, nil, map[string]any{
		"template_id": templateID, "user_ids": userIDs,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": templateID,
	})
}
```

修改 `src/backend/internal/panel/users.go`：

`subscriptionProfileDTO` 结构体替换为：

```go
type subscriptionProfileDTO struct {
	Mode               string   `json:"mode"`
	Preset             string   `json:"preset"`
	Categories         []string `json:"categories"`
	PortableTemplateID string   `json:"portable_template_id"`
	MihomoTemplateID   string   `json:"mihomo_template_id"`
	SingboxTemplateID  string   `json:"singbox_template_id"`
	QuanXTemplateID    string   `json:"quanx_template_id"`
	AssignedPortableTemplateID string `json:"assigned_portable_template_id"`
	AssignForcedPortable       bool   `json:"assign_forced_portable"`
	AssignedMihomoTemplateID   string `json:"assigned_mihomo_template_id"`
	AssignForcedMihomo         bool   `json:"assign_forced_mihomo"`
	AssignedSingboxTemplateID  string `json:"assigned_singbox_template_id"`
	AssignForcedSingbox        bool   `json:"assign_forced_singbox"`
	AssignedQuanXTemplateID    string `json:"assigned_quanx_template_id"`
	AssignForcedQuanX          bool   `json:"assign_forced_quanx"`
}
```

`subscriptionProfileToDTO` 替换为：

```go
func subscriptionProfileToDTO(profile store.SubscriptionProfile) subscriptionProfileDTO {
	var categories []string
	_ = json.Unmarshal([]byte(profile.CategoriesJSON), &categories)
	return subscriptionProfileDTO{
		Mode: profile.Mode, Preset: profile.Preset, Categories: categories,
		PortableTemplateID: profile.PortableTemplateID, MihomoTemplateID: profile.MihomoTemplateID,
		SingboxTemplateID: profile.SingboxTemplateID, QuanXTemplateID: profile.QuanXTemplateID,
		AssignedPortableTemplateID: profile.AssignedPortableTemplateID,
		AssignForcedPortable:       profile.AssignForcedPortable,
		AssignedMihomoTemplateID:   profile.AssignedMihomoTemplateID,
		AssignForcedMihomo:         profile.AssignForcedMihomo,
		AssignedSingboxTemplateID:  profile.AssignedSingboxTemplateID,
		AssignForcedSingbox:        profile.AssignForcedSingbox,
		AssignedQuanXTemplateID:    profile.AssignedQuanXTemplateID,
		AssignForcedQuanX:          profile.AssignForcedQuanX,
	}
}
```

`handleUpdateUserSubSettings` 中 `routingProfile` 构建块（约 930-942 行）替换为（读取当前 profile 保留指派字段）：

```go
	var routingProfile *store.SubscriptionProfile
	if req.Routing != nil {
		profile, err := profileFromInput(req.UserID, req.Routing)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.validateSubscriptionProfileTemplates(r.Context(), profile); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 订阅设置只改用户自选；管理员指派槽位与强制标记原样保留。
		if current, err := s.st.UserSubscriptionProfile(r.Context(), req.UserID); err == nil {
			profile.AssignedPortableTemplateID = current.AssignedPortableTemplateID
			profile.AssignForcedPortable = current.AssignForcedPortable
			profile.AssignedMihomoTemplateID = current.AssignedMihomoTemplateID
			profile.AssignForcedMihomo = current.AssignForcedMihomo
			profile.AssignedSingboxTemplateID = current.AssignedSingboxTemplateID
			profile.AssignForcedSingbox = current.AssignForcedSingbox
			profile.AssignedQuanXTemplateID = current.AssignedQuanXTemplateID
			profile.AssignForcedQuanX = current.AssignForcedQuanX
		}
		routingProfile = &profile
	}
```

修改 `src/backend/internal/panel/panel.go`，在 `s.handleRefreshSubscriptionTemplates` 行后新增：

```go
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/assign",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_ids", "template_id"}},
		s.handleAssignSubscriptionTemplate)
	s.registerRPC(mux, http.MethodPost, "/api/subscription/template/unassign",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_ids", "template_id"}},
		s.handleUnassignSubscriptionTemplate)
```

修改 `docs/openapi.yaml`，在 `/api/subscription/template/refresh` 块（`subscriptionTemplateRefresh`）之后追加：

```yaml
  /api/subscription/template/assign:
    post:
      operationId: subscriptionTemplateAssign
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/subscription/template/unassign:
    post:
      operationId: subscriptionTemplateUnassign
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/panel/ -run 'TestAssignSubscriptionTemplate|TestUnassignSubscriptionTemplate|TestUpdateUserSubSettingsPreservesAssignment' -v"
```
Expected: PASS。

- [ ] **Step 5: 全量 panel 测试 + vet + 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/internal/panel/ && go vet ./src/backend/internal/panel/"
```
Expected: PASS，vet 无输出。

```bash
git add src/backend/internal/panel/ docs/openapi.yaml
git commit -m "feat(panel): batch template assignment endpoints with subscription republish"
```

---

### Task 4: 前端契约与 API client

**Files:**
- Modify: `src/frontend/src/lib/api-contract.generated.ts`（由 `npm run generate:api` 重新生成）
- Modify: `src/frontend/src/lib/types.ts`（`SubscriptionRoutingProfile` 新增 8 字段）
- Modify: `src/frontend/src/lib/api.ts`（新增 assign/unassign 方法）
- Modify: `src/frontend/src/lib/subscription-routing.ts`（默认值）

**Interfaces:**
- Consumes: `docs/openapi.yaml` 新端点（Task 3）。
- Produces:
  - `api.assignSubscriptionTemplate(userIds: number[], templateId: string, forced: boolean)` → `Promise<{user_ids, template_id, forced}>`
  - `api.unassignSubscriptionTemplate(userIds: number[], templateId: string)` → `Promise<{user_ids, template_id}>`
  - `SubscriptionRoutingProfile` 新增：`assigned_portable_template_id: string; assign_forced_portable: boolean; assigned_mihomo_template_id: string; assign_forced_mihomo: boolean; assigned_singbox_template_id: string; assign_forced_singbox: boolean; assigned_quanx_template_id: string; assign_forced_quanx: boolean`。

- [ ] **Step 1: 重新生成 API 契约**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment/src/frontend && npm run generate:api"
```
Expected: `api-contract.generated.ts` 更新（新增 `SubscriptionTemplateAssign` 等操作类型）。

- [ ] **Step 2: 扩展手写类型（无测试直接改类型，随后 tsc 验证）**

修改 `src/frontend/src/lib/types.ts` 的 `SubscriptionRoutingProfile`：

```ts
export interface SubscriptionRoutingProfile {
  mode: 'suggested' | 'template'
  preset: 'minimal' | 'balanced' | 'comprehensive'
  categories: string[]
  portable_template_id: string
  mihomo_template_id: string
  singbox_template_id: string
  quanx_template_id: string
  assigned_portable_template_id: string
  assign_forced_portable: boolean
  assigned_mihomo_template_id: string
  assign_forced_mihomo: boolean
  assigned_singbox_template_id: string
  assign_forced_singbox: boolean
  assigned_quanx_template_id: string
  assign_forced_quanx: boolean
}
```

修改 `src/frontend/src/lib/subscription-routing.ts`：

```ts
export const defaultSubscriptionRouting: SubscriptionRoutingProfile = {
  mode: 'suggested',
  preset: 'balanced',
  categories: ['ai', 'youtube', 'google', 'private', 'domestic', 'telegram', 'github', 'overseas'],
  portable_template_id: '',
  mihomo_template_id: '',
  singbox_template_id: '',
  quanx_template_id: '',
  assigned_portable_template_id: '',
  assign_forced_portable: false,
  assigned_mihomo_template_id: '',
  assign_forced_mihomo: false,
  assigned_singbox_template_id: '',
  assign_forced_singbox: false,
  assigned_quanx_template_id: '',
  assign_forced_quanx: false,
}
```

修改 `src/frontend/src/lib/api.ts`，在 `refreshSubscriptionTemplates` 后新增：

```ts
  assignSubscriptionTemplate: (userIds: number[], templateId: string, forced: boolean) =>
    requester.post<{ user_ids: number[]; template_id: string; forced: boolean }>(
      '/api/subscription/template/assign',
      { user_ids: userIds, template_id: templateId, forced },
    ),
  unassignSubscriptionTemplate: (userIds: number[], templateId: string) =>
    requester.post<{ user_ids: number[]; template_id: string }>(
      '/api/subscription/template/unassign',
      { user_ids: userIds, template_id: templateId },
    ),
```

- [ ] **Step 3: 验证契约与类型**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment/src/frontend && npm run check:api && npx tsc -b"
```
Expected: check:api 通过，tsc 无错误。

- [ ] **Step 4: 提交**

```bash
git add src/frontend/src/lib/
git commit -m "feat(frontend): template assignment types and api client"
```

---

### Task 5: 前端 — SubscriptionRoutingFields 展示指派/强制状态

**Files:**
- Modify: `src/frontend/src/components/SubscriptionRoutingFields.tsx`

**Interfaces:**
- Consumes: `SubscriptionRoutingProfile` 新字段（Task 4）、`templates` prop。
- Produces: 订阅设置对话框内：强制指派槽位禁用并标注「已强制指派」；普通指派显示「已指派…自选优先」提示；主策略区同规则。

- [ ] **Step 1: 实现（组件无独立测试文件，用 `npx tsc -b` 验证）**

修改 `src/frontend/src/components/SubscriptionRoutingFields.tsx`：

新增 import（文件头部）：

```tsx
import { Notice } from '@/components/PagePrimitives'
```

组件内新增辅助（`set` 定义之后）：

```tsx
  const templateName = (id: string) => templates.find((template) => template.id === id)?.name ?? id
  const mainAssigned = value.assigned_portable_template_id !== ''
```

主策略区块改造（`value.mode === 'suggested' ? (...)` 三处 Select 与块首）：

- 在 `<div className="grid gap-3 sm:grid-cols-2">`（规则来源/预设/模板选择）之前插入：

```tsx
      {value.assigned_portable_template_id && (
        <Notice tone="info">
          {value.assign_forced_portable
            ? `已强制指派模板「${templateName(value.assigned_portable_template_id)}」，以下自选设置暂不生效。`
            : `已指派模板「${templateName(value.assigned_portable_template_id)}」（自选优先，可在此覆盖）。`}
        </Notice>
      )}
```

- 「规则来源」`<Select>` 加 `disabled={value.assign_forced_portable}`。
- 「规则预设」`<Select>` 加 `disabled={value.assign_forced_portable}`。
- 「中立 / ACL4SSR 模板」`<Select>` 加 `disabled={value.assign_forced_portable}`，且 `<SelectValue placeholder={value.assign_forced_portable ? '已强制指派' : '选择模板'} />`。
- 分类 checkbox 区域（suggested 模式）外层包一层禁用：`<fieldset disabled={value.assign_forced_portable}>...</fieldset>`。

原生覆盖 `details` 区块整体替换（三列 map 改为显式局部变量避免 TS 混合索引报错）：

```tsx
      <details className="rounded-md border px-3 py-2">
        <summary className="cursor-pointer text-sm font-medium">客户端原生模板覆盖</summary>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          {([
            ['mihomo_template_id', 'assigned_mihomo_template_id', 'assign_forced_mihomo', 'Mihomo', 'mihomo'],
            ['singbox_template_id', 'assigned_singbox_template_id', 'assign_forced_singbox', 'Sing-box', 'singbox'],
            ['quanx_template_id', 'assigned_quanx_template_id', 'assign_forced_quanx', 'Quantumult X', 'quanx'],
          ] as const).map(([field, assignedField, forcedField, label, kind]) => {
            const selected = value[field] as string
            const assigned = value[assignedField] as string
            const forced = Boolean(value[forcedField])
            return (
              <div key={field} className="space-y-2">
                <Label>{label}</Label>
                <Select
                  value={selected || 'none'}
                  disabled={forced}
                  onValueChange={(id) => id && set({ [field]: id === 'none' ? '' : id })}
                >
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">{forced ? '跟随指派' : '跟随主策略'}</SelectItem>
                    {native(kind).map((template) => <SelectItem key={template.id} value={template.id}>{template.name}</SelectItem>)}
                  </SelectContent>
                </Select>
                {assigned ? (
                  <p className="text-xs text-muted-foreground">
                    {forced
                      ? `已强制指派「${templateName(assigned)}」`
                      : `已指派「${templateName(assigned)}」，自选优先`}
                  </p>
                ) : null}
              </div>
            )
          })}
        </div>
      </details>
```

同时把组件内已有的 `set` 定义改为保留，`mainAssigned` 辅助若未使用可删去（`npx tsc` 无未使用变量检查则保留亦可）。

- [ ] **Step 2: 验证**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment/src/frontend && npx tsc -b && npm run lint"
```
Expected: 无错误。

- [ ] **Step 3: 提交**

```bash
git add src/frontend/src/components/SubscriptionRoutingFields.tsx
git commit -m "feat(frontend): show template assignment state in subscription settings"
```

---

### Task 6: 前端 — Users 页双 Tab + 模板指派 Tab

**Files:**
- Create: `src/frontend/src/components/TemplateAssignmentTab.tsx`
- Modify: `src/frontend/src/pages/Users.tsx`（Tabs 包装 + 指派 Tab 挂载）

**Interfaces:**
- Consumes: `SubUser[]`、`SubscriptionTemplate[]`、`api.assignSubscriptionTemplate`、`api.unassignSubscriptionTemplate`（Task 4）、`onChanged` 回调（Users.tsx 的 `load`）。
- Produces: `TemplateAssignmentTab` 组件（props：`{ users: SubUser[]; templates: SubscriptionTemplate[]; onChanged: () => void }`）。

- [ ] **Step 1: 创建指派 Tab 组件**

创建 `src/frontend/src/components/TemplateAssignmentTab.tsx`：

```tsx
import { useMemo, useState } from 'react'
import { ClipboardCheckIcon, XIcon } from 'lucide-react'

import { EmptyState, Notice } from '@/components/PagePrimitives'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import type { SubUser, SubscriptionTemplate } from '@/lib/types'

const SLOTS = [
  ['assigned_portable_template_id', 'assign_forced_portable'],
  ['assigned_mihomo_template_id', 'assign_forced_mihomo'],
  ['assigned_singbox_template_id', 'assign_forced_singbox'],
  ['assigned_quanx_template_id', 'assign_forced_quanx'],
] as const

const KIND_LABELS: Record<SubscriptionTemplate['kind'], string> = {
  portable: '主策略',
  acl4ssr: '主策略',
  mihomo: 'Mihomo',
  singbox: 'Sing-box',
  quanx: 'Quantumult X',
}

interface TemplateAssignmentTabProps {
  users: SubUser[]
  templates: SubscriptionTemplate[]
  onChanged: () => void
}

export function TemplateAssignmentTab({ users, templates, onChanged }: TemplateAssignmentTabProps) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [templateId, setTemplateId] = useState('')
  const [forced, setForced] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const assignable = templates.filter((template) => template.content)
  const assignedUsers = useMemo(() => {
    const byTemplate = new Map<string, Array<{ user: SubUser; forced: boolean }>>()
    for (const user of users) {
      for (const [assignedField, forcedField] of SLOTS) {
        const id = user.routing[assignedField] as string
        if (!id) continue
        const entries = byTemplate.get(id) ?? []
        entries.push({ user, forced: Boolean(user.routing[forcedField]) })
        byTemplate.set(id, entries)
      }
    }
    return byTemplate
  }, [users])

  const unassigned = users.filter((user) => !SLOTS.some(([assignedField]) => user.routing[assignedField] as string))

  const toggle = (id: number, checked: boolean) => {
    setSelected((cur) => {
      const next = new Set(cur)
      if (checked) {
        next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }

  const openDialog = () => {
    setError('')
    setTemplateId('')
    setForced(false)
    setDialogOpen(true)
  }

  const assign = async () => {
    if (!templateId || selected.size === 0) return
    setSaving(true)
    setError('')
    try {
      await api.assignSubscriptionTemplate([...selected], templateId, forced)
      setDialogOpen(false)
      setSelected(new Set())
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const unassign = async (user: SubUser, id: string) => {
    setError('')
    try {
      await api.unassignSubscriptionTemplate([user.id], id)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const templateOf = (id: string) => templates.find((template) => template.id === id)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3 rounded-lg border p-3">
        <div className="min-w-0 flex-1">
          <Label>指派用户（勾选后批量指派模板）</Label>
          <div className="mt-2 grid max-h-56 gap-1 overflow-y-auto rounded-md border p-2">
            {users.length === 0 ? (
              <p className="p-2 text-sm text-muted-foreground">暂无用户</p>
            ) : (
              users.map((user) => (
                <label key={user.id} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-muted/60">
                  <input
                    type="checkbox"
                    className="size-4 shrink-0 accent-primary"
                    checked={selected.has(user.id)}
                    onChange={(event) => toggle(user.id, event.target.checked)}
                  />
                  <span className="truncate">{user.name}</span>
                  {user.disabled && <Badge variant="destructive">已停用</Badge>}
                </label>
              ))
            )}
          </div>
        </div>
        <Button onClick={openDialog} disabled={selected.size === 0 || assignable.length === 0}>
          <ClipboardCheckIcon />
          指派模板（{selected.size}）
        </Button>
      </div>

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="space-y-3">
        {[...assignedUsers.entries()]
          .sort(([a], [b]) => (templateOf(a)?.name ?? a).localeCompare(templateOf(b)?.name ?? b))
          .map(([id, entries]) => {
            const template = templateOf(id)
            return (
              <div key={id} className="rounded-lg border p-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{template?.name ?? id}</span>
                  {template && <Badge variant="secondary">{KIND_LABELS[template.kind]}</Badge>}
                  <span className="ml-auto text-xs text-muted-foreground">{entries.length} 个用户</span>
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  {entries.map(({ user, forced: isForced }) => (
                    <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                      {user.name}
                      {isForced && <Badge variant="destructive">强制</Badge>}
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title="取消该用户的此模板指派"
                        onClick={() => unassign(user, id)}
                      >
                        <XIcon />
                      </Button>
                    </span>
                  ))}
                </div>
              </div>
            )
          })}
        {unassigned.length > 0 && (
          <div className="rounded-lg border border-dashed p-3">
            <div className="flex items-center gap-2">
              <span className="font-medium">未指派</span>
              <span className="ml-auto text-xs text-muted-foreground">{unassigned.length} 个用户</span>
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              {unassigned.map((user) => (
                <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                  {user.name}
                  {user.disabled && <Badge variant="destructive">已停用</Badge>}
                </span>
              ))}
            </div>
          </div>
        )}
        {assignedUsers.size === 0 && unassigned.length === 0 && users.length === 0 ? (
          <EmptyState icon={<ClipboardCheckIcon />} title="暂无用户" description="先创建用户，再指派模板" />
        ) : null}
        {assignedUsers.size === 0 && users.length > 0 ? (
          <EmptyState icon={<ClipboardCheckIcon />} title="暂无模板指派" description="勾选上方用户后指派模板" />
        ) : null}
      </div>

      <Dialog open={dialogOpen} onOpenChange={(next) => !next && setDialogOpen(false)}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>指派模板</DialogTitle>
            <DialogDescription>
              为选中的 {selected.size} 个用户指派模板；未强制时用户自选优先，强制后用户自选失效。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-2">
              <Label>模板（按类型分组）</Label>
              <Select value={templateId} onValueChange={(id) => id && setTemplateId(id)}>
                <SelectTrigger className="w-full"><SelectValue placeholder="选择模板" /></SelectTrigger>
                <SelectContent>
                  {(['portable', 'acl4ssr', 'mihomo', 'singbox', 'quanx'] as const).map((kind) => {
                    const items = assignable.filter((template) => template.kind === kind)
                    if (items.length === 0) return null
                    return items.map((template) => (
                      <SelectItem key={template.id} value={template.id}>
                        {template.name}（{KIND_LABELS[kind]}）
                      </SelectItem>
                    ))
                  })}
                </SelectContent>
              </Select>
              {assignable.length === 0 && (
                <p className="text-xs text-muted-foreground">暂无可用模板，请先在「订阅模板」页创建或刷新缓存。</p>
              )}
            </div>
            <label className="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
              <input
                type="checkbox"
                className="size-4 shrink-0 accent-primary"
                checked={forced}
                onChange={(event) => setForced(event.target.checked)}
              />
              强制覆盖用户自选（指派后用户自选选项失效，显示跟随指派）
            </label>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button onClick={assign} disabled={saving || !templateId || selected.size === 0}>
              {saving ? '指派中…' : '指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
```

说明：`size="icon-xs"`（`button.tsx` 已定义）与 `lucide-react` 的 `ClipboardCheckIcon` 均已确认存在。

- [ ] **Step 2: Users.tsx 接入双 Tab**

修改 `src/frontend/src/pages/Users.tsx`：

- 新增 import（第 21 行 `SubscriptionRoutingFields` import 之后）：

```tsx
import { TemplateAssignmentTab } from '@/components/TemplateAssignmentTab'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
```

- 新增状态（`const [historyLoading, setHistoryLoading] = useState(false)` 之后）：

```tsx
  const [usersTab, setUsersTab] = useState<'users' | 'assign'>('users')
```

- 渲染改造：`PageHeader` 之后、`{error && ...}` 保持，将原用户列表区域包进 Tabs。原结构：

```tsx
      {!loading && users.length === 0 ? (
        <EmptyState ... />
      ) : null}
      {!loading && users.length === 0 ? null : (
        <div className="flex flex-col gap-3">
          ...
        </div>
      )}
```

替换为：

```tsx
      <Tabs value={usersTab} onValueChange={(value) => value && setUsersTab(value as 'users' | 'assign')}>
        <TabsList>
          <TabsTrigger value="users">用户</TabsTrigger>
          <TabsTrigger value="assign">模板指派</TabsTrigger>
        </TabsList>
      </Tabs>
      {usersTab === 'assign' ? (
        <TemplateAssignmentTab users={users} templates={templates} onChanged={() => load(true)} />
      ) : (
        <>
          {!loading && users.length === 0 ? (
            <EmptyState
              icon={<UsersIcon />}
              title="暂无用户"
              description="点击上方“创建用户”开始"
            />
          ) : null}
          {!loading && users.length === 0 ? null : (
            <div className="flex flex-col gap-3">
              {/* 原用户卡片列表原样保留 */}
            </div>
          )}
        </>
      )}
```

注意：用 `onChanged={() => load(true)}` 复用现有轮询加载（`load` 已有 silent 参数），避免重复定义刷新函数。

- [ ] **Step 3: 验证**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment/src/frontend && npx tsc -b && npm run lint && npx vitest run"
```
Expected: tsc 无错误、lint 通过、vitest 全过。

- [ ] **Step 4: 提交**

```bash
git add src/frontend/src/pages/Users.tsx src/frontend/src/components/TemplateAssignmentTab.tsx
git commit -m "feat(frontend): user page template assignment tab"
```

---

### Task 7: 全量验证与合并回 main

**Files:**
- 无代码文件（验证 + 分支合并）

- [ ] **Step 1: 后端全量测试与 vet**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && go test ./src/backend/... ./src/agent/... ./src/shared/... && go vet ./src/backend/... ./src/agent/... ./src/shared/..."
```
Expected: 全部 PASS，vet 无输出。

- [ ] **Step 2: 前端全量检查**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment/src/frontend && npm run check:api && npx tsc -b && npm run lint && npx vitest run"
```
Expected: 全部通过。

- [ ] **Step 3: 检查工作区干净并查看提交历史**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex/.worktree/user-template-assignment && git status --porcelain && git log --oneline main..feat/user-template-assignment"
```
Expected: 无未提交改动；提交列表 = 计划内各任务提交。

- [ ] **Step 4: 合并回 main**

```bash
git checkout main
git merge --no-ff feat/user-template-assignment -m "Merge feat/user-template-assignment: 用户模板指派"
git worktree remove .worktree/user-template-assignment
git branch -d feat/user-template-assignment
```
Expected: 合并成功，worktree 与分支清理完毕（不推送，除非用户要求）。

- [ ] **Step 5: 最终验证（main 上）**

```bash
wsl -d Ubuntu -- bash -lc "cd ~/workspace/Lattix-codex && go build ./src/backend/... && git log --oneline -3"
```
Expected: 构建通过；最近提交为 merge commit。
