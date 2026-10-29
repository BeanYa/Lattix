# 用户、分配与分组

> 拆自[框架设计索引](../framework-design.md)，对应原 §8（用户、链路与凭证）、§16（逐链路用户分配）、§22（链路分组与用户分组）。

## 用户、链路与凭证

- `users` 只表示真实业务用户、订阅 token、有效期和配额，不创建“虚拟业务用户”。
- `user_chain_assignments` 直接表达用户可用的链。一个用户可同时使用多条链，一条链可供多个用户使用。
- 每个 assignment 生成并稳定保留一个 `access_uuid`。同一用户的多条链即使共享同一入口 `IP:port`，
  Xray 仍可按 email identity 分流并统计到具体用户和链。
- 用户停权、恢复、删除或修改链分配时，Panel 对受影响 Endpoint 发送完整 reconcile。路由规则按 chain
  聚合，而不是每个 assignment 一条规则。
- `user_nodes`、`user.add`、`user.remove` 只保留给历史/独立节点兼容，不承载新链路授权。

## 逐链路用户分配（底层仍为 user_nodes）

替代全对全隐含关系：`user_nodes` 关联表，**默认全关**——新建用户/节点无任何关联，
管理员在用户列表“分配链路”对话框勾选（`POST /api/user/set-nodes` 整体替换）。
创建用户时可直接预选链路对应的业务 `node_id`，省略则维持默认全关。

- **扇出语义**：node.apply 只携带分配到该节点的用户 UUID；分配变更按差量扇出
  user.add/user.remove（载荷仅含受影响节点）；删除用户仍向所有服务器幂等扇出 user.remove。
  （引入链后 `user_nodes` 指向链的**出口**节点——UUID 只存在于出口 xray，入口/中间跳不接收用户扇出，见[代理链与 NAT](chain-nat.md)。）
- **订阅**：YAML 与 links 端点均只含分配到该用户的 active 节点。
- **显式停用/启用开关**：`users.disabled`（`POST /api/user/update` 带 `disabled` 字段，
  用户列表行内"停用/启用"按钮）。停用 → 对其已分配节点扇出 user.remove；启用 → 扇出 user.add 恢复。
  disabled 与 expired（见[订阅](subscription.md)）正交，**有效停权态 = disabled OR expired OR quota_exhausted
  （流量配额用尽）OR 未生效（effective_at 在未来）**：user.add/user.remove 扇出只在
  有效停权态跃迁时发生（已 expired 再 disable 不重复扇出；恢复需全部解除，未生效态由
  sweeper 到点激活解除、配额停权由周期重置/手动重置解除）；有效停权态下
  订阅 YAML/links proxies 为空、userinfo 头照常、落地页显示"已停用"，`NodeUserUUIDs` 不下发。
- Agent 侧无新增状态：`AddUserPayload.Nodes`（tag → 协议参数）驱动精确落点；
  `Nodes` 必须明确给出，不存在“作用于全部节点”的隐式语义。

## 链路分组与用户分组

分组在用户/链路直接分配语义之上提供一层编排便捷，不引入新状态机：

- **链路分组**（`link_groups` + `link_group_chains` / `link_group_external_subscriptions`）：
  把多条共享入口链路与外部订阅编排为一个组。外部订阅**整体原子参与**——移除即整组
  节点从订阅消失。
- **用户分组**（`user_groups` + `user_group_members` + `user_group_links`）：把用户编组
  并关联一个或多个链路分组。组内用户订阅**由分组派生**：直接分配被遮蔽（数据保留，
  移出分组即恢复），与用户外部订阅引入（[订阅](subscription.md)三模式）叠加生效。
- **触发重发布**：分组自身或相关服务器/外部订阅更新自动触发组内全部用户订阅重发布；
  长操作接入 observe 进度弹窗（见[操作进度观察](observe.md)）。
- 管理端点 `/api/link-group/{list,create,update,delete}` 与
  `/api/user-group/{list,create,update,delete}`；e2e 见 `scripts/e2e/groups.sh`。
