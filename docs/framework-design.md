# Lattix 设计文档（MVP 实施契约）

> 本文档是 MVP 阶段的完整项目目标。凡标注"后续迭代"的内容均不属于 MVP 范围，
> 不在 MVP 中做任何为其预留的实现，除非本文明确说明（如 requester 接口）。
> HTTP RPC、Requester 与 Agent WS 的协议细节以
> [RPC API、Requester 与 Agent 通道设计](rpc-api-design.md) 为准。
> 服务器测试的目录、原子任务、隔离、权限降级与结果协议以
> [服务器测试设计与实现契约](server-testing-design.md) 为准。

## 1. 项目目标

主控面板（Backend）统一管理多台受控服务器（Agent），流程：

1. 管理员在面板通过可视化向导填写某协议在 xray 上的完整配置，生成一份**虚拟配置**；
2. 虚拟配置经控制通道下发到目标服务器的 Agent；
3. Agent 将虚拟配置落地为 xray 实际配置并生效，上报实际配置结果；
4. 面板统一管理链路、服务器级共享入口和用户链路分配，按 assignment 生成订阅凭证。

参考 3x-ui / s-ui / miaomiaowuX 的交互模式，不做商业化。

**规模假设**：服务器少于 30 台，管理员 1 人，实际用户少于 30 人。所有服务器假设具备公网 IP 与完整公网访问能力；NAT 机器（共享 IP / 无入站）与代理链支持自 0.0.2 之后迭代实现，见 §21。

**MVP 协议范围**：仅 VLESS + Reality，flow 固定 `xtls-rprx-vision`，uTLS 指纹固定 `chrome`。

## 2. 架构总览

```
┌─────────────┐        单条 WebSocket 长连接         ┌─────────────┐
│   Backend   │ ◄──────── (agent 主动拨出) ──────── │    Agent    │
│  (面板+API)  │                                    │ (受控服务器) │
└──────┬──────┘                                     └──────┬──────┘
       │                                                   │
   SQLite 存储                                        独占管理 xray
       │                                            (配置落地/热操作)
┌──────┴──────┐
│  Frontend   │  React SPA，构建产物由 Backend 直接托管
└─────────────┘
```

核心决策：

- **单通道**：Agent → Backend 一条 WebSocket 长连接承担全部双向通信。Backend 永不主动外连 Agent，Agent 不提供任何监听 API 端点。该形态同时兼容公网服务器与 NAT 服务器（§21，自 0.0.2 之后迭代实现）。
- **无 fallback 实现**：Backend 侧定义 `requester` 接口隔离"发送命令"与"具体传输"，MVP 只有 WebSocket 一个实现；gRPC/HTTP 等其他实现属后续迭代。
- **离线排队**：Agent 离线期间，发往它的命令滞留于 `commands` 表，重连后补发。
- **重发与死信**：重连时将该服务器 `sent` 未终态的命令重置为 `queued` 重新补发（幂等性由 Agent 各处理器保证）；`attempts` 超过上限（10 次）标记 `failed` 死信，不再重发；`node.apply` 死信时对应节点同步置 `failed` 并记录原因（§6）。

## 3. 仓库结构与技术栈

```
src/frontend/  # Vite + React + TypeScript + shadcn/ui，包管理器使用 bun
               # 可插拔主题系统（src/frontend/src/themes/，默认 Apple HIG，见 frontend.md）
src/backend/   # Go：面板 HTTP API + Agent WS 端点 + SQLite
src/agent/     # Go：独立二进制，systemd 托管
src/shared/    # Go module：WS 消息结构体、虚拟配置类型，backend/agent 共用
scripts/
  install-panel.sh   # 面板原生/Docker 安装实现
  install-agent.sh   # Agent 安装实现
install.sh           # 唯一面向用户的统一安装入口
```

- 前端依赖安装与构建：`bun install` / `bun run build`，锁文件入库。设计语言由语义
  令牌 + 主题注册表驱动：默认 Apple HIG，经典 Cream Grid 作为可选主题安装，设计主题
  与浅色/深色外观两个维度运行时切换（约定见 [前端开发](frontend.md)）。
- 后端与 Agent 均为 Go，通过 Shared module 共享消息定义，保证协议两端类型一致。
- 数据库：SQLite（规模假设下绰绰有余）。
- 版本管理：git，monorepo。**项目初始化第一步即初始化 git 仓库**（`git init`），全部开发在 git 工作流中进行；`.gitignore`、前端锁文件（bun）、go.work 等随首次提交入库。

## 4. 数据模型（SQLite）

| 表 | 字段（要点） |
|---|---|
| `servers` | id, alias, country_code(ISO 3166-1 alpha-2), location(城市/机房位置), address(公网地址), learned_addr(拨入学习地址), nic_addresses(agent 上报网卡地址 JSON), tags(有序标签 JSON，名称模板 `TAG[n]` 来源), token(长期凭证), last_seen_at, xray_version, config_drift(§17), created_at |
| `users` | id, name, uuid, sub_token, expires_at(可空，unix 秒，NULL=长期), expired(0/1 到期停权标记，§9), disabled(0/1 显式停用标记，§16), created_at |
| `nodes` | id, name(解析后的管理/订阅名称), server_id, protocol, port, config_template(JSON), realized_config(JSON), status, error, created_at |
| `commands` | id, request_id, trace_id, server_id, type, data(JSON), status(queued/sent/acked/failed/abandoned), error, attempts, created_at, updated_at |
| `user_nodes` | user_id, node_id（§16 逐节点用户分配，默认全关） |
| `shared_endpoints` | server_id, protocol, port, profile_hash, config_template/realized_config, status；兼容链共享一个 VLESS+REALITY 监听 |
| `user_chain_assignments` | user_id, chain_id, access_uuid；真实用户与链的直接多对多分配 |
| `server_metrics` | server_id, load1/load5/load15, cpu_percent, mem/disk 用量、默认出口网卡速率/累计量、uptime、latency_ms、updated_at（§13 主机遥测最新值） |
| `server_metric_history` | 与 `server_metrics` 同口径的 24 小时时序样本；按服务器与采样时间索引（§13） |
| `providers` | id, name（大小写不敏感唯一）, website_url, created_at, updated_at |
| `server_billing` | server_id, enabled, provider_id, amount_minor（原币种最小单位整数）, currency（ISO 4217）, service_started_on, interval_count + interval_unit, next_renewal_on, status, assumed_valid_through, last_inspected_on |
| `server_traffic_plans` | server_id, quota_bytes（NULL=无限）, accounting_mode（outbound/bidirectional/max）, reset_anchor_on, reset_count + reset_unit, tracking_started_on |
| `server_network_usage_daily` | server_id, usage_date, tx_bytes, rx_bytes（主机网卡累计计数器按日增量） |
| `exchange_rates` | base_currency, quote_currency, rate（十进制字符串）, rate_date, source, fetched_at（Frankfurter 持久化缓存） |
| `custom_exchange_rates` | source_currency（唯一）+ source_amount, target_currency（保存时的展示币种）+ target_amount, enabled；至少一侧金额为 1，每个目标币种仅一个启用锚点 |
| `traffic` | node_id, user_uuid, up, down, updated_at（§13 流量累计：节点维度 user_uuid=''，用户维度 node_id=0） |
| `chains` | 稳定 chain/service/endpoint identity、published/desired revision、倍率、发布状态与软删除时间 |
| `chain_hops` / `chain_hop_identities` | 当前期望工作拓扑与不会复用的稳定 hop identity |
| `chain_revisions` / `chain_revision_tasks` | 不可变拓扑快照及 apply/cleanup 任务状态机；任务关联离线命令队列 |
| `traffic_cursors` | Agent Xray 实例绝对计数器游标，用于幂等补差 |
| `chain_traffic_totals` / `chain_traffic_daily` | 链与逐跳 raw/effective 累计、倍率余数及按 revision/统计时区的日桶 |
| `chain_traffic_baselines` / `chain_multiplier_events` | 流量重置 checkpoint 与倍率分段审计 |
| `endpoint_traffic_totals` | 共享入口 inbound 的运维流量累计；用户/链权威流量另按 assignment identity 入账 |
| `link_groups` / `link_group_chains` / `link_group_external_subscriptions` | 链路分组及其成员（共享入口链路多条编排、外部订阅整体原子参与，§22） |
| `user_groups` / `user_group_members` / `user_group_links` | 用户分组、成员及「用户组 × 链路分组」关联（§22；组内用户订阅由分组派生） |

