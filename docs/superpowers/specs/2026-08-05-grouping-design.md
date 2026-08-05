# 分组（链路分组 + 用户分组）设计

日期：2026-08-05

## 背景

当前用户与链路的关系只有两种直接绑定：`user_chain_assignments`（用户 ↔ 链路，含共享入口 access_uuid）与 `user_nodes`（用户 ↔ 单机节点），外部订阅经 `user_external_subscriptions` 逐用户分配。成规模的部署中，管理员需要在多个用户之间重复维护相同链路集合，且外部订阅（一个订阅可能包含数十个节点）逐用户勾选非常繁琐。

本功能引入两级分组：

- **链路分组**：将链路（仅共享入口链路，规则与现状一致）与外部订阅编排进命名分组；外部订阅整体原子参与（如外部订阅 A 含 50 个节点，加入分组即全部加入，移除即全部移除）。
- **用户分组**：将用户编组，并为用户分组关联若干链路分组；组内用户不再直接分配链路，其订阅内容由分组派生。

## 目标

- 新增分组页（前端 `/groups`），内含「链路分组」与「用户分组」两个 tab。
- 链路分组管理：命名、勾选链路（复用现有「分配链路」交互）、勾选外部订阅（每项可选 叠加/并入/仅节点，默认 叠加）。
- 用户分组管理：命名、勾选用户、勾选关联的链路分组。
- 分组或其相关服务器更新（链路发布/删除、服务器资料变更、外部订阅同步/删除）时，自动触发受影响用户的订阅重新发布。
- 分组派生链路的客户端凭据稳定：分组内容调整不改变组内用户的访问 UUID。

## 非目标

- 不修改现有的「直接分配」流程；非分组用户行为与现状完全一致。
- 不支持链路分组内直接放单机节点（与现状一致：分配对象只有共享入口链路 + 外部订阅）。
- 不做分组嵌套、分组权限、分组流量统计聚合等扩展。
- 不迁移现有直接分配数据到分组（移入分组即遮蔽，移出即恢复）。

## 方案

读时解析（virtual resolution）：分组关系只存关联行，订阅构建、共享入口 reconcile、受影响用户查询在读取时展开分组。不做写时物化展开，避免双写不一致与凭据漂移。

### 数据模型（schemaVersion 11 → 12）

```sql
CREATE TABLE IF NOT EXISTS link_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS link_group_chains (
    group_id   INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    chain_id   INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (group_id, chain_id)
);
CREATE TABLE IF NOT EXISTS link_group_external_subscriptions (
    group_id        INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'stack',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (group_id, subscription_id)
);
CREATE TABLE IF NOT EXISTS user_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS user_group_members (
    user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_group_id, user_id)
);
CREATE TABLE IF NOT EXISTS user_group_links (
    user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    link_group_id INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_group_id, link_group_id)
);
```

### 解析规则

- 用户 **在 ≥1 个用户分组**（`user_group_members`）：有效链路 = 其全部用户分组经 `user_group_links` 引用的链路分组并集去重；**直接分配被遮蔽**——`user_chain_assignments`、`user_nodes`、`user_external_subscriptions` 的行保留但读取时忽略；移出全部分组后直接分配恢复生效。
- 用户不在任何用户分组：行为与现状完全一致。
- 链路分组只接受 **endpoint_id ≠ 0** 的链路（复用 `Store.ValidateAssignableChains` 规则）；编辑链路分组时校验，运行期某链路失去共享入口则按现状规则优雅降级（订阅中剔除 + warning）。
- 外部订阅原子性：`link_group_external_subscriptions` 只存 `(group_id, subscription_id, mode)`；节点展开发生在订阅构建时（`ListExternalChains`），故 50 个节点天然同进同出。
- 分组派生链路（含共享入口）的客户端凭据：**确定性 UUIDv5**（固定命名空间 + `用户UUID:chain_id`），不落库、跨分组调整稳定。

### 后端 Store 新增（internal/store/groups.go）

CRUD：

