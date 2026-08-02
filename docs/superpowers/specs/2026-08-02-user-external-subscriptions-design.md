# 用户外部订阅合并功能设计（叠加 / 并入 / 附加）

日期：2026-08-02

## 背景与问题

前置：外部订阅导入功能（`2026-08-02-external-subscriptions-design.md`）实现管理员导入第三方订阅 URL，解析节点保存到 `external_chains`，流量信息保存到 `external_subscriptions`。本期将外部订阅**关联到用户**，以「一个外部订阅」为单位引入，提供三种引入方式：叠加 / 并入 / 附加。引入后，用户实际从订阅地址获取到的订阅内容需包含外部订阅节点，`subscription-userinfo` 流量头按模式合并计算。

## 语义矫正（相对原始需求）

原始需求中的「XX引入（减法？）」示例语义为：外部订阅**已用**并入面板配额池，**总额度不参与叠加**（total 不变、used 累加）。「减法」命名有歧义（易误读为从面板额度中扣除外部额度），正式命名为**并入（merge）**：

| 模式 | 代码值 | total | used（up/down） | 剩余 | expire |
|------|--------|-------|-----------------|------|--------|
| 叠加 | `stack` | 面板 + Σ叠加订阅 | 面板 + Σ叠加订阅 | Σ各池剩余 | min(面板, Σ叠加订阅) |
| 并入 | `merge` | 面板（不变） | 面板 + Σ并入订阅已用 | 面板剩余 − Σ并入已用（UI 0 封底） | 面板自身 |
| 附加 | `nodes` | 面板（不变） | 面板（不变） | 面板 | 面板自身 |

例：面板 500G/300G + 订阅 X 200G/100G → 叠加：total 700G、used 400G；并入：total 500G、used 400G、剩余 100G。

## 设计决策

- **独立关联表 `user_external_subscriptions`**：用户与外部订阅的多对多关系 + 模式；不触碰 `users` / `chains` / `external_subscriptions` / `traffic` 现有结构与状态机。
- **不物化合并统计**：合并值在读取时实时计算（`external_subscriptions` 的流量是同步快照，同步后自动反映到所有关联用户的合并视图，无需缓存表）。
- **数据隔离**：用户面板已用（`traffic` 表）与外部订阅已用/可用（`external_subscriptions` 表）分别保存，互不影响；仅展示层合并。
- **未知额度**：外部订阅 `total = 0`（未提供 `subscription-userinfo`）视为「额度未知」，不参与叠加/并入计算，仅引入节点；统计表中显示「未知」。
- **已用数值时间基准**：面板已用为实时测量，外部已用为最近一次同步快照，合并值非同一时点，文档明示。
- **配额仅展示**：现状后端无配额强制逻辑（`traffic` 表注释「仅统计」），合并值仅用于展示，不做强制。
- **订阅头报真实值**：`subscription-userinfo` 头不封底（used 可大于 total）；仅前端 UI 剩余显示 0 封底。
- **`reset_day` 不合并**：保持面板自身的重置日（外部订阅重置节奏与面板无关）。

## 数据模型

`external_subscriptions` 与 `external_chains` 由外部订阅会话定义（见其设计文档），本期新增：

```sql
CREATE TABLE IF NOT EXISTS user_external_subscriptions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'stack',  -- stack | merge | nodes
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, subscription_id)
);
```

级联删除：删除外部订阅或删除用户时自动清理关联。

## 合并算法

新增 `extsub.MergeUserTraffic`（纯函数，`src/backend/internal/extsub/merge.go`）：

```
total = 面板 total + Σ(模式=stack 且 total>0 的 total)
up    = 面板 up + Σ(模式∈{stack,merge} 且 total>0 的 up)
down  = 面板 down + Σ(模式∈{stack,merge} 且 total>0 的 down)
expire = 模式=stack 且 total>0 的部分取 min(面板, 各订阅)；merge/nodes 取面板
```

- 多订阅混合：并入先并入面板池，叠加再叠加独立池，与配置顺序无关。
- 剩余（展示用）= max(0, total − up − down)；头不封底。
- 附加订阅的流量在统计表中单独展示（自己的已用/可用），不进入合并。

## 订阅渲染（`src/backend/internal/sub`）

现状：订阅正文是预生成快照（`PublishUser` → `compileNodes` → 各格式渲染器），`subscription-userinfo` 头在请求时实时读取 `traffic` 表发出。

**方案**：扩展 `proxyItem` 增加 `external *extsub.Node` 字段，`compileNodes` / `renderLinks` 分支到新构建器，复用整条快照管线（策略/分组/规则逻辑零改动）。不建独立渲染管线。

改动：

- `proxyItem` 增加 `external *extsub.Node`（非空 = 外部订阅节点，凭据取自 config，不派生用户 UUID）。
- 新构建器（支持 12 种协议：vless/vmess/ss/ssr/trojan/hysteria2/tuic/wireguard/anytls/snell/socks/http）：
  - `buildExternalClash`（mihomo YAML）
  - `buildExternalSingbox`（sing-box JSON）
  - `buildExternalQuanX`（Quantumult X）
  - `buildExternalLink`（base64 分享链接序列化，与解析器互逆）
  - 现有 `clashProxy` / `sbOutbound` 结构补充 omitempty 可选字段（hy2 的 ports/obfs、wireguard 的 public-key 等）；客户端格式无法表达的协议跳过并记 warning。
