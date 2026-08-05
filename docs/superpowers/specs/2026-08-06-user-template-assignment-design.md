# 用户模板指派设计

日期：2026-08-06

## 背景

当前用户订阅模板只有两个来源：用户自选（创建/订阅设置对话框，`user_subscription_profiles` 的 `mode/preset/categories/portable_template_id/mihomo_template_id/singbox_template_id/quanx_template_id`）与默认建议规则。成规模的部署中，管理员需要在多个用户之间批量统一模板（如全部换用某个 ACL4SSR 内置模板），逐用户打开订阅设置修改非常繁琐，且无法区分"管理员指派"与"用户自选"。

本功能在用户页新增「模板指派」，支持将某个模板（自定义或内置）批量指派给多个用户：

- 指派模板按 kind 写入对应槽位（portable/acl4ssr → 主策略槽位；mihomo/singbox/quanx → 对应原生覆盖槽位），其余槽位保持不变。
- 默认用户自选优先：用户自己在订阅设置中选过模板则用户自选生效，未自选则跟随指派。
- 提供**强制覆盖**：指派时勾选强制覆盖后，该槽位的用户自选失效（订阅设置中显示「跟随指派」且不可改），但自选值仍保留在库中，解除强制/取消指派后自动恢复。
- 指派/取消指派完成后即时重新发布受影响用户的订阅快照。
- 交互复用现有「分配链路」的弹窗流程，仅对象变为多选用户。

## 目标

- 用户页新增「模板指派」独立 Tab：按模板分组显示使用它的用户，多选用户 + 选模板（内置或自定义）+ 强制覆盖的批量指派。
- 保留现有「订阅设置」对话框的用户自选模板功能；指派与自选按上文优先级共存。
- 指派完成后更新用户订阅缓存（重新发布快照）。
- 提供取消指派（含解除强制覆盖）操作，可逆。

## 非目标

- 不修改现有「分配链路」「订阅设置」流程；未指派用户行为与现状完全一致。
- 不做模板页级（全局默认模板）指派。
- 不区分指派操作的来源用户（不做审计人细化，沿用现有 audit 事件机制记录操作与 user_ids）。
- 不做单槽位内多个指派的队列/历史（一个槽位同一时刻只有一个指派模板）。

## 方案

### 数据模型（schemaVersion 12 → 13）

`user_subscription_profiles` 按 kind 对应槽位各新增两列（指派模板 + 强制标记），沿用 `ensureColumns` 迁移：

```sql
assigned_portable_template_id TEXT NOT NULL DEFAULT '',
assign_forced_portable         INTEGER NOT NULL DEFAULT 0,
assigned_mihomo_template_id   TEXT NOT NULL DEFAULT '',
assign_forced_mihomo           INTEGER NOT NULL DEFAULT 0,
assigned_singbox_template_id  TEXT NOT NULL DEFAULT '',
assign_forced_singbox          INTEGER NOT NULL DEFAULT 0,
assigned_quanx_template_id    TEXT NOT NULL DEFAULT '',
assign_forced_quanx            INTEGER NOT NULL DEFAULT 0
```

同步更新 `Schema` DDL 与 `store.SubscriptionProfile` 结构体、`UserSubscriptionProfile` 查询、`SaveUserSubscriptionProfile` 的 INSERT/ON CONFLICT 语句。

`DeleteSubscriptionTemplate` 的引用检查（`user_subscription_profiles WHERE portable_template_id=? OR ...`）扩展覆盖 4 个 assigned 列。

### 生效规则（纯函数，可单测）

`store.EffectiveProfile(profile SubscriptionProfile) SubscriptionProfile`：输入原始 profile（含 assigned 与 forced 字段），输出订阅构建实际使用的 profile。

