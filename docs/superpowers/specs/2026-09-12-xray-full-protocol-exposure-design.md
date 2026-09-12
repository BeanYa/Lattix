# xray 全协议/加密能力暴露设计

日期：2026-09-12
状态：待评审

## 1. 背景与目标

部分服务商限制入站协议（只允许 hy2 / reality 类流量），而 Lattix 前端目前只暴露 vless/socks/http/dokodemo 四个协议，且 reality 仅对 vless 开放。后端实际已支持 7 协议 + reality，xray 26.x（项目 fallback 版本 26.3.27）还新增了 Hysteria2 原生入站。

目标：

1. 把 xray 已支持、且对"服务商协议限制"场景有意义的协议/传输/安全层/加密方法全部打通到链路创建表单。
2. 用户不懂协议细节：所有密钥/密码/路径类参数默认可留空自动生成，表单按协议动态渲染，只出现有意义的选项。
3. 不引入第二个 core（sing-box/hysteria 官方二进制均不做）。

非目标（YAGNI）：mkcp（header/seed 已移除，走 finalmask 复杂度高、场景小众）、quic/h2 传输（xray 已删除）、vless fallbacks、REALITY mldsa65、ECH、TUIC/AnyTLS/ShadowTLS（需 sing-box，另立项）。

## 2. 支持矩阵（目标态）

xray 26.x 要点：`network` 更名 `method`、`tcp` 更名 `raw`（旧名仍兼容，本设计继续用旧名，由现有模板管线隔离）；ws/grpc/httpupgrade 仍可用但有弃用警告；reality 只允许 raw/grpc/xhttp。

| 协议 | 可选传输 | 可选安全层 | 协议级选项 |
|---|---|---|---|
| vless | tcp / xhttp / grpc / ws / httpupgrade | reality(仅 tcp/xhttp/grpc) / tls / none¹ | flow: vision(仅 tcp+tls/reality 且未启用加密时可直转)、none；encryption: none/x25519/mlkem768 |
| vmess | 同上 | reality(仅 tcp/xhttp/grpc) / tls / none | 加密: auto/aes-128-gcm/chacha20-poly1305 |
| trojan | 同上 | reality(仅 tcp/xhttp/grpc) / tls | 无（flow 已被 xray 移除） |
| shadowsocks | 无传输层 | none | method: 2022-blake3-{aes-128-gcm,aes-256-gcm,chacha20-poly1305}、aes-128-gcm、aes-256-gcm、chacha20-ietf-poly1305 |
| hysteria2（新增） | 自带 QUIC | tls（强制） | auth 密码（自动生成）、salamander 混淆密码（可选）、brutalUp/brutalDown（可选）、udpHop 端口跳跃（可选） |
| socks / http | 无 | none | 账密（现状） |
| dokodemo-door | 无 | none | 转发目标（现状） |

¹ vless + security=none 仅在启用 VLESS Encryption 时允许（公网地址明文 vless 会被 xray 拒绝且无意义）。

normalize 兼容性规则（后端权威，前端镜像）：

- reality 只允许 vless/vmess/trojan × tcp/grpc/xhttp；选 ws/httpupgrade 时安全层只能 tls/none。
- vision flow 仅 vless + tcp + (reality|tls)。
- trojan 不允许 security=none。
- hysteria2 无 network 概念，表单隐藏传输/安全层选择，恒为 QUIC+TLS。
- ss/socks/http/dokodemo 无安全层与传输选择。

## 3. 架构

现有管线不变：panel 生成带占位符的 inbound JSON 模板 → WebSocket 下发 → agent 填充 → `xray run -test` 校验 → gRPC 热更新（不支持热操作的协议回退重启）。本设计只是把这个管线支持的模板种类扩大，并新增 TLS 证书占位符。

### 3.1 shared（`src/shared/config.go`）