说明：

- `commands` 表充当**离线命令队列**并保留命令生命周期；面板操作日志与 API 请求日志
  使用独立存储、容量和查询接口，详见 [日志系统设计](logging-design.md)。
- `nodes.config_template` 是面板侧虚拟配置（含占位符）；`nodes.realized_config` 是 Agent 上报的实际生效值（端口、public_key、short_id 等）。
- `servers.country_code` 与 `servers.location` 在创建/编辑服务器时必填；国家通过选择器写入标准两位代码，`COUNTRY` 与 `COUNTRY_FLAG` 由代码派生。Location 提供按国家过滤的本地城市建议，同时允许管理员填写机房区域等自由文本。
- `servers.address` 是订阅中节点地址的唯一来源（§9）：**创建服务器时由管理员填写公网地址，agent 不校验**；留空则按 agent WS 拨入的对端 IP 自动学习（可信反代——回环、内建内网/容器网段或 `trusted_proxies` 追加网段——时从 `X-Forwarded-For` 右向左取第一个非可信 IP，公网对端直连不信任该头以防伪造），一经写入不再被覆盖（地址变更由管理员通过 `POST /api/server/update` 修改）。每次 `agent.session.open` 另将拨入学习地址写入 `learned_addr`、将 agent 上报的网卡非回环地址写入 `nic_addresses`，二者仅作面板"编辑地址"的内置候选（可选内置地址或自定义），不参与自动学习决策。`servers` 同时保存机器类型与 NAT 可用端口段元数据（含非 1:1 映射的 public_port），NAT 类型 address 强制必填、禁用自动学习；引入链后订阅地址改取链**入口**的 address。
- 用户-节点关联（§16）：全新数据库中的新建用户和节点默认不关联，由管理员显式分配；结构迁移不会隐式补全或改变现有关联。
- 服务器费用、生命周期状态机、流量额度、汇率换算及后端公共定时任务器的完整契约见[后端调度与服务器计费设计](billing-scheduler-design.md)。费用原价和币种分列保存；关闭“统计计费”仅排除费用逻辑和状态展示，不删除资料。流量计划始终独立生效，默认无限额度。

## 5. 控制通道协议

连接：Agent 携带服务器 token 拨出至 `Backend /api/agent/ws`。

信封：JSON，统一包含 `kind`、`type`、`request_id`、`trace_id` 和
`data`；响应另含 `code`、`message`。请求和响应使用相同 `type`，
`type` 采用 `domain.action`。完整结构见
[WS JSON Schema](ws-protocol.schema.json)。

消息类型：

| type | 方向 | 说明 |
|---|---|---|
| `agent.session.open` | agent→panel | 每条已鉴权 WS 的会话初始化：agent/xray 版本、运行状态、网卡地址与上次生命周期观测；首次 bootstrap 在两阶段事务中换发长期凭据，普通重连不轮换 token |
| `agent.session.ready` | agent→panel | Agent 已应用生命周期快照且首次 liveness 成功，Panel 据此完成 connecting/reconnecting → online |
| `agent.credential.commit` | agent→panel | Agent 已原子保存首次换发的长期凭据，Panel 据此使 bootstrap 失效 |
| `panel.lifecycle.changed` | panel→agent | 带回执的 Panel 生命周期同步；更新开始前以有界 ACK 屏障确认 Agent 已暂停延迟探测 |
| `agent.settings.sync` | agent→panel | session ready 后、设置提示后及在线期间周期拉取全局 Agent 设置；请求携带已应用 revision，响应按需返回完整统一设置文档 |
| `agent.settings.changed` | panel→agent | 在线设置变更提示；仅触发 Agent 立即 pull，不承载配置本身 |
| `node.apply` | panel→agent | 下发节点：虚拟配置模板 + 分配到该节点的用户 UUID 列表（§16） |
| `shared-endpoint.apply` | panel→agent | 完整替换服务器级共享入口：assignment clients + 按 chain 聚合的 routes；重发复用端口与 Reality 密钥 |
| `shared-endpoint.remove` | panel→agent | 显式删除共享入口配置件；当前常驻复用策略不自动触发 |
| `node.remove` | panel→agent | 删除节点 |
| `user.add` | panel→agent | 向载荷指定节点的 inbound 热加入一个用户（`nodes` 参数携带各节点协议参数；必填，缺省/为空回执错误） |
| `user.remove` | panel→agent | 从载荷指定节点的 inbound 热移除一个用户（`nodes` 必填，同 user.add） |
| 对应请求的 `response` | agent→panel | `code=OK` 时 data 可含 realized_config；失败由稳定 code 与安全 message 表达 |
| `agent.uninstall` | panel→agent | 卸载 agent：先回执再自毁。`purge_xray=true` 连同 xray/配置清除，`false` 仅移除 agent（xray 与节点继续运行）。识别 install.sh 安装树（`…/lattix-agent/bin/lattix-agent` 或 `…/.lattix-agent/bin/lattix-agent`）：系统路径经独立 systemd unit 跑清理（`systemd-run` → `/run` oneshot → setsid 三级回退，避免 `KillMode=control-group` 杀掉 cleaner；脚本先 `disable --now` 压制 `Restart=always`）；用户态 setsid 停 runner/crontab 后删文件。dev 非安装路径不触碰宿主机安装。清理清单与 `latx-ag uninstall` 双向对齐（含 connection/command-queue/lock/log/bak）。面板删服务器为 best-effort，不证明远端物理删除 |
| `xray.upgrade` | panel→agent | 升级 xray 到指定版本（§18）：下载官方 release 校验 .dgst 后替换重启，失败回滚 |
| `agent.upgrade` | panel→agent | 下载、校验并原子替换当前 Agent，响应后退出并由 systemd 拉起 |
| `telemetry.report` | agent→panel | 周期遥测（§13）：xray 版本/运行状态、主机指标、流量增量；无需回执 |
| `config.drift` | agent→panel | 配置漂移状态变化（§17）：外部修改时 true，修复/恢复后 false |
| `server.settings.sync` | agent→panel | 服务器级设置（当前为 xray 版本锁定，面板默认 + 单服务器覆盖两级）按 revision 拉取同步，机制照抄 `agent.settings.sync`；面板默认或覆盖变更经 `server.settings.changed` 提示在线 Agent 立即 pull |
| `xray.cleanup` | panel→agent | 按期望 inbound tag / piece 集合清理 xray 受管配置件（支持 dry_run 预览），详见 [xray 清理设计](xray-cleanup-design.md) |
| `xray.rebuild` | panel→agent | 重建服务器 xray 配置（期望状态全量重放，服务器页"重建 xray 配置"） |
| `server-test.run` / `server-test.progress` / `server-test.result` | panel→agent / agent→panel | 服务器测试原子任务下发、尽力进度上报与断线可恢复的权威最终报告，详见[服务器测试设计](server-testing-design.md) |

