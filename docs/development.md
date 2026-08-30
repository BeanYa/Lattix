# 开发指南

面向贡献者的构建、端到端验收与发版流程。前端命令、目录结构与主题系统约定见
[前端开发](frontend.md)；总体设计见 [设计文档](framework-design.md)。

## 仓库结构

```
src/frontend/  # Vite + React + TypeScript + shadcn/ui + Three.js（包管理器 bun）
               # 含可插拔主题系统（src/frontend/src/themes/，默认 Apple HIG，可切换经典 Cream Grid）
src/backend/   # Go：面板 HTTP API + Agent WS 端点 + SQLite
src/agent/     # Go：独立二进制，systemd 托管
src/shared/    # Go module：WS 消息结构体、虚拟配置类型，backend/agent 共用
scripts/
  install-panel.sh    # 面板原生/Docker 安装实现，由根 install.sh 按 tag 加载
  install-agent.sh    # Agent 安装实现，由根 install.sh 按 tag 加载
  latx.sh / latx-ag.sh # 面板/Agent 管理 CLI
  e2e/                # 各功能端到端验收脚本
  dev/                # 开发辅助（PoC / 隔离测试）
install.sh            # 面向用户的统一安装入口
```

## 构建

```bash
go build ./src/backend/... ./src/agent/...
cd src/frontend && bun install --frozen-lockfile && bun run build   # 含 tsc 类型检查
```

## 手动构建与运行面板

```bash
cd src/frontend
bun install --frozen-lockfile
bun run build
rm -rf ../backend/internal/web/dist
mkdir -p ../backend/internal/web/dist
cp -a dist/. ../backend/internal/web/dist/
cd ../..
go build -o lattix-backend ./src/backend/cmd/backend

# HTTP（本地/受信网络或反代后端）
./lattix-backend -addr :8080

# 自带证书
./lattix-backend -addr :443 -tls-cert fullchain.pem -tls-key privkey.pem

# ACME 自动证书（需 443 公网可达）
./lattix-backend -addr :443 -tls-acme-domain panel.example.com
```

默认账号由启动参数或环境变量决定（安装器会生成密码）。正式（release）构建拒绝
以公开已知的默认密码 `lattix-admin` 启动，必须显式设置
`-admin-pass`/`LATTIX_ADMIN_PASS`（dev 构建不受限）。

## 端到端验收（e2e）

需本机 xray 二进制（`XRAY_BIN` 可覆盖）：

```bash
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/xray.sh           # 基础流水线回归
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/protocols.sh      # 全协议节点 + 订阅
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/vlessenc.sh       # VLESS Encryption 数据通路
LATX_ALLOW_PRIVATE_OUTBOUND=1 XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/groups.sh  # 分组（测试钩子放行 loopback 外部订阅夹具）
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/usernodes.sh      # 逐节点用户分配
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/telemetry.sh      # 流量统计 + 主机遥测
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/reconcile.sh      # 配置漂移检测与修复
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/upgrade.sh        # xray 升级（本地镜像，无外网）
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/tls.sh            # 面板 TLS / wss
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/links.sh          # 分享链接订阅 + userinfo 头/落地页/用户有效期
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/settings.sh       # 设置页 / 改密 / TLS 域名路径模式 / 自重启
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/alerts.sh         # 事件告警（webhook/防抖/三类事件）+ SQLite 备份下载
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/event-log.sh      # 操作日志 / 请求日志 / 容量与清空
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/panel-update.sh   # 面板更新状态机与原子替换
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/panel.sh          # 当前 Panel × Agent RPC 协议回归
XRAY_BIN=/usr/local/bin/xray bash scripts/dev/poc-reverse.sh    # xray reverse bridge/portal 可行性 PoC（§21）
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/chains.sh         # 代理链 + NAT：建链/真实流量/降级自愈/重试/拆链
bash scripts/e2e/install-panel.sh                               # 面板一键安装 + latx 管理程序（本地假 release，无外网）
bash scripts/e2e/install-entry.sh                               # 根安装入口参数/版本转发（无外网）
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/install-agent.sh  # Agent 引导安装 + latx-ag（本地假 release，LATX_DEV=1）
bash scripts/dev/test-install-agent-bbr.sh                      # Agent 安装器 BBR 能力/权限/持久化隔离测试
```

`LATX_ALLOW_PRIVATE_OUTBOUND=1` 是仅用于测试的钩子（放行 SSRF 私网拨号），
生产环境严禁设置。

Agent 常用参数：`-panel`（固定面板 WS 地址）、`-token`（bootstrap token）、
`-state` / `-settings`（本地状态与面板同步设置）及 `-xray-release-base`
（xray 下载镜像源）。重连、遥测与漂移检测间隔由面板"设置 → Agent"统一下发。

## CI/CD 与版本发布

- **发版**：push `v*` tag 触发 `.github/workflows/release.yml`——构建前后端并
  注入版本（前端嵌入单个 Go 面板二进制）、打包 amd64/arm64 的 panel 与 agent
  tarball、生成 `checksums.txt`，并以 matrix 执行 `scripts/e2e/` 中可在 CI
  环境运行的 e2e 回归（`telemetry.sh`、`vlessenc.sh` 依赖真实外网数据通路不进
  CI；`groups.sh` 的外部订阅夹具起在 loopback，matrix 条目注入
  `LATX_ALLOW_PRIVATE_OUTBOUND=1` 测试钩子放行 SSRF 私网拨号）。通过后先发布
  `ghcr.io/beanya/lattix:<version>` 与 `latest` 多架构镜像，再发布 GitHub
  Release。Release 另附带仓库原样的 `install-panel.sh` / `install-agent.sh` /
  `latx.sh` / `latx-ag.sh`，其 sha256 一并纳入 `checksums.txt`，供根安装器
  校验子安装脚本。Release notes 依次取自 `docs/CHANGELOG.md` 的对应版本段、
  `[Unreleased]` 段（皆空回退自动生成），`scripts/dev/changelog-cut.sh vX.Y.Z`
  发布前后执行均可：日常变更累计在 `[Unreleased]`，固化后归入对应版本段。
- **协议同步发布**：Panel、Frontend 和 Agent 按同一版本发布，不维护旧 HTTP/WS
  协议兼容窗口；Agent 对格式有效但未知的动作返回 `UNSUPPORTED_ACTION`。业务
  数据库在新面板启动、对外提供服务前自动执行一次事务化结构迁移，迁移失败会
  回滚并阻止面板启动。
- **agent 自升级**：服务器页"升级 agent"下发 `agent.upgrade`，agent 从 GitHub
  release 下载目标版本、校验 checksums.txt 后原子自替换并退出（systemd 拉起
  完成升级）。
