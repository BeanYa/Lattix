# Lattix

多服务器 xray 代理管理面板：主控面板（Backend）统一管理多台受控服务器（Agent），
可视化创建代理节点，按用户生成订阅。

- 单条 WebSocket 长连接承担全部双向通信（Agent 主动拨出，Backend 永不外连）
- 虚拟配置模板下发，Agent 落地为 xray 实际配置并上报生效值
- 节点创建/删除、用户增删全部零重启热操作（xray gRPC API），重启兜底 + 失败回滚
- 参考 3x-ui / s-ui 的交互模式，不做商业化

## 快速开始

面板支持 `linux/amd64` 与 `linux/arm64`，安装时需要 `root` 或 `sudo`，并需要
`curl`。推荐使用 Docker Compose；若希望直接由 systemd 托管，也可选择原生安装。

### 交互式安装（推荐）

在目标 Linux 服务器执行：

```bash
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh | bash
```

选择面板安装模式后，可继续设置部署地址、端口、管理员账号密码和配置目录，直接回车则
采用对应模式的默认值。安装完成后，终端会输出访问地址和管理员凭据。Docker
模式默认只监听 `127.0.0.1:8080`，需通过 Nginx/OpenResty 反向代理
对外提供服务；原生模式默认监听 `0.0.0.0:8080`。

### 非交互式安装

```bash
# Docker Compose（推荐；主机需已有 Docker Engine 与 Compose 插件）
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode docker

# Docker Compose，并在缺少 Docker 时自动安装
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode docker --install-docker

# 原生二进制 + systemd
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode native
```

不传 `--version` 时安装最新稳定 Release；如需固定版本，追加
`--version vX.Y.Z`。统一入口优先从该版本 Release 资产加载安装实现（无资产时
回退对应 Git tag 的原始文件），并按 Release `checksums.txt` 校验子安装脚本与
下载的 Release 文件，避免脚本与程序版本不一致。

### 脚本选项

| 选项 | 是否必需 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--mode docker\|native` | 是 | 无 | 选择 Docker Compose 或原生 systemd 安装 |
| `--version vX.Y.Z` | 否 | 最新稳定版 | 安装指定 Release |
| `--install-docker` | 否 | 关闭 | Docker 缺失时通过 `get.docker.com` 安装并启动；仅 Docker 模式有效 |
| `--bind <地址>` | 否 | Docker: `127.0.0.1`；原生: `0.0.0.0` | 面板监听地址 |
| `--port <端口>` | 否 | `8080` | 面板监听端口，范围 `1–65535` |
| `--admin-user <用户名>` | 否 | `admin` | 初始管理员用户名 |
| `--admin-pass <密码>` | 否 | 随机 8 位字母 | 初始管理员密码；生产环境建议显式设置强密码 |
| `--public-url <URL>` | 否 | 空 | 面板对外访问地址，反向代理或公网地址与监听地址不同时应设置 |
| `--config-dir <绝对路径>` | 否 | Docker: `/opt/lattix-panel`；原生: `/usr/local/lattix-panel` | Compose/程序、配置和持久数据的宿主机目录；路径不能包含空白 |

例如：

```bash
curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh |
  bash -s -- panel --mode docker \
  --bind 127.0.0.1 --port 8080 \
  --admin-user admin --admin-pass 'change-this-password' \
  --config-dir /opt/lattix-panel \
  --public-url https://panel.example.com