Panel 生命周期为 `startup|active|updating|faulted`；每个 Agent 的连接状态为 `never_connected|connecting|reconnecting|online|offline|auth_rejected`。HTTP Upgrade 使用 Bearer token，明确鉴权失败返回 HTTP 403 与结构化 RPC body，暂时不可用返回 503。liveness 与 latency probe 分离：liveness 维持连接，latency 只在 active 测量且失败不再断开 WS。完整语义见 [Panel 生命周期与 Agent 连接状态机](panel-lifecycle-state-machine-design.md)。

## 6. 节点生命周期与 apply 流水线

节点状态机：`pending → applying → active | failed`。`failed` 携带错误详情，面板提供重试按钮。（自 0.0.2 之后迭代：链场景的跨机编排、链级状态机与逐跳重试见 §21；本节流水线对链中每一跳仍然适用。）

Agent 收到 `node.apply` 后的落地流水线（顺序固定）：

1. 填充模板占位符（见 §7）；
2. **dest 预检**：对模板 dest 做 TCP+TLS 可达性检查；不可达则按 `node.apply` 携带的白名单候选（`dest_candidates`，面板内置并随版本更新，尽量丰富）逐个尝试，全部失败则上报 error；
3. 写入临时配置文件；
4. `xray run -test -config <file>` 校验，失败则丢弃并上报 error；
5. 落盘正式配置（Agent **独占管理** `/opt/lattix-agent/config/xray.json`；
   用户模式对应 `~/.lattix-agent/config/xray.json`）；
6. 调 xray gRPC API 热操作（`AddInbound` / `AlterInbound` / `RemoveInbound`）；
7. 热操作失败才 `systemctl restart xray`；
8. 重启失败则恢复上一份可用配置、再次重启，并上报 failed。

参考依据：3x-ui/x-ui 系通过 xray gRPC API 的 `AlterInbound`（AddUser/RemoveUserOperation）实现增删用户零重启；XrayR/V2bX 类商业化节点侧通过进程内重载 xray-core 达成同等效果。MVP 采用前者为主路径、重启为兜底。

## 7. 虚拟配置与参数分工

虚拟配置 = **xray inbound JSON 模板 + 占位符**，Agent 填值后**原样写入**，不存在任何"翻译层"。

| 参数 | 生成方 | 说明 |
|---|---|---|
| 独立节点用户 UUID | 面板 | 历史/独立节点继续使用 `users.uuid` |
| 链路访问 UUID | 面板 | 每个 user-chain assignment 独立 `access_uuid`；Xray email 为 `access:<assignment_id>` |
| 内部隧道 UUID | 面板 | `service_uuid` 只认证入口到出口的内部连接；Xray email 为 `tunnel:<service_uuid>` |
| Reality 密钥对 | **Agent** | 执行 `xray x25519` 生成，私钥不出服务器，public_key 随 `RPC response` 上报 |
| short_id | 面板 | 随模板下发 |
| 端口 | 两者皆可 | 向导中可指定（Agent 检查占用，冲突报错）或留空（Agent 挑空闲端口上报） |
| dest / serverNames | 面板 | 向导表单（带默认值）；留空时由 Agent 按白名单预检自动选择（§6），选定值随 `RPC response` 上报 |

## 8. 用户、链路与凭证

- `users` 只表示真实业务用户、订阅 token、有效期和配额，不创建“虚拟业务用户”。
- `user_chain_assignments` 直接表达用户可用的链。一个用户可同时使用多条链，一条链可供多个用户使用。
- 每个 assignment 生成并稳定保留一个 `access_uuid`。同一用户的多条链即使共享同一入口 `IP:port`，
  Xray 仍可按 email identity 分流并统计到具体用户和链。
- 用户停权、恢复、删除或修改链分配时，Panel 对受影响 Endpoint 发送完整 reconcile。路由规则按 chain
  聚合，而不是每个 assignment 一条规则。
- `user_nodes`、`user.add`、`user.remove` 只保留给历史/独立节点兼容，不承载新链路授权。

## 9. 订阅

`GET /sub/{sub_token}` 按 User-Agent 或 `format` 返回 Mihomo、sing-box、Quantumult X 节点、Quantumult X 完整配置或 base64 分享链接。请求 `Accept` 含 `text/html`（浏览器）时返回订阅落地页。生成、模板缓存、规则制品和原子发布的完整约定见 [订阅分流与模板](subscription-routing-design.md)。

- 目标客户端：Mihomo 内核系、sing-box 和 Quantumult X。原版 Clash 不支持 VLESS+Reality，不在目标范围。
- 内容：Lattix 节点、用户凭据和链路端点填入用户选择的分流策略；共享 Endpoint 的连接地址和端口取入口，凭据取该链 assignment 的 `access_uuid`，地区归属取出口。Mihomo、sing-box 与 Quantumult X 使用各自结构化渲染器，不做订阅格式互转；独立节点仍使用 `users.uuid`。
- 链路命名在创建时解析并固化；服务器资料或自动端口后续变化不自动重命名。默认模板：
  直连 `{{COUNTRY_FLAG}}{{LOCATION}}-Direct`，中转 `{{EXIT.COUNTRY_FLAG}}-Out`。
- 全局变量 `ID/NAME/COUNTRY/COUNTRY_CODE/COUNTRY_FLAG/LOCATION/ADDRESS/TAG[n]` 取客户端实际连接的服务器：
  直连为唯一服务器，中转为入口；另有 `PROTOCOL/PORT/HOPS`。`PORT` 选择自动端口时解析为 `auto`。
  `PANEL_SHORT` 解析为面板缩写（设置页 `panel_short`，默认 `Lattix`），与服务器拓扑无关。
- 拓扑对象 `ENTRY/EXIT/HOP[n]` 代表服务器，支持属性
  `.ID/.NAME/.COUNTRY/.COUNTRY_CODE/.COUNTRY_FLAG/.LOCATION/.ADDRESS/.TAG[n]`；
  `HOP[0] == ENTRY`，`HOP[最后] == EXIT`，数组索引从 0 开始。直连中三者均指唯一服务器。
- 前端输入 `{{` 后提供光标感知的变量补全；前端即时预览，后端执行同一套最终校验。
  未知变量/属性、非法或越界索引、缺失服务器资料、空结果和超长结果均拒绝创建并定位变量。
  名称中使用自动 `PORT` 时仅提示其会解析为 `auto`，不阻止创建。
