# xray 全协议暴露 P2（ws/httpupgrade 传输 + UDP 中转管道）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打通 ws/httpupgrade 传输全链路（panel 模板 → agent 填充提取 → 订阅 links/mihomo/singbox → 前端表单），并把中转跳 dokodemo 管道的 `network` 从写死 tcp 升级为按出口协议分层（ss 出口 = `tcp,udp`，ss 中转 UDP 同步受益）；顺带修复 P1 终审 Important#1（vless 共享端点豁免作用域过宽）。**硬性要求（沿用 P1 红线）：存量链路（vless 共享链、明文 socks/http 链、dokodemo 转发链、reverse/encrypted 隧道链、P1 新增协议链）全部不失效，以完整 e2e 回归证明。**

**Architecture:** 沿用现有"panel 模板 → agent 填充 → xray run -test → 热更新/重启回退"管线，不引入新 core。安全层概念以 `security` 字段（reality/none；tls 属 P3，本期显式 400）贯通 VirtualConfig/RealizedConfig/订阅三侧；ws/httpupgrade 仅以 security=none 出现（矩阵约束），vless 在该组合下强制 VLESS Encryption（spec §2 脚注 1）。UDP 管道由 panel 在 ForwardSpec 中下发监听层、agent 渲染与分层探测，端口占用查询同步分层。

**Tech Stack:** Go（backend/agent/shared 三个 module，go.work workspace）、React+TS（vite/vitest/oxlint）、SQLite（modernc.org/sqlite）、OpenAPI 契约 `docs/openapi.yaml` → 前端生成类型。

**Spec:** `docs/superpowers/specs/2026-09-12-xray-full-protocol-exposure-design.md`（§2 矩阵、§3.2 链路拓扑与端口冲突治理、§3.4 订阅输出、§7 第 2 条）

## Global Constraints

- 不新增任何第三方依赖（Go 与前端均如此）；复用现有 requester/状态机/管线基础设施（AGENTS.md）。
- 协议/传输/安全层常量必须与 `src/shared/config.go` 保持一致，前端常量注释沿用"与后端 shared 包保持一致"。
- 后端 API 字段变更必须同步改 `docs/openapi.yaml` 并在 `src/frontend` 跑 `npm run generate:api`（`npm run build` 内含 `--check` 会拦截不一致）。
- **P2 合法组合矩阵**（spec §2 + normalize 兼容性规则；矩阵外一律 400 并指明冲突字段）：
  - vless/vmess/trojan × tcp/grpc/xhttp × reality —— P1 现状，不得改变。
  - vmess × ws/httpupgrade × none —— 本期新增合法。
  - vless × ws/httpupgrade × none —— 仅当启用 VLESS Encryption（encryption 非空）时合法。
  - vless/vmess × tcp/grpc/xhttp × none —— 矩阵合法（vless 同样须带 Encryption）；前端不暴露入口，仅 API 层放行。
  - trojan × ws/httpupgrade 或 trojan × security=none —— 非法（trojan 不允许 none；ws/httpupgrade 需 tls，属 P3）。
  - reality × ws/httpupgrade —— 非法（xray 官方约束）。
  - security=tls —— 本期一律 400（P3 提供），报文指明"将在 P3 提供"。
  - vision flow 仅 vless + tcp + reality（tls 组合属 P3）。
  - ss/socks/http/dokodemo 无传输/安全层选项，显式传 network/security → 400。
- xray 25.x 起：ws 的 path/host 在 `streamSettings.wsSettings`（host 在 `headers.Host`），httpupgrade 在 `httpupgradeSettings`（path/host 平铺）；以仓库现有 grpc/xhttp 实现为模式参照。
- mihomo 无独立 `network: httpupgrade`：输出为 `network: ws` + `ws-opts.v2ray-http-upgrade: true`（`clashWsOpts` 结构体已有该三字段）；sing-box 有原生 `httpupgrade` transport。
- Go 源文件与前端 TS/TSX 文件均为 CRLF 行尾，编辑时保持；shell 脚本与 openapi.yaml 为 LF。
- 提交信息沿用仓库惯例：`type(scope): 中文摘要`。
- 每个 Task 完成后运行对应验证命令，全绿才提交。
- **存量回归红线**：任何 Task 不得改变既有协议链路在 reality/tcp/grpc/xhttp 组合下的模板结构、端口分配与订阅输出；Task 8 的全量 e2e 回归（含"编辑存量链路原样保存"用例）必须全绿才算 P2 完成。
- 前端不要重装依赖（main 仓 `src/frontend/node_modules` 已装好；若必须装，用 `npm install --legacy-peer-deps`）。

验证命令速查：
- shared: `cd src/shared && go test ./...`
- backend: `cd src/backend && go test ./...`
- agent: `cd src/agent && go test ./...`
- frontend: `cd src/frontend && npm run generate:api && npm test && npm run lint && npm run build`
- e2e（最后统一跑，前置 `export XRAY_BIN=/usr/local/bin/xray`；links.sh 需先 `rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist`；groups.sh 需 `LATX_ALLOW_PRIVATE_OUTBOUND=1`；chains.sh 含外网真实流量）:
  `bash scripts/e2e/protocols.sh` 等 7 个脚本（见 Task 8）。

---

### Task 1: panel — 删除 findPortConflict 的 vless 共享端点豁免（P1 终审 Important#1）

**Files:**
- Modify: `src/backend/internal/panel/ports.go:19-42`（`findPortConflict`）
- Test: `src/backend/internal/panel/ports_test.go:37-44`

**Interfaces:**
- Consumes: 既有 `findPortConflict(occupants []store.PortOccupant, protocol string, port int, excludeChainID int64) error`（签名不变）。
- Produces: 无新签名。行为变更：vless 撞 `Source == "endpoint"` 占用不再无条件放行。

**背景**：链路入口路径的两个调用点（`chains.go:340`、`chains.go:607`）已用 `req.Node.Protocol != shared.ProtocolVLESS` 门控——vless 链入口根本不进 `findPortConflict`，豁免在入口路径是死代码；但 `handleCreateNode`（`nodes.go:236`）与出口节点校验（`chains.go:352`、`chains.go:619`）对 vless 也会调用，豁免在这三条路径上错误放行撞共享端点端口的 vless 节点。共享合并语义由 `EnsureSharedEndpoint` 自身处理，不需要此豁免。

- [ ] **Step 1: 改测试为期望冲突（先失败）**

`src/backend/internal/panel/ports_test.go`，把 `:37-39` 的"vless 加入共享端点 → 放行"用例替换为：

```go
	// vless 撞共享端点 → 冲突：共享合并语义仅存在于链路入口路径（由 chains.go 调用点
	// 按 protocol != vless 门控，不进本函数）；节点创建/出口节点路径不得放行。
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 10080, 0); err == nil {
		t.Error("vless 撞共享端点应冲突（共享合并门控在 chains.go 调用点，不在此函数）")
	}
```

`:41-44` 的"trojan 撞共享端点 → 冲突"用例保持不变。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/panel/ -run TestFindPortConflict -v`
Expected: FAIL（"vless 撞共享端点应冲突"）

- [ ] **Step 3: 删除豁免**

`src/backend/internal/panel/ports.go`：删除 `:31-33` 的豁免块：

```go
		if o.Source == "endpoint" && protocol == shared.ProtocolVLESS {
			continue
		}
```

并把函数文档注释（`:19-21`）改为：

```go
// findPortConflict 端口冲突前置判定（纯函数）：同端口 + 传输层重叠即冲突。
// excludeChainID 用于编辑链路时排除自身既有占用。vless 链入口的共享端点合并语义
// 由调用点门控（chains.go 入口校验 protocol != vless 才进本函数），此处不做协议豁免。
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/panel/ -run TestFindPortConflict -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/ports.go src/backend/internal/panel/ports_test.go
git commit -m "fix(panel): 删除 findPortConflict vless 共享端点豁免（入口门控已覆盖合并语义）"
```

---

### Task 2: shared — ws/httpupgrade 传输常量 + security 概念贯通配置结构

**Files:**
- Modify: `src/shared/config.go:32-40`（Networks）、`:202-220`（VirtualConfig）、`:230-247`（RealizedConfig）
- Test: `src/shared/config_test.go`（追加）

**Interfaces:**
- Produces:
  - `const NetworkWS = "ws"`、`NetworkHTTPUpgrade = "httpupgrade"`。
  - `var Networks = []string{tcp, grpc, xhttp, ws, httpupgrade}`（全部可选传输，前端镜像）。
  - `var RealityNetworks = []string{tcp, grpc, xhttp}`（Reality 兼容子集，xray 官方约束）。
  - `const SecurityReality = "reality"`、`SecurityTLS = "tls"`、`SecurityNone = "none"`；`var Securities = []string{reality, tls, none}`。
  - `VirtualConfig.Security string \`json:"security,omitempty"\``（空 = 按 network 推导，见 Task 3）。
  - `RealizedConfig.Security string \`json:"security,omitempty"\``（agent 上报；空 = 旧数据）。
  - `func (rc RealizedConfig) EffectiveSecurity() string` — 显式值优先；旧 realized（无 security 字段）按 `PublicKey != ""` 回退推导 reality，否则 none。订阅三处（links/mihomo/singbox）与 quanx 共用。

- [ ] **Step 1: 写失败测试**

`src/shared/config_test.go` 追加：

```go
func TestNetworksSplit(t *testing.T) {
	for _, n := range []string{"tcp", "grpc", "xhttp", "ws", "httpupgrade"} {
		if !ValidValue(n, Networks) {
			t.Errorf("Networks 应包含 %s", n)
		}
	}
	for _, n := range []string{NetworkTCP, NetworkGRPC, NetworkXHTTP} {
		if !ValidValue(n, RealityNetworks) {
			t.Errorf("RealityNetworks 应包含 %s", n)
		}
	}
	for _, n := range []string{NetworkWS, NetworkHTTPUpgrade} {
		if ValidValue(n, RealityNetworks) {
			t.Errorf("RealityNetworks 不应包含 %s（xray 官方约束）", n)
		}
	}
}

func TestSecurities(t *testing.T) {
	if !ValidValue(SecurityReality, Securities) || !ValidValue(SecurityTLS, Securities) || !ValidValue(SecurityNone, Securities) {
		t.Error("Securities 应包含 reality/tls/none")
	}
}

func TestEffectiveSecurity(t *testing.T) {
	if got := (RealizedConfig{Security: SecurityNone}).EffectiveSecurity(); got != SecurityNone {
		t.Errorf("显式 none 应原样返回，实际 %q", got)
	}
	if got := (RealizedConfig{PublicKey: "pk"}).EffectiveSecurity(); got != SecurityReality {
		t.Errorf("旧 realized（有公钥无 security 字段）应回退 reality，实际 %q", got)
	}
	if got := (RealizedConfig{}).EffectiveSecurity(); got != SecurityNone {
		t.Errorf("空 realized 应为 none，实际 %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/shared && go test ./... -run 'TestNetworksSplit|TestSecurities|TestEffectiveSecurity' -v`
Expected: FAIL（undefined: RealityNetworks 等）

- [ ] **Step 3: 实现**

`src/shared/config.go` 传输常量区块（`:32-40`）替换为：

