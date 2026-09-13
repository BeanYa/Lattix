# xray 全协议暴露 P4（Hysteria2 全链路 + 端口跳跃 + 入口协议区块）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 Hysteria2（xray 26.3.27 原生 inbound/outbound，协议名 `hysteria`）全链路：panel 模板（settings.clients + network:hysteria + alpn h3 + tlsSettings 复用 P3 证书占位符 + finalmask salamander/quicParams）→ agent 填充（hy2 用户分支 + 版本门控 + iptables DNAT 端口跳跃）→ dispatch（出口侧共享监听 + 入口终结模式末段 hy2 outbound + 端到端逐跳 UDP 端口段）→ 订阅四格式（hysteria2 链接 / mihomo / sing-box / quanx 跳过）→ 前端（hy2 字段区 + 可勾选的"入口协议"独立配置区块）。**硬性要求（沿用 P1-P3 红线）：存量链路（vless 共享端点链、reality/tls/none 各组合、ws/httpupgrade 明文链、dokodemo 转发链、reverse/encrypted 隧道链、ss UDP 中转链）全部不失效，以完整 e2e 回归证明。**

**Architecture:** 沿用"panel 模板 → agent 填充 → xray run -test → 热更新/重启回退"管线。三个结构性决策（均已源码核实/实测，见下）：
1. **端口跳跃 = DNAT 路径**：xray hy2 服务端只监听单端口（`transport/internet/hysteria/hub.go` Listen 仅 `ListenSystemPacket`），udpHop 仅客户端实现（`dialer.go` 在段内随机换目标端口）——出口 agent 用 iptables DNAT 把跳跃段映射到 hy2 监听端口（官方 hysteria 同款做法）；中转逐跳仍用逐端口 dokodemo UDP inbound 1:1 转发（P2 已验证、无需特权）。
2. **hy2 出口共享监听**：复用 shared_endpoints 骨架（store 层无 vless 硬编码），位置在**出口**侧（与 vless 入口侧对称）：同机多条 hy2 链共享同一 hy2 监听 + 同一跳跃段（首链 profile 为准）；`chains` 表新增 `service_endpoint_id` 列（migration v18）。
3. **入口协议区块**：链路表单可勾选的独立入口协议（v1 仅 VLESS+Reality，复用入口侧共享端点骨架）；勾选后客户端只见入口协议参数，末段（入口/末跳 → 出口）由 xray 以 hy2 outbound 直拨出口（UDP），中间各段沿用现有 encrypted/reverse 隧道。

**Tech Stack:** Go（backend/agent/shared 三个 module，go.work workspace）、React+TS（vite/vitest/oxlint）、SQLite（modernc.org/sqlite，migrations.go ensureColumns 列迁移机制）、OpenAPI 契约 `docs/openapi.yaml` → 前端生成类型。iptables 是 agent 在目标机上调用的系统命令（hy2 端口跳跃 DNAT），不是 Go 依赖。

**Spec:** `docs/superpowers/specs/2026-09-12-xray-full-protocol-exposure-design.md`（§2 矩阵 hy2 行、§3.1 shared、§3.2 panel 含「链路拓扑与协议落地点」「异构入口协议」「hy2 端口跳跃方案」「端口冲突治理」四个评审澄清块、§3.3 agent、§3.4 订阅、§3.5 前端、§4 数据流、§5 错误处理、§6 测试、§7 第 4 条）

**关键已验证事实**（xray 26.3.27 源码 + 本机活体实测，作为实现依据；module cache `github.com/xtls/xray-core@v1.260327.1-0.20260717222851-6e3322d21914`）：

- **协议名与 settings**：inbound/outbound 协议名均为 `"hysteria"`（非 hysteria2）。inbound settings = `{"version":2,"clients":[{"auth":"...","level":0,"email":"..."}]}`——**用户列表键是 `clients` 不是 `users`**（`infra/conf/hysteria.go:38` `json:"clients"`；写 `users` 被静默忽略 → 空 validator → 所有 auth 失败，Task 1 实测踩中）。outbound settings 只有 `{"version":2,"address":"<host>","port":<int>}`（无用户概念）。
- **streamSettings 必须显式 `"network":"hysteria"`**（Task 1 实测新事实）：缺省 `network` 为 `"tcp"`（`infra/conf/transport_internet.go` StreamConfig.Build `ProtocolName:"tcp"`），hysteriaSettings 仅填充 TransportSettings、不改变传输选择——此时 QUIC 跑在 TCP 承载上（服务端 TCP LISTEN 单端口、客户端 TCP ESTAB，数据面也能通但 UDP 跳跃/salamander 语义全失）。模板（inbound 与 outbound）必须携带 `"network":"hysteria"` 才是真 QUIC/UDP。
- **tlsSettings 必须 `alpn:["h3"]`**（Task 1 实测新事实）：服务端用 quic-go http3.Server（`hub.go`），客户端不提供 h3 ALPN 时握手报 `CRYPTO_ERROR 0x178: tls: no application protocol`。
- **客户端口令在 transport 层**：hy2 出站把 `streamSettings.hysteriaSettings.auth` 作为 `Hysteria-Auth` 头发送（`dialer.go:198`）；服务端有用户列表时走 validator 按 auth 匹配用户，无用户列表时回退比对 `hysteriaSettings.auth`（`hub.go:43-110` AuthHTTP）。因此：入站模板 hysteriaSettings 只需 `{"version":2}`（口令在 settings.clients）；出站 hysteriaSettings.auth = 用户口令。
- **salamander 混淆 = finalmask udp mask**（不是 hysteriaSettings 字段）：`"finalmask":{"udp":[{"type":"salamander","settings":{"password":"..."}}]}`（`transport_finalmask.go:629-647` Salamander{Password, PacketSize}）。
- **带宽/拥塞/udpHop = finalmask.quicParams**：`{"congestion":"brutal","brutalUp":"50 mbps","brutalDown":"100 mbps","udpHop":{"ports":"20000-20031","interval":"10-30"}}`（`transport_finalmask.go:888` QuicParamsConfig）。`ports`/`interval` 必须是**字符串区间形式**（PortList/Int32Range 自定义 UnmarshalJSON，`common.go:244,313`；对象写法报 "Invalid integer range"）；带宽下限 65536 B/s；congestion 合法值 reno/bbr/brutal/force-brutal；hysteriaSettings 里的旧式 congestion/up/down/udphop 仅 LogWarning "move to finalmask/quicParams"。
- **udpHop 仅客户端实现**：`hub.go` Listen() 只 `ListenSystemPacket` 单端口（Task 1 复核：服务端 finalmask 声明 udpHop 段后 `ss` 仍只见 UDP 14439 单端口，段内无监听）；`dialer.go:142-163` 客户端在 UdpHop.Ports 内随机换目标端口，**且首包（QUIC 握手）即落段内随机端口**——无 DNAT 时客户端声明 udpHop 必然握手失败，故无跳跃场景客户端不得声明 udpHop。服务端不绑定跳跃段 → 必须 iptables DNAT。spec §3.2 端口跳跃方案的开放问题由此收敛为 DNAT 路径。
- **活体数据面已通过（2026-09-14 复核修正）**：早期一次性实测的 200 实为**缺省 network=tcp 的 TCP 承载**（偶然可用但非设计目标）；按定稿模板（`network:"hysteria"` + settings.clients + alpn h3）重测，服务端 UDP 14439 单端口监听、客户端 UDP QUIC 拨号、`curl -x socks5h://… https://example.com/` 返回 200（服务端日志带 `email: u1`，clients 键鉴权生效）→ xray↔xray hy2 真 QUIC/UDP 数据面可用。实测固化为 `scripts/dev/hy2-probe.sh`（幂等可重复，含 TCP 监听/段内绑定反向断言）。SS-2022 保底（spec §3.2 版本风险条款）预计不启用，仍作风险预案保留在 Task 1/Task 6。
- **本机 uid=1000 无 iptables 权限**：DNAT 用例在 e2e 中加 root/iptables 守卫跳过；逐跳 dokodemo UDP 转发用例不受影响（无需特权）。

## Global Constraints

- 不新增任何第三方依赖（Go 与前端均如此）；iptables 是系统命令调用（`iptables -t nat …`，带 `--comment lattix:<tag>` 标识便于清理），不进 install 脚本、不是 Go module 依赖。复用现有 requester/状态机/管线基础设施（AGENTS.md）。
- 协议/字段常量必须与 `src/shared/config.go` 保持一致，前端常量注释沿用"与后端 shared 包保持一致"。hy2 在面板/API/DB 层的协议值一律是 `"hysteria"`（xray 协议名）；仅订阅输出层映射为客户端类型名（mihomo/sing-box `hysteria2`、链接 scheme `hysteria2://`）。
- 后端 API 字段变更必须同步改 `docs/openapi.yaml` 并在 `src/frontend` 跑 `npm run generate:api`（`npm run build` 内含 `--check` 会拦截不一致）。
- **P4 新增合法组合矩阵**（spec §2；矩阵外一律 400 并指明冲突字段）：
  - hysteria：无 network 概念（API 显式传 network → 400；生成的 xray 模板内部 `streamSettings.network` 恒为 `"hysteria"`，见事实区）；security 仅允许缺省/tls（内部恒为 tls，显式传 reality/none → 400）；cert_mode/tls_domain 复用 P3 证书双模式（selfsign 默认伪装域 / acme 落地服务器域名）；flow/method/cipher/encryption/short_id/dest/server_names 一律清空。
  - hy2 协议级选项：`obfs_password`（salamander，留空自动生成随机串；显式空语义不存在——要关混淆只能不传字段且关闭自动生成的路径不存在，v1 恒开启混淆）、`up_mbps`/`down_mbps`（默认 50/100，≥1；0=不声明 brutal 带宽回退 BBR）、`port_hop`（`"off"`=关闭跳跃；`""`=默认开启、panel 自动分配 32 段；`"a-b"`=显式段，长度 8-200）。
  - 入口协议区块（`entry_node`）：v1 仅允许 vless + reality（子参数全部可空自动生成）；仅多跳链可勾选（单跳 400）；勾选时出口协议仅允许 hysteria/vless（其他 400「入口协议区块 v1 仅支持 hysteria2/vless 出口」）。
  - 存量矩阵（reality/none/tls × 五传输、ss/socks/http/dokodemo 无传输/安全层）全部不变。
- **端口分层**：hy2 为 udp-only（`PortLayers("hysteria")="udp"`）；hy2(UDP) 与 vless(TCP) 同端口号允许共存；hy2 跳跃段视为 udp 层连续保留段：段与段、段与单端口均不得重叠（含逐跳转发端口）；同机 hy2 链走出口共享监听合并（共享同一监听端口与跳跃段）。
- **DNAT 治理**：DNAT 规则生命周期跟随 hy2 监听（ApplyNode/ApplySharedEndpoint 建立，RemoveNode/RemoveSharedEndpoint 清除，rebuild 后按期望状态重建）；无 root/CAP_NET_ADMIN 或 iptables 缺失时节点/端点 failed 并报明原因（"端口跳跃需要 iptables DNAT 权限，请关闭端口跳跃或以 root 运行 agent"）。
- Go 源文件与前端 TS/TSX 文件均为 CRLF 行尾，编辑时保持；shell 脚本与 openapi.yaml 为 LF。
- 提交信息沿用仓库惯例：`type(scope): 中文摘要`。
- 每个 Task 完成后运行对应验证命令，全绿才提交。
- **存量回归红线**：任何 Task 不得改变既有协议的模板结构、端口分配、共享端点行为与订阅输出；Task 9 的全量 e2e 回归（7 个脚本，含"编辑存量链路原样保存"用例）必须全绿才算 P4 完成。
- 前端不要重装依赖（main 仓 `src/frontend/node_modules` 已装好；若必须装，用 `npm install --legacy-peer-deps`）。
- hy2 不做 AlterInbound 热操作适配：`hot.go:182-204` alterUser 对 hysteria 返回错误，自动走 `withRestartFallback` 重启兜底（`manager.go:232-244`），配置文件中用户列表已先行更新（`config.go:120-167` mutateClients + `mutateUserList`），语义正确。

验证命令速查：
- shared: `cd src/shared && go test ./...`
- backend: `cd src/backend && go test ./...`
- agent: `cd src/agent && go test ./...`
- frontend: `cd src/frontend && npm run generate:api && npm test && npm run lint && npm run build`
- e2e（最后统一跑，前置 `export XRAY_BIN=/usr/local/bin/xray`；links.sh 需先 `rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist`；groups.sh 需 `LATX_ALLOW_PRIVATE_OUTBOUND=1`；chains.sh 含外网真实流量）：
  `bash scripts/e2e/protocols.sh` 等 7 个脚本（见 Task 9）。

---

### Task 1: hy2 数据面验证与模板定稿（实验任务，结论回填）

**Files:**
- Create: `scripts/dev/hy2-probe.sh`（一次性验证脚本，LF；保留供复测）
- Modify: 本文件（结论回填到上方「关键已验证事实」与 Task 3 模板，如有出入）

**Interfaces:**
- Consumes: 本机 `/usr/local/bin/xray`（Xray 26.3.27）；无需改动任何 Go 代码。
- Produces: 定稿的 hy2 inbound/outbound 模板 JSON 形态（Task 3/Task 5 引用）；udpHop 服务端行为的最终结论。

**背景**：关键事实区的结论已经由一次性实测得出（xray↔xray 数据面 200、udpHop 仅客户端、salamander=finalmask）。本任务把这些实测固化为可重复脚本并补齐两个未覆盖点：(a) 服务端 udpHop 行为的实测复核（起带 quicParams.udpHop 的服务端，`ss -ulnp` 确认仅单端口监听）；(b) iptables DNAT 冒烟（rootful 环境；非 root 记录跳过原因）。**若任何实测与上文结论相反**（例如 xray 服务端真的绑定整个段），停止后续任务，把本文件 Task 3/5/6 的 DNAT 方案替换为「xray 自绑段」方案（agent 不再管理 iptables，forward 逐端口 dokodemo 不变，端口段治理不变）再继续。

> **Task 1 已完成（2026-09-14）**：核心结论全部成立（服务端仅 UDP 单端口监听、udpHop 仅客户端、salamander=finalmask、DNAT 路径），但原脚本模板有四处实测出入，已回填上方事实区并修正 Task 3/5/6 模板：`network:"hysteria"` 必须显式（原脚本缺省走 TCP 承载）、用户列表键 `clients`（非 `users`）、`tlsSettings.alpn:["h3"]` 必须、客户端 udpHop 首包即落段内随机端口（无 DNAT 不可声明）。DNAT 冒烟因本机 uid=1000 无 iptables 按守卫 SKIP（rootful 环境复跑 `scripts/dev/hy2-probe.sh` 即覆盖，Step 3 内置客户端带 udpHop 重测）。

- [x] **Step 1: 写 scripts/dev/hy2-probe.sh（server+client 数据面 + udpHop 监听复核）**

最终脚本（仓库内 `scripts/dev/hy2-probe.sh`，以此为准；相对原计划脚本的关键修正：server/client streamSettings 加 `"network":"hysteria"`、server settings `users`→`clients`、双侧 tlsSettings 加 `"alpn":["h3"]`、客户端基线不含 udpHop）：

```bash
# 结构（完整脚本见仓库）：
#   Step 1) 服务端（自签证书 + network:hysteria + alpn h3 + settings.clients
#           + salamander + brutal + finalmask 声明 udpHop 段）起听后：
#           ss -tuanp 断言仅 UDP 14439 单端口（TCP 监听或段内端口出现 → FAIL 并提示换方案）
#   Step 2) 客户端（socks → hysteria outbound + pinnedPeerCertSha256 pin，无 udpHop）
#           curl 经 socks 断言 200
#   Step 3) root+iptables 守卫内：DNAT（REDIRECT 20000:20031→14439，PREROUTING+OUTPUT
#           双链挂 LATTIX_UDPHOP）下发后，客户端带 udpHop 段重测断言 200；
#           非 root 输出 SKIP（不假绿）
# 幂等：mktemp 临时目录 + EXIT trap 清理进程/iptables 规则/临时文件。
```

Run: `bash scripts/dev/hy2-probe.sh`
Expected: PASS（Step 1 仅 UDP 14439；Step 2 http_code=200。2026-09-14 本机实测通过，服务端日志 `accepted … [hy2 >> direct] email: u1` 证明 clients 键鉴权生效）

- [x] **Step 2: iptables DNAT 冒烟（rootful；非 root 记录跳过）**

```bash
# 需要 root（或 CAP_NET_ADMIN）。在非特权环境执行会失败并跳过——记录原因即可。
# 脚本内嵌守卫：非 root 或无 iptables → 输出 "SKIP: DNAT 冒烟需要 root + iptables（当前 uid=…）"
# rootful 路径：iptables -t nat 建 LATTIX_UDPHOP 链（REDIRECT --to-ports 14439，
# comment "lattix:probe"），PREROUTING+OUTPUT 挂链；客户端带 udpHop 段重测断言 200；
# EXIT trap 负责 -D/-F/-X 清理（OUTPUT 链是本机回环探针流量所需，生产外部流量走 PREROUTING）。
```

Run: 同上脚本内嵌守卫执行
Expected: rootful 环境数据面 200；非 root 输出 SKIP（2026-09-14 本机 uid=1000 无 iptables → SKIP，已记录；rootful 环境复跑脚本即覆盖）

- [x] **Step 3: 结论回填**

已回填：事实区修正 settings.clients / network:"hysteria" / alpn h3 / udpHop 首包随机端口四条；Task 3（buildVirtualConfig 模板键、hy2StreamSettings、测试断言）、Task 5（fill 测试模板、mutateClients 键）、Task 6（renderHy2Outbound network/alpn）相应调整。

Run: `git diff --stat`
Expected: 新增 scripts/dev/hy2-probe.sh + 本文件结论回填修订

提交：`git add scripts/dev && git commit -m "test(dev): P4 hy2 数据面探针脚本"`；计划回填单独 `docs(plan)` 提交。

---

### Task 2: shared 层——hy2 常量、配置字段、端口段解析与派生函数

**Files:**
- Modify: `src/shared/config.go`（协议常量 :16-30、PortLayers :140-149、VirtualConfig :236-254、RealizedConfig :266-284、文件尾追加 hy2 助手）
- Modify: `src/shared/ports.go`（文件尾追加 SpanInListenRanges）
- Modify: `src/shared/messages.go`（SharedEndpointRoute :272-280、ForwardSpec :404-414）
- Test: `src/shared/config_test.go`（追加；不存在则新建）、`src/shared/ports_test.go`（追加）

**Interfaces:**
- Consumes: 既有 `ValidValue`（config.go:117）、`rangesOverlap`（ports.go:78）、`ClientCredential`（config.go:259）。
- Produces（后续任务统一引用，签名不得再变）:
  - `const ProtocolHysteria2 = "hysteria"`（xray 协议名）；`Protocols` 追加。
  - `PortLayers(protocol string) string`：hysteria → `"udp"`（其余不变）。
  - `const Hy2PortHopDefaultLen = 32`、`Hy2PortHopMinLen = 8`、`Hy2PortHopMaxLen = 200`、`Hy2PortHopInterval = "10-30"`、`XrayMinVersionHy2 = "26.3.27"`。
  - `func Hy2UserPassword(uuid string) string`——hy2 用户口令确定性派生（agent clients 填充 / 入口终结 outbound / 订阅链接三端共用）。
  - `func ParsePortHop(s string) (start, end int, err error)`、`func FormatPortHop(start, end int) string`。
  - `func SpanInListenRanges(rs []PortRange, start, end int) bool`——段整体落在某一段监听侧内（NAT 校验，checkPortInRanges 的段版）。
  - `VirtualConfig` 新增：`ObfsPassword string \`json:"obfs_password,omitempty"\``、`UpMbps int \`json:"up_mbps,omitempty"\``、`DownMbps int \`json:"down_mbps,omitempty"\``、`PortHop string \`json:"port_hop,omitempty"\``。
  - `RealizedConfig` 新增同名四个字段（同 json tag，agent 回显/订阅取用）。
  - `SharedEndpointRoute` 新增：`ExitProtocol string \`json:"exit_protocol,omitempty"\``（出口业务协议判别：hysteria 时 agent 渲染 hy2 outbound；空=vless 现状）。
  - `ForwardSpec` 新增：`HopPortEnd int \`json:"hop_port_end,omitempty"\``（hy2 端到端跳跃段终点，0=单端口现状；段起点 = Port）、`Hy2Target *Hy2DialSpec \`json:"hy2_target,omitempty"\``（入口终结模式末段 hy2 拨号参数，非空时 forward piece 渲染 dokodemo-udp inbound + hy2 outbound 而非 freedom 直连）。
  - `type Hy2DialSpec struct { Address string; Port int; Auth string; SNI string; CertSHA256 string; ObfsPassword string; UpMbps int; DownMbps int; PortHop string }`（json tag 全小写下划线，omitempty 除 Address/Port/Auth）。

- [ ] **Step 1: config.go 协议常量与 PortLayers（先写失败测试）**

`src/shared/config_test.go` 追加（文件不存在则新建，package shared）：

```go
// TestHysteria2Protocol 验证 hy2 协议常量入向导集合且分层为 udp-only（P4）。
func TestHysteria2Protocol(t *testing.T) {
	if !ValidValue(ProtocolHysteria2, Protocols) {
		t.Fatal("Protocols 缺少 hysteria")
	}
	if got := PortLayers(ProtocolHysteria2); got != "udp" {
		t.Fatalf("PortLayers(hysteria) = %q, want udp", got)
	}
	// 存量回归：ss/dokodemo tcp,udp；vless tcp。
	if got := PortLayers(ProtocolShadowsocks); got != "tcp,udp" {
		t.Fatalf("PortLayers(ss) 回归: %q", got)
	}
	if got := PortLayers(ProtocolVLESS); got != "tcp" {
		t.Fatalf("PortLayers(vless) 回归: %q", got)
	}
	// hy2 不是 reality 协议（无 dest/密钥对），但有用户列表。
	if IsRealityProtocol(ProtocolHysteria2) {
		t.Fatal("hysteria 不应为 reality 协议")
	}
	if !HasUserList(ProtocolHysteria2) {
		t.Fatal("hysteria 应有用户列表")
	}
}

// TestHy2UserPassword 验证口令派生确定性与两端一致（agent 填充与订阅共用）。
func TestHy2UserPassword(t *testing.T) {
	a := Hy2UserPassword("11111111-2222-3333-4444-555555555555")
	b := Hy2UserPassword("11111111-2222-3333-4444-555555555555")
	c := Hy2UserPassword("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if a == "" || a != b || a == c {
		t.Fatalf("派生应确定且随输入变化: %q %q %q", a, b, c)
	}
}

// TestParsePortHop 验证端口段解析/格式化往返与非法输入。
func TestParsePortHop(t *testing.T) {
	start, end, err := ParsePortHop("20000-20031")
	if err != nil || start != 20000 || end != 20031 {
		t.Fatalf("ParsePortHop = %d,%d,%v", start, end, err)
	}
	if got := FormatPortHop(start, end); got != "20000-20031" {
		t.Fatalf("FormatPortHop = %q", got)
	}
	for _, bad := range []string{"", "abc", "20000", "20031-20000", "0-100", "20000-70000", "20000-20000"} {
		if _, _, err := ParsePortHop(bad); err == nil {
			t.Fatalf("ParsePortHop(%q) 应报错", bad)
		}
	}
}
```

Run: `cd src/shared && go test ./... -run 'TestHysteria2|TestHy2|TestParsePortHop' -v`
Expected: FAIL（常量与函数尚不存在，编译失败）

实现 `src/shared/config.go`：

1. :17-24 常量块尾部（`ProtocolDokodemo` 行后）追加：
```go
	ProtocolHysteria2   = "hysteria"      // xray 26.x hy2 入站协议名（非 "hysteria2"；订阅层再映射客户端类型名）
```
2. :27-30 `Protocols` 改为：
```go
var Protocols = []string{
	ProtocolVLESS, ProtocolVMess, ProtocolTrojan, ProtocolShadowsocks, ProtocolHysteria2,
	ProtocolSocks, ProtocolHTTP, ProtocolDokodemo,
}
```
3. :140-149 `PortLayers` 注释与函数改为：
```go
// PortLayers 返回协议监听占用的传输层（端口冲突治理的分层依据）：
// shadowsocks/dokodemo-door 同时监听 tcp+udp，hysteria 为 udp-only（QUIC），其余仅 tcp。
func PortLayers(protocol string) string {
	switch protocol {
	case ProtocolShadowsocks, ProtocolDokodemo:
		return "tcp,udp"
	case ProtocolHysteria2:
		return "udp"
	default:
		return "tcp"
	}
}
```
4. 文件尾（`EffectiveSecurity` 之后）追加：
```go
// hy2 端口跳跃段长边界（spec §2）：默认 32，可用公共端口不足最小段长（8）时允许关闭
// 跳跃退回固定单端口，上限 200（配置膨胀可控）。XrayMinVersionHy2 为 hy2 inbound 引入
// 版本（agent 版本门控）；Hy2PortHopInterval 为 udpHop.interval 默认区间（秒，字符串区间）。
const (
	Hy2PortHopDefaultLen = 32
	Hy2PortHopMinLen     = 8
	Hy2PortHopMaxLen     = 200
	Hy2PortHopInterval   = "10-30"
	XrayMinVersionHy2    = "26.3.27"
)

// Hy2UserPassword 从用户 UUID 确定性派生 hy2 口令（auth 字符串）：
// panel 订阅、agent clients 填充、入口终结 outbound 三端共用（仿 SSUserPassword）。
func Hy2UserPassword(uuid string) string {
	sum := sha256.Sum256([]byte("lattix/hy2:" + uuid))
	return base64.RawURLEncoding.EncodeToString(sum[:24])
}

// ParsePortHop 解析 hy2 端口跳跃段 "a-b"（finalmask quicParams udpHop.ports 同形）；
// 1-65535 且 a<b。FormatPortHop 为其逆操作。
func ParsePortHop(s string) (start, end int, err error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("端口跳跃段应为 \"a-b\" 形式: %q", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(a))
	end, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || start < 1 || end > 65535 || start >= end {
		return 0, 0, fmt.Errorf("端口跳跃段非法: %q（须 1-65535 且 a<b）", s)
	}
	return start, end, nil
}

func FormatPortHop(start, end int) string { return fmt.Sprintf("%d-%d", start, end) }
```
（imports 追加 `"strconv"`；sha256/base64/strings/fmt 已在。）

