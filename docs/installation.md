# 安装与运维

本文是 [README](../README.md) 快速开始的完整版：安装脚本选项、两种部署模式
对比、重装与更新行为、`latx` / `latx-ag` 运维命令、反向代理与 TLS 要点。
部署模型的边界（Docker 更新语义、ACME 端口要求、证书路径差异等）见
[已知问题与部署边界](KNOWN_ISSUES.md)。

## 系统要求

- 面板支持 `linux/amd64` 与 `linux/arm64`；安装时需要 `root` 或 `sudo`，并需要 `curl`。
- Docker 模式额外需要 Docker Engine 与 Compose 插件；缺失时可由安装器经
  `--install-docker` 自动安装（交互向导会先询问确认）。
- 原生模式额外需要 systemd、`tar`、`sha256sum`。

## 安装方式对比

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

Docker 模式不在宿主机安装 `latx`，也不注册 Lattix systemd unit；BBR、宿主机
防火墙、Nginx、日志轮转等仍由宿主机管理员负责（见
[已知问题与部署边界](KNOWN_ISSUES.md)）。

## 脚本选项

统一入口 `install.sh`：无参数时进入面板 Docker/原生模式交互向导（需要终端，
`curl ... | bash` 下向导从 `/dev/tty` 读取选择）；自动化安装使用 `panel`
子命令；`agent` 子命令仅供面板"添加服务器"生成的安装命令调用，不作为用户
自行触发的入口。

不传 `--version` 时安装最新稳定 Release；追加 `--version vX.Y.Z` 钉版。根入口
优先从该版本 Release 资产加载子安装器（无资产时回退对应 Git tag 的原始文件），
执行前按同版本 `checksums.txt` 校验 SHA256，不匹配即中止；旧版本 Release 无
脚本条目时警告并跳过校验，保持钉版旧版本可安装。

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

## 重装与数据保留

重复安装时两种模式都会保留数据。Docker 模式沿用已有 `config/.env` 中的监听、
端口、账号、密码和对外地址，命令行参数优先；原生模式保留数据、证书、ACME
缓存以及已有账号、密码和对外地址，监听地址与端口未指定时恢复默认值。

## 更新与卸载

- 原生面板：`latx update [version]` 从 GitHub Release 更新（默认 latest，
  amd64/arm64）；`latx uninstall [--purge-db]` 卸载（默认保留数据库并提示路径）。
- Docker 面板：页面"设置 → 面板维护"更新并重启；页面更新不拉取 GHCR 镜像，
  强制重建容器会恢复 `.env` 所钉版本，详见
  [已知问题与部署边界](KNOWN_ISSUES.md)。
- Agent：面板服务器页"升级 agent"下发原子自替换；也可在节点上执行
  `latx-ag update [version]`。`latx-ag uninstall [--purge-xray]` 卸载
  （默认保留 xray 与节点运行；`--purge-xray` 连同 xray 删除）。

## 原生面板运维（latx）

原生安装后直接运行 `latx` 可选择 English / 中文交互式运维菜单（直接按 Enter
默认 English）；也可使用子命令进行自动化运维。可用 `LATX_LANG=en|zh latx`
跳过语言选择：

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
| `latx reset-admin <newpass>` | 重置管理员密码并轮换会话密钥（重置即全部会话失效） |
| `latx uninstall [--purge-db]` | 卸载面板（默认保留 DB） |
| `latx version` | latx 与面板版本 |

## 安装 Agent（受控服务器）

Agent 不提供用户自行触发的安装入口。请在面板"添加服务器"后，将面板生成的
完整安装命令原样放到受控机执行；不要手工拼装命令或复用其他服务器的命令。
正式版本（release 构建）的下发命令通过根安装器钉到面板当前版本，安装实现与
agent 二进制天然同版。

脚本流程：安装指定版本 xray（校验官方 `.dgst`）→ 安装 agent 与 `latx-ag`
节点管理程序（校验 SHA256）→ 注册 systemd → best-effort 开启 TCP BBR →
首连换发长期凭证。system 模式文件集中在
`/opt/lattix-agent/{bin,config,data,logs}`，并创建 `/usr/local/bin/latx-ag`
软链接；用户模式使用 `~/.lattix-agent/`。重装自动先停旧服务并保留
state/settings，由 token 中的面板身份与 epoch 安全选择凭证。

