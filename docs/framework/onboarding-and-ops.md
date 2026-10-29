# 服务器引导与运维程序

> 拆自[框架设计索引](../framework-design.md)，对应原 §11（服务器引导流程）、§20（面板侧引导与 latx 运维程序）。安装脚本参数与运维命令的用户手册见[安装与运维](../installation.md)。

## 服务器引导流程

1. 面板"添加服务器"填写别名、公网地址与 **xray 版本**（默认 `latest`，也可指定具体版本），生成一次性 **bootstrap token** 与一行安装命令（另含机器类型与 NAT 可用端口段，见[代理链与 NAT](chain-nat.md)；均为面板侧元数据，不下发到 agent，引导流程不变）；
2. 面板生成的命令调用仓库根 `install.sh agent --version <面板版本>`。根入口优先取该
   版本 Release 资产中的 `install-agent.sh`（回退 Git tag 原始文件），执行前按同版本
   `checksums.txt` 校验 SHA256，后者按创建时指定的 xray 版本安装
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

凭证刷新立即递增 credential epoch、撤销旧 token 并以 WS 4001 关闭现有连接。
Agent 收到该明确拒绝后停止自动连接；管理员必须执行新的安装命令。同面板刷新保留 Xray
配置，跨面板仅在新面板认证成功后清理旧受管状态。

## 面板侧引导与 latx 运维程序

面板自身的安装/运维与 agent 引导同形态：release 钉版脚本 + checksums 校验 +
单文件 bash 管理程序。

**统一安装入口**：用户只执行仓库根 `install.sh`。无参数时直接进入面板的
Docker/原生模式向导；面板自动化安装使用 `panel` 子命令。`agent` 子命令仅供面板
"添加服务器"生成的安装命令调用，不作为用户自行触发的安装入口。版本默认取最新稳定
Release，显式 `--version` 可钉版。根入口再加载对应版本的子安装器：优先取该版本
Release 资产（`install-panel.sh`/`install-agent.sh` 与 `latx.sh`/`latx-ag.sh` 均为
Release 资产，sha256 纳入 `checksums.txt`），回退对应 Git tag 的原始文件；执行前按
同版本 `checksums.txt` 校验 SHA256，不匹配即中止（旧版本 Release 无脚本条目时警告
并跳过校验，保持钉版旧版本可安装）。

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
`lattix-backend -reset-admin`，见下）、`uninstall [--purge-db] [--yes]`（确认后停服删 unit
与安装根目录，默认保留 DB 并提示路径）、`version`。unit 名 `LATX_UNIT`、
面板地址 `LATX_PANEL_URL`（默认 `http://127.0.0.1:8080`）可覆盖。

**latx-ag**（`scripts/latx-ag.sh`，CI stamp 后由 agent install.sh 安装为
`/usr/local/bin/latx-ag`）：节点侧同款单文件 bash 管理程序。子命令 `status`
（agent/xray 服务状态与版本、面板地址——读 install.sh 写入的 env 文件、state 中的
服务器 ID、配置指纹；漂移基线在 agent 内存中不落盘，故仅显示指纹）、
`start|stop|restart|enable|disable`（systemd 包装，unit `lattix-agent`，
`LATX_AG_UNIT` 覆盖；无 systemd 时自动按用户态模式管理守护脚本与 crontab `@reboot`）、
`log` / `log-xray`（journalctl，`-n N` 不跟随）、
`update [version]`（latest 经 GitHub API 解析，下载 agent 包
`lattix-agent-linux-<arch>.tar.gz` + checksums.txt 校验 → 解包 →
**预检**新二进制 `-version` → 停服前/启动前自动执行兼容性迁移 → 停服替换 → 启服校验版本）、
`migrate [--from V] [--phase pre|post] [--data-dir DIR]`（兼容性迁移注册表；
三条升级路径自动执行，裸执行为修复模式，见[框架索引·仓库结构](../framework-design.md)与[已知问题](../KNOWN_ISSUES.md)）、
`xray-update [version]`
（官方 .dgst 校验 SHA2-256，拿不到校验文件即失败，与 agent upgrade.go 同语义；
备份 .bak → 替换 → 重启 → 版本校验，失败回滚；`XRAY_RELEASE_BASE` 对齐
`-xray-release-base` 镜像语义）、`uninstall [--purge-xray] [--yes]`（确认后卸载；清理清单与
agent 远程自卸载双向对齐：二进制/bak、runner、env/state/settings/connection/
command-queue、lock、日志；purge 时连同 xray 与 APP_ROOT；默认仅 agent，xray
与节点继续运行）、`version`。
latx-ag 随 GitHub Release 的 agent 包 `lattix-agent-linux-<arch>.tar.gz` 分发，
统一经 checksums.txt 校验。
install.sh 成功输出面板地址 / agent 状态 / xray 版本与 latx-ag 运维提示块。
`LATX_DEV=1` + `LATX_PREFIX` 路径前缀提供与 install-panel.sh 同款的 DEV 降级
（e2e/install-agent.sh 走此路径）。`dev/test-install-agent-bbr.sh` 通过隔离的
sysctl/modprobe/config 替身覆盖 BBR 已启用、`fq` 失败、内核不支持、容器拒绝 sysctl
及单行原因 WARNING，不触碰测试宿主的真实网络参数。

**`-reset-admin` 启动参数**：`lattix-backend -reset-admin <newpass> -db <path>`
与设置页改密同等语义（bcrypt 哈希写 `settings`，≥8 位校验；并删除会话签名密钥使其
下次使用时重新生成——会话签名密钥为独立随机值，不派生自口令材料，见[安全](security.md)），写完输出提示
即退出，不启动面板；面板运行中执行安全（busy_timeout）；密钥每次请求读库，删键后
下一个请求即重新生成，全部会话立即失效，无需重启面板。
