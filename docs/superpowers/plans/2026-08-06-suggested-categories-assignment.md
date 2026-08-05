# 建议规则指派改为分组勾选 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用户页模板指派的「建议规则」从固定三个预设改为勾选具体规则分组（参考 miaomiaowu 规则选择交互），存储与生效均改为分组列表。

**Architecture:** 后端 `user_subscription_profiles` 新增 `assigned_suggested_categories`（JSON 数组）列并迁移回填旧 preset；`EffectiveProfile` 与面板 API 从 preset 语义切换为 categories；前端指派对话框改为「建议规则/使用模板」模式切换 + 预设快捷填充下拉 + 分组勾选网格。

**Tech Stack:** Go 1.26（go.work：backend/shared/agent）、modernc.org/sqlite（PRAGMA user_version 迁移）、React + Vite + wouter + shadcn/ui（base-ui）、openapi-typescript codegen（本任务不涉及 openapi 变更，assign/unassign 端点契约仅引用通用 RPCBody）。

**Spec:** `docs/superpowers/specs/2026-08-06-suggested-categories-assignment-design.md`

## Global Constraints

- 后端测试命令（仓库根目录，go.work 生效）：`go test ./...`
- 前端命令（`src/frontend`）：`bun run build`（含 `generate-api-types.mjs --check`）、`bun run test`、`bun run lint`
- `docs/openapi.yaml` 与路由由 `panel/contract_test.go` 强校验——本计划不改 openapi.yaml（assign/unassign 已在其中且只引用通用 RPCBody）
- 旧 `assigned_suggested_preset` 数据库列保留不删除，代码不再读写
- 前端无 Collapsible 组件，折叠用原生 `<details>`（与 `SubscriptionRoutingFields.tsx:130` 的「客户端原生模板覆盖」一致）
- 分组定义以 `sub/builtInCategories`（`src/backend/internal/sub/policy.go:25-44`）与前端 `SubscriptionRuleCategory` 的 `in_minimal`/`in_balanced` 标志为唯一事实来源
- 不参考 miaomiaowu 的 DNS 段

---

### Task 1: Store — 新增列、结构体字段、持久化与迁移回填

**Files:**
- Modify: `src/backend/internal/store/store.go`（Schema DDL，~292 行）
- Modify: `src/backend/internal/store/migrations.go`（schemaVersion 15 + ensureColumns + 回填）
- Modify: `src/backend/internal/store/subscriptions.go`（struct + 查询 + 保存）
- Test: `src/backend/internal/store/subscriptions_assignment_test.go`
- Test: `src/backend/internal/store/migrations_test.go`

**Interfaces:**
- Consumes: `store.presetCategorySets`（subscriptions.go:114，`map[string][]string`）
- Produces: `store.SubscriptionProfile.AssignedSuggestedCategories string`（JSON 数组字符串）；DB 列 `assigned_suggested_categories TEXT NOT NULL DEFAULT ''`

- [ ] **Step 1: 写失败测试 — 新字段持久化**

在 `src/backend/internal/store/subscriptions_assignment_test.go` 的 `TestSubscriptionProfilePersistsSuggestedPresetAssignment`（第 164-194 行）中，把 `AssignedSuggestedPreset: "minimal"` 改为同时写入新字段：

```go
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:                  `["ai"]`,
		AssignedSuggestedPreset:         "minimal", AssignForcedPortable: true,
		AssignedSuggestedCategories:     `["private","domestic","overseas"]`,
		GenerationStatus: SubscriptionGenerationMissing,
	}
```
并把断言改为（旧字段断言保留，新字段一并断言）：

```go
	if got.AssignedSuggestedPreset != "minimal" || got.AssignedSuggestedCategories != `["private","domestic","overseas"]` || !got.AssignForcedPortable {
		t.Fatalf("suggested assignment lost: %+v", got)
	}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run TestSubscriptionProfilePersistsSuggestedPresetAssignment -v`
Expected: FAIL（`got.AssignedSuggestedCategories` 为空 / 编译错误：无该字段）

- [ ] **Step 3: store.go Schema DDL 加列**

在 `src/backend/internal/store/store.go` 第 292 行 `assigned_suggested_preset` 行之后加：

```sql
    assigned_suggested_categories TEXT NOT NULL DEFAULT '', -- 建议规则分组指派（JSON 数组，与模板指派互斥）
```

- [ ] **Step 4: migrations.go — schemaVersion 15 + 加列 + 回填**

`src/backend/internal/store/migrations.go`：
1. 第 14 行 `const schemaVersion = 14` → `const schemaVersion = 15`
2. 在第 181 行 `return migrateCommands(tx)` 之前（map 循环之后）追加独立加列 + 回填块（`migrations.go` 已 import `encoding/json`；注意：**不要**把该列加入上方 map 的 `"user_subscription_profiles"` 列表，否则重复 ensureColumns 会得到 added=false 导致回填不执行）：