```

### Docker 与原生安装的区别

| | Docker Compose（推荐） | 原生二进制 + systemd |
| --- | --- | --- |
| 运行方式 | GHCR 镜像，非 root 容器 | Release 二进制，systemd 服务 |
| 前置条件 | Docker Engine + Compose 插件 | systemd、`curl`、`tar`、`sha256sum` |
| 默认监听 | `127.0.0.1:8080` | `0.0.0.0:8080` |
| 安装位置 | `/opt/lattix-panel/` | `/usr/local/lattix-panel/` |
| 持久数据 | `/opt/lattix-panel/data/` | `/usr/local/lattix-panel/data/` |
| 配置文件 | `/opt/lattix-panel/config/.env` | `/usr/local/lattix-panel/config.env` |
| 主机运维命令 | 使用 `docker compose` | 提供 `latx` |
| 对宿主机的改动 | 创建目录并启动容器；只有明确同意时才安装 Docker | 安装二进制、`latx` 并注册 systemd unit |

重复安装时两种模式都会保留数据。Docker 模式还会沿用已有 `.env` 中的监听、端口、
账号、密码和对外地址，命令行参数优先；原生模式保留数据、证书、ACME 缓存以及已有
账号、密码和对外地址，监听地址与端口未指定时恢复默认值。

更完整的反向代理、ACME、证书路径、更新行为和部署限制见
[部署边界](docs/KNOWN_ISSUES.md)。

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

## 功能

**管理界面**

- 可插拔主题系统：默认 Apple HIG 设计语言，经典 Cream Grid 作为可选主题安装；
  设计主题与浅色/深色外观两个维度在顶栏运行时切换并持久化（见
  [前端开发](docs/frontend.md)），不依赖外部字体/资源 CDN
- 仪表盘提供可旋转的 low-poly 地球链路拓扑，根据服务器地理位置显示节点、状态和独立动画链路
- 地球节点支持地平线淡入淡出，云层独立运动并在拖动时保留惯性

**链路与协议**

- 统一链路页：创建时选择直连或中转；直连仅一台服务器，中转按入口 → 中转(≤2) → 出口展示
- 直连支持 vless / socks / http / dokodemo-door，中转出口支持 vless / socks / http（vmess / trojan / shadowsocks 与 gRPC
  传输已被 xray 标记 deprecated，API 保留兼容但向导不再提供新建）
- Reality 安全层，传输 tcp / XHTTP（gRPC 已废弃屏蔽）；dest 预检 + 白名单 fallback
- VLESS 详细选项：flow（vision/无）、XHTTP path/mode/host、uTLS 指纹
- VLESS Encryption（mlkem768 后量子 / x25519 认证），可与 vision 组合（1-RTT）
- 链路名称模板：直连默认 `{{COUNTRY_FLAG}}{{LOCATION}}-Direct`，中转默认
  `{{EXIT.COUNTRY_FLAG}}-Out`；支持 `ENTRY/EXIT/HOP[n]` 服务器对象属性、0 起始 Tag 索引、
  光标补全、实时预览与前后端严格校验
- 服务器创建/编辑维护标准 Country 与 Location；城市选择器辅助输入，仍允许自定义机房位置
- 状态统一展示为部署中 / 正常 / 异常 / 降级，失败详情 + 重试链路

**代理链与 NAT（§21，v0.0.3）**

- 代理链：入口 → 中间跳(≤2) → 出口，上限 4 跳；端到端加密（客户端协议在出口终止，
  入口/中间跳纯 TCP 透传只见密文），订阅只见入口
- NAT 机器两档：受限直连（共享 IP + IDC 映射端口段，支持非 1:1 映射）与仅出口
  （全 CGNAT，xray reverse bridge 反向上来）；隧道口为独立 VLESS+Reality inbound
- 链级状态机 + 逐跳独立重试；跳离线自动 degraded（订阅不剔除，客户端测速规避）+ 告警

**用户与订阅**

- 逐链路用户分配（默认全关）：用户勾选直连/中转链路，底层继续按业务 node_id 差量扇出
- 用户凭证复用 UUID：vless id / trojan 密码 / ss 派生密钥 / socks+http 账密
- 订阅：`GET /sub/{token}` 按 UA 或 `format` 返回 Mihomo、sing-box、Quantumult X、
  Quantumult X 完整配置或 base64 分享链接；浏览器仍返回订阅落地页
- 订阅分流：内置 Minimal/Balanced/Comprehensive 与分类勾选，支持 Lattix 中立 YAML、
  ACL4SSR 社区模板和客户端原生覆盖；模板/规则缓存与用户发布快照相互隔离
- 用户订阅文件预生成并原子缓存，公开地址只读 published revision；模板页支持未填充预览，
  用户页支持实际发布结果预览和手动重新生成。详见 [订阅分流与模板](docs/subscription-routing-design.md)
- 订阅响应头 `subscription-userinfo`（已用上下行流量、到期时刻）与
  `profile-update-interval: 24`，客户端自动展示用量并按天刷新
- 用户有效期：创建/编辑可设到期时刻，到期自动停权（订阅保留但节点为空，
  sweeper 扇出 user.remove），延长或清除有效期自动恢复（扇出 user.add）
- 用户停用/启用开关：停用立即停权（扇出 user.remove、订阅节点清空、落地页显示"已停用"），
  启用恢复；与到期停权正交，两者都解除才恢复且不会重复扇出
- 订阅落地页：浏览器打开 `GET /sub/{token}` 得到自包含页面（用量/有效期/节点数、
  订阅地址复制、二维码、clash/mihomo 一键导入），不依赖任何外网资源
- 落地页客户端下载：面板代取 GitHub Release 客户端安装包并校验发布方 SHA-256
  （缓存 72 小时、上限 512 MiB），再以 3 小时会话票据直链交给浏览器原生下载，
  天然支持断点续传；票据与订阅 token、任务双向绑定，过期即 403
- 外部订阅：管理页导入第三方订阅 URL（base64 分享链接 / Clash-mihomo YAML /
  v2rayN 自定义格式，12 种协议），解析节点保存到外部链路表，订阅信息（流量/到期/节点数）
  保存到外部订阅表；支持自定义 UA、跳过证书校验与手动/定时同步（每订阅可配间隔）
- 用户外部订阅分配：以订阅为单位引入用户订阅（叠加/并入/附加三模式），
  `subscription-userinfo` 头按模式实时合并外部额度（剩余 0 封底仅展示），
  外部同步完成后自动重发布所有关联用户
- 分组：链路分组编排共享入口链路与外部订阅（外部订阅整体原子参与，移除即整组节点消失），
  用户分组把用户编组并关联链路分组；组内用户订阅由分组派生（直接分配被遮蔽，移出分组恢复），
  分组或相关服务器/外部订阅更新自动触发组内用户订阅重发布

**运维**

- 成本统计：对启用统计计费的服务器按日/月/年周期摊算成本（开通日至服务截止，含推定
  有效宽限），按统计币种换算（公共汇率 / 自定义锚点双口径），堆叠柱状图 / 占比环形图 /
  服务器汇总 / 周期 × 服务器明细矩阵；分「已生效成本」与「计算成本」两个口径，后者对
  未到期服务器按日成本 × 周期天数（日 1 / 月 30 / 年 365）估算每周期成本
- 流量统计（节点/用户双维度，xray stats 采集）与服务器探针（CPU/负载/内存/磁盘/默认出口网卡/uptime/Agent→Panel 延迟，24 小时历史）
- Panel 生命周期与 Agent 连接状态分离：启动、更新或运行时故障不会被误判为普通离线或凭据失效；
  Agent 重连过程和鉴权拒绝可独立观测
- 旁路式操作进度观察（observe）：链路/节点增删改与重试、链路分组与用户分组操作、
  服务器修复/清理/重建、外部订阅与模板同步等长操作的响应信封携带 `observe_id`，
  前端弹窗展示阶段清单、进度与警告；观察纯尽力而为，失败不影响业务操作本身
- 配置漂移 reconcile：外部篡改 xray 配置自动检测上报，一键修复（重放节点）
- 事件告警：服务器离线 / 配置漂移 / 节点失败（仅状态跃迁触发，同服务器同事件
  5 分钟防抖；WS 断连经 10 秒消音窗口仍离线才判离线，秒级抖动不产生误告警或链路降级），
  Webhook + Telegram Bot 双通道，设置页配置并可发测试消息
- 操作日志与请求日志：操作审计使用独立 SQLite，请求记录使用分段 JSONL；支持过滤、
  独立容量限制和清空，敏感凭证不落日志，业务 SQLite 备份不包含日志
- SQLite 备份：设置页"面板维护"一键下载（`GET /api/backup/download`，VACUUM INTO 一致性快照）
- xray 版本升级管理：面板下发，agent 校验官方 .dgst 后原子替换，失败回滚
- 离线命令队列：agent 离线期间命令滞留，重连补发；重发死信（10 次）
- 服务器引导：一行安装命令（bootstrap token 换发长期凭证），卸载可选"仅 agent"/"连同 xray"
- 计划重启优雅排空：面板以 WS 1012 通知 Agent 快速重连，不产生误离线告警或链路降级

**安全**

- 面板 TLS：自带证书 / ACME 自动证书（Let's Encrypt）/ 反代终止 TLS（openresty、nginx）
- HTTPS 下会话 cookie Secure；agent 走系统 CA 校验 wss
- install 通道以 SHA256 校验保障完整性；Reality/VLESS Encryption 私钥不出服务器
- Agent 能力面收敛：只执行配置落地、重启、上报、自卸载、升级，不接受任意命令

## 使用与运维

### 原生面板运维

原生安装后直接运行 `latx` 可选择 English / 中文交互式运维菜单（直接按 Enter 默认
English）；也可使用子命令进行自动化运维。可用 `LATX_LANG=en|zh latx` 跳过语言选择：

| 命令 | 说明 |
| --- | --- |
| `latx` | 选择语言并打开交互式运维菜单（默认 English） |
| `latx status` | 服务状态、监听端口、面板版本与地址 |
| `latx start\|stop\|restart\|enable\|disable` | systemctl 包装 |
| `latx log [-n N]` | 跟随面板日志（`-n N` 时不跟随） |
| `latx update [version]` | 从 GitHub Release 更新面板（默认 latest，amd64/arm64） |
| `latx cert <domain> [port]` | 用 acme.sh 通过 HTTP standalone 申请证书，写入面板证书目录并切 HTTPS（默认验证端口 80） |
| `latx acme <domain>` | 使用面板内置 ACME（TLS-ALPN-01）并切 HTTPS |
| `latx bbr` | 开启 BBR 拥塞控制（需要 root） |
| `latx reset-admin <newpass>` | 重置管理员密码（改密即全部会话失效） |
| `latx uninstall [--purge-db]` | 卸载面板（默认保留 DB） |
| `latx version` | latx 与面板版本 |

### 手动构建与运行面板

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

默认账号由启动参数或环境变量决定（安装器会生成密码）。正式（release）构建拒绝以公开已知的默认密码 `lattix-admin` 启动，必须显式设置 `-admin-pass`/`LATTIX_ADMIN_PASS`（dev 构建不受限）。
宿主机 Nginx/OpenResty 终止 TLS 时，只需把站点整体反代到默认的
`127.0.0.1:8080`，并为 `/api/agent/ws` 转发 Upgrade/Connection 头，同时转发
`Host`、`X-Forwarded-Proto` 和 `X-Forwarded-For`。容器内没有 Nginx，Go 进程直接
提供 SPA、API、WebSocket 和订阅内容。

面板"设置"页可在线修改：对外地址与显示时区（立即生效）；可信反代网段
（`trusted_proxies`，CIDR 逗号分隔，立即生效）——本机回环与内网/容器网段
（10/8、172.16/12、192.168/16、100.64/10 等）**默认可信**，1panel/OpenResty、
docker、局域网 nginx 反代终止 TLS 时无需配置，面板即采纳
`X-Forwarded-Proto`/`X-Forwarded-For`（安装命令协议、订阅链接与日志 IP 随之正确）；
该设置仅用于追加公网回源网段（如 CDN），公网对端直连的伪造声明始终不采信；
TLS 模式 / 自带证书 PEM /
ACME 域名（保存后重启进程生效）；管理员密码（bcrypt 落库，改密即全部会话失效）；
事件告警（Webhook 地址 / Telegram Bot token / chat_id，三项全空即关闭，可发测试消息）；
操作日志保留条数与请求日志容量；全局 Agent 重连策略、遥测上报间隔和配置漂移检测间隔。
设置页保存的值存于 SQLite `settings` 表并**优先于对应启动参数**，清除后恢复跟随启动参数。
Agent 设置使用递增 revision 同步：在线 Agent 保存后立即拉取，离线 Agent 重连后同步；
默认持续指数退避重连、每 60 秒上报遥测、每 15 秒检测配置漂移。业务 SQLite 备份
**不包含**独立存储的操作日志和请求日志。
"设置 → 面板维护"提供一键重启（`POST /api/panel/restart`）与 SQLite 备份下载
（`GET /api/backup/download`）：systemd 托管时退出后由
systemd 拉起；Docker 模式则由进程退出触发 Compose 的 `restart: unless-stopped`，
从同一容器内已替换的二进制启动。非托管开发模式才自派生新进程接管。

### Panel 生命周期与 Agent 连接

Panel 维护全局 `startup | active | updating | faulted` 生命周期，Agent 在此基础上独立维护
`never_connected | connecting | reconnecting | online | offline | auth_rejected` 连接状态。
`startup` 等待初始化完成，`active` 正常提供服务，`updating` 仅协调更新期间需要暂停的动作，
`faulted` 表示关键运行时错误并等待恢复。Panel 恢复后 Agent 通过低频重试重新连接，无回滚状态。

Agent 通过 HTTP Upgrade 的 Bearer 凭据鉴权。bootstrap token 只用于首次接入，并通过
`agent.session.open` / `agent.credential.commit` 两阶段换发长期凭据；后续重连沿用长期凭据。
可信且结构完整的 HTTP 403 会进入 `auth_rejected`，HTTP 503 或普通网络错误按暂时故障重试。

Panel 进入 `updating` 时暂停 Agent→Panel 延迟探测并清理待完成探针，但保留最近 3 个完成样本；
回到 `active` 后，各 Agent 在 0–30 秒内随机恢复探测，并继续向原窗口追加数据。liveness 保活
始终独立运行，延迟探测超时不会关闭 WebSocket。删除服务器时，Panel 使用同一请求 ID 尽力
投递卸载命令，最多尝试 10 次（100ms 指数退避，单次上限 10 秒）；收到回执或耗尽次数后删除
数据并使凭据失效。

完整状态转换、生命周期版本、会话握手与异常处理见
[Panel 生命周期与 Agent 连接状态机](docs/panel-lifecycle-state-machine-design.md)。

### 服务器测试

服务器详情页支持由管理员选择 IP 质量、TCP/大包、教育网、国际连通、IPv4/IPv6 回程与
单线程测速（speedtest.net 目标经 Ookla CLI 执行，回程探测含 TCP 返回路径 traceroute），
整组选择作为一个 Agent 原子任务执行。Panel 维护测试目录并只保存每台服务器
最近一次结果；Agent 独立运行、尽力上报进度，并在断线期间持久保存权威最终报告。NAT 机型
默认只选择 IP 质量，其他分类需确认可能不可用和流量风险后再运行。完整的数据源、权限降级、
隔离、队列、重启恢复与结果协议见
[服务器测试设计与实现契约](docs/server-testing-design.md)。

TLS 另支持**域名路径模式**（`tls_mode=path`）：面板按域名从证书根目录读取
`<tls-dir>/<域名>/fullchain.pem` 与 `privkey.pem`（certbot 风格，目录由 `-tls-dir`
指定；安装器统一设置为数据目录下的 `certs`，Docker 容器内为 `/data/certs`，
宿主机对应 `/opt/lattix-panel/data/certs`）。外部 ACME（如 `latx cert`）
申请/续期后直接写入该目录：保存设置时只填域名；续期替换文件后下一次 TLS 握手即
自动加载新证书，**免重启**（加载失败时回退已缓存证书，握手不中断）。

### 安装 Agent（受控服务器）

Agent 不提供用户自行触发的安装入口。请在面板"添加服务器"后，将面板生成的完整安装
命令原样放到受控机执行；不要手工拼装命令或复用其他服务器的命令。正式版本
（release 构建）的下发命令通过根安装器钉到面板当前版本，安装实现与 agent 二进制
天然同版。

根入口优先从对应版本 Release 资产加载 `install-agent.sh`（无资产时回退 Git tag 的
`scripts/install-agent.sh`），执行前按 Release `checksums.txt` 校验脚本本身；
脚本从同版本 GitHub Release
下载 `lattix-agent-linux-<arch>.tar.gz`（agent + latx-ag）并校验 `checksums.txt`。
面板不再托管安装脚本或二进制资源，也没有下载源切换设置。

脚本完成：安装指定版本 xray（校验官方 `.dgst`）→ 安装 agent 与 `latx-ag` 节点管理程序
（校验 SHA256）→ 注册 systemd → best-effort 开启 TCP BBR → 首连换发长期凭证。
system 模式文件集中在 `/opt/lattix-agent/{bin,config,data,logs}`，并创建
`/usr/local/bin/latx-ag` 软链接；用户模式使用 `~/.lattix-agent/`。
重装自动先停旧服务并保留 state/settings，由 token 中的面板身份与 epoch 安全选择凭证。
BBR 已启用时不改现有配置；否则尝试加载
`tcp_bbr`，将 `net.core.default_qdisc=fq` 与
`net.ipv4.tcp_congestion_control=bbr` 持久化到
`/etc/sysctl.d/99-lattix-bbr.conf`，并只即时设置这两个参数。`fq` 设置失败不影响
BBR 成功判定。

BBR 是宿主内核能力。Podman/LXC 等容器按实际能力尝试：宿主未提供 BBR、容器未获
sysctl 权限或安装以非 root 运行时都不会阻断 Agent 安装；脚本仅在 BBR 最终未生效或
无法持久化时，于全部安装步骤完成后输出一行包含具体原因的 `WARNING`。安装成功输出
还包括面板地址、agent 服务状态、xray 版本与 `latx-ag` 运维提示块。卸载 Agent 不撤销
机器级 BBR 配置。

安装后用 `latx-ag`（`/usr/local/bin/latx-ag`）运维本节点：

| 命令 | 说明 |
| --- | --- |
| `latx-ag status` | agent/xray 服务状态、版本、面板地址、服务器 ID 与配置指纹 |
| `latx-ag start\|stop\|restart\|enable\|disable` | systemctl 包装（unit `lattix-agent`） |
| `latx-ag log [-n N]` / `latx-ag log-xray [-n N]` | 跟随 agent / xray 日志（`-n N` 时不跟随） |
| `latx-ag update [version]` | 从 GitHub release 更新 agent（默认 latest，预检 `-version` 后停服替换） |
| `latx-ag xray-update [version]` | 更新 xray（官方 .dgst 校验 SHA2-256，失败回滚 .bak；`XRAY_RELEASE_BASE` 可指向镜像） |
| `latx-ag uninstall [--purge-xray]` | 卸载 agent（默认保留 xray 与节点运行；`--purge-xray` 连同 xray 删除） |
| `latx-ag version` | latx-ag、agent 与 xray 版本 |

### CI/CD 与版本发布（§18）

- **发版**：push `v*` tag 触发 `.github/workflows/release.yml`——构建前后端并注入版本
  （前端嵌入单个 Go 面板二进制）、打包 amd64/arm64 的 panel 与 agent tarball、
  生成 `checksums.txt`，并以 matrix 执行 `scripts/e2e/` 中可在 CI 环境运行的
  e2e 回归（`telemetry.sh`、`vlessenc.sh` 依赖真实外网数据通路不进 CI；
  `chains.sh`、`protocols.sh`、`groups.sh` 暂被订阅发布/SSRF 防护问题阻塞）。通过后
  先发布 `ghcr.io/beanya/lattix:<version>` 与 `latest` 多架构镜像，再发布
  GitHub Release。Release 另附带仓库原样的 `install-panel.sh` /
  `install-agent.sh` / `latx.sh` / `latx-ag.sh`，其 sha256 一并纳入
  `checksums.txt`，供根安装器校验子安装脚本。Release notes 依次取自
  `docs/CHANGELOG.md` 的对应版本段、`[Unreleased]` 段（皆空回退自动生成），
  `scripts/dev/changelog-cut.sh vX.Y.Z` 发布前后执行均可：日常变更累计在
  `[Unreleased]`，固化后归入对应版本段。
- **协议同步发布**：Panel、Frontend 和 Agent 按同一版本发布，不维护旧 HTTP/WS 协议
  兼容窗口；Agent 对格式有效但未知的动作返回 `UNSUPPORTED_ACTION`。业务数据库在新面板
  启动、对外提供服务前自动执行一次事务化结构迁移，迁移失败会回滚并阻止面板启动。
- **agent 自升级**：服务器页"升级 agent"下发 `agent.upgrade`，agent 从 GitHub release
  下载目标版本、校验 checksums.txt 后原子自替换并退出（systemd 拉起完成升级）。

## 开发

```bash
# 构建
go build ./src/backend/... ./src/agent/...
cd src/frontend && bun run build   # 含 tsc 类型检查