- **（自 0.0.2 之后迭代，§21）**：引入链后，proxies 条目的 `server`/端口取链**入口**的 address 与 public_port（非 1:1 映射时），public-key/short-id/UUID 取链**出口**；命名中的别名与端口取入口。链 degraded 不剔除入口条目，靠客户端测速规避。
- **跳过原因可见**：发布时收集已分配但未纳入订阅的链的原因（未发布有效修订 / 条目构造失败），持久化在订阅快照 `warnings`（schema v10 新增列），经预览与"重新生成"API 返回，前端以"部分条目未纳入本次订阅"提示，不再静默丢弃。
- **mihomo 开箱即用**：`clash` 格式内置 fake-ip DNS（`enhanced-mode: fake-ip`、198.18.0.1/16、本地域名 filter 与默认/DoH nameserver；节点域名经 `proxy-server-nameserver` 用国内可达解析器直查，不设境外回退，避免节点测速超时）；策略含 GEOSITE/GEOIP 规则时另输出 `geodata-mode` + `geo-auto-update` 与 `geox-url`（MetaCubeX/meta-rules-dat），规则在客户端直接生效。
- **响应头**：`/sub/{token}` 的所有文件格式返回 `subscription-userinfo`，包含 upload、download 以及可选 total、expire、reset_day、plan_name、app_url；同时返回 `profile-update-interval`。
- **用户有效期**：创建或更新用户可带 `expires_at`；过去时间返回 `HTTP 200 + INVALID_ARGUMENT`。`POST /api/user/update` 可修改或清除（null = 长期；省略字段保持不变）。列表 DTO 带 `expires_at`/`expired`/`disabled`。backend sweeper（1 分钟周期，`LATTIX_EXPIRY_SWEEP_INTERVAL` 可覆盖）：`expires_at` 已过且 `expired=0` → 置 1 → 对其已分配节点所在服务器扇出 `user.remove`（显式 nodes 载荷；已 disabled 的用户只补记标记不重复扇出）；管理员延长/清除有效期（expired 1→0）→ 扇出 `user.add` 恢复（disabled 用户除外，见 §16 有效停权态）。过期用户订阅照常返回但 proxies 为空（links 同样空），userinfo 头保留 expire；`node.apply` 的 `NodeUserUUIDs` 不下发 expired/disabled 用户。
- 分享链接集合已实现：`GET /sub/{token}?format=links`（§14，仅含分配的 active 节点）。
- **落地页客户端下载**：面板代取 GitHub Release 客户端安装包并校验发布方 SHA-256
  （缓存 72 小时、流式上限 512 MiB，校验失败即失败并删件）；落地页经
  `/api/sub/{token}/client-download/{start,status,ticket,file}` 四端点获取 3 小时
  会话票据直链，交给浏览器原生下载（`http.ServeFile` 天然支持 Range 断点续传）。
  票据与订阅 token、任务双向绑定，过期或跨订阅使用返回 403；状态端点回显校验值
  与来源供用户独立核验。
- **外部订阅（导入 + 用户关联）**：管理页导入第三方订阅 URL，节点与订阅信息独立落库
  （`external_chains` / `external_subscriptions`，不进入 `chains` 状态机与流量统计），
  用户以「一个外部订阅」为单位引入订阅输出。详见下方小节。

### 9.1 外部订阅导入与用户关联

- **导入**：支持三种格式（base64 分享链接、Clash/mihomo YAML `proxies:`、v2rayN 自定义
  base64 JSON 条目）与 12 种协议（vless/vmess/ss/ssr/trojan/hysteria2/tuic/wireguard/
  anytls/snell/socks/http），统一解析为标准化 JSON 存入 `external_chains`
  （`config_sha256` 去重）；订阅记录 `external_subscriptions` 保存
  `subscription-userinfo` 流量（upload/download/total/expire，浮点取整、expire=0 忽略）、
  节点数、识别到的格式与最近同步状态。
- **拉取安全**：URL 仅允许 https，拒绝 localhost / 内网 / CGNAT（100.64/10）/
  TEST-NET 等保留段地址（防 SSRF）；每订阅可配自定义 UA（默认 clash-meta/2.4.0，
  未拿到流量头时自动以 clash UA 重试一次）与跳过证书校验开关；30s 超时、2 MiB 上限。
- **同步**：创建即同步、手动按钮与定时任务三入口；每订阅可配自动同步开关与更新间隔
  （`UpdateIntervalHours`，最小 1 小时、默认 24 小时），调度任务
  `external_subscriptions.sync` 每 15 分钟扫描到期订阅；重同步为事务内全量替换节点
  （事务内校验订阅仍存在，防删除竞态）；失败保留记录并写 `last_error`，可重试。
  同步成功的订阅自动重发布所有关联用户。前端外部订阅页提供列表、增删改、同步与节点查看。
- **用户关联**：`user_external_subscriptions` 表（user × subscription + 模式），三模式：
  **叠加（stack）** total/used 都并入面板池、**并入（merge）** 仅 used 并入（total 不变）、
  **附加（nodes）** 仅引入节点（流量单独展示）；`total=0` 视为额度未知不参与合并。
  合并为读取时实时计算（`extsub.MergeUserTraffic`，纯函数），不物化缓存表；
  `subscription-userinfo` 头与 `/api/sub/{token}/info` 均按合并值发出（头不封底，
  仅前端剩余显示 0 封底；`reset_day` 不合并）。
- **渲染**：订阅快照管线扩展 `proxyItem.external`，为外部节点走独立构建器
  （mihomo YAML / sing-box JSON / Quantumult X / base64 分享链接，与解析器互逆）；
  客户端格式无法表达的协议跳过并记入快照 warning。外部订阅同步成功后自动重发布
  所有关联用户（`UsersByExternalSubscriptionID` + 异步重生成队列）。

## 10. 面板页面与 API

页面：登录 / 仪表盘（服务器数、在线数、链路数、正常/降级数、用户数）/ 服务器列表（创建和编辑时填写别名、Country、Location、机器类型、公网地址、NAT 可用端口、Tag 与 xray 版本）/ **链路页** / 用户列表 / **外部订阅页**（第三方订阅导入、同步与节点查看，§9.1）/ **分组页**（链路分组与用户分组，§22）/ **订阅模板页**（分流模板管理与分配，见[订阅分流与模板](subscription-routing-design.md)）/ **成本统计页**（已生效成本 + 计算成本两个口径，见[后端调度与服务器计费设计](billing-scheduler-design.md)）/ **运行监控页** / **日志页**（操作日志与请求日志，见[日志系统设计](logging-design.md)）/ **设置页**。

链路页是唯一的产品入口，不保留 `/nodes` 页面或重定向。创建时先选择：

- **直连**：一台服务器即完整链路，拓扑卡片只显示 `直连 <服务器>:<端口>`，不标入口/出口。
- **中转**：显示 `入口 → 中转（0-2 台）→ 出口`；原 role `middle` 的用户文案统一为“中转”。
- 两类链路混排为同一种卡片，顶部统一显示名称、类型、整体状态、创建时间与业务流量；
  中转内部出口业务 node 不作为直连重复展示。状态文案统一为部署中/正常/异常/降级。
- 创建、重试与删除按钮统一使用“链路”语义；直连调用 node 流程，中转调用 chain 编排。
- 直连支持 vless/socks/http/dokodemo-door；中转出口支持 vless/socks/http。
- 用户页统一称“分配链路”，中转选择项内部仍映射其出口业务 `node_id`。

管理 API 走 HTTP + session（账号密码登录）；Agent 通道走 token（§5）。产品层合并不新增第三套统一 API：
直连使用 `/api/node/*`，中转使用 `/api/chain/*`，前端聚合展示。

**已知问题**：离线城市建议来自 `country-state-city` 本地数据，按需加载后数据块约 8 MB
（gzip 约 2.3 MB）；不影响首屏，但首次打开服务器国家/城市控件会产生额外加载。城市名称以数据集原文为主，
Location 允许自由输入作为兜底。后续可按国家拆分数据文件以降低单次加载量。

## 11. 服务器引导流程

