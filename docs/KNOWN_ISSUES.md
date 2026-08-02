# 已知问题与部署边界

本文记录 Lattix 当前明确接受的限制。它们不是安装失败时的临时排障建议，而是部署模型本身的边界。

## Docker 页面更新与镜像版本可能短暂不一致

Docker 模式采用与原生二进制相同的自更新流程：面板从 GitHub Release 下载当前架构的面板二进制，校验 `checksums.txt` 后原子替换容器内程序；页面执行重启时，面板进程退出，由 Compose 的 `restart: unless-stopped` 拉起同一容器。

该流程不会调用 Docker API，也不会拉取或改写 GHCR 镜像。因此：

- 正常的容器停止/启动、进程重启和宿主机重启会保留更新后的程序；
- 删除容器、强制重建容器或迁移部署时，会重新使用 `config/.env` 中指定的镜像版本；
- 重建前如需保持页面更新后的版本，应先把 `LATTIX_VERSION` 改为目标版本，再执行 `docker compose --env-file config/.env pull && docker compose --env-file config/.env up -d`。

这是为了避免挂载 Docker Socket 或安装宿主机 updater 所做的明确取舍。

## Docker 模式不提供宿主机运维能力

Docker 安装只创建 Compose 配置、`config/.env` 和持久数据目录，不安装宿主机版 `latx`，不注册 Lattix systemd unit、cron 或其他服务。BBR、宿主机防火墙、Nginx、系统日志轮转等仍由宿主机管理员负责。

交互安装可以在 Docker 缺失时询问是否安装 Docker Engine 与 Compose 插件；这是唯一可能新增的宿主机服务，且必须由用户明确确认。非交互安装只有显式传入 `--install-docker` 才执行。

## 内置 ACME 需要公网 443 可达

面板内置 ACME 使用 TLS-ALPN-01。Docker 默认只把面板绑定到 `127.0.0.1:8080`，适合由宿主机 Nginx/OpenResty 终止 TLS。若改用面板内置 ACME，必须让公网 TCP 443 映射到容器内面板端口，并确保没有其他服务占用该端口。

使用宿主机 Nginx 反代时不需要启用面板自身 TLS；Nginx 必须转发 `Host`、`X-Forwarded-Proto`、`X-Forwarded-For` 和 WebSocket Upgrade 头。

## 证书路径在宿主机与容器内不同

Docker 部署的证书目录为：

- 宿主机：`/opt/lattix-panel/data/certs/`
- 容器内：`/data/certs/`

设置页和后端使用容器内路径 `/data/certs/<域名>/fullchain.pem` 与 `privkey.pem`。宿主机工具写入证书时必须使用对应的宿主机路径。ACME 缓存持久化在 `/opt/lattix-panel/data/acme-cache/`。

## GHCR 镜像必须保持公开

一键 Docker 安装不要求 GitHub Token，依赖 `ghcr.io/beanya/lattix` 可匿名拉取。如果 GitHub Container Package 被改为私有，即使 Release 正常发布，新的匿名 Docker 安装也会失败。

## 城市数据块体积较大

前端的离线国家/城市数据按需加载，但当前生成的数据块约 8 MB（gzip 约 2.3 MB）。
它不阻塞首屏，首次打开服务器国家/城市控件时会产生一次较大的下载。后续可按国家拆分，
源站已为带 hash 的 `/assets/*` 输出一年 immutable 缓存；CDN 或反向代理应遵循该响应头。

## 仅支持 Linux

面板原生包和 Docker 镜像支持 `linux/amd64`、`linux/arm64`。安装脚本不支持 macOS、Windows 或其他内核；Docker Desktop 也不属于正式支持的生产部署环境。
