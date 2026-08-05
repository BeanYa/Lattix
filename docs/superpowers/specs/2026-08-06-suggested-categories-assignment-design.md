# 建议规则指派改为分组勾选

日期：2026-08-06 · 状态：设计已确认

## 背景

用户页「模板指派」Tab 中，指派建议规则（suggested）目前只能选择固定的 Minimal / Balanced / Comprehensive 三个预设，存储在 `assigned_suggested_preset` 字段，发布时按 `presetCategorySets` 展开为固定分组集合生效。管理员无法按需指派具体的规则分组（如只指派 AI 服务 + 电报 + Github）。

要求：指派建议规则时改为可勾选具体分组；交互参照 miaomiaowu（Kevin-231213/miaomiaowu）生成订阅时的规则选择（`rule-selector.tsx` + `predefined-rules.ts`），**不参考其 DNS 段**（该实现 DNS 配置有问题，本功能不涉及 DNS 改动）。

## 目标

- 指派建议规则 = 指派一组具体分组（categories），不再存储预设名。
- 指派对话框采用 miaomiaowu 规则选择交互：预设下拉（自定义/极简/均衡/完整）作为快捷填充 + 可折叠分组勾选网格（图标 + 标签，显示已选数量）。
- 已有 `assigned_suggested_preset` 指派自动迁移为分组列表，旧用户指派效果不变。
- 前端展示（指派列表、订阅设置提示）改为显示具体分组。

## 非目标

- 不改动用户自选（mode/preset/categories）的数据模型与交互，用户自选仍支持预设选择。
- 不参考 / 不修改 miaomiaowu 的 DNS 配置。
- 不删除旧 `assigned_suggested_preset` 数据库列（迁移只加列；旧列保留不再读写）。

## 设计

### 1. 数据模型与迁移（backend/store）

`user_subscription_profiles` 新增列（`migrations.go` 的 ensureColumns 表，schemaVersion +1）：

```sql
assigned_suggested_categories TEXT NOT NULL DEFAULT ''  -- JSON 数组：指派的分组 id 列表
```

迁移回填（migrations 内执行，使用 `presetCategorySets`）：

```sql
-- 对 assigned_suggested_preset != '' 的行，按 presetCategorySets[preset] 展开写入新列
```

`store.SubscriptionProfile` 的 `AssignedSuggestedPreset string` 字段**完全替换**为 `AssignedSuggestedCategories string`（JSON 字符串）。`UserSubscriptionProfile` 查询、`SaveUserSubscriptionProfile` 的 INSERT / ON CONFLICT 同步替换。

### 2. 发布生效逻辑

`store.EffectiveProfile`（subscriptions.go:130）：

- `AssignedSuggestedCategories != ""` 且（`AssignForcedPortable` 或用户未自选模板）时：
  `Mode = suggested`，`CategoriesJSON = AssignedSuggestedCategories`（替代原 preset 展开），`Preset` 置占位值 `balanced`（用户自选值不受影响）。

`sub/publisher.go` `resolvePolicy`（publisher.go:168）：

- source label 不再依赖 `strings.Title(profile.Preset)`，统一为「内置建议规则」——指派任意分组时不再误导显示 "Balanced"。
- 逻辑不变：`selected` 从 `CategoriesJSON` 解析，失败时回退 `presetCategories[Preset]`。

### 3. 面板 API（panel）

`template_assignment.go`：

- assign 请求体 `suggested_preset string` → `suggested_categories []string`：
  - 校验：与 `template_id` 二选一（均空/均非空 → 400）；非空；去重；每个 id 必须是合法分组（以 `sub.Categories()` 的 id 集合校验，panel 已 import sub）；按内置顺序排序。
  - 生效：`AssignedPortableTemplateID = ""`、`AssignedSuggestedCategories = categories JSON`、`AssignForcedPortable = forced`（与模板指派互斥，同现状）。
- unassign 请求体同改：`suggested_categories`（非空数组）作为「清除建议规则指派」标记 → 清 `AssignedSuggestedCategories` + `AssignForcedPortable = false`。
- `subscriptionProfileDTO`（users.go:179）：`AssignedSuggestedPreset string` → `AssignedSuggestedCategories []string`（从 JSON 解析）。
- audit 事件字段同步改为 `suggested_categories`。

### 4. OpenAPI 契约

- `docs/openapi.yaml`：assign / unassign 端点请求体与 `subscriptionProfileDTO` schema 中 `suggested_preset` → `suggested_categories`（`type: array`，`items: string`）。
- 重新生成 `src/frontend/src/lib/api-contract.generated.ts`（`bun run generate:api`；`panel/contract_test.go` 强校验 openapi 与路由一致）。

### 5. 前端

- `types.ts`：`SubscriptionRoutingProfile.assigned_suggested_preset: string` → `assigned_suggested_categories: string[]`。
- `subscription-routing.ts`：默认值 `assigned_suggested_categories: []`。
- `api.ts`：`assignSubscriptionTemplate` / `unassignSubscriptionTemplate` 的 target 类型改为 `{ template_id?: string; suggested_categories?: string[] }`。
- `Users.tsx`：把已加载的 `ruleCategories` 传入 `TemplateAssignmentTab`（新增 prop `categories: SubscriptionRuleCategory[]`）。
- `TemplateAssignmentTab.tsx` 指派对话框，参照 miaomiaowu `generator.tsx` 规则模式 + `rule-selector.tsx`：
  - 模式切换：「建议规则 / 使用模板」双按钮。
  - 建议规则 → 预设下拉（自定义 / 极简规则 / 均衡规则 / 完整规则；选预设按 `SubscriptionRuleCategory.in_minimal` / `in_balanced` 标志自动填充对应分组，comprehensive = 全选，可再手动调整）+ 可折叠面板「已选择 N 个类别」+ 分组勾选网格（图标 + 标签，同现有 `SubscriptionRoutingFields` 生效分类样式）。
  - 使用模板 → 现有按类型分组的模板 Select。
  - 指派发送勾选的分组列表；打开对话框默认预填均衡分组（可调整）。
- 已指派区：建议规则区块按分组集合签名（JSON key）分组展示，chips 显示分组图标 + 标签（修复现按 `suggestedUsers[0]` 归组的隐患）；分组标签从 `categories` prop 查表。
- `SubscriptionRoutingFields.tsx`：指派提示文案由 preset 名改为分组标签列表（如「已强制指派建议规则（AI 服务、电报消息、Github）」）。

### 6. 测试

- store：`EffectiveProfile` 分组指派生效 / 未指派保持用户自选；迁移回填（preset → categories）。
- panel：`template_assignment_test.go` — assign（categories 校验：空/非法 id/重复/与 template_id 互斥）、unassign、强制与非强制、DTO 字段；`subscriptions_assignment_test.go` 同步更新。
- 前端：`bun run build`（含 codegen --check）+ tsc；`api-contract.generated.ts` 更新。

### 7. 不采纳项

- miaomiaowu DNS 段不参考（其 DNS 配置有问题），DNS 维持 Lattix 现有实现。
- 仅参考其规则选择交互与分组数据结构（`RULE_CATEGORIES` 与 Lattix `builtInCategories` 一致，本仓库 policy.go 已内建）。