- `subscriptionItems`：遍历用户关联的外部订阅（`ListUserExternalSubscriptions`），取其节点（`ListExternalChains`）转 `proxyItem{external: …}` 追加；用户过期/禁用时早退逻辑已覆盖（不出外部节点）。
- `setSubHeaders`（`sub.go:67`）：upload/download/total/expire 改用 `MergeUserTraffic`（面板实时流量 + 关联订阅）；`reset_day` 不变。
- 公开接口 `sub/api.go` 的 `HandleSubInfo`（`/api/sub/{token}/info`）同步合并。

## 快照重发布联动

- 用户关联变更（`set-external-subscriptions` / 创建用户）→ 重发布该用户快照。
- **外部订阅同步完成后 → 重发布所有关联用户**：`store.UsersByExternalSubscriptionID(subID)` 取关联用户，走 `sub.Server.EnqueueUsers` 异步重生成队列（与 nodes/chains 现有模式一致）。
- 落点（外部订阅导入会话已合入 main，集成点已闭合）：
  - `handleSyncExternalSubscription`（`panel/external_subscriptions.go:105`）同步成功后调用；
  - 定时任务 `external_subscriptions.sync`（`panel.go:95`）中执行完 `SyncDue` 后调用——`extsub.Service.SyncDue` 返回值扩展为 `([]int64, error)`（本次实际同步过的订阅 ID，唯一调用方即该定时任务；extsub 包不可 import sub，由 panel 编排）。

## API 与前端

- `POST /api/user/set-external-subscriptions`：请求 `{user_id, items: [{subscription_id, mode}]}`，整表替换（沿用 `set-nodes` 模式），校验用户存在、订阅存在、mode 合法；成功后重发布该用户。响应 `{status:"ok"}`。
- `POST /api/user/create` 请求体支持可选 `external_subscriptions: [{subscription_id, mode}]`。
- `userDTO` 增加：
  - `external_subscriptions: [{subscription_id, name, mode, upload, download, total, expire, remaining, node_count}]`（joined，remaining 0 封底，total=0 时 remaining 为 null 表示未知）
  - `merged_traffic: {upload, download, total, expire}`（合并结果，供页面统计展示）
- 前端 `Users.tsx` 分配弹窗新增「外部订阅」区：可用外部订阅勾选 + 每项模式选择（叠加/并入/附加）+ 小统计表（名称/模式/额度/已用/剩余/到期 + 合并汇总行）。创建用户弹窗同样支持。

## Store 新方法（`src/backend/internal/store/user_external_subscriptions.go`）

```go
type UserExternalSubscription struct {
    ID             int64
    UserID         int64
    SubscriptionID int64
    Mode           string // stack | merge | nodes
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
// ListUserExternalSubscriptions 返回用户关联订阅（含外部订阅统计字段的 join 结果）。
func (s *Store) ListUserExternalSubscriptions(ctx, userID) ([]UserExternalSubscriptionJoined, error)
func (s *Store) SetUserExternalSubscriptions(ctx, userID, items []UserExternalSubscription) error // 事务整表替换
func (s *Store) UsersByExternalSubscriptionID(ctx, subID) ([]int64, error)
```

## 测试

- `merge_test.go`：表驱动——三模式、混合模式、total=0、expire 规则、0 封底。
- store 测试：关联 CRUD、整表替换、删除订阅级联清关联、`UsersByExternalSubscriptionID`。
- sub 测试：httptest 验证合并后的 `subscription-userinfo` 头；外部节点进入各格式快照；过期用户不出外部节点。
- link 序列化测试：各协议 config → 分享链接 → 重新解析一致性。
- panel handler 测试：`set-external-subscriptions`（跟随 `server_tests_test.go` 风格）。
- `docs/openapi.yaml` 补充路径与 DTO 字段（`npm run build` 的 codegen `--check` 强校验）。

## 改动清单

| 文件 | 改动 |
|------|------|
| `src/backend/internal/store/store.go` | Schema 追加 `user_external_subscriptions` 表 |
| `src/backend/internal/store/user_external_subscriptions.go` + `_test.go` | 新 |
| `src/backend/internal/extsub/merge.go` + `merge_test.go` | 新，合并纯函数 |
| `src/backend/internal/sub/sub.go` | `proxyItem.external`、`setSubHeaders` 合并、`subscriptionItems` 追加外部条目 |
| `src/backend/internal/sub/publisher.go` | `compileNodes` 分支、`RepublishUsersWithExternalSubscription` |
| `src/backend/internal/sub/external*.go` | 新，四种格式的外部节点构建器 |
| `src/backend/internal/panel/users.go` | `handleSetUserExternalSubscriptions`、`toUserDTO` 扩展、create 支持 |
| `src/frontend/src/pages/Users.tsx` | 分配弹窗外部订阅区 + 统计表 |
| `src/frontend/src/lib/api.ts` / `lib/types.ts` | API 方法与类型 |
| `docs/openapi.yaml` | 路径与 DTO 字段 |

## 非目标（后续版本）

- 外部节点测速
- 外部订阅流量历史统计/归档
- 按协议/地区筛选外部节点
- 配额强制（现状无强制，维持仅展示）
