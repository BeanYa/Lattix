# Lattix 设计文档（MVP 实施契约）

> 本文档是 MVP 阶段的完整项目目标。凡标注"后续迭代"的内容均不属于 MVP 范围，
> 不在 MVP 中做任何为其预留的实现，除非本文明确说明（如 requester 接口）。

## 1. 项目目标

主控面板（Backend）统一管理多台受控服务器（Agent），流程：

1. 管理员在面板通过可视化向导填写某协议在 xray 上的完整配置，生成一份**虚拟配置**；
2. 虚拟配置经控制通道下发到目标服务器的 Agent；
3. Agent 将虚拟配置落地为 xray 实际配置并生效，上报实际配置结果；
4. 面板统一管理所有服务器生成的代理节点（inbound），按节点为用户生成订阅链接。

参考 3x-ui / s-ui / miaomiaowuX 的交互模式，不做商业化。

**规模假设**：服务器 1–30 台，管理员 1 人，实际用户 ≤ 50 人。所有服务器假设具备公网 IP 与完整公网访问能力（NAT 场景见 §14）。

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

- **单通道**：Agent → Backend 一条 WebSocket 长连接承担全部双向通信。Backend 永不主动外连 Agent，Agent 不提供任何监听 API 端点。该形态同时兼容公网服务器与未来的 NAT 服务器。
- **无 fallback 实现**：Backend 侧定义 `requester` 接口隔离"发送命令"与"具体传输"，MVP 只有 WebSocket 一个实现；gRPC/HTTP 等其他实现属后续迭代。
- **离线排队**：Agent 离线期间，发往它的命令滞留于 `commands` 表，重连后补发。
- **重发与死信**：重连时将该服务器 `sent` 未终态的命令重置为 `queued` 重新补发（幂等性由 Agent 各处理器保证）；`attempts` 超过上限（10 次）标记 `failed` 死信，不再重发；`apply_node` 死信时对应节点同步置 `failed` 并记录原因（§6）。

## 3. 仓库结构与技术栈

```
src/frontend/  # Vite + React + TypeScript + shadcn/ui，包管理器使用 bun
src/backend/   # Go：面板 HTTP API + Agent WS 端点 + SQLite
src/agent/     # Go：独立二进制，systemd 托管
src/shared/    # Go module：WS 消息结构体、虚拟配置类型，backend/agent 共用
scripts/
  install.sh   # Agent 引导安装脚本
```

- 前端依赖安装与构建：`bun install` / `bun run build`，锁文件入库。
- 后端与 Agent 均为 Go，通过 Shared module 共享消息定义，保证协议两端类型一致。
- 数据库：SQLite（规模假设下绰绰有余）。
- 版本管理：git，monorepo。**项目初始化第一步即初始化 git 仓库**（`git init`），全部开发在 git 工作流中进行；`.gitignore`、前端锁文件（bun）、go.work 等随首次提交入库。

## 4. 数据模型（SQLite）

| 表 | 字段（要点） |
|---|---|
| `servers` | id, alias, address(公网地址), token(长期凭证), last_seen_at, xray_version, config_drift(§17), created_at |
| `users` | id, name, uuid, sub_token, expires_at(可空，unix 秒，NULL=长期), expired(0/1 到期停权标记，§9), disabled(0/1 显式停用标记，§16), created_at |
| `nodes` | id, server_id, protocol, port, config_template(JSON), realized_config(JSON), status, error, created_at |
| `commands` | id, server_id, type, payload(JSON), status(queued/sent/acked/failed), attempts, created_at, updated_at |
| `user_nodes` | user_id, node_id（§16 逐节点用户分配，默认全关） |
| `server_metrics` | server_id, load1, cpu_percent, mem_total, mem_used, updated_at（§13 主机遥测最新值） |
| `traffic` | node_id, user_uuid, up, down, updated_at（§13 流量累计：节点维度 user_uuid=''，用户维度 node_id=0） |

说明：

- `commands` 表同时充当**离线命令队列**与**操作日志**（全部保留，不自动清理；重发/死信语义见 §2）。
- `nodes.config_template` 是面板侧虚拟配置（含占位符）；`nodes.realized_config` 是 Agent 上报的实际生效值（端口、public_key、short_id 等）。
- `servers.address` 是订阅中节点地址的唯一来源（§9）：**创建服务器时由管理员填写公网地址，agent 不校验**；留空则按 agent WS 拨入的 RemoteAddr 自动学习，一经写入不再被覆盖（地址变更由管理员修改，PATCH /api/servers/{id}）。
- 用户-节点关联（§16）：`user_nodes` 引入前成员关系隐含为"全部用户 ∈ 全部节点"（§8），经 `PRAGMA user_version` 一次性迁移补全存量关联；此后新建用户/节点默认全关。

