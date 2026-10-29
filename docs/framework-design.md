# Lattix 框架设计（总览与文档索引）

> 本文档是总体设计的入口：项目目标、架构总览、仓库结构、面板页面与范围决策在此，
> 各子系统的实施契约拆分在 [framework/](framework/) 专题文件与 docs/ 下各专题设计文档，
> 末尾文档地图给出完整索引。
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

**规模假设**：服务器少于 30 台，管理员 1 人，实际用户少于 30 人。所有服务器假设具备公网 IP 与完整公网访问能力；NAT 机器（共享 IP / 无入站）与代理链已实现，见[代理链与 NAT](framework/chain-nat.md)。

**协议范围**：全协议向导覆盖 xray 全部 inbound 协议（vless / vmess / trojan / shadowsocks / socks / http / dokodemo-door），见[全协议向导](framework/protocol-wizard.md)。

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

- **单通道**：Agent → Backend 一条 WebSocket 长连接承担全部双向通信。Backend 永不主动外连 Agent，Agent 不提供任何监听 API 端点。该形态同时兼容公网服务器与 NAT 服务器（[代理链与 NAT](framework/chain-nat.md)）。
- **无 fallback 实现**：Backend 侧定义 `requester` 接口隔离"发送命令"与"具体传输"，当前只有 WebSocket 一个实现；gRPC/HTTP 等其他实现属后续迭代。
- **离线排队**：Agent 离线期间，发往它的命令滞留于 `commands` 表，重连后补发。
- **重发与死信**：重连时将该服务器 `sent` 未终态的命令重置为 `queued` 重新补发（幂等性由 Agent 各处理器保证）；`attempts` 超过上限（10 次）标记 `failed` 死信，不再重发；`node.apply` 死信时对应节点同步置 `failed` 并记录原因（[控制通道](framework/control-channel.md)）。

## 3. 仓库结构与技术栈

```
src/frontend/  # Vite + React + TypeScript + shadcn/ui，包管理器使用 bun
               # 单一设计语言「液态玻璃」（Lattix Liquid Glass，见根 DESIGN.md），
               # 默认深色外观（约定见 frontend.md）
src/backend/   # Go：面板 HTTP API + Agent WS 端点 + SQLite
               # internal/panel 下另拆五个子包：scheduler（公共周期任务调度）、
               # releases（agent/xray 版本目录）、exchange（汇率目录与成本换算）、
               # cdn（CDN 节点目录接线）、selfupdate（面板自更新）
src/agent/     # Go：独立二进制，systemd 托管（无 systemd 时用户态守护）；
               # internal/fileutil 为各组件共用的原子落盘/复制原语
src/shared/    # Go module：WS 消息结构体、虚拟配置类型，backend/agent 共用
scripts/
  install-panel.sh   # 面板原生/Docker 安装实现
  install-agent.sh   # Agent 安装实现（含兼容性迁移：新版 latx-ag 注册表，服务启动前执行）
install.sh           # 唯一面向用户的统一安装入口
```

- **兼容性迁移**：节点侧数据格式跨版本不兼容时（如 0.1.23 更名服务器测试类别），在
  latx-ag.sh 维护迁移注册表（`MIGRATION_REGISTRY`，条目 = 标识 | 触发版本窗口 | pre/post
  相位），`latx-ag update`、install.sh 重装、Agent 面板自升级（internal/selfupdate）三条
  升级路径统一以「新 latx-ag」子进程在旧新版交接点执行；条目必须幂等自守卫，完成落
  `data/migrations/<id>.done` 标记。详见 latx-ag.sh 注释与 `latx-ag migrate`（[引导与运维](framework/onboarding-and-ops.md)、[已知问题](KNOWN_ISSUES.md)）。

- 前端依赖安装与构建：`bun install` / `bun run build`，锁文件入库。设计语言为「液态玻璃」，
  令牌层在 `src/styles/tokens.css`（约定见根 [DESIGN.md](../DESIGN.md) 与[前端开发](frontend.md)）；
  深色是默认外观，浅色为同一令牌体系的日间版。
- 后端与 Agent 均为 Go，通过 Shared module 共享消息定义，保证协议两端类型一致。
- 数据库：SQLite（规模假设下绰绰有余）。
- 版本管理：git，monorepo。

## 4. 面板页面

页面入口（`src/frontend/src/App.tsx` 与 `components/nav-config.ts`）：登录 / 公开订阅页 /
控制台（原仪表盘：服务器数、在线数、链路数、正常/降级数、用户数与流量图表）/ 服务器
（列表 + 卡片视图，创建和编辑时填写别名、Country、Location、机器类型、公网地址、NAT 可用
端口、Tag 与 xray 版本）/ 链路（概览/详情双 tab，见[代理链与 NAT](framework/chain-nat.md)）/
链路分组 / 用户 / 用户分组 / 订阅模板（分流模板管理与分配，见
[订阅分流与模板](subscription-routing-design.md)）/ 外部订阅（第三方订阅导入、同步与节点
查看）/ 运行监控 / 成本统计（已生效成本 + 计算成本两个口径，见
[后端调度与服务器计费设计](billing-scheduler-design.md)）/ 操作日志 / 请求日志（见
[日志系统设计](logging-design.md)）/ 设置。

