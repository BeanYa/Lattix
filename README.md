# Lattix

多服务器 xray 代理管理面板：主控面板（Backend）统一管理多台受控服务器（Agent），
可视化创建代理节点，按用户生成订阅。

- 单条 WebSocket 长连接承担全部双向通信（Agent 主动拨出，Backend 永不外连）
- 虚拟配置模板下发，Agent 落地为 xray 实际配置并上报生效值
- 节点创建/删除、用户增删全部零重启热操作（xray gRPC API），重启兜底 + 失败回滚
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

```
src/frontend/  # Vite + React + TypeScript + shadcn/ui（包管理器 bun）
src/backend/   # Go：面板 HTTP API + Agent WS 端点 + SQLite
src/agent/     # Go：独立二进制，systemd 托管
src/shared/    # Go module：WS 消息结构体、虚拟配置类型，backend/agent 共用
scripts/
  install.sh          # Agent 引导安装脚本（release 钉版 / 面板托管双模式，均校验 SHA256）
  dev-e2e-*.sh        # 各功能端到端验收脚本
```

## 功能

**节点与协议**

- 节点向导：vless / socks / http / dokodemo-door（vmess / trojan / shadowsocks 与 gRPC
  传输已被 xray 标记 deprecated，API 保留兼容但向导不再提供新建）
- Reality 安全层，传输 tcp / XHTTP（gRPC 已废弃屏蔽）；dest 预检 + 白名单 fallback
- VLESS 详细选项：flow（vision/无）、XHTTP path/mode/host、uTLS 指纹
- VLESS Encryption（mlkem768 后量子 / x25519 认证），可与 vision 组合（1-RTT）
- 节点状态机 `pending → applying → active | failed`，失败详情 + 重试

**用户与订阅**

- 逐节点用户分配（默认全关）：用户勾选节点，差量扇出，订阅只含分配节点
- 用户凭证复用 UUID：vless id / trojan 密码 / ss 派生密钥 / socks+http 账密
- 订阅：`GET /sub/{token}` mihomo（Clash.Meta）YAML；`GET /sub/{token}/links`
  base64 分享链接集合（vless/trojan/vmess/ss）；用户页二维码扫码导入
- 订阅响应头 `subscription-userinfo`（已用上下行流量、到期时刻）与
  `profile-update-interval: 24`，客户端自动展示用量并按天刷新
- 用户有效期：创建/编辑可设到期时刻，到期自动停权（订阅保留但节点为空，
  sweeper 扇出 remove_user），延长或清除有效期自动恢复（扇出 add_user）
- 订阅落地页：浏览器打开 `GET /sub/{token}` 得到自包含页面（用量/有效期/节点数、
  订阅地址复制、二维码、clash/mihomo 一键导入），不依赖任何外网资源

**运维**

- 流量统计（节点/用户双维度，xray stats 采集）与主机遥测（load/CPU/内存）
- 配置漂移 reconcile：外部篡改 xray 配置自动检测上报，一键修复（重放节点）
- 事件告警：服务器离线 / 配置漂移 / 节点失败（仅状态跃迁触发，同服务器同事件
  5 分钟防抖），Webhook + Telegram Bot 双通道，设置页配置并可发测试消息
- SQLite 备份：设置页"面板维护"一键下载（`GET /api/backup`，VACUUM INTO 一致性快照）
- xray 版本升级管理：面板下发，agent 校验官方 .dgst 后原子替换，失败回滚
- 离线命令队列：agent 离线期间命令滞留，重连补发；重发死信（10 次）
- 服务器引导：一行安装命令（bootstrap token 换发长期凭证），卸载可选"仅 agent"/"连同 xray"

**安全**

- 面板 TLS：自带证书 / ACME 自动证书（Let's Encrypt）/ 反代终止 TLS（openresty、nginx）
- HTTPS 下会话 cookie Secure；agent 走系统 CA 校验 wss
- install 通道以 SHA256 校验保障完整性；Reality/VLESS Encryption 私钥不出服务器
- Agent 能力面收敛：只执行配置落地、重启、上报、自卸载、升级，不接受任意命令

## 快速开始

### 构建与运行面板

```bash
go build -o lattix-backend ./src/backend/cmd/backend
cd src/frontend && bun install && bun run build && cd ../..

# HTTP（本地/受信网络或反代后端）
./lattix-backend -addr :8080

# 自带证书
./lattix-backend -addr :443 -tls-cert fullchain.pem -tls-key privkey.pem

# ACME 自动证书（需 443 公网可达）
./lattix-backend -addr :443 -tls-acme-domain panel.example.com
```

默认账号 `admin` / `lattix-admin`（`-admin-user` / `-admin-pass` 修改，生产必改）。
反代部署（docker + openresty/nginx 终止 TLS）时反代 `/`、`/api/agent/ws`（带 Upgrade 头）、
`/sub`、`/install.sh`、`/dist`，并以 `-public-url https://域名` 或 `X-Forwarded-Proto` 告知面板。

面板"设置"页可在线修改：对外地址与显示时区（立即生效）；TLS 模式 / 自带证书 PEM /
ACME 域名（保存后重启进程生效）；管理员密码（bcrypt 落库，改密即全部会话失效）；
事件告警（Webhook 地址 / Telegram Bot token / chat_id，三项全空即关闭，可发测试消息）。
设置页保存的值存于 SQLite `settings` 表并**优先于对应启动参数**，清除后恢复跟随启动参数。
"设置 → 面板维护"提供一键重启（`POST /api/settings/restart`）与 SQLite 备份下载
（`GET /api/backup`）：systemd 托管时退出后由
systemd 拉起，否则自派生新进程接管（同参数同端口，等待旧进程释放端口）；面板版本更新
替换二进制后也经此接口重启生效。