## 5. 控制通道协议

连接：Agent 携带服务器 token 拨出至 `Backend /api/agent/ws`。

信封：JSON，`{id, type, payload}`，`id` 用于请求/响应关联。

消息类型：

| type | 方向 | 说明 |
|---|---|---|
| `hello` | agent→panel | 首连认证：token、agent 版本、xray 版本、xray 运行状态；bootstrap token 在此换发长期凭证（以 `last_seen_at` 为空判定 bootstrap 状态；长期 token 一经换发**不再轮换**，agent 侧内存兜底防止落盘失败锁死） |
| `apply_node` | panel→agent | 下发节点：虚拟配置模板 + 分配到该节点的用户 UUID 列表（§16） |
| `remove_node` | panel→agent | 删除节点 |
| `add_user` | panel→agent | 向载荷指定节点的 inbound 热加入一个用户（`nodes` 参数携带各节点协议参数；必填，缺省/为空回执错误） |
| `remove_user` | panel→agent | 从载荷指定节点的 inbound 热移除一个用户（`nodes` 必填，同 add_user） |
| `apply_result` | agent→panel | 上报执行结果：成功返回 realized_config，失败返回 error |
| `uninstall` | panel→agent | 卸载 agent：`purge_xray=true` 时连同 install.sh 安装的 xray 与配置一并清除，`false` 时仅移除 agent（xray 及节点继续运行）；agent 先回执再自毁 |
| `upgrade_xray` | panel→agent | 升级 xray 到指定版本（§18）：下载官方 release 校验 .dgst 后替换重启，失败回滚 |
| `telemetry` | agent→panel | 周期遥测（§13）：xray 版本/运行状态、主机指标、流量增量；无需回执 |
| `drift_report` | agent→panel | 配置漂移状态变化（§17）：外部修改时 true，修复/恢复后 false |

在线/离线状态由 WS 连接推导，连接存亡由应用层心跳判定：panel 每 30s 向 agent 发 WS ping，任一侧 90s 无任何字节（含 pong 等控制帧）即判连接死亡——panel 侧注销连接并按离线处理（显示离线 + sent 命令重置），agent 侧读循环报错退出后 5s 退避重连。半开 TCP 最长 90s 被识别，不会假在线。

## 6. 节点生命周期与 apply 流水线

节点状态机：`pending → applying → active | failed`。`failed` 携带错误详情，面板提供重试按钮。

Agent 收到 `apply_node` 后的落地流水线（顺序固定）：

1. 填充模板占位符（见 §7）；
2. **dest 预检**：对模板 dest 做 TCP+TLS 可达性检查；不可达则按 `apply_node` 携带的白名单候选（`dest_candidates`，面板内置并随版本更新，尽量丰富）逐个尝试，全部失败则上报 error；
3. 写入临时配置文件；
4. `xray run -test -config <file>` 校验，失败则丢弃并上报 error；
5. 落盘正式配置（Agent **独占管理** `/usr/local/etc/xray/config.json`）；
6. 调 xray gRPC API 热操作（`AddInbound` / `AlterInbound` / `RemoveInbound`）；
7. 热操作失败才 `systemctl restart xray`；
8. 重启失败则恢复上一份可用配置、再次重启，并上报 failed。

参考依据：3x-ui/x-ui 系通过 xray gRPC API 的 `AlterInbound`（AddUser/RemoveUserOperation）实现增删用户零重启；XrayR/V2bX 类商业化节点侧通过进程内重载 xray-core 达成同等效果。MVP 采用前者为主路径、重启为兜底。

## 7. 虚拟配置与参数分工

虚拟配置 = **xray inbound JSON 模板 + 占位符**，Agent 填值后**原样写入**，不存在任何"翻译层"。