1. 面板"添加服务器"填写别名、公网地址与 **xray 版本**（默认 `latest`，也可指定具体版本），生成一次性 **bootstrap token** 与一行安装命令（自 0.0.2 之后迭代起另含机器类型与 NAT 可用端口段，§21；均为面板侧元数据，不下发到 agent，引导流程不变）；
2. 面板生成的命令调用仓库根 `install.sh agent --version <面板版本>`。根入口从该
   Git tag 加载 `scripts/install-agent.sh`，后者按创建时指定的 xray 版本安装
   （`latest` 经 GitHub API 解析并校验官方 `.dgst`）→ 从同版本 GitHub Release
   下载并校验 `lattix-agent-linux-<arch>.tar.gz` → 安装 Agent/`latx-ag` →
   best-effort 开启 TCP BBR → 写入面板地址与 bootstrap token。面板不托管安装脚本或
   二进制资源，也不提供资源源切换。无 root（或无 systemd）时仍可进入用户态守护模式。
   system 模式文件集中在 `/opt/lattix-agent/{bin,config,data,logs}`，用户模式集中在
   `~/.lattix-agent/{bin,config,data,logs}`。重装保留 state/settings，由结构化 token 的
   panel instance ID 与 credential epoch 决定使用长期 token 或新 bootstrap；
   - BBR 已启用时不改系统配置；否则尝试加载 `tcp_bbr`，写入独立持久化文件
     `/etc/sysctl.d/99-lattix-bbr.conf`，并以 `sysctl -w` 只即时设置
     `net.core.default_qdisc=fq` 与 `net.ipv4.tcp_congestion_control=bbr`，不得执行
     `sysctl --system`；
   - `fq` 为 best-effort，最终只以 TCP 拥塞算法是否为 `bbr` 判定成功。Podman/LXC 不按
     容器类型跳过，而按实际内核能力和权限尝试；安装器不自动调用 sudo；
   - BBR 未生效或持久化失败不得中止安装。全部 Agent 安装输出完成后仅追加一行包含
     具体失败原因的 `WARNING`；Agent 卸载不撤销机器级 BBR 配置；
3. Agent 启动首连，以 bootstrap token 换发长期服务器 token；**实际安装的 xray 版本随 `agent.session.open` 上报，面板服务器列表展示实际版本号**。

凭证刷新（§10）立即递增 credential epoch、撤销旧 token 并以 WS 4001 关闭现有连接。
Agent 收到该明确拒绝后停止自动连接；管理员必须执行新的安装命令。同面板刷新保留 Xray
配置，跨面板仅在新面板认证成功后清理旧受管状态。

## 12. 安全

- 面板 TLS 已实现（§14 已知问题关闭），四种部署形态：
  - **自带证书**：`-tls-cert`/`-tls-key` 启动参数指定证书与私钥（须为受信 CA 签发，agent 走系统 CA 校验 wss）；
  - **ACME 自动证书**：`-tls-acme-domain`（Let's Encrypt，TLS-ALPN-01 挑战，仅需 443 公网可达，无需 80 端口），缓存目录 `-tls-acme-cache`；
  - **域名路径模式**（设置页 `tls_mode=path`）：按域名从 `-tls-dir`（安装器统一为数据目录下 `certs/`）读取 `<域名>/fullchain.pem` 与 `privkey.pem`（certbot 风格）；外部 ACME（如 `latx cert`）续期替换文件后下一次 TLS 握手自动加载新证书，免重启（加载失败回退已缓存证书，握手不中断）；
  - **反向代理终止 TLS**（推荐生产形态，如 docker + openresty/nginx 管理证书）：面板保持 HTTP，
    反代整个站点并为 `/api/agent/ws` 转发 Upgrade/Connection 头，
    并以 `-public-url https://域名` 或 `X-Forwarded-Proto: https` 告知面板生成 https 链接。
- HTTPS（含反代）下会话 cookie 带 `Secure`；安装命令/订阅链接按上述推断生成 `https://`/`wss://`。
- agent 与面板 Release 二进制的完整性由 `checksums.txt` 的 **SHA256 校验**保障；
  安装实现从对应 Git tag 经 Raw GitHub HTTPS 加载，二进制只从 GitHub Release 获取。
- Reality 私钥永不出服务器（§7）；VLESS Encryption 的 decryption 私钥侧同理（§15）。
- Agent 能力面收敛：只执行 xray 配置落地、服务重启、状态上报、自卸载，不接受任意命令。

## 13. 遥测（已实现）

Agent 以 `telemetry` 消息周期上报（默认 60s，由面板 Agent 设置统一调整）：