# 端到端验收（需本机 xray 二进制，XRAY_BIN 可覆盖）
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/xray.sh           # 基础流水线回归
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/protocols.sh      # 全协议节点 + 订阅
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/vlessenc.sh       # VLESS Encryption 数据通路
XRAY_BIN=/usr/local/bin/xray bash scripts/e2e/groups.sh         # 分组：链路分组/用户分组/原子外部订阅/触发重发布
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

Agent 常用参数：`-panel`（固定面板 WS 地址）、`-token`（bootstrap token）、
`-state` / `-settings`（本地状态与面板同步设置）及 `-xray-release-base`（xray 下载镜像源）。
重连、遥测与漂移检测间隔由面板“设置 → Agent”统一下发。

详细设计见 [docs/framework-design.md](docs/framework-design.md)，Panel 与 Agent 状态机见
[docs/panel-lifecycle-state-machine-design.md](docs/panel-lifecycle-state-machine-design.md)，链路编辑、
离线 revision 与流量口径见
[docs/chain-revisions-traffic-design.md](docs/chain-revisions-traffic-design.md)，前端开发命令见
[docs/frontend.md](docs/frontend.md)。

## 已知问题

| 项 | 说明 |
|---|---|
| trojan/vmess over Reality | 非标准 TLS 证书组合，依赖客户端 reality 支持（mihomo 系可用） |
| socks/http 明文 | 仅账密认证无加密，不应暴露于不可信网络 |
| ss 无 Reality | 仅 AEAD 加密；且 ss 已被 xray 标记 deprecated，向导不再提供新建 |
| ws/h2/kcp/httpupgrade 不开放 | 与 Reality 不兼容（xray 官方仅 tcp/gRPC/XHTTP；gRPC 亦已 deprecated 被屏蔽） |
| fallbacks 等高级选项未开放 | vless/trojan 的 fallbacks、xhttp extra 参数等暂不支持 |
| 流量仅统计无配额 | 超限不强制停用；配额决策见 [设计文档 §14](docs/framework-design.md#14-mvp-已知问题与后续迭代目标) |
| 漂移检测基线 | agent 停机期间的外部改动无法区分，以启动时文件为基线 |
| xray 版本兼容 | 新版 xray-core 已移除 vmess alterId（服务端），mihomo 客户端订阅仍携带 `alterId: 0` |
| 单管理员 | 多管理员/RBAC 决策不做；fallback 传输（gRPC/HTTP）明确不做（NAT/中继已实现，见 §21） |

部署与更新边界见 [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)。