```go
// 传输方式（network）：ws/httpupgrade 在 xray 25.x+ 有弃用警告但仍可用；
// Reality 仅与 tcp/grpc/xhttp 组合（xray 官方约束，见 RealityNetworks）。
const (
	NetworkTCP         = "tcp"
	NetworkGRPC        = "grpc"
	NetworkXHTTP       = "xhttp"
	NetworkWS          = "ws"
	NetworkHTTPUpgrade = "httpupgrade"
)

// Networks 是向导可选的全部传输方式。
var Networks = []string{NetworkTCP, NetworkGRPC, NetworkXHTTP, NetworkWS, NetworkHTTPUpgrade}

// RealityNetworks 是 Reality 安全层兼容的传输子集（xray 官方约束）。
var RealityNetworks = []string{NetworkTCP, NetworkGRPC, NetworkXHTTP}

// 安全层（security）：tls 属 P3（自签/ACME 证书），本期仅 reality/none 可用。
const (
	SecurityReality = "reality"
	SecurityTLS     = "tls"
	SecurityNone    = "none"
)

// Securities 是设计矩阵中的全部安全层取值（normalize 对 tls 单独 400 引导）。
var Securities = []string{SecurityReality, SecurityTLS, SecurityNone}
```

`VirtualConfig`（`:205-220`）在 `Network` 字段后追加 `Security`，并把 `Path/Mode/Host/ServiceName` 注释改为多传输共用：

```go
	Network       string             `json:"network,omitempty"`
	Security      string             `json:"security,omitempty"`       // reality/none（tls 属 P3）；空 = 按 network 推导
	ServiceName   string             `json:"service_name,omitempty"`   // grpc
	Path          string             `json:"path,omitempty"`           // xhttp/ws/httpupgrade
	Mode          string             `json:"mode,omitempty"`           // xhttp
	Host          string             `json:"host,omitempty"`           // xhttp/ws/httpupgrade
```

`RealizedConfig`（`:232-247`）在 `Network` 字段后追加：

```go
	Security    string `json:"security,omitempty"` // 生效安全层（reality/none；tls 属 P3）
```

文件末尾追加：

```go
// EffectiveSecurity 返回生效安全层：显式值优先；旧 realized（无 security 字段）
// 按 reality 指纹（public_key 非空）回退推导（订阅输出兼容存量数据）。
func (rc RealizedConfig) EffectiveSecurity() string {
	if rc.Security != "" {
		return rc.Security
	}
	if rc.PublicKey != "" {
		return SecurityReality
	}
	return SecurityNone
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/shared && go test ./... && cd ../backend && go build ./... && cd ../agent && go build ./...`
Expected: PASS + 两模块编译通过（新增字段/常量不破坏既有调用）

- [ ] **Step 5: Commit**

```bash
git add src/shared/config.go src/shared/config_test.go
git commit -m "feat(shared): ws/httpupgrade 传输常量与 security 安全层字段"
```

---

### Task 3: panel — normalize 安全层矩阵 + buildVirtualConfig ws/httpupgrade 模板 + 契约

**Files:**
- Modify: `src/backend/internal/panel/nodes.go:80-100`（`createNodeRequest`）、`:103-207`（`normalize`）、`:476-484`（streamSettings 接线）、`:494-508`（VirtualConfig 返回）、`:511-537`（`realityStreamSettings` 重构）
- Modify: `docs/openapi.yaml:902-921`（VirtualConfig schema 加 `security`）
- Modify: `src/frontend/src/lib/api-contract.generated.ts`（`npm run generate:api` 产物）
- Test: `src/backend/internal/panel/nodes_test.go`（追加）

**Interfaces:**
- Consumes: `shared.NetworkWS/NetworkHTTPUpgrade/Networks/RealityNetworks/SecurityReality/SecurityNone/SecurityTLS/Securities`（Task 2）。
- Produces:
  - `createNodeRequest.Security string \`json:"security"\``（空 = 推导：reality 兼容传输 → reality；ws/httpupgrade → none）。
  - `func networkSubSettings(req createNodeRequest) map[string]any` — 按 network 返回 grpcSettings/xhttpSettings/wsSettings/httpupgradeSettings 子段（tcp 返回 nil）；reality 与 none 两种 streamSettings 共用。
  - `func plainStreamSettings(req createNodeRequest) map[string]any` — security=none 的 streamSettings（无 realitySettings）。
  - `buildVirtualConfig` 返回的 `VirtualConfig` 携带 `Security`（Task 4 agent 与 Task 7 前端回填依赖）。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/panel/nodes_test.go` 追加（文件头 import 需补 `encoding/json`）：

```go
// TestNormalizeTransportSecurityMatrix 验证 spec §2 矩阵的 normalize 落地：
// 合法组合放行并补默认值，矩阵外组合 400（报错指明冲突字段）。
func TestNormalizeTransportSecurityMatrix(t *testing.T) {
	// 合法：vmess+ws → security 自动推导 none，path/host 保留
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Path: "/p", Host: "h.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+ws 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Path != "/p" || req.Host != "h.example.com" {
		t.Errorf("vmess+ws 归一化不符: security=%q path=%q host=%q", req.Security, req.Path, req.Host)
	}
	if req.ShortID != "" || req.Dest != "" || len(req.ServerNames) != 0 {
		t.Errorf("security=none 应清空 reality 专有字段: %+v", req)
	}
	// 合法：vless+httpupgrade+VLESS Encryption（spec §2 脚注 1）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkHTTPUpgrade, Encryption: shared.VLessEncMLKEM768}
	if err := req.normalize(); err != nil {
		t.Fatalf("vless+httpupgrade+Encryption 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Flow != "" || req.Path != "/" {
		t.Errorf("vless+httpupgrade 归一化不符: security=%q flow=%q path=%q", req.Security, req.Flow, req.Path)
	}
	// 非法：reality × ws
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Security: shared.SecurityReality}
	if err := req.normalize(); err == nil {
		t.Error("reality×ws 应 400")
	}
	// 非法：trojan × ws（推导 none，trojan 不允许 none）
	req = &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("trojan+ws 应 400")
	}
	// 非法：vless+ws 无 Encryption
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("vless+ws 无 Encryption 应 400")
	}
	// 非法：ss 显式带传输层
	req = &createNodeRequest{Protocol: shared.ProtocolShadowsocks, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("ss 带 network 应 400")
	}
	// 非法：security=tls（P3 才开放）
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS}
	if err := req.normalize(); err == nil {
		t.Error("security=tls 应 400（P3 提供）")
	}
	// 非法：vision × none（vision 仅 vless+tcp+reality）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityNone, Encryption: shared.VLessEncX25519, Flow: shared.FlowVision}
	if err := req.normalize(); err == nil {
		t.Error("vision+none 应 400")
	}
	// 回归：存量默认（vless 空表单）仍 = tcp+reality+vision
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Security != shared.SecurityReality || req.Network != shared.NetworkTCP || req.Flow != shared.FlowVision {
		t.Errorf("存量默认不符: security=%q network=%q flow=%q", req.Security, req.Network, req.Flow)
	}
}