- 新增 `ProtocolHysteria2 = "hysteria"`（xray 26.x 入站协议名），`Protocols` 追加。
- `Networks` 增加 `ws`、`httpupgrade`；新增 `Securities = [reality, tls, none]`、`Security` 字段入 `VirtualConfig`。
- 新增 `VMessCiphers`、`Hy2` 相关字段：`VirtualConfig` 增加 `Security`、`Cipher`(vmess)、`ObfsPassword`(salamander，空=不启用)、`UpMbps/DownMbps`、`PortHop`。
- 新占位符 `{{TLS_CERT_FILE}}`、`{{TLS_KEY_FILE}}`（agent 侧自签证书路径）。hy2 用户 auth 不需要新占位符，走现有 `{{CLIENTS}}` 机制（见 3.3）。
- `RealizedConfig` 增加：`Security`、`SNI`、`CertSHA256`（自签证书 pin）、`Cipher`、`ObfsPassword`、hy2 带宽/端口跳跃回显。vmess 的 `Cipher` 仅影响订阅输出（客户端 cipher 提示），xray 26.x 的 vmess inbound 本身无此字段。

### 3.2 panel（`src/backend/internal/panel/`）

- `nodes.go normalize()`：实现第 2 节兼容性矩阵；为每协议补默认值（密钥类留空=agent 生成）。
- `buildVirtualConfig()`：每协议 × 安全层 × 传输生成模板；新增 `tlsStreamSettings()`（tlsSettings.certificates 引用占位符路径）、`wsSettings`/`httpupgradeSettings` 分支、hysteria2 模板（settings.users + hysteriaSettings + finalmask.quicParams/udp）。
- `chains.go`：复用现状。hysteria2/vmess/trojan/ss 链路不享受 vless 共享端点（沿用非 vless 协议独立监听现状）；plaintext 特例逻辑不变。
- OpenAPI 契约更新 → 前端 `api-contract.generated.ts` 重新生成。

### 3.3 agent（`src/agent/internal/xray/`）

- `fill.go`：
  - `clientCredentialEntry` 新增 hy2 分支 `{auth, email, level:0}`（auth 为 per-user 随机串，派生方式仿 `SSUserPassword`）。
  - 新占位符填充：`{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}` → 首次填充时调用 `xray tls cert` 在配置目录生成自签 CA+证书（按节点 tag 缓存复用），计算证书 SHA256 写入 `RealizedConfig.CertSHA256`。
  - Realized 提取扩展：tlsSettings serverName、hy2 参数。
- `hot.go`：vmess/trojan 已有热操作；hy2 若 `AlterInbound` 不支持则走现有重启回退（`manager.go:212-224`），不专门适配。
- xray 版本门控：agent 上报 xray 版本（升级链路已有此信息），hy2 节点要求 ≥ 26.3.27，低于则 `ApplyNode` 返回明确错误，panel 在节点选择器中对低版本 server 禁用 hy2 选项。

### 3.4 订阅输出（`src/backend/internal/sub/`）

四种格式全部扩展，参考 `extsub` 现成的 hy2/ws 解析与再导出代码：

- `links.go`：vless/vmess/trojan 支持 ws/httpupgrade/tls 参数；新增 `hysteria2://` 链接（insecure 或 pin）。
- `sub.go`(mihomo)：hy2、ws、httpupgrade、tls(非 reality) 输出；vmess cipher。
- `singbox.go`：hy2 outbound；`buildSbTLS` 拆分 reality/普通 tls 两个变体（当前硬编码 reality，`singbox.go:118`）。
- `quanx.go`：尽力而为（vmess/socks/http 本就不支持，hy2 视 QuanX 能力输出或跳过）。
- 自签证书客户端表达：支持 pin 的格式输出 pin（mihomo `fingerprint`），不支持的输出 `allowInsecure/skip-cert-verify`，保证开箱即用。

### 3.5 前端（`src/frontend/`）

面向不懂协议的用户的表单设计：

