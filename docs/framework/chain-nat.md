# 代理链与 NAT

> 拆自[框架设计索引](../framework-design.md)，对应原 §21、§21.1 与 §10 的链路页语义。revision、离线发布、删除、流量倍率和日/月统计的完整契约见[链路 Revision 与流量统计设计](../chain-revisions-traffic-design.md)。

产品概念统一为**链路**：
直连是长度 1 的链路，中转是 `入口 → [中转...] → 出口 → Internet`，客户端仅见入口。

**链路页**是唯一的产品入口，不保留 `/nodes` 页面或重定向（概览/详情双 tab，概览为默认）。创建时先选择：

- **直连**：一台服务器即完整链路，拓扑卡片只显示 `直连 <服务器>:<端口>`，不标入口/出口。
- **中转**：显示 `入口 → 中转（0-2 台）→ 出口`；原 role `middle` 的用户文案统一为“中转”。
- 两类链路混排为同一种卡片，顶部统一显示名称、类型、整体状态、创建时间与业务流量；
  中转内部出口业务 node 不作为直连重复展示。状态文案统一为部署中/正常/异常/降级。
- **不可达 tag**（§告警分类 Ban告警）：入口（第一跳）服务器被 Ban 巡检判定（`banned`）
  且为可直连服务器时，详情卡与概览行对对应链路打红色「{目标省市}不可达」tag——地域随
  三网测速目标省市设置（市级目标显示市，否则省，见 §三网测速）；用户订阅页链路状态
  （`/api/sub/{token}/status` 的 `entry_banned` + `unreachable_region`）同步打标；
  服务器恢复（`recovering`/`normal`）后标记自动消失。判定逻辑见[服务器维护](server-maintenance.md)。
- 创建、重试与删除按钮统一使用“链路”语义；直连调用 node 流程，中转调用 chain 编排。
- 直连支持 vless/socks/http/dokodemo-door；中转出口支持 vless/socks/http。
- 用户页统一称“分配链路”，中转选择项内部仍映射其出口业务 `node_id`。
- 管理层合并不新增第三套统一 API：直连使用 `/api/node/*`，中转使用 `/api/chain/*`，前端聚合展示。

**产品与实现统一**：

- 所有代理入口均落为 `chains`；直连是 1 跳 revision，中转是 2～4 跳 revision。
- 每条 chain 保留内部 `service_node_id/service_uuid`，并引用入口服务器级 `shared_endpoint`。
- 用户授权由 `user_chain_assignments` 表达；订阅使用 assignment 的 `access_uuid`，不再把业务用户
  UUID 下发到出口 service。
- 创建和编辑共用一套 chain API、revision planner 与任务状态机，不再维护直连 node/中转 chain 两套流程。
- 逐跳流量仅用于展示；共享链的用户与链路总量以入口 `access:*` 计数为准，各跳不得相加。

**能力边界**：

- 链长上限 4 跳，同一服务器在一条链中不重复（O(n) 查重即环检测）。
- 端到端加密下中间跳只做 TCP 拼接，3+ 跳的收益仅为路径混淆，故不开放任意长度。

**数据平面**：

- 每跳规则统一：下游有入站能力 → 直连转发（dokodemo-door 式纯 TCP 透传）；
  下游无入站能力 → 下游以 xray reverse bridge 反向上来（bridge/portal 对）。
- **共享入口终止**：客户端 VLESS+Reality 在入口 Endpoint 终止，入口按 assignment identity 选择 chain；
  多跳时再用 chain 的 `service_uuid` 建立到出口的内部 VLESS+Reality 连接。
- **隧道口安全**：portal = 每跳独立的 VLESS+Reality inbound（不共享，吊销粒度细）；
  密钥对由该跳 agent 生成、随 `RPC response` 上报，私钥不出服务器（沿用[虚拟配置](control-channel.md)原则）。
  无认证的隧道监听口会成为开放中继，明确禁止。
- **面板纯控制面**：用户流量永不经过面板机器；B→C 链路中 C 直接对 B 建隧道，
  面板只负责向各跳下发角色与参数。

**NAT 机器两档**（添加服务器表单：机器类型 = 独立 IP / NAT）：

| 档 | 条件 | 可任角色 | 说明 |
|---|---|---|---|
| 受限直连 | NAT + 可用端口非空 | 入口/中间/出口 | 共享公网 IP + IDC 映射端口段（NAT VPS 形态），零隧道开销 |
| 仅出口 | NAT + 可用端口留空 | 出口 | 全 CGNAT 无入站，走 reverse |

- **可用端口**：多项填写（默认一项，"+" 添加），每项支持单端口（`10000`）与范围
  （`10001-10010`）；**支持非 1:1 映射**（外部段:内部段，默认 1:1）。
  非 1:1 时 `realized_config` 区分 `listen_port` / `public_port`，订阅取 `public_port`。
- NAT 类型 `servers.address` **强制必填**（共享 IP 由 IDC 提供），禁用 RemoteAddr 自动学习
  （多出口/负载均衡 NAT 会学错地址，导致订阅静默失效）。