// TestBuildVirtualConfigWSPlain 验证 ws/httpupgrade + security=none 模板形状
// （xray 25.x：ws 在 wsSettings、host 在 headers.Host；httpupgrade 在 httpupgradeSettings）。
func TestBuildVirtualConfigWSPlain(t *testing.T) {
	req := createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS,
		Security: shared.SecurityNone, Path: "/p", Host: "h.example.com"}
	vc := buildVirtualConfig(req)
	if vc.Security != shared.SecurityNone {
		t.Errorf("VirtualConfig.Security 应为 none，实际 %q", vc.Security)
	}
	var tmpl struct {
		StreamSettings struct {
			Network    string `json:"network"`
			Security   string `json:"security"`
			WsSettings struct {
				Path    string            `json:"path"`
				Headers map[string]string `json:"headers"`
			} `json:"wsSettings"`
			RealitySettings json.RawMessage `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl); err != nil {
		t.Fatal(err)
	}
	ss := tmpl.StreamSettings
	if ss.Network != "ws" || ss.Security != "none" {
		t.Errorf("streamSettings 顶层不符: network=%q security=%q", ss.Network, ss.Security)
	}
	if ss.WsSettings.Path != "/p" || ss.WsSettings.Headers["Host"] != "h.example.com" {
		t.Errorf("wsSettings 不符: %+v", ss.WsSettings)
	}
	if len(ss.RealitySettings) != 0 {
		t.Error("security=none 模板不应带 realitySettings")
	}

	req = createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkHTTPUpgrade,
		Security: shared.SecurityNone, Path: "/hu", Host: "cdn.example.com"}
	vc = buildVirtualConfig(req)
	var tmpl2 struct {
		StreamSettings struct {
			Network             string `json:"network"`
			HTTPUpgradeSettings struct {
				Path string `json:"path"`
				Host string `json:"host"`
			} `json:"httpupgradeSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl2); err != nil {
		t.Fatal(err)
	}
	if tmpl2.StreamSettings.Network != "httpupgrade" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Path != "/hu" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Host != "cdn.example.com" {
		t.Errorf("httpupgradeSettings 不符: %+v", tmpl2.StreamSettings)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/panel/ -run 'TestNormalizeTransportSecurityMatrix|TestBuildVirtualConfigWSPlain' -v`
Expected: FAIL（unknown field Security / 非法组合未拦截）

- [ ] **Step 3: 实现**

a) `createNodeRequest`（`nodes.go:80-100`）在 `Network` 行后插入，并把 `Path/Host` 注释改为多传输：

```go
	Network       string   `json:"network"`        // tcp（默认）/ grpc / xhttp / ws / httpupgrade
	Security      string   `json:"security"`       // reality（默认推导）/ none；tls 属 P3（400 引导）
	ServiceName   string   `json:"service_name"`   // grpc，默认 "grpc"
	Path          string   `json:"path"`           // xhttp/ws/httpupgrade，默认 "/"
	Mode          string   `json:"mode"`           // xhttp，默认 auto
	Host          string   `json:"host"`           // xhttp/ws/httpupgrade，可空
```

同时把结构体文档注释（`:76-79`）中"reality 系（vless/vmess/trojan）使用 short_id/dest/server_names/fingerprint/network 及 grpc/xhttp 子选项"改为"……及 grpc/xhttp/ws/httpupgrade 子选项；security 仅 reality 系协议有效（reality/none，tls 属 P3）"。

b) `normalize()` 的 `if shared.IsRealityProtocol(req.Protocol) { ... }` 块（`:116-158`）整体替换为：

```go
	if shared.IsRealityProtocol(req.Protocol) {
		if req.Network == "" {
			req.Network = shared.NetworkTCP
		}
		if !shared.ValidValue(req.Network, shared.Networks) {
			return fmt.Errorf("不支持的传输方式: %s", req.Network)
		}
		// security 缺省推导：reality 兼容传输默认 reality；ws/httpupgrade 只能 none（矩阵）。
		if req.Security == "" {
			if shared.ValidValue(req.Network, shared.RealityNetworks) {
				req.Security = shared.SecurityReality
			} else {
				req.Security = shared.SecurityNone
			}
		}
		if !shared.ValidValue(req.Security, shared.Securities) {
			return fmt.Errorf("不支持的 security: %s", req.Security)
		}
		if req.Security == shared.SecurityTLS {
			return fmt.Errorf("security=tls 将在 P3 阶段提供，当前请选择 reality 或 none")
		}
		if req.Security == shared.SecurityReality && !shared.ValidValue(req.Network, shared.RealityNetworks) {
			return fmt.Errorf("network=%s 与 security=reality 冲突：Reality 仅支持 tcp/grpc/xhttp", req.Network)
		}
		switch req.Network {
		case shared.NetworkGRPC:
			if req.ServiceName == "" {
				req.ServiceName = "grpc"
			}
			req.Path, req.Mode, req.Host = "", "", ""
		case shared.NetworkXHTTP:
			if req.Path == "" {
				req.Path = "/"
			}
			if req.Mode == "" {
				req.Mode = "auto"
			}
			if !shared.ValidValue(req.Mode, shared.XHTTPModes) {
				return fmt.Errorf("不支持的 xhttp mode: %s", req.Mode)
			}
			req.ServiceName = ""
		case shared.NetworkWS, shared.NetworkHTTPUpgrade:
			if req.Path == "" {
				req.Path = "/"
			}
			req.ServiceName, req.Mode = "", ""
		default: // tcp
			req.ServiceName, req.Path, req.Mode, req.Host = "", "", "", ""
		}
		if req.Security == shared.SecurityReality {
			if req.Fingerprint == "" {
				req.Fingerprint = shared.FingerprintChrome
			}
			if !shared.ValidValue(req.Fingerprint, shared.Fingerprints) {
				return fmt.Errorf("不支持的 uTLS 指纹: %s", req.Fingerprint)
			}
			if req.ShortID == "" {
				req.ShortID = randomHex(8)
			}
			if req.Dest == "" {
				req.Dest = "dl.google.com:443"
			}
			if len(req.ServerNames) == 0 {
				req.ServerNames = []string{"dl.google.com"}
			}
		} else {
			// security=none：Reality 专有字段无意义，一律清空；并按协议执行矩阵约束。
			req.ShortID, req.Dest, req.ServerNames, req.Fingerprint = "", "", nil, ""
			if req.Protocol == shared.ProtocolTrojan {
				return fmt.Errorf("trojan 不允许 security=none（protocol 与 security 冲突；trojan 需 reality，tls 将在 P3 提供）")
			}
			if req.Protocol == shared.ProtocolVLESS && req.Encryption == "" {
				return fmt.Errorf("vless 在 security=none 下必须启用 VLESS Encryption（security 与 encryption 冲突）")
			}
		}
	} else {
		// ss/socks/http/dokodemo 无传输/安全层选项（矩阵外组合 400 并指明冲突字段）。
		if req.Network != "" {
			return fmt.Errorf("协议 %s 无传输层选项（protocol 与 network 冲突）", req.Protocol)
		}
		if req.Security != "" {
			return fmt.Errorf("协议 %s 无安全层选项（protocol 与 security 冲突）", req.Protocol)
		}
	}
```

c) 协议 switch 的 vless 分支（`:161-180`）替换为：

```go
	case shared.ProtocolVLESS:
		// flow 语义：未填 + tcp + reality → 默认 vision；显式 "none" → 无 flow；其余组合必须无 flow。
		if req.Flow == "" && req.Network == shared.NetworkTCP && req.Security == shared.SecurityReality {
			req.Flow = shared.FlowVision
		}
		if req.Flow == "none" {
			req.Flow = ""
		}
		if req.Flow != "" && req.Flow != shared.FlowVision {
			return fmt.Errorf("不支持的 flow: %s", req.Flow)
		}
		if req.Flow == shared.FlowVision && req.Network != shared.NetworkTCP {
			return fmt.Errorf("flow=%s 仅适用于 tcp 传输（grpc/xhttp/ws/httpupgrade 请选择无 flow）", shared.FlowVision)
		}
		if req.Flow == shared.FlowVision && req.Security != shared.SecurityReality {
			return fmt.Errorf("flow=%s 与 security=%s 冲突：vision 仅 reality（tls 将在 P3 提供）", shared.FlowVision, req.Security)
		}
		if req.Encryption != "" {
			if !shared.ValidValue(req.Encryption, shared.VLessEncMethods) {
				return fmt.Errorf("不支持的 VLESS Encryption 认证方式: %s", req.Encryption)
			}
			// vision + Encryption 允许组合（native 拼接），客户端字符串按 1-RTT 下发（§15）。
		}
```

d) streamSettings 接线（`:482-484`）替换为：

```go
	if shared.IsRealityProtocol(req.Protocol) {
		if req.Security == shared.SecurityNone {
			inbound["streamSettings"] = plainStreamSettings(req)
		} else {
			inbound["streamSettings"] = realityStreamSettings(req)
		}
	}
```

e) `buildVirtualConfig` 返回字面量（`:494-508`）在 `Network: req.Network,` 行后加 `Security: req.Security,`。

f) `realityStreamSettings`（`:511-537`）的 network 子段 switch 提取为共用助手，并新增 plain 变体：

```go
// realityStreamSettings 构造 reality 安全层的 streamSettings（Reality 仅支持 tcp/grpc/xhttp）。
func realityStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  req.Network,
		"security": "reality",
		"realitySettings": map[string]any{
			"show":         false,
			"dest":         req.Dest,
			"xver":         0,
			"minClientVer": "0", // 26.7.11+ 缺省默认 26.3.27，会拒绝版本声明旧的客户端（mihomo/clash）
			"serverNames":  req.ServerNames,
			"privateKey":   shared.PlaceholderRealityPrivateKey,
			"shortIds":     []string{req.ShortID},
		},
	}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// plainStreamSettings 构造 security=none 的 streamSettings（无 realitySettings）：
// vmess 自带 AEAD；vless 由 VLESS Encryption 提供认证（spec §2 脚注 1）。
func plainStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{"network": req.Network, "security": "none"}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// networkSubSettings 返回各传输的子配置段（tcp 无子段）：
// xray 25.x 起 ws 在 wsSettings（host 在 headers.Host）、httpupgrade 在 httpupgradeSettings。
func networkSubSettings(req createNodeRequest) map[string]any {
	switch req.Network {
	case shared.NetworkGRPC:
		return map[string]any{"grpcSettings": map[string]any{"serviceName": req.ServiceName}}
	case shared.NetworkXHTTP:
		x := map[string]any{"path": req.Path, "mode": req.Mode}
		if req.Host != "" {
			x["host"] = req.Host
		}
		return map[string]any{"xhttpSettings": x}
	case shared.NetworkWS:
		w := map[string]any{"path": req.Path}
		if req.Host != "" {
			w["headers"] = map[string]any{"Host": req.Host}
		}
		return map[string]any{"wsSettings": w}
	case shared.NetworkHTTPUpgrade:
		h := map[string]any{"path": req.Path}
		if req.Host != "" {
			h["host"] = req.Host
		}
		return map[string]any{"httpupgradeSettings": h}
	}
	return nil
}
```

g) `docs/openapi.yaml` VirtualConfig schema（`:910` 的 `network:` 行后）加：

```yaml
        security: {type: string}
```

h) 重新生成前端契约类型：`cd src/frontend && npm run generate:api`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/panel/ -v -run 'Normalize|StreamSettings|VirtualConfig' && go test ./... && cd ../frontend && npm run check:api`
Expected: PASS + 契约一致（既有 TestNormalizeVMessCipher / TestRealityStreamSettingsPinsMinClientVer 不回归）

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/nodes.go src/backend/internal/panel/nodes_test.go docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts
git commit -m "feat(panel): security 矩阵 normalize 与 ws/httpupgrade 模板生成"
```

---

### Task 4: agent — fill 提取 ws/httpupgrade/Security + pickPort :0 兜底 UDP 探测 + 端点路由 outbound 适配

**Files:**
- Modify: `src/agent/internal/xray/fill.go:100-120`（probe 结构）、`:131-147`（realized 构造）、`:301-351`（pickPort :0 兜底）
- Modify: `src/agent/internal/xray/endpoint.go:125-160`（`renderSharedEndpointOutbound`）
- Test: `src/agent/internal/xray/fill_test.go`（新建）、`src/agent/internal/xray/endpoint_test.go`（追加）

**Interfaces:**
- Consumes: `shared.SecurityReality/SecurityNone`（Task 2）；panel 产出的 ws/httpupgrade 模板（Task 3）。
- Produces:
  - `func templateSecurity(tmpl map[string]json.RawMessage) string` — 模板含 realitySettings → `reality`，有 streamSettings 但无 realitySettings → `none`，无 streamSettings → `""`。
  - `func pickRandomFreePort(layers string) (int, error)` — :0 兜底分支抽出：OS 随机端口须 layers 全部层空闲（修复 P1 已知缺口"自动分配不探测 UDP 层"）。
  - `fillTemplate` 上报的 `RealizedConfig` 新增 `Security`，且 `Path/Host` 对 ws（含 headers.Host）/httpupgrade 正确提取（Task 5 订阅与 Task 7 前端回填依赖）。

- [ ] **Step 1: 写失败测试**

新建 `src/agent/internal/xray/fill_test.go`：

```go
package xray

import (
	"encoding/json"
	"testing"

	"lattix/shared"
)

// TestFillTemplateWSRealized 验证 ws 模板的 realized 提取：path/host（headers.Host）
// 与 security=none（无 realitySettings 无公钥）。
func TestFillTemplateWSRealized(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVMess,
		Security: shared.SecurityNone,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vmess", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "ws", "security": "none",
				"wsSettings": {"path": "/p", "headers": {"Host": "h.example.com"}}}
		}`),
	}
	m := &Manager{}
	_, realized, err := m.fillTemplate(23401, "node_1", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Network != "ws" || realized.Path != "/p" || realized.Host != "h.example.com" {
		t.Errorf("ws realized 提取不符: %+v", realized)
	}
	if realized.Security != shared.SecurityNone {
		t.Errorf("security 应为 none，实际 %q", realized.Security)
	}
	if realized.PublicKey != "" || realized.ShortID != "" || realized.ServerName != "" {
		t.Errorf("非 reality 模板不应有 reality 字段: %+v", realized)
	}
}

// TestFillTemplateHTTPUpgradeRealized 验证 httpupgrade 模板（httpupgradeSettings 平铺 path/host）。
func TestFillTemplateHTTPUpgradeRealized(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVMess,
		Security: shared.SecurityNone,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vmess", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "httpupgrade", "security": "none",
				"httpupgradeSettings": {"path": "/hu", "host": "cdn.example.com"}}
		}`),
	}
	m := &Manager{}
	_, realized, err := m.fillTemplate(23402, "node_2", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Network != "httpupgrade" || realized.Path != "/hu" || realized.Host != "cdn.example.com" {
		t.Errorf("httpupgrade realized 提取不符: %+v", realized)
	}
	if realized.Security != shared.SecurityNone {
		t.Errorf("security 应为 none，实际 %q", realized.Security)
	}
}

