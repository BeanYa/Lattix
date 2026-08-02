# 链路 Revision 与流量统计设计

## 目标与边界

Lattix 是控制面，用户流量不经过 Panel。所有代理入口统一建模为 `chain`：直连是 1 跳，
中转是 2～4 跳；客户端只连接入口服务器，出口和内部传输细节对客户端透明。

受控链路只支持安装了 Lattix Agent 的服务器；外部订阅节点独立存储于
`external_chains`，不参与链路状态机、Revision 与流量统计，仅作为订阅输出条目
（见 [framework-design §9.1](framework-design.md#91-外部订阅导入与用户关联)）。
不引入动态路由、负载均衡或商业化用户分组。Panel 与 Agent 使用完全相同的发布版本，
不维护跨版本命令兼容层。

## 稳定身份与 Revision

- `chain_id` 是链路的稳定身份。
- 每条链拥有稳定的 `service_node_id` 和内部 `service_uuid`；它们只用于链内 transport，不是业务用户。
- 用户授权绑定 `user_chain_assignments`。每个用户/链 assignment 拥有独立 `access_uuid`，因此同一
  用户可以同时使用多个共享同一 `IP:port` 的链，并被入口准确分流。
- `shared_endpoints` 是服务器级 VLESS+REALITY 监听。兼容 profile 复用同一端口和 Reality 密钥；
  已被不兼容受管监听占用的端口返回冲突，未受管进程占用由 Agent bind probe 检出。
  Agent 侧占用探测区分端口归属：端口被自身受管 xray 持有的视为可复用，重发/自愈直接沿用已落地
  端口，不误判占用；其他服务占用的端口才报冲突。
- `chain_revisions` 保存包含有序 hop 的不可变目标快照；chain 分别引用当前发布 revision 和待部署 revision。
- revision 快照保存 1～4 个有序受控服务器。保留的 hop 沿用稳定 `hop_id`；删除后重加
  同一服务器视为新 hop。
- 删除的 hop、revision 和 chain 只做归档，不删除流量或任务审计历史。

直连和中转不再走两套产品模型或 API。新建 VLESS 链默认使用共享 Endpoint；历史 `user_nodes`
和独立节点订阅继续兼容，编辑历史链不会隐式迁移数据面。

## 共享入口与端口策略

- 独立 IP 服务器自动选端口时挑随机空闲端口（443 不再是默认首选）；管理员显式指定端口时，冲突必须
  报错，不静默改端口。
- NAT 服务器只从 `allowed_ports` 选择监听端口，不越过运营商映射范围；自动端口段内挑选，手动指定
  端口段内校验（面板校验 + Agent 载荷候选双保险）。多个兼容链复用一个 Endpoint，
  因而只消耗一个公网映射端口。
- 共享入口后，每条链原有的 entry forward 改为 `127.0.0.1` 内部监听，不再占用 NAT 公网端口。
- Endpoint routing 按 chain 聚合：一条 chain 一条路由规则，规则的 `user` 数组包含该链全部 active
  assignment identity；规则数量是 O(chains)，客户端条目数量是 O(assignments)。

## 编辑范围

编辑器与创建器使用同一份表单，支持：

- 修改名称；
- 对 1～4 台服务器进行添加、删除、替换和重排；
- 修改用户可见入口端口；
- 修改链级代理协议及完整协议参数；
- 修改流量倍率。

同一服务器不能在一条链内重复。服务器地址、NAT 端口范围和标签仍在服务器管理中修改。
内部 transport 由 planner 自动选择，不开放逐跳手工配置。

## 差分 Planner

Planner 从出口向入口构造规范化 spec，并对规范化 JSON 求 hash。只有自身 spec 及全部下游依赖
均相同的 piece 才可复用；无法证明等价、realized 数据缺失或配置漂移时，保守重建受影响范围。

部署遵循 make-before-break：

1. 持久化 desired revision 和幂等任务图；
2. 从出口向入口部署新增或变化的 service、portal、bridge、forward piece；
3. 入口内部 forward 最后切换；
4. 提升 published revision；
5. 以完整期望状态 reconcile 共享 Endpoint（clients + 按链聚合 routes；即使无用户也部署监听，
   端口与 Reality 密钥即刻生效）；
6. 立即清理旧 revision 不再引用的 piece。

不设客户端迁移宽限期。协议或端口无法并存时允许最终切换发生一次短暂 Xray reload。完整重建
也必须遵守上述顺序，不能先拆旧链。

每个任务使用稳定幂等键 `chain_id/revision_id/hop_id/piece/action`。编辑任务描述依赖和阶段；已有
Agent command 队列负责投递、重试与回执。Panel 重启后从数据库继续未完成任务，同一 chain 同时
只允许一个编辑。

## 状态与离线语义

Panel 的 Agent 在线状态只表示 Agent→Panel 控制通道可达，不代表服务器对客户端或相邻跳不可达。
已经落盘的 Xray 数据面在 Panel 或控制通道断开时继续工作。

### 链路状态机（chainFSM）

所有链状态变更统一经 `internal/dispatch/chain_fsm.go` 的状态机入口，不直接调用
`store.SetChainStatus`。状态机提供三个入口：

| 入口 | 用途 | 触发方 |
|------|------|--------|
| `Transition` | 显式状态变更（编排推进/重试/删除） | advanceChain、RetryChain、failChain、StartChain |
| `Evaluate` | 条件驱动评估（active ↔ degraded） | Agent 上/离线、端点 ack、发布后、周期自愈 |
| `InvalidateForServerDeletion` | 服务器删除级联失效 | panel.handleDeleteServer |

辅助入口 `ResumeChainsByServer`：Agent 重连后恢复该服务器上处于编排中的链
（applying/waiting_for_agent/active_unconfirmed → advanceChain）。

设计原则：

- 转换表强制校验合法性，非法转换拒绝并记录日志（不 panic）；
- 相同状态幂等（from == to 直接返回，不触发副作用）；
- 副作用（告警、操作日志、端点 reconcile）声明式绑定到状态进入，与转换逻辑解耦；
- Evaluate 是幂等的：相同条件重复调用不产生多余转换或副作用。

### 状态定义

链路共有 11 个状态：

| 状态 | 含义 |
|------|------|
| `pending` | 已创建，未开始编排 |
| `applying` | 正在部署 revision piece |
| `active` | 目标配置均已确认，订阅可用 |
| `degraded` | 运行时推导：某跳离线或端点未就绪（订阅仍输出） |
| `failed` | 首次编排失败 |
| `waiting_for_agent` | 目标 revision 需要修改的 Agent 不可达 |
| `active_unconfirmed` | 管理员强制发布了未确认配置 |
| `active_failed` | 强制发布后的任务执行失败 |
| `cleanup_pending` | 新 revision 已生效，旧配置仍待清理 |
| `invalid` | 引用的服务器已被删除 |
| `deleted` | 链路已软删除（终态） |

### 转换表

```text
pending            → applying | invalid | deleted
applying           → active | active_unconfirmed | failed | waiting_for_agent | invalid | deleted
active             → degraded | active_failed | cleanup_pending | invalid | deleted
degraded           → active | active_failed | cleanup_pending | invalid | deleted
failed             → applying（重试）| invalid | deleted
active_unconfirmed → active（agent 确认）| applying（重试）| failed | invalid | deleted
active_failed      → applying（重试）| active（重新评估恢复）| invalid | deleted
waiting_for_agent  → applying | failed | invalid | deleted
cleanup_pending    → active（清理完成）| deleted | invalid
invalid            → deleted
deleted            →（终态，无出边）
```

### 外部事件输入

| 事件 | 接线 | FSM 处理 |
|------|------|----------|
| Agent 上线 | hub.OnConnect → OnAgentConnect | Evaluate（恢复 degraded→active）+ ResumeChainsByServer（续编排） |
| Agent 离线 | hub.OnDisconnect → RecomputeChainsByServer | Evaluate（active→degraded + 告警） |
| 共享端点 ack | handleCommandResponse | Evaluate（端点就绪 → degraded→active 恢复） |
| 编排推进 | advanceChain / handleChainHopResult | Transition（applying→active/failed 等） |
| 管理员操作 | RetryChain / 删链 | Transition（failed→applying / *→deleted） |
| 服务器删除 | panel.handleDeleteServer | InvalidateForServerDeletion（事务级联 → invalid） |
| 周期自愈 | 60s ticker → ReconcileStaleEndpoints | 间接触发 Evaluate |

### 条件评估（Evaluate）

Evaluate 仅对 `active` 或 `degraded` 状态的链生效，按优先级检查：

1. **跳服务器在线性**：任一跳 server 离线 → active→degraded（附定位描述）+ chain_degraded 告警；
2. **共享端点就绪性**：端点未 active → active→degraded + 自动重试（服务器在线时立即 ReconcileSharedEndpoint）；
3. **恢复判定**：全部跳在线 + 端点 active + 全部跳 hop status 为 active → degraded→active + chain.recovered 日志。

### 三层自愈保障

| 层级 | 触发 | 覆盖场景 |
|------|------|----------|
| 即时重试 | Evaluate 检测端点未 active 且服务器在线 | 命令丢失、首次 reconcile 失败 |
| 连接自愈 | OnAgentConnect → reconcile 该服务器全部未 active 端点 + 恢复编排 | 死信命令、Agent 重启、编排停滞 |
| 周期兜底 | 60s ReconcileStaleEndpoints 遍历所有在线服务器 | 上述两层未覆盖的边缘情况 |

### 离线语义

普通创建或编辑遇到必须修改的离线 Agent 时不发布，任务保持队列状态；完全未变化的已部署 hop
可以直接复用。管理员可以执行"强制发布"：立即更新订阅、抛弃旧 revision 并开始 cleanup，未确认
命令继续排队。强制发布是不可自动回滚的单向操作；失败后只能重试或再次编辑。

被目标拓扑删除的离线服务器不阻塞发布，其 cleanup 命令排队。若仍留在目标拓扑的前驱服务器需要
修改下一跳且当前离线，普通发布必须等待，除非管理员强制发布。

已发布的 `active` 链在任意入口、中间跳或出口 Agent 断开时由 Evaluate 推导为 `degraded`；这只
表示控制面无法确认该跳，不撤销订阅，也不推断数据面已经中断。全部 Agent 恢复在线且 hop 均为
`active` 后自动恢复为 `active`。Panel 重启时 `ResumeChains` 全量恢复编排中的链；运行时 Agent
重连由 `ResumeChainsByServer` 即时恢复。

## 删除语义

删除服务器时先找出引用它的 active/desired revision：

1. 相关链路立即标记 `invalid` 并从订阅移除；
2. 取消未发布 revision；
3. 向其余服务器发送相关 cleanup；
4. 发给被删除服务器的 queued 命令标记 `abandoned`，迟到回执忽略；
5. 撤销 Agent 凭证并删除服务器。

失效链路保留，可通过替换缺失 hop 重新发布。删除 chain 时立即退出订阅并清理配置，但 chain、
revision、流量和任务历史均软删除保留。

## 内部传输

- 用户 VLESS+REALITY 在共享入口终止；多跳链由入口使用内部 `service_uuid` 重新建立到出口 service 的
  VLESS+REALITY 连接，已有 forward/portal/bridge 仅承载该内部连接；
- SOCKS/HTTP 等明文协议用于多跳时，内部段自动加密；
- 下游无入站能力时使用 Reality reverse portal/bridge；
- 链路详情展示 planner 选择的 `direct`、`encrypted` 或 `reverse`，但用户不能逐跳修改。

## 流量口径

Agent 上报 Xray 计数器绝对快照，而不是未确认的区间增量。每帧携带 `xray_instance_id` 和绝对值；
Backend 持久化每个实例/计数器的最后值并在事务内计算增量。重复快照增量为零，丢帧由下一帧补差，
Xray 重启通过新 instance 区分。

- 共享 Endpoint 的 `access:<assignment_id>` 用户计数器是用户流量和链路总流量的唯一权威来源；
- 用户总量按 assignment 反查真实 `user_id` 后累计；链路总量按 assignment 的 `chain_id` 累计；
- `tunnel:<service_uuid>` 永不进入用户配额，出口 service 与 hop 计数只用于服务器/链路运维对账；
- 历史非共享链仍以出口 service inbound 为链路总量权威；
- 中间 hop 只用于展示，不作计费准确性承诺；
- portal、bridge 和内部 outbound 不计入 hop 业务流量；
- 上下行始终以客户端视角定义，各 hop 流量不得相加。

### 控制通道离线时的补报

Agent→Panel 控制通道断开不会停止 Xray 绝对计数器。只要 Xray 实例未重启，Agent 重连后的首帧
与 Backend 持久化游标补差，可以一次性补齐断线期间的累计量：

- 共享入口 Agent 离线时，用户/链路权威总量暂停，重连后由入口绝对计数器补差；
- 中间或出口 Agent 离线时，用户/链路权威总量仍由入口补差，相关 hop/server 运维展示暂停；
- 所有 Agent 离线时所有展示暂停，以各自重连后的绝对计数器补差。

当前实现不是计费级离线流水，存在以下明确边界：

- 离线期间 Xray 重启会改变 `xray_instance_id`；新实例从零累计，旧实例最后一次上报之后的流量
  无法恢复，因为 Agent 不持久化带序号的本地流量流水；
- 补差只有 Backend 接收时间，没有离线期间的分段采样时间，整段增量归入重连当天的日桶；累计
  总量可补齐，但跨日/月曲线无法还原真实日期分布；
- 若离线期间链路倍率改变，补报增量按重连时匹配到的当前 published revision 与倍率入账，不能按
  离线期间的历史倍率拆分。

若后续需要计费级准确性，Agent 必须持久化包含实例、计数器、采样区间、revision/倍率和单调序号的
增量流水，Backend 按序号幂等回放；本阶段保持绝对计数器补差，不引入该复杂度。

流量倍率属于 chain，默认 `1.000`，范围 `0.001～1000.000`，最多三位小数。API 和数据库使用十进制
字符串/千分整数，不使用二进制浮点累计。倍率从修改时刻起作用于新增流量，不追溯历史；后端同时
保留 raw 与倍率后流量，跨 telemetry 保存取整余数。

## 日/月统计与重置

- 原始采样时间使用 UTC；新增独立 `traffic_timezone`，默认 `Asia/Shanghai`；
- 只持久化每日桶，包含 raw/effective 上下行、chain、hop、revision 和统计时区；
- 月统计由每日桶动态汇总；日桶长期保留；
- 链路详情提供按日 30/90/365 天和按月 12/24 月视图；
- 删除 hop 的数据只在历史 revision 中展示。

“重置流量”创建 checkpoint：链路和当前各 hop 的累计展示从该时刻归零，但不删除 Xray 计数器、
每日桶或自然日/月历史。支持单链和全部链路重置，不支持单 hop 重置。

## 订阅发布

订阅只输出 published revision。普通编辑在新入口得到确认前继续输出最后一个 confirmed revision；
全新 chain 在确认前不进入默认订阅。强制发布会立即改为输出 unconfirmed revision。内部 hop 变化且
入口参数不变时，用户链接保持不变；入口服务器、端口或协议变化后，客户端需刷新稳定的订阅 URL。
同一真实用户在每条链的订阅 UUID 都来自对应 assignment，而不是全局 `users.uuid`。

发布时收集已分配但未纳入订阅的链的原因（未发布有效修订 / 条目构造失败），持久化到快照 `warnings`
（schema v10）并经预览/"重新生成"API 返回，前端提示"部分条目未纳入本次订阅"。`clash` 格式内置
fake-ip DNS，策略含 GEOSITE/GEOIP 规则时另输出 geodata-mode + geox-url，规则在客户端直接生效。

## Panel / Agent 版本同步

Panel 更新后向所有在线 Agent 下发同步到相同版本的更新命令；离线 Agent 重连后立即同步。版本不一致
期间已有数据面继续运行，revision 命令可以入队但不得投递。强制发布不能绕过版本不一致。更新失败
显示 `version_sync_failed` 并允许重试；Panel 更新不等待永久离线 Agent。

## 验收测试

- Planner：增删、替换、重排、协议变化、正向/反向变化、完整重建回退；
- 状态机：正常编辑、离线等待、重启续跑、强制发布、迟到回执、失败、cleanup、服务器删除、
  非法转换拒绝、Agent 重连恢复编排、端点自愈三层保障、degraded↔active 往返；
- Agent：revision piece 共存、幂等、入口最后切换、清理与 Xray 回滚；
- 流量：绝对值去重、丢帧补差、实例切换、倍率分段、跨日/月、时区和 reset；
- API/前端：预览、提交、强制发布、重试、删除、流量查询、类型检查和生产构建；
- CI 使用 fake Agent 完成编排集成测试，真实多机网络/Xray 保留手工验收脚本。
