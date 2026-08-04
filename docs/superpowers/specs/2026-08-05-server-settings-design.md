# 服务器设置（ServerSettings）默认 + 逐服务器覆盖设计

日期：2026-08-05
状态：已评审

## 目标

- 面板设置页新增「服务器设置」区块（defaultsetting），首期字段：xray 版本。
- 每台服务器可单独覆盖（customsetting），服务器设置优先；未覆盖的字段继承面板默认。
- 合并后的生效设置经平行 sync 通道下发给 agent，agent 应用（xray 版本自动对齐）。
- 数据模型与同步机制均照抄现有 AgentSettings 模式，保持面板/agent 状态机模式连贯。

## 数据模型（shared）

```go
// ServerSettings 是面板默认 + 服务器覆盖共用的数据模型（字段级覆盖，可扩展）。
type ServerSettings struct {
    XrayVersion *string `json:"xray_version,omitempty"` // nil=未设置; "latest"; "vX.Y.Z"
}

// ServerSettingsDocument 是面板下发的生效文档（agent 落盘）。
type ServerSettingsDocument struct {
    SchemaVersion int            `json:"schema_version"`
    Server        ServerSettings `json:"server"`
}
```

协议（照抄 AgentSettings 全套）：
- `TypeServerSettingsSync`（agent 拉取/回执请求）
- `TypeServerSettingsChanged`（面板通知事件）
- `ServerSettingsSyncPayload`（PanelInstanceID, AppliedRevision, LastApplyError）
- `ServerSettingsSyncResult`（Changed, Settings *ServerSettingsDocument）
- `ServerSettingsChangedPayload`（Revision）

校验：`xray_version` 合法值 = 空/nil、`latest`、或 `vX.Y.Z`（正则 `v\d+\.\d+\.\d+`）。

## 存储（store）

- 面板默认：`settings` 表新键 `SettingServerSettings = "server_settings"`，
  JSON `{"revision": N, "xray_version": "latest"}`。默认值 `revision=1, xray_version="latest"`
  （保持现状行为）。方法 `DefaultServerSettings(ctx)`（读 + 首建默认）与
  `UpdateDefaultServerSettings(ctx, desired)`（校验 + revision+1，事务）。
- 服务器覆盖：`servers` 表新列 `custom_settings TEXT NOT NULL DEFAULT ''`，
  内容为 `ServerSettings` 的部分 JSON（仅覆盖的字段），JSON 内带 `revision` 字段
  （如 `{"revision": 2, "xray_version": "v1.8.24"}`），避免再加列。
  方法 `ServerCustomSettings(ctx, id)`、`UpdateServerCustomSettings(ctx, id, settings)`。
- 服务器状态三列（照抄 `agent_settings_*`）：`server_settings_revision INTEGER NOT NULL DEFAULT 0`、
  `server_settings_error TEXT NOT NULL DEFAULT ''`、`server_settings_reported_at DATETIME`。
  方法 `ReportServerSettings(ctx, id, revision, applyError)`。
- 合并：`EffectiveServerSettings(ctx, id)` 返回 `(settings, revision)`；
  **effective revision = default.revision + custom.revision**（单调递增，杜绝回退碰撞）；
  字段级合并 = custom 中 JSON 存在的字段覆盖 default（`*string` 指针非 nil 即覆盖）。
- 迁移：`schemaVersion` 10 → 11，`migrateSchema` 中 `ensureColumns` 增加 4 列。

## 面板 API（panel）

- `GET /api/settings`：settingsDTO 增加 `server_settings` 段（默认值 + revision）。
- `PUT /api/settings`：校验 xray_version（latest 或 vX.Y.Z），保存 default + revision+1，
  成功后 `NotifyServerSettingsChanged` 通知**所有**在线服务器；audit 记录变更。
- `GET /api/servers`：serverDTO 增加 `custom_settings`（该服务器覆盖，nil=无）与
  `effective_xray_version`（合并结果）。
- `PATCH /api/servers/{id}`：接受 `custom_settings`（null/空 = 清除覆盖回退默认），
  保存后仅通知**该**服务器；audit 记录。
- 升级对话框保持现状（手动触发优先），目标版本默认预选 effective 版本。

## Dispatcher

- `handleServerSettingsSync(serverID, env)`（照抄 `handleAgentSettingsSync`）：
  解析 → 截断 LastApplyError 至 512 → `ReportServerSettings` →
  算该服务器 effective（`EffectiveServerSettings`）→ 比对
  `payload.AppliedRevision != effectiveRevision || payload.PanelInstanceID != panelID || LastApplyError != ""`
  → 变化则回 `ServerSettingsDocument`。
- `NotifyServerSettingsChanged(ctx, serverID)`：serverID=0 时通知全部在线服务器，
  否则仅该服务器；best-effort，agent 兜底在 session.open 后与周期拉取。

## Agent 侧

- `main.go` 消息分发：`TypeServerSettingsChanged` → `sendServerSettingsSync`；
  `TypeServerSettingsSync` 响应 → 校验 → `state.SaveSettings` → 应用 → 回执确认。
  落盘文件与 AgentSettings 分开（新 state 方法 `SaveServerSettings/LoadServerSettings`）。
- 新增 `serverRuntimeSettings`（照抄 `runtimeSettings`：持有/应用/变更通知/重置）。
- **xray 版本对齐**（核心新逻辑）：
  - effective `xray_version` 为 nil 或 `"latest"` → 不动作；
  - 固定版本与当前 xray 版本不一致 → 触发 `mgr.UpgradeXray(version)`；一致则跳过；
  - 防抖：仅 sync revision 变化时触发一次；失败记 lastApplyError 回执（面板显示 failed），
    不立即无限重试，下次 revision 变化或 agent 重启后再试；升级进行中不重复触发。

## 边界处理

- 非法版本号：面板保存时拒绝；agent 收到也校验拒绝。
- 指定版本不存在（GitHub 404）：agent 升级失败 → 面板状态 failed + 错误透出。
- 手动升级与自动对齐共存：手动升级后与期望不符会被再次对齐（钉版本语义成立）；
  解除需清除覆盖或改面板默认。
- 旧 agent 兼容：未知消息类型走现有 `recv unknown type` 兜底回复
  `CodeUnsupportedAction`，面板状态显示 pending，不崩溃。
- 离线服务器：通知 best-effort，重连后 session.open → sync 补齐（命令队列不动）。

## 测试

- store：default/custom 读写、字段级合并、effective revision 单调性、清除覆盖回退、
  `ReportServerSettings` 状态落库。
- dispatcher：sync 有变化/无变化/无效 payload、通知范围（全部 vs 单服务器）。
- agent：对齐触发条件（一致跳过/不一致触发/latest 不动作）、失败不重试、升级中不重复触发。
- 面板 API：PUT 校验、PATCH 覆盖/清除、DTO 字段。

## 改动文件

- `src/shared/agent_settings.go`（或新文件 `server_settings.go`）+ `messages.go`
- `store/store.go`（Schema + schemaVersion）+ `migrations.go` + `settings.go` + `servers.go`
- `dispatch/dispatcher.go`
- `panel/settings.go` + `panel/servers.go`
- `agent/cmd/agent/main.go` + `runtime_settings.go`（或新文件）+ `internal/state/state.go`
- `frontend/src/pages/Settings.tsx` + `Servers.tsx`
