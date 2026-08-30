# Lattix

多服务器 xray 代理管理面板：主控面板（Backend）统一管理多台受控服务器（Agent），
可视化创建代理节点，按用户生成订阅。

- 单条 WebSocket 长连接承担全部双向通信（Agent 主动拨出，Backend 永不外连）
- 虚拟配置模板下发，Agent 落地为 xray 实际配置并上报生效值
- 节点创建/删除、用户增删全部零重启热操作（xray gRPC API），重启兜底 + 失败回滚
- 直连与中转（代理链/NAT）链路、Reality 安全层、多格式订阅与分流模板、
  流量统计与事件告警
- 参考 3x-ui / s-ui 的交互模式，不做商业化

## 架构

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

- `src/backend/`：Go，面板 HTTP API + Agent WS 端点 + SQLite
- `src/agent/`：Go，独立二进制，systemd 托管
- `src/frontend/`：Vite + React + TypeScript + shadcn/ui（包管理器 bun）
- `src/shared/`：Go module，WS 消息结构体与虚拟配置类型，backend/agent 共用

## 快速开始

交互式安装（推荐）——在目标 Linux 服务器执行：

```bash
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh | bash
```

按向导选择 Docker Compose（推荐）或原生 systemd 模式，可继续设置部署地址、
端口、管理员账号密码和配置目录，直接回车采用默认值。安装完成后终端输出访问
地址和管理员凭据。

非交互安装：

```bash
# Docker Compose（推荐；主机需已有 Docker Engine 与 Compose 插件）
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode docker

# 原生二进制 + systemd
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode native
```

Docker 模式默认只监听 `127.0.0.1:8080`，需经 Nginx/OpenResty 反向代理对外；
原生模式默认监听 `0.0.0.0:8080`。管理员密码未显式设置时随机生成，并在安装
完成时输出。完整的脚本选项、更新卸载与运维命令见
[安装与运维](docs/installation.md)。

面板装好后，在面板"添加服务器"获取受控机（Agent）的一行安装命令，原样放到
受控机执行即可。

## 系统要求

- `linux/amd64` 或 `linux/arm64`
- 安装面板需要 `root` 或 `sudo`，以及 `curl`
- Docker 模式：Docker Engine + Compose 插件（缺失时可加 `--install-docker`
  自动安装）；原生模式：systemd、`tar`、`sha256sum`

## 文档

| 文档 | 内容 |
| --- | --- |
| [安装与运维](docs/installation.md) | 脚本选项全表、模式对比、更新卸载、latx / latx-ag 命令、反代与 TLS |
| [已知问题与部署边界](docs/KNOWN_ISSUES.md) | 部署模型边界与明确接受的限制 |
| [Changelog](docs/CHANGELOG.md) | 版本变更记录 |
| [设计文档](docs/framework-design.md) | 总体设计与实施契约：数据模型、控制通道、安全与各子系统 |
| [开发指南](docs/development.md) | 构建、手动运行、e2e 验收、CI/CD 与发版流程 |
| [前端开发](docs/frontend.md) | 前端命令、目录结构与可插拔主题系统 |
| [Panel 生命周期与 Agent 连接状态机](docs/panel-lifecycle-state-machine-design.md) | 启动/更新/故障生命周期与 Agent 连接状态转换 |
| [RPC API、Requester 与 Agent 通道](docs/rpc-api-design.md) | 面板 API 形态、requester 约定与 agent 动作协议 |
| [OpenAPI](docs/openapi.yaml) | 面板 HTTP API 契约（前端类型由此生成） |
| [WS 协议 Schema](docs/ws-protocol.schema.json) | Panel ↔ Agent WebSocket 消息结构 |
| [链路 Revision 与流量统计](docs/chain-revisions-traffic-design.md) | 链路编辑、离线 revision 与流量口径 |
| [订阅分流与模板](docs/subscription-routing-design.md) | 订阅模板、规则缓存与原子发布 |
| [代理链与 NAT](docs/framework-design.md#21-代理链与-nat-支持已实现v003) | 入口 → 中转 → 出口链路与 NAT 两档支持（设计文档 §21） |
| [服务器测试](docs/server-testing-design.md) | IP 质量/回程/测速等服务器测试的设计与实现契约 |
| [服务器探针监控](docs/server-probe-monitoring-design.md) | CPU/内存/磁盘/延迟探针与 24 小时历史 |
| [日志系统](docs/logging-design.md) | 操作日志与请求日志的存储、过滤与容量限制 |
| [后端调度与服务器计费](docs/billing-scheduler-design.md) | 周期调度器与服务器成本统计 |
| [优雅停机与 Agent 设置同步](docs/graceful-shutdown-agent-settings-design.md) | WS 1012 排空重连与设置 revision 同步 |
| [xray 缓存清理](docs/xray-cleanup-design.md) | agent 侧 xray 缓存清理契约 |
| [CodeGraph 仓库分析](docs/codegraph-analysis.md) | 代码库结构与依赖的静态分析（英文） |