5. `VirtualConfig`（:236-254）在 `Cipher` 行后追加字段：
```go
	ObfsPassword  string             `json:"obfs_password,omitempty"` // hy2 salamander 混淆密码（panel 生成，模板内固定值）
	UpMbps        int                `json:"up_mbps,omitempty"`       // hy2 brutal 上行声明（0=不输出 brutalUp，回退 BBR）
	DownMbps      int                `json:"down_mbps,omitempty"`     // hy2 brutal 下行声明
	PortHop       string             `json:"port_hop,omitempty"`      // hy2 udpHop 段 "a-b"（空=关闭跳跃；DNAT 治理见 spec §3.2）
```
`Protocol` 行注释追加 hysteria。`RealizedConfig`（:266-284）在 `Encryption` 行后追加同名字段：
```go
	ObfsPassword string `json:"obfs_password,omitempty"` // hy2 salamander 混淆密码（回显，订阅 obfs-password）
	UpMbps       int    `json:"up_mbps,omitempty"`       // hy2 brutal 上行（回显，订阅 up/upmbps）
	DownMbps     int    `json:"down_mbps,omitempty"`
	PortHop      string `json:"port_hop,omitempty"`      // hy2 跳跃段回显（订阅 ports/mport）
```

Run: `cd src/shared && go test ./... -v && go build ./...`
Expected: PASS

- [ ] **Step 2: ports.go SpanInListenRanges（先写失败测试）**

`src/shared/ports_test.go` 追加：

```go
// TestSpanInListenRanges 验证段整体落段判定（hy2 跳跃段 NAT 校验，P4）。
func TestSpanInListenRanges(t *testing.T) {
	rs := []PortRange{{PubStart: 20000, PubEnd: 20099}, {PubStart: 30000, PubEnd: 30010}}
	if !SpanInListenRanges(rs, 20000, 20031) {
		t.Fatal("[20000,20031] 应整体落在第一段内")
	}
	if SpanInListenRanges(rs, 20090, 20110) {
		t.Fatal("跨段间隙的段不应通过（段须连续落在同一段内）")
	}
	if SpanInListenRanges(nil, 20000, 20031) {
		t.Fatal("无端口段（非 NAT）不应通过——调用方对 direct 机走全端口空间分支")
	}
	// 非 1:1 映射按监听侧判定。
	mapped := []PortRange{{PubStart: 40000, PubEnd: 40099, ListenStart: 50000, ListenEnd: 50099}}
	if !SpanInListenRanges(mapped, 50000, 50031) || SpanInListenRanges(mapped, 40000, 40031) {
		t.Fatal("非 1:1 段应按监听侧区间判定")
	}
}
```

Run: `cd src/shared && go test ./... -run TestSpanInListenRanges -v`
Expected: FAIL（函数不存在）

实现：`src/shared/ports.go` 文件尾追加：

```go
// SpanInListenRanges 报告段 [start,end] 是否整体落在某一段的监听侧区间内
// （hy2 跳跃段须连续完整落段，跨段间隙不通过，spec §2/§3.2）。
func SpanInListenRanges(rs []PortRange, start, end int) bool {
	for _, r := range rs {
		s, e := r.listenRange()
		if start >= s && end <= e {
			return true
		}
	}
	return false
}
```

Run: `cd src/shared && go test ./... -v`
Expected: PASS

- [ ] **Step 3: messages.go——SharedEndpointRoute.ExitProtocol、ForwardSpec.HopPortEnd/Hy2Target、Hy2DialSpec**

`src/shared/messages.go` 三处编辑：

1. `SharedEndpointRoute`（:272-280）`Target` 行后追加：
```go
	ExitProtocol  string         `json:"exit_protocol,omitempty"` // 出口业务协议（hysteria 时 agent 渲染 hy2 outbound；空=vless 现状）
```
2. `ForwardSpec`（:404-414）`ListenFamily` 行后追加：
```go
	HopPortEnd      int          `json:"hop_port_end,omitempty"` // hy2 端到端跳跃段终点（段起点=Port；0=单端口现状，逐端口 dokodemo UDP 1:1）
	Hy2Target       *Hy2DialSpec `json:"hy2_target,omitempty"`   // 入口终结模式末段 hy2 拨号参数（非空时代替 freedom 直连）
```
3. `ForwardSpec` 定义之后追加：
```go
// Hy2DialSpec 是入口终结模式末段（入口/末跳 → 出口）的 hy2 拨号参数（§3.2 异构入口协议）：
// 由 panel/dispatch 从出口 RealizedConfig + 公网地址组装；Auth = Hy2UserPassword(链 serviceUUID)。
type Hy2DialSpec struct {
	Address      string `json:"address"`
	Port         int    `json:"port"` // PortHop 非空时为段起点（客户端 udpHop 在段内换端口，出口 DNAT 收敛到监听端口）
	Auth         string `json:"auth"`
	SNI          string `json:"sni"`
	CertSHA256   string `json:"cert_sha256,omitempty"`   // 自签证书 pin（hex；ACME 留空走系统根验证）
	ObfsPassword string `json:"obfs_password,omitempty"` // salamander 混淆密码（空=不启用）
	UpMbps       int    `json:"up_mbps,omitempty"`
	DownMbps     int    `json:"down_mbps,omitempty"`
	PortHop      string `json:"port_hop,omitempty"` // 客户端 udpHop 段 "a-b"（空=不跳跃）
}
```

Run: `cd src/shared && go build ./... && go vet ./... && cd ../backend && go build ./... && cd ../agent && go build ./...`
Expected: PASS（纯新增字段，三模块编译不回归）

提交：`git add -A src/shared && git commit -m "feat(shared): P4 hy2 协议常量/端口段解析/链路口令派生与转发规格字段"`

---

### Task 3: panel 节点侧——hy2 normalize 矩阵、hy2 模板、端口段治理、契约

**Files:**
- Modify: `src/backend/internal/panel/nodes.go`（createNodeRequest :81-104、normalize :107-279、handleCreateNode :282-352、buildVirtualConfig :516-596、文件尾追加 hy2StreamSettings/resolveHy2PortHop）
- Modify: `src/backend/internal/panel/ports.go`（findPortConflict :22-39、checkPortConflict :42-51、追加 allocUDPPortHop）
- Modify: `src/backend/internal/store/ports.go`（PortOccupant :11-18、PortOccupants :22-76）
- Modify: `docs/openapi.yaml`（VirtualConfig schema :902-924 + 节点创建请求 schema 的四个 hy2 字段）
- Test: `src/backend/internal/panel/nodes_test.go`（追加）、`src/backend/internal/panel/ports_test.go`（追加；不存在则新建）、`src/backend/internal/store/ports_test.go`（追加；不存在则新建）

**Interfaces:**
- Consumes: `shared.ProtocolHysteria2/ParsePortHop/SpanInListenRanges/Hy2PortHop*`（Task 2）；`tlsCamouflagePool/validateTLSDomain/applyACMEDomain/serverDomain`（nodes.go:680-731 现状复用）；`randomHex`（panel 既有）。
- Produces:
  - `createNodeRequest` 新增：`ObfsPassword string \`json:"obfs_password"\``、`UpMbps int \`json:"up_mbps"\``、`DownMbps int \`json:"down_mbps"\``、`PortHop string \`json:"port_hop"\``。
  - `func hy2StreamSettings(req createNodeRequest) map[string]any`（panel 包级私有）。
  - `func (s *Server) resolveHy2PortHop(ctx context.Context, req *createNodeRequest, srv *store.Server, excludeChainID int64) error`——port_hop 三段语义落地（off/自动分配/显式校验），handleCreateNode 与 Task 4 的链处理器共用。
  - `func findPortConflict(occupants []store.PortOccupant, protocol string, port, portEnd int, excludeChainID int64) error`（签名加 portEnd；portEnd=0 等价现状）。
  - `func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port, portEnd int, excludeChainID int64) error`。
  - `func allocUDPPortHop(occupants []store.PortOccupant, rs []shared.PortRange, length int) (start, end int, ok bool)`——在候选空间（NAT=各段监听侧并集；direct=40000-61000）内找长度为 length 的连续空闲 udp 段。
  - `store.PortOccupant` 新增 `PortEnd int`（0=单端口；>0=连续保留段 [Port,PortEnd]，hy2 跳跃段）。

**端口段占用数据源设计**（store/ports.go 扩展的依据）：hy2 节点/共享端点自身监听是单端口（udp）；跳跃段是额外保留。PortOccupants 对 `protocol='hysteria'` 且 config_template 含非空 `port_hop` 的行，除单端口行外再追加一条段行（Port=段起点，PortEnd=段终点，Layers="udp"）。chain_forward 查询对 hy2 出口链同理：逐跳保留段 = [forward_port, forward_port + 段长 - 1]（全跳同段，1:1 转发），段长从出口节点 config_template 的 port_hop 解析。SQLite 侧用 `json_extract` + `instr/substr` 解析 "a-b"（nodes.config_template / shared_endpoints.config_template 均为 VirtualConfig JSON 文本）。

- [ ] **Step 1: store.PortOccupant.PortEnd + PortOccupants 段语义（先写失败测试）**

`src/backend/internal/store/ports_test.go`（不存在则新建，package store，用既有测试库 helper——参照同包其他 *_test.go 的 NewForTest 风格）追加：

```go
// TestPortOccupantsHy2HopRange 验证 hy2 节点/共享端口的跳跃段以段行形式进入占用画像（P4）。
func TestPortOccupantsHy2HopRange(t *testing.T) {
	st := newTestStore(t) // 若同包已有等价 helper 则沿用其名
	ctx := context.Background()
	srv := insertTestServer(t, st) // 同上：复用同包既有服务器 fixture
	// hy2 节点：port=21000，port_hop="30000-30031"。
	vc := `{"protocol":"hysteria","port":21000,"port_hop":"30000-30031","template":{}}`
	id := insertTestNode(t, st, srv, "hysteria", 21000, vc) // 复用同包既有节点 fixture
	_ = id
	occ, err := st.PortOccupants(ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	var single, span *PortOccupant
	for i := range occ {
		if occ[i].Port == 21000 && occ[i].PortEnd == 0 && occ[i].Layers == "udp" {
			single = &occ[i]
		}
		if occ[i].Port == 30000 && occ[i].PortEnd == 30031 && occ[i].Layers == "udp" {
			span = &occ[i]
		}
	}
	if single == nil || span == nil {
		t.Fatalf("hy2 占用画像缺行（单端口/跳跃段）: %+v", occ)
	}
	// 无 port_hop 的 hy2 节点只产单端口行；vless 节点不受影响。
}
```

（fixture 名以同包现状为准；若 nodes 行插入走 SQL 则注意 config_template 列存 VirtualConfig JSON。）

Run: `cd src/backend && go test ./internal/store/ -run TestPortOccupantsHy2 -v`
Expected: FAIL（PortEnd 字段不存在，编译失败）

实现 `src/backend/internal/store/ports.go`：

1. `PortOccupant`（:11-18）改为：
```go
// PortOccupant 是一台服务器上一个已被占用端口的画像（端口冲突前置治理的数据源）。
// PortEnd=0 表示单端口；PortEnd>0 表示连续保留段 [Port,PortEnd]（hy2 端口跳跃段，§3.2）。
type PortOccupant struct {
	Port     int
	PortEnd  int
	Layers   string // "tcp" / "udp" / "tcp,udp"
	Source   string // "node" | "endpoint" | "chain_forward" | "chain_portal"
	Protocol string
	ChainID  int64 // 0 = 无链归属（独立节点）
	RefName  string
}
```
2. `PortOccupants`（:22-76）：appendRows 的 Scan 与查询列不变（段行不走 appendRows）。在四条既有查询之后追加两条段查询：
```go
	// hy2 跳跃段保留（§3.2 端口段治理）：hy2 节点/共享端点 config_template 含非空
	// port_hop（"a-b"）时，该段在 udp 层整体保留（段行 Port=a, PortEnd=b）。
	const hopExpr = `json_extract(%s, '$.port_hop')`
	const hopStart = `CAST(substr(` + hopExpr + `, 1, instr(` + hopExpr + `, '-') - 1) AS INTEGER)`
	const hopEnd = `CAST(substr(` + hopExpr + `, instr(` + hopExpr + `, '-') + 1) AS INTEGER)`
	appendSpan := func(query string, args ...any) error {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o PortOccupant
			o.Layers = "udp"
			o.Protocol = shared.ProtocolHysteria2
			if err := rows.Scan(&o.Port, &o.PortEnd, &o.ChainID, &o.RefName, &o.Source); err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	}
	if err := appendSpan(fmt.Sprintf(`SELECT %s, %s,
		COALESCE((SELECT c.id FROM chains c WHERE c.service_node_id=n.id AND c.deleted_at IS NULL),0),
		n.name, 'node' FROM nodes n
		WHERE n.server_id=? AND n.protocol='hysteria' AND `+hopExpr+` <> ''`,
		fmt.Sprintf(hopStart, "n.config_template"), fmt.Sprintf(hopEnd, "n.config_template")),
		/* 同上把 %s 实参固定为 n.config_template */ serverID); err != nil {
		return nil, fmt.Errorf("query hy2 node hop spans: %w", err)
	}
	if err := appendSpan(fmt.Sprintf(`SELECT %s, %s, 0, 'shared-endpoint #' || e.id, 'endpoint'
		FROM shared_endpoints e WHERE e.server_id=? AND e.protocol='hysteria'
		AND e.status IN ('pending','applying','active') AND `+hopExpr+` <> ''`,
		fmt.Sprintf(hopStart, "e.config_template"), fmt.Sprintf(hopEnd, "e.config_template")), serverID); err != nil {
		return nil, fmt.Errorf("query hy2 endpoint hop spans: %w", err)
	}
	// hy2 链逐跳转发保留段（端到端跳跃）：段 = [forward_port, forward_port + 段长 - 1]，
	// 段长从出口节点 config_template 的 port_hop 解析；port_hop 空 = 单端口现状（不产段行）。
	if err := appendSpan(fmt.Sprintf(`SELECT h.forward_port,
		h.forward_port + %s - %s, c.id, c.name, 'chain_forward'
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		JOIN nodes n ON n.id=c.service_node_id
		WHERE h.server_id=? AND h.forward_port>0 AND n.protocol='hysteria' AND `+hopExpr+` <> ''`,
		fmt.Sprintf(hopEnd, "n.config_template"), fmt.Sprintf(hopStart, "n.config_template")), serverID); err != nil {
		return nil, fmt.Errorf("query hy2 chain hop spans: %w", err)
	}
```
（上面 `hopExpr/hopStart/hopEnd` 里的 `%s` 实参即列名 `n.config_template`/`e.config_template`；实现时把占位替换落实为普通 fmt.Sprintf 拼接，SQL 注入面无——列名是代码常量。既有四条查询的 `PortEnd` 由 Go 零值兜底为 0。）

3. 既有 nodes 查询（:48-51）的 hy2 行 Layers 由 `shared.PortLayers("hysteria")="udp"` 自动正确，无需改。

Run: `cd src/backend && go test ./internal/store/ -v`
Expected: PASS（含新用例；既有用例不回归——PortEnd 零值不改变旧判定）

- [ ] **Step 2: panel/ports.go 段感知冲突判定 + 空闲段分配（先写失败测试）**

`src/backend/internal/panel/ports_test.go`（不存在则新建，package panel）追加：

```go
// TestFindPortConflictSpan 验证段-段/段-点/跨层语义（P4 §3.2 端口冲突治理）。
func TestFindPortConflictSpan(t *testing.T) {
	occ := []store.PortOccupant{
		{Port: 20000, PortEnd: 20031, Layers: "udp", Source: "node", Protocol: "hysteria", RefName: "hy2甲"},
		{Port: 21000, Layers: "udp", Source: "node", Protocol: "hysteria", RefName: "hy2乙"},
		{Port: 20010, Layers: "tcp", Source: "node", Protocol: "vless", RefName: "vless丙"},
	}
	// 段与段重叠（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20020, 20051, 0); err == nil {
		t.Fatal("段 [20020,20051] 与既有段 [20000,20031] 重叠应冲突")
	}
	// 点落入段（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20010, 0, 0); err == nil {
		t.Fatal("点 20010 落入既有段应冲突")
	}
	// 段覆盖点（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20990, 21010, 0); err == nil {
		t.Fatal("段 [20990,21010] 覆盖单端口 21000 应冲突")
	}
	// TCP/UDP 独立空间：tcp 点与 udp 段同号不冲突（反之亦然）。
	if err := findPortConflict(occ, "vless", 20010, 0, 0); err != nil {
		t.Fatalf("tcp 20010 与 udp 段同号应共存: %v", err)
	}
	// 同层不重叠 → 通过；excludeChainID 排除自身。
	if err := findPortConflict(occ, "hysteria", 20100, 20131, 0); err != nil {
		t.Fatalf("不相交段应通过: %v", err)
	}
}

// TestAllocUDPPortHop 验证空闲段自动分配避开已保留段/点且落在 NAT 段内。
func TestAllocUDPPortHop(t *testing.T) {
	occ := []store.PortOccupant{{Port: 40000, PortEnd: 40031, Layers: "udp", Source: "node"}}
	start, end, ok := allocUDPPortHop(occ, nil, 32)
	if !ok || start != 40032 || end != 40063 {
		t.Fatalf("direct 机应分配到 40032-40063: %d,%d,%v", start, end, ok)
	}
	rs := []shared.PortRange{{PubStart: 50000, PubEnd: 50099}}
	start, end, ok = allocUDPPortHop(occ, rs, 32)
	if !ok || start != 50000 || end != 50031 {
		t.Fatalf("NAT 机应在段内分配 50000-50031: %d,%d,%v", start, end, ok)
	}
	if _, _, ok = allocUDPPortHop(occ, []shared.PortRange{{PubStart: 50000, PubEnd: 50003}}, 32); ok {
		t.Fatal("段长不足应 ok=false（调用方据此关跳跃或报错）")
	}
}
```

Run: `cd src/backend && go test ./internal/panel/ -run 'TestFindPortConflictSpan|TestAllocUDPPortHop' -v`
Expected: FAIL（签名/函数不存在，编译失败）

实现 `src/backend/internal/panel/ports.go`：

1. `findPortConflict`（:22-39）整体替换为：
```go
// findPortConflict 端口冲突前置判定（纯函数）：传输层重叠且区间相交即冲突。
// portEnd=0 表示单端口 [port,port]；>0 表示连续保留段 [port,portEnd]（hy2 跳跃段）。
// excludeChainID 用于编辑链路时排除自身既有占用。vless 链入口的共享端点合并语义
// 由调用点门控（chains.go 入口校验 endpointID==0 才进本函数），此处不做协议豁免。
func findPortConflict(occupants []store.PortOccupant, protocol string, port, portEnd int, excludeChainID int64) error {
	layers := shared.PortLayers(protocol)
	reqEnd := portEnd
	if reqEnd == 0 {
		reqEnd = port
	}
	for _, o := range occupants {
		occEnd := o.PortEnd
		if occEnd == 0 {
			occEnd = o.Port
		}
		if !shared.LayersOverlap(layers, o.Layers) || port > occEnd || o.Port > reqEnd {
			continue
		}
		if excludeChainID != 0 && o.ChainID == excludeChainID {
			continue
		}
		source := portOccupantSourceNames[o.Source]
		if source == "" {
			source = "监听"
		}
		if o.PortEnd > 0 {
			return fmt.Errorf("端口段 %d-%d 与%s「%s」的保留段 %d-%d 重叠（%s 层），请更换或留空自动分配",
				port, reqEnd, source, o.RefName, o.Port, o.PortEnd, o.Layers)
		}
		return fmt.Errorf("端口 %d 已被%s「%s」占用（%s 层），请更换端口或留空自动分配",
			port, source, o.RefName, o.Layers)
	}
	return nil
}
```
2. `checkPortConflict`（:42-51）签名加 portEnd：
```go
// checkPortConflict 查询服务器端口占用并判定冲突（端口 0 = 自动分配，无需校验）。
// portEnd=0 单端口；>0 校验段 [port,portEnd]（hy2 跳跃段）。
func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port, portEnd int, excludeChainID int64) error {
	if port <= 0 {
		return nil
	}
	occupants, err := s.st.PortOccupants(ctx, serverID)
	if err != nil {
		return err
	}
	return findPortConflict(occupants, protocol, port, portEnd, excludeChainID)
}
```
3. 文件尾追加：
```go
// allocUDPPortHop 在候选空间内分配连续 length 个空闲 udp 端口（hy2 跳跃段自动分配）：
// NAT 机候选 = 各段监听侧并集（段整体须落在同一 NAT 段内，span 语义）；
// direct 机候选 = [40000,61000]。避开全部 udp 层占用（单端口与保留段）。
func allocUDPPortHop(occupants []store.PortOccupant, rs []shared.PortRange, length int) (start, end int, ok bool) {
	type window struct{ lo, hi int }
	var windows []window
	if len(rs) > 0 {
		for _, r := range rs {
			s, e := r.PubStart, r.PubEnd
			if r.ListenStart != 0 {
				s, e = r.ListenStart, r.ListenEnd
			}
			windows = append(windows, window{s, e})
		}
	} else {
		windows = []window{{40000, 61000}}
	}
	conflicts := func(p int) bool {
		for _, o := range occupants {
			if !shared.LayersOverlap("udp", o.Layers) {
				continue
			}
			oe := o.PortEnd
			if oe == 0 {
				oe = o.Port
			}
			if p >= o.Port && p <= oe {
				return true
			}
		}
		return false
	}
	for _, w := range windows {
		for s := w.lo; s+length-1 <= w.hi; s++ {
			free := true
			for p := s; p < s+length; p++ {
				if conflicts(p) {
					s = p // 跳到占用点之后继续
					free = false
					break
				}
			}
			if free && s+length-1 <= w.hi {
				return s, s + length - 1, true
			}
		}
	}
	return 0, 0, false
}
```
4. 既有调用点签名对齐（编译错误逐一修）：`nodes.go:313` → `s.checkPortConflict(r.Context(), req.ServerID, req.Protocol, *req.Port, 0, 0)`；`chains.go:346` → `s.checkPortConflict(r.Context(), entrySrv.ID, req.Node.Protocol, entryPort, 0, 0)`；`chains.go:361` → `s.checkPortConflict(r.Context(), exitSrv.ID, req.Node.Protocol, *req.Node.Port, 0, 0)`；`chains.go:623` → `s.checkPortConflict(r.Context(), servers[0].ID, req.Node.Protocol, *req.EntryPort, 0, req.ChainID)`；`chains.go:637` → `s.checkPortConflict(r.Context(), servers[len(servers)-1].ID, req.Node.Protocol, *req.Node.Port, 0, req.ChainID)`。（入口/出口单端口校验不带段；hy2 跳跃段的段校验走 Step 4 的 resolveHy2PortHop。）

Run: `cd src/backend && go test ./internal/panel/ ./internal/store/ -v && go build ./...`
Expected: PASS

- [ ] **Step 3: normalize hy2 分支 + buildVirtualConfig hy2 模板（先写失败测试）**

`src/backend/internal/panel/nodes_test.go` 追加：

```go
// TestNormalizeHysteria2 验证 hy2 矩阵（spec §2）：无 network/security 选择，
// 恒 tls + 证书模式复用；带宽/混淆/跳跃默认值；矩阵外组合 400。
func TestNormalizeHysteria2(t *testing.T) {
	req := &createNodeRequest{Protocol: "hysteria"}
	if err := req.normalize(); err != nil {
		t.Fatalf("默认 hy2 应合法: %v", err)
	}
	if req.Security != shared.SecurityTLS || req.CertMode != shared.CertModeSelfSign {
		t.Fatalf("hy2 应强制 tls/selfsign: %+v", req)
	}
	if req.TLSDomain == "" || req.Fingerprint != shared.FingerprintChrome {
		t.Fatalf("hy2 伪装域/指纹默认值缺失: %+v", req)
	}
	if req.ObfsPassword == "" || req.UpMbps != 50 || req.DownMbps != 100 || req.PortHop != "" {
		t.Fatalf("hy2 默认值不符（obfs 自动生成、50/100、port_hop 留待分配）: %+v", req)
	}
	// 矩阵外：network/reality/none 一律 400 并指明冲突字段。
	for _, bad := range []createNodeRequest{
		{Protocol: "hysteria", Network: "tcp"},
		{Protocol: "hysteria", Security: "reality"},
		{Protocol: "hysteria", Security: "none"},
		{Protocol: "hysteria", PortHop: "abc"},
		{Protocol: "hysteria", PortHop: "20000-20001"}, // 段长 < 8
		{Protocol: "hysteria", UpMbps: -1},
	} {
		if err := bad.normalize(); err == nil {
			t.Fatalf("非法组合应 400: %+v", bad)
		}
	}
	// off 关闭跳跃；显式段保留。
	off := &createNodeRequest{Protocol: "hysteria", PortHop: "off"}
	if err := off.normalize(); err != nil || off.PortHop != "" {
		t.Fatalf("port_hop=off 应归一为空: %v %+v", err, off)
	}
	explicit := &createNodeRequest{Protocol: "hysteria", PortHop: "31000-31031"}
	if err := explicit.normalize(); err != nil || explicit.PortHop != "31000-31031" {
		t.Fatalf("显式段应保留: %v %+v", err, explicit)
	}
}

// TestBuildVirtualConfigHysteria2 验证 hy2 模板形态（Task 1 定稿）：settings.clients +
// tls 证书占位符 + hysteriaSettings + finalmask（salamander/quicParams）+
// network:"hysteria" + alpn h3（后两者缺了会退化为 TCP 承载/握手失败，见事实区）。
func TestBuildVirtualConfigHysteria2(t *testing.T) {
	req := createNodeRequest{Protocol: "hysteria", Security: "tls", CertMode: "selfsign",
		TLSDomain: "www.example.com", ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100,
		PortHop: "20000-20031"}
	vc := buildVirtualConfig(req)
	var inbound map[string]any
	if err := json.Unmarshal(vc.Template, &inbound); err != nil {
		t.Fatal(err)
	}
	if inbound["protocol"] != "hysteria" {
		t.Fatalf("协议名须为 hysteria: %v", inbound["protocol"])
	}
	settings := inbound["settings"].(map[string]any)
	if settings["version"].(float64) != 2 || settings["clients"] != shared.PlaceholderClients {
		t.Fatalf("settings 不符: %v", settings)
	}
	ss := inbound["streamSettings"].(map[string]any)
	if ss["network"] != "hysteria" {
		t.Fatal("hy2 模板必须显式 network=hysteria（缺省 tcp 会退化为 TCP 承载，Task 1 实测）")
	}
	if ss["security"] != "tls" || ss["hysteriaSettings"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("streamSettings 不符: %v", ss)
	}
	fm := ss["finalmask"].(map[string]any)
	udp := fm["udp"].([]any)[0].(map[string]any)
	if udp["type"] != "salamander" || udp["settings"].(map[string]any)["password"] != "obfs-pw" {
		t.Fatalf("salamander 段不符: %v", udp)
	}
	quic := fm["quicParams"].(map[string]any)
	if quic["congestion"] != "brutal" || quic["brutalUp"] != "50 mbps" || quic["brutalDown"] != "100 mbps" {
		t.Fatalf("quicParams 不符: %v", quic)
	}
	if _, hasHop := quic["udpHop"]; hasHop {
		t.Fatal("服务端模板不含 udpHop（DNAT 路径：段由 iptables 收敛，客户端侧才声明 udpHop）")
	}
	tlsS := ss["tlsSettings"].(map[string]any)
	if alpn, ok := tlsS["alpn"].([]any); !ok || len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("tlsSettings 必须 alpn=[h3]（否则握手 no application protocol）: %v", tlsS)
	}
	certs := tlsS["certificates"].([]any)[0].(map[string]any)
	if certs["certificateFile"] != shared.PlaceholderTLSCertFile || certs["keyFile"] != shared.PlaceholderTLSKeyFile {
		t.Fatalf("证书占位符缺失: %v", certs)
	}
	if vc.ObfsPassword != "obfs-pw" || vc.UpMbps != 50 || vc.DownMbps != 100 || vc.PortHop != "20000-20031" {
		t.Fatalf("VirtualConfig 字段透传不符: %+v", vc)
	}
}
```