```go
	addedProfiles, err := ensureColumns(tx, "user_subscription_profiles", []columnMigration{
		{"assigned_suggested_categories", "TEXT NOT NULL DEFAULT ''"},
	})
	if err != nil {
		return err
	}
	if addedProfiles["assigned_suggested_categories"] {
		// 旧建议规则预设指派展开为分组列表，效果不变。
		for preset, ids := range presetCategorySets {
			raw, err := json.Marshal(ids)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE user_subscription_profiles
				SET assigned_suggested_categories = ?
				WHERE assigned_suggested_preset = ? AND assigned_suggested_categories = ''`,
				string(raw), preset); err != nil {
				return fmt.Errorf("backfill assigned_suggested_categories: %w", err)
			}
		}
	}
```

- [ ] **Step 5: subscriptions.go — struct + 查询 + 保存**

`src/backend/internal/store/subscriptions.go`：
1. struct（第 40 行 `AssignedSuggestedPreset` 之后）加：

```go
	AssignedSuggestedCategories string // 建议规则分组指派（JSON 数组），与 AssignedPortableTemplateID 互斥
```

2. 查询（第 154-165 行）：SELECT 列表 `assigned_suggested_preset,` 后加 `assigned_suggested_categories,`，Scan 列表对应加 `&profile.AssignedSuggestedCategories,`
3. 保存（第 197-223 行）：INSERT 列列表与 VALUES 占位符各加一个 `assigned_suggested_categories`，ON CONFLICT SET 加 `assigned_suggested_categories=excluded.assigned_suggested_categories,`，参数列表加 `profile.AssignedSuggestedCategories`（放在 `profile.AssignedSuggestedPreset` 之后）

- [ ] **Step 6: migrations_test.go — 版本断言 + 回填测试**

1. `TestMigrateLegacyPreservesSubToken` 第 343-345 行 `version != 14` → `version != 15`
2. `TestOpenMigratesLegacySchemaAndPreservesData` 的 `assertColumns` 调用后追加一行：

```go
	assertColumns(t, st.db, "user_subscription_profiles", "assigned_suggested_categories")