| 参数 | 生成方 | 说明 |
|---|---|---|
| 用户 UUID | 面板 | 同一用户跨所有服务器使用同一 UUID（VLESS client `id` 必填） |
| Reality 密钥对 | **Agent** | 执行 `xray x25519` 生成，私钥不出服务器，public_key 随 `apply_result` 上报 |
| short_id | 面板 | 随模板下发 |
| 端口 | 两者皆可 | 向导中可指定（Agent 检查占用，冲突报错）或留空（Agent 挑空闲端口上报） |
| dest / serverNames | 面板 | 向导表单（带默认值）；留空时由 Agent 按白名单预检自动选择（§6），选定值随 `apply_result` 上报 |

## 8. 用户-节点模型（扇出语义）

- 单管理员；多用户，每个用户一个独立 UUID、一个独立 `sub_token`。
- MVP：每个用户是**每个节点**的 client（隐含全对全关系）。
  - 新建节点 → `apply_node` 携带当前全量用户列表一次性下发；
  - 新建用户 → 向所有在线服务器 `add_user` 扇出，离线服务器留 `commands` 队列补发；
  - 删除用户 → `remove_user` 扇出。
- 逐节点的用户分配（n 个链接 ↔ 不定个节点）属后续迭代。

## 9. 订阅

`GET /sub/{sub_token}` → 返回 **mihomo（Clash.Meta）格式 YAML**；请求 `Accept` 含 `text/html`（浏览器）时改为返回**订阅落地页**（自包含 HTML，不依赖前端构建产物与任何 CDN/外网资源，token 即鉴权，无效 404）：已用流量 ↑/↓、有效期（或"长期"）、节点数、YAML/links 订阅地址复制按钮、订阅地址二维码（内嵌 qrcode-generator，MIT）、mihomo 系一键导入（`clash://` / `mihomo://install-config?url=`）；已到期用户显示"已到期"，被停用用户显示"已停用"（§16）。`GET /sub/{sub_token}/links`（§14）不分流。

- 目标客户端：mihomo 内核系（Clash Verge / Clash Party / FlClash 等）。原版 Clash 不支持 VLESS+Reality，不在目标范围。
- 内容：proxies 列表（每个节点一项，`type: vless`，`server` 取 `servers.address`（§4），嵌入**该用户自己的 UUID**、`flow`、`reality-opts: {public-key, short-id}`、`servername`、`udp: true`）+ 一个 `select` 类型 proxy-group + `MATCH` 规则。
- 节点命名：`{服务器别名}-vless-{端口}`，如 `tokyo01-vless-8443`。
- **响应头**：`/sub/{token}` 与 `/sub/{token}/links` 均返回 `subscription-userinfo: upload=<bytes>; download=<bytes>[; expire=<unix秒>]`（upload/download 取 `traffic` 表用户维度 node_id=0 的跨服务器累计；仅设了有效期才带 expire；无流量配额故无 total）与 `profile-update-interval: 24`（客户端按天自动刷新）。
- **用户有效期**：创建用户可带 `expires_at`（过去时间 → 400）；`PATCH /api/users/{id}` 修改/清除（null = 长期，过去时间同样 400——"借到期立即停权"由 §16 的 disabled 开关承担；省略的字段保持不变）；列表 DTO 带 `expires_at`/`expired`/`disabled`。backend sweeper（1 分钟周期，`LATTIX_EXPIRY_SWEEP_INTERVAL` 可覆盖）：`expires_at` 已过且 `expired=0` → 置 1 → 对其已分配节点所在服务器扇出 `remove_user`（显式 nodes 载荷；已 disabled 的用户只补记标记不重复扇出）；管理员延长/清除有效期（expired 1→0）→ 扇出 `add_user` 恢复（disabled 用户除外，见 §16 有效停权态）。过期用户订阅照常返回但 proxies 为空（links 同样空），userinfo 头保留 expire；`apply_node` 的 `NodeUserUUIDs` 不下发 expired/disabled 用户。
- `vless://` 分享链接集合端点已实现：`GET /sub/{token}/links`（§14，仅含分配的 active 节点）。

## 10. 面板页面与 API

页面：登录 / 仪表盘（服务器数、在线数、节点数、用户数）/ 服务器列表（"添加服务器"填写别名、**公网地址**（留空自动学习，§4）与 **xray 版本**（默认 `latest`，§11），生成一行安装命令；可删除服务器——在线 agent 收到 `uninstall` 自卸载，**删除时可选"仅 agent"或"连同 xray"**（§5），离线仅删记录；可刷新凭证重取安装命令——已安装的换发后旧凭证失效，未安装的换发新 bootstrap token）/ 节点创建向导（选服务器 → VLESS+Reality 表单，端口可空 = 自动）/ 用户列表（创建用户 → 展示并复制订阅链接）。