Run: `cd src/backend && go test ./internal/panel/ -run 'TestNormalizeHysteria2|TestBuildVirtualConfigHysteria2' -v`
Expected: FAIL（分支不存在）

实现 `src/backend/internal/panel/nodes.go`：

1. `createNodeRequest`（:81-104）`TargetPort` 行前追加：
```go
	ObfsPassword  string   `json:"obfs_password"`  // hysteria：salamander 混淆密码，留空自动生成
	UpMbps        int      `json:"up_mbps"`        // hysteria：brutal 上行声明，默认 50（0=不声明，回退 BBR）
	DownMbps      int      `json:"down_mbps"`      // hysteria：brutal 下行声明，默认 100
	PortHop       string   `json:"port_hop"`       // hysteria："off"=关闭跳跃；""=自动分配 32 段；"a-b"=显式段
```
结构体上方注释（:77-80）追加一句：`obfs_password/up_mbps/down_mbps/port_hop 仅 hysteria（恒 QUIC+TLS，无 network/security 选择）`。

2. `normalize`（:107-279）：:115-118 的 cipher 清理之后追加 hy2 早分支：
```go
	// hysteria：无 network 概念、恒 QUIC+TLS（spec §2 矩阵）；证书模式复用 TLS 双模式。
	if req.Protocol == shared.ProtocolHysteria2 {
		if req.Network != "" {
			return fmt.Errorf("hysteria2 无传输层选项（protocol 与 network 冲突，自带 QUIC）")
		}
		if req.Security != "" && req.Security != shared.SecurityTLS {
			return fmt.Errorf("hysteria2 强制 TLS 安全层（protocol 与 security 冲突）")
		}
		req.Security = shared.SecurityTLS
		req.Flow, req.Method, req.Cipher, req.Encryption = "", "", "", ""
		req.ShortID, req.Dest, req.ServerNames = "", "", nil
		req.ServiceName, req.Path, req.Mode, req.Host = "", "", "", ""
		if req.Fingerprint == "" {
			req.Fingerprint = shared.FingerprintChrome
		}
		if !shared.ValidValue(req.Fingerprint, shared.Fingerprints) {
			return fmt.Errorf("不支持的 uTLS 指纹: %s", req.Fingerprint)
		}
		if req.CertMode == "" {
			req.CertMode = shared.CertModeSelfSign
		}
		if !shared.ValidValue(req.CertMode, shared.CertModes) {
			return fmt.Errorf("不支持的证书模式: %s", req.CertMode)
		}
		if req.CertMode == shared.CertModeSelfSign {
			if req.TLSDomain == "" {
				req.TLSDomain = tlsCamouflagePool[randomInt(len(tlsCamouflagePool))]
			}
			if err := validateTLSDomain(req.TLSDomain); err != nil {
				return err
			}
		} else {
			req.TLSDomain = "" // acme：由 applyACMEDomain 从落地服务器地址检测填充
		}
		if req.ObfsPassword == "" {
			req.ObfsPassword = randomHex(16) // salamander 混淆密码（panel 生成，订阅回显）
		}
		if req.UpMbps == 0 && req.DownMbps == 0 {
			req.UpMbps, req.DownMbps = 50, 100
		}
		if req.UpMbps < 0 || req.DownMbps < 0 {
			return fmt.Errorf("hy2 带宽声明须为非负整数（up_mbps/down_mbps）")
		}
		// port_hop 三段语义：off→关闭（归一为空）；""→默认开启，由 resolveHy2PortHop
		// 自动分配（需服务器上下文，不在 normalize 内）；显式段此处做语法与长度校验。
		if req.PortHop == "off" {
			req.PortHop = ""
		} else if req.PortHop != "" {
			start, end, err := shared.ParsePortHop(req.PortHop)
			if err != nil {
				return err
			}
			if end-start+1 < shared.Hy2PortHopMinLen || end-start+1 > shared.Hy2PortHopMaxLen {
				return fmt.Errorf("端口跳跃段长须为 %d-%d（当前 %d）",
					shared.Hy2PortHopMinLen, shared.Hy2PortHopMaxLen, end-start+1)
			}
			req.PortHop = shared.FormatPortHop(start, end)
		}
		return nil
	}
```
3. `buildVirtualConfig`（:516-596）：settings switch（:518-551）追加：
```go
	case shared.ProtocolHysteria2:
		settings["version"] = 2
		settings["clients"] = shared.PlaceholderClients // hy2 用户列表键为 clients（非 users，Task 1 实测）
```
streamSettings 块（:559-568）改为：
```go
	if req.Protocol == shared.ProtocolHysteria2 {
		inbound["streamSettings"] = hy2StreamSettings(req)
	} else if shared.IsRealityProtocol(req.Protocol) {
		switch req.Security {
		case shared.SecurityNone:
			inbound["streamSettings"] = plainStreamSettings(req)
		case shared.SecurityTLS:
			inbound["streamSettings"] = tlsStreamSettings(req)
		default:
			inbound["streamSettings"] = realityStreamSettings(req)
		}
	}
```
sniffing 块（:569-571）条件追加 hy2 排除（QUIC 流量无需 sniffing）：
```go
	if req.Protocol != shared.ProtocolDokodemo && req.Protocol != shared.ProtocolHysteria2 {
```
返回结构体（:578-595）`Cipher` 行后追加：
```go
		ObfsPassword: req.ObfsPassword,
		UpMbps:       req.UpMbps,
		DownMbps:     req.DownMbps,
		PortHop:      req.PortHop,
```
4. 文件尾（`applyACMEDomain` 之后）追加：
```go
// hy2StreamSettings 构造 hy2 的 streamSettings（Task 1 实测定稿）：network 恒 "hysteria"
// （缺省 tcp 会退化为 TCP 承载）；tlsSettings 带 alpn [h3]（否则握手 no application
// protocol），证书占位符复用 P3 双模式；hysteriaSettings 仅声明 version=2（用户口令在
// settings.clients，见 Task 1 事实区）；salamander 混淆与 brutal 带宽在 finalmask。
// 服务端模板不含 udpHop——udpHop 仅客户端实现，服务端段收敛走 iptables DNAT（spec §3.2）。
func hy2StreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  "hysteria",
		"security": "tls",
		"tlsSettings": map[string]any{
			"serverName": req.TLSDomain,
			"alpn":       []string{"h3"},
			"certificates": []map[string]any{{
				"certificateFile": shared.PlaceholderTLSCertFile,
				"keyFile":         shared.PlaceholderTLSKeyFile,
			}},
		},
		"hysteriaSettings": map[string]any{"version": 2},
	}
	fm := map[string]any{}
	if req.ObfsPassword != "" {
		fm["udp"] = []map[string]any{{
			"type":     "salamander",
			"settings": map[string]any{"password": req.ObfsPassword},
		}}
	}
	quic := map[string]any{}
	if req.UpMbps > 0 || req.DownMbps > 0 {
		quic["congestion"] = "brutal"
		if req.UpMbps > 0 {
			quic["brutalUp"] = fmt.Sprintf("%d mbps", req.UpMbps)
		}
		if req.DownMbps > 0 {
			quic["brutalDown"] = fmt.Sprintf("%d mbps", req.DownMbps)
		}
	}
	if len(quic) > 0 {
		fm["quicParams"] = quic
	}
	if len(fm) > 0 {
		ss["finalmask"] = fm
	}
	return ss
}

// resolveHy2PortHop 落地 port_hop 三段语义的服务器侧部分（normalize 只做语法校验）：
// "" （默认开）→ 按服务器空闲 udp 段自动分配 32 段（避开全部已保留段与端口；
// NAT 机段整体落在监听侧段内，可用段不足最小段长 8 时关闭跳跃——功能可用，
// 仅失去跳跃的抗 QoS 能力，spec §2）；显式段 → NAT 落段校验 + 段冲突校验。
// 多跳链的"全跳同段"逐跳校验在链处理器（Task 4）做，这里只管落地（出口）机。
func (s *Server) resolveHy2PortHop(ctx context.Context, req *createNodeRequest, srv *store.Server, excludeChainID int64) error {
	if req.Protocol != shared.ProtocolHysteria2 {
		return nil
	}
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil {
		return err
	}
	occupants, err := s.st.PortOccupants(ctx, srv.ID)
	if err != nil {
		return err
	}
	if req.PortHop == "" {
		if req.Port == nil {
			// 未显式关跳跃（off 已在 normalize 归一为空，与默认空无法区分）——
			// 约定：normalize 后 PortHop=="" 且未显式 off 时尝试自动分配；
			// 区分手段：链/节点处理器在调 normalize 前记录原始值。
			// （实现要点见 Task 4 Step 2 的 hopWanted 局部变量。）
		}
		start, end, ok := allocUDPPortHop(occupants, ranges, shared.Hy2PortHopDefaultLen)
		if !ok {
			start, end, ok = allocUDPPortHop(occupants, ranges, shared.Hy2PortHopMinLen)
		}
		if ok {
			req.PortHop = shared.FormatPortHop(start, end)
		}
		return nil // 段不足：保持空 = 关闭跳跃（spec §2 允许降级）
	}
	start, end, _ := shared.ParsePortHop(req.PortHop)
	if len(ranges) > 0 && !shared.SpanInListenRanges(ranges, start, end) {
		return fmt.Errorf("端口跳跃段 %s 不在该 NAT 服务器可用段内", req.PortHop)
	}
	return findPortConflict(occupants, req.Protocol, start, end, excludeChainID)
}
```

注意：`resolveHy2PortHop` 的"off 与默认空"区分依赖调用方在 normalize **之前**读原始 `req.PortHop == "off"`。为消除这个隐晦点，实现时把 normalize 的 off 归一改为保留哨兵：normalize 内 `if req.PortHop == "off" { req.PortHop = "off" }`（不归一），由 `resolveHy2PortHop` 开头处理：
```go
	if req.PortHop == "off" {
		req.PortHop = ""
		return nil
	}
```
同时 normalize 里的显式段校验分支条件改为 `req.PortHop != "" && req.PortHop != "off"`，normalize 测试里 off 断言相应改为 `off.PortHop != "off"`。**以本注释版为准**（消除隐晦的调用方约定）。

5. `handleCreateNode`（:282-352）：:301-305 的 applyACMEDomain 之后、端口校验块之前插入：
```go
	// hy2 端口跳跃：自动分配 / 显式段 NAT+冲突校验（spec §2/§3.2）。
	if err := s.resolveHy2PortHop(r.Context(), &req, srv, 0); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```
（applyACMEDomain 现有判定 `req.Security != tls || req.CertMode != acme` 对 hy2 天然适用——normalize 已把 hy2 security 置 tls。）

Run: `cd src/backend && go test ./internal/panel/ -v && go build ./...`
Expected: PASS（既有 normalize/模板用例不回归——hy2 早分支在 cipher 清理之后、IsRealityProtocol 块之前 return，存量协议路径不变）

- [ ] **Step 4: openapi 契约 + 前端类型再生成**

`docs/openapi.yaml` `VirtualConfig` schema（:902-924）`tls_domain` 行后追加：
```yaml
        obfs_password: {type: string}
        up_mbps: {type: integer, minimum: 0}
        down_mbps: {type: integer, minimum: 0}
        port_hop: {type: string}
```
节点/链创建请求体 schema（含 `cert_mode`/`tls_domain` 的请求 schema，定位：`grep -n "tls_domain" docs/openapi.yaml` 的其余命中）同样追加这四个属性。

Run: `cd src/frontend && npm run generate:api && npm run build`
Expected: PASS（`--check` 不拦截；生成类型含新字段）

Run: `cd src/backend && go test ./... && go vet ./...`
Expected: PASS

提交：`git add -A src/backend docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts && git commit -m "feat(panel): P4 hy2 节点矩阵/模板与端口段冲突治理"`

---
### Task 4: 链侧数据模型——service_endpoint_id 迁移、入口协议区块、hy2 出口共享监听

**Files:**
- Modify: `src/backend/internal/store/store.go`（chains CREATE TABLE :422-436 加列）
- Modify: `src/backend/internal/store/migrations.go`（schemaVersion :14 → 18、chains 列清单 :140-150 追加）
- Modify: `src/backend/internal/store/chains.go`（Chain :47-59、chainCols :81-82、scanChain :84-92 及所有 INSERT/UPDATE chains 的语句——`grep -n "INSERT INTO chains\|UPDATE chains SET" src/backend/internal/store/*.go` 逐点加列）
- Modify: `src/backend/internal/store/revisions.go`（ChainRevisionSnapshot :52-63 加 ServiceEndpointID、InitialChainDeployment :100-110 加 ServiceEndpointID、CreateInitialChainDeployment 写入）
- Modify: `src/backend/internal/store/endpoints.go`（追加 EnsureProtocolSharedEndpoint；EnsureSharedEndpoint 注释 :66-70 更新为 vless/hysteria 双用途）
- Modify: `src/backend/internal/panel/chains.go`（createChainRequest :210-219 / editChainRequest :226-233 加 EntryNode、handleCreateChain :237-491、handleEditChain :499-855、revisionTopology :857-880、toChainDTO 加 entry_config）
- Modify: `src/backend/internal/dispatch/revision_plan.go`（validateTopology 白名单 :126-131）
- Modify: `docs/openapi.yaml`（Chain schema 加 entry_config；链创建/编辑请求 schema 加 entry_node）
- Test: `src/backend/internal/store/migrations_test.go`（追加 v18 用例）、`src/backend/internal/store/endpoints_test.go`（追加）、`src/backend/internal/panel/chains_test.go`（追加）、`src/backend/internal/dispatch/revision_plan_test.go`（追加）

**Interfaces:**
- Consumes: `EnsureSharedEndpoint`（endpoints.go:71-118，store 层无协议硬编码）；`buildVirtualConfig/resolveHy2PortHop/allocUDPPortHop`（Task 3）；`transportForServers/inboundCapable`（chains.go:910-918,1168-1170）。
- Produces:
  - `chains.service_endpoint_id INTEGER NOT NULL DEFAULT 0`（schemaVersion 17→18；0=无出口侧共享监听，存量行零迁移成本）。
  - `Chain.ServiceEndpointID int64`；`ChainRevisionSnapshot.ServiceEndpointID int64 \`json:"service_endpoint_id,omitempty"\``；`InitialChainDeployment.ServiceEndpointID int64`。
  - `func (s *Store) EnsureProtocolSharedEndpoint(ctx context.Context, serverID int64, protocol string, port int, config json.RawMessage) (*SharedEndpoint, bool, error)`——hy2 出口共享：同机同协议仅一个活跃共享监听（pending/applying/active 即并入，首链 profile 为准，spec §3.2）；port 仅用于创建时（0=agent 自动）。
  - `createChainRequest.EntryNode *createNodeRequest \`json:"entry_node,omitempty"\``；`editChainRequest.EntryNode *createNodeRequest`（nil=端到端现状）。
  - `chainDTO` 新增 `EntryConfig *shared.VirtualConfig \`json:"entry_config,omitempty"\``（链有入口端点时回填，前端编辑回填用，Task 8）。
  - revision 白名单新值：hop.Transport `"hy2"`（语义：本跳→下一跳为入口终结模式的 hy2 直拨段，仅允许出现在倒数第二跳且出口协议为 hysteria）。

**数据模型一句话**：入口协议区块 = 入口机 vless+reality 共享端点（复用 `chains.endpoint_id` 与现有 EnsureSharedEndpoint/reconcile 骨架，出口协议不再限 vless）；hy2 出口共享监听 = 出口机 hysteria 共享端点（新列 `chains.service_endpoint_id` 指向，复用 shared_endpoints 表与端点状态机）。一条 hy2 入口终结链同时挂两个端点；端到端 hy2 链只挂 service_endpoint_id。

- [ ] **Step 1: store migration v18 + Chain/快照/部署结构加列（先写失败测试）**

`src/backend/internal/store/migrations_test.go` 追加：

```go
// TestMigrateV18ServiceEndpointID 验证 chains.service_endpoint_id 列迁移（P4）：
// 存量库（v17）升级后列存在且存量行为 0（端到端现状）。
func TestMigrateV18ServiceEndpointID(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 17`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO chains (name, status) VALUES ('legacy', 'active')`); err != nil {
		t.Fatal(err)
	}
	st := &Store{db: db}
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	_ = st
	var version, serviceEndpointID int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	if err := db.QueryRow(`SELECT service_endpoint_id FROM chains WHERE name='legacy'`).
		Scan(&serviceEndpointID); err != nil {
		t.Fatalf("迁移后列缺失或读失败: %v", err)
	}
	if serviceEndpointID != 0 {
		t.Fatalf("存量行应为 0: %d", serviceEndpointID)
	}
}
```

Run: `cd src/backend && go test ./internal/store/ -run TestMigrateV18 -v`
Expected: FAIL（列不存在）

实现：
1. `migrations.go:14` `schemaVersion = 17` → `18`；:140-150 `"chains"` 列清单 `{"deleted_at", ...}` 行前（或行后，顺序无关）追加：
```go
			{"service_endpoint_id", "INTEGER NOT NULL DEFAULT 0"}, // hy2 出口侧共享监听（P4，0=无）
```
2. `store.go` chains CREATE TABLE（:422-436）`endpoint_id` 行后追加：
```go
    service_endpoint_id      INTEGER NOT NULL DEFAULT 0,
```
3. `chains.go`：`Chain`（:47-59）`EndpointID` 行后加 `ServiceEndpointID int64`；`chainCols`（:81-82）改为：
```go
const chainCols = `id, name, service_node_id, endpoint_id, service_endpoint_id, service_uuid, published_revision_id, desired_revision_id,
	traffic_multiplier_milli, status, error, created_at`
```
`scanChain`（:84-92）Scan 参数在 `&c.EndpointID` 后插入 `&c.ServiceEndpointID`。随后 `grep -rn "INSERT INTO chains\|UPDATE chains SET" src/backend/internal/store/` 逐点加列（CreateInitialChainDeployment 的 INSERT、ReplaceWorkingChainTopology 等），值来自 `InitialChainDeployment.ServiceEndpointID` / `ChainRevisionSnapshot.ServiceEndpointID`。
4. `revisions.go`：`ChainRevisionSnapshot`（:52-63）`EndpointID` 行后加：
```go
	ServiceEndpointID      int64              `json:"service_endpoint_id,omitempty"` // hy2 出口侧共享监听（P4）
```
`InitialChainDeployment`（:100-110）`EndpointID` 行后加 `ServiceEndpointID int64`。

Run: `cd src/backend && go test ./internal/store/ -v && go build ./...`
Expected: PASS（含 v18 用例；既有迁移/链用例不回归——新列默认值 0 不改变旧行为）

- [ ] **Step 2: store.EnsureProtocolSharedEndpoint（hy2 出口共享，先写失败测试）**

`src/backend/internal/store/endpoints_test.go` 追加：

```go
// TestEnsureProtocolSharedEndpoint 验证 hy2 出口共享：同机同协议仅一个活跃监听，
// 第二链并入（首链 profile/端口为准）；不同服务器互不影响；删除态不复用。
func TestEnsureProtocolSharedEndpoint(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	srv := insertTestServer(t, st)
	cfg1 := json.RawMessage(`{"protocol":"hysteria","port":0,"port_hop":"30000-30031","template":{}}`)
	ep1, created, err := st.EnsureProtocolSharedEndpoint(ctx, srv, "hysteria", 0, cfg1)
	if err != nil || !created {
		t.Fatalf("首链应创建: created=%v err=%v", created, err)
	}
	cfg2 := json.RawMessage(`{"protocol":"hysteria","port":0,"port_hop":"40000-40031","template":{}}`)
	ep2, created, err := st.EnsureProtocolSharedEndpoint(ctx, srv, "hysteria", 0, cfg2)
	if err != nil || created || ep2.ID != ep1.ID {
		t.Fatalf("第二链应并入首链监听: id=%d created=%v err=%v", ep2.ID, created, err)
	}
	// 显式端口创建；后续 port=0 的链也并入（端口以既有为准）。
	// 非法参数（空 config / 非法 JSON / serverID=0）报错。
	if _, _, err := st.EnsureProtocolSharedEndpoint(ctx, 0, "hysteria", 0, cfg1); err == nil {
		t.Fatal("serverID=0 应报错")
	}
}
```

Run: `cd src/backend && go test ./internal/store/ -run TestEnsureProtocolSharedEndpoint -v`
Expected: FAIL（方法不存在）

实现 `src/backend/internal/store/endpoints.go`：
1. `EnsureSharedEndpoint` 注释（:66-70）更新为"入口侧 vless 共享端点用；hy2 出口共享见 EnsureProtocolSharedEndpoint"。
2. `EnsureSharedEndpoint` 之后追加：
```go
// EnsureProtocolSharedEndpoint 返回服务器上指定协议的共享监听（hy2 出口侧共享，
// spec §3.2：同机多条 hy2 链共享同一监听与端口段，首条链路的证书/混淆/带宽/段参数
// 为准）：存在 pending/applying/active 的同协议端点即并入（显式 port 也并入，
// 端口以既有监听为准），否则以 config 创建。返回 (endpoint, created, error)。
func (s *Store) EnsureProtocolSharedEndpoint(ctx context.Context, serverID int64, protocol string,
	port int, config json.RawMessage) (*SharedEndpoint, bool, error) {
	if serverID <= 0 || protocol == "" || !json.Valid(config) {
		return nil, false, fmt.Errorf("invalid protocol shared endpoint")
	}
	existing, err := scanEndpoint(s.db.QueryRowContext(ctx, `SELECT `+endpointCols+`
		FROM shared_endpoints WHERE server_id=? AND protocol=? AND status IN ('pending','applying','active')
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'applying' THEN 1 ELSE 2 END, id LIMIT 1`,
		serverID, protocol))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO shared_endpoints
		(server_id, protocol, port, profile_hash, config_template) VALUES (?, ?, ?, ?, ?)`,
		serverID, protocol, port, protocol, string(config)) // profile_hash 无并入语义，存协议名占位
	if err != nil {
		return nil, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	endpoint, err := s.SharedEndpointByID(ctx, id)
	return endpoint, true, err
}
```

Run: `cd src/backend && go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 3: revision_plan 白名单加 "hy2"（先写失败测试）**

`src/backend/internal/dispatch/revision_plan_test.go` 追加：

```go
// TestValidateTopologyHy2Transport 验证末段 hy2 transport 白名单（P4 入口终结）。
func TestValidateTopologyHy2Transport(t *testing.T) {
	topo := RevisionTopology{RevisionID: 1, ServiceID: 9, Hops: []RevisionHopSpec{
		{HopID: 1, ServerID: 1, Transport: "hy2"},
		{HopID: 2, ServerID: 2},
	}}
	if err := validateTopology(topo); err != nil {
		t.Fatalf("hy2 transport 应合法: %v", err)
	}
	topo.Hops[0].Transport = "bogus"
	if err := validateTopology(topo); err == nil {
		t.Fatal("未知 transport 应拒绝")
	}
}
```

Run: `cd src/backend && go test ./internal/dispatch/ -run TestValidateTopologyHy2 -v`
Expected: FAIL（"hy2" 不在白名单）

实现 `src/backend/internal/dispatch/revision_plan.go:126-131`：
```go
		switch hop.Transport {
		case "", "direct", "encrypted", "reverse", "hy2": // hy2：入口终结模式末段（P4）
		default:
			return fmt.Errorf("unsupported transport %q", hop.Transport)
		}
```

Run: `cd src/backend && go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 4: chains.go 入口协议区块 + hy2 出口共享接线（先写失败测试）**

`src/backend/internal/panel/chains_test.go` 追加（复用同包既有 HTTP 测试 helper——参照现有 chain 创建用例的 server/agent fixture 风格）：

```go
// TestCreateChainHy2ExitShared 验证 hy2 出口链挂出口侧共享监听：两条同机 hy2 链
// 共用同一 service_endpoint_id（spec §3.2 出口共享），端到端（无入口区块）时
// endpoint_id 保持 0；入口前置端口校验按 udp 层判定。
func TestCreateChainHy2ExitShared(t *testing.T) {
	// 两台 direct 服务器 A(入口)/C(出口)，与现有链测试同款 fixture。
	// 链1：POST /api/chain/create {"entry":{"server_id":A},"exit":{"server_id":C},
	//   "node":{"protocol":"hysteria"}} → 201；chain.service_endpoint_id != 0，endpoint_id == 0。
	// 链2：同机同参数 → 201；service_endpoint_id 与链1相同（并入共享监听）。
	// 断言 shared_endpoints 表 C 机上仅一行 protocol='hysteria'。
}

