# 安全

> 拆自[框架设计索引](../framework-design.md)，对应原 §12。服务器测试侧的安全约束另见[服务器测试设计](../server-testing-design.md)。

- 面板 TLS 已实现，四种部署形态：
  - **自带证书**：`-tls-cert`/`-tls-key` 启动参数指定证书与私钥（须为受信 CA 签发，agent 走系统 CA 校验 wss）；
  - **ACME 自动证书**：`-tls-acme-domain`（Let's Encrypt，TLS-ALPN-01 挑战，仅需 443 公网可达，无需 80 端口），缓存目录 `-tls-acme-cache`；
  - **域名路径模式**（设置页 `tls_mode=path`）：按域名从 `-tls-dir`（安装器统一为数据目录下 `certs/`）读取 `<域名>/fullchain.pem` 与 `privkey.pem`（certbot 风格）；外部 ACME（如 `latx cert`）续期替换文件后下一次 TLS 握手自动加载新证书，免重启（加载失败回退已缓存证书，握手不中断）；
  - **反向代理终止 TLS**（推荐生产形态，如 docker + openresty/nginx 管理证书）：面板保持 HTTP，
    反代整个站点并为 `/api/agent/ws` 转发 Upgrade/Connection 头，
    并以 `-public-url https://域名` 或 `X-Forwarded-Proto: https` 告知面板生成 https 链接。
- HTTPS（含反代）下会话 cookie 带 `Secure`；安装命令/订阅链接按上述推断生成 `https://`/`wss://`。
- 会话签名密钥为独立随机值，持久化于 settings（`session_secret`），不派生自口令材料；
  改密即轮换，全部会话失效需重新登录。
- 登录限流为 per-IP（5 次/分钟 → 封 5 分钟）与 per-username（10 次/5 分钟 → 封 15 分钟）
  双桶并存，任一命中即拒（429 + `Retry-After`），防伪造 `X-Forwarded-For` 绕过 per-IP 桶；
  bcrypt 校验另有并发上限。
- 全站统一输出安全响应头四件套：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、
  `Referrer-Policy: strict-origin-when-cross-origin`、CSP `frame-ancestors 'none'`。
- agent 与面板 Release 二进制的完整性由 `checksums.txt` 的 **SHA256 校验**保障，二进制只从
  GitHub Release 获取；安装/管理脚本（`install-panel.sh`/`install-agent.sh`/`latx.sh`/`latx-ag.sh`）
  同为 Release 资产并纳入 `checksums.txt`，根 `install.sh` 优先取 Release 资产（回退 Git tag
  原始文件），执行前校验，不匹配即中止（旧版本 Release 无脚本条目时警告并跳过校验，保持可安装）。
- 出向 HTTP 拨号带 SSRF 防护：外部订阅拉取、CDN 节点目录、告警 webhook、订阅模板刷新等生产
  客户端经 requester 的 `GuardedDialContext` 先解析目标主机名，命中回环/内网/保留地址段即拒连
  （防域名解析绕过 URL 层校验）；e2e 可经 `LATX_ALLOW_PRIVATE_OUTBOUND=1` 放行（仅限测试，
  生产严禁设置）。
- Agent 的 bootstrap/长期 token 经 `LATTIX_TOKEN` 环境变量注入（systemd EnvironmentFile /
  用户态守护脚本），不出现在进程命令行参数。
- 落地页客户端下载的新建任务按订阅 token 滑动窗口限流（每 token 10 次/小时），超限返回
  429 + `Retry-After`；去重命中、票据签发与文件下载（含 Range 续传）不计数（见[订阅](subscription.md)）。
- 匿名订阅端点（`/sub/{token}` 与 `/api/sub/{token}/*`）的内部错误对外收敛为通用文案，
  细节只写进程日志。
- 服务器测试的上游 ip.sh 脚本钉定版本，下载与缓存均强制 SHA256 校验（见
  [服务器测试设计](../server-testing-design.md)）。
- Reality 私钥永不出服务器（见[控制通道·虚拟配置](control-channel.md)）；VLESS Encryption 的 decryption 私钥侧同理（见[全协议向导](protocol-wizard.md)）。
- Agent 能力面收敛：只执行 xray 配置落地、服务重启、状态上报、自卸载，不接受任意命令。