- **xray 版本与运行状态**：升级管理（§18）完成后据此刷新面板展示。
- **主机指标**：采集 CPU、1/5/15 分钟负载、内存、根文件系统、系统 uptime、默认路由出口网卡实时速率与开机累计量，以及 Agent→Panel WebSocket RTT。`server_metrics` 保存最新值，`server_metric_history` 保留最近 24 小时；服务器卡片展示核心摘要，详情抽屉展示完整信息和趋势。主机网卡流量与下述 Xray 业务流量完全独立。
- **流量统计（仅统计，不做强制配额）**：骨架配置启用 xray stats（inbound 级 + 用户级 policy），
  用户条目带 `level: 0`；Agent 经 gRPC StatsService 拉取包含 `xray_instance_id` 的进程级绝对计数器，
  Backend 持久化游标并计算增量。相同实例重连可补齐丢帧和控制通道离线期间的累计量，重复快照
  增量为零；实例变化按新计数器起点处理。增量写入 `traffic`（节点与用户维度）以及链路/逐跳统计。
  Agent 启动时确保 config.json 含 stats/policy 配置（缺失则落盘重启）。
  socks/http 的 accounts 无 email，仅覆盖节点维度。出口 service inbound 是链路权威流量；入口和
  中间 hop 分别用于对账和展示。离线补差的 Xray 重启丢失窗口、日桶归属及倍率边界见
  [链路 Revision 与流量统计设计](chain-revisions-traffic-design.md#控制通道离线时的补报)。

在线/离线由 WS 连接推导，连接存亡由 §2 的 Agent 主动心跳判定（30s Ping，90s 无流量判死）。

## 14. MVP 已知问题与后续迭代目标

| 项 | 说明 |
|---|---|
| 面板 TLS | ✅ 已实现（见 §12）：自带证书 / ACME 自动证书 / 反代终止 TLS |
| 配置漂移 reconcile | ✅ 已实现（见 §17）：检测上报 + 管理员修复按钮 |
| 流量统计与配额 | ✅ 已实现（见 §13）：仅统计；强制配额未做 |
| 主机遥测 | ✅ 已实现（见 §13） |
| 逐节点用户分配 | ✅ 已实现（见 §16）：默认全关，用户勾选节点 |
| 全协议向导 | ✅ 已实现（见 §15）：xray 全部 inbound 协议 |
| `vless://` 链接订阅 | ✅ 已实现：GET /sub/{token}?format=links 返回 base64 链接集合（vless/trojan/vmess/ss） |
| 订阅二维码 | ✅ 已实现：用户列表扫码导入 |
| xray 版本升级管理 | ✅ 已实现（见 §18） |
| 事件告警与 SQLite 备份 | ✅ 已实现（见 §19）：Webhook + Telegram，VACUUM INTO 下载 |
| 多管理员 / RBAC | 决策不做：单管理员符合规模假设 |
| fallback 传输实现 | 决策不做：WS 通道全绿，无实际需求；requester 接口已隔离，需要时再实现 |
| NAT / 中继链路 | ✅ 已实现（v0.0.3，见 §21） |

## 15. 全协议向导（已实现，超出 MVP 范围）

协议范围：xray 全部 inbound 协议——vless / vmess / trojan / shadowsocks / socks / http / dokodemo-door。

决策：

- **安全层**：能走 Reality 的全走 Reality，不引入证书管理。Reality 仅与 RAW(tcp)/gRPC/XHTTP
  三种传输组合（xray 官方约束）；ws/h2/kcp/httpupgrade 不开放。
- **deprecated 屏蔽**：xray 已将 vmess / trojan / shadowsocks（全部用法）及 gRPC 传输标记为
  deprecated（推荐 VLESS + XHTTP / VLESS Encryption）。当前向导不再提供这些协议与 gRPC 传输。
- **VLESS Encryption**：vless 可选 X25519 / ML-KEM-768（后量子，推荐）认证，
  由 Agent 执行 `xray vlessenc` 生成 decryption/encryption 对——decryption 填入模板（私钥不出服务器），
  encryption 客户端字符串随 realized_config 上报，订阅输出 mihomo `encryption` 字段。
  可与 flow vision 组合（native 拼接），组合时客户端字符串按 1-RTT 下发。
- **用户凭证**：复用用户 UUID——vless/vmess 作 `id`，trojan 作 `password`，ss 作密码
  （2022-blake3 定长密钥由 UUID 确定性派生：aes-128-gcm 取 UUID 原始 16 字节，
  aes-256-gcm/chacha20 取 sha256(UUID)），socks/http 用户名与密码均为 UUID。
- **VLESS 详细选项**：flow（vision/无，vision 仅 tcp）、network（tcp/xhttp 及各自子选项）、
  uTLS 指纹（纯订阅侧参数）均以表单开放。
- **热操作**：user.add/user.remove 扇出载荷按服务器携带各节点协议参数（`AddUserPayload.Nodes`），
  Agent 按协议构造 account；ss/socks/http 热增删不支持时由既有"热操作失败 → 重启 → 回滚"流水线兜底。
- **订阅**：按协议生成 mihomo 代理项（vless/vmess/trojan 带 reality-opts，ss/socks5/http 标准类型）；
  节点命名优先使用创建时解析的名称模板，空名称回退 `{别名}-{协议}-{端口}`；
  dokodemo-door 为端口转发，不进订阅。

已知问题（在 §14 表格基础上新增）：

| 项 | 说明 |
|---|---|
| trojan/vmess over Reality | 非标准 TLS 证书组合，依赖客户端 reality 支持（mihomo 系可用），非 Reality 客户端不可用 |
| socks/http 明文 | 仅账密认证无加密，不应暴露于不可信网络 |
| ss 无 Reality | 仅 AEAD 加密 |
| xray 版本兼容 | 新版 xray-core 已移除 vmess alterId（服务端），mihomo 客户端订阅仍携带 `alterId: 0` |
| fallbacks 等高级选项未开放 | vless/trojan 的 fallbacks、xhttp extra 参数等暂不支持 |

## 16. 逐链路用户分配（底层仍为 user_nodes）

替代 §8 的全对全隐含关系：`user_nodes` 关联表，**默认全关**——新建用户/节点无任何关联，
管理员在用户列表“分配链路”对话框勾选（`POST /api/user/set-nodes` 整体替换）。
创建用户时可直接预选链路对应的业务 `node_id`，省略则维持默认全关。

- **扇出语义**：node.apply 只携带分配到该节点的用户 UUID；分配变更按差量扇出
  user.add/user.remove（载荷仅含受影响节点）；删除用户仍向所有服务器幂等扇出 user.remove。
  （自 0.0.2 之后迭代，§21：引入链后 `user_nodes` 指向链的**出口**节点——UUID 只存在于出口 xray，入口/中间跳不接收用户扇出。）
- **订阅**：YAML 与 links 端点均只含分配到该用户的 active 节点。
- **显式停用/启用开关**：`users.disabled`（`POST /api/user/update` 带 `disabled` 字段，
  用户列表行内"停用/启用"按钮）。停用 → 对其已分配节点扇出 user.remove；启用 → 扇出 user.add 恢复。
  disabled 与 expired（§9）正交，**有效停权态 = disabled OR expired**：user.add/user.remove 扇出只在
  有效停权态跃迁时发生（已 expired 再 disable 不重复扇出；恢复需两者都解除）；有效停权态下
  订阅 YAML/links proxies 为空、userinfo 头照常、落地页显示"已停用"，`NodeUserUUIDs` 不下发。
- Agent 侧无新增状态：`AddUserPayload.Nodes`（tag → 协议参数）驱动精确落点；
  `Nodes` 必须明确给出，不存在“作用于全部节点”的隐式语义。

## 17. 配置漂移 reconcile（已实现，超出 MVP 范围）

检测上报 + 管理员修复：

- Agent 每次落盘记录配置哈希，按面板 Agent 设置中的间隔周期比对（默认 15s）；
  外部篡改/删除即上报 `config.drift`（仅状态变化时），回滚路径同步基线避免误报。
- 面板置 `servers.config_drift` 标志，服务器列表显示"配置漂移"徽章与"修复漂移"按钮。
- 修复 = `POST /api/server/repair` 重放该服务器全部 active 节点；
  Agent 检出漂移后的下一次落盘以"骨架 + 受管节点 inbound"净化配置为基，
  外部对非节点段（log/routing 等）的改动一并还原，漂移标志自动清除。
- 基线说明：agent 停机期间的外部改动无法区分，以启动时文件为基线。

## 18. xray 版本升级管理（已实现，超出 MVP 范围）

面板服务器列表"升级 xray"（`POST /api/server/upgrade-xray`，版本为 `latest` 或 `vX.Y.Z`）→
`xray.upgrade` 命令下发（离线留队列补发）→ Agent：

1. `latest` 经 GitHub API 解析（与 install.sh 同款）；设置 `-xray-release-base` 镜像基址时跳过 API，直接走 release 的 `latest/download` 重定向约定下载，实际版本由步骤 3 校验与 telemetry 上报；
2. 下载官方 release 包与 `.dgst`，校验 SHA2-256（获取不到校验文件即失败）；
3. 备份旧二进制（`.bak`）→ 原子替换 → 重启 → 校验实际版本；任一步失败回滚并重启；
4. 成功后新版本经 telemetry（§13）刷新面板展示。

下载基址可用 `-xray-release-base` 指向镜像/代理（官方 GitHub 不可达的被控机场景）。

## 19. 事件告警与备份（已实现，超出 MVP 范围）

单人运维的离线通知与数据兜底。

**事件告警**（设置页"告警"区块，三项全空 = 关闭）：

- 触发仅状态跃迁，不周期重发：
  - `server_offline`：WS 断开导致 online→offline（hub unregister 实际移除注册连接为唯一挂点；
    被新连接顶替的旧连接注销不算，session 重连不重复报；断连后经消音窗口（默认 10s，
    `LATTIX_OFFLINE_DEBOUNCE` 可覆盖）仍离线才触发，窗口内重连则静默）；
  - `config_drift`：dispatcher 收到 `config.drift` true（§17）；
  - `node_failed`：RPC response 失败或命令死信导致节点置 failed（§6）。
- 通道（各自独立判定，异步发送不阻塞主路径，5s 超时，失败仅记日志）：
  - Webhook：POST JSON `{"event","server","node","detail","time"}` 到 `alert_webhook_url`；
  - Telegram：Bot API `sendMessage` 纯文本，需 `alert_telegram_bot_token` +
    `alert_telegram_chat_id` 同时具备（token 与 tls key 同风格：不回显，仅给置位标记）。
- 防抖动：同一服务器同一事件 5 分钟内不重复发（内存 map 记上次发送时间，重启清零可接受；
  `LATTIX_ALERT_DEBOUNCE` 可覆盖窗口，dev/e2e 用）。
- `POST /api/setting/test-alerts`：按已保存配置向两通道各发一条测试消息，返回各通道成败。

**SQLite 备份**：`GET /api/backup/download`（session 鉴权）`VACUUM INTO` 到临时文件后以
`lattix-backup-<YYYYMMDD-HHMMSS>.db` 附件返回，发送完成清理临时文件；单连接 +
busy_timeout 下与并发读写安全共存，失败 500。设置页"面板维护"区块提供下载按钮。

## 20. 面板侧引导与 latx 运维程序（已实现，超出 MVP 范围）

面板自身的安装/运维与 agent 引导（§11）同形态：release 钉版脚本 + checksums 校验 +
单文件 bash 管理程序。

**统一安装入口**：用户只执行仓库根 `install.sh`。无参数时直接进入面板的
Docker/原生模式向导；面板自动化安装使用 `panel` 子命令。`agent` 子命令仅供面板
"添加服务器"生成的安装命令调用，不作为用户自行触发的安装入口。版本默认取最新稳定
Release，显式 `--version` 可钉版。根入口再从对应 Git tag 加载
`scripts/install-panel.sh` 或 `scripts/install-agent.sh`；安装脚本不作为 Release 资产。

**安装参数**：Docker 与原生模式的交互向导均允许设置部署地址/端口、管理员账号密码和
配置目录，各项留空时采用对应模式的默认值（重装时优先复用已有配置）。非交互安装使用
对应参数，其中 `--config-dir` 指定 Compose/程序、配置和持久数据所在的宿主机根目录。

- 发布 `linux/amd64`、`linux/arm64` 的公开 GHCR 镜像
  `ghcr.io/beanya/lattix:<version>`，同时更新 `latest`；镜像为非 root 单进程，
  React 产物嵌入 Go 二进制，容器内没有 Nginx；
- 安装器只创建 `/opt/lattix-panel/{compose.yaml,config/.env,data/}` 并启动 Compose。
  默认映射 `127.0.0.1:8080:8080`，数据、证书、ACME 缓存分别持久化到 `data/`、
  `data/certs/`、`data/acme-cache/`；除非用户明确确认安装 Docker，不注册或修改其他
  宿主机服务；
- `.env` 是 Compose 与容器基础参数的唯一来源。管理员密码默认 8 位随机大小写字母；
  重装时优先级为“显式参数 > 已有 `.env` > 默认/随机值”；
- 页面更新与原生模式共用单二进制替换流程。替换容器可写层中的程序后，重启接口令进程退出，
  `restart: unless-stopped` 在同一容器内拉起新版。强制重建容器会恢复 `.env` 所钉镜像版本，
  页面更新不访问 Docker Socket。

**原生模式**：下载并校验当前架构的 panel tarball（单个嵌入前端的
`lattix-backend` + `latx`），默认安装到 `/usr/local/lattix-panel`（可由
`--config-dir` 覆盖）并注册 systemd。
重装先停服，保留 DB、证书、ACME 缓存和管理员配置，再替换二进制并启动。
`LATX_DEV=1` 保留给本地 e2e 的无 systemd 路径。

**latx**（`scripts/latx.sh`，CI stamp 后安装为 `/usr/local/bin/latx`）：全部函数化的
单文件 bash 管理程序，子命令 `status`（服务状态/监听端口/面板版本/面板地址）、
`start|stop|restart|enable|disable`（systemctl 包装，非 root 明确报错）、
`log [-n N]`（journalctl，`-n` 不跟随）、`update [version]`（latest 经 GitHub API
解析，下载当前架构 tarball + checksums.txt 校验后停服替换单个 `lattix-backend`，
启服并校验 `-version`；amd64/arm64）、`acme <domain>`（登录 → `POST /api/setting/update` 保存 tls_mode=acme →
POST restart → 等待恢复并验证 `https://<domain>` 可达，凭据 read -s 或
`LATX_ADMIN_USER`/`LATX_ADMIN_PASS`）、`reset-admin <newpass>`（调
`lattix-backend -reset-admin`，见下）、`uninstall [--purge-db]`（确认后停服删 unit
与安装根目录，默认保留 DB 并提示路径）、`version`。unit 名 `LATX_UNIT`、
面板地址 `LATX_PANEL_URL`（默认 `http://127.0.0.1:8080`）可覆盖。

**latx-ag**（`scripts/latx-ag.sh`，CI stamp 后由 agent install.sh 安装为
`/usr/local/bin/latx-ag`）：节点侧同款单文件 bash 管理程序。子命令 `status`
（agent/xray 服务状态与版本、面板地址——读 install.sh 写入的 env 文件、state 中的
服务器 ID、配置指纹；§17 漂移基线在 agent 内存中不落盘，故仅显示指纹）、
`start|stop|restart|enable|disable`（systemctl 包装，unit `lattix-agent`，
`LATX_AG_UNIT` 覆盖）、`log` / `log-xray`（journalctl，`-n N` 不跟随）、
`update [version]`（latest 经 GitHub API 解析，下载 agent 包
`lattix-agent-linux-<arch>.tar.gz` + checksums.txt 校验 → 解包 →
**预检**新二进制 `-version` → 停服替换 → 启服校验版本）、`xray-update [version]`
（官方 .dgst 校验 SHA2-256，拿不到校验文件即失败，与 agent upgrade.go 同语义；
备份 .bak → 替换 → 重启 → 版本校验，失败回滚；`XRAY_RELEASE_BASE` 对齐
`-xray-release-base` 镜像语义）、`uninstall [--purge-xray]`（确认后卸载；清理清单与
agent 远程自卸载双向对齐：二进制/bak、runner、env/state/settings/connection/
command-queue、lock、日志；purge 时连同 xray 与 APP_ROOT；默认仅 agent，xray
与节点继续运行）、`version`。
latx-ag 随 GitHub Release 的 agent 包 `lattix-agent-linux-<arch>.tar.gz` 分发，
统一经 checksums.txt 校验（§11）。
install.sh 成功输出面板地址 / agent 状态 / xray 版本与 latx-ag 运维提示块。
`LATX_DEV=1` + `LATX_PREFIX` 路径前缀提供与 install-panel.sh 同款的 DEV 降级
（e2e/install-agent.sh 走此路径）。`dev/test-install-agent-bbr.sh` 通过隔离的
sysctl/modprobe/config 替身覆盖 BBR 已启用、`fq` 失败、内核不支持、容器拒绝 sysctl
及单行原因 WARNING，不触碰测试宿主的真实网络参数。

**`-reset-admin` 启动参数**：`lattix-backend -reset-admin <newpass> -db <path>`
与设置页改密同一代码路径（bcrypt 哈希写 `settings`，≥8 位校验，会话签名密钥派生自
密码哈希故改密即全部会话失效，§10），写完输出提示即退出，不启动面板；
面板运行中执行安全（busy_timeout）。

## 21. 代理链与 NAT 支持（已实现，v0.0.3）

推翻 §14 原"NAT / 中继链路不做"的决策。产品概念统一为**链路**：
直连是长度 1 的链路，中转是 `入口 → [中转...] → 出口 → Internet`，客户端仅见入口。

**产品与实现统一**：

- 所有代理入口均落为 `chains`；直连是 1 跳 revision，中转是 2～4 跳 revision。
- 每条 chain 保留内部 `service_node_id/service_uuid`，并引用入口服务器级 `shared_endpoint`。
- 用户授权由 `user_chain_assignments` 表达；订阅使用 assignment 的 `access_uuid`，不再把业务用户
  UUID 下发到出口 service。
- 创建和编辑共用一套 chain API、revision planner 与任务状态机，不再维护直连 node/中转 chain 两套流程。
- 逐跳流量仅用于展示；共享链的用户与链路总量以入口 `access:*` 计数为准，各跳不得相加。
- revision、离线发布、删除、流量倍率和日/月统计的完整契约见
  [链路 Revision 与流量统计设计](chain-revisions-traffic-design.md)。

**能力边界**：

- 链长上限 4 跳，同一服务器在一条链中不重复（O(n) 查重即环检测）。
- 端到端加密下中间跳只做 TCP 拼接，3+ 跳的收益仅为路径混淆，故不开放任意长度。

**数据平面**：

- 每跳规则统一：下游有入站能力 → 直连转发（dokodemo-door 式纯 TCP 透传）；
  下游无入站能力 → 下游以 xray reverse bridge 反向上来（bridge/portal 对）。
- **共享入口终止**：客户端 VLESS+Reality 在入口 Endpoint 终止，入口按 assignment identity 选择 chain；
  多跳时再用 chain 的 `service_uuid` 建立到出口的内部 VLESS+Reality 连接。
- **隧道口安全**：portal = 每跳独立的 VLESS+Reality inbound（不共享，吊销粒度细）；
  密钥对由该跳 agent 生成、随 `RPC response` 上报，私钥不出服务器（沿用 §7 原则）。
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
  管理员可强制发布未确认 revision。已发布 active 链任一 hop 离线时由链路状态机（chainFSM）
  推导为 `degraded`，但不退出订阅；服务器删除经 FSM 校验后使引用链路 `invalid` 并退出订阅。
  状态重算由 Agent 连接状态跃迁、端点 ack、周期自愈三层触发；Panel 重启时 ResumeChains 全量
  恢复，运行时 Agent 重连由 ResumeChainsByServer 即时恢复。详见链路设计文档。

**agent 侧**：

- 无全局 `-nat` 模式开关：角色（bridge+业务 inbound / portal+转发 / 转发）完全由
  apply 载荷决定，控制通道、遥测、漂移 reconcile（§17）、xray 升级（§18）全部复用。
- install.sh 与引导流程不变；机器类型与端口段是面板侧元数据，不下发到 agent
  （NAT 受限直连机经 `port_candidates` 下发段内监听候选：自动端口段内挑选、手动端口段内校验）。

**订阅**：条目 = 入口 Endpoint 的 `address:public_port` + Endpoint 的 public_key/short_id + assignment `access_uuid`；
命名沿用 `{入口别名}-{协议}-{端口}`；links 端点（§9/§14）同构。

**端口复用**：自动选端口只挑空闲端口——独立 IP 机挑随机空闲端口（443 不再是默认/首选），NAT 机
只从可用段内挑选；管理员显式指定端口不回退，VLESS 链指定已被受管监听占用的端口时自动加入共享监听（不再报错），
仅未受管的 OS 占用在部署时报错。Agent 占用探测区分端口归属：端口已被自身
受管 xray 持有的（受管端口）视为可复用，重发/自愈直接沿用已落地端口，不因自身 xray 持有而误判
占用；其他服务占用的端口才报冲突。相同 server/port 上任意 VLESS 链可加入既有 Endpoint 共享监听（跨 profile）：
入口参数以先占用链为准（订阅渲染端点参数保持一致），不同协议仍报冲突。NAT 手动指定端口段内校验（面板校验 + Agent 载荷候选双保险），所有共享链的 entry
forward 改为 loopback 内部口，不再消耗公网映射。

**实施时待定的小项**（不阻塞设计）：向导链路构图的详细校验规则（入口必须有入站能力，出口任意，
中间跳至少一侧可达）。

**PoC 结论（已验证，GO）**：`scripts/dev/poc-reverse.sh` 实证 reverse bridge/portal + 隧道 Reality +
端到端 Reality 透传可行（链路通、portal 重启自愈、隧道口抗探测分流正常）。两个实测要点：
Reality dest 不稳定（如 www.microsoft.com）会导致合法客户端握手一并失败，dest 白名单必须只收稳定目标
（§6 destCandidates 已遵循，隧道 inbound 复用同一白名单）；反向通道注册存在启动竞态，
bridge 首拨失败由 xray 自动重试兜底，编排层无需处理。

### 21.1 实现契约（消息、存储、编排细节）

**每跳的 xray 配置件**（piece，agent 按载荷渲染，全部并入受管 config.json）：

| piece | 所在机 | 内容 |
|---|---|---|
| `forward` | 入口/中间跳 | dokodemo-door 透传 inbound（固定目标，无认证即无滥用面：攻击者只能弹到出口 Reality 口被分流）+ 路由（直连 → freedom 拨下一跳 `address:port`；反向 → reverse portal） |
| `portal` | 反向链的上游机 | VLESS+Reality interconn inbound + reverse portal（密钥对 agent 生成上报，UUID/shortID 面板下发，dest 走 §6 预检+白名单） |
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
hub 注销/注册连接时由 FSM Evaluate 重算——链任一跳 server 离线 → degraded + §19 告警
（新事件 `chain_degraded`，防抖沿用）；全部跳 server 在线且跳均 active → active。
服务器删除经 `InvalidateForServerDeletion` 事务级联失效。Agent 重连后 `ResumeChainsByServer`
恢复编排中的链。完整转换表与三层自愈设计见[链路设计文档](chain-revisions-traffic-design.md)。

## 22. 链路分组与用户分组（已实现，超出 MVP 范围）

分组在用户/链路直接分配语义（§8/§16）之上提供一层编排便携，不引入新状态机：

- **链路分组**（`link_groups` + `link_group_chains` / `link_group_external_subscriptions`）：
  把多条共享入口链路与外部订阅编排为一个组。外部订阅**整体原子参与**——移除即整组
  节点从订阅消失。
- **用户分组**（`user_groups` + `user_group_members` + `user_group_links`）：把用户编组
  并关联一个或多个链路分组。组内用户订阅**由分组派生**：直接分配被遮蔽（数据保留，
  移出分组即恢复），与用户外部订阅引入（§9.1 三模式）叠加生效。
- **触发重发布**：分组自身或相关服务器/外部订阅更新自动触发组内全部用户订阅重发布；
  长操作接入 observe 进度弹窗（§23）。
- 管理端点 `/api/link-group/{list,create,update,delete}` 与
  `/api/user-group/{list,create,update,delete}`；e2e 见 `scripts/e2e/groups.sh`。

## 23. 旁路式操作进度观察（observe，已实现）

长操作在业务校验通过后创建**观察记录**，在流程自然节点上报 stage/percent/message，
前端轮询渲染全局唯一进度弹窗（阶段清单 + 进度条 + 警告框）。观察**严格尽力而为**：
全部方法 nil 安全、panic 恢复，容量满时该操作照常执行但不观察，任何观察故障不影响
业务操作本身。

- **覆盖操作**：链路创建/编辑/强制发布/重试/删除、节点创建/重试/删除、链路分组与
  用户分组 CRUD、用户创建/停权跃迁（停用/启用）/分配与外部订阅变更/订阅设置保存/
  重新生成订阅、订阅模板指派/取消指派（批量用户逐个发布，观察按用户数推进）、
  服务器修复（重放节点）/清理 xray 缓存/重建 xray 配置、外部订阅创建/更新/删除/同步、
  订阅模板刷新。用户删除（无发布）与订阅 token 重置（同步发布 + 失败回滚不变量）
  不观察。
- **信封与查询**：响应信封可选携带 `observe_id`（32 hex，`writeRPC` 注入；openapi
  envelope 已含该可选字段），前端 `postObserved` 提取后以 400ms 间隔轮询
  `GET /api/observe-task/get?observe_id=`；404 进入 `lost` 态（提示"操作可能仍在
  后台继续"），`done` 后 1 秒自动关闭（有警告则保留人工确认）。
- **注册表限制**（进程内、不持久化）：最多 64 个并发 running 观察；终态观察保留
  5 分钟；running 超过 5 分钟被惰性清理并以"进度超时"警告收口。
- **订阅重生成收口**：用户/分组/链路删除与外部订阅同步可 `WatchUsers`，订阅再生器
  每发布一个用户推进百分比，全部发布后自动 Finish——观察存活至订阅重生成完成。