- **协议选择器改为带说明的列表**：每项附一句话定位与徽章，例如：
  - `VLESS + Reality`（推荐·抗封锁最强）
  - `Hysteria2`（UDP 高速·需服务商放行 UDP）
  - `Trojan / VMess`（兼容性广）
  - `Shadowsocks`（轻量·特征明显）
  - `SOCKS/HTTP`、`端口转发`（特殊用途）
- **动态渲染**：选协议后只显示该协议有意义的字段；不兼容组合在前端即时禁用/自动纠正（镜像后端矩阵）。
- **默认即最优**：新链路默认 vless+reality+tcp+vision+mlkem768（维持现状默认）；密钥/密码/shortID/path 等全部可留空自动生成，界面上标注"留空自动生成"。
- **高级折叠**：fingerprint、xhttp mode/host、hy2 带宽/端口跳跃、salamander 等进"高级选项"折叠区。
- 常量与门禁更新：`DIRECT_PROTOCOLS`/`RELAY_PROTOCOLS` 扩为全量（dokodemo 仍禁多跳出口）、`NETWORKS` 加 ws/httpupgrade、`REALITY_PROTOCOLS` 扩为 vless/vmess/trojan、新增 vmess cipher、hy2 字段区。
- 编辑回填（`use-chain-form.ts:208-268` 解析模板 JSON）每协议/安全层/传输补分支。

### 3.6 安装/升级

`scripts/install-agent.sh` 不变（xray latest ≥ 26.3.27 即含 hy2）；存量 agent 通过既有 `xray.upgrade` 链路升级即可。hy2 需要 UDP 入站放行，文档补充防火墙提示。

## 4. 数据流（以新建 hy2 链路为例）

1. 前端提交 `protocol=hysteria`，auth 留空。
2. panel normalize 校验/补默认 → 生成 hysteria inbound 模板（含 `{{PORT}}/{{TAG}}/{{CLIENTS}}/{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}`）→ 存 `nodes.config_template` → 下发。
3. agent 填充：生成自签证书（无则 `xray tls cert`）、用户 auth 列表 → `xray run -test` → 生效 → 上报 RealizedConfig（SNI、CertSHA256、带宽等）。
4. 订阅按格式输出 hy2 链接（pin 或 insecure）。

## 5. 错误处理

- normalize 阶段拒绝一切非法组合（矩阵外请求 400，错误信息指明哪两个字段冲突）。
- agent 填充失败/校验失败沿用现有 `failed` 状态与 `.prev` 回滚。
- hy2 + xray < 26.3.27：明确报错"节点 xray 版本过低，请先在节点页升级 xray"。
- `xray tls cert` 失败：节点进入 failed，错误透出。
- ws/grpc/httpupgrade 弃用警告不阻断（xray 仅 warning）。

## 6. 测试

- 单测：`panel/nodes_test.go` 矩阵 normalize 用例（每协议合法/非法组合）；`sub/*_test.go` 新协议各格式输出；`fill` 测试（自签证书生成 mock）。
- e2e：`scripts/e2e/protocols.sh` 扩展全矩阵（每协议至少一组数据面验证，hy2 用第二个 xray 做客户端仿 `vlessenc.sh`）；`links.sh` 订阅断言扩展。
- 存量用例中硬编码 vless 的 fixture 不受影响（协议字段本身向后兼容，无 DB migration）。

## 7. 分阶段实施（供 plan 参考）

1. **P1 解锁存量**：前端暴露 vmess/trojan/ss + reality 门禁放宽 + grpc 传输；纯前端 + 订阅小改。
2. **P2 传输扩展**：ws/httpupgrade 全链路（panel 模板、fill 提取、订阅、前端）。
3. **P3 TLS 安全层**：agent 自签证书管线 + tlsStreamSettings + 订阅 pin/insecure。
4. **P4 Hysteria2**：新协议全链路 + 版本门控。

每阶段独立可交付、可回滚。
