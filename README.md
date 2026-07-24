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
  install.sh          # Agent 引导安装脚本（由面板托管，注入二进制 SHA256）
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

**运维**

- 流量统计（节点/用户双维度，xray stats 采集）与主机遥测（load/CPU/内存）
- 配置漂移 reconcile：外部篡改 xray 配置自动检测上报，一键修复（重放节点）
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

### 安装 Agent（受控服务器）

面板"添加服务器"生成一行安装命令，在受控机执行：

```bash
curl -fsSL http://<面板地址>/install.sh | bash -s -- \
  --panel http://<面板地址> --token <bootstrap token> --xray-version latest
```

脚本完成：安装指定版本 xray（校验官方 .dgst）→ 安装 agent（校验面板注入的 SHA256）→
注册 systemd → 首连换发长期凭证。重装自动先停旧服务并清除旧 state。

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
XRAY_BIN=/usr/local/bin/xray bash scripts/dev-e2e-links.sh      # 分享链接订阅
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