```

3. 新增回填测试（文件末尾追加）：

```go
func TestOpenBackfillsAssignedSuggestedCategories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suggested.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 v14 库：旧表带 assigned_suggested_preset 指派，无新列。
	if _, err := db.Exec(legacySchema + `
CREATE TABLE user_subscription_profiles (
    user_id INTEGER PRIMARY KEY,
    mode TEXT NOT NULL DEFAULT 'suggested',
    preset TEXT NOT NULL DEFAULT 'balanced',
    categories TEXT NOT NULL DEFAULT '[]',
    portable_template_id TEXT NOT NULL DEFAULT '',
    mihomo_template_id TEXT NOT NULL DEFAULT '',
    singbox_template_id TEXT NOT NULL DEFAULT '',
    quanx_template_id TEXT NOT NULL DEFAULT '',
    assigned_portable_template_id TEXT NOT NULL DEFAULT '',
    assigned_mihomo_template_id TEXT NOT NULL DEFAULT '',
    assigned_singbox_template_id TEXT NOT NULL DEFAULT '',
    assigned_quanx_template_id TEXT NOT NULL DEFAULT '',
    assigned_suggested_preset TEXT NOT NULL DEFAULT '',
    assign_forced_portable INTEGER NOT NULL DEFAULT 0,
    assign_forced_mihomo INTEGER NOT NULL DEFAULT 0,
    assign_forced_singbox INTEGER NOT NULL DEFAULT 0,
    assign_forced_quanx INTEGER NOT NULL DEFAULT 0,
    generation_status TEXT NOT NULL DEFAULT 'missing',
    generation_error TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO user_subscription_profiles (user_id, mode, preset, categories, assigned_suggested_preset, generation_status)
VALUES (1, 'suggested', 'balanced', '["ai"]', 'comprehensive', 'missing');
PRAGMA user_version = 14;`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var got string
	if err := st.db.QueryRow(`SELECT assigned_suggested_categories FROM user_subscription_profiles WHERE user_id = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(got), &ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(presetCategorySets["comprehensive"]) {
		t.Fatalf("backfilled categories = %v", ids)
	}
	for _, want := range presetCategorySets["comprehensive"] {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("backfilled categories missing %q: %v", want, ids)
		}
	}
}
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/store/`
Expected: PASS（Task 1 全部测试含新回填测试）

- [ ] **Step 8: 提交**

```bash
git add src/backend/internal/store/
git commit -m "feat(store): assigned suggested categories column with preset backfill"
```

---

### Task 2: EffectiveProfile 与发布标签切换为分组

**Files:**
- Modify: `src/backend/internal/store/subscriptions.go`（EffectiveProfile + 删 presetCategoriesJSON）
- Modify: `src/backend/internal/sub/publisher.go:161-168`（source label）
- Test: `src/backend/internal/store/subscriptions_assignment_test.go`
- Test: `src/backend/internal/sub/subscription_routing_test.go`

**Interfaces:**
- Consumes: `SubscriptionProfile.AssignedSuggestedCategories`（Task 1）
- Produces: 发布生效规则——`AssignedSuggestedCategories != ""` 时 Mode=suggested、CategoriesJSON=指派值、Preset 占位 "balanced"；source label 恒为「内置建议规则」

- [ ] **Step 1: 写失败测试 — EffectiveProfile 用分组生效**

`src/backend/internal/store/subscriptions_assignment_test.go`：

1. `TestEffectiveProfileAssignedSuggestedPresetApplies`（第 70-100 行）整体替换为：

```go
func TestEffectiveProfileAssignedSuggestedCategoriesApplies(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:              `["ai"]`,
		AssignedSuggestedCategories: `["ads","ai","gaming"]`,
		AssignForcedPortable:        false,
	}
	got := EffectiveProfile(profile)
	if got.Mode != SubscriptionModeSuggested || got.Preset != "balanced" {
		t.Fatalf("assigned suggested not applied: mode=%q preset=%q", got.Mode, got.Preset)
	}
	if got.CategoriesJSON != `["ads","ai","gaming"]` {
		t.Fatalf("assigned categories not applied: %q", got.CategoriesJSON)
	}
}
```

2. `TestEffectiveProfileUserTemplateWinsOverSuggestedAssignment`（第 102-111 行）中 `AssignedSuggestedPreset: "minimal"` → `AssignedSuggestedCategories: `["private","domestic","overseas"]``
3. `TestEffectiveProfileForcedSuggestedOverridesUserChoice`（第 113-126 行）中 `AssignedSuggestedPreset: "minimal"` → `AssignedSuggestedCategories: `["private","domestic","overseas"]``，断言 `got.Preset != "minimal"` → `got.Preset != "balanced"`（place 值）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run 'TestEffectiveProfileAssignedSuggestedCategoriesApplies|TestEffectiveProfileUserTemplateWinsOverSuggestedAssignment|TestEffectiveProfileForcedSuggestedOverridesUserChoice' -v`
Expected: FAIL（CategoriesJSON 未被指派值覆盖 / preset 仍为 minimal）

- [ ] **Step 3: 实现 EffectiveProfile**

`src/backend/internal/store/subscriptions.go` 第 128-148 行整体替换为：

```go
// EffectiveProfile 合并管理员指派与用户自选，返回订阅构建实际使用的 profile：
// 指派槽位在用户未自选（或强制覆盖）时生效；用户自选优先于普通指派。
func EffectiveProfile(p SubscriptionProfile) SubscriptionProfile {
	if p.AssignedSuggestedCategories != "" && (p.AssignForcedPortable || p.Mode != SubscriptionModeTemplate || p.PortableTemplateID == "") {
		p.Mode = SubscriptionModeSuggested
		p.Preset = "balanced" // 占位值；发布标签不再依赖 preset 名。
		p.CategoriesJSON = p.AssignedSuggestedCategories
	} else if p.AssignedPortableTemplateID != "" && (p.AssignForcedPortable || p.Mode != SubscriptionModeTemplate || p.PortableTemplateID == "") {
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
（`presetCategorySets` 保留——Task 1 回填仍用；`presetCategoriesJSON` 函数（第 120-126 行）自此无调用方，直接删除。）

- [ ] **Step 4: publisher.go source label**

`src/backend/internal/sub/publisher.go` 第 168 行：

```go
		policy, err := suggestedPolicy(sortedSelectedCategories(selected))
		return policy, "内置建议规则", nil, err
```
（`strings.Title` 不再使用；`strings` 包第 61/265/304-305 行仍在使用，import 保留。）

- [ ] **Step 5: sub 发布测试改用分组**

`src/backend/internal/sub/subscription_routing_test.go` 第 539-567 行 `TestPublishUserAssignedSuggestedPresetProducesUsableRules` 整体替换为：

```go
func TestPublishUserAssignedSuggestedCategoriesProducesUsableRules(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000007", "suggested-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户默认（建议规则/balanced/单分类），被指派 ads+ai+gaming 分组。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:                  `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["ads","ai","gaming"]`,
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "AI 服务") || !strings.Contains(clash, "广告拦截") || !strings.Contains(clash, "游戏平台") {
		t.Fatalf("assigned suggested categories rules missing: %s", clash)
	}
	if strings.Contains(clash, "油管视频") || strings.Contains(clash, "电报消息") {
		t.Fatalf("unassigned categories leaked into rules: %s", clash)
	}
}
```

- [ ] **Step 6: 运行测试**

Run: `go test ./internal/store/ ./internal/sub/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add src/backend/internal/store/ src/backend/internal/sub/
git commit -m "feat(sub): apply assigned suggested categories in effective profile"
```

---

### Task 3: Panel API 与 DTO 切换为 suggested_categories

**Files:**
- Modify: `src/backend/internal/panel/template_assignment.go`
- Modify: `src/backend/internal/panel/users.go:179-215`（DTO）
- Modify: `src/backend/internal/store/subscriptions.go`（删除 `AssignedSuggestedPreset` 字段与读写）
- Test: `src/backend/internal/panel/template_assignment_test.go`

**Interfaces:**
- Consumes: `sub.Categories()`（`src/backend/internal/sub/templates.go:41`，返回 `[]CategoryDTO`，字段 `ID string`）
- Produces: 请求体 `suggested_categories: []string`（assign/unassign）；响应 DTO `assigned_suggested_categories: []string`；删除 `assigned_suggested_preset`

- [ ] **Step 1: 写失败测试 — API 用 suggested_categories**

`src/backend/internal/panel/template_assignment_test.go`：

1. `TestAssignSubscriptionTemplateSuggestedPreset`（第 194-223 行）整体替换为：

```go
func TestAssignSubscriptionTemplateSuggestedCategories(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000015", "tok-f", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	// 乱序 + 重复 → 按内置顺序去重存储。
	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"suggested_categories":["gaming","ads","ai","gaming"],"forced":true}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != `["ads","ai","gaming"]` || !profile.AssignForcedPortable {
		t.Fatalf("suggested assignment = %+v", profile)
	}
	if profile.AssignedPortableTemplateID != "" {
		t.Fatalf("template slot unexpectedly set: %+v", profile)
	}
	snapshot, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil || snapshot.Status != store.SubscriptionGenerationReady {
		t.Fatalf("snapshot = %+v, err %v", snapshot, err)
	}
}
```

2. `TestAssignSubscriptionTemplateSuggestedMutualExclusion`（第 225-268 行）中三处请求体替换：`"suggested_preset":"balanced"` → `"suggested_categories":["ai","youtube","google","private","domestic","telegram","github","overseas"]`；`"suggested_preset":"minimal"` → `"suggested_categories":["private","domestic","overseas"]`；断言 `profile.AssignedSuggestedPreset != ""` 与 `!= "minimal"` 改为 `profile.AssignedSuggestedCategories != ""` 与 `!= `["private","domestic","overseas"]``
3. `TestAssignSubscriptionTemplateSuggestedValidation`（第 270-287 行）整体替换为：

```go
func TestAssignSubscriptionTemplateSuggestedValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	if _, code := assignRequest(t, server, `{"user_ids":[1],"suggested_categories":["unknown-category"]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("unknown category: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"suggested_categories":[]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("empty categories: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"template_id":"tpl","suggested_categories":["ai"]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("both targets: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("no target: code=%q", code)
	}
}
```

4. `TestUnassignSubscriptionTemplateSuggestedKeepsUserChoice`（第 289-333 行）：保存时 `AssignedSuggestedPreset: "balanced", AssignForcedPortable: true` → `AssignedSuggestedCategories: `["ai","youtube","google","private","domestic","telegram","github","overseas"]`, AssignForcedPortable: true`；请求体 `"suggested_preset":"balanced"` → `"suggested_categories":["ai","youtube","google","private","domestic","telegram","github","overseas"]`；断言 `profile.AssignedSuggestedPreset != ""` → `profile.AssignedSuggestedCategories != ""`

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/ -run 'TestAssignSubscriptionTemplateSuggested|TestUnassignSubscriptionTemplateSuggested' -v`
Expected: `TestAssignSubscriptionTemplateSuggestedCategories` FAIL（status=400，因旧实现不认识 `suggested_categories`，`template_id` 与 `suggested_preset` 均为空 → CodeInvalidArgument）；validation/unassign 测试在旧实现下可能部分通过，属正常——它们将作为回归测试在实现后全部转绿。

- [ ] **Step 3: 实现 panel template_assignment.go**

`src/backend/internal/panel/template_assignment.go` 整体重构（import 增加 `encoding/json`、`sort`、`lattix/backend/internal/sub`；`errors`/`fmt`/`net/http`/`strings`/`store` 保留）：

1. 删除 `validSuggestedPreset`（第 58-65 行），替换为：

```go
// normalizeSuggestedCategories 校验并规范化分组列表：未知 id → 400；空 → 400；去重并按内置顺序排序。
func normalizeSuggestedCategories(raw []string) ([]string, error) {
	known := make(map[string]bool)
	order := make(map[string]int)
	for index, category := range sub.Categories() {
		known[category.ID] = true
		order[category.ID] = index
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !known[id] {
			return nil, fmt.Errorf("未知分组 %q", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, errors.New("suggested_categories 不能为空")
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out, nil
}
```

2. `assignmentTarget`（第 68-96 行）签名与实现替换为：

```go
// assignmentTarget 解析指派目标：template_id 与 suggested_categories 二选一（均空或均非空 → 400）。
func (s *Server) assignmentTarget(w http.ResponseWriter, r *http.Request, templateID string, suggestedCategories []string) (target *store.SubscriptionTemplate, categories []string, ok bool) {
	templateID = strings.TrimSpace(templateID)
	if (templateID == "") == (len(suggestedCategories) == 0) {
		writeError(w, http.StatusBadRequest, "template_id 与 suggested_categories 必须二选一")
		return nil, nil, false
	}
	if len(suggestedCategories) > 0 {
		normalized, err := normalizeSuggestedCategories(suggestedCategories)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, nil, false
		}
		return nil, normalized, true
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return nil, nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	if strings.TrimSpace(template.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("订阅模板 %q 尚无有效缓存", template.Name))
		return nil, nil, false
	}
	return &template, nil, true
}
```

3. `handleAssignSubscriptionTemplate`：req 结构体 `SuggestedPreset string \`json:"suggested_preset"\`` → `SuggestedCategories []string \`json:"suggested_categories"\``；调用处 `s.assignmentTarget(w, r, req.TemplateID, req.SuggestedPreset)` → `(req.TemplateID, req.SuggestedCategories)`；第 134-145 行建议规则分支替换为：

```go
		if categories != nil {
			// 建议规则指派与模板指派同为主策略槽位，互斥。
			profile.AssignedPortableTemplateID = ""
			raw, _ := json.Marshal(categories)
			profile.AssignedSuggestedCategories = string(raw)
			profile.AssignForcedPortable = req.Forced
		} else {
			if err := applyTemplateAssignment(&profile, template.Kind, template.ID, req.Forced); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			profile.AssignedSuggestedCategories = ""
		}
```
audit 与响应 map 的 `"suggested_preset": suggestedPreset` → `"suggested_categories": categories`

4. `handleUnassignSubscriptionTemplate`：req 结构体同改；调用处同改；第 202-205 行：

```go
		if categories != nil {
			profile.AssignedSuggestedCategories = ""
			profile.AssignForcedPortable = false
		} else {
```
audit 与响应 map 同改。

- [ ] **Step 4: 实现 users.go DTO**

`src/backend/internal/panel/users.go`：
1. 第 195 行 `AssignedSuggestedPreset   string   \`json:"assigned_suggested_preset"\`` → `AssignedSuggestedCategories []string \`json:"assigned_suggested_categories"\``
2. `subscriptionProfileToDTO`（第 198-215 行）：`AssignedSuggestedPreset: profile.AssignedSuggestedPreset,` 替换为：

```go
		AssignedSuggestedCategories: assignedSuggestedCategories(profile),
```
3. 文件内新增（复用既有 `json` import）：

```go
func assignedSuggestedCategories(profile store.SubscriptionProfile) []string {
	var ids []string
	_ = json.Unmarshal([]byte(profile.AssignedSuggestedCategories), &ids)
	return ids
}
```

- [ ] **Step 5: store 删除旧字段**

`src/backend/internal/store/subscriptions.go`：
1. struct 删 `AssignedSuggestedPreset string` 行（第 40 行）
2. 查询（第 154-165 行）：SELECT/Scan 删 `assigned_suggested_preset` 与 `&profile.AssignedSuggestedPreset`
3. 保存（第 197-223 行）：INSERT 列、VALUES、ON CONFLICT、参数全删 `assigned_suggested_preset`
4. `presetCategoriesJSON` 若已无用则删除（`rg presetCategoriesJSON src/backend` 确认仅剩本函数定义时删）；`presetCategorySets` 保留（回填用）
5. store.go 第 292 行 Schema DDL 的 `assigned_suggested_preset` 行保留（列不删除，注释更新为「已废弃，保留兼容；新指派使用 assigned_suggested_categories」）；`migrations.go` 第 167 行的 ensureColumns 条目保留
6. `src/backend/internal/store/subscriptions_assignment_test.go` 的 `TestSubscriptionProfilePersistsSuggestedPresetAssignment`（Task 1 改过）同步更新——去掉旧字段引用：测试名改为 `TestSubscriptionProfilePersistsSuggestedCategoriesAssignment`，`profile` 中删 `AssignedSuggestedPreset: "minimal",`，断言改为：

```go
	if got.AssignedSuggestedCategories != `["private","domestic","overseas"]` || !got.AssignForcedPortable {
		t.Fatalf("suggested assignment lost: %+v", got)
	}
```

- [ ] **Step 6: 运行全部后端测试**

Run: `go test ./...`
Expected: PASS（含 `panel/contract_test.go`；本任务不改 openapi.yaml）

- [ ] **Step 7: 提交**

```bash
git add src/backend/
git commit -m "feat(panel): assign suggested rules by category list, drop preset assignment"
```

---

### Task 4: 前端 — 类型、API 客户端与订阅设置提示

**Files:**
- Modify: `src/frontend/src/lib/types.ts:586`
- Modify: `src/frontend/src/lib/subscription-routing.ts:19`
- Modify: `src/frontend/src/lib/api.ts:321-330`
- Modify: `src/frontend/src/components/SubscriptionRoutingFields.tsx:57-63`

**Interfaces:**
- Consumes: `SubscriptionRuleCategory`（types.ts:609，含 `id`/`label`/`icon`/`in_minimal`/`in_balanced`）
- Produces: `SubscriptionRoutingProfile.assigned_suggested_categories: string[]`；`api.assignSubscriptionTemplate(userIds, { template_id? | suggested_categories? }, forced)`

- [ ] **Step 1: 类型与默认值**

`src/frontend/src/lib/types.ts` 第 586 行 `assigned_suggested_preset: string` → `assigned_suggested_categories: string[]`

`src/frontend/src/lib/subscription-routing.ts` 第 19 行 `assigned_suggested_preset: '',` → `assigned_suggested_categories: [],`

- [ ] **Step 2: api.ts**

`src/frontend/src/lib/api.ts` 第 321-330 行替换为：

```ts
  assignSubscriptionTemplate: (userIds: number[], target: { template_id?: string; suggested_categories?: string[] }, forced: boolean) =>
    requester.post<{ user_ids: number[]; template_id?: string; suggested_categories?: string[]; forced: boolean }>(
      '/api/subscription/template/assign',
      { user_ids: userIds, ...target, forced },
    ),
  unassignSubscriptionTemplate: (userIds: number[], target: { template_id?: string; suggested_categories?: string[] }) =>
    requester.post<{ user_ids: number[]; template_id?: string; suggested_categories?: string[] }>(
      '/api/subscription/template/unassign',
      { user_ids: userIds, ...target },
    ),
```

- [ ] **Step 3: SubscriptionRoutingFields 提示**

`src/frontend/src/components/SubscriptionRoutingFields.tsx`：
1. 第 57-63 行的提示块替换为：

```tsx
      {value.assigned_suggested_categories.length > 0 && (
        <Notice tone="info">
          {value.assign_forced_portable
            ? `已强制指派建议规则（${suggestedCategoryLabels(value.assigned_suggested_categories, categories)}），以下自选设置暂不生效。`
            : `已指派建议规则（${suggestedCategoryLabels(value.assigned_suggested_categories, categories)}）（自选优先，可在此覆盖）。`}
        </Notice>
      )}
```
2. 文件顶部（`categoriesForPreset` 之后）新增：

```tsx
function suggestedCategoryLabels(ids: string[], categories: SubscriptionRuleCategory[]): string {
  const byId = new Map(categories.map((category) => [category.id, category.label]))
  const labels = ids.map((id) => byId.get(id) ?? id)
  return labels.length === 0 ? '未指定分组' : labels.join('、')
}
```
3. `presetLabels` 保留（规则预设下拉仍使用）。

- [ ] **Step 4: 类型检查**

Run: `cd src/frontend && bunx tsc -b --force`
Expected: PASS（`assigned_suggested_preset` 引用应全部清零，可 `rg "assigned_suggested_preset" src/frontend/src` 复核）

- [ ] **Step 5: 提交**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/lib/subscription-routing.ts src/frontend/src/lib/api.ts src/frontend/src/components/SubscriptionRoutingFields.tsx
git commit -m "feat(frontend): suggested categories in routing profile types and notices"
```

---

### Task 5: 前端 — 模板指派对话框改为分组勾选（miaomiaowu 风格）

**Files:**
- Modify: `src/frontend/src/components/TemplateAssignmentTab.tsx`（整体重写）
- Modify: `src/frontend/src/pages/Users.tsx:521`（传 categories prop）
- Test: 无既有组件测试；用 `bun run build` + `bun run test` + `bun run lint` 验证

**Interfaces:**
- Consumes: `SubscriptionRuleCategory`（`api.subscriptionCategories`，Users.tsx 已加载为 `ruleCategories`）
- Produces: `TemplateAssignmentTab({ users, templates, categories, onChanged })`

- [ ] **Step 1: Users.tsx 传参**

`src/frontend/src/pages/Users.tsx` 第 521 行：

```tsx
        <TemplateAssignmentTab users={users} templates={templates} categories={ruleCategories} onChanged={() => load(true)} />
```

- [ ] **Step 2: 重写 TemplateAssignmentTab.tsx**

`src/frontend/src/components/TemplateAssignmentTab.tsx` 整体替换为（完整文件）：

```tsx
import { useMemo, useState } from 'react'
import { ChevronDownIcon, ChevronUpIcon, ClipboardCheckIcon, XIcon } from 'lucide-react'

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
import type { SubUser, SubscriptionRuleCategory, SubscriptionTemplate } from '@/lib/types'

const SLOTS = [
  ['assigned_portable_template_id', 'assign_forced_portable'],
  ['assigned_mihomo_template_id', 'assign_forced_mihomo'],
  ['assigned_singbox_template_id', 'assign_forced_singbox'],
  ['assigned_quanx_template_id', 'assign_forced_quanx'],
] as const

const PRESET_OPTIONS = [
  ['custom', '自定义'],
  ['minimal', '极简规则'],
  ['balanced', '均衡规则（推荐）'],
  ['comprehensive', '完整规则'],
] as const

type PresetId = (typeof PRESET_OPTIONS)[number][0]

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
  categories: SubscriptionRuleCategory[]
  onChanged: () => void
}

export function TemplateAssignmentTab({ users, templates, categories, onChanged }: TemplateAssignmentTabProps) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [ruleMode, setRuleMode] = useState<'suggested' | 'template'>('suggested')
  const [preset, setPreset] = useState<PresetId>('balanced')
  const [selectedCategories, setSelectedCategories] = useState<string[]>([])
  const [categoryOpen, setCategoryOpen] = useState(true)
  const [templateId, setTemplateId] = useState('')
  const [forced, setForced] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const categoryById = useMemo(() => new Map(categories.map((category) => [category.id, category])), [categories])

  const categoriesForPreset = (next: Exclude<PresetId, 'custom'>): string[] => {
    if (next === 'comprehensive') return categories.map((category) => category.id)
    return categories
      .filter((category) => next === 'minimal' ? category.in_minimal : category.in_balanced)
      .map((category) => category.id)
  }

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

  const suggestedBySet = useMemo(() => {
    const groups = new Map<string, SubUser[]>()
    for (const user of users) {
      const ids = user.routing.assigned_suggested_categories
      if (ids.length === 0) continue
      const key = JSON.stringify(ids)
      const list = groups.get(key) ?? []
      list.push(user)
      groups.set(key, list)
    }
    return groups
  }, [users])

  const suggestedTitle = (ids: string[]) => {
    const labels = ids.map((id) => {
      const category = categoryById.get(id)
      return category ? `${category.icon} ${category.label}` : id
    })
    return labels.length === 0 ? '未指定分组' : labels.join('、')
  }

  const unassigned = users.filter((user) =>
    !SLOTS.some(([assignedField]) => user.routing[assignedField] as string)
    && user.routing.assigned_suggested_categories.length === 0,
  )

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

  const toggleCategory = (id: string, checked: boolean) => {
    setSelectedCategories((cur) => (checked ? [...cur, id] : cur.filter((item) => item !== id)))
  }

  const openDialog = () => {
    setError('')
    setRuleMode('suggested')
    setPreset('balanced')
    setSelectedCategories(categoriesForPreset('balanced'))
    setCategoryOpen(true)
    setTemplateId('')
    setForced(false)
    setDialogOpen(true)
  }

  const assign = async () => {
    if (selected.size === 0) return
    if (ruleMode === 'template' && !templateId) return
    if (ruleMode === 'suggested' && selectedCategories.length === 0) return
    setSaving(true)
    setError('')
    try {
      const target = ruleMode === 'template'
        ? { template_id: templateId }
        : { suggested_categories: selectedCategories }
      await api.assignSubscriptionTemplate([...selected], target, forced)
      setDialogOpen(false)
      setSelected(new Set())
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const unassign = async (user: SubUser, target: { template_id?: string; suggested_categories?: string[] }) => {
    setError('')
    try {
      await api.unassignSubscriptionTemplate([user.id], target)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const unassignTemplate = (user: SubUser, id: string) => unassign(user, { template_id: id })
  const unassignSuggested = (user: SubUser, ids: string[]) => unassign(user, { suggested_categories: ids })

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
        <Button onClick={openDialog} disabled={selected.size === 0 || (assignable.length === 0 && ruleMode === 'template')}>
          <ClipboardCheckIcon />
          指派模板（{selected.size}）
        </Button>
      </div>

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="space-y-3">
        {[...suggestedBySet.entries()].map(([key, groupUsers]) => {
          const ids = JSON.parse(key) as string[]
          return (
            <div key={key} className="rounded-lg border p-3">
              <div className="flex items-center gap-2">
                <span className="font-medium">建议规则 · {suggestedTitle(ids)}</span>
                <Badge variant="secondary">主策略</Badge>
                <span className="ml-auto text-xs text-muted-foreground">{groupUsers.length} 个用户</span>
              </div>
              <div className="mt-2 flex flex-wrap gap-2">
                {groupUsers.map((user) => (
                  <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                    {user.name}
                    {user.routing.assign_forced_portable && <Badge variant="destructive">强制</Badge>}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title="取消该用户的建议规则指派"
                      onClick={() => unassignSuggested(user, ids)}
                    >
                      <XIcon />
                    </Button>
                  </span>
                ))}
              </div>
            </div>
          )
        })}
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
                        onClick={() => unassignTemplate(user, id)}
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
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>指派模板</DialogTitle>
            <DialogDescription>
              为选中的 {selected.size} 个用户指派模板；未强制时用户自选优先，强制后用户自选失效。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex gap-2">
              <Button
                type="button"
                variant={ruleMode === 'suggested' ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => setRuleMode('suggested')}
              >
                建议规则
              </Button>
              <Button
                type="button"
                variant={ruleMode === 'template' ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => setRuleMode('template')}
              >
                使用模板
              </Button>
            </div>

            {ruleMode === 'suggested' ? (
              <>
                <div className="space-y-2">
                  <Label>规则选择</Label>
                  <Select
                    value={preset}
                    onValueChange={(value) => {
                      if (!value) return
                      const next = value as PresetId
                      setPreset(next)
                      if (next !== 'custom') setSelectedCategories(categoriesForPreset(next))
                    }}
                  >
                    <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {PRESET_OPTIONS.map(([id, label]) => <SelectItem key={id} value={id}>{label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    {preset === 'custom' && '自定义选择需要的规则类别'}
                    {preset === 'minimal' && '已自动选择基础规则，可以手动调整'}
                    {preset === 'balanced' && '已自动选择常用规则，可以手动调整'}
                    {preset === 'comprehensive' && '已自动选择所有规则，可以手动调整'}
                  </p>
                </div>
                <details
                  open={categoryOpen}
                  onToggle={(event) => setCategoryOpen(event.currentTarget.open)}
                  className="rounded-md border p-3"
                >
                  <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium">
                    <span>生效分组</span>
                    <span className="text-xs text-muted-foreground">已选择 {selectedCategories.length} 个类别</span>
                    <span className="ml-auto">
                      {categoryOpen ? <ChevronUpIcon className="size-4" /> : <ChevronDownIcon className="size-4" />}
                    </span>
                  </summary>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {categories.map((category) => (
                      <label key={category.id} className="flex min-w-0 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
                        <input
                          type="checkbox"
                          className="size-4 shrink-0 accent-primary"
                          checked={selectedCategories.includes(category.id)}
                          onChange={(event) => toggleCategory(category.id, event.target.checked)}
                        />
                        <span aria-hidden="true">{category.icon}</span>
                        <span className="min-w-0 break-words">{category.label}</span>
                      </label>
                    ))}
                  </div>
                </details>
              </>
            ) : (
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
                  <p className="text-xs text-muted-foreground">模板缓存为空时可先指派建议规则。</p>
                )}
              </div>
            )}

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
            <Button
              onClick={assign}
              disabled={saving || selected.size === 0 || (ruleMode === 'template' ? !templateId : selectedCategories.length === 0)}
            >
              {saving ? '指派中…' : '指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
```

- [ ] **Step 3: 类型检查**

Run: `cd src/frontend && bunx tsc -b --force`
Expected: PASS

- [ ] **Step 4: 构建 + 测试 + lint**

Run: `cd src/frontend && bun run build && bun run test && bun run lint`
Expected: 全部 PASS（build 内含 codegen `--check`，未改 openapi.yaml 应无变化）

- [ ] **Step 5: 提交**

```bash
git add src/frontend/src/components/TemplateAssignmentTab.tsx src/frontend/src/pages/Users.tsx
git commit -m "feat(frontend): suggested rule assignment with category picker"
```

---

### Task 6: 全量验证与收尾

**Files:**
- 无代码变更；仅验证

- [ ] **Step 1: 全量后端测试**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 2: 全量前端验证**

Run: `cd src/frontend && bun run build && bun run test && bun run lint`
Expected: 全部 PASS

- [ ] **Step 3: 残留引用检查**

Run: `rg -n "assigned_suggested_preset|suggested_preset|validSuggestedPreset" src docs --glob '!docs/openapi.yaml' --glob '!**/api-contract.generated.ts'`
Expected: 仅剩 `docs/superpowers/plans/`、`docs/superpowers/specs/` 内的历史文档引用与 `store.go` 注释「已废弃」说明（DB 列名保留）

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "chore: verify suggested categories assignment end to end"
```
（若 Step 3 无残留，可跳过本次提交——无变更时 `git commit` 会报 nothing to commit，改为不提交）