// TestFillTemplateRealitySecurity 回归：reality 模板提取 security=reality。
func TestFillTemplateRealitySecurity(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVLESS,
		Security: shared.SecurityReality,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vless", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}", "decryption": "none"},
			"streamSettings": {"network": "tcp", "security": "reality",
				"realitySettings": {"dest": "dl.google.com:443", "serverNames": ["dl.google.com"],
					"privateKey": "{{PRIVATE_KEY}}", "shortIds": ["ab12"]}}
		}`),
	}
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	m, _ := newRebuildTestManager(t)
	_, realized, err := m.fillTemplate(23403, "node_3", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Security != shared.SecurityReality {
		t.Errorf("reality 模板 security 应为 reality，实际 %q", realized.Security)
	}
	if realized.ShortID != "ab12" || realized.ServerName != "dl.google.com" {
		t.Errorf("reality realized 回归不符: %+v", realized)
	}
}

// TestPickRandomFreePortLayered 验证 :0 兜底返回端口在指定层全部空闲
// （P1 缺口：自动分配分支不探测 UDP 层）。
func TestPickRandomFreePortLayered(t *testing.T) {
	p, err := pickRandomFreePort("tcp,udp")
	if err != nil {
		t.Fatal(err)
	}
	if p <= 0 {
		t.Fatalf("端口应 >0，实际 %d", p)
	}
	if err := probePortFree("tcp,udp", p); err != nil {
		t.Errorf("返回端口 %d 应 tcp+udp 双层空闲: %v", p, err)
	}
}
```

`xray x25519` 外部命令与 dest 探测复用既有测试桩：`newRebuildTestManager(t)`（`rebuild_test.go:17`，同包直接可用，假 xray 脚本对 `x25519` 输出可解析假值）+ `destReachable` 包级变量替换（`rebuild_test.go:92-94` 同款模式），**无分叉**。

`templateSecurity` 为纯函数，单独测试（正式用例，非备选）：

```go
// TestTemplateSecurity 验证纯函数按模板内容推导 security。
func TestTemplateSecurity(t *testing.T) {
	reality := map[string]json.RawMessage{
		"streamSettings": json.RawMessage(`{"network":"tcp","realitySettings":{}}`),
	}
	if got := templateSecurity(reality); got != shared.SecurityReality {
		t.Errorf("含 realitySettings 应为 reality，实际 %q", got)
	}
	plain := map[string]json.RawMessage{
		"streamSettings": json.RawMessage(`{"network":"ws"}`),
	}
	if got := templateSecurity(plain); got != shared.SecurityNone {
		t.Errorf("无 realitySettings 应为 none，实际 %q", got)
	}
	if got := templateSecurity(map[string]json.RawMessage{}); got != "" {
		t.Errorf("无 streamSettings 应为空串，实际 %q", got)
	}
}
```

`src/agent/internal/xray/endpoint_test.go` 追加：

```go
// TestRenderSharedEndpointOutboundPlain 验证端点路由 outbound 按出口 realized 分派：
// 出口无 reality 公钥（ws/httpupgrade + none）时不输出 realitySettings，
// 传输子段走 wsSettings/httpupgradeSettings；VLESS Encryption 随 user.encryption 携带。
func TestRenderSharedEndpointOutboundPlain(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 1, TargetAddress: "127.0.0.1", TargetPort: 1443, TunnelUUID: "t-uuid",
		Target: shared.RealizedConfig{Network: shared.NetworkWS, Path: "/p", Host: "h.example.com",
			Security: shared.SecurityNone, Encryption: "mlkem768x25519plus.0rtt.XXX"},
	}
	ob := renderSharedEndpointOutbound(route, "shared_endpoint_route_1_1")
	stream := nested(ob, "streamSettings")
	if stream["security"] != "none" {
		t.Errorf("无公钥出口应为 security=none: %v", stream)
	}
	if _, ok := stream["realitySettings"]; ok {
		t.Error("security=none 不应输出 realitySettings")
	}
	ws, ok := stream["wsSettings"].(map[string]any)
	if !ok || ws["path"] != "/p" {
		t.Fatalf("wsSettings 不符: %v", stream)
	}
	headers, _ := ws["headers"].(map[string]any)
	if headers["Host"] != "h.example.com" {
		t.Errorf("ws headers.Host 不符: %v", ws)
	}
	settings := nested(ob, "settings")
	vnext := settings["vnext"].([]map[string]any)[0]
	user := vnext["users"].([]map[string]any)[0]
	if user["encryption"] != "mlkem768x25519plus.0rtt.XXX" {
		t.Errorf("隧道身份应携带 Encryption 客户端字符串: %v", user)
	}
}
```

（`nested` 助手见 `chain_test.go`，endpoint_test.go 同包可直接用。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/agent && go test ./internal/xray/ -run 'TestFillTemplateWSRealized|TestFillTemplateHTTPUpgradeRealized|TestFillTemplateRealitySecurity|TestTemplateSecurity|TestPickRandomFreePortLayered|TestRenderSharedEndpointOutboundPlain' -v`
Expected: FAIL（undefined: pickRandomFreePort / templateSecurity / ws realized 提取为空 / outbound 硬编码 reality）

- [ ] **Step 3: 实现**

a) `fill.go` probe 结构（`:105-119` 的 `StreamSettings` 内）在 `XHTTPSettings` 后追加：

```go
			WsSettings struct {
				Path    string `json:"path"`
				Headers struct {
					Host string `json:"Host"`
				} `json:"headers"`
			} `json:"wsSettings"`
			HTTPUpgradeSettings struct {
				Path string `json:"path"`
				Host string `json:"host"`
			} `json:"httpupgradeSettings"`
```

b) realized 构造（`:131-146`）：字面量中 `Encryption: encClient,` 后加 `Security: templateSecurity(tmpl),`；字面量之后、`return` 之前追加按传输覆盖提取：

```go
	// ws/httpupgrade 的 path/host 在各自子段（ws 的 host 在 headers.Host），
	// 与 xhttp 平铺字段不同源，按 network 分派覆盖。
	switch realized.Network {
	case shared.NetworkWS:
		realized.Path = probe.StreamSettings.WsSettings.Path
		realized.Host = probe.StreamSettings.WsSettings.Headers.Host
		realized.Mode = ""
	case shared.NetworkHTTPUpgrade:
		realized.Path = probe.StreamSettings.HTTPUpgradeSettings.Path
		realized.Host = probe.StreamSettings.HTTPUpgradeSettings.Host
		realized.Mode = ""
	}
```

c) `fill.go` 新增（放在 `pinRealityMinClientVer` 之后）：

```go
// templateSecurity 从填充后的模板推导安全层：streamSettings 含 realitySettings → reality；
// 有 streamSettings 但无 realitySettings → none；无 streamSettings（ss/socks/http/dokodemo）→ ""。
func templateSecurity(tmpl map[string]json.RawMessage) string {
	ssRaw, ok := tmpl["streamSettings"]
	if !ok {
		return ""
	}
	var ss map[string]json.RawMessage
	if err := json.Unmarshal(ssRaw, &ss); err != nil {
		return ""
	}
	if _, ok := ss["realitySettings"]; ok {
		return shared.SecurityReality
	}
	return shared.SecurityNone
}
```

d) `pickPort` 的 :0 兜底（`:345-350`）替换为 `return pickRandomFreePort(layers)`，并新增：

```go
// pickRandomFreePort 无候选时的 :0 兜底：TCP/UDP 是独立端口空间，
// OS 随机端口须指定层全部空闲（P2 修复：原只监听 tcp，ss/UDP 型协议可能撞上 UDP 占用）。
func pickRandomFreePort(layers string) (int, error) {
	for {
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			return 0, err
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if err := probePortFree(layers, port); err == nil {
			return port, nil
		}
	}
}
```

e) `endpoint.go` `renderSharedEndpointOutbound`（`:125-160`）的 stream 构造替换为按出口 realized 分派：

```go
	network := route.Target.Network
	if network == "" {
		network = shared.NetworkTCP
	}
	// 隧道段安全层跟随出口业务 inbound：reality 出口（公钥非空）→ reality；
	// ws/httpupgrade + none 出口（VLESS Encryption 认证）→ 明文传输（spec §3.2/P2）。
	stream := map[string]any{"network": network}
	if route.Target.PublicKey != "" {
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName": route.Target.ServerName, "publicKey": route.Target.PublicKey,
			"shortId": route.Target.ShortID, "fingerprint": shared.FingerprintChrome,
		}
	} else {
		stream["security"] = "none"
	}
	switch network {
	case shared.NetworkGRPC:
		stream["grpcSettings"] = map[string]any{"serviceName": route.Target.ServiceName}
	case shared.NetworkXHTTP:
		stream["xhttpSettings"] = map[string]any{
			"path": route.Target.Path, "mode": route.Target.Mode, "host": route.Target.Host,
		}
	case shared.NetworkWS:
		w := map[string]any{"path": route.Target.Path}
		if route.Target.Host != "" {
			w["headers"] = map[string]any{"Host": route.Target.Host}
		}
		stream["wsSettings"] = w
	case shared.NetworkHTTPUpgrade:
		h := map[string]any{"path": route.Target.Path}
		if route.Target.Host != "" {
			h["host"] = route.Target.Host
		}
		stream["httpupgradeSettings"] = h
	}
```

（函数其余部分——user 构造、vnext 组装、返回——保持不变；`user["encryption"]` 的既有 `route.Target.Encryption` 分支已覆盖 none+Encryption 场景。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/agent && go test ./internal/xray/ -v -run 'TestFillTemplate|TestTemplateSecurity|TestPickRandomFreePort|TestRenderSharedEndpointOutbound|TestPickPort|TestPickChainPort' && go test ./...`
Expected: PASS（含既有用例全绿）

- [ ] **Step 5: Commit**

```bash
git add src/agent/internal/xray/fill.go src/agent/internal/xray/fill_test.go src/agent/internal/xray/endpoint.go src/agent/internal/xray/endpoint_test.go
git commit -m "feat(agent): fill 提取 ws/httpupgrade/security，pickPort 兜底探测 UDP，端点路由适配明文传输"
```

---

### Task 5: sub — ws/httpupgrade/security=none 的 links/mihomo/singbox/quanx 输出

**Files:**
- Modify: `src/backend/internal/sub/links.go:26-66`（buildShareLink vless/vmess 分支）、`:79-91`（setTransportQuery）
- Modify: `src/backend/internal/sub/sub.go:790-812`（buildProxy vless/vmess 分支）、`:836-846`（applyReality 后新增 applyPlainTransport）
- Modify: `src/backend/internal/sub/singbox.go:30-36`（sbTransport 加 Headers）、`:120-135`（buildSbTLS none 返回 nil）、`:137-146`（buildSbTransport 加 ws/httpupgrade）
- Modify: `src/backend/internal/sub/quanx.go:24-60`（vless/trojan 非 reality 跳过）
- Test: `src/backend/internal/sub/links_test.go`（追加）