- `ListLinkGroups(ctx)` → 全量链路分组（含 chain_ids、外部订阅+mode、关联 user_group 名列表、链路/外部订阅计数）
- `CreateLinkGroup(ctx, name, chainIDs, extSubs)` / `UpdateLinkGroup(ctx, id, name, chainIDs, extSubs)`（整体替换成员，tx）/ `DeleteLinkGroup(ctx, id)`（级联删除）
- `ListUserGroups(ctx)` → 全量用户分组（含 member user_ids、link_group_ids、用户/链路分组计数）
- `CreateUserGroup(ctx, name, userIDs, linkGroupIDs)` / `UpdateUserGroup(ctx, id, name, userIDs, linkGroupIDs)`（整体替换，tx）/ `DeleteUserGroup(ctx, id)`（级联删除）

解析查询：

- `EffectiveUserChainAssignments(ctx, userID)` → `[]UserChainAssignment`：分组用户返回分组派生链路（AccessUUID 用 UUIDv5 计算填充），非分组用户返回直接行。两者都经 `chains.deleted_at IS NULL` 过滤。
- `EffectiveUserExternalSubscriptions(ctx, userID)` → 直接行或分组派生行（mode 取分组行；分组用户忽略直接行）。
- `UserGroupIDsForUser(ctx, userID)` → 用户所属用户分组；`UserInAnyGroup(ctx, userID) bool`。

受影响用户查询（供触发重发布）：

- `SubscriptionUserIDsForChain/Endpoint/Server`（扩展）：直接分支 UNION 分组可达分支——`user_group_members` → `user_group_links` → `link_group_chains`。
- `SubscriptionUserIDsForLinkGroup(ctx, linkGroupID)`：经 `user_group_links` 引用该链路分组的用户分组全体成员。
- `UsersForUserGroup(ctx, userGroupID)`：全体成员。
- `republishExternalSubUsers`（扩展）：直接分支 UNION 分组分支——`link_group_external_subscriptions` → `user_group_links` → `user_group_members`。

### 订阅构建修改（internal/sub）

- `subscriptionItems`（sub/sub.go:617）：`UserNodeIDs` / `UserChainAssignments` / `ListUserExternalSubscriptions` 三处读取改为对应的 Effective 版本（`EffectiveUserChainAssignments`、`EffectiveUserExternalSubscriptions`；`allowed` 集合在分组用户下为空）。`ListChains` 的分配判定（`assignmentByChain`）以 Effective 结果为准，分组派生行的 `EndpointID`/`AccessUUID` 与直接行同构，`chainSubscriptionItem` 无需改动。
- userinfo 头部合并处（发布快照数据源，需在实现时定位 `ListUserExternalSubscriptions` 的其余调用点）改为 Effective 版本，保证 `subscription-userinfo` 的配额合并与订阅节点一致。

### 共享入口 reconcile（internal/dispatch、internal/store）

- `Store.ActiveEndpointAssignments(endpointID)`（store/endpoints.go:202）扩展：直接行 UNION 分组派生行（仅 `u.expired=0 AND u.disabled=0`；分组用户的行在 Go 侧补 UUIDv5），保持单查询性能目标不劣化过多（两条查询 + Go 合并）。
- `dispatch/endpoint.go` 使用方（`ReconcileSharedEndpoint` 全量 reconcile）无需改动，数据源扩展后分组用户的客户端凭据自动在共享入口生效。
- 分组增删改时对涉及端点调用 `ReconcileSharedEndpoint`（幂等全量下发，与现有 `reconcileAssignmentEndpoints` 同模式）。

### 分组变更触发