- 端口段建后可改（`POST /api/server/update`）：缩小区间时校验存量节点/链跳/共享端点不越界，越界拒绝；
  机器类型建后不互转。

**存储**：

- `chains` 保存稳定链路、`service_node_id/service_uuid` 和 `endpoint_id`，分别引用 published/desired revision；
  `chain_hops` 保存当前期望拓扑，`chain_hop_identities` 保证 hop ID 删除后不复用。
- `chain_revisions` 保存不可变快照，`chain_revision_tasks` 保存从出口到入口的 apply 和后续 cleanup
  状态；任务通过 `commands` 队列投递并支持重启续跑。
- `shared_endpoints` 与 `user_chain_assignments` 分别保存可复用监听和 assignment 凭证；`user_nodes`
  仅兼容独立节点。
- Agent 上报带 Xray 实例标识的绝对计数器快照；`access:<assignment_id>` 同时归属真实用户和 chain，
  `tunnel:<service_uuid>` 不进入用户配额。后端保存倍率分段累计和每日桶，月度由每日桶汇总。

**编排**：

- 创建和编辑均由不可变 revision 驱动：出口向入口计算依赖并部署，入口最后切换，随后立即清理旧
  revision。无法证明配置等价时保守重建受影响范围。
- Agent 离线仅代表控制通道不可达，已部署数据面继续运行。普通编辑等待必须修改的离线 Agent；
  等待中（`waiting_for_agent`）的编辑可再次编辑取代（无在途命令，废弃安全），管理员可强制发布
  未确认 revision。已发布 active 链任一 hop 离线时由链路状态机（chainFSM）
  推导为 `degraded`，但不退出订阅；服务器删除经 FSM 校验后使引用链路 `invalid` 并退出订阅。
  状态重算由 Agent 连接状态跃迁、端点 ack、周期自愈三层触发；Panel 重启时 ResumeChains 全量
  恢复，运行时 Agent 重连由 ResumeChainsByServer 即时恢复。详见链路设计文档。

**agent 侧**：

- 无全局 `-nat` 模式开关：角色（bridge+业务 inbound / portal+转发 / 转发）完全由
  apply 载荷决定，控制通道、遥测、漂移 reconcile、xray 升级全部复用。
- install.sh 与引导流程（见[引导与运维](onboarding-and-ops.md)）不变；机器类型与端口段是面板侧元数据，不下发到 agent
  （NAT 受限直连机经 `port_candidates` 下发段内监听候选：自动端口段内挑选、手动端口段内校验）。

**订阅**：条目 = 入口 Endpoint 的 `address:public_port` + Endpoint 的 public_key/short_id + assignment `access_uuid`；
命名沿用 `{入口别名}-{协议}-{端口}`；links 端点同构（见[订阅](subscription.md)）。

**端口复用**：自动选端口只挑空闲端口——独立 IP 机挑随机空闲端口（443 不再是默认/首选），NAT 机
只从可用段内挑选；管理员显式指定端口不回退，VLESS 链指定已被受管监听占用的端口时自动加入共享监听（不再报错），
仅未受管的 OS 占用在部署时报错。Agent 占用探测区分端口归属：端口已被自身
受管 xray 持有的（受管端口）视为可复用，重发/自愈直接沿用已落地端口，不因自身 xray 持有而误判
占用；其他服务占用的端口才报冲突。相同 server/port 上任意 VLESS 链可加入既有 Endpoint 共享监听（跨 profile）：
入口参数以先占用链为准（订阅渲染端点参数保持一致），不同协议仍报冲突。NAT 手动指定端口段内校验（面板校验 + Agent 载荷候选双保险），所有共享链的 entry
forward 改为 loopback 内部口，不再消耗公网映射。

**PoC 结论（已验证，GO）**：`scripts/dev/poc-reverse.sh` 实证 reverse bridge/portal + 隧道 Reality +
端到端 Reality 透传可行（链路通、portal 重启自愈、隧道口抗探测分流正常）。两个实测要点：
Reality dest 不稳定（如 www.microsoft.com）会导致合法客户端握手一并失败，dest 白名单必须只收稳定目标
（destCandidates 已遵循，隧道 inbound 复用同一白名单）；反向通道注册存在启动竞态，
bridge 首拨失败由 xray 自动重试兜底，编排层无需处理。

## 实现契约（消息、存储、编排细节）

**每跳的 xray 配置件**（piece，agent 按载荷渲染，全部并入受管 config.json）：