管理 API 走 HTTP + session（账号密码登录）；Agent 通道走 token（§5）。

## 11. 服务器引导流程

1. 面板"添加服务器"填写别名、公网地址与 **xray 版本**（默认 `latest`，也可指定具体版本），生成一次性 **bootstrap token** 与一行安装命令；
2. `install.sh` 在被控机执行：按创建时指定的 xray 版本安装（`latest` 在执行时经 GitHub API 解析最新 release；**校验官方 release checksums**）→ 下载/安装 Agent 二进制 → 注册 systemd → 写入面板地址与 bootstrap token（重装时清除旧 state 文件，确保使用新 bootstrap token）。Agent 二进制两种获取方式：**release 钉版模式**（正式版本，install.sh 由 CI 烧入版本号，agent 与校验和取自同版本 GitHub release 资产，校验 `checksums.txt`）与**面板托管模式**（dev 构建，agent 从面板 `/dist` 下载，**按面板注入的 SHA256 校验**，明文 HTTP 下保证二进制不被中间人替换）；
3. Agent 启动首连，以 bootstrap token 换发长期服务器 token；**实际安装的 xray 版本随 hello 上报，面板服务器列表展示实际版本号**。

凭证刷新（§10）将 `last_seen_at` 重置为空，使服务器回到 bootstrap 状态，下次 hello 重新换发。

## 12. 安全

- 面板 TLS 已实现（§14 已知问题关闭），三种部署形态：
  - **自带证书**：`-tls-cert`/`-tls-key` 启动参数指定证书与私钥（须为受信 CA 签发，agent 走系统 CA 校验 wss）；
  - **ACME 自动证书**：`-tls-acme-domain`（Let's Encrypt，TLS-ALPN-01 挑战，仅需 443 公网可达，无需 80 端口），缓存目录 `-tls-acme-cache`；
  - **反向代理终止 TLS**（推荐生产形态，如 docker + openresty/nginx 管理证书）：面板保持 HTTP，
    反代转发 `/`、`/api/agent/ws`（带 Upgrade 头）、`/sub`、`/install.sh`、`/dist`，
    并以 `-public-url https://域名` 或 `X-Forwarded-Proto: https` 告知面板生成 https 链接。
- HTTPS（含反代）下会话 cookie 带 `Secure`；安装命令/订阅链接按上述推断生成 `https://`/`wss://`。
- install 通道（install.sh、agent 二进制）的完整性由 **SHA256 校验**保障（§11）：release 钉版模式下信任锚定在 GitHub release 资产与 `checksums.txt`（HTTPS 投递）；面板托管模式在 HTTP 部署下明文传输，校验和由面板注入脚本，等价于把信任锚定在 install.sh 的投递路径上，HTTPS 部署下由 TLS 保障。
- Reality 私钥永不出服务器（§7）；VLESS Encryption 的 decryption 私钥侧同理（§15）。
- Agent 能力面收敛：只执行 xray 配置落地、服务重启、状态上报、自卸载，不接受任意命令。

## 13. 遥测（已实现）

Agent 以 `telemetry` 消息周期上报（默认 60s，`-telemetry-interval` 可调）：

- **xray 版本与运行状态**：升级管理（§18）完成后据此刷新面板展示。
- **主机指标**：/proc 采集 load1、CPU 使用率、内存总量/占用 → `server_metrics` 表（每服务器最新值）→ 服务器列表"负载"列。
- **流量统计（仅统计，不做强制配额）**：骨架配置启用 xray stats（inbound 级 + 用户级 policy），
  用户条目带 `level: 0`；Agent 经 gRPC StatsService 拉取计数器并按采样区间计算增量
  （连接建立后首帧仅建立采样基线、不上报流量，避免重连后把全量当增量重复计数）→
  `traffic` 表累计（节点与用户两个维度）→ 节点/用户列表"流量"列。
  旧版本生成的 config.json 由 Agent 启动时自动补齐 stats/policy 配置（缺失则落盘重启）。
  socks/http 的 accounts 无 email，仅覆盖节点维度。

在线/离线由 WS 连接推导，连接存亡由 §2 的应用层心跳判定（30s ping，90s 无流量判死）。

## 14. MVP 已知问题与后续迭代目标