BBR 是宿主内核能力。Podman/LXC 等容器按实际能力尝试：宿主未提供 BBR、容器
未获 sysctl 权限或安装以非 root 运行时都不会阻断 Agent 安装；脚本仅在 BBR
最终未生效或无法持久化时，于全部安装步骤完成后输出一行包含具体原因的
`WARNING`。安装成功输出还包括面板地址、agent 服务状态、xray 版本与
`latx-ag` 运维提示块。卸载 Agent 不撤销机器级 BBR 配置。

## 节点运维（latx-ag）

安装 Agent 后用 `latx-ag`（`/usr/local/bin/latx-ag`）运维本节点：

| 命令 | 说明 |
| --- | --- |
| `latx-ag status` | agent/xray 服务状态、版本、面板地址、服务器 ID 与配置指纹 |
| `latx-ag start\|stop\|restart\|enable\|disable` | systemctl 包装（unit `lattix-agent`） |
| `latx-ag log [-n N]` / `latx-ag log-xray [-n N]` | 跟随 agent / xray 日志（`-n N` 时不跟随） |
| `latx-ag update [version]` | 从 GitHub release 更新 agent（默认 latest，预检 `-version` 后停服替换） |
| `latx-ag xray-update [version]` | 更新 xray（官方 .dgst 校验 SHA2-256，失败回滚 .bak；`XRAY_RELEASE_BASE` 可指向镜像） |
| `latx-ag uninstall [--purge-xray]` | 卸载 agent（默认保留 xray 与节点运行；`--purge-xray` 连同 xray 删除） |
| `latx-ag version` | latx-ag、agent 与 xray 版本 |

## 反向代理与 TLS

Docker 模式默认只监听 `127.0.0.1:8080`，需通过宿主机 Nginx/OpenResty 反向代理
对外提供服务；原生模式默认监听 `0.0.0.0:8080`。宿主机反代终止 TLS 时，只需把
站点整体反代到面板地址，并为 `/api/agent/ws` 转发 Upgrade/Connection 头，同时
转发 `Host`、`X-Forwarded-Proto` 和 `X-Forwarded-For`。容器内没有 Nginx，Go
进程直接提供 SPA、API、WebSocket 和订阅内容。

面板 TLS 三选一：自带证书 / 内置 ACME 自动证书（Let's Encrypt，TLS-ALPN-01，
需 443 公网可达）/ 反代终止 TLS。另有域名路径模式（`tls_mode=path`）：面板按
域名从证书根目录读取 `<tls-dir>/<域名>/fullchain.pem` 与 `privkey.pem`
（certbot 风格，目录由 `-tls-dir` 指定；安装器统一设置为数据目录下的 `certs`）。
外部 ACME（如 `latx cert`）续期替换文件后，下一次 TLS 握手即自动加载新证书，
免重启（加载失败时回退已缓存证书，握手不中断）。

本机回环与内网/容器网段（10/8、172.16/12、192.168/16、100.64/10 等）默认
可信，1panel/OpenResty、docker、局域网 nginx 反代终止 TLS 时无需配置，面板即
采纳 `X-Forwarded-Proto`/`X-Forwarded-For`（安装命令协议、订阅链接与日志 IP
随之正确）；设置页"可信反代网段"（`trusted_proxies`，CIDR 逗号分隔）仅用于
追加公网回源网段（如 CDN），公网对端直连的伪造声明始终不采信。

## 设置页在线修改

面板"设置"页可在线修改：对外地址与显示时区（立即生效）；可信反代网段（立即
生效）；TLS 模式 / 自带证书 PEM / ACME 域名（保存后重启进程生效）；管理员密码
（bcrypt 落库，改密即全部会话失效）；事件告警（Webhook 地址 / Telegram Bot
token / chat_id，三项全空即关闭，可发测试消息）；操作日志保留条数与请求日志
容量；全局 Agent 重连策略、遥测上报间隔和配置漂移检测间隔。设置页保存的值存于
SQLite `settings` 表并优先于对应启动参数，清除后恢复跟随启动参数。Agent 设置
使用递增 revision 同步：在线 Agent 保存后立即拉取，离线 Agent 重连后同步；
默认持续指数退避重连、每 60 秒上报遥测、每 15 秒检测配置漂移。

"设置 → 面板维护"提供一键重启与 SQLite 备份下载（`VACUUM INTO` 一致性快照；
备份不包含独立存储的操作日志和请求日志）。systemd 托管时进程退出后由 systemd
拉起；Docker 模式则由进程退出触发 Compose 的 `restart: unless-stopped`，从同一
容器内已替换的二进制启动。