| piece | 所在机 | 内容 |
|---|---|---|
| `forward` | 入口/中间跳 | dokodemo-door 透传 inbound（固定目标，无认证即无滥用面：攻击者只能弹到出口 Reality 口被分流）+ 路由（直连 → freedom 拨下一跳 `address:port`；反向 → reverse portal） |
| `portal` | 反向链的上游机 | VLESS+Reality interconn inbound + reverse portal（密钥对 agent 生成上报，UUID/shortID 面板下发，dest 走预检+白名单） |
| `bridge` | 反向链的下游机（仅出口档 NAT） | reverse bridge + VLESS+Reality interconn outbound + routing（bridge → freedom 拨回环业务 inbound） |
| 业务 inbound | 出口 | 复用现有 `node.apply`（普通 nodes 行），监听不变 |
| shared endpoint | 入口 | VLESS+Reality inbound + assignment clients + 按 chain 聚合的 routing；入口 forward 为 `127.0.0.1` 内部口 |

**新消息类型**（协议演化规则：新语义走新类型）：

- `chain-hop.apply`：`{chain_id, hop_id, kind: portal|bridge|forward, portal?, bridge?, forward?, dest_candidates?}`。
  - PortalSpec `{tag, tunnel_domain, port(0=自动), port_candidates?, tunnel_uuid, short_id, dest, server_names}`
  - BridgeSpec `{tunnel_domain, portal_address, portal_port, tunnel_uuid, public_key, short_id, server_name}`
  - ForwardSpec `{tag, port, port_candidates?, target_address, target_port, via_tunnel_domain?}`
- `chain-hop.remove`：`{hop_id, kind}`（删链逐跳反向下发）。
- `shared-endpoint.apply`：`{endpoint_id, config, clients, routes, dest_candidates?, port_candidates?}`；
  完整期望状态替换，assignment 变更不改变已实现端口和 Reality 密钥。建链即部署监听：即使
  routes/clients 为空也下发 apply（端口 + Reality 密钥即刻落地），用户分配仅做增量添加，不再是部署前提。
- `shared-endpoint.remove`：`{endpoint_id}`。
- `RPC response` 增加 `hop_id`、`kind`（omitempty），portal/forward 复用 `realized_config.port/public_key` 回执。
- `node.apply` 载荷增加 `port_candidates`（omitempty）：受限直连 NAT 机上节点端口从段内挑选。

**tunnel_domain**：`c<chainID>h<hopID>.lx`，链内唯一，reverse 路由键。

**建链编排**（panel 状态机，满足凭证依赖；每步成功回执后推进，失败定位到跳）：

1. 多跳链先部署出口内部业务 inbound（`node.apply`，只含 `tunnel:*` client）→ realized 端口；
2. 各反向链的 `portal`（由出口向入口方向逐个）→ 回执 pubkey/端口；
3. 各反向链的 `bridge`（携带对应 portal 凭证）；
4. 各 `forward`（由出口向入口方向；共享入口的 entry forward 只监听 loopback）；
5. 发布 revision 后完整 reconcile shared Endpoint（无用户也部署监听，端口与密钥即刻生效）；
   Endpoint active 后订阅输出该链。
任一链内 piece 失败 → 链 failed 定位到跳，重试只重放失败 piece；Endpoint 失败单独保留错误与重试状态。

**存储 DDL**（全新安装基线；存量库由面板启动迁移到该结构）：

```sql
CREATE TABLE servers (
  -- 其他基础字段省略
  machine_type TEXT NOT NULL DEFAULT 'direct', -- direct|nat
  allowed_ports TEXT NOT NULL DEFAULT ''       -- JSON [{pub_start,pub_end,listen_start,listen_end}]
);
CREATE TABLE chains (id INTEGER PRIMARY KEY AUTOINCREMENT, status TEXT NOT NULL DEFAULT 'pending',
  error TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE chain_hops (id INTEGER PRIMARY KEY AUTOINCREMENT,
  chain_id INTEGER NOT NULL REFERENCES chains(id), seq INTEGER NOT NULL,
  server_id INTEGER NOT NULL REFERENCES servers(id), role TEXT NOT NULL,
  node_id INTEGER NOT NULL DEFAULT 0,           -- 仅出口跳：业务 nodes.id
  status TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
  forward_port INTEGER NOT NULL DEFAULT 0,      -- entry 跳 = 订阅端口
  portal_port INTEGER NOT NULL DEFAULT 0, portal_public_key TEXT NOT NULL DEFAULT '',
  portal_server_name TEXT NOT NULL DEFAULT '',  -- portal 回执的 Reality SNI（bridge spec 用）
  tunnel_uuid TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
-- 链状态：pending/applying/active/degraded/failed/waiting_for_agent/active_unconfirmed/active_failed/cleanup_pending/invalid/deleted
-- 跳状态：pending/applying/active/failed
```

**degraded 推导与链路状态机**：所有链状态变更经 `internal/dispatch/chain_fsm.go` 的转换表校验。
hub 注销/注册连接时由 FSM Evaluate 重算——链任一跳 server 离线 → degraded + 告警
（`chain_degraded`，防抖沿用，见[服务器维护](server-maintenance.md)）；全部跳 server 在线且跳均 active → active。
服务器删除经 `InvalidateForServerDeletion` 事务级联失效。Agent 重连后 `ResumeChainsByServer`
恢复编排中的链。完整转换表与三层自愈设计见[链路设计文档](../chain-revisions-traffic-design.md)。
