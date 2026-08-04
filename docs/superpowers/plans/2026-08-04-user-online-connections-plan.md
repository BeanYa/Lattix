# 用户在线连接数展示 — 实现计划

日期：2026-08-04
Spec：`docs/superpowers/specs/2026-08-04-user-online-connections-design.md`（实现时以本计划为准，spec 为设计背景）

## 全局约束（Global Constraints）

- 数据源 = xray `StatsService.GetUsersStats`；用户 email = 用户 UUID。
- `TelemetryPayload` 新增 `OnlineUsers []OnlineUserStat`（json tag `online_users,omitempty`），`OnlineUserStat{User string; IPs []string}`（json tag `user`/`ips`）。
- policy 键列表在 `src/agent/internal/xray/manager.go` `ensureStatsPolicy` 的 `levels["0"]` 追加 `"statsUserOnline"`。
- Agent 上报走现有 `telemetry.report`；`GetUsersStats` Unimplemented/失败 → 静默降级（不上报该字段）。
- Backend 内存 tracker：`serverID → user → IP set`，帧全量覆盖；freshness 固定 120s；`GET /api/user/list` 每用户附 `online_connections int`。
- 前端 `SubUser` 新增 `online_connections: number`，Users 表格新增「在线」列（>0 徽标高亮、0 显示 `-`）。
- 不新增 RPC、不落库、不加依赖；共享端点（shared endpoint）不在本期范围。

## Task 1: Shared 协议扩展

**文件**：`src/shared/messages.go`

在 `TelemetryPayload`（现位于文件末尾附近）新增字段与类型：

```go
// OnlineUserStat 是某服务器当前在线的一个用户（有活跃连接）及其源 IP 列表（去重）。
type OnlineUserStat struct {
	User string   `json:"user"` // 用户 UUID（xray email）
	IPs  []string `json:"ips"`
}
```

`TelemetryPayload` 追加字段：

```go
OnlineUsers []OnlineUserStat `json:"online_users,omitempty"` // 该服务器在线用户全量快照
```

**测试**：`src/shared/messages_test.go` 补 JSON 序列化断言：空 `OnlineUsers` 时 `online_users` 键不出现（omitempty）；非空时结构正确（user/ips 字段名）。

**验证**：`cd src/shared && go build ./... && go test ./...`

## Task 2: Agent xray 层（QueryOnlineUsers + policy）

**文件**：`src/agent/internal/xray/hot.go`、`src/agent/internal/xray/manager.go`、`src/agent/internal/xray/manager_telemetry_test.go`

1. `hot.go` 新增：

```go
// QueryOnlineUsers 拉取全部在线用户及源 IP（StatsService.GetUsersStats，一次 RPC 覆盖全部用户）。
// 老 xray 核心无此 API 时返回 Unimplemented 错误（由调用方降级）。
func (c *HotClient) QueryOnlineUsers() ([]shared.OnlineUserStat, error)
```

内部用 `statscommand.NewStatsServiceClient(conn).GetUsersStats(ctx, &statscommand.GetUsersStatsRequest{})`，遍历 `resp.Users`：`Email` → `User`，`Ips[*].Ip` → `IPs`（nil/空跳过）。复用现有 `callConn`。

2. `manager.go` 的 `ensureStatsPolicy`（约 463 行）：`for _, key := range []string{"statsUserUplink", "statsUserDownlink"}` 追加 `"statsUserOnline"`。

3. `manager.go` `Manager` 增加方法 `QueryOnlineUsers() ([]shared.OnlineUserStat, error)` 转发 `m.hot.QueryOnlineUsers()`（对照现有 `QueryStats` 转发模式）。

4. 测试（`manager_telemetry_test.go`）：扩展现有 `ensureStatsPolicy` 断言循环，包含 `statsUserOnline`；新增加载配置后 `policy.levels["0"].statsUserOnline == true` 的断言。

**验证**：`cd src/agent && go build ./... && go test ./internal/xray/`

## Task 3: Agent telemetry 上报在线用户

**文件**：`src/agent/cmd/agent/telemetry.go`、`src/agent/cmd/agent/telemetry_test.go`

1. `telemetry` 新增方法：

```go
func (t *telemetry) onlineUsers() []shared.OnlineUserStat
```