// TestCreateChainEntryProtocolBlock 验证入口协议区块：勾选 entry_node（vless+reality）
// 的 hy2 中转链 → endpoint_id != 0（入口机 vless 端点）且 service_endpoint_id != 0，
// hops[0].transport == "hy2"；v1 非法组合 400（单跳、出口协议 vmess、entry_node 协议非 vless）。
func TestCreateChainEntryProtocolBlock(t *testing.T) {
	// 合法：{"entry":{"server_id":A},"exit":{"server_id":C},"entry_node":{"protocol":"vless"},
	//   "node":{"protocol":"hysteria"}} → 201，两端点齐备。
	// 非法：entry_node.protocol="trojan" → 400「入口协议区块 v1 仅支持 VLESS+Reality」；
	//        单跳 + entry_node → 400；出口 node.protocol="vmess" + entry_node → 400。
}
```

Run: `cd src/backend && go test ./internal/panel/ -run 'TestCreateChainHy2|TestCreateChainEntryProtocol' -v`
Expected: FAIL（字段/分支不存在，编译失败）

实现 `src/backend/internal/panel/chains.go`：

1. `createChainRequest`（:210-219）与 `editChainRequest`（:226-233）`Node` 行后各追加：
```go
	EntryNode         *createNodeRequest `json:"entry_node,omitempty"` // 入口协议区块（P4；nil=端到端现状，v1 仅 vless+reality）
```
2. `handleCreateChain`：`req.Node.normalize()`（:260）之后插入入口区块规范化：
```go
	// 入口协议区块（P4 §3.2 异构入口）：v1 仅 vless+reality；仅多跳；出口仅 hy2/vless。
	if req.EntryNode != nil {
		if len(refs) < 2 {
			writeError(w, http.StatusBadRequest, "入口协议区块仅用于多跳链路（单跳客户端直连出口即可）")
			return
		}
		if req.EntryNode.Protocol != "" && req.EntryNode.Protocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议区块 v1 仅支持 VLESS+Reality")
			return
		}
		if req.Node.Protocol != shared.ProtocolHysteria2 && req.Node.Protocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议区块 v1 仅支持 hysteria2/vless 出口")
			return
		}
		req.EntryNode.Protocol = shared.ProtocolVLESS
		req.EntryNode.Security = shared.SecurityReality // 固定 reality（子参数可空自动生成）
		req.EntryNode.Name = req.Name
		req.EntryNode.ServerID = refs[0].ServerID
		if err := req.EntryNode.normalize(); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("入口协议参数: %v", err))
			return
		}
	}