| 项 | 说明 |
|---|---|
| 面板 TLS | ✅ 已实现（见 §12）：自带证书 / ACME 自动证书 / 反代终止 TLS |
| 配置漂移 reconcile | ✅ 已实现（见 §17）：检测上报 + 管理员修复按钮 |
| 流量统计与配额 | ✅ 已实现（见 §13）：仅统计；强制配额未做 |
| 主机遥测 | ✅ 已实现（见 §13） |
| 逐节点用户分配 | ✅ 已实现（见 §16）：默认全关，用户勾选节点 |
| 全协议向导 | ✅ 已实现（见 §15）：xray 全部 inbound 协议 |
| `vless://` 链接订阅 | ✅ 已实现：GET /sub/{token}/links 返回 base64 链接集合（vless/trojan/vmess/ss） |
| 订阅二维码 | ✅ 已实现：用户列表扫码导入 |
| xray 版本升级管理 | ✅ 已实现（见 §18） |
| 事件告警与 SQLite 备份 | ✅ 已实现（见 §19）：Webhook + Telegram，VACUUM INTO 下载 |
| 多管理员 / RBAC | 决策不做：单管理员符合规模假设 |
| fallback 传输实现 | 决策不做：WS 通道全绿，无实际需求；requester 接口已隔离，需要时再实现 |
| NAT / 中继链路 | 决策不做（明确排除） |

## 15. 全协议向导（已实现，超出 MVP 范围）

协议范围：xray 全部 inbound 协议——vless / vmess / trojan / shadowsocks / socks / http / dokodemo-door。

决策：

- **安全层**：能走 Reality 的全走 Reality，不引入证书管理。Reality 仅与 RAW(tcp)/gRPC/XHTTP
  三种传输组合（xray 官方约束）；ws/h2/kcp/httpupgrade 不开放。
- **deprecated 屏蔽**：xray 已将 vmess / trojan / shadowsocks（全部用法）及 gRPC 传输标记为
  deprecated（推荐迁移 VLESS + XHTTP / VLESS Encryption）。向导不再提供这些协议与 gRPC 传输，
  节点列表对存量 deprecated 节点标"已废弃"；后端 API 保留兼容（存量节点继续运行、可重试可删除）。
  手动迁移路径：新建等价 vless + XHTTP 节点 → 用户切换订阅节点 → 删除旧节点
  （协议变更必然改变客户端配置，无法无感迁移）。
- **VLESS Encryption**：vless 可选 X25519 / ML-KEM-768（后量子，推荐）认证，
  由 Agent 执行 `xray vlessenc` 生成 decryption/encryption 对——decryption 填入模板（私钥不出服务器），
  encryption 客户端字符串随 realized_config 上报，订阅输出 mihomo `encryption` 字段。
  可与 flow vision 组合（native 拼接），组合时客户端字符串按 1-RTT 下发。
- **用户凭证**：复用用户 UUID——vless/vmess 作 `id`，trojan 作 `password`，ss 作密码
  （2022-blake3 定长密钥由 UUID 确定性派生：aes-128-gcm 取 UUID 原始 16 字节，
  aes-256-gcm/chacha20 取 sha256(UUID)），socks/http 用户名与密码均为 UUID。
- **VLESS 详细选项**：flow（vision/无，vision 仅 tcp）、network（tcp/xhttp 及各自子选项）、
  uTLS 指纹（纯订阅侧参数）均以表单开放。
- **热操作**：add_user/remove_user 扇出载荷按服务器携带各节点协议参数（`AddUserPayload.Nodes`），
  Agent 按协议构造 account；ss/socks/http 热增删不支持时由既有"热操作失败 → 重启 → 回滚"流水线兜底。
- **订阅**：按协议生成 mihomo 代理项（vless/vmess/trojan 带 reality-opts，ss/socks5/http 标准类型）；
  节点命名 `{别名}-{协议}-{端口}`；dokodemo-door 为端口转发，不进订阅。

已知问题（在 §14 表格基础上新增）：

| 项 | 说明 |
|---|---|
| trojan/vmess over Reality | 非标准 TLS 证书组合，依赖客户端 reality 支持（mihomo 系可用），非 Reality 客户端不可用 |
| socks/http 明文 | 仅账密认证无加密，不应暴露于不可信网络 |
| ss 无 Reality | 仅 AEAD 加密 |
| xray 版本兼容 | 新版 xray-core 已移除 vmess alterId（服务端），mihomo 客户端订阅仍携带 `alterId: 0` |
| fallbacks 等高级选项未开放 | vless/trojan 的 fallbacks、xhttp extra 参数等暂不支持 |