- **主策略槽位**：`assigned_portable_template_id != ""` 且（`assign_forced_portable` 或 用户未自选模板，即 `profile.Mode != SubscriptionModeTemplate` 或自选 portable 为空）→ 生效值 = `Mode: template, PortableTemplateID: assigned`；否则保留用户自选（`Mode/Preset/CategoriesJSON/PortableTemplateID` 原样）。
- **原生槽位**（mihomo/singbox/quanx）：`assigned_<kind>_template_id != ""` 且（`assign_forced_<kind>` 或 用户该槽位为空）→ 生效值 = assigned；否则保留用户自选。
- 未指派槽位 → 现有行为不变（空 = 跟随主策略/默认）。

`sub/publisher.go`：`PublishUser` 在取到 `UserSubscriptionProfile` 后先经 `EffectiveProfile`，`resolvePolicy` 与 native 覆盖循环均使用生效值，其余逻辑不变。

### 后端 API（panel）

- `POST /api/subscription/template/assign`
  - 请求：`{"user_ids": [int], "template_id": "string", "forced": bool}`
  - 校验：user_ids 非空且全部存在；模板存在、`content` 缓存非空、kind ∈ {portable, acl4ssr, mihomo, singbox, quanx}（映射到对应槽位，acl4ssr 归主策略槽位）。
  - 行为：按 kind 写对应 `assigned_*` 槽位 + `assign_forced_*` 标记（逐用户）；对每个用户 `s.subscriptions.PublishUser(ctx, id, s.panelBase(r))`；审计 `subscription.template.assigned`（template_id、user_ids、forced）。
- `POST /api/subscription/template/unassign`
  - 请求：`{"user_ids": [int], "template_id": "string"}`
  - 行为：按 kind 清除对应 `assigned_*` 与 `assign_forced_*`（逐用户）；对每个用户 `PublishUser`；审计 `subscription.template.unassigned`。用户自选值不触碰。
- 用户 DTO（`subscriptionProfileDTO`）扩展：`assigned_portable_template_id / assign_forced_portable` 等 8 个字段（前端据此渲染「跟随指派」与禁用态）。`subscriptionProfileInput` 不接收 assigned 字段（指派只能走专用端点）。

路由注册于 `panel.go` 的 subscription 组，权限 write。

### 前端

Users 页改为两个 Tab：「用户」|「模板指派」。

- 模板指派 Tab：
  - 顶部指派区：多选用户（checkbox 列表，含搜索/全选可选）→「指派模板」按钮打开弹窗：模板选择（按 kind 分组展示：主策略 portable/acl4ssr、Mihomo、Sing-box、Quantumult X）、「强制覆盖用户自选」勾选框 → 保存 → 批量指派 + 重新加载。
  - 下方按模板分组列表：每个被使用的模板一组，列出使用它的用户（强制覆盖用户带徽章），每组每个用户提供移除操作（= unassign 单个用户该模板）；另有「未指派」分组列出无任何指派槽位的用户。
  - 空态：无模板或暂无指派时的引导。
- 订阅设置对话框（保留自选功能）：
  - 槽位已指派（未强制）时：显示提示「已指派模板「X」，可在此自选覆盖」；字段照常可编辑，保存后自选生效（自选优先）。
  - 槽位已指派且强制时：对应字段禁用，显示「已强制指派「X」」；native 覆盖槽位按各自强制状态独立处理。
- api client 新增 `assignTemplate(userIds, templateId, forced)` 与 `unassignTemplate(userIds, templateId)`。

### 测试

- store：`EffectiveProfile` 优先级矩阵（未指派/指派自选优先/强制覆盖/原生槽位各自独立）；迁移后列存在（沿用现有迁移测试模式）；`DeleteSubscriptionTemplate` 引用检查覆盖 assigned 列。
- sub：`resolvePolicy` 使用生效 profile（指派 portable → 走模板模式；强制覆盖忽略用户自选）；native 覆盖使用生效 mihomo/singbox/quanx。
- panel：assign 校验（kind→槽位映射、模板缓存为空、用户不存在、user_ids 空）、批量 `PublishUser` 调用、unassign 清除与自选保留。