TLS 另支持**域名路径模式**（`tls_mode=path`）：面板按域名从证书根目录读取
`<tls-dir>/<域名>/fullchain.pem` 与 `privkey.pem`（certbot 风格，目录由 `-tls-dir`
指定，默认 `certs`，启动时解析为绝对路径并显示在设置页）。外部 ACME（如安装脚本）
申请/续期后直接写入该目录：保存设置时只填域名；续期替换文件后下一次 TLS 握手即
自动加载新证书，**免重启**（加载失败时回退已缓存证书，握手不中断）。

### 安装 Agent（受控服务器）

面板"添加服务器"生成一行安装命令，在受控机执行。正式版本（release 构建）的命令
钉到面板同版本的 GitHub release 资产，脚本与 agent 二进制天然同版：

```bash
curl -fsSL https://github.com/BeanYa/Lattix/releases/download/v0.0.1/install.sh | bash -s -- \
  --panel http://<面板地址> --token <bootstrap token> --xray-version latest
```

dev 构建（无对应 release）回退面板托管模式：`curl -fsSL http://<面板地址>/install.sh | ...`。
两种模式共用同一脚本：release 模式从 GitHub release 下载 agent 并校验 `checksums.txt`；
面板托管模式从面板 `/dist` 下载并校验面板注入的 SHA256。

脚本完成：安装指定版本 xray（校验官方 .dgst）→ 安装 agent（校验 SHA256）→
注册 systemd → 首连换发长期凭证。重装自动先停旧服务并清除旧 state。

### CI/CD 与版本兼容（§18）

- **发版**：push `v*` tag 触发 `.github/workflows/release.yml`——构建前后端并注入版本
  （`-ldflags -X main.version/-X main.githubRepo`）、stamp install.sh、生成 checksums.txt、
  **新面板 × 上一 tag agent 兼容性 e2e 回归**后发布 GitHub Release。
- **协议只增不改**：`src/shared/messages.go` 头注释载明演化规则，PR 由
  `scripts/check-protocol-compat.sh`（protocol-check workflow）强制"协议行只增不减"；
  agent 对未知命令回执 `unsupported command`，面板据此终态不重试。
- **兼容窗口**：面板允许领先 agent 一个发布位次（v0.0.x 形态比 patch 差）。hello 握手时
  判定：主版本不符拒绝连接；落后超窗口置 `upgrade_needed`——常规命令滞留，
  仅放行 `upgrade_agent` / `uninstall`，UI 显示"agent 需升级"。
- **agent 自升级**：服务器页"升级 agent"下发 `upgrade_agent`，agent 从 GitHub release
  下载目标版本、校验 checksums.txt 后原子自替换并退出（systemd 拉起完成升级）。

## 开发

```bash
# 构建
go build ./src/backend/... ./src/agent/...
cd src/frontend && bun run build   # 含 tsc 类型检查

# 端到端验收（需本机 xray 二进制，XRAY_BIN 可覆盖）
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-xray.sh       # 基础流水线回归
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-protocols.sh  # 全协议节点 + 订阅
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-vlessenc.sh   # VLESS Encryption 数据通路
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-usernodes.sh  # 逐节点用户分配
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-telemetry.sh  # 流量统计 + 主机遥测
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-reconcile.sh  # 配置漂移检测与修复
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-upgrade.sh    # xray 升级（本地镜像，无外网）
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-tls.sh        # 面板 TLS / wss
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-links.sh      # 分享链接订阅 + userinfo 头/落地页/用户有效期
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-settings.sh   # 设置页 / 改密 / TLS 域名路径模式 / 自重启
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-alerts.sh     # 事件告警（webhook/防抖/三类事件）+ SQLite 备份下载
```

Agent 常用参数：`-panel`（面板 WS 地址）、`-token`（bootstrap token）、`-xray-release-base`
（xray 下载镜像源）、`-telemetry-interval` / `-drift-interval`（遥测/漂移检测间隔）。

详细设计见 [framework-design.md](framework-design.md)。

## 已知问题

| 项 | 说明 |
|---|---|
| trojan/vmess over Reality | 非标准 TLS 证书组合，依赖客户端 reality 支持（mihomo 系可用） |
| socks/http 明文 | 仅账密认证无加密，不应暴露于不可信网络 |
| ss 无 Reality | 仅 AEAD 加密；且 ss 已被 xray 标记 deprecated，向导不再提供新建 |
| ws/h2/kcp/httpupgrade 不开放 | 与 Reality 不兼容（xray 官方仅 tcp/gRPC/XHTTP；gRPC 亦已 deprecated 被屏蔽） |
| fallbacks 等高级选项未开放 | vless/trojan 的 fallbacks、xhttp extra 参数等暂不支持 |
| 流量仅统计无配额 | 超限不强制停用；配额决策见 framework-design.md §14 |
| 漂移检测基线 | agent 停机期间的外部改动无法区分，以启动时文件为基线 |
| xray 版本兼容 | 新版 xray-core 已移除 vmess alterId（服务端），mihomo 客户端订阅仍携带 `alterId: 0` |
| 单管理员 | 多管理员/RBAC 决策不做；fallback 传输（gRPC/HTTP）与 NAT 中继明确不做 |