**Interfaces:**
- Consumes: `RealizedConfig.Security/EffectiveSecurity()`（Task 2 实现、Task 4 agent 上报）。
- Produces:
  - `func vmessTLS(rc shared.RealizedConfig) string`（links.go 包级私有）— vmess 分享 JSON 的 `tls` 字段：reality → `"reality"`，否则 `""`。
  - `func applyPlainTransport(p *clashProxy, rc shared.RealizedConfig)`（sub.go 包级私有）— security=none 节点的 mihomo 传输选项：ws → `ws-opts{path,headers.Host}`；httpupgrade → `network=ws` + `v2ray-http-upgrade: true`（mihomo 无独立 httpupgrade network）。
  - `sbTransport.Headers map[string]string \`json:"headers,omitempty"\``（sing-box ws 用）。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/sub/links_test.go` 追加：

```go
// TestBuildShareLinkWSPlain 验证 ws/httpupgrade + security=none 的分享链接：
// vmess JSON 的 tls 为空、net/host/path 正确；vless 链接无 pbk/sid/sni、带 type/encryption。
func TestBuildShareLinkWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	n := testNode("1.2.3.4", shared.ProtocolVMess)
	link, ok := buildShareLink(n, rc, "uuid")
	if !ok {
		t.Fatal("vmess ws link unsupported")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["net"] != "ws" || m["host"] != "h.example.com" || m["path"] != "/p" {
		t.Errorf("vmess ws 字段不符: %v", m)
	}
	if m["tls"] != "" {
		t.Errorf("security=none 的 vmess tls 字段应为空，实际 %q", m["tls"])
	}

	rc2 := shared.RealizedConfig{Port: 8443, Network: shared.NetworkHTTPUpgrade, Path: "/hu",
		Security: shared.SecurityNone, Encryption: "mlkem768x25519plus.0rtt.XXX"}
	link2, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc2, "uuid")
	if !ok {
		t.Fatal("vless httpupgrade link unsupported")
	}
	for _, want := range []string{"type=httpupgrade", "security=none", "encryption=mlkem768x25519plus", "path=%2Fhu"} {
		if !strings.Contains(link2, want) {
			t.Errorf("vless httpupgrade 链接缺 %q: %s", want, link2)
		}
	}
	for _, absent := range []string{"pbk=", "sid=", "sni="} {
		if strings.Contains(link2, absent) {
			t.Errorf("security=none 链接不应含 %q: %s", absent, link2)
		}
	}
}

// TestBuildProxyWSPlain 验证 mihomo 输出：security=none 无 tls/reality-opts；
// ws → ws-opts；httpupgrade → network=ws + v2ray-http-upgrade（mihomo 惯例）。
func TestBuildProxyWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.TLS || p.RealityOpts != nil || p.Servername != "" {
		t.Errorf("明文 ws 不应带 tls/reality-opts/servername: %+v", p)
	}
	if p.Network != "ws" || p.WsOpts == nil || p.WsOpts.Path != "/p" || p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Errorf("ws-opts 不符: %+v", p.WsOpts)
	}

	rc.Network = shared.NetworkHTTPUpgrade
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != "ws" || p.WsOpts == nil || !p.WsOpts.V2rayHTTPUpgrade {
		t.Errorf("httpupgrade 应映射为 network=ws + v2ray-http-upgrade: network=%q opts=%+v", p.Network, p.WsOpts)
	}
}

// TestBuildSbOutboundWSPlain 验证 sing-box 输出：security=none 无 tls 块；
// ws 用 headers.Host，httpupgrade 用原生 httpupgrade transport（host 平铺）。
func TestBuildSbOutboundWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS != nil {
		t.Errorf("security=none 不应输出 tls 块: %+v", ob.TLS)
	}
	if ob.Transport == nil || ob.Transport.Type != "ws" || ob.Transport.Path != "/p" ||
		ob.Transport.Headers["Host"] != "h.example.com" {
		t.Errorf("sing-box ws transport 不符: %+v", ob.Transport)
	}

	rc.Network = shared.NetworkHTTPUpgrade
	ob, err = buildSbOutbound(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.Transport == nil || ob.Transport.Type != "httpupgrade" || ob.Transport.Host != "h.example.com" {
		t.Errorf("sing-box httpupgrade transport 不符: %+v", ob.Transport)
	}
}

// TestQuanXSkipsPlainVLESS 验证 QuanX 对 security=none 节点尽力而为：跳过不输出。
func TestQuanXSkipsPlainVLESS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS, Security: shared.SecurityNone}
	if line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid"); line != "" {
		t.Errorf("QuanX 应跳过明文 vless，实际输出 %q", line)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/sub/ -run 'WSPlain|QuanXSkips' -v`
Expected: FAIL（现有实现硬编码 reality）

- [ ] **Step 3: 实现**

a) `links.go` vless 分支（`:27-44`）替换为：

```go
	case shared.ProtocolVLESS:
		q := url.Values{}
		q.Set("type", rc.Network)
		security := rc.EffectiveSecurity()
		q.Set("security", security)
		if security == shared.SecurityReality {
			q.Set("pbk", rc.PublicKey)
			q.Set("sid", rc.ShortID)
			q.Set("sni", rc.ServerName)
			q.Set("fp", rc.Fingerprint)
		}
		if rc.Flow != "" {
			q.Set("flow", rc.Flow)
		}
		if rc.Encryption != "" {
			q.Set("encryption", rc.Encryption)
		} else {
			q.Set("encryption", "none") // 新版客户端要求显式声明
		}
		setTransportQuery(q, rc)
		return fmt.Sprintf("vless://%s@%s?%s#%s", uuid, addr, q.Encode(), name), true
```

b) `links.go` vmess 分支（`:55-66`）：`"tls": "reality"` 改为 `"tls": vmessTLS(rc)`；同文件追加：

```go
// vmessTLS 是 vmess 分享 JSON 的 tls 字段：reality → "reality"，无安全层 → 空串。
func vmessTLS(rc shared.RealizedConfig) string {
	if rc.EffectiveSecurity() == shared.SecurityReality {
		return "reality"
	}
	return ""
}
```

c) `links.go` `setTransportQuery`（`:80-91`）switch 追加：

```go
	case shared.NetworkWS, shared.NetworkHTTPUpgrade:
		q.Set("path", rc.Path)
		if rc.Host != "" {
			q.Set("host", rc.Host)
		}
```

并更新函数注释为"写入 grpc/xhttp/ws/httpupgrade 的传输参数"。

d) `sub.go` buildProxy 的 vless 分支（`:791-798`）替换为：

```go
	case shared.ProtocolVLESS:
		p.UUID = uuid
		p.Network = rc.Network
		p.Flow = rc.Flow
		p.Encryption = rc.Encryption
		if rc.EffectiveSecurity() == shared.SecurityReality {
			p.TLS = true
			p.Servername = rc.ServerName
			applyReality(&p, rc)
		} else {
			applyPlainTransport(&p, rc)
		}
```

vmess 分支（`:799-807`）替换为：

```go
	case shared.ProtocolVMess:
		zero := 0
		p.UUID = uuid
		p.AlterID = &zero
		p.Cipher = vmessCipher(n.ConfigTemplate)
		p.Network = rc.Network
		if rc.EffectiveSecurity() == shared.SecurityReality {
			p.TLS = true
			p.Servername = rc.ServerName
			applyReality(&p, rc)
		} else {
			applyPlainTransport(&p, rc)
		}
```

`applyReality` 之后追加：

```go
// applyPlainTransport 填充 security=none 节点的传输选项（ws/httpupgrade；tcp 无选项）。
// mihomo 无独立 httpupgrade network：映射为 network=ws + ws-opts.v2ray-http-upgrade。
func applyPlainTransport(p *clashProxy, rc shared.RealizedConfig) {
	switch rc.Network {
	case shared.NetworkWS:
		opts := clashWsOpts{Path: rc.Path}
		if rc.Host != "" {
			opts.Headers = map[string]string{"Host": rc.Host}
		}
		p.WsOpts = &opts
	case shared.NetworkHTTPUpgrade:
		p.Network = "ws"
		opts := clashWsOpts{Path: rc.Path, V2rayHTTPUpgrade: true}
		if rc.Host != "" {
			opts.Headers = map[string]string{"Host": rc.Host}
		}
		p.WsOpts = &opts
	}
}
```

e) `singbox.go`：`sbTransport` 结构体（`:30-36`）在 `Host` 行后加 `Headers map[string]string \`json:"headers,omitempty"\` // ws`。`buildSbTLS`（`:120-135`）开头加守卫并更新注释：

```go
// buildSbTLS 构造 sing-box TLS 配置；security=none 返回 nil（普通 tls 变体属 P3，
// 届时按 spec §3.4 拆分为 reality/普通 tls 两个构造函数）。
func buildSbTLS(rc shared.RealizedConfig) *sbTLS {
	if rc.EffectiveSecurity() != shared.SecurityReality {
		return nil
	}
	tls := &sbTLS{ ...原样保留... }
	return tls
}
```

`buildSbTransport`（`:137-146`）switch 在 xhttp case 后追加：

```go
	case shared.NetworkWS:
		tr := &sbTransport{Type: "ws", Path: rc.Path}
		if rc.Host != "" {
			tr.Headers = map[string]string{"Host": rc.Host}
		}
		return tr
	case shared.NetworkHTTPUpgrade:
		return &sbTransport{Type: "httpupgrade", Path: rc.Path, Host: rc.Host}
```

f) `quanx.go`：vless 分支（`:25`）与 trojan 分支（`:49`）开头各加（QuanX 尽力而为，仅输出 reality 形态）：

```go
		if rc.EffectiveSecurity() != shared.SecurityReality {
			return "" // QuanX 仅输出 reality 形态（ws/httpupgrade + none 跳过，§3.4 尽力而为）
		}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/sub/ -v && go build ./...`
Expected: PASS（含既有 TestBuildShareLinkIPv6Bracket / TestVMessShareLinkCipher 等不回归）

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/sub/links.go src/backend/internal/sub/sub.go src/backend/internal/sub/singbox.go src/backend/internal/sub/quanx.go src/backend/internal/sub/links_test.go
git commit -m "feat(sub): ws/httpupgrade 与 security=none 的 links/mihomo/singbox/quanx 输出"
```

---

### Task 6: UDP 中转管道 — ForwardSpec 按出口协议分层（ss 出口 = tcp,udp）

**Files:**
- Modify: `src/shared/messages.go:400-412`（`ForwardSpec` 加 `Network`）
- Modify: `src/backend/internal/dispatch/chain.go:323-326`（ForwardSpec 构造填 Network）
- Modify: `src/backend/internal/panel/chains.go:339`（注释）、`:839-853`（`revisionTopology` hop settings 加 forward_layers）
- Modify: `src/backend/internal/store/ports.go:62-67`（chain_forward 占用按出口协议分层）
- Modify: `src/agent/internal/xray/chain.go:344-380`（renderForward 分层探测）、`:382-397`（renderForwardInbound network）、`:399-414`（pickChainPort 加 layers 参数）
- Modify: `src/agent/internal/xray/endpoint.go:49`（pickChainPort 调用补 `"tcp"`）
- Test: `src/agent/internal/xray/chain_test.go:177-191`（追加 UDP 用例）、`:295-333`（既有 pickChainPort 调用补参）、`src/backend/internal/store/ports_test.go:62-140`（追加 ss 链占用用例）

**Interfaces:**
- Produces:
  - `ForwardSpec.Network string \`json:"network,omitempty"\`` — dokodemo 管道监听层（`"tcp"`/`"tcp,udp"`）；空 = tcp（兼容旧面板载荷与 agent 本地已渲染 piece 重放）。
  - `func forwardLayers(spec *shared.ForwardSpec) string`（agent chain.go 包级私有）— 空回退 `"tcp"`。
  - `func (m *Manager) pickChainPort(preferred int, candidates []int, prev *state.ChainPiece, tag string, layers string) (int, error)` — 签名加 `layers`（endpoint/portal 调用点恒 `"tcp"`，forward 传 `forwardLayers(spec)`）。