```
3. `handleCreateChain`：:301-306 的 `applyACMEDomain(&req.Node, exitSrv)` 之后插入 hy2 出口侧处理（端口跳跃跨跳校验 + 出口共享监听）。**hy2 跳跃段全跳同段语义**：端到端模式段落在每一跳 udp 空间（逐端口 1:1），入口终结模式段仅存在于末段 hy2 拨号（出口 DNAT），但端口段治理统一按"落地机保留"处理，逐跳 NAT 落段校验仅对端到端模式逐跳执行：
```go
	// hy2 端口跳跃（spec §2/§3.2）：先对出口机做分配/显式段校验（复用 Task 3 助手）；
	// 端到端（无入口区块）时逐跳校验段整体落在各跳 NAT 段内且与各跳既有占用无冲突。
	if req.Node.Protocol == shared.ProtocolHysteria2 {
		if err := s.resolveHy2PortHop(r.Context(), &req.Node, exitSrv, 0); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Node.PortHop != "" && req.EntryNode == nil {
			start, end, _ := shared.ParsePortHop(req.Node.PortHop)
			for _, hopSrv := range servers[:len(servers)-1] {
				ranges, err := shared.ParsePortRanges(hopSrv.AllowedPorts)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if len(ranges) > 0 && !shared.SpanInListenRanges(ranges, start, end) {
					writeError(w, http.StatusBadRequest,
						fmt.Sprintf("端口跳跃段 %s 不在服务器 %s 可用段内", req.Node.PortHop, hopSrv.Alias))
					return
				}
				if err := s.checkPortConflict(r.Context(), hopSrv.ID, req.Node.Protocol, start, end, 0); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}
	}
```
4. `handleCreateChain`：:344-350 的入口冲突校验门控改为按"是否挂入口端点"（vless 出口或勾选入口区块都走共享端点合并）：
```go
	// 入口监听是 dokodemo 管道（层随出口协议：ss 为 tcp,udp，hy2 为 udp，其余 tcp）；
	// vless 出口或勾选入口协议区块时入口走共享端点合并，跳过前置校验。
	entryShared := req.Node.Protocol == shared.ProtocolVLESS || req.EntryNode != nil
	if entryPort > 0 && !entryShared {
		if err := s.checkPortConflict(r.Context(), entrySrv.ID, req.Node.Protocol, entryPort, 0, 0); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
```
5. `handleCreateChain`：端点创建块（:375-405）重构为"入口端点 + 出口共享监听"两段。`if vc.Protocol == shared.ProtocolVLESS {` 块整体替换为：
```go
	// 入口端点：vless 出口（现状）或勾选入口协议区块（P4，入口协议配置独立于出口）。
	if req.Node.Protocol == shared.ProtocolVLESS || req.EntryNode != nil {
		endpointSource := vc
		if req.EntryNode != nil {
			endpointSource = buildVirtualConfig(*req.EntryNode)
		}
		endpointConfig := endpointSource
		endpointConfig.Port = entryPort
		endpointConfig.StaticClients = nil
		endpointJSON, err := json.Marshal(endpointConfig)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile := endpointConfig
		profile.Port = 0
		profileJSON, _ := json.Marshal(profile)
		profileHash := fmt.Sprintf("%x", sha256.Sum256(profileJSON))
		endpoint, _, err := s.st.EnsureSharedEndpoint(r.Context(), entrySrv.ID, shared.ProtocolVLESS,
			entryPort, profileHash, endpointJSON)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		endpointID = endpoint.ID
		serviceUUID = uuid.NewString()
		vc.StaticClients = []shared.ClientCredential{{ID: serviceUUID, Email: "tunnel:" + serviceUUID}}
		// A one-hop shared chain exits directly from the endpoint and does not
		// need a second public listener on the same server.
		if len(servers) == 1 {
			vc.Port = 0
			req.Node.Port = nil
		}
	}
	// hy2 出口共享监听（P4 §3.2）：同机多链并入同一 hy2 监听与端口段（首链 profile 为准）。
	serviceEndpointID := int64(0)
	if vc.Protocol == shared.ProtocolHysteria2 {
		endpointJSON, err := json.Marshal(vc)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		port := 0
		if req.Node.Port != nil {
			port = *req.Node.Port
		}
		svcEndpoint, _, err := s.st.EnsureProtocolSharedEndpoint(r.Context(), exitSrv.ID,
			shared.ProtocolHysteria2, port, endpointJSON)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		serviceEndpointID = svcEndpoint.ID
		// 出口节点自身不再监听（共享监听承载）；端口/实现参数以共享端点 realized 为准（Task 6 镜像）。
		vc.Port = 0
		req.Node.Port = nil
	}
```
（注意原块内 `endpointID`/`serviceUUID` 变量声明 :373-374 保留；`serviceEndpointID` 新声明。入口区块链的 `entryShared` 语义与 hop0 forward_port 归集（:423-426）自动兼容——`endpointID != 0` 时 hop0 forward 为 0 由既有逻辑保证。）
6. `handleCreateChain`：hop 循环的 transport 赋值（:429-431）改为支持末段 hy2：
```go
		transport := ""
		if i < len(servers)-1 {
			transport = transportForServers(servers[i+1], plaintext)
			// 入口终结模式末段：出口为 hy2 且可直连时由本跳 xray 以 hy2 outbound 直拨（§3.2）；
			// 出口无入站能力（零公共端口 NAT）保持 reverse（功能可用，失去 UDP 收益）。
			if i == len(servers)-2 && req.EntryNode != nil &&
				req.Node.Protocol == shared.ProtocolHysteria2 && inboundCapable(servers[i+1]) {
				transport = "hy2"
			}
		}
```
7. `handleCreateChain`：`CreateInitialChainDeployment` 调用（:442-446）加字段：
```go
		Name: req.Name, ServiceServerID: exitSrv.ID, ServiceProtocol: vc.Protocol,
		ServicePort: req.Node.Port, ServiceConfig: vcJSON, EndpointID: endpointID,
		ServiceEndpointID: serviceEndpointID, ServiceUUID: serviceUUID,
```
8. `handleEditChain`（:499-855）镜像同样改动：
   - :587 `req.Node.normalize()` 后插入与第 2 点相同的入口区块规范化（`len(req.Hops)` 替换 `len(refs)`，`servers[0]` 替换 `refs[0]`）。
   - :596 applyACMEDomain 后插入与第 3 点相同的 resolveHy2PortHop + 逐跳校验（excludeChainID 传 `req.ChainID`）。
   - :622 门控 `req.Node.Protocol != shared.ProtocolVLESS` 改为 `!entryShared`（entryShared 定义同第 4 点）。
   - :635 `singleHopShared` 保持 vless 语义不变；hy2 链出口端口校验因 `req.Node.Port` 在共享后置 nil 而自然跳过。
   - 端点复用/替换块（:645-681）：条件 `vc.Protocol == shared.ProtocolVLESS && current.Snapshot.EndpointID != 0` 改为 `(vc.Protocol == shared.ProtocolVLESS || req.EntryNode != nil) && current.Snapshot.EndpointID != 0`，块内 `endpointSource` 同第 5 点分派；块后追加 hy2 出口共享（同第 5 点第二段，EnsureProtocolSharedEndpoint + vc.Port=0）。
   - `desired` 快照（:738-741）加 `ServiceEndpointID: serviceEndpointID`。
   - 编辑 transport 循环（:717-737）：`transportForServers` 计算后加与第 6 点相同的末段 hy2 覆写。
9. `revisionTopology`（:857-880）：返回值 `dispatch.RevisionTopology` 加 `ServiceEndpointID`（先确认 dispatch.RevisionTopology 定义并加字段 `ServiceEndpointID int64`——`revision_plan.go` 中 RevisionTopology struct，与 EndpointID 无关的纯携带字段，不进哈希）。
10. `toChainDTO`（:137-187 区域）：`chainDTO` 加 `EntryConfig *shared.VirtualConfig \`json:"entry_config,omitempty"\``；构造处当 `c.EndpointID != 0` 时 `SharedEndpointByID` 取端点 `ConfigTemplate` 反序列化填入（取不到则留 nil，不阻断列表）。

Run: `cd src/backend && go test ./internal/panel/ ./internal/dispatch/ ./internal/store/ -v && go build ./...`
Expected: PASS（含新用例；既有链用例——vless 端点合并、编辑原样保存、ss 明文链——不回归）

- [ ] **Step 5: openapi 契约（Chain.entry_config / 链请求 entry_node）+ 前端类型再生成**

`docs/openapi.yaml`：Chain schema 加 `entry_config`（`$ref: '#/components/schemas/VirtualConfig'`）；链创建/编辑请求 schema 加 `entry_node`（同节点请求 schema 引用）。定位：`grep -n "entry_port\|traffic_multiplier" docs/openapi.yaml`。

Run: `cd src/frontend && npm run generate:api && npm run build && cd ../backend && go test ./...`
Expected: PASS

提交：`git add -A src/backend docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts && git commit -m "feat(panel): P4 入口协议区块与 hy2 出口共享监听数据模型"`

---
### Task 5: agent——hy2 用户分支与 realized 回显、版本门控、DNAT 端口跳跃、共享端点放行与 hy2 outbound、forward 末段/逐端口段

**Files:**
- Modify: `src/agent/internal/xray/fill.go`（clientCredentialEntry :197-222 加 hy2 分支、fillTemplate realized 提取 :156-174 加 hy2 字段）
- Modify: `src/agent/internal/xray/manager.go`（ApplyNode :111-141 版本门控与 DNAT 接线、RemoveNode :144-166 DNAT 清理）
- Create: `src/agent/internal/xray/udphop.go`（iptables DNAT 包装，测试缝）
- Modify: `src/agent/internal/xray/endpoint.go`（ApplySharedEndpoint :25-111 放行 hysteria + layers + DNAT、renderSharedEndpointOutbound :128-192 按 ExitProtocol 分派）
- Modify: `src/agent/internal/xray/config.go`（mutateClients 用户列表键 :130-133 加 hy2→users）
- Modify: `src/agent/internal/xray/chain.go`（renderForward :344-380 Hy2Target/HopPortEnd、applyChainPiece :685 多 inbound、chainPieceTags :580-601）
- Modify: `src/agent/internal/state/state.go`（ChainPiece :94-106 加 Inbounds）
- Test: `src/agent/internal/xray/fill_test.go`（追加）、`src/agent/internal/xray/udphop_test.go`（新建）、`src/agent/internal/xray/endpoint_test.go`（追加）、`src/agent/internal/xray/chain_test.go`（追加）、`src/agent/internal/xray/manager_test.go`（追加）

**Interfaces:**
- Consumes: `shared.ProtocolHysteria2/Hy2UserPassword/XrayMinVersionHy2/Hy2PortHopInterval/ParsePortHop/Hy2DialSpec`（Task 2）；`ensureTLSCertificate`（P3，fill.go:60-68 占位符块已接线）；`pickChainPort/forwardLayers`（chain.go:351,401-406）。
- Produces:
  - `clientCredentialEntry` hy2 分支：用户条目 `{"auth": Hy2UserPassword(id), "email": email, "level": 0}`。
  - `func xrayVersionAtLeast(version, min string) bool`（agent xray 包级私有，"26.3.27" 三段数字比较；解析失败返回 false=门控拒绝）。
  - `func (m *Manager) ensureUdpHopDNAT(tag string, portHop string, listenPort int) error`、`func (m *Manager) removeUdpHopDNAT(tag string) error`（udphop.go；portHop 空 = no-op）。
  - `renderHy2Outbound(tag string, dial shared.Hy2DialSpec) map[string]any`（endpoint.go，Task 6 的 chain.go 末段也复用——放 endpoint.go 导出同包函数）。
  - `state.ChainPiece` 新增 `Inbounds []json.RawMessage \`json:"inbounds,omitempty"\``（逐端口跳跃段的附加 dokodemo inbound；主 inbound 仍在 Inbound 字段）。

- [ ] **Step 1: fill.go hy2 用户条目与 realized 字段（先写失败测试）**

`src/agent/internal/xray/fill_test.go` 追加：

```go
// TestClientCredentialEntryHysteria 验证 hy2 用户条目形态（settings.clients 元素，P4）。
func TestClientCredentialEntryHysteria(t *testing.T) {
	e := clientCredentialEntry(shared.ProtocolHysteria2, "", "",
		shared.ClientCredential{ID: "uuid-1", Email: "access:7"})
	if e["auth"] != shared.Hy2UserPassword("uuid-1") || e["email"] != "access:7" || e["level"] != 0 {
		t.Fatalf("hy2 用户条目不符: %v", e)
	}
	if _, hasID := e["id"]; hasID {
		t.Fatal("hy2 条目不应携带 id 键（auth 为凭据）")
	}
}

// TestFillTemplateHysteriaRealized 验证 hy2 模板的 realized 回显（SNI/pin/obfs/带宽/段）。
func TestFillTemplateHysteriaRealized(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolHysteria2, Security: shared.SecurityTLS,
		CertMode: shared.CertModeSelfSign, TLSDomain: "www.example.com",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031",
		Template: json.RawMessage(`{"tag":"{{TAG}}","protocol":"hysteria","port":"{{PORT}}",
			"settings":{"version":2,"clients":"{{CLIENTS}}"},
			"streamSettings":{"network":"hysteria","security":"tls",
			"tlsSettings":{"serverName":"www.example.com","alpn":["h3"],"certificates":[{"certificateFile":"{{TLS_CERT_FILE}}","keyFile":"{{TLS_KEY_FILE}}"}]},
			"hysteriaSettings":{"version":2},
			"finalmask":{"udp":[{"type":"salamander","settings":{"password":"obfs-pw"}}],
			"quicParams":{"congestion":"brutal","brutalUp":"50 mbps","brutalDown":"100 mbps"}}}}`),
	}
	m := testManager(t) // 复用同包既有 manager fixture（xray 桩）
	_, realized, err := m.fillTemplate(21000, "node_1", vc, []string{"uuid-1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Security != "tls" || realized.SNI != "www.example.com" {
		t.Fatalf("SNI/security 回显不符: %+v", realized)
	}
	if realized.ObfsPassword != "obfs-pw" || realized.UpMbps != 50 || realized.DownMbps != 100 ||
		realized.PortHop != "20000-20031" {
		t.Fatalf("hy2 字段回显不符: %+v", realized)
	}
}
```

Run: `cd src/agent && go test ./internal/xray/ -run 'TestClientCredentialEntryHysteria|TestFillTemplateHysteriaRealized' -v`
Expected: FAIL（分支/字段不存在）

实现 `src/agent/internal/xray/fill.go`：
1. `clientCredentialEntry`（:197-222）switch 内 `case shared.ProtocolShadowsocks:` 行前插入：
```go
	case shared.ProtocolHysteria2:
		// hy2 用户条目（settings.clients 元素）：auth 为确定性派生口令（订阅/入口终结 outbound 同源）。
		return map[string]any{"auth": shared.Hy2UserPassword(credential.ID), "email": email, "level": 0}
```
2. `fillTemplate` realized 结构体字面量（:156-174）`CertSHA256: certPin,` 行后追加：
```go
		ObfsPassword: vc.ObfsPassword,
		UpMbps:       vc.UpMbps,
		DownMbps:     vc.DownMbps,
		PortHop:      vc.PortHop,
```
（hy2 这四个参数是模板内固定值，直接以 vc 回显——与 Method/Flow 同款"vc 透传"语义；SNI/CertSHA256 由 P3 证书管线已填。）

3. `config.go` `mutateClients` 键选择（:130-133）保持默认 `clients` 即可——hy2 用户列表键也是 `settings.clients`（Task 1 实测修正：xray hy2 inbound 用 `json:"clients"`，无需为 hysteria 特判；原计划 `key = "users"` 分支删除）：

Run: `cd src/agent && go test ./internal/xray/ -v && go build ./...`
Expected: PASS

- [ ] **Step 2: 版本门控（先写失败测试）**

`src/agent/internal/xray/manager_test.go` 追加：

```go
// TestXrayVersionAtLeast 验证三段数字版本比较（hy2 门控 ≥ 26.3.27）。
func TestXrayVersionAtLeast(t *testing.T) {
	cases := map[string]bool{
		"26.3.27": true, "26.3.28": true, "26.10.1": true, "27.0.0": true,
		"26.3.26": false, "26.2.99": false, "25.12.8": false,
		"": false, "unknown": false, "26.3": false,
	}
	for v, want := range cases {
		if got := xrayVersionAtLeast(v, shared.XrayMinVersionHy2); got != want {
			t.Errorf("xrayVersionAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestApplyNodeHysteriaVersionGate 验证低版本 xray 拒绝 hy2 节点（指向性错误文案）。
func TestApplyNodeHysteriaVersionGate(t *testing.T) {
	m := testManager(t) // fixture 的 Version 桩返回 "25.8.3"
	vc := shared.VirtualConfig{Protocol: shared.ProtocolHysteria2, Template: json.RawMessage(`{}`)}
	if _, err := m.ApplyNode(1, vc, nil, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "节点 xray 版本过低") {
		t.Fatalf("低版本应拒绝并提示升级: %v", err)
	}
}
```

（fixture 的 Version 桩：参照同包现有测试对 `m.bin` 的桩法——若现有测试用假 xray 脚本，则让脚本输出 `Xray 25.8.3`；实现时以同包现状为准。）

Run: `cd src/agent && go test ./internal/xray/ -run 'TestXrayVersionAtLeast|TestApplyNodeHysteriaVersionGate' -v`
Expected: FAIL（函数不存在）

实现 `src/agent/internal/xray/manager.go`：
1. `ApplyNode`（:111-141）`m.mu.Lock()` 之后插入：
```go
	// hy2 版本门控（spec §3.3/§5）：xray < 26.3.27 无 hy2 inbound，明确拒绝并指向升级链路。
	if vc.Protocol == shared.ProtocolHysteria2 {
		version, _ := m.Version()
		if !xrayVersionAtLeast(version, shared.XrayMinVersionHy2) {
			return nil, fmt.Errorf("节点 xray 版本过低（hysteria2 需要 xray ≥ %s，当前 %s），请先在节点页升级 xray",
				shared.XrayMinVersionHy2, version)
		}
	}
```
2. `Version` 之后追加：
```go
// xrayVersionAtLeast 三段数字版本比较（"26.3.27" ≥ min）；解析失败返回 false（门控拒绝）。
func xrayVersionAtLeast(version, min string) bool {
	parse := func(s string) (int, int, int, bool) {
		var a, b, c int
		if n, err := fmt.Sscanf(s, "%d.%d.%d", &a, &b, &c); err != nil || n != 3 {
			return 0, 0, 0, false
		}
		return a, b, c, true
	}
	a1, b1, c1, ok1 := parse(version)
	a2, b2, c2, ok2 := parse(min)
	if !ok1 || !ok2 {
		return false
	}
	if a1 != a2 {
		return a1 > a2
	}
	if b1 != b2 {
		return b1 > b2
	}
	return c1 >= c2
}
```
3. `ApplySharedEndpoint`（endpoint.go:25-33）同样门控——见 Step 4 一并改。

Run: `cd src/agent && go test ./internal/xray/ -v`
Expected: PASS

- [ ] **Step 3: udphop.go——iptables DNAT 管理（先写失败测试）**

`src/agent/internal/xray/udphop_test.go` 新建：

```go
package xray

import (
	"strings"
	"testing"
)

// TestEnsureUdpHopDNAT 验证 DNAT 规则建立/清理的 iptables 调用序列（命令经测试缝捕获）。
func TestEnsureUdpHopDNAT(t *testing.T) {
	var calls []string
	runIPTables = func(bin string, args ...string) error {
		calls = append(calls, bin+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { runIPTables = runIPTablesImpl }()
	m := &Manager{}
	if err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "--dport 20000:20031") || !strings.Contains(joined, "--to-ports 14439") {
		t.Fatalf("DNAT 规则不符:\n%s", joined)
	}
	if !strings.Contains(joined, "lattix:node_1") {
		t.Fatalf("规则缺 tag 注释（清理依据）:\n%s", joined)
	}
	// 幂等：重复建立不产生重复规则（先按 tag 清再加）。
	calls = nil
	if err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439); err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("幂等重放应走清+加序列")
	}
	// 清理：按 tag 注释删除全部规则。
	calls = nil
	if err := m.removeUdpHopDNAT("node_1"); err != nil {
		t.Fatal(err)
	}
	// portHop 空 = no-op。
	calls = nil
	if err := m.ensureUdpHopDNAT("node_1", "", 14439); err != nil || len(calls) != 0 {
		t.Fatal("空跳跃段应为 no-op")
	}
}

// TestEnsureUdpHopDNATPermissionError 验证无权限时的指向性错误（spec §5/全局约束）。
func TestEnsureUdpHopDNATPermissionError(t *testing.T) {
	runIPTables = func(bin string, args ...string) error { return errTestIPTables }
	defer func() { runIPTables = runIPTablesImpl }()
	m := &Manager{}
	err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439)
	if err == nil || !strings.Contains(err.Error(), "端口跳跃需要 iptables DNAT 权限") {
		t.Fatalf("应报指向性错误: %v", err)
	}
}
```

实现 `src/agent/internal/xray/udphop.go`（新文件，CRLF）：

```go
package xray

import (
	"fmt"
	"os/exec"
	"strings"

	"lattix/shared"
)

// hy2 端口跳跃的 DNAT 治理（spec §3.2 DNAT 路径）：xray hy2 服务端只监听单端口，
// udpHop 仅客户端实现——服务端用 iptables nat 把跳跃段流量 REDIRECT 到 hy2 监听端口。
// 规则以 --comment "lattix:<tag>" 标识，生命周期跟随对应 inbound（建立幂等=先清后加；
// 清理按注释匹配删除）。仅有 IPv4 治理（v1；IPv6 跳跃段后续版本补 ip6tables）。

// runIPTables 测试缝：单测捕获调用序列，不执行真实 iptables。
var runIPTables = runIPTablesImpl

func runIPTablesImpl(bin string, args ...string) error {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureUdpHopDNAT 建立 tag 对应 inbound 的跳跃段 DNAT（portHop 空 = no-op）。
// 幂等：先按 tag 清理既有规则再添加。
func (m *Manager) ensureUdpHopDNAT(tag string, portHop string, listenPort int) error {
	if portHop == "" {
		return nil
	}
	start, end, err := shared.ParsePortHop(portHop)
	if err != nil {
		return err
	}
	if err := m.removeUdpHopDNAT(tag); err != nil {
		return err
	}
	comment := "lattix:" + tag
	dport := fmt.Sprintf("%d:%d", start, end)
	toPorts := fmt.Sprintf("%d", listenPort)
	if err := runIPTables("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp",
		"--dport", dport, "-m", "comment", "--comment", comment,
		"-j", "REDIRECT", "--to-ports", toPorts); err != nil {
		return fmt.Errorf("端口跳跃需要 iptables DNAT 权限（或以 root 运行 agent / 关闭端口跳跃）: %w", err)
	}
	return nil
}

// removeUdpHopDNAT 按 tag 注释清理 DNAT 规则（逐条列出后删除；无规则 = 幂等成功）。
func (m *Manager) removeUdpHopDNAT(tag string) error {
	comment := "lattix:" + tag
	// -S 列出规则，逐条把 -A 替换为 -D 删除；iptables 缺失/无权限时同样按指向性错误上抛。
	out := listIPTablesRules("nat", "PREROUTING")
	for _, rule := range strings.Split(out, "\n") {
		if !strings.Contains(rule, comment) {
			continue
		}
		del := strings.Replace(rule, "-A PREROUTING", "-D PREROUTING", 1)
		args := strings.Fields(del)
		if err := runIPTables("iptables", append([]string{"-t", "nat"}, args...)...); err != nil {
			return fmt.Errorf("清理端口跳跃 DNAT 规则失败: %w", err)
		}
	}
	return nil
}

// listIPTablesRules 列出某表某链的规则（-S 输出）；经 runIPTables 同缝执行，
// 这里用捕获输出的变体（测试缝只盖写命令执行的封装层）。
var listIPTablesRules = func(table, chain string) string {
	out, err := exec.Command("iptables", "-t", table, "-S", chain).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
```

（测试里的 `errTestIPTables` 在 udphop_test.go 顶部定义：`var errTestIPTables = errors.New("iptables: Permission denied")`。listIPTablesRules 与 runIPTables 同缝改造：实现时把 remove 的列举也走 `runIPTablesCapture` 包级变量，保持单缝——最终代码以"单测试缝"为准重构。）

Run: `cd src/agent && go test ./internal/xray/ -run TestEnsureUdpHop -v`
Expected: PASS

- [ ] **Step 4: endpoint.go——放行 hysteria、layers、DNAT、hy2 outbound 分派（先写失败测试）**

`src/agent/internal/xray/endpoint_test.go` 追加：

```go
// TestApplySharedEndpointHysteria 验证 hy2 出口共享监听落地（P4）：放行 hysteria、
// udp 层端口探测、realized 携带 hy2 字段、无路由时零 outbound。
func TestApplySharedEndpointHysteria(t *testing.T) {
	m := testManager(t) // Version 桩需 ≥ 26.3.27（参照 Step 2 fixture 说明）
	p := shared.ApplySharedEndpointPayload{
		EndpointID: 5,
		Config: shared.VirtualConfig{
			Protocol: shared.ProtocolHysteria2, Security: shared.SecurityTLS,
			CertMode: shared.CertModeSelfSign, TLSDomain: "www.example.com",
			ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100,
			Template: json.RawMessage(`{"tag":"{{TAG}}","protocol":"hysteria","port":"{{PORT}}",
				"settings":{"version":2,"clients":"{{CLIENTS}}"},
				"streamSettings":{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"www.example.com",
				"alpn":["h3"],"certificates":[{"certificateFile":"{{TLS_CERT_FILE}}","keyFile":"{{TLS_KEY_FILE}}"}]},
				"hysteriaSettings":{"version":2}}`),
		},
		Clients: []shared.ClientCredential{{ID: "uuid-1", Email: "tunnel:uuid-1"}},
	}
	realized, err := m.ApplySharedEndpoint(p)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Port == 0 || realized.ObfsPassword != "obfs-pw" {
		t.Fatalf("realized 不符: %+v", realized)
	}
	// 配置含 hy2 inbound，clients 条目为 {auth,email,level:0}。
	cur, _ := m.loadConfig()
	found := false
	for _, raw := range cur.inbounds() {
		var ib struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		}
		if json.Unmarshal(raw, &ib) == nil && ib.Protocol == "hysteria" {
			found = true
		}
	}
	if !found {
		t.Fatal("受管配置缺 hy2 inbound")
	}
}

// TestRenderSharedEndpointOutboundHy2 验证入口终结模式的 hy2 outbound 渲染
// （route.ExitProtocol=hysteria：protocol/address/port + pin + auth + salamander + udpHop）。
func TestRenderSharedEndpointOutboundHy2(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 3, ExitProtocol: shared.ProtocolHysteria2,
		TargetAddress: "203.0.113.9", TargetPort: 21000, TunnelUUID: "svc-uuid",
		Target: shared.RealizedConfig{
			SNI: "www.example.com", CertSHA256: "deadbeef", ObfsPassword: "obfs-pw",
			UpMbps: 50, DownMbps: 100, PortHop: "20000-20031",
		},
	}
	ob := renderSharedEndpointOutbound(route, "shared_endpoint_route_5_3")
	if ob["protocol"] != "hysteria" {
		t.Fatalf("outbound 协议应为 hysteria: %v", ob["protocol"])
	}
	settings := ob["settings"].(map[string]any)
	if settings["version"] != 2 || settings["address"] != "203.0.113.9" || settings["port"] != 21000 {
		t.Fatalf("settings 不符: %v", settings)
	}
	ss := ob["streamSettings"].(map[string]any)
	if ss["network"] != "hysteria" {
		t.Fatal("hy2 outbound 必须显式 network=hysteria（Task 1 实测：缺省 tcp 退化为 TCP 承载）")
	}
	tlsS := ss["tlsSettings"].(map[string]any)
	if tlsS["serverName"] != "www.example.com" || tlsS["pinnedPeerCertSha256"] != "deadbeef" {
		t.Fatalf("tlsSettings 不符: %v", tlsS)
	}
	if ss["hysteriaSettings"].(map[string]any)["auth"] != shared.Hy2UserPassword("svc-uuid") {
		t.Fatalf("auth 应为派生口令: %v", ss["hysteriaSettings"])
	}
	fm := ss["finalmask"].(map[string]any)
	quic := fm["quicParams"].(map[string]any)
	hop := quic["udpHop"].(map[string]any)
	if hop["ports"] != "20000-20031" || hop["interval"] != shared.Hy2PortHopInterval {
		t.Fatalf("udpHop 不符: %v", hop)
	}
}
```

Run: `cd src/agent && go test ./internal/xray/ -run 'TestApplySharedEndpointHysteria|TestRenderSharedEndpointOutboundHy2' -v`
Expected: FAIL（gate 拒绝 hysteria / 分派不存在）

实现 `src/agent/internal/xray/endpoint.go`：
1. `ApplySharedEndpoint` gate（:31-33）替换为：
```go
	if p.Config.Protocol != shared.ProtocolVLESS && p.Config.Protocol != shared.ProtocolHysteria2 {
		return nil, fmt.Errorf("共享端点仅支持 VLESS/hysteria2")
	}
	// hy2 出口共享监听同版本门控（ApplyNode 同款）。
	if p.Config.Protocol == shared.ProtocolHysteria2 {
		version, _ := m.Version()
		if !xrayVersionAtLeast(version, shared.XrayMinVersionHy2) {
			return nil, fmt.Errorf("节点 xray 版本过低（hysteria2 需要 xray ≥ %s，当前 %s），请先在节点页升级 xray",
				shared.XrayMinVersionHy2, version)
		}
	}
```
2. `pickChainPort` 调用（:48）layers 实参 `"tcp"` 改为 `shared.PortLayers(config.Protocol)`（hy2 → udp 探测）。
3. `commitConfig` 成功后（:103-105 区域，`m.restartApply()` 之前）插入 DNAT 接线：
```go
	// hy2 端口跳跃：段 → 监听端口的 DNAT（spec §3.2 DNAT 路径；空段 no-op）。
	if config.Protocol == shared.ProtocolHysteria2 {
		if err := m.ensureUdpHopDNAT(shared.SharedEndpointTag(p.EndpointID), config.PortHop, realized.Port); err != nil {
			return nil, err
		}
	}
```
4. `RemoveSharedEndpoint`（:113-115）改为清理 DNAT 后再删 piece：
```go
func (m *Manager) RemoveSharedEndpoint(endpointID int64) error {
	if err := m.removeUdpHopDNAT(shared.SharedEndpointTag(endpointID)); err != nil {
		return err
	}
	return m.RemoveChainHop(endpointID, sharedEndpointPieceKind)
}
```
5. `renderSharedEndpointOutbound`（:128-192）函数头改为分派：
```go
func renderSharedEndpointOutbound(route shared.SharedEndpointRoute, tag string) map[string]any {
	// 入口终结模式（P4 §3.2）：出口为 hy2 时末段以 hy2 outbound 直拨出口（UDP）。
	if route.ExitProtocol == shared.ProtocolHysteria2 {
		return renderHy2Outbound(tag, shared.Hy2DialSpec{
			Address: route.TargetAddress, Port: route.TargetPort,
			Auth:         shared.Hy2UserPassword(route.TunnelUUID),
			SNI:          route.Target.SNI,
			CertSHA256:   route.Target.CertSHA256,
			ObfsPassword: route.Target.ObfsPassword,
			UpMbps:       route.Target.UpMbps,
			DownMbps:     route.Target.DownMbps,
			PortHop:      route.Target.PortHop,
		})
	}
	user := map[string]any{"id": route.TunnelUUID, "encryption": "none"}
	// …（以下原 vless 渲染体 :129-191 原样保留）
```
6. 文件尾追加：
```go
// renderHy2Outbound 渲染 hy2 直拨 outbound（Task 1 实测定稿形态）：
// network 恒 "hysteria"、tlsSettings 带 alpn [h3]（两者缺失分别退化为 TCP 承载/
// 握手失败）；客户端口令在 transport hysteriaSettings.auth；salamander/brutal/udpHop
// 在 finalmask；自签证书以 pinnedPeerCertSha256 钉住（hex），ACME 走系统根验证。
// 入口共享端点路由与链末段 forward（chain.go）共用。
func renderHy2Outbound(tag string, dial shared.Hy2DialSpec) map[string]any {
	tlsSettings := map[string]any{
		"serverName":  dial.SNI,
		"alpn":        []string{"h3"},
		"fingerprint": shared.FingerprintChrome,
	}
	if dial.CertSHA256 != "" {
		tlsSettings["pinnedPeerCertSha256"] = dial.CertSHA256
	}
	fm := map[string]any{}
	if dial.ObfsPassword != "" {
		fm["udp"] = []map[string]any{{
			"type":     "salamander",
			"settings": map[string]any{"password": dial.ObfsPassword},
		}}
	}
	quic := map[string]any{}
	if dial.UpMbps > 0 || dial.DownMbps > 0 {
		quic["congestion"] = "brutal"
		if dial.UpMbps > 0 {
			quic["brutalUp"] = fmt.Sprintf("%d mbps", dial.UpMbps)
		}
		if dial.DownMbps > 0 {
			quic["brutalDown"] = fmt.Sprintf("%d mbps", dial.DownMbps)
		}
	}
	port := dial.Port
	if dial.PortHop != "" {
		start, _, err := shared.ParsePortHop(dial.PortHop)
		if err == nil {
			port = start // 段起点为基址，客户端 udpHop 在段内换端口（出口 DNAT 收敛）
			quic["udpHop"] = map[string]any{"ports": dial.PortHop, "interval": shared.Hy2PortHopInterval}
		}
	}
	if len(quic) > 0 {
		fm["quicParams"] = quic
	}
	stream := map[string]any{
		"network":          "hysteria",
		"security":         "tls",
		"tlsSettings":      tlsSettings,
		"hysteriaSettings": map[string]any{"version": 2, "auth": dial.Auth},
	}
	if len(fm) > 0 {
		stream["finalmask"] = fm
	}
	return map[string]any{
		"tag": tag, "protocol": "hysteria",
		"settings":       map[string]any{"version": 2, "address": dial.Address, "port": port},
		"streamSettings": stream,
	}
}
```

Run: `cd src/agent && go test ./internal/xray/ -v && go build ./...`
Expected: PASS

- [ ] **Step 5: chain.go forward——Hy2Target 末段与 HopPortEnd 逐端口段（先写失败测试）**

`src/agent/internal/state/state.go` `ChainPiece`（:94-106）`Inbound` 行后追加：
```go
	Inbounds   []json.RawMessage `json:"inbounds,omitempty"`   // forward：hy2 端到端跳跃段逐端口附加 inbound（P4）
```

`src/agent/internal/xray/chain_test.go` 追加：

```go
// TestRenderForwardHy2Target 验证入口终结末段 piece：dokodemo-udp inbound + hy2 outbound
// （routing 指向 hy2 outbound 而非 freedom）。
func TestRenderForwardHy2Target(t *testing.T) {
	p := shared.ApplyChainHopPayload{
		ChainID: 1, HopID: 7, Kind: shared.HopKindForward,
		Forward: &shared.ForwardSpec{
			Tag: shared.ChainForwardTag(7), Port: 22000, Network: "udp",
			TargetAddress: "203.0.113.9", TargetPort: 21000,
			Hy2Target: &shared.Hy2DialSpec{
				Address: "203.0.113.9", Port: 21000, Auth: "auth-x",
				SNI: "www.example.com", CertSHA256: "deadbeef",
			},
		},
	}
	m := testManager(t)
	cur, _ := m.loadConfig()
	_, rec, err := m.renderForward(p, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Outbounds) != 1 {
		t.Fatalf("应含 1 条 hy2 outbound: %+v", rec)
	}
	var ob map[string]any
	if err := json.Unmarshal(rec.Outbounds[0], &ob); err != nil {
		t.Fatal(err)
	}
	if ob["protocol"] != "hysteria" {
		t.Fatalf("outbound 应为 hysteria: %v", ob["protocol"])
	}
	// 路由规则指向 hy2 outbound tag（= forward tag）。
	var rule struct {
		OutboundTag string `json:"outboundTag"`
	}
	if err := json.Unmarshal(rec.Rules[0], &rule); err != nil || rule.OutboundTag != shared.ChainForwardTag(7) {
		t.Fatalf("路由应指向 hy2 outbound: %+v", rec.Rules[0])
	}
}

// TestRenderForwardHopPorts 验证端到端跳跃段：主 inbound 外逐端口附加 dokodemo inbound
// （[Port+1, HopPortEnd]，目标下一跳同号端口，udp-only）。
func TestRenderForwardHopPorts(t *testing.T) {
	p := shared.ApplyChainHopPayload{
		ChainID: 1, HopID: 8, Kind: shared.HopKindForward,
		Forward: &shared.ForwardSpec{
			Tag: shared.ChainForwardTag(8), Port: 30000, HopPortEnd: 30002,
			Network: "udp", TargetAddress: "198.51.100.2", TargetPort: 30000,
		},
	}
	m := testManager(t)
	cur, _ := m.loadConfig()
	_, rec, err := m.renderForward(p, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Inbounds) != 2 { // 30001, 30002（主 inbound = 30000）
		t.Fatalf("附加 inbound 数不符: %+v", rec.Inbounds)
	}
	for i, raw := range rec.Inbounds {
		var ib struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Settings struct {
				Port    int    `json:"port"`
				Network string `json:"network"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(raw, &ib); err != nil {
			t.Fatal(err)
		}
		want := 30001 + i
		if ib.Port != want || ib.Settings.Port != want || ib.Settings.Network != "udp" {
			t.Fatalf("附加 inbound 应 1:1 同号转发: %+v", ib)
		}
	}
}
```

Run: `cd src/agent && go test ./internal/xray/ -run 'TestRenderForwardHy2Target|TestRenderForwardHopPorts' -v`
Expected: FAIL（字段/分支不存在）

实现 `src/agent/internal/xray/chain.go`：
1. `renderForward`（:344-380）`outboundTag` 计算块（:355-361）替换为：
```go
	outboundTag := directOutboundTag
	if spec.Hy2Target != nil {
		outboundTag = tag // 入口终结末段：路由指向本 piece 的 hy2 outbound
	} else if spec.ViaTunnelDomain != "" {
		outboundTag = reversePortalTagByDomain(cur, spec.ViaTunnelDomain)
		if outboundTag == "" {
			return nil, state.ChainPiece{}, fmt.Errorf("via_tunnel_domain %q 对应的上游 portal 尚未就绪", spec.ViaTunnelDomain)
		}
	}
```
`rec` 组装（:374-378）替换为：
```go
	rec := state.ChainPiece{
		HopID: p.HopID, Kind: p.Kind, Port: port,
		Inbound: inboundRaw,
		Rules:   []json.RawMessage{ruleRaw},
	}
	if spec.Hy2Target != nil {
		outbound, err := json.Marshal(renderHy2Outbound(tag, *spec.Hy2Target))
		if err != nil {
			return nil, state.ChainPiece{}, err
		}
		rec.Outbounds = append(rec.Outbounds, outbound)
	}
	// hy2 端到端跳跃段（spec §3.2）：段内其余端口逐端口 dokodemo UDP 1:1 转发
	// （主 inbound 覆盖段起点；目标 = 下一跳同号端口）。
	for hp := port + 1; hp <= spec.HopPortEnd && spec.HopPortEnd > 0; hp++ {
		extra, err := json.Marshal(renderForwardInbound(&shared.ForwardSpec{
			TargetAddress: spec.TargetAddress, TargetPort: spec.TargetPort - port + hp,
			Network: "udp", LocalOnly: spec.LocalOnly, ListenFamily: spec.ListenFamily,
		}, fmt.Sprintf("%s_hop_%d", tag, hp), hp))
		if err != nil {
			return nil, state.ChainPiece{}, err
		}
		rec.Inbounds = append(rec.Inbounds, extra)
	}
	return &shared.RealizedConfig{Port: port}, rec, nil
```
（注意 1:1 语义：下一跳同号端口 = `spec.TargetPort - port + hp`——直连段 TargetPort 是下一跳段起点，偏移一致；`spec.TargetPort` 在末段为出口 realized 监听端口时，段内端口经出口 DNAT 收敛，逐端口目标仍写同号段端口，由 dispatch 保证 TargetPort 传段起点——见 Task 6 Step 2。）
2. `applyChainPiece`（:685-689）`rec.Inbound` 块后追加：
```go
	for _, raw := range rec.Inbounds {
		nc = nc.upsertInbound(inboundTag(raw), raw)
	}
```
3. `chainPieceTags`/removeChainPieceItems（:580-601 区域）：piece 的 inbound 清理目前按 tag 前缀/枚举——确认实现后让 `<tag>_hop_<port>` 附加 inbound 一并清除（实现要点：removeChainPieceItems 若按 piece 记录的 Inbound 单条删除，则扩展为同时遍历 `rec.Inbounds`；若按 tag 前缀匹配，则 `_hop_` 前缀天然覆盖）。**执行时先读现状代码再定删除点，并把结论写进任务完成说明。**

Run: `cd src/agent && go test ./... -v && go vet ./...`
Expected: PASS（含新用例；既有 forward/endpoint/cleanup 用例不回归）

提交：`git add -A src/agent && git commit -m "feat(agent): P4 hy2 填充/版本门控/端口跳跃 DNAT 与 hy2 outbound 渲染"`

---
### Task 6: dispatch——hy2 出口共享编排、末段 hy2 路由、逐跳端口段下发、用户扇出改道

**Files:**
- Modify: `src/backend/internal/dispatch/chain.go`（阶段 1 :163-221 出口共享分支、阶段 4 :315-370 Hy2Target/HopPortEnd、reconcilePublishedEndpoints :450-465、forcedServiceRealized :586-620）
- Modify: `src/backend/internal/dispatch/endpoint.go`（ReconcileSharedEndpoint :16-91 按端点协议分派 + route ExitProtocol）
- Modify: `src/backend/internal/dispatch/revision_plan.go`（RevisionTopology 加 ServiceEndpointID；materializeRevision 2 跳 hy2 免管道）
- Modify: `src/backend/internal/store/endpoints.go`（追加 ChainsByServiceEndpoint / ActiveServiceEndpointUsers 查询）
- Modify: `src/backend/internal/panel/users.go`（fanoutUserDiff :862-892 与 reconcileAssignmentEndpoints :852-858 的 hy2 改道）
- Modify: `src/backend/internal/panel/chains.go`（删除链 :1153-1157 追加出口共享 reconcile）
- Test: `src/backend/internal/dispatch/endpoint_test.go`（追加）、`src/backend/internal/dispatch/chain_test.go`（追加）、`src/backend/internal/store/endpoints_test.go`（追加）、`src/backend/internal/panel/users_test.go`（追加）

**Interfaces:**
- Consumes: `store.EnsureProtocolSharedEndpoint` 产物（Task 4 落库的 `chains.service_endpoint_id`）；`shared.Hy2DialSpec/Hy2UserPassword/ParsePortHop`（Task 2）；`SetNodeActive`（store/nodes.go:121，realized 镜像写库）；`renderHy2Outbound` 消费的消息字段（Task 5）。
- Produces:
  - `func (s *Store) ChainsByServiceEndpoint(ctx context.Context, endpointID int64) ([]Chain, error)`——引用某出口共享监听的未删链。
  - `func (s *Store) ActiveServiceEndpointUsers(ctx context.Context, endpointID int64) ([]string, error)`——端到端 hy2 链（`service_endpoint_id=? AND endpoint_id=0`）的业务用户 UUID 全集（直接分配 + 分组派生，镜像 ActiveEndpointAssignments 语义但返回 UUID）。
  - `ReconcileSharedEndpoint` 行为扩展：`endpoint.Protocol == "hysteria"` → 出口侧模式（routes=nil；clients = Σ 引用链的 tunnel 身份（有入口端点的链）∪ 业务用户 UUID（端到端链））；vless 入口端点行为不变，仅 route 组装加 ExitProtocol/出口地址分派。
  - `RevisionTopology.ServiceEndpointID int64`（纯携带，不进 piece 哈希）。

**编排语义**（advanceChain 对 hy2 出口共享链的变化）：阶段 1 出口节点不再走 apply_node——出口监听由共享端点承载：dispatcher 对 `service_endpoint_id != 0` 的链先 `ReconcileSharedEndpoint(service_endpoint_id)`，等端点 active 后把端点 realized 镜像到出口节点（`SetNodeActive`），后续阶段（portal/bridge/forward/publish）复用现状（`node.RealizedConfig` 即有值）。`xray run -test`/重启/回滚仍在 agent 侧端点 apply 内完成，端点状态机（efsm）语义不变。

- [ ] **Step 1: store 查询（先写失败测试）**

`src/backend/internal/store/endpoints_test.go` 追加：

```go
// TestChainsByServiceEndpoint 与 TestActiveServiceEndpointUsers 验证出口共享监听的两个
// 数据源（P4）：引用链枚举 + 端到端链业务用户全集（分组派生并入，排除 expired/disabled）。
func TestChainsByServiceEndpoint(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	// fixture：端点 E（hysteria）、链1（service_endpoint_id=E, endpoint_id=0）、
	// 链2（service_endpoint_id=E, endpoint_id=E2 入口终结）、链3（已删除，同引用）。
	chains, err := st.ChainsByServiceEndpoint(ctx, endpointE)
	if err != nil || len(chains) != 2 {
		t.Fatalf("应返回两条未删引用链: %v %d", err, len(chains))
	}
}
func TestActiveServiceEndpointUsers(t *testing.T) {
	// fixture：端到端 hy2 链上直接分配用户 u1（有效）、u2（disabled）、
	// 分组成员 u3（经 link_group_chains 派生）。
	uuids, err := st.ActiveServiceEndpointUsers(ctx, endpointE)
	if err != nil {
		t.Fatal(err)
	}
	// 期望恰好含 u1 与 u3（u2 排除），无重复。
}
```

Run: `cd src/backend && go test ./internal/store/ -run 'TestChainsByServiceEndpoint|TestActiveServiceEndpointUsers' -v`
Expected: FAIL（方法不存在）

实现 `src/backend/internal/store/endpoints.go` 文件尾追加：

```go
// ChainsByServiceEndpoint 返回引用指定出口共享监听（service_endpoint_id）的未删链（P4）。
func (s *Store) ChainsByServiceEndpoint(ctx context.Context, endpointID int64) ([]Chain, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+chainCols+` FROM chains WHERE service_endpoint_id=? AND deleted_at IS NULL ORDER BY id`, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chain
	for rows.Next() {
		c, err := scanChain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ActiveServiceEndpointUsers 返回端到端 hy2 链（引用该出口共享监听且无入口端点）的
// 业务用户 UUID 全集：直接分配 + 分组派生（镜像 ActiveEndpointAssignments 的生效规则：
// 排除 expired/disabled，分组成员的直接分配被分组派生遮蔽），供出口监听 users 填充。
func (s *Store) ActiveServiceEndpointUsers(ctx context.Context, endpointID int64) ([]string, error) {
	set := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT u.uuid FROM user_chain_assignments a
		JOIN chains c ON c.id=a.chain_id
		JOIN users u ON u.id=a.user_id
		WHERE c.service_endpoint_id=? AND c.endpoint_id=0 AND c.deleted_at IS NULL
		AND u.expired=0 AND u.disabled=0
		AND NOT EXISTS (SELECT 1 FROM user_group_members g WHERE g.user_id = a.user_id)`, endpointID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			rows.Close()
			return nil, err
		}
		set[uuid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	groupRows, err := s.db.QueryContext(ctx, `SELECT DISTINCT u.uuid
		FROM user_group_members ugm
		JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
		JOIN link_group_chains lgc ON lgc.group_id = ugl.link_group_id
		JOIN chains c ON c.id = lgc.chain_id
		JOIN users u ON u.id = ugm.user_id
		WHERE c.service_endpoint_id=? AND c.endpoint_id=0 AND c.deleted_at IS NULL
		AND u.expired=0 AND u.disabled=0`, endpointID)
	if err != nil {
		return nil, err
	}
	for groupRows.Next() {
		var uuid string
		if err := groupRows.Scan(&uuid); err != nil {
			groupRows.Close()
			return nil, err
		}
		set[uuid] = true
	}
	groupRows.Close()
	if err := groupRows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for uuid := range set {
		out = append(out, uuid)
	}
	sort.Strings(out)
	return out, nil
}
```

Run: `cd src/backend && go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 2: advanceChain 阶段 1 出口共享分支 + 阶段 4 hy2 参数下发（先写失败测试）**

`src/backend/internal/dispatch/chain_test.go` 追加：

```go
// TestAdvanceChainHy2SharedExit 验证 hy2 出口共享链的阶段 1：不发 apply_node，
// 先 reconcile 出口共享端点，端点 active 后出口节点镜像 realized 并推进后续阶段。
func TestAdvanceChainHy2SharedExit(t *testing.T) {
	// fixture：A/C 两 agent 在线；链 = A(入口)→C(出口)，出口节点 protocol=hysteria，
	// chains.service_endpoint_id=E（shared_endpoints 行 protocol=hysteria, status=pending）。
	// 触发 advanceChain → 断言：C 收到 apply_shared_endpoint（而非 apply_node）；
	// 回执端点 active 后再次 advance → 节点 realized_config = 端点 realized 镜像、
	// 节点 status=active，且阶段 4 下发的 forward spec Network="udp"。
}

// TestAdvanceChainHy2LastMile 验证入口终结末段（transport="hy2"）：
// 末段 forward spec 携带 Hy2Target（地址=出口公网、端口=段起点/realized、auth=派生、
// SNI/pin/obfs/带宽/段透传）；2 跳链（入口即末跳）不产 forward piece（端点直拨）。
func TestAdvanceChainHy2LastMile(t *testing.T) {
	// fixture：3 跳链 A→B→C，入口区块勾选，出口 hy2 realized 就绪。
	// 断言 B 的 forward spec.Hy2Target != nil 且字段齐全；A 的 forward spec.Hy2Target == nil。
	// 另起 2 跳链 A→C（入口区块）→ 断言 A 无 forward piece（revision apply_keys 无 forward/<hopA>）。
}

// TestAdvanceChainHy2HopPorts 验证端到端跳跃段下发：exit port_hop="30000-30031" 时
// 各跳 forward spec.Port=段起点（forward_port）、HopPortEnd=30031、Network="udp"。
func TestAdvanceChainHy2HopPorts(t *testing.T) {
	// fixture：2 跳链 A→C 端到端 hy2 + port_hop。
	// 断言 A 的 forward spec.HopPortEnd == 30031。
}
```

Run: `cd src/backend && go test ./internal/dispatch/ -run 'TestAdvanceChainHy2' -v`
Expected: FAIL（分支不存在）

实现 `src/backend/internal/dispatch/chain.go`：

1. 阶段 1（:163-221）：`node, err := d.st.NodeByID(...)` 之后、`if endpointID != 0 && len(hops) == 1` 短路之前插入出口共享分支（`endpointID` 变量在函数前部已解析——hy2 出口共享与入口端点正交，须同时取 `revision.Snapshot.ServiceEndpointID`）：
```go
	// hy2 出口共享监听（P4）：出口监听由共享端点承载，不走 apply_node。
	// 端点未 active → 触发 reconcile 并等待；active → 镜像 realized 到出口节点（幂等）。
	if serviceEndpointID != 0 && node.Status != store.NodeStatusActive {
		svcEndpoint, err := d.st.SharedEndpointByID(ctx, serviceEndpointID)
		if err != nil {
			log.Printf("dispatch: chain %d service endpoint %d: %v", chainID, serviceEndpointID, err)
			return
		}
		if svcEndpoint.Status != store.EndpointStatusActive {
			if svcEndpoint.Status != store.EndpointStatusApplying {
				if err := d.ReconcileSharedEndpoint(ctx, serviceEndpointID); err != nil {
					log.Printf("dispatch: chain %d reconcile service endpoint: %v", chainID, err)
				}
			}
			return // 等端点 apply 回执（端点回执处理器触发后续 advance）
		}
		var endpointRealized json.RawMessage = svcEndpoint.RealizedConfig
		if err := d.st.SetNodeActive(ctx, node.ID, endpointRealized); err != nil {
			log.Printf("dispatch: chain %d mirror service endpoint realized: %v", chainID, err)
		}
		return // realized 落库后由节点状态变化路径再次推进
	}
```
（`serviceEndpointID` 来源：advanceChain 前部已加载 revision 快照处加 `serviceEndpointID := revision.Snapshot.ServiceEndpointID`。端点 apply 回执处理器——与节点回执同处（dispatcher.go:1195 区域 SetNodeActive 的调用点）——确认其在 `apply_shared_endpoint` 回执后触发受影响链的 advance；若现状只 reconcile 订阅，则在回执处理器补 `d.advanceChain` 对 `ChainsByServiceEndpoint` 的触发。**执行时核实并记录。**）
2. 阶段 4（:315-370）：`spec := &shared.ForwardSpec{...}` 组装块（:323-338）之后、目标计算块（:339-361）之中插入两个 hy2 分支：
```go
		// hy2 端到端跳跃段（P4 §3.2）：出口 port_hop 非空时，各跳保留段
		// [forward_port, forward_port+段长-1]（全跳同段，逐端口 1:1；段终点随 spec 下发）。
		if hopPortEnd > 0 && hop.Transport != "hy2" {
			spec.HopPortEnd = hop.ForwardPort + hopSpanLen - 1
		}
		// 入口终结末段（transport="hy2"）：本跳 xray 以 hy2 outbound 直拨出口（UDP），
		// 不再生成 freedom 直连；拨号参数 = 出口公网地址 + realized + 派生 tunnel 口令。
		if hop.Transport == "hy2" {
			dial := &shared.Hy2DialSpec{
				Address:      store.ResolveServerAddress(servers[exit.ServerID], exit.Address),
				Port:         publicPortOf(servers[exit.ServerID], rc.Port),
				Auth:         shared.Hy2UserPassword(revision.Snapshot.ServiceUUID),
				SNI:          rc.SNI,
				CertSHA256:   rc.CertSHA256,
				ObfsPassword: rc.ObfsPassword,
				UpMbps:       rc.UpMbps,
				DownMbps:     rc.DownMbps,
				PortHop:      rc.PortHop,
			}
			if rc.PortHop != "" {
				if start, _, err := shared.ParsePortHop(rc.PortHop); err == nil {
					dial.Port = publicPortOf(servers[exit.ServerID], start) // 段起点为基址
				}
			}
			spec.Hy2Target = dial
			spec.Network = "udp" // 本跳 dokodemo 只接 UDP（入口侧流量已是 UDP 隧道包）
		}
```
（`hopPortEnd/hopSpanLen` 在阶段 4 循环前由快照 ServiceConfig 的 port_hop 解析：空=0/0；`exit`/`rc`/`revision` 变量均已在函数内。直连段目标计算（:347-350）对 transport="hy2" 的下一跳不再使用——dokodemo inbound 仍需 TargetAddress/TargetPort 字段（xray 必填），保留现状计算结果即可（不被路由使用）；2 跳免管道在 revision_plan 层处理，见 Step 3。）
3. `reconcilePublishedEndpoints`（:450-465）：两个 `ReconcileSharedEndpoint(...EndpointID)` 调用点后按同款模式追加 `ServiceEndpointID` 的 reconcile（previous 与 revision 各一，0 跳过、去重）。
4. `forcedServiceRealized`（:586-620）：`realized.Method = virtual.Method` 行后追加：
```go
	realized.ObfsPassword = virtual.ObfsPassword
	realized.UpMbps = virtual.UpMbps
	realized.DownMbps = virtual.DownMbps
	realized.PortHop = virtual.PortHop
```
并在 ss PSK 校验（:612-614）后追加：
```go
	if virtual.Protocol == shared.ProtocolHysteria2 && realized.SNI == "" {
		return nil, fmt.Errorf("强制发布失败：hysteria2 尚无 Agent 上报的 TLS 参数，请等待出口 Agent 在线")
	}
```

Run: `cd src/backend && go test ./internal/dispatch/ -v`
Expected: PASS（含新用例；既有编排用例不回归——service_endpoint_id=0 时阶段 1 分支不进）

- [ ] **Step 3: revision_plan 2 跳免管道 + RevisionTopology.ServiceEndpointID**

`src/backend/internal/dispatch/revision_plan.go`：
1. `RevisionTopology` struct 加 `ServiceEndpointID int64`（纯携带字段，不进任何哈希——service 哈希已含 ServiceConfig）。
2. `materializeRevision`（:135-157 区域）forward piece 生成循环前加 2 跳豁免：
```go
	// 入口终结 2 跳 hy2（P4 §3.2）：入口即末跳，共享端点直接以 hy2 outbound 拨出口，
	// hop0 不再生成 forward 管道（3 跳及以上保留 hop0 回环管道接中段）。
	skipEntryForward := len(topology.Hops) == 2 && topology.Hops[0].Transport == "hy2"
```
forward piece 循环内对 `i == 0 && skipEntryForward` 跳过（其余 piece 生成逻辑不变）。
3. `chains.go revisionTopology`（panel，:857-880）返回值已含 ServiceEndpointID（Task 4 Step 4-9）。

`src/backend/internal/dispatch/revision_plan_test.go` 追加：

```go
// TestMaterializeHy2TwoHopNoEntryForward 验证 2 跳入口终结链 hop0 免管道（P4）。
func TestMaterializeHy2TwoHopNoEntryForward(t *testing.T) {
	pieces, err := materializeRevision(RevisionTopology{RevisionID: 1, ServiceID: 9,
		Hops: []RevisionHopSpec{
			{HopID: 1, ServerID: 1, Transport: "hy2"},
			{HopID: 2, ServerID: 2},
		}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pieces {
		if p.Kind == RevisionPieceForward && p.HopID == 1 {
			t.Fatalf("2 跳 hy2 链 hop0 不应产 forward piece: %+v", pieces)
		}
	}
	// 3 跳链 hop0 仍有 forward piece（回环管道接中段）。
}
```

Run: `cd src/backend && go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 4: ReconcileSharedEndpoint 按端点协议分派 + route ExitProtocol（先写失败测试）**

`src/backend/internal/dispatch/endpoint_test.go` 追加：

```go
// TestReconcileHy2ServiceEndpoint 验证出口侧 hy2 端点 reconcile：routes 为空；
// clients = 入口终结链的 tunnel 身份 ∪ 端到端链的业务用户 UUID（spec §3.2 出口共享）。
func TestReconcileHy2ServiceEndpoint(t *testing.T) {
	// fixture：hy2 端点 E；链1（service_endpoint_id=E，endpoint_id=0 端到端，
	//   业务用户 u1 直接分配）；链2（service_endpoint_id=E，endpoint_id=E2，
	//   service_uuid=svc2）。
	// ReconcileSharedEndpoint(E) → 断言下发 payload：Routes 为空；
	// Clients 含 {ID: u1, Email: u1} 与 {ID: svc2, Email: "tunnel:svc2"}。
}

// TestReconcileEntryEndpointHy2Route 验证入口端点 route 的出口协议分派：
// 2 跳入口终结 hy2 链 → route.ExitProtocol=hysteria、TargetAddress=出口公网地址、
// TargetPort=出口 realized 公网端口、Target=出口 realized（spec §3.2 endpoint.go 改造点）；
// vless 链 route 保持 127.0.0.1 回环（回归）。
func TestReconcileEntryEndpointHy2Route(t *testing.T) {
	// fixture：入口端点 E2（vless）；2 跳链 A→C，入口区块，出口 hy2 realized。
	// ReconcileSharedEndpoint(E2) → route 断言如上；另建 vless 存量链回归断言。
}
```

Run: `cd src/backend && go test ./internal/dispatch/ -run 'TestReconcileHy2|TestReconcileEntryEndpointHy2' -v`
Expected: FAIL（分派不存在）

实现 `src/backend/internal/dispatch/endpoint.go`：

1. `ReconcileSharedEndpoint`（:16-91）endpoint 加载（:17-24）之后插入出口侧早分支：
```go
	// hy2 出口共享监听（P4 §3.2）：无路由（出口即终点，freedom 出站）；
	// clients = 入口终结链的 tunnel 身份 ∪ 端到端链的业务用户 UUID。
	if endpoint.Protocol == shared.ProtocolHysteria2 {
		chains, err := d.st.ChainsByServiceEndpoint(ctx, endpointID)
		if err != nil {
			return err
		}
		payload := shared.ApplySharedEndpointPayload{EndpointID: endpointID, Config: config}
		server, err := d.st.ServerByID(ctx, endpoint.ServerID)
		if err != nil {
			return err
		}
		if server.MachineType == "nat" {
			payload.PortCandidates = listenCandidatesOf(server)
		}
		for _, chain := range chains {
			if chain.EndpointID != 0 && chain.ServiceUUID != "" {
				payload.Clients = append(payload.Clients, shared.ClientCredential{
					ID: chain.ServiceUUID, Email: "tunnel:" + chain.ServiceUUID})
			}
		}
		uuids, err := d.st.ActiveServiceEndpointUsers(ctx, endpointID)
		if err != nil {
			return err
		}
		for _, uuid := range uuids {
			payload.Clients = append(payload.Clients, shared.ClientCredential{ID: uuid, Email: uuid})
		}
		sort.Slice(payload.Clients, func(i, j int) bool { return payload.Clients[i].Email < payload.Clients[j].Email })
		if err := d.efsm.Transition(ctx, endpointID, store.EndpointStatusApplying, "下发端点部署命令", nil); err != nil {
			return err
		}
		_, err = d.Enqueue(ctx, endpoint.ServerID, shared.TypeApplySharedEndpoint, payload)
		return err
	}
```
2. route 组装（:68-78，`if !route.Direct {` 块）替换为按出口协议分派：
```go
		if !route.Direct {
			entry := revision.Snapshot.Hops[0]
			if err := json.Unmarshal(revision.Snapshot.ServiceRealized, &route.Target); err != nil {
				continue
			}
			var svc struct {
				Protocol string `json:"protocol"`
			}
			_ = json.Unmarshal(revision.Snapshot.ServiceConfig, &svc)
			// 入口终结 2 跳 hy2（P4 §3.2）：端点直接以 hy2 outbound 拨出口
			// （出口公网地址 + realized 端口），不再绕回环管道；其余维持回环现状。
			if svc.Protocol == shared.ProtocolHysteria2 && len(revision.Snapshot.Hops) == 2 &&
				revision.Snapshot.Hops[0].Transport == "hy2" {
				exitHop := revision.Snapshot.Hops[1]
				exitSrv, err := d.st.ServerByID(ctx, exitHop.ServerID)
				if err != nil || route.Target.Port == 0 {
					continue
				}
				route.ExitProtocol = shared.ProtocolHysteria2
				route.TargetAddress = store.ResolveServerAddress(exitSrv, exitHop.Address)
				route.TargetPort = publicPortOf(exitSrv, route.Target.Port)
			} else {
				if entry.ForwardPort == 0 || len(revision.Snapshot.ServiceRealized) == 0 {
					continue
				}
				route.TargetAddress = "127.0.0.1"
				route.TargetPort = entry.ForwardPort
			}
		}
```
（原块 :70-77 的 `len(revision.Snapshot.ServiceRealized) == 0` 前置校验与 `json.Unmarshal(...,&route.Target)` 已并入新结构；`route.Target.PortHop` 非空时 agent 侧自行换算段起点（Task 5 renderHy2Outbound），这里 TargetPort 传 realized 监听端口原值即可。）

Run: `cd src/backend && go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 5: 用户扇出改道与删链 reconcile**

`src/backend/internal/panel/users.go`：
1. `reconcileAssignmentEndpoints`（:852-858）扩展为同时收集出口共享监听：
```go
func (s *Server) reconcileAssignmentEndpoints(ctx context.Context, groups ...[]store.UserChainAssignment) {
	endpointIDs := map[int64]bool{}
	for _, id := range s.st.SharedEndpointIDsForAssignments(groups...) {
		endpointIDs[id] = true
	}
	// 端到端 hy2 链（P4）：业务用户在出口共享监听上，分配变更须 reconcile 出口端点。
	for _, group := range groups {
		for _, a := range group {
			if chain, err := s.st.ChainByID(ctx, a.ChainID); err == nil &&
				chain.ServiceEndpointID != 0 && chain.EndpointID == 0 {
				endpointIDs[chain.ServiceEndpointID] = true
			}
		}
	}
	for endpointID := range endpointIDs {
		if err := s.disp.ReconcileSharedEndpoint(ctx, endpointID); err != nil {
			log.Printf("panel: reconcile shared endpoint %d: %v", endpointID, err)
		}
	}
}
```
2. `fanoutUserDiff`（:862-892）：hy2 出口共享链的出口节点不再持有自身 inbound，须从 add/remove_user 扇出中排除（其用户由第 1 点的出口端点 reconcile 承载）——调用 `nodeParamsByServer` 前过滤：
```go
	// hy2 出口共享链（P4）：出口监听由共享端点承载，add/remove_user 不扇出到这些节点。
	exclude := map[int64]bool{}
	for _, n := range nodes {
		if chain, err := s.st.ChainByServiceNode(ctx, n.ID); err == nil && chain != nil &&
			chain.ServiceEndpointID != 0 {
			exclude[n.ID] = true
		}
	}
	filter := func(in []store.Node) []store.Node {
		out := in[:0]
		for _, n := range in {
			if !exclude[n.ID] {
				out = append(out, n)
			}
		}
		return out
	}
	addNodes, removeNodes = filter(addNodes), filter(removeNodes)
```
（`ChainByServiceNode` 若不存在则在 store/chains.go 加 `func (s *Store) ChainByServiceNode(ctx context.Context, nodeID int64) (*Chain, error)`——`SELECT ... FROM chains WHERE service_node_id=? AND deleted_at IS NULL LIMIT 1`，未命中返回 nil,nil。）
3. `src/backend/internal/panel/chains.go` 删除链处理器（:1153-1157）追加：
```go
	if chain.ServiceEndpointID != 0 {
		if err := s.disp.ReconcileSharedEndpoint(r.Context(), chain.ServiceEndpointID); err != nil {
			log.Printf("panel: reconcile service endpoint %d after chain delete: %v", chain.ServiceEndpointID, err)
		}
	}
```

`src/backend/internal/panel/users_test.go` 追加对应用例（hy2 链分配用户 → 出口端点 reconcile 被调用、add_user 不扇出到出口节点；复用同包既有 dispatcher 桩）。

Run: `cd src/backend && go test ./... -v && go vet ./...`
Expected: PASS

提交：`git add -A src/backend && git commit -m "feat(dispatch): P4 hy2 出口共享编排与入口终结末段路由"`

---
### Task 7: 订阅输出——hysteria2 链接 / mihomo / sing-box（quanx 跳过）

**Files:**
- Modify: `src/backend/internal/sub/links.go`（buildShareLink :14-108 加 hysteria 分支）
- Modify: `src/backend/internal/sub/sub.go`（buildProxy :772-854 加 hysteria 分支）
- Modify: `src/backend/internal/sub/singbox.go`（sbOutbound :40-55 加 hy2 字段、buildSbOutbound :58-120 加分支）
- Modify: `src/backend/internal/sub/quanx.go`（buildQuanXLine :14 加 hy2 跳过注释分支）
- Test: `src/backend/internal/sub/links_test.go`、`sub_test.go`、`singbox_test.go`（各追加）

**Interfaces:**
- Consumes: `shared.Hy2UserPassword`（Task 2）、`RealizedConfig.{SNI,CertSHA256,ObfsPassword,UpMbps,DownMbps,PortHop}`（Task 2/5 回显）；`clashProxy` 既有 hy2 字段（sub.go:310-315 Ports/Obfs/ObfsPassword/Up/Down、:296 Fingerprint、:311 SkipCertVerify）；链订阅组装（sub.go:679-768——端到端链 `entryNode.Protocol=virtual.Protocol` 天然落到 hysteria；入口终结链走端点 vless 分支 :696-727 天然只见入口协议参数，均无需改）。
- Produces: 无新签名（内部分支扩展）。

**自签信任表达**（沿用 P3 策略）：mihomo 输出 `fingerprint`（证书 sha256 hex pin，与 CertSHA256 同值直用）；hysteria2 URI 与 sing-box 无 pin 表达 → 回退 `insecure=1`/`tls.insecure=true`；ACME 模式全部按普通 TLS 输出（无 pin/insecure）。

- [ ] **Step 1: links.go hysteria2:// 链接（先写失败测试）**

`src/backend/internal/sub/links_test.go` 追加：

```go
// TestHysteria2ShareLink 验证 hysteria2:// 链接（P4 §3.4）：口令派生、跳跃段端口表达、
// salamander/带宽参数、自签 insecure 回退与 ACME 干净输出。
func TestHysteria2ShareLink(t *testing.T) {
	rc := shared.RealizedConfig{Port: 21000, SNI: "www.example.com", CertSHA256: "deadbeef",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031"}
	link, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolHysteria2), rc, "uuid-1")
	if !ok || !strings.HasPrefix(link, "hysteria2://") {
		t.Fatalf("hy2 链接缺失: %q", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.User.String() != shared.Hy2UserPassword("uuid-1") {
		t.Fatalf("口令应为派生值: %q", u.User.String())
	}
	q := u.Query()
	if q.Get("sni") != "www.example.com" || q.Get("insecure") != "1" {
		t.Fatalf("自签应回退 insecure=1: %v", q)
	}
	if q.Get("obfs") != "salamander" || q.Get("obfs-password") != "obfs-pw" {
		t.Fatalf("obfs 参数缺失: %v", q)
	}
	if q.Get("mport") != "20000-20031" {
		t.Fatalf("跳跃段应以 mport 表达: %v", q)
	}
	// ACME（无 pin）：不输出 insecure。
	rcACME := rc
	rcACME.CertSHA256 = ""
	linkACME, _ := buildShareLink(testNode("1.2.3.4", shared.ProtocolHysteria2), rcACME, "uuid-1")
	u2, _ := url.Parse(linkACME)
	if u2.Query().Get("insecure") != "" {
		t.Fatalf("ACME 不应携带 insecure: %s", linkACME)
	}
	// 无跳跃段：无 mport、端口为主端口。
	rcNoHop := rcACME
	rcNoHop.PortHop = ""
	linkNoHop, _ := buildShareLink(testNode("1.2.3.4", shared.ProtocolHysteria2), rcNoHop, "uuid-1")
	u3, _ := url.Parse(linkNoHop)
	if u3.Query().Get("mport") != "" || u3.Port() != "21000" {
		t.Fatalf("无段链接形态不符: %s", linkNoHop)
	}
}
```

Run: `cd src/backend && go test ./internal/sub/ -run TestHysteria2ShareLink -v`
Expected: FAIL（分支不存在，buildShareLink 返回 false）

实现 `src/backend/internal/sub/links.go` `buildShareLink`（:14-108）ss 分支（:98-105）之后插入：

```go
	case shared.ProtocolHysteria2:
		// hysteria2://password@host:port?params#name；口令 = Hy2UserPassword(uuid)。
		// 端口跳跃段以 mport 表达（v2rayN/NekoBox 惯例；主端口 = 段起点由链订阅组装保证）。
		// 自签：URI 无 pin 表达，回退 insecure=1（§3.4 开箱即用）；ACME 按普通 TLS。
		q := url.Values{}
		q.Set("sni", rc.SNI)
		if rc.CertSHA256 != "" {
			q.Set("insecure", "1")
		}
		if rc.ObfsPassword != "" {
			q.Set("obfs", "salamander")
			q.Set("obfs-password", rc.ObfsPassword)
		}
		if rc.UpMbps > 0 {
			q.Set("upmbps", fmt.Sprintf("%d", rc.UpMbps))
		}
		if rc.DownMbps > 0 {
			q.Set("downmbps", fmt.Sprintf("%d", rc.DownMbps))
		}
		if rc.PortHop != "" {
			q.Set("mport", rc.PortHop)
		}
		return fmt.Sprintf("hysteria2://%s@%s?%s#%s",
			url.QueryEscape(shared.Hy2UserPassword(uuid)), addr, q.Encode(), name), true
```

Run: `cd src/backend && go test ./internal/sub/ -run TestHysteria2ShareLink -v`
Expected: PASS

- [ ] **Step 2: sub.go mihomo hysteria2 代理项（先写失败测试）**

`src/backend/internal/sub/sub_test.go` 追加：

```go
// TestBuildProxyHysteria2 验证 mihomo hysteria2 代理项（P4 §3.4）：
// 自签 fingerprint pin、salamander obfs、带宽、ports 段、口令派生；ACME 无 fingerprint。
func TestBuildProxyHysteria2(t *testing.T) {
	rc := shared.RealizedConfig{Port: 21000, Security: shared.SecurityTLS,
		SNI: "www.example.com", CertSHA256: "deadbeef",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031"}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolHysteria2), rc, "uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "hysteria2" || p.Password != shared.Hy2UserPassword("uuid-1") {
		t.Fatalf("类型/口令不符: %+v", p)
	}
	if p.SNI != "www.example.com" || p.Fingerprint != "deadbeef" {
		t.Fatalf("SNI/pin 不符: %+v", p)
	}
	if p.Obfs != "salamander" || p.ObfsPassword != "obfs-pw" {
		t.Fatalf("obfs 不符: %+v", p)
	}
	if p.Ports != "20000-20031" || p.Up != "50 Mbps" || p.Down != "100 Mbps" {
		t.Fatalf("段/带宽不符: %+v", p)
	}
	rcACME := rc
	rcACME.CertSHA256 = ""
	p2, _ := buildProxy(testNode("1.2.3.4", shared.ProtocolHysteria2), rcACME, "uuid-1")
	if p2.Fingerprint != "" {
		t.Fatalf("ACME 不应输出 fingerprint: %+v", p2)
	}
}
```

Run: `cd src/backend && go test ./internal/sub/ -run TestBuildProxyHysteria2 -v`
Expected: FAIL（default 分支报"未知协议"）

实现 `src/backend/internal/sub/sub.go` `buildProxy`（:791-852）ss 分支（:833-841）之后插入：

```go
	case shared.ProtocolHysteria2:
		p.Type = "hysteria2" // mihomo 类型名
		p.Password = shared.Hy2UserPassword(uuid)
		p.SNI = rc.SNI
		if rc.CertSHA256 != "" {
			p.Fingerprint = rc.CertSHA256 // 自签：证书 sha256 pin（mihomo fingerprint）
		}
		if rc.ObfsPassword != "" {
			p.Obfs = "salamander"
			p.ObfsPassword = rc.ObfsPassword
		}
		if rc.UpMbps > 0 {
			p.Up = fmt.Sprintf("%d Mbps", rc.UpMbps)
		}
		if rc.DownMbps > 0 {
			p.Down = fmt.Sprintf("%d Mbps", rc.DownMbps)
		}
		if rc.PortHop != "" {
			p.Ports = rc.PortHop // mihomo ports 多端口段
		}
```

Run: `cd src/backend && go test ./internal/sub/ -run TestBuildProxyHysteria2 -v`
Expected: PASS

- [ ] **Step 3: singbox.go hysteria2 outbound + quanx 跳过（先写失败测试）**

`src/backend/internal/sub/singbox_test.go` 追加：

```go
// TestBuildSbOutboundHysteria2 验证 sing-box hysteria2 outbound（P4 §3.4）：
// 口令派生、tls server_name、自签 insecure 回退（无 pin 表达）、obfs 与 server_ports。
func TestBuildSbOutboundHysteria2(t *testing.T) {
	rc := shared.RealizedConfig{Port: 21000, Security: shared.SecurityTLS,
		SNI: "www.example.com", CertSHA256: "deadbeef",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031"}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolHysteria2), rc, "uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if ob.Type != "hysteria2" || ob.Password != shared.Hy2UserPassword("uuid-1") {
		t.Fatalf("类型/口令不符: %+v", ob)
	}
	if ob.TLS == nil || !ob.TLS.Enabled || ob.TLS.ServerName != "www.example.com" || !ob.TLS.Insecure {
		t.Fatalf("tls 不符（自签应 insecure）: %+v", ob.TLS)
	}
	if ob.Obfs == nil || ob.Obfs.Type != "salamander" || ob.Obfs.Password != "obfs-pw" {
		t.Fatalf("obfs 不符: %+v", ob.Obfs)
	}
	if len(ob.ServerPorts) != 1 || ob.ServerPorts[0] != "20000-20031" {
		t.Fatalf("server_ports 不符: %+v", ob.ServerPorts)
	}
	rcACME := rc
	rcACME.CertSHA256 = ""
	ob2, _ := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolHysteria2), rcACME, "uuid-1")
	if ob2.TLS.Insecure {
		t.Fatalf("ACME 不应 insecure: %+v", ob2.TLS)
	}
}
```

Run: `cd src/backend && go test ./internal/sub/ -run TestBuildSbOutboundHysteria2 -v`
Expected: FAIL（default 分支报不支持）

实现 `src/backend/internal/sub/singbox.go`：
1. `sbOutbound`（:40-55）`Method` 行后追加字段：
```go
	Obfs       *sbHy2Obfs `json:"obfs,omitempty"`        // hysteria2 salamander
	ServerPorts []string   `json:"server_ports,omitempty"` // hysteria2 端口段
	UpMbps     int        `json:"up_mbps,omitempty"`      // hysteria2
	DownMbps   int        `json:"down_mbps,omitempty"`    // hysteria2
```
`sbOutbound` 定义之后追加：
```go
// sbHy2Obfs 是 sing-box hysteria2 的 salamander 混淆段。
type sbHy2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}
```
2. `buildSbOutbound`（:76-118）ss 分支（:99-106）之后插入：
```go
	case shared.ProtocolHysteria2:
		ob.Type = "hysteria2"
		ob.Password = shared.Hy2UserPassword(uuid)
		ob.TLS = &sbTLS{
			Enabled:    true,
			ServerName: rc.SNI,
			Insecure:   rc.CertSHA256 != "", // 自签：sing-box 无 pin 表达，回退 insecure（§3.4）
		}
		if rc.ObfsPassword != "" {
			ob.Obfs = &sbHy2Obfs{Type: "salamander", Password: rc.ObfsPassword}
		}
		if rc.PortHop != "" {
			ob.ServerPorts = []string{rc.PortHop}
		}
		ob.UpMbps = rc.UpMbps
		ob.DownMbps = rc.DownMbps
```
3. `quanx.go` `buildQuanXLine` 协议 switch 的 default 前（或合适分支处）加：
```go
	case shared.ProtocolHysteria2:
		return "" // QuanX 不输出 hy2（尽力而为条款：v1 跳过，spec §3.4）
```

Run: `cd src/backend && go test ./internal/sub/ -v && go build ./...`
Expected: PASS（含新用例；既有 vless/trojan/vmess/ss/ws/tls 用例不回归）

提交：`git add -A src/backend && git commit -m "feat(sub): P4 hysteria2 链接/mihomo/sing-box 订阅输出"`

---

### Task 8: 前端——hy2 协议字段区、入口协议区块、编辑回填、版本门控禁用

**Files:**
- Modify: `src/frontend/src/lib/types.ts`（CreateNodeRequest :411-434 加四字段；CreateChainRequest :524-530 / EditChainRequest :532-539 加 entry_node；Chain 类型加 entry_config）
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts`（DIRECT_PROTOCOLS/RELAY_PROTOCOLS :17-26、PROTOCOL_LABELS :44-52、ChainFormState :125-157、initialChainForm :159-191、openEdit :275-343、onProtocolChange :385-400、onSubmit :420-576、isHy2 派生）
- Modify: `src/frontend/src/pages/chains/ChainFormDialog.tsx`（协议选择器 :371-385 hy2 版本禁用、TLS/证书模式区域 :449-512 对 hy2 复用、追加 hy2 字段区与入口协议区块、landingServer/landingDomain :168-173 复用）
- Modify: `src/frontend/src/lib/xray-version.ts`（新建；若已有版本比较助手则复用——执行时 `grep -rn "xray_version" src/frontend/src/lib/` 确认）
- Test: `src/frontend/src/pages/chains/use-chain-form.test.ts`（追加；不存在则按同目录测试现状新建）、`src/frontend/src/lib/xray-version.test.ts`（新建）

**Interfaces:**
- Consumes: Task 4 的 `entry_node`/`entry_config` API 字段与再生成类型；`Server.xray_version/effective_xray_version`（types.ts:200,218）；`addressFamily`（前端既有，ChainFormDialog 已引用）。
- Produces:
  - `function xrayVersionAtLeast(version: string | null | undefined, min: string): boolean`（lib/xray-version.ts；null/解析失败=false）。
  - `ChainFormState` 新增：`obfsPassword: string`、`upMbps: string`、`downMbps: string`、`portHop: string`（`''`=默认开/`'off'`=关/`'a-b'`=显式段）、`entryProtocolEnabled: boolean`、`entryShortId: string`、`entryDestPreset: string`、`entryDest: string`、`entryServerNames: string`、`entryFingerprint: string`。
  - `isHy2`（controller 派生：`form.protocol === 'hysteria'`）。

**表单行为**（镜像后端矩阵，spec §3.5）：选中 hy2 后隐藏传输/安全层/flow/encryption 选择器；显示证书模式单选（复用现有 TLS 区域——hy2 强制 tls，该区域对 hy2 恒显示）+ hy2 字段区（混淆密码留空自动生成、上/下行带宽默认 50/100、端口跳跃开关+显式段输入）；fingerprint/带宽/显式段进"高级选项"折叠（若现有表单无折叠区组件，则直接平铺并标注"留空自动生成"——执行时以现状最小改动为准）。hy2 出口的中转链显示提示"推荐勾选入口协议：规避运营商 UDP QoS"。入口协议区块 = 勾选框 + VLESS+Reality 子表单（short_id/dest/server_names/fingerprint 全部可空自动生成，协议选择器禁用态固定 vless）。

- [ ] **Step 1: 版本比较助手 + types.ts 字段（先写失败测试）**

`src/frontend/src/lib/xray-version.ts` 新建：

```ts
// xrayVersionAtLeast 三段数字版本比较（hy2 协议选项的版本门控，与后端 shared.XrayMinVersionHy2 同步）。
export const XRAY_MIN_VERSION_HY2 = '26.3.27'

export function xrayVersionAtLeast(
  version: string | null | undefined,
  min: string = XRAY_MIN_VERSION_HY2,
): boolean {
  const parse = (s: string): [number, number, number] | null => {
    const m = /^(\d+)\.(\d+)\.(\d+)/.exec(s.trim())
    return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null
  }
  if (!version) return false
  const a = parse(version)
  const b = parse(min)
  if (!a || !b) return false
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] > b[i]
  }
  return true
}
```

`src/frontend/src/lib/xray-version.test.ts` 新建：

```ts
import { describe, expect, it } from 'vitest'

import { xrayVersionAtLeast } from './xray-version'

describe('xrayVersionAtLeast', () => {
  it('按三段数字比较', () => {
    expect(xrayVersionAtLeast('26.3.27')).toBe(true)
    expect(xrayVersionAtLeast('26.10.1')).toBe(true)
    expect(xrayVersionAtLeast('26.3.26')).toBe(false)
    expect(xrayVersionAtLeast('25.12.8')).toBe(false)
    expect(xrayVersionAtLeast(null)).toBe(false)
    expect(xrayVersionAtLeast('unknown')).toBe(false)
  })
})
```

`src/frontend/src/lib/types.ts`：
1. `CreateNodeRequest`（:411-434）`target_port` 行后追加：
```ts
  obfs_password?: string
  up_mbps?: number
  down_mbps?: number
  port_hop?: string
```
2. `CreateChainRequest`（:524-530）与 `EditChainRequest`（:532-539）`node` 行后各追加：
```ts
  entry_node?: Omit<CreateNodeRequest, 'server_id' | 'name'>
```
3. `Chain` 类型（定位 `export interface Chain`，其含 service_config 字段处）追加：
```ts
  entry_config?: VirtualConfig | null
```

Run: `cd src/frontend && npm test -- xray-version && npm run generate:api && npm run build`
Expected: PASS（注意 entry_config 若已由契约生成则 types.ts 手加处改为引用生成类型，避免重复定义冲突）

- [ ] **Step 2: use-chain-form——hy2 状态与提交载荷（先写失败测试）**

`src/frontend/src/pages/chains/use-chain-form.test.ts`（不存在则新建，参照其他 hook 测试风格）追加：

```ts
// hy2 提交载荷：证书模式/混淆/带宽/跳跃段透传；port_hop=off 归一；入口区块载荷组装。
// 编辑回填：service_config(hysteria) → hy2 字段回填；entry_config 存在 → 入口区块勾选回填。
```

实现 `src/frontend/src/pages/chains/use-chain-form.ts`：
1. 常量：`DIRECT_PROTOCOLS` 与 `RELAY_PROTOCOLS` 在 `'shadowsocks'` 后插入 `'hysteria'`；`PROTOCOL_LABELS` 加：
```ts
  hysteria: 'Hysteria2（UDP 高速 · 需服务商放行 UDP）',
```
2. `ChainFormState`（:125-157）`cipher` 行后追加：
```ts
  obfsPassword: string
  upMbps: string
  downMbps: string
  portHop: string // ''=默认开（自动分配 32 段）；'off'=关闭；'a-b'=显式段
  entryProtocolEnabled: boolean
  entryShortId: string
  entryDestPreset: string
  entryDest: string
  entryServerNames: string
  entryFingerprint: string
```
`initialChainForm`（:159-191）`cipher: 'auto',` 行后追加：
```ts
  obfsPassword: '',
  upMbps: '50',
  downMbps: '100',
  portHop: '',
  entryProtocolEnabled: false,
  entryShortId: '',
  entryDestPreset: DEFAULT_REALITY_DEST,
  entryDest: 'dl.google.com:443',
  entryServerNames: 'dl.google.com',
  entryFingerprint: 'chrome',
```
3. `openEdit`（:275-343）`setForm({...})` 内 `cipher:` 行后追加回填：
```ts
      obfsPassword: String(virtual.obfs_password || ''),
      upMbps: virtual.up_mbps ? String(virtual.up_mbps) : '50',
      downMbps: virtual.down_mbps ? String(virtual.down_mbps) : '100',
      portHop: String(virtual.port_hop || ''),
      entryProtocolEnabled: chain.entry_config != null && String(virtual.protocol ?? '') !== 'vless',
      entryShortId: '',
      entryDestPreset: DEFAULT_REALITY_DEST,
      entryDest: 'dl.google.com:443',
      entryServerNames: 'dl.google.com',
      entryFingerprint: 'chrome',
```
（入口子参数回填：从 `chain.entry_config.template` 的 realitySettings 解析 shortIds/dest/serverNames——复用主表单 :296-302 的同款解析逻辑，抽局部函数 `parseRealityTemplate(template)` 同时服务两处；vless 出口链不带入口区块（endpoint 即主协议），`entryProtocolEnabled` 仅对非 vless 出口且存在 entry_config 时为 true。）
4. `onProtocolChange`（:385-400）：hy2 选中时清安全层相关状态的纠偏追加：
```ts
      // hy2 无 network/security 概念：选择后隐藏对应选择器（提交载荷不携带）。
```
（无状态副作用需要——security 字段提交时按 isHy2 分支跳过，见下。）
5. `onSubmit`（:420-576）`if (isReality) {` 块（:463-517）之前插入 hy2 分支：
```ts
    if (form.protocol === 'hysteria') {
      // hy2 恒 QUIC+TLS：证书模式复用 TLS 区域；矩阵外字段一律不提交（后端 400 兜底）。
      nodeBody.cert_mode = form.certMode
      if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
        nodeBody.tls_domain = form.tlsDomain.trim()
      }
      if (form.obfsPassword.trim()) {
        nodeBody.obfs_password = form.obfsPassword.trim()
      }
      if (form.upMbps.trim()) {
        nodeBody.up_mbps = Number(form.upMbps)
      }
      if (form.downMbps.trim()) {
        nodeBody.down_mbps = Number(form.downMbps)
      }
      if (form.portHop === 'off' || form.portHop.trim()) {
        nodeBody.port_hop = form.portHop.trim() // 'off' 或 'a-b'；'' = 默认开（后端自动分配）
      }
      if (form.chainType === 'relay' && form.entryProtocolEnabled) {
        // 入口协议区块（v1 固定 vless+reality，子参数留空自动生成）。
        const entryNode: EditChainRequest['entry_node'] = { protocol: 'vless', security: 'reality' }
        if (form.entryShortId.trim()) entryNode!.short_id = form.entryShortId.trim()
        if (form.entryDest.trim()) entryNode!.dest = form.entryDest.trim()
        const entryNames = form.entryServerNames.split(',').map((s) => s.trim()).filter(Boolean)
        if (entryNames.length > 0) entryNode!.server_names = entryNames
        entryNode!.fingerprint = form.entryFingerprint
        ;(nodeBody as { __entryNode?: unknown }).__entryNode = entryNode
      }
    }
```
`isReality` 块的进入条件保持不变（`isReality` 为 REALITY_PROTOCOLS 成员判定，hysteria 不在其中，自然跳过）；`body.entry_node` 的挂载：上面用临时字段是示意——**实现时**把 entryNode 组装移到 `nodeBody` 之后、`api.createChain/editChain` 调用前，直接 `body.entry_node = entryNode`（CreateChainRequest/EditChainRequest 已有该字段），不要走 `__entryNode`  hack。
6. controller 返回值（:578-600）`isReality` 后加 `isHy2`；`isHy2` 定义（isReality 定义附近）：
```ts
  const isHy2 = form.protocol === 'hysteria'
```

Run: `cd src/frontend && npm test && npm run lint`
Expected: PASS

- [ ] **Step 3: ChainFormDialog——hy2 渲染与入口区块 UI**

`src/frontend/src/pages/chains/ChainFormDialog.tsx`：
1. 解构（:147-165）`isReality` 后加 `isHy2`。
2. 协议选择器（:371-385）：hy2 选项按落地服务器版本禁用——`SelectItem` 循环改为：
```tsx
                {(form.chainType === 'direct' ? DIRECT_PROTOCOLS : RELAY_PROTOCOLS).map((p) => {
                  const hy2Blocked =
                    p === 'hysteria' &&
                    !xrayVersionAtLeast(
                      landingServer?.effective_xray_version ?? landingServer?.xray_version,
                    )
                  return (
                    <SelectItem key={p} value={p} disabled={hy2Blocked}>
                      {PROTOCOL_LABELS[p] ?? p}
                      {hy2Blocked ? '（节点 xray 版本过低，请先在节点页升级 xray）' : ''}
                    </SelectItem>
                  )
                })}
```
（`landingServer` 定义 :168-173 已存在，需移到协议选择器之前；SelectItem 若无 disabled prop 则按组件库现状用 `opacity-50 pointer-events-none` 类名方案——执行时以 `src/frontend/src/components/ui/select` 现状为准。）
3. 传输/安全层选择器与 reality 区域（:401-512 的 `{isReality && (...)}`）：条件改为 `{isReality && !isHy2 && (...)}`——但**证书模式区域**（:449-512 `{form.security === 'tls' && (...)}`）需对 hy2 恒显示：把证书模式块抽出为独立条件 `{(form.security === 'tls' && isReality) || isHy2}`。实现时把该块改为：
```tsx
          {((isReality && form.security === 'tls') || isHy2) && (
            <div className="space-y-2">…证书模式单选与伪装域名输入（原样）…</div>
          )}
```
4. 证书模式块之后追加 hy2 字段区：
```tsx
          {isHy2 && (
            <>
              <div className="space-y-2">
                <Label htmlFor="obfsPassword">混淆密码（salamander，留空自动生成）</Label>
                <Input
                  id="obfsPassword"
                  value={form.obfsPassword}
                  onChange={(e) => patch({ obfsPassword: e.target.value })}
                  placeholder="留空自动生成"
                />
              </div>
              <div className="grid grid-cols-2 gap-2">
                <div className="space-y-2">
                  <Label htmlFor="upMbps">上行带宽（Mbps）</Label>
                  <Input
                    id="upMbps" type="number" min={0}
                    value={form.upMbps}
                    onChange={(e) => patch({ upMbps: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="downMbps">下行带宽（Mbps）</Label>
                  <Input
                    id="downMbps" type="number" min={0}
                    value={form.downMbps}
                    onChange={(e) => patch({ downMbps: e.target.value })}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor="portHop">端口跳跃段（留空自动分配 32 段；off 关闭）</Label>
                <Input
                  id="portHop"
                  value={form.portHop}
                  onChange={(e) => patch({ portHop: e.target.value })}
                  placeholder="如 20000-20031；NAT 公共端口不足时自动关闭并降级"
                />
                <p className="cg-chain-hint">
                  客户端在段内逐包换端口以抗 QoS；公共端口不足 8 个的机器自动退回固定单端口。
                </p>
              </div>
            </>
          )}
```
5. 入口协议区块（relay 且出口 hy2 时显示推荐提示；relay 且出口 ∈ {hysteria, vless} 时可勾选）——协议选择器之后追加：
```tsx
          {form.chainType === 'relay' && (form.protocol === 'hysteria' || form.protocol === 'vless') && (
            <div className="space-y-2">
              <label className={cn('cg-chain-type', form.entryProtocolEnabled && 'is-selected')}>
                <input
                  type="checkbox"
                  className="sr-only"
                  checked={form.entryProtocolEnabled}
                  onChange={(e) => patch({ entryProtocolEnabled: e.target.checked })}
                />
                使用独立入口协议（VLESS + Reality）
              </label>
              {form.protocol === 'hysteria' && !form.entryProtocolEnabled && (
                <p className="cg-chain-hint">推荐：勾选后 UDP 只在服务器间流动，规避运营商 UDP QoS。</p>
              )}
              {form.entryProtocolEnabled && (
                <>
                  <p className="cg-chain-hint">
                    入口以 VLESS+Reality 终结客户端流量后按出口协议转发；客户端仅见入口协议参数。
                    以下参数全部可留空自动生成。
                  </p>
                  <div className="space-y-2">
                    <Label htmlFor="entryShortId">入口 short_id（可空）</Label>
                    <Input
                      id="entryShortId"
                      value={form.entryShortId}
                      onChange={(e) => patch({ entryShortId: e.target.value })}
                      placeholder="留空自动生成"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="entryDest">入口 dest（可空）</Label>
                    <Input
                      id="entryDest"
                      value={form.entryDest}
                      onChange={(e) => patch({ entryDest: e.target.value })}
                      placeholder="dl.google.com:443"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="entryServerNames">入口 server_names（逗号分隔，可空）</Label>
                    <Input
                      id="entryServerNames"
                      value={form.entryServerNames}
                      onChange={(e) => patch({ entryServerNames: e.target.value })}
                      placeholder="dl.google.com"
                    />
                  </div>
                </>
              )}
            </div>
          )}
```

Run: `cd src/frontend && npm test && npm run lint && npm run build`
Expected: PASS（含契约 --check）

提交：`git add -A src/frontend && git commit -m "feat(frontend): P4 hy2 字段区与入口协议区块表单"`

---

### Task 9: e2e 扩展（hy2 数据面/端口段治理/共享监听/入口区块）与全量回归

**Files:**
- Modify: `scripts/e2e/protocols.sh`（cleanup :24-31 加 hy2 客户端进程清理；:204 矩阵 400 块后插 hy2 节点创建块；:206-213 端口冲突块尾部加 hy2 端口段治理用例 + root 守卫的跳跃段/DNAT 块；订阅断言段 :231-285 加 hysteria2 断言并调 PROXY_COUNT；:318 tls 数据面块后插 hy2 数据面块；LF 保持）
- Modify: `scripts/e2e/chains.sh`（cleanup :40-51 加 hy2 链客户端进程清理；末尾 `echo "E2E-CHAINS PASS"` 前插 hy2 链块：端到端链 6/共享监听合并链 7/入口区块链 8 + root 守卫 DNAT 块 + 删除清理断言；LF 保持）
- Modify: `scripts/e2e/links.sh`（:156-166 节点创建段加 hy2 节点 N3；:168-186 links/YAML 断言段加 hysteria2 断言；停用/启用段 :296/:326 的 wait_clients 加 N3；LF 保持）

**Interfaces:**
- Consumes: Task 2-8 全部产物（面板 `hysteria` 协议节点/链创建 API、realized `sni/cert_sha256/obfs_password/up_mbps/down_mbps/port_hop` 回显、`chains.service_endpoint_id` DTO 字段、`entry_node` 请求字段、hysteria2:// 链接格式 `hysteria2://<派生口令>@host:port?sni&insecure&obfs&obfs-password&upmbps&downmbps&mport#name`、mihomo `type: hysteria2` 代理项）；e2e 既有 helper（protocols.sh 的 `create_node/wait_node/rpc_expect_fail/rpc_data/db`；chains.sh 的 `rpc_data/py/chain_field/wait_chain/db/PROBE_URL/CHAINS_SKIP_EXTERNAL`；links.sh 的 `wait_active/wait_sub_vless/wait_clients`）。
- Produces: 无新接口；本任务产物是回归证据。

**特权前提（设计结论的 e2e 投影）**：无 root/CAP_NET_ADMIN 时带 port_hop 的 hy2 监听会按指向性错误 failed（Task 5 ensureUdpHopDNAT），所以**非 root 跑 e2e 时所有长期存活的 hy2 节点/链一律 `port_hop:"off"` 或落在零段 NAT（自动关跳跃）**；自动段分配/DNAT 落地/段重叠 400/删除清理集中在 root+iptables 守卫块内，非 root 输出 SKIP（该路径由 Task 3/5 单测覆盖）。版本门控（xray < 26.3.27 → failed）本机 xray 26.3.27 达标无法触发，仅 Task 5 单测覆盖，e2e 不造旧版本二进制。

- [ ] **Step 1: protocols.sh hy2 节点创建块（:204 后插入）**

cleanup 函数（:24-31）的 kill 行加 `${HY2XPID:-}`、pkill 段加一行 `pkill -f "xray run -config $WORK/client-hy2.json" 2>/dev/null || true`。

`:204 echo "   矩阵外组合均被 400 拦截 OK（…）"` 行后插入：

```bash
echo ">> hysteria2（port_hop=off：自签默认证书 + 显式 obfs/brutal 透传）"
# port_hop 显式 off：非 root 环境 DNAT 不可用，带段监听会按指向性错误 failed（Task 5）；
# 段语义（自动分配/DNAT/重叠 400）见下方 root 守卫块。
R="$(create_node '{"server_id":1,"protocol":"hysteria","port_hop":"off","obfs_password":"e2e-obfs","up_mbps":50,"down_mbps":100}')"
python3 -c 'import json,sys,re; rc=json.loads(sys.argv[1]); assert rc.get("security")=="tls" and rc.get("sni") and re.fullmatch(r"[0-9a-f]{64}", rc.get("cert_sha256","")) and rc.get("obfs_password")=="e2e-obfs" and rc.get("up_mbps")==50 and rc.get("down_mbps")==100 and not rc.get("port_hop"), rc' "$R"
HY2_NODE_ID="$(db "SELECT id FROM nodes WHERE protocol='hysteria' ORDER BY id LIMIT 1")"
echo "   hy2 realized 含 sni/cert_sha256/obfs/brutal 且 port_hop 为空 OK（UDP 监听，跳过 TCP 探活）"
```

Run: `bash scripts/e2e/protocols.sh`
Expected: 执行到 hy2 块通过（后续断言此时还未加，脚本会在订阅计数处失败——属预期，Step 2/3 补齐后转绿）

- [ ] **Step 2: protocols.sh 端口段治理用例（:206-213 块尾部追加）**

`:213 echo "   同层同端口（异协议/同协议/跨层 ss）均被 400 拦截 OK"` 行后插入：

```bash
echo ">> hy2 端口段治理：udp/tcp 同号共存、同层同号冲突、非法段 400"
R="$(create_node "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$CLASH_PORT,\"port_hop\":\"off\"}")"
echo "   hy2(udp) 与 trojan(tcp) 同号 $CLASH_PORT 共存 201 OK"
HY2_CLASH_PORT=24567
R="$(create_node "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$HY2_CLASH_PORT,\"port_hop\":\"off\"}")"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$HY2_CLASH_PORT,\"port_hop\":\"off\"}"
echo "   第二个 hy2 同号 $HY2_CLASH_PORT 被 400 拦截 OK"
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"hysteria","port_hop":"bad-range"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"hysteria","port_hop":"30500-30505"}'
echo "   非法段格式 / 段长 <8 被 400 拦截 OK"

echo ">> hy2 端口跳跃（root + iptables：自动段分配 + DNAT 落地 + 段重叠 400 + 删除清理）"
if [[ "$(id -u)" == "0" ]] && command -v iptables >/dev/null; then
    R="$(create_node '{"server_id":1,"protocol":"hysteria"}')"
    HOP_RANGE="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]).get("port_hop",""))' "$R")"
    [[ "$HOP_RANGE" =~ ^[0-9]+-[0-9]+$ ]] || { echo "FAIL: 自动跳跃段未分配: $R"; exit 1; }
    HOP_NID="$(db "SELECT id FROM nodes WHERE protocol='hysteria' AND json_extract(realized_config,'$.port_hop')='$HOP_RANGE'")"
    iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" \
        && echo "OK: agent 已下发 udpHop DNAT（段 $HOP_RANGE，comment lattix:node_$HOP_NID）" \
        || { echo "FAIL: 未见 DNAT 规则"; iptables -t nat -S; exit 1; }
    rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":39000,\"port_hop\":\"$(( ${HOP_RANGE%-*} + 5 ))-$(( ${HOP_RANGE%-*} + 15 ))\"}"
    echo "   显式段与自动段 $HOP_RANGE 重叠被 400 拦截 OK"
    rpc_data POST /api/node/delete "{\"node_id\":$HOP_NID}" >/dev/null
    for _ in $(seq 1 15); do
        iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" || break
        sleep 1
    done
    ! iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" \
        && echo "OK: 删除节点后 DNAT 规则已清除" \
        || { echo "FAIL: DNAT 规则残留"; iptables -t nat -S; exit 1; }
else
    echo "SKIP: 非 root 或无 iptables——自动段分配/DNAT/段重叠由 Task 3/5 单测覆盖"
fi
```

Run: `bash scripts/e2e/protocols.sh`
Expected: 端口段治理用例通过（非 root 时跳跃块输出 SKIP）

- [ ] **Step 3: protocols.sh 订阅断言调整（:231-285 段内三处编辑）**

1. `check "type: ss"` 行后加一行 `check "type: hysteria2"`。
2. :253 `grep -q "allowInsecure=1" …` 行后加：

```bash
grep -q "^hysteria2://" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 hysteria2://"; echo "$LINKS_OUT"; exit 1; }
```

3. PROXY_COUNT 段（:281-284）改为：

```bash
PROXY_COUNT="$(grep -c 'server: ' <<<"$SUB")"
EXPECTED=17
[[ "$HAS_VLESSENC" == "true" ]] && EXPECTED=18
[[ "$PROXY_COUNT" -eq "$EXPECTED" ]] || { echo "FAIL: 订阅应有 $EXPECTED 个代理（dokodemo 除外），实际 $PROXY_COUNT"; echo "$SUB"; exit 1; }
echo "   $EXPECTED 个代理项、ws/httpupgrade/hysteria2 字段 OK，dokodemo 已排除"
```

（14 个存量节点 + hy2 主用例/udp 同号/同层冲突共 3 个 hy2 节点 = 17；root 守卫块内的自动段节点在块内已删除，不计入。）

Run: `bash scripts/e2e/protocols.sh`
Expected: 订阅断言全绿（计数 17/18）

- [ ] **Step 4: protocols.sh hy2 数据面块（:318 tls 数据面块后插入）**

`:317-318` 的 tls 200 断言行后插入（客户端不发 udpHop——e2e 无 DNAT 时直发主端口；跳跃数据面语义由 Task 1 探针 + root 守卫块覆盖）：

```bash
echo ">> hysteria2 自签数据面（口令取自订阅 hysteria2:// 链接，pin 钉住自签证书）"
HY2_RC="$(db "SELECT realized_config FROM nodes WHERE id=$HY2_NODE_ID")"
HY2_PORT="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["port"])' "$HY2_RC")"
HY2_SNI="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sni"])' "$HY2_RC")"
HY2_PIN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["cert_sha256"])' "$HY2_RC")"
HY2_LINK="$(python3 - "$LINKS_OUT" "$HY2_PORT" <<'PY'
import sys, urllib.parse
for l in sys.argv[1].splitlines():
    if l.startswith("hysteria2://") and urllib.parse.urlparse(l).port == int(sys.argv[2]):
        print(l); break
PY
)"
[[ -n "$HY2_LINK" ]] || { echo "FAIL: links 缺主用例 hy2 链接（端口 $HY2_PORT）"; echo "$LINKS_OUT"; exit 1; }
python3 - "$WORK/client-hy2.json" "$HY2_LINK" "$HY2_PORT" "$HY2_SNI" "$HY2_PIN" <<'PY'
import json, sys, urllib.parse
path, link, port, sni, pin = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], sys.argv[5]
u = urllib.parse.urlparse(link)
q = urllib.parse.parse_qs(u.query)
password = urllib.parse.unquote(u.username)
assert q.get("sni") == [sni] and q.get("insecure") == ["1"], link
assert q.get("obfs") == ["salamander"] and q.get("obfs-password") == ["e2e-obfs"], link
assert q.get("upmbps") == ["50"] and q.get("downmbps") == ["100"], link
assert "mport" not in q, link  # port_hop=off：无 mport
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11814,
                  "protocol": "socks", "settings": {"auth": "noauth", "udp": True}}],
    "outbounds": [{
        "tag": "hy2", "protocol": "hysteria",
        "settings": {"version": 2, "address": "127.0.0.1", "port": port},
        "streamSettings": {"security": "tls",
            "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                            "pinnedPeerCertSha256": pin},
            "hysteriaSettings": {"version": 2, "auth": password},
            "finalmask": {"udp": [{"type": "salamander", "settings": {"password": "e2e-obfs"}}],
                          "quicParams": {"congestion": "brutal",
                                         "brutalUp": "50 mbps", "brutalDown": "100 mbps"}}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hy2.json" >/dev/null || { echo "FAIL: hy2 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hy2.json" >"$WORK/client-hy2.log" 2>&1 &
HY2XPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11814" --max-time 8 https://example.com/ || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HY2XPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: hysteria2 自签链路 200（派生口令 + salamander + brutal + pin 全通）" \
    || { echo "FAIL: hy2 链路未通"; tail -n 5 "$WORK/client-hy2.log"; exit 1; }
```

Run: `bash scripts/e2e/protocols.sh`
Expected: `E2E-PROTOCOLS PASS`（含 hy2 数据面 200）

- [ ] **Step 5: chains.sh hy2 链块（`echo "E2E-CHAINS PASS"` 前插入）**

cleanup（:40-51）kill 行加 `${HY2XPID6:-} ${HY2CXPID:-}`，pkill 段加两行 `pkill -f "xray run -config $WORK/client-hy2-chain6.json" 2>/dev/null || true` 与 `pkill -f "xray run -config $WORK/client-hy2-chain8.json" 2>/dev/null || true`。

`echo "E2E-CHAINS PASS"` 行前插入：

```bash
echo ">> 链6（hy2 端到端：A=入口、C=零段 NAT 出口，出口共享监听）→ active"
CHAIN6="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"hysteria\"}}")"
CH6="$(py "d['id']" "$CHAIN6")"
wait_chain "$CH6" active 90
SEP6="$(chain_field "$CH6" "c.get('service_endpoint_id')")"
# chain_field 对缺失字段打印 Python None——显式排除，避免误判为已挂端点。
[[ -n "$SEP6" && "$SEP6" != "0" && "$SEP6" != "None" ]] \
    && echo "OK: 链6 出口走 hy2 共享监听（service_endpoint_id=$SEP6）" \
    || { echo "FAIL: hy2 链未挂共享监听: $CHAIN6"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(db "SELECT status FROM shared_endpoints WHERE id=$SEP6")" == "active" ]] && break
    sleep 1
done
[[ "$(db "SELECT status FROM shared_endpoints WHERE id=$SEP6")" == "active" ]] \
    && echo "OK: hy2 出口共享端点 active（UDP 不探活）" \
    || { echo "FAIL: hy2 共享端点: $(db "SELECT status||'|'||COALESCE(error,'') FROM shared_endpoints WHERE id=$SEP6")"; exit 1; }
wait_chain "$CH6" active 30
NID6="$(py "d['hops'][-1]['node_id']" "$CHAIN6")"
CH6_EXIT_RC="$(db "SELECT realized_config FROM nodes WHERE id=$NID6")"
CH6_EXIT_SNI="$(py "d.get('sni') or ''" "$CH6_EXIT_RC")"
CH6_EXIT_PIN="$(py "d.get('cert_sha256') or ''" "$CH6_EXIT_RC")"
CH6_EXIT_OBFS="$(py "d.get('obfs_password') or ''" "$CH6_EXIT_RC")"
CH6_EXIT_HOP="$(py "d.get('port_hop') or ''" "$CH6_EXIT_RC")"
[[ -n "$CH6_EXIT_SNI" && "${#CH6_EXIT_PIN}" == "64" && -n "$CH6_EXIT_OBFS" ]] \
    && echo "OK: 链6 出口 hy2 realized 经端点镜像就绪（sni/pin/obfs）" \
    || { echo "FAIL: 链6 出口 realized 缺失: $CH6_EXIT_RC"; exit 1; }
[[ -z "$CH6_EXIT_HOP" ]] \
    && echo "OK: 零公共端口 NAT 出口自动关闭端口跳跃（功能可用，spec §3.2 回退语义）" \
    || { echo "FAIL: 零段 NAT 不应携带 port_hop: $CH6_EXIT_HOP"; exit 1; }
grep -q "chainfwd_" "$XRAY_CONFIG_A" && echo "OK: A 配置含端到端 forward 配置件（逐跳 UDP）" \
    || { echo "FAIL: A 配置件"; exit 1; }
grep -qE '"protocol":\s*"hysteria"' "$XRAY_CONFIG_C" && echo "OK: C 配置含 hy2 共享监听 inbound" \
    || { echo "FAIL: C 配置缺 hy2 inbound"; exit 1; }

echo ">> 链7（同机第二条 hy2 链 → 共享监听合并：service_endpoint_id 相同）"
CHAIN7="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"hysteria\"}}")"
CH7="$(py "d['id']" "$CHAIN7")"
wait_chain "$CH7" active 90
SEP7="$(chain_field "$CH7" "c.get('service_endpoint_id')")"
[[ "$SEP7" == "$SEP6" ]] \
    && echo "OK: 链7 与链6 合并到同一 hy2 共享监听（endpoint $SEP6）" \
    || { echo "FAIL: 共享监听未合并（$SEP6 vs $SEP7）"; exit 1; }

echo ">> 链8（入口协议区块：entry_node=vless+reality 入口终结 + hy2 出口）→ active"
CHAIN8="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"hysteria\"},\"entry_node\":{\"protocol\":\"vless\",\"security\":\"reality\",\"flow\":\"xtls-rprx-vision\"}}")"
CH8="$(py "d['id']" "$CHAIN8")"
wait_chain "$CH8" active 90
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH8" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP8_PORT="$(chain_field "$CH8" "c['entry_port']")"
EP8_ID="$(chain_field "$CH8" "c['endpoint_id']")"
[[ -n "$EP8_PORT" && "$EP8_PORT" != "0" && "$EP8_PORT" != "None" && -n "$EP8_ID" && "$EP8_ID" != "0" && "$EP8_ID" != "None" ]] \
    && echo "OK: 链8 入口区块端点 active（entry_port=$EP8_PORT）" \
    || { echo "FAIL: 链8 入口区块端点未就绪: $(chain_field "$CH8" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH8" active 30
grep -qE '"protocol":\s*"hysteria"' "$XRAY_CONFIG_A" \
    && echo "OK: 入口终结模式末段 hy2 outbound 落在 A" \
    || { echo "FAIL: A 配置缺末段 hy2 outbound"; exit 1; }

echo ">> 分配用户到 hy2 三条链（chain_ids；端到端链订阅为 hysteria2://，入口区块链为 vless://）"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH6,$CH7,$CH8]}" >/dev/null
ACCESS_UUID8=""
for _ in $(seq 1 15); do
    ACCESS_UUID8="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=[a for a in (u.get('chain_assignments') or []) if a.get('chain_id')==$CH8]
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID8" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID8" ]] || { echo "FAIL: 未取到链8 assignment"; exit 1; }

if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
echo ">> 链6 端到端 hy2 数据面（订阅 hysteria2:// 链接直连，逐跳 UDP 转发到出口共享监听）"
LINK6=""
for _ in $(seq 1 20); do
    LINK6="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=links" | base64 -d | grep '^hysteria2://' | head -1 || true)"
    [[ -n "$LINK6" ]] && break
    sleep 1
done
[[ -n "$LINK6" ]] || { echo "FAIL: 订阅缺端到端 hy2 链条目"; exit 1; }
python3 - "$WORK/client-hy2-chain6.json" "$LINK6" "$CH6_EXIT_PIN" <<'PY'
import json, sys, urllib.parse
path, link, pin = sys.argv[1], sys.argv[2], sys.argv[3]
u = urllib.parse.urlparse(link)
q = urllib.parse.parse_qs(u.query)
password = urllib.parse.unquote(u.username)
sni = q["sni"][0]
obfs = q.get("obfs-password", [""])[0]
up = q.get("upmbps", ["50"])[0]
down = q.get("downmbps", ["100"])[0]
assert q.get("obfs") == ["salamander"] and obfs, link
assert "mport" not in q, link  # 零段 NAT 出口：无跳跃段
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11816,
                  "protocol": "socks", "settings": {"auth": "noauth", "udp": True}}],
    "outbounds": [{
        "tag": "hy2", "protocol": "hysteria",
        "settings": {"version": 2, "address": "127.0.0.1", "port": u.port},
        "streamSettings": {"security": "tls",
            "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                            "pinnedPeerCertSha256": pin},
            "hysteriaSettings": {"version": 2, "auth": password},
            "finalmask": {"udp": [{"type": "salamander", "settings": {"password": obfs}}],
                          "quicParams": {"congestion": "brutal",
                                         "brutalUp": f"{up} mbps", "brutalDown": f"{down} mbps"}}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hy2-chain6.json" >/dev/null || { echo "FAIL: 链6 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hy2-chain6.json" >"$WORK/client-hy2-chain6.log" 2>&1 &
HY2XPID6=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11816" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HY2XPID6 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: 端到端 hy2 链路 200（client→A 逐跳 UDP→C 共享监听→出口）" \
    || { echo "FAIL: 链6 链路未通"; tail -n 5 "$WORK/client-hy2-chain6.log"; exit 1; }

echo ">> 链8 入口区块数据面（vless+reality 客户端 → 入口区块 → 末段 hy2 隧道 → 出口）"
EP8_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP8_ID")"
EP8_PUB="$(py "d['public_key']" "$EP8_RC")"
EP8_SID="$(py "d['short_id']" "$EP8_RC")"
EP8_SNAME="$(py "d['server_name']" "$EP8_RC")"
python3 - "$WORK/client-hy2-chain8.json" "$EP8_PORT" "$ACCESS_UUID8" "$EP8_PUB" "$EP8_SID" "$EP8_SNAME" <<'PY'
import json, sys
path, port, uuid, pbk, sid, sname = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11817,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": "none",
                                           "flow": "xtls-rprx-vision"}]}]},
        "streamSettings": {"network": "tcp", "security": "reality",
                           "realitySettings": {"serverName": sname, "fingerprint": "chrome",
                                               "publicKey": pbk, "shortId": sid}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hy2-chain8.json" >/dev/null || { echo "FAIL: 链8 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hy2-chain8.json" >"$WORK/client-hy2-chain8.log" 2>&1 &
HY2CXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11817" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HY2CXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: 入口区块+hy2 出口链路 200（client→vless+reality 入口区块→hy2 隧道→出口）" \
    || { echo "FAIL: 链8 链路未通"; tail -n 5 "$WORK/client-hy2-chain8.log"; exit 1; }
else
    echo "SKIP: CHAINS_SKIP_EXTERNAL=1，跳过链6/链8 外网数据面"
fi

echo ">> DNAT 端口跳跃冒烟（root + iptables 才执行；在 direct 机 A 上建显式段 hy2 节点）"
if [[ "$(id -u)" == "0" ]] && command -v iptables >/dev/null; then
    DNAT_RES="$(rpc_data POST /api/node/create "{\"server_id\":$AID,\"protocol\":\"hysteria\",\"port_hop\":\"41000-41031\"}")"
    DNAT_NID="$(py "d['id']" "$DNAT_RES")"
    ok_active=""
    for _ in $(seq 1 30); do
        [[ "$(db "SELECT status FROM nodes WHERE id=$DNAT_NID")" == "active" ]] && { ok_active=1; break; }
        sleep 1
    done
    [[ -n "$ok_active" ]] || { echo "FAIL: DNAT hy2 节点未 active: $(db "SELECT status||'|'||COALESCE(error,'') FROM nodes WHERE id=$DNAT_NID")"; exit 1; }
    if iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$DNAT_NID"; then
        echo "OK: A agent 已下发 udpHop DNAT（comment lattix:node_$DNAT_NID）"
    else
        echo "FAIL: 未见 lattix DNAT 规则"; iptables -t nat -S; exit 1
    fi
    rpc_data POST /api/node/delete "{\"node_id\":$DNAT_NID}" >/dev/null
    for _ in $(seq 1 15); do
        iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$DNAT_NID" || break
        sleep 1
    done
    ! iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$DNAT_NID" \
        && echo "OK: 删除节点后 DNAT 规则已清除" \
        || { echo "FAIL: DNAT 规则残留"; iptables -t nat -S; exit 1; }
else
    echo "SKIP: 非 root 或无 iptables，跳过 DNAT 断言（Task 1 冒烟 + Task 5 单测覆盖）"
fi

echo ">> 删 hy2 链：链8/7 → 共享监听保留（链6 仍引用）→ 链6 → 共享监听释放"
rpc_data POST /api/chain/delete "{\"chain_id\":$CH8}" >/dev/null
rpc_data POST /api/chain/delete "{\"chain_id\":$CH7}" >/dev/null
[[ "$(db "SELECT COUNT(*) FROM shared_endpoints WHERE id=$SEP6")" == "1" ]] \
    && echo "OK: 链6 仍引用，hy2 共享监听保留" || { echo "FAIL: 共享监听提前释放"; exit 1; }
rpc_data POST /api/chain/delete "{\"chain_id\":$CH6}" >/dev/null
for _ in $(seq 1 30); do
    [[ "$(db "SELECT COUNT(*) FROM shared_endpoints WHERE id=$SEP6")" == "0" ]] && break
    sleep 1
done
[[ "$(db "SELECT COUNT(*) FROM shared_endpoints WHERE id=$SEP6")" == "0" ]] \
    && echo "OK: 最后一条 hy2 链删除后共享监听已释放" \
    || { echo "FAIL: hy2 共享监听残留"; exit 1; }
```

Run: `bash scripts/e2e/chains.sh`（离线环境 `CHAINS_SKIP_EXTERNAL=1 bash scripts/e2e/chains.sh`）
Expected: `E2E-CHAINS PASS`（存量链 1-5 断言不回归 + hy2 链块全绿；非 root 输出 DNAT SKIP）

- [ ] **Step 6: links.sh hy2 节点与链接断言**

1. :166 `rpc_data POST /api/user/set-nodes "{\"user_id\":1,\"node_ids\":[$N1,$N2]}" >/dev/null` 行前插入：

```bash
N3="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"hysteria","port_hop":"off"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
wait_active "$N3"
```

（port_hop=off 同 protocols.sh 的特权前提：非 root 环境 DNAT 不可用。）

2. set-nodes 行改为 `node_ids`:[$N1,$N2,$N3]。
3. :181 命名 fragment 断言行后插入：

```bash
HY2_LINE="$(grep '^hysteria2://' <<<"$LINKS" | head -1)"
[[ -n "$HY2_LINE" ]] && echo "OK: hysteria2 分享链接存在" || { echo "FAIL: 缺 hysteria2 链接"; echo "$LINKS"; exit 1; }
python3 - "$HY2_LINE" <<'PY'
import sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1])
q = urllib.parse.parse_qs(u.query)
assert u.username, sys.argv[1]                                    # 派生口令
assert q.get("sni"), sys.argv[1]
assert q.get("insecure") == ["1"], sys.argv[1]                    # 自签回退
assert q.get("obfs") == ["salamander"] and q.get("obfs-password"), sys.argv[1]
assert "mport" not in q, sys.argv[1]                              # port_hop=off 无段
assert u.fragment.startswith("lk01-hysteria-"), sys.argv[1]       # 节点命名
PY
```

4. YAML 断言段（:184-186）`echo "OK: YAML 订阅正常"` 所属断言后插入：

```bash
[[ "$(grep -c 'type: hysteria2' <<<"$SUB")" == "1" ]] \
    && echo "OK: YAML 订阅含 hysteria2 项" || { echo "FAIL: YAML 缺 hysteria2"; echo "$SUB"; exit 1; }
```

5. 停用段（:296-297）的 `wait_clients "$N1" "$UUID1" absent && wait_clients "$N2" "$UUID1" absent \` 行改为 `wait_clients "$N1" "$UUID1" absent && wait_clients "$N2" "$UUID1" absent && wait_clients "$N3" "$UUID1" absent \`（续行符保留），下一行 echo 文案"（两节点）"改"（三节点）"；启用段（:326-327）的 `wait_clients "$N1" "$UUID1" present && wait_clients "$N2" "$UUID1" present \` 行同样加 `&& wait_clients "$N3" "$UUID1" present`（续行符前），文案同改。（`wait_sub_vless` 只数 vless 行，不受 hy2 节点影响，无需调整。）

Run: `rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist && bash scripts/e2e/links.sh`
Expected: `E2E-LINKS PASS`（hysteria2 链接/YAML/扇出断言全绿）

- [ ] **Step 7: 全量回归（七个 e2e + 三模块 go test + 前端三件套）**

```bash
# 前置：links.sh 的订阅落地页 SPA 断言需要前端产物
rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist
# 七个 e2e（存量链路不失效的红线证明；chains.sh 外网默认开启，离线加 CHAINS_SKIP_EXTERNAL=1）
bash scripts/e2e/protocols.sh
bash scripts/e2e/chains.sh
bash scripts/e2e/links.sh
LATX_ALLOW_PRIVATE_OUTBOUND=1 bash scripts/e2e/groups.sh
bash scripts/e2e/usernodes.sh
bash scripts/e2e/reconcile.sh
bash scripts/e2e/vlessenc.sh
# 三模块单测
(cd src/shared && go test ./...) && (cd src/backend && go test ./...) && (cd src/agent && go test ./...)
# 前端：契约同步 + 测试 + lint + 构建
cd src/frontend && npm run generate:api && npm test && npm run lint && npm run build
```

Run: 上述整串
Expected: 七个脚本各打印 `E2E-* PASS`；三模块 go test 全 PASS；前端 generate:api 无 diff（契约已同步）+ test/lint/build 全绿

提交：`git add -A scripts/e2e && git commit -m "test(e2e): P4 hy2 数据面/端口段治理/共享监听与全量回归"`

---

## 计划自检记录

**spec 覆盖映射**（`docs/superpowers/specs/2026-09-12-xray-full-protocol-exposure-design.md`，P4 相关节）：

| spec 条目 | 覆盖任务 |
|---|---|
| §1 目标（hy2 全链路 + 存量不失效） | T2-T8 链路逐层落地；T1 数据面实证；T9 全量回归证明红线 |
| §2 协议矩阵（hysteria 行：udp-only、tls 恒开、cert_mode 复用 P3） | T3（normalize 矩阵 + 模板）、T8（前端字段区）、T9（protocols.sh 400 用例） |
| §3.1 端口分层（udp 独立层、同号跨层共存） | T2（PortLayers）、T3（findPortConflict 层语义）、T9（同号共存 201/同层 400） |
| §3.2 端口跳跃四澄清块（DNAT 收敛、段治理、NAT 落段/回退、生命周期） | T3（resolveHy2PortHop/allocUDPPortHop/PortOccupants 段行）、T4（逐跳保留段）、T5（ensureUdpHopDNAT/removeUdpHopDNAT + 权限指向性错误）、T6（逐跳段下发）、T9（root 守卫 DNAT 块 + 零段 NAT 回退断言） |
| §3.3 出口共享监听（同机 hy2 链合并） | T4（service_endpoint_id/EnsureProtocolSharedEndpoint）、T5（endpoint 放行 hysteria）、T6（共享编排 + realized 镜像）、T9（链6/7 合并 + 引用计数释放） |
| §3.4 订阅输出（hysteria2 链接/mihomo/sing-box/quanx 跳过） | T7（links/sub/singbox + 测试）、T9（三格式断言） |
| §3.5 入口协议区块（entry_node 入口终结） | T4（createChainRequest.EntryNode）、T6（末段 hy2 路由）、T8（前端区块）、T9（链8 数据面） |
| §4 链侧编排（阶段语义复用五阶段） | T4（数据模型）、T5（agent 执行）、T6（dispatch 编排） |
| §5 错误语义（版本门控/DNAT 权限/端口冲突指向性） | T3（400 文案）、T5（xrayVersionAtLeast + DNAT 权限错误）、T6（编排失败路径）；T9 注明版本门控仅单测覆盖的原因 |
| §6 验收（每节 Given/When/Then） | T3-T8 各任务测试 + T9 e2e 逐条对应 |
| §7.4 红线（存量链路不失效） | 全文 Global Constraints + T9 Step 7 七个 e2e 全量回归 |

**占位符扫描**：全文代码块无 `...`/`TODO`/`略` 省略；出现的 `{{TAG}}/{{PORT}}/{{CLIENTS}}/{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}` 为 xray 模板既有占位符机制（P3 沿用），`lattix:<tag>` 为 iptables comment 格式串，均属合法内容而非未完成标记。

**类型一致性抽查**：`Hy2DialSpec`（Address/Port/Auth/SNI/CertSHA256/ObfsPassword/UpMbps/DownMbps/PortHop）在 T2 定义、T5 renderHy2Outbound 消费、T6 dispatch 组装三处字段一致；`findPortConflict(occupants, protocol, port, portEnd, excludeChainID)` 五参签名在 T3 定义与 T3/T4 全部调用点一致；`ProtocolHysteria2="hysteria"` 在 T2 定义，T3/T5/T6/T7 引用一致（仅 T7 订阅输出层映射客户端名 hysteria2）；VC/RC 四字段 json tag（obfs_password/up_mbps/down_mbps/port_hop）在 T2 定义、T3 请求结构、T5 回显、T7 订阅、T9 e2e 断言五处一致。

**自检修正记录**（写计划过程中已并入正文）：
1. T1 补独立提交行（`test(dev): P4 hy2 数据面探针脚本`），T2 提交行收窄为只 add src/shared——原 T2 提交行把 Task 1 的 scripts/dev 产物一并扫入，与 T1"单文件提交"表述矛盾。
2. T3 normalize 注释：`port_hop=="off"` 不归一为空（保留哨兵），由 resolveHy2PortHop 开头处理 off 并归一；显式段校验条件为 `PortHop != "" && != "off"`；对应测试断言 `off.PortHop != "off"`。
3. T5 udphop.go 注记：listIPTablesRules 与 runIPTables 合并为单测试缝 runIPTablesCapture，实现时按单缝重构。
4. T5 removeChainPieceItems 注记：`_hop_` 附加 inbound 清理点需执行时先读现状代码再定。
5. T8 注记：entryNode 不用 `__entryNode` hack，直接在 create/edit body 上挂 `body.entry_node`。
6. T9 写法定稿时的对齐（相对任务交接草稿）：(a) 非 root e2e 机器上带 port_hop 的 hy2 监听会按 T5 设计 failed——故 T9 全部长期存活 hy2 节点改为 `port_hop:"off"`，自动段/DNAT/段重叠收敛进 root 守卫块（与 T1 探针、T3/T5 单测分层覆盖，不在非 root e2e 重复）；(b) chains.sh 零段 NAT 出口按 T3 回退语义断言 port_hop 为空，而非草稿的"断言段存在"；(c) chains.sh 的 chain_field 对缺失字段打印 Python None，service_endpoint_id/entry_port/endpoint_id 断言补 `!= "None"` 守卫；(d) T9 全部 bash/python 代码块经 `bash -n`/`py_compile` 语法核验通过。

**与 P3 计划的衔接**：hy2 模板复用 P3 证书占位符（`{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}`）与 cert_mode 管线（自签/ACME 无新代码路径）；realized 的 SNI/CertSHA256 由 P3 证书管线填充，T5 仅补 hy2 四字段透传；订阅侧自签回退 insecure=1 的决策与 P3 links 的 allowInsecure=1 同源（hy2 URI 无 pin 表达，mihomo 用 fingerprint 精确 pin）；T9 数据面客户端复用 P3 tls 数据面块的 pinnedPeerCertSha256 模式。