## 16. 逐节点用户分配（已实现，超出 MVP 范围）

替代 §8 的全对全隐含关系：`user_nodes` 关联表，**默认全关**——新建用户/节点无任何关联，
管理员在用户列表"分配节点"对话框勾选（`PUT /api/users/{id}/nodes` 整体替换）。

- **扇出语义**：apply_node 只携带分配到该节点的用户 UUID；分配变更按差量扇出
  add_user/remove_user（载荷仅含受影响节点）；删除用户仍向所有服务器幂等扇出 remove_user。
- **订阅**：YAML 与 links 端点均只含分配到该用户的 active 节点。
- **显式停用/启用开关**：`users.disabled`（`PATCH /api/users/{id}` 带 `disabled` 字段，
  用户列表行内"停用/启用"按钮）。停用 → 对其已分配节点扇出 remove_user；启用 → 扇出 add_user 恢复。
  disabled 与 expired（§9）正交，**有效停权态 = disabled OR expired**：add_user/remove_user 扇出只在
  有效停权态跃迁时发生（已 expired 再 disable 不重复扇出；恢复需两者都解除）；有效停权态下
  订阅 YAML/links proxies 为空、userinfo 头照常、落地页显示"已停用"，`NodeUserUUIDs` 不下发。
- **迁移**：`PRAGMA user_version` 一次性为存量用户补全"全节点"关联，现有订阅不受影响。
- Agent 侧无新增状态：`AddUserPayload.Nodes`（tag → 协议参数）驱动精确落点；
  载荷缺省 `Nodes` 时兼容旧语义（作用于全部节点）。

## 17. 配置漂移 reconcile（已实现，超出 MVP 范围）

检测上报 + 管理员修复：

- Agent 每次落盘记录配置哈希，周期比对（默认 15s，`-drift-interval` 可调）；
  外部篡改/删除即上报 `drift_report`（仅状态变化时），回滚路径同步基线避免误报。
- 面板置 `servers.config_drift` 标志，服务器列表显示"配置漂移"徽章与"修复漂移"按钮。
- 修复 = `POST /api/servers/{id}/repair` 重放该服务器全部 active 节点；
  Agent 检出漂移后的下一次落盘以"骨架 + 受管节点 inbound"净化配置为基，
  外部对非节点段（log/routing 等）的改动一并还原，漂移标志自动清除。
- 基线说明：agent 停机期间的外部改动无法区分，以启动时文件为基线。

## 18. xray 版本升级管理（已实现，超出 MVP 范围）

面板服务器列表"升级 xray"（`POST /api/servers/{id}/upgrade`，版本为 `latest` 或 `vX.Y.Z`）→
`upgrade_xray` 命令下发（离线留队列补发）→ Agent：

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
    被新连接顶替的旧连接注销不算，hello 重连不重复报）；
  - `config_drift`：dispatcher 收到 `drift_report` true（§17）；
  - `node_failed`：apply_result 失败或命令死信导致节点置 failed（§6）。
- 通道（各自独立判定，异步发送不阻塞主路径，5s 超时，失败仅记日志）：
  - Webhook：POST JSON `{"event","server","node","detail","time"}` 到 `alert_webhook_url`；
  - Telegram：Bot API `sendMessage` 纯文本，需 `alert_telegram_bot_token` +
    `alert_telegram_chat_id` 同时具备（token 与 tls key 同风格：不回显，仅给置位标记）。
- 防抖动：同一服务器同一事件 5 分钟内不重复发（内存 map 记上次发送时间，重启清零可接受；
  `LATTIX_ALERT_DEBOUNCE` 可覆盖窗口，dev/e2e 用）。
- `POST /api/settings/alerts/test`：按已保存配置向两通道各发一条测试消息，返回各通道成败。
- e2e 加速：`LATTIX_WS_PING_INTERVAL`（Go duration）覆盖 WS 心跳周期（pong 超时 = 3 倍），
  参照 `LATTIX_EXPIRY_SWEEP_INTERVAL` 先例。

**SQLite 备份**：`GET /api/backup`（session 鉴权）`VACUUM INTO` 到临时文件后以
`lattix-backup-<YYYYMMDD-HHMMSS>.db` 附件返回，发送完成清理临时文件；单连接 +
busy_timeout 下与并发读写安全共存，失败 500。设置页"面板维护"区块提供下载按钮。