- Consumes: `shared.PortLayers`（P1）；`shared.ForwardSpec`（本任务扩展）。

**语义**：panel 在 dispatch 阶段按出口业务节点协议推导：`spec.Network = shared.PortLayers(node.Protocol)`（ss = `tcp,udp`，其余 = `tcp`）。`revisionTopology` 把 `forward_layers` 纳入 hop settings 哈希——出口协议被编辑改变（如 trojan→ss）时 forward piece 触发重发；协议不变时 current/desired 两侧同函数计算、哈希相等，不产生误重发（存量编辑回归不受影响）。`store.PortOccupants` 的 chain_forward 占用同步按链出口节点协议分层，使 panel 前置校验与 agent 实际监听一致。

- [ ] **Step 1: 写失败测试（agent）**

`src/agent/internal/xray/chain_test.go` `TestRenderForwardInbound` 之后追加：

```go
// TestRenderForwardInboundUDPLayers 验证 forward 管道按出口协议分层：
// spec.Network="tcp,udp"（ss 出口）→ dokodemo 监听双层；空 → 回退 tcp（旧载荷兼容）。
func TestRenderForwardInboundUDPLayers(t *testing.T) {
	spec := &shared.ForwardSpec{TargetAddress: "127.0.0.1", TargetPort: 21001, Network: "tcp,udp"}
	ib := renderForwardInbound(spec, shared.ChainForwardTag(9), 11009)
	if got := nested(ib, "settings")["network"]; got != "tcp,udp" {
		t.Fatalf("ss 出口管道应监听 tcp,udp，实际 %v", got)
	}
	legacy := &shared.ForwardSpec{TargetAddress: "127.0.0.1", TargetPort: 21001}
	ib = renderForwardInbound(legacy, shared.ChainForwardTag(10), 11010)
	if got := nested(ib, "settings")["network"]; got != "tcp" {
		t.Fatalf("空 Network 应回退 tcp，实际 %v", got)
	}
}

// TestPickChainPortLayered 验证 forward 端口探测分层：UDP 被外部占用时
// layers=tcp,udp 报冲突、layers=tcp 放行。
func TestPickChainPortLayered(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	m := &Manager{}
	if _, err := m.pickChainPort(udpPort, nil, nil, "", "tcp,udp"); err == nil {
		t.Error("UDP 占用时 layers=tcp,udp 应报冲突")
	}
	if got, err := m.pickChainPort(udpPort, nil, nil, "", "tcp"); err != nil || got != udpPort {
		t.Errorf("UDP 占用不影响 tcp 层: got=%d err=%v", got, err)
	}
}
```

同时把既有 `pickChainPort` 调用点补第五个实参 `"tcp"`：`:299`、`:310`、`:325`、`:328`、`:399`（这些调用当前是 4 实参，本步骤先不管编译失败，Step 2 一并见红）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/agent && go test ./internal/xray/ -run 'TestRenderForwardInboundUDPLayers|TestPickChainPortLayered' -v`
Expected: FAIL（编译错误：pickChainPort 参数数量不符 / undefined 行为缺失）

- [ ] **Step 3: 实现（agent + shared + dispatch + panel + store）**

a) `src/shared/messages.go` `ForwardSpec`（`:403-412`）在 `ListenFamily` 行前加：

```go
	Network         string `json:"network,omitempty"`      // dokodemo 管道监听层（"tcp"/"tcp,udp"）；空 = tcp（兼容旧面板）
```

并更新结构体文档注释，加一句：`Network 由 panel 按出口业务协议推导（shared.PortLayers）：ss 出口为 tcp,udp，其余 tcp（§3.2 UDP 中转管道）。`

b) `src/agent/internal/xray/chain.go`：

- `renderForward`（`:351`）改为 `port, err := m.pickChainPort(spec.Port, spec.PortCandidates, prev, tag, forwardLayers(spec))`。
- `renderForwardInbound`（`:384-397`）的 settings 改为 `"network": forwardLayers(spec)`，并更新函数注释加"network 按出口协议分层（ss 出口 tcp,udp）"。
- `pickChainPort`（`:403-414`）签名加 `layers string`，两处 `m.pickPort(..., "tcp")` 改传 `layers`；文档注释补"layers 为监听层（endpoint/portal 恒 tcp，forward 随出口协议）"。
- 新增：

```go
// forwardLayers 返回 forward 管道的监听层（空 = tcp，兼容旧面板载荷）。
func forwardLayers(spec *shared.ForwardSpec) string {
	if spec.Network != "" {
		return spec.Network
	}
	return "tcp"
}
```

c) `src/agent/internal/xray/endpoint.go:49`：`m.pickChainPort(config.Port, portCandidates, prev, shared.SharedEndpointTag(p.EndpointID), "tcp")`（共享端点恒 vless+reality/none over tcp 系，tcp 层）。

d) `src/backend/internal/dispatch/chain.go:323-326` ForwardSpec 构造加字段（`node` 为 `:164` 已加载的出口业务节点）：

```go
		spec := &shared.ForwardSpec{
			Tag:  shared.ChainForwardTag(hop.ID),
			Port: hop.ForwardPort, // 0 = 自动（用户未指定的入口/中间跳）
			// dokodemo 管道按出口协议分层：ss 出口监听 tcp,udp（UDP 中转），其余 tcp（§3.2）。
			Network: shared.PortLayers(node.Protocol),
		}
```

e) `src/backend/internal/panel/chains.go`：

- `:339` 注释改为：`// 入口监听是 dokodemo 管道（层随出口协议：ss 为 tcp,udp，其余 tcp）；vless 入口走共享端点合并，跳过前置校验。`（调用代码不变——`req.Node.Protocol` 经 `PortLayers` 推导的层与新管道语义一致。）
- `revisionTopology`（`:839-853`）：函数开头解析出口协议，hop settings map 加 `forward_layers`：

```go
func revisionTopology(revisionID int64, snapshot store.ChainRevisionSnapshot) dispatch.RevisionTopology {
	hops := make([]dispatch.RevisionHopSpec, 0, len(snapshot.Hops))
	// UDP 中转管道分层（§3.2）：出口协议纳入 hop settings 哈希——协议编辑变更
	// （如 trojan→ss）时触发 forward piece 重发以切换 tcp/tcp,udp；协议不变时
	// current/desired 同函数计算，哈希相等不误重发。
	var svc struct {
		Protocol string `json:"protocol"`
	}
	_ = json.Unmarshal(snapshot.ServiceConfig, &svc)
	forwardLayers := shared.PortLayers(svc.Protocol)
	for index, hop := range snapshot.Hops {
		settings, _ := json.Marshal(map[string]any{
			"tunnel_uuid":    hop.TunnelUUID,
			"local_only":     index == 0 && snapshot.EndpointID != 0,
			"address":        hop.Address, // 地址引用（§9）：选择变更须触发本跳 piece 重发并沿下游哈希传播
			"forward_layers": forwardLayers,
		})
		hops = append(hops, dispatch.RevisionHopSpec{HopID: hop.HopID, ServerID: hop.ServerID,
			Transport: hop.Transport, ListenPort: hop.ForwardPort, Settings: settings})
	}
	return dispatch.RevisionTopology{RevisionID: revisionID, ServiceID: snapshot.ServiceNodeID,
		Service: snapshot.ServiceConfig, Hops: hops,
		DirectShared: snapshot.EndpointID != 0 && len(snapshot.Hops) == 1}
}
```

f) `src/backend/internal/store/ports.go`：chain_forward 查询（`:62-67`）改为按链出口节点协议分层（fixedLayers 传 `""`，走 `PortLayers`）：

```go
	// 链路逐跳 forward：dokodemo 管道按出口业务协议分层（ss 出口 tcp,udp，其余 tcp，§3.2）；
	// 服务节点缺失（异常数据）回退 vless=tcp 保持保守。portal 为 vless+reality，恒 tcp。
	if err := appendRows(`SELECT h.forward_port, COALESCE(n.protocol, 'vless'), c.id, c.name
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		LEFT JOIN nodes n ON n.id=c.service_node_id
		WHERE h.server_id=? AND h.forward_port>0`, "chain_forward", "", serverID); err != nil {
		return nil, fmt.Errorf("query forward occupants: %w", err)
	}
```

- [ ] **Step 4: 写失败测试（store）→ 实现已含 → 验证**

`src/backend/internal/store/ports_test.go` 的 `TestPortOccupantsChainSources`：在软删链 fixture 之后追加一条 ss 出口的活链并断言分层：

```go
	// ss 出口链：forward 管道应按出口协议标记 tcp,udp（§3.2 UDP 中转管道）。
	ssChain, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "链SS", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolShadowsocks,
		ServiceConfig: json.RawMessage(`{"protocol":"shadowsocks"}`), TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: entryID, Role: HopRoleEntry, Transport: "reverse", ForwardPort: 21003, TunnelUUID: "tunnel-c"},
			{ServerID: exitID, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
```

occupants 断言段追加：

```go
	ssFwd, ok := byPort[21003]
	if !ok || ssFwd.Source != "chain_forward" || ssFwd.Layers != "tcp,udp" || ssFwd.ChainID != ssChain.ChainID {
		t.Errorf("ss 出口链 forward 应为 tcp,udp 双层: %+v", ssFwd)
	}
```

Run: `cd src/backend && go test ./internal/store/ -run TestPortOccupants -v && go test ./... && go build ./...`
Expected: PASS（该用例在 Step 3-f 前会先红：layers 为 tcp——可按 TDD 顺序先加测试跑红再改 store/ports.go）

- [ ] **Step 5: 全量验证 + Commit**

Run: `cd src/agent && go test ./... && cd ../backend && go test ./... && cd ../shared && go test ./...`
Expected: 全绿

```bash
git add src/shared/messages.go src/backend/internal/dispatch/chain.go src/backend/internal/panel/chains.go src/backend/internal/store/ports.go src/backend/internal/store/ports_test.go src/agent/internal/xray/chain.go src/agent/internal/xray/endpoint.go src/agent/internal/xray/chain_test.go
git commit -m "feat(agent,panel): UDP 中转管道——dokodemo 按出口协议分层（ss 出口 tcp,udp）"
```

---

### Task 7: frontend — 链路表单 ws/httpupgrade 传输