| 事件 | 动作 |
|---|---|
| 链路分组创建/更新/删除（链路或外部订阅成员变化） | 受影响用户（`SubscriptionUserIDsForLinkGroup`）→ `EnqueueUsers(ids, "")`；端点 reconcile：`reconcileAssignmentEndpoints`（受变更链路所属端点，若无法精确定位则对全部分组链路端点 reconcile） |
| 用户分组创建/更新/删除 | 受影响用户（全体成员）→ `EnqueueUsers`；对该用户分组关联的全部链路分组的链路端点 reconcile |
| 链路发布（`OnChainPublished`）/ 链路删除 | 已扩展的 `SubscriptionUserIDsForChain` 自动含分组用户 |
| 服务器资料变更（`EnqueueUsersForServer`） | 已扩展的 `SubscriptionUserIDsForServer` 自动含分组用户 |
| 外部订阅手动/定时同步（`republishExternalSubUsers`）、外部订阅删除 | 已扩展的分组可达用户自动重发布 |

- 分组变更（链路分组/用户分组增删改）统一走 `EnqueueUsers(ids, "")`（异步去抖，与外部订阅同步后重发布一致）。

### 面板 API（internal/panel/groups.go）

```
GET  /api/link-group/list
POST /api/link-group/create   body: { name, chain_ids: [], external_subscriptions: [{ subscription_id, mode }] }
POST /api/link-group/update   body: { id, name?, chain_ids: [], external_subscriptions: [...] }
POST /api/link-group/delete   body: { id }
GET  /api/user-group/list
POST /api/user-group/create   body: { name, user_ids: [], link_group_ids: [] }
POST /api/user-group/update   body: { id, name?, user_ids: [], link_group_ids: [] }
POST /api/user-group/delete   body: { id }
```

- 校验：name 非空且唯一；链路必须存在且 endpoint_id ≠ 0；外部订阅/用户/链路分组必须存在；去重。
- 写成功后在响应前完成触发动作；发布失败按现状模式返回错误（调用方看到"重新生成订阅失败"），数据已提交不回滚。
- 审计：`link_group.create/update/delete`、`user_group.create/update/delete`。
- openapi.yaml 补充 8 个端点定义，前端 `npm run generate:api` 后新增 `api.*` 方法与类型。

### 前端

- 新页面 `src/frontend/src/pages/Groups.tsx`；`App.tsx` 注册 `/groups`；`Layout.tsx` 导航「分组」（资源管理段，外部订阅之后，lucide `Layers` 图标）。
- 页面结构（复用 Costs 双 tab 模式，受控 Tabs）：
  - **链路分组 tab**：分组卡片列表（名称、链路数、外部订阅数、被哪些用户分组引用）；新建/编辑对话框复用 Users.tsx「分配链路」交互（`buildLinkOptions` 链路勾选 + 外部订阅勾选带 `ExternalModeSelect`）；删除需确认。
  - **用户分组 tab**：分组卡片列表（名称、成员数、关联链路分组数）；新建/编辑对话框：用户勾选列表 + 链路分组勾选列表；删除需确认。
- Users.tsx：用户卡片与「分配链路」对话框显示提示「该用户位于用户分组中，直接分配不生效」（`UserGroupIDsForUser` 信息经 `/api/user/list` 响应附带 `user_group_ids` 字段）。
- 5 秒轮询刷新（与现有页面一致）。

### 迁移

`schemaVersion` 11 → 12；`Schema` 常量追加 6 张表 DDL；无数据迁移。

## 测试

- store 单测（store/groups_test.go）：
  - 解析规则：分组用户遮蔽直接分配、多用户分组并集去重、非分组用户不受影响、UUIDv5 稳定且唯一（同用户不同链路不同值）。
  - 受影响用户查询：链路/端点/服务器/外部订阅的分组可达分支。
- 订阅构建测试：分组用户订阅含分组链路与外部订阅全部节点；移除外部订阅后 50 节点消失（原子性）；分组用户不出现直接分配的链路。
- 面板 handler 测试：校验失败路径（重名、链路无共享入口、不存在的成员）、审计写入。
- e2e 脚本（scripts/e2e/groups.sh 或扩展现有脚本）：建链路分组（含外部订阅）→ 建用户分组并关联 → 组用户订阅包含组内链路与外部订阅节点 → 从链路分组移除外部订阅 → 订阅刷新后节点消失 → 链路重新发布/服务器别名变更 → 组用户订阅自动更新。