调 `t.mgr.QueryOnlineUsers()`；错误时（含 Unimplemented）`log.Printf` 调试级（沿用现有风格）并返回 nil；成功返回结果。

2. `collect()` 组装：`OnlineUsers: t.onlineUsers()`。

**测试**：`telemetry_test.go` 按现有 fake manager 模式（若存在）覆盖：成功路径（payload 含 online_users）、失败路径（返回空且不 panic）。

**验证**：`cd src/agent && go build ./... && go test ./cmd/agent/`

## Task 4: Backend OnlineUsersTracker

**文件**：新文件 `src/backend/internal/panel/online_users.go` + `online_users_test.go`

```go
// OnlineUsersTracker 聚合各服务器上报的在线用户快照（telemetry 帧全量覆盖）。
type OnlineUsersTracker struct {
	mu        sync.Mutex
	servers   map[int64]map[string]map[string]struct{} // serverID → user → IP set
	updatedAt map[int64]time.Time
}

// FreshnessWindow 是服务器快照的新鲜度窗口：窗口内无更新的服务器记录不计入。
const FreshnessWindow = 2 * time.Minute

// ApplySnapshot 用某服务器一帧全量快照替换该服务器记录（空快照 = 清除该服务器）。
func (t *OnlineUsersTracker) ApplySnapshot(serverID int64, users []shared.OnlineUserStat, now time.Time)

// ConnectionsByUser 返回某用户跨服务器去重后的在线连接数；超过 FreshnessWindow 的服务器记录不计入。
func (t *OnlineUsersTracker) ConnectionsByUser(userUUID string, now time.Time) int
```

语义：`ApplySnapshot` 空列表时删除该 serverID 条目（等价清空）；`ConnectionsByUser` 遍历各服务器该用户 IP set 并集的大小。

**测试**（`online_users_test.go`）：多服务器快照覆盖（同 user 不同服务器）；跨服务器同 IP 去重只计 1；freshness 过期（updatedAt 超过窗口 → 不计入）；空快照清除；无数据返回 0。

**验证**：`cd src/backend && go build ./... && go test ./internal/panel/`

## Task 5: Backend 集成（dispatcher + user list API）

**文件**：`src/backend/internal/panel/panel.go`、`src/backend/internal/panel/users.go`、`src/backend/internal/dispatch/dispatcher.go` 及对应测试

1. `panel.Server` 持有 `onlineUsers *OnlineUsersTracker`（`NewServer` 初始化 `&OnlineUsersTracker{}`）；提供 getter（如 `OnlineUsers() *OnlineUsersTracker`）。

2. `dispatcher.go handleTelemetry`（约 816 行）：`p.OnlineUsers` 非空或需清空时调用 `tracker.ApplySnapshot(serverID, p.OnlineUsers, time.Now().UTC())`。Dispatcher 需能拿到 tracker：按现有注入模式（检查 Dispatcher 是否已有 panel 引用；若没有，构造函数加参或通过回调注入）。

3. `users.go handleListUsers`：响应 DTO 每用户新增 `OnlineConnections int json:"online_connections"`，值 = `tracker.ConnectionsByUser(u.UUID, time.Now().UTC())`。

4. 测试：
   - `dispatcher` 测试：telemetry 帧含 `online_users` 时 tracker 被应用（按现有 `handleTelemetry` 测试模式）。
   - `users` 测试：`handleListUsers` 响应含 `online_connections`（tracker 无数据 → 0；有数据 → 正确值）。

**验证**：`cd src/backend && go build ./... && go test ./internal/panel/ ./internal/dispatch/`

## Task 6: Frontend 在线列

**文件**：`src/frontend/src/lib/types.ts`、`src/frontend/src/pages/Users.tsx`

1. `SubUser`（`types.ts`）新增 `online_connections: number`。
2. `Users.tsx` 表格新增「在线」列表头；每行单元格：`u.online_connections > 0` 时显示 `text-success` 高亮的数字徽标，否则显示 `-`。列风格对齐现有表格列（如「流量」列）。列表已 5s 轮询，无刷新逻辑改动。

**验证**：`cd src/frontend && bun run build`（类型检查 + 构建通过）。

## 收尾验证

- `cd src/backend && go build ./... && go vet ./... && go test ./...`
- `cd src/agent && go build ./... && go test ./...`
- `cd src/shared && go build ./... && go test ./...`
- `cd src/frontend && bun run build`