**Files:**
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts:27`（NETWORKS）、`:324-333`（onProtocolChange 纠偏）、`:388-443`（onSubmit 映射）、新增 onNetworkChange
- Modify: `src/frontend/src/pages/chains/ChainFormDialog.tsx:390-534`（isReality 区块：network onValueChange、ws/httpupgrade 输入块、明文时隐藏 reality 字段）

**Interfaces:**
- Consumes: 后端 `createNodeRequest`（Task 3 后 ws/httpupgrade 合法；security 由后端按 network 推导，前端不传 security 字段——`CreateNodeRequest` 手写类型无需变更）；生成的 VirtualConfig 类型含 `security`（Task 3 契约）。
- Produces: `onNetworkChange(value: string | null)`（`ChainFormController` 导出成员，对话框解构使用）。`ChainFormState` 无新增字段（path/host/mode/serviceName 复用；security 由 network 推导）。

- [ ] **Step 1: 常量与纠偏（use-chain-form.ts）**

a) `:27` 的 NETWORKS 改为：

```ts
// 与后端 shared 包保持一致（ws/httpupgrade 为 security=none 明文传输；reality 仅 tcp/grpc/xhttp）。
export const NETWORKS = ['tcp', 'xhttp', 'grpc', 'ws', 'httpupgrade']
```

b) 新增明文传输判定与 network 切换纠偏（放在 `onProtocolChange` 之前），并修改 `onProtocolChange`：

```ts
/** ws/httpupgrade 为明文传输（security=none，无 reality 字段）。 */
export function isPlainNetwork(network: string): boolean {
  return network === 'ws' || network === 'httpupgrade'
}

const onNetworkChange = (value: string | null) => {
  if (!value) return
  setForm((current) => ({
    ...current,
    network: value,
    // 跨传输纠偏：vision flow 仅 tcp+reality
    flow: value === 'tcp' ? current.flow : 'none',
    // vless 明文传输必须有 VLESS Encryption 兜底（后端矩阵，前端即时纠正）
    encryption:
      current.protocol === 'vless' && isPlainNetwork(value) && current.encryption === 'none'
        ? 'mlkem768'
        : current.encryption,
  }))
}
```

`onProtocolChange`（`:324-333`）的 setForm 内追加一行 encryption 纠偏（vless + 明文传输时不允许 none）：

```ts
  setForm((current) => ({
    ...current,
    protocol: value,
    // 跨协议纠偏：flow/encryption 仅 vless 有意义
    flow: value === 'vless' ? current.flow : 'none',
    encryption:
      value === 'vless'
        ? isPlainNetwork(current.network) && current.encryption === 'none'
          ? 'mlkem768'
          : current.encryption
        : 'none',
  }))
```

return 对象加 `onNetworkChange`。

c) `onSubmit`（`:396-429` 的 `if (isReality)` 块）替换为：

```ts
    if (isReality) {
      nodeBody.network = form.network
      if (isPlainNetwork(form.network)) {
        // ws/httpupgrade = security=none（后端按 network 推导）：不带 reality 字段
        nodeBody.path = form.path.trim() || '/'
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
        }
        if (form.protocol === 'trojan') {
          setCreateError('trojan 使用 ws/httpupgrade 传输需 TLS 安全层（将在后续版本提供）')
          return
        }
        if (form.protocol === 'vless' && form.encryption === 'none') {
          setCreateError('vless 使用 ws/httpupgrade 传输时必须启用 VLESS Encryption')
          return
        }
      } else {
        nodeBody.fingerprint = form.fingerprint
        if (form.shortId.trim()) {
          nodeBody.short_id = form.shortId.trim()
        }
        if (form.dest.trim()) {
          nodeBody.dest = form.dest.trim()
        }
        const names = form.serverNames
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
        if (names.length > 0) {
          nodeBody.server_names = names
        }
      }
      if (form.network === 'xhttp') {
        nodeBody.path = form.path.trim() || '/'
        nodeBody.mode = form.mode
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
        }
      }
      if (form.network === 'grpc') {
        nodeBody.service_name = form.serviceName.trim() || 'grpc'
      }
      if (form.protocol === 'vless') {
        // vision 仅 tcp；xhttp/ws/httpupgrade 必须无 flow；vision + Encryption 允许组合（§15）
        nodeBody.flow = form.network === 'tcp' ? form.flow : 'none'
        if (form.encryption !== 'none') {
          nodeBody.encryption = form.encryption
        }
      }
    }
```

d) 编辑回填（`openEdit` `:275-304`）无需新增字段：`network/path/host/encryption` 已从 `virtual.*` 回填，ws/httpupgrade 链路编辑自动正确。

- [ ] **Step 2: 对话框渲染（ChainFormDialog.tsx）**

a) 从 controller 解构加 `onNetworkChange`（`:145-161`）；import 加 `isPlainNetwork`。

b) 组件内派生（`serverSelectItems` 定义之后）：

```tsx
  const plainNetwork = isPlainNetwork(form.network)
```

c) 传输选择器（`:392-406`）的 `onValueChange={(v) => v && patch({ network: v })}` 改为 `onValueChange={onNetworkChange}`。

d) grpc 输入块（`:444-454`）之后插入 ws/httpupgrade 输入块：

```tsx
              {plainNetwork && (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="plainPath">
                      {form.network === 'ws' ? 'WS path' : 'HTTPUpgrade path'}
                    </Label>
                    <Input
                      id="plainPath"
                      value={form.path}
                      onChange={(e) => patch({ path: e.target.value })}
                      placeholder="/"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="plainHost">
                      {form.network === 'ws' ? 'WS host（可空）' : 'HTTPUpgrade host（可空）'}
                    </Label>
                    <Input
                      id="plainHost"
                      value={form.host}
                      onChange={(e) => patch({ host: e.target.value })}
                      placeholder="留空不设置"
                    />
                  </div>
                  <p className="cg-chain-hint">
                    ws/httpupgrade 为明文传输（security=none），适合套 CDN；vless 需启用 VLESS
                    Encryption，trojan 暂不支持（需 TLS，后续版本提供）。
                  </p>
                </>
              )}
```

e) 明文传输时隐藏 reality 专有字段：把 uTLS 指纹块（`:497-514`）、shortId 块（`:515-523`）、RealityDestPicker（`:524-532`）三段各自用 `{!plainNetwork && (...)}` 包裹。flow 块（`:476-496`，条件已是 `form.network === 'tcp'`）与 Encryption 块（`:455-475`，vless 全传输都需要）保持不变。

- [ ] **Step 3: 验证**

Run: `cd src/frontend && npm run lint && npm test && npm run build`
Expected: 全绿（本目录无 vitest 用例，lint+build 为准）

- [ ] **Step 4: Commit**

```bash
git add src/frontend/src/pages/chains/
git commit -m "feat(frontend): 链路表单支持 ws/httpupgrade 传输（明文安全层 + 矩阵纠偏）"
```

---

### Task 8: e2e — ws/httpupgrade 用例 + UDP 管道断言 + 全量存量回归

**Files:**
- Modify: `scripts/e2e/protocols.sh`（vlessenc 门控、ws/httpupgrade 节点、矩阵 400 用例、订阅断言与计数）
- Modify: `scripts/e2e/chains.sh`（ss 链 UDP 管道断言、vmess-ws 中继真实流量、vless-httpupgrade 中继真实流量、删链计数）

- [ ] **Step 1: protocols.sh — vlessenc 门控 + ws/httpupgrade 节点**

a) `:11-12` 的 XRAY_BIN 检查之后加（沿用 links.sh 模式）：

```bash
# VLESS Encryption 需带 vlessenc 子命令的 xray（§15）；缺失时跳过 vless+httpupgrade 用例。
HAS_VLESSENC=true
"$XRAY_BIN" vlessenc >/dev/null 2>&1 || HAS_VLESSENC=false
```

b) `:165-167` 的"vmess cipher"段之后追加：

```bash
echo ">> vmess ws（security=none 明文传输）"
R="$(create_node '{"server_id":1,"protocol":"vmess","network":"ws","path":"/wsp","host":"cdn.example.com"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="ws" and rc["path"]=="/wsp" and rc["host"]=="cdn.example.com" and rc.get("public_key","")=="" and rc.get("security")=="none", rc' "$R" && check_port "$R"

echo ">> vless httpupgrade + VLESS Encryption（security=none 须带 Encryption，§2 脚注 1）"
if [[ "$HAS_VLESSENC" == "true" ]]; then
    R="$(create_node '{"server_id":1,"protocol":"vless","network":"httpupgrade","path":"/hu","encryption":"mlkem768"}')"
    python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="httpupgrade" and rc["path"]=="/hu" and rc.get("security")=="none" and rc.get("encryption","").startswith("mlkem768x25519plus."), rc' "$R" && check_port "$R"
else
    echo "SKIP: xray 缺 vlessenc 子命令，跳过 vless+httpupgrade 用例"
fi

echo ">> 矩阵外组合 400（reality×ws / trojan×ws / vless+ws 无 Encryption / ss 带传输层 / tls 未开放）"
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","network":"ws","security":"reality"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"trojan","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vless","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"shadowsocks","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls"}'
echo "   矩阵外组合均被 400 拦截 OK"
```

c) 订阅断言段（`:194-213`）追加与计数调整：

```bash
check "ws-opts:"
if [[ "$HAS_VLESSENC" == "true" ]]; then
    check "v2ray-http-upgrade: true"
fi
LINKS_OUT="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=links" | base64 -d)"
grep -q "type=ws" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 type=ws"; echo "$LINKS_OUT"; exit 1; }
if [[ "$HAS_VLESSENC" == "true" ]]; then
    grep -q "type=httpupgrade" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 type=httpupgrade"; echo "$LINKS_OUT"; exit 1; }
fi
```

`PROXY_COUNT` 断言（`:211-212`）改为：

```bash
PROXY_COUNT="$(grep -c 'server: ' <<<"$SUB")"
EXPECTED=12
[[ "$HAS_VLESSENC" == "true" ]] && EXPECTED=13
[[ "$PROXY_COUNT" -eq "$EXPECTED" ]] || { echo "FAIL: 订阅应有 $EXPECTED 个代理（dokodemo 除外），实际 $PROXY_COUNT"; echo "$SUB"; exit 1; }
echo "   $EXPECTED 个代理项、ws/httpupgrade 字段 OK，dokodemo 已排除"
```

- [ ] **Step 2: chains.sh — ss 链 UDP 管道断言**

在存量编辑回归段（`:305-317`）之后、删链段（`:319`）之前插入：

```bash
echo ">> UDP 中转管道：forward inbound 按出口协议分层"
CH1_HOP="$(chain_field "$CH1" "c['hops'][0]['id']")"
CH2_HOP="$(chain_field "$CH2" "c['hops'][0]['id']")"
python3 - "$XRAY_CONFIG_A" "$CH1_HOP" "$CH2_HOP" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
def fwd(tag):
    return next((i for i in cfg["inbounds"] if i.get("tag") == tag), None)
vless_fwd = fwd(f"chainfwd_{sys.argv[2]}")
ss_fwd = fwd(f"chainfwd_{sys.argv[3]}")
assert vless_fwd and vless_fwd["settings"].get("network") == "tcp", vless_fwd
assert ss_fwd and ss_fwd["settings"].get("network") == "tcp,udp", ss_fwd
PY
echo "OK: vless 链管道 tcp / ss 链管道 tcp,udp"
# ss 链入口 UDP 层确在监听：同号 UDP 端口绑定应冲突
python3 -c "import socket; socket.socket(socket.AF_INET, socket.SOCK_DGRAM).bind(('0.0.0.0', $BLOCK_PORT))" \
    2>/dev/null && { echo "FAIL: ss 链入口 UDP $BLOCK_PORT 未被监听"; exit 1; } \
    || echo "OK: ss 链入口 UDP 层已监听（绑定冲突证明）"