管理 API 走 HTTP + session（账号密码登录）；Agent 通道走 token（[控制通道](framework/control-channel.md)）。
产品层合并不新增第三套统一 API：直连使用 `/api/node/*`，中转使用 `/api/chain/*`，前端聚合展示。

**已知问题**：离线城市建议来自 `country-state-city` 本地数据，按需加载后数据块约 8 MB
（gzip 约 2.3 MB）；不影响首屏，但首次打开服务器国家/城市控件会产生额外加载。城市名称以数据集原文为主，
Location 允许自由输入作为兜底。后续可按国家拆分数据文件以降低单次加载量。

## 5. 范围与决策记录

MVP 期列出的后续迭代目标均已完成（订阅链接/二维码、TLS、漂移 reconcile、流量统计、
主机遥测、逐节点分配、全协议向导、xray 升级管理、告警与备份、NAT/中继链路），各契约
见 framework/ 专题文件。仍然有效的**决策不做**项：

| 项 | 决策 |
|---|---|
| 多管理员 / RBAC | 不做：单管理员符合规模假设 |
| fallback 传输实现 | 不做：WS 通道全绿，无实际需求；requester 接口已隔离，需要时再实现 |

## 6. 文档地图

### framework/ 专题（本设计文档的拆分）

| 文档 | 内容（原章节） |
| --- | --- |
| [数据模型](framework/data-model.md) | SQLite 全库表清单与所属专题索引（§4） |
| [控制通道](framework/control-channel.md) | WS 协议、节点生命周期与 apply 流水线、虚拟配置参数分工（§5–§7） |
| [订阅](framework/subscription.md) | 订阅输出、用户生命周期（试用/生效/配额）、客户端下载、外部订阅导入与关联（§9、§9.1） |
| [用户与分组](framework/users-and-groups.md) | 用户链路凭证、逐链路分配、有效停权态、链路/用户分组（§8、§16、§22） |
| [引导与运维](framework/onboarding-and-ops.md) | 服务器引导流程、面板安装、latx / latx-ag 运维程序（§11、§20） |
| [安全](framework/security.md) | TLS 四形态、会话与限流、SSRF 防护、完整性校验（§12） |
| [遥测](framework/telemetry.md) | 遥测上报、主机指标、流量统计口径（§13） |
| [全协议向导](framework/protocol-wizard.md) | 协议范围、Reality/VLESS Encryption 决策与已知问题（§15） |
| [服务器维护](framework/server-maintenance.md) | 配置漂移 reconcile、xray 升级、事件告警三分类与 Ban 观察、备份（§17–§19） |
| [代理链与 NAT](framework/chain-nat.md) | 链路统一模型、NAT 两档、piece/消息/编排实现契约、链路页语义（§10、§21、§21.1） |
| [操作进度观察](framework/observe.md) | observe 旁路进度、覆盖操作与限制（§23） |

### 专题设计文档（docs/）

| 文档 | 内容 |
| --- | --- |
| [RPC API、Requester 与 Agent 通道](rpc-api-design.md) | 面板 API 形态、requester 约定与 agent 动作协议 |
| [OpenAPI](openapi.yaml) | 面板 HTTP API 契约（前端类型由此生成，路由权威） |
| [WS 协议 Schema](ws-protocol.schema.json) | Panel ↔ Agent WebSocket 消息结构（部分参考，以 Go 序列化为准） |
| [链路 Revision 与流量统计](chain-revisions-traffic-design.md) | 链路编辑、离线 revision 与流量口径 |
| [链路统计](chain-link-stats-design.md) | 服务器对测速（latency.probe 轮次、有向延迟表）与链路一览统计 |
| [订阅分流与模板](subscription-routing-design.md) | 订阅模板四级配置、规则缓存与原子发布 |
| [服务器测试](server-testing-design.md) | IP 质量/回程/测速/流媒体/连通性测试与三网目录测速的设计契约 |
| [服务器探针监控](server-probe-monitoring-design.md) | CPU/内存/磁盘/延迟探针与 24 小时历史 |
| [Panel 生命周期与 Agent 连接状态机](panel-lifecycle-state-machine-design.md) | 启动/更新/故障生命周期与 Agent 连接状态转换 |
| [优雅停机与 Agent 设置同步](graceful-shutdown-agent-settings-design.md) | WS 1012 排空重连与设置 revision 同步 |
| [后端调度与服务器计费](billing-scheduler-design.md) | 周期调度器、服务器成本统计与到期预警 |
| [日志系统](logging-design.md) | 操作日志与请求日志的存储、过滤与容量限制 |
| [xray 缓存清理](xray-cleanup-design.md) | agent 侧 xray 缓存清理契约 |
| [安装与运维](installation.md) | 脚本选项全表、模式对比、更新卸载、latx / latx-ag 命令、反代与 TLS |
| [开发指南](development.md) | 构建、手动运行、e2e 验收、CI/CD 与发版流程 |
| [前端开发](frontend.md) | 前端命令、目录结构与设计系统约定 |
| [已知问题与部署边界](KNOWN_ISSUES.md) | 部署模型边界与明确接受的限制 |
| [Changelog](CHANGELOG.md) | 版本变更记录 |
