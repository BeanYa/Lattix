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
- **重发与死信**：重连时将该服务器 `sent` 未终态的命令重置为 `queued` 重新补发（幂等性由 Agent 各处理器保证）；`attempts` 超过上限（10 次）标记 `failed` 死信，不再重发。

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
| `servers` | id, alias, address(公网地址), token(长期凭证), last_seen_at, xray_version, created_at |
| `users` | id, name, uuid, sub_token, created_at |
| `nodes` | id, server_id, protocol(vless), port, config_template(JSON), realized_config(JSON), status, error, created_at |
| `commands` | id, server_id, type, payload(JSON), status(queued/sent/acked/failed), attempts, created_at, updated_at |

说明：

- `commands` 表同时充当**离线命令队列**与**操作日志**（全部保留，不自动清理；重发/死信语义见 §2）。
- `nodes.config_template` 是面板侧虚拟配置（含占位符）；`nodes.realized_config` 是 Agent 上报的实际生效值（端口、public_key、short_id 等）。
- `servers.address` 是订阅中节点地址的唯一来源（§9）：**创建服务器时由管理员填写公网地址，agent 不校验**；留空则按 agent WS 拨入的 RemoteAddr 自动学习，一经写入不再被覆盖（地址变更由管理员修改）。
- **无用户-节点关联表**：MVP 成员关系隐含为"全部用户 ∈ 全部节点"（见 §8）。

## 5. 控制通道协议

连接：Agent 携带服务器 token 拨出至 `Backend /api/agent/ws`。

信封：JSON，`{id, type, payload}`，`id` 用于请求/响应关联。

消息类型：

| type | 方向 | 说明 |
|---|---|---|
| `hello` | agent→panel | 首连认证：token、agent 版本、xray 版本、xray 运行状态；bootstrap token 在此换发长期凭证（以 `last_seen_at` 为空判定 bootstrap 状态；长期 token 一经换发**不再轮换**，agent 侧内存兜底防止落盘失败锁死） |
| `apply_node` | panel→agent | 下发节点：虚拟配置模板 + 全量用户 UUID 列表 |
| `remove_node` | panel→agent | 删除节点 |
| `add_user` | panel→agent | 向该服务器所有 inbound 热加入一个用户 |
| `remove_user` | panel→agent | 从该服务器所有 inbound 热移除一个用户 |
| `apply_result` | agent→panel | 上报执行结果：成功返回 realized_config，失败返回 error |
| `uninstall` | panel→agent | 卸载 agent：`purge_xray=true` 时连同 install.sh 安装的 xray 与配置一并清除，`false` 时仅移除 agent（xray 及节点继续运行）；agent 先回执再自毁 |

在线/离线状态由 WS 连接是否存在直接推导，无周期心跳遥测。

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

`GET /sub/{sub_token}` → 返回 **mihomo（Clash.Meta）格式 YAML**。

- 目标客户端：mihomo 内核系（Clash Verge / Clash Party / FlClash 等）。原版 Clash 不支持 VLESS+Reality，不在目标范围。
- 内容：proxies 列表（每个节点一项，`type: vless`，`server` 取 `servers.address`（§4），嵌入**该用户自己的 UUID**、`flow`、`reality-opts: {public-key, short-id}`、`servername`、`udp: true`）+ 一个 `select` 类型 proxy-group + `MATCH` 规则。
- 节点命名：`{服务器别名}-vless-{端口}`，如 `tokyo01-vless-8443`。
- `vless://` 分享链接集合格式属后续迭代（实现成本极低，随时可补）。

## 10. 面板页面与 API

页面：登录 / 仪表盘（服务器数、在线数、节点数、用户数）/ 服务器列表（"添加服务器"填写别名、**公网地址**（留空自动学习，§4）与 **xray 版本**（默认 `latest`，§11），生成一行安装命令；可删除服务器——在线 agent 收到 `uninstall` 自卸载，**删除时可选"仅 agent"或"连同 xray"**（§5），离线仅删记录；可刷新凭证重取安装命令——已安装的换发后旧凭证失效，未安装的换发新 bootstrap token）/ 节点创建向导（选服务器 → VLESS+Reality 表单，端口可空 = 自动）/ 用户列表（创建用户 → 展示并复制订阅链接）。

管理 API 走 HTTP + session（账号密码登录）；Agent 通道走 token（§5）。

## 11. 服务器引导流程

1. 面板"添加服务器"填写别名、公网地址与 **xray 版本**（默认 `latest`，也可指定具体版本），生成一次性 **bootstrap token** 与一行安装命令；
2. `install.sh` 在被控机执行：按创建时指定的 xray 版本安装（`latest` 在执行时经 GitHub API 解析最新 release；**校验官方 release checksums**）→ 下载/安装 Agent 二进制（**按面板注入的 SHA256 校验**，明文 HTTP 下保证二进制不被中间人替换）→ 注册 systemd → 写入面板地址与 bootstrap token（重装时清除旧 state 文件，确保使用新 bootstrap token）；
3. Agent 启动首连，以 bootstrap token 换发长期服务器 token；**实际安装的 xray 版本随 hello 上报，面板服务器列表展示实际版本号**。

凭证刷新（§10）将 `last_seen_at` 重置为空，使服务器回到 bootstrap 状态，下次 hello 重新换发。

## 12. 安全

- MVP 运行于本地/受信网络：**HTTP + token**，面板 TLS 进已知问题。
- install 通道（install.sh、agent 二进制）明文传输，但**以 SHA256 校验保障完整性**（§11）：校验和由面板注入脚本，等价于把信任锚定在 install.sh 的投递路径上。
- Reality 私钥永不出服务器（§7）。
- Agent 能力面收敛：只执行 xray 配置落地、服务重启、状态上报、自卸载，不接受任意命令。

## 13. 遥测

MVP 仅：WS 连接存在性（在线/离线）、`hello` 与失败上报中携带的 xray 版本与运行状态。CPU/内存/负载不进 MVP。

## 14. MVP 已知问题与后续迭代目标

| 项 | 说明 |
|---|---|
| 面板 TLS | 当前 HTTP 明文 + token，公网部署前必须补 |
| 配置漂移 reconcile | 服务器上手动改 xray 配置不会被检测/覆盖修复，Agent 仅以命令为准 |
| 流量统计与配额 | 需 xray stats API 采集 + Agent 上报，接口位置已留 |
| 主机遥测 | CPU/内存/负载展示 |
| 逐节点用户分配 | 用户-节点关联表 + n 个订阅链接 ↔ 不定个节点 |
| 全协议向导 | vmess / trojan / shadowsocks 等 xray 全部协议的可视化创建 |
| `vless://` 链接订阅 | 纯文本/base64 链接集合端点 |
| fallback 传输实现 | requester 接口的 gRPC/HTTP 实现 |
| NAT / 中继链路 | 一键组织 A 服务器 inbound → NAT 落地 → 互联网 |
| xray 版本升级管理 | 面板下发升级指令 |
| 订阅二维码 | 移动端扫码导入 |
| 多管理员 / RBAC | 当前单管理员 |