```

- [ ] **Step 3: chains.sh — vmess-ws 中继链真实流量**

紧接 Step 2 插入块之后：

```bash
echo ">> 链3（vmess+ws 中继，A=入口、C=出口）→ active → 真实流量"
CHAIN3="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vmess\",\"network\":\"ws\",\"path\":\"/wsp\"}}")"
CH3="$(py "d['id']" "$CHAIN3")"
NID3="$(py "d['hops'][-1]['node_id']" "$CHAIN3")"
wait_chain "$CH3" active 90 && echo "OK: vmess+ws 中继链 active"
ENTRY3_PORT="$(chain_field "$CH3" "c['hops'][0]['forward_port']")"
[[ "$ENTRY3_PORT" != "0" && -n "$ENTRY3_PORT" ]] || { echo "FAIL: 链3 入口端口"; exit 1; }
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH1,$CH3]}" >/dev/null
# 订阅断言：vmess 链条目 network=ws + ws-opts、无 reality-opts
SUB3=""
for _ in $(seq 1 20); do
    SUB3="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
    grep -q "type: vmess" <<<"$SUB3" && break
    sleep 0.5
done
python3 - "$SUB3" "$ENTRY3_PORT" "$UUID1" <<'PY'
import sys, yaml
doc = yaml.safe_load(sys.argv[1])
port, uuid = int(sys.argv[2]), sys.argv[3]
vm = next(p for p in doc["proxies"] if p["type"] == "vmess")
assert vm["network"] == "ws" and vm["port"] == port and vm["uuid"] == uuid, vm
assert vm.get("ws-opts", {}).get("path") == "/wsp", vm
assert "reality-opts" not in vm and not vm.get("tls"), vm
PY
echo "OK: 订阅 vmess+ws 条目（入口端口/用户 UUID/ws-opts/无 reality）"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-ws.json" "$ENTRY3_PORT" "$UUID1" <<'PY'
import json, sys
path, port, uuid = sys.argv[1], int(sys.argv[2]), sys.argv[3]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11810,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vmess",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "security": "auto"}]}]},
        "streamSettings": {"network": "ws", "security": "none",
                           "wsSettings": {"path": "/wsp"}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-ws.json" >/dev/null || { echo "FAIL: ws 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-ws.json" >"$WORK/client-ws.log" 2>&1 &
WSXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11810" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $WSXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vmess+ws 中继链路 200（client→入口管道→reverse 隧道→出口）" \
    || { echo "FAIL: vmess+ws 链路未通"; tail -n 5 "$WORK/client-ws.log"; exit 1; }
fi
```

（本机已验证 `python3 -c "import yaml"` 可用，直接用 yaml 断言版本；若部署到无 pyyaml 的环境，退回 grep 三件套：`grep -q "type: vmess"` + `grep -q "ws-opts:"` + `grep -q "path: /wsp"`。）

- [ ] **Step 4: chains.sh — vless+httpupgrade 中继链真实流量（共享端点 + Encryption 路径）**

vlessenc 门控（脚本头部 XRAY_BIN 检查后加 `HAS_VLESSENC` 探测，同 protocols.sh Step 1-a）。紧接 Step 3 之后：

```bash
if [[ "$HAS_VLESSENC" == "true" ]]; then
echo ">> 链4（vless+httpupgrade+Encryption 中继，共享端点入口）→ active → 真实流量"
CHAIN4="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\",\"network\":\"httpupgrade\",\"path\":\"/hu\",\"encryption\":\"mlkem768\"}}")"
CH4="$(py "d['id']" "$CHAIN4")"
wait_chain "$CH4" active 90
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH4" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP4_PORT="$(chain_field "$CH4" "c['entry_port']")"
EP4_ID="$(chain_field "$CH4" "c['endpoint_id']")"
[[ -n "$EP4_PORT" && "$EP4_PORT" != "0" ]] || { echo "FAIL: 链4 端点未就绪: $(chain_field "$CH4" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH4" active 30
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH1,$CH3,$CH4]}" >/dev/null
ACCESS_UUID4=""
for _ in $(seq 1 15); do
    ACCESS_UUID4="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=[a for a in (u.get('chain_assignments') or []) if a.get('chain_id')==$CH4]
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID4" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID4" ]] || { echo "FAIL: 未取到链4 assignment"; exit 1; }
EP4_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP4_ID")"
EP4_ENC="$(py "d.get('encryption') or ''" "$EP4_RC")"
[[ "$EP4_ENC" == mlkem768x25519plus.* ]] || { echo "FAIL: 链4 端点 encryption 缺失: $EP4_RC"; exit 1; }
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-hu.json" "$EP4_PORT" "$ACCESS_UUID4" "$EP4_ENC" <<'PY'
import json, sys
path, port, uuid, enc = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11811,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": enc}]}]},
        "streamSettings": {"network": "httpupgrade", "security": "none",
                           "httpupgradeSettings": {"path": "/hu"}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hu.json" >/dev/null || { echo "FAIL: httpupgrade 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hu.json" >"$WORK/client-hu.log" 2>&1 &
HUXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11811" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HUXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless+httpupgrade 中继链路 200（client→端点→加密隧道→出口）" \
    || { echo "FAIL: vless+httpupgrade 链路未通"; tail -n 5 "$WORK/client-hu.log"; exit 1; }
fi
else
    echo "SKIP: xray 缺 vlessenc，跳过链4（vless+httpupgrade）"
fi
```

- [ ] **Step 5: chains.sh — 删链段计数更新**

删链段（`:319-342`）：在删 CH2/CH1 之前加删 CH3（与 CH4，若创建）；`chain-hop.remove` 计数 6 改为按链数计算：

```bash
rpc_data POST /api/chain/delete "{\"chain_id\":$CH3}" >/dev/null
[[ "$HAS_VLESSENC" == "true" ]] && rpc_data POST /api/chain/delete "{\"chain_id\":$CH4}" >/dev/null
```

```bash
EXPECT_REMOVE=9
[[ "$HAS_VLESSENC" == "true" ]] && EXPECT_REMOVE=12
[[ "$(rpc_data GET /api/chain/list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')" == "0" ]] \
    && echo "OK: 链行已消失" || { echo "FAIL: 链列表非空"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(db "SELECT COUNT(*) FROM commands WHERE type IN ('chain-hop.remove','node.remove') AND status != 'acked'")" == "0" ]] && break
    sleep 1
done
[[ "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove'")" == "$EXPECT_REMOVE" \
&& "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove' AND status='acked'")" == "$EXPECT_REMOVE" ]] \
    && echo "OK: chain-hop.remove 全部 acked（$EXPECT_REMOVE 件）" \
    || { echo "FAIL: chain-hop.remove 未全部 acked"; db "SELECT type,status FROM commands"; exit 1; }
```

注意同时把 cleanup() 的 pkill 列表加上两个新客户端配置（`$WORK/client-ws.json`、`$WORK/client-hu.json`），并kill新增变量 WSXPID/HUXPID（脚本内已即时 kill，cleanup 兜底）。

- [ ] **Step 6: 全量 e2e 回归（存量链路不失效的证明）**

```bash
export XRAY_BIN=/usr/local/bin/xray
bash scripts/e2e/protocols.sh    # 全协议 + ws/httpupgrade + 矩阵 400 + 端口冲突 + cipher
bash scripts/e2e/chains.sh       # 链生命周期 + 存量编辑回归 + UDP 管道 + ws/httpupgrade 真实流量
rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist
bash scripts/e2e/links.sh        # 订阅/分享链接输出一致性（需前端 dist 就位）
LATX_ALLOW_PRIVATE_OUTBOUND=1 bash scripts/e2e/groups.sh   # 分组订阅与派生链
bash scripts/e2e/usernodes.sh    # 按用户节点操作
bash scripts/e2e/reconcile.sh    # 漂移自愈
bash scripts/e2e/vlessenc.sh     # vless Encryption 数据面
```

Expected: 每个脚本结尾均输出对应 `PASS`。任一失败即回退对应 Task 排查，不允许带病提交。

- [ ] **Step 7: 全仓回归 + Commit**

```bash
cd src/shared && go test ./... && cd ../backend && go test ./... && cd ../agent && go test ./...
cd ../frontend && npm run lint && npm test && npm run build
git add scripts/e2e/protocols.sh scripts/e2e/chains.sh
git commit -m "test(e2e): protocols 覆盖 ws/httpupgrade 与矩阵 400；chains 覆盖 UDP 管道与 ws/httpupgrade 中继真实流量"
```

---

## 自检记录

- **Spec 覆盖**：P2 范围 = ws/httpupgrade 全链路（Task 2 常量 → Task 3 panel 模板/normalize → Task 4 agent 提取 → Task 5 订阅 → Task 7 前端）+ UDP 中转管道（Task 6：ForwardSpec.Network / renderForwardInbound / pickChainPort 分层 / PortOccupants 分层 / revisionTopology 触发重发）+ P1 已知缺口 pickPort :0 兜底不探测 UDP（Task 4-d）+ P1 终审 Important#1（Task 1）+ 存量回归红线（Global Constraints + Task 8）。TLS 安全层与 hy2 属 P3/P4，不在本计划。
- **矩阵落地**：normalize 拒绝矩阵外组合并指明冲突字段（Task 3 测试逐项覆盖 reality×ws、trojan×ws、vless+ws 无 Encryption、ss 带 network、security=tls、vision×none）；前端镜像纠偏（Task 7）。
- **占位符扫描**：无 TBD/"适当处理"；全部测试与实现代码完整给出，无执行分叉（Task 4 的 reality 模板测试已复用现成桩 `newRebuildTestManager` + `destReachable` 包级变量替换，见 `rebuild_test.go:17/92-94`）。
- **类型一致性**：`NetworkWS/NetworkHTTPUpgrade/RealityNetworks/Security*`（Task 2）↔ Task 3/4/5/7 引用一致；`EffectiveSecurity()`（Task 2）↔ links/sub/singbox/quanx（Task 5）一致；`templateSecurity`（Task 4）返回 `shared.SecurityReality/SecurityNone`；`ForwardSpec.Network`/`forwardLayers`/`pickChainPort` 5 参签名（Task 6）↔ 全部调用点（endpoint.go:49、chain.go:191/351）已列出；`networkSubSettings/plainStreamSettings`（Task 3）↔ buildVirtualConfig 接线一致；`applyPlainTransport/vmessTLS/sbTransport.Headers`（Task 5）↔ 测试断言一致。
- **存量兼容**：旧 realized（无 security）经 `EffectiveSecurity` 回退 reality；旧 ForwardSpec（无 network）经 `forwardLayers` 回退 tcp；`revisionTopology` 双测同函数计算不误重发；reality 路径模板结构逐字段未变（Task 3 重构仅提取共用函数，realityStreamSettings 输出 key 集不变——由既有 e2e 全绿背书）。
