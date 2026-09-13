# xray 全协议暴露 P3（TLS 安全层：自签 + ACME 双证书模式）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 TLS 安全层两证书模式——自签（伪装域名 + 证书 pin）与 ACME（落地服务器域名，agent 自装 acme.sh 签发）——贯通 panel 模板（tlsStreamSettings + 证书占位符）→ agent 填充（`xray tls cert` / acme.sh）→ 订阅（pin/insecure/普通 TLS）→ 前端（安全层选择器 + TLS 区域）；顺带打包 P2 终审遗留清理（7 项，分布见 Task 1 说明）。**硬性要求（沿用 P1/P2 红线）：存量链路（reality/tcp/grpc/xhttp 组合、ws/httpupgrade 明文链、dokodemo 转发链、reverse/encrypted 隧道链）全部不失效，以完整 e2e 回归证明。**

**Architecture:** 沿用"panel 模板 → agent 填充 → xray run -test → 热更新/重启回退"管线。TLS 以 `security=tls` 贯通 VirtualConfig/RealizedConfig/订阅三侧：模板 `tlsSettings.certificates` 引用新占位符 `{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}`，agent 按 `VirtualConfig.CertMode`（selfsign/acme）在 `config/certs/<tag>/` 落地证书（幂等复用，不轮换），自签 pin（证书 DER 的 sha256 hex）与 SNI 随 `RealizedConfig.SNI/CertSHA256` 上报；链隧道段（共享端点 outbound）以 xray 26.x 的 `pinnedPeerCertSha256`（hex 字符串）钉住自签出口证书，ACME 走系统根验证。

**Tech Stack:** Go（backend/agent/shared 三个 module，go.work workspace）、React+TS（vite/vitest/oxlint）、SQLite（modernc.org/sqlite）、OpenAPI 契约 `docs/openapi.yaml` → 前端生成类型。acme.sh 是 agent 在目标机上安装的 shell 脚本，不是 Go 依赖。

**Spec:** `docs/superpowers/specs/2026-09-12-xray-full-protocol-exposure-design.md`（§2 矩阵 tls 列、§3.1 shared、§3.2 tlsStreamSettings、§3.3 证书策略、§3.4 agent、§3.5 订阅、§4 前端 TLS 区域、§5 错误处理、§6 测试、§7 第 3 条）

**关键已验证事实**（本机 xray 26.3.27 实测，作为实现依据）：

- `xray tls cert -domain=<d> -name=<d> -file=<prefix>` 生成自签证书 `<prefix>.crt` / `<prefix>.key`（SAN 含 domain；不加 `-name` 时 CN 默认为 "Xray Inc"）。xray CLI 不支持"CA 签发下级证书"（`-ca` 仅生成 CA 自身），故自签模式用单张自签证书兼作服务器证书，pin 该证书——spec §3.3"自签 CA + 服务器证书"的语义由 pin 保证，不依赖 CA 链。
- xray 26.x 客户端 tlsSettings 的 `allowInsecure` **已移除**，迁移为 `pinnedPeerCertSha256`（**单数、hex 字符串**，非 base64、非数组）。实测：正确 pin 数据面 200、错误 pin 拒绝。因此 `RealizedConfig.CertSHA256` 一律存 **小写 hex**——xray 客户端 pin 与 mihomo `fingerprint` 订阅字段两处直接共用，无需转码。

## Global Constraints

- 不新增任何第三方依赖（Go 与前端均如此）；acme.sh 由 agent 在目标机首次使用 ACME 模式时按需自安装（官方安装脚本），不进 install 脚本、不是 Go module 依赖。复用现有 requester/状态机/管线基础设施（AGENTS.md）。
- 协议/传输/安全层/证书模式常量必须与 `src/shared/config.go` 保持一致，前端常量注释沿用"与后端 shared 包保持一致"。
- 后端 API 字段变更必须同步改 `docs/openapi.yaml` 并在 `src/frontend` 跑 `npm run generate:api`（`npm run build` 内含 `--check` 会拦截不一致）。
- **P3 合法组合矩阵**（spec §2 + normalize 兼容性规则；矩阵外一律 400 并指明冲突字段）：
  - vless/vmess/trojan × tcp/grpc/xhttp × reality —— 存量，不得改变。
  - vmess/vless(带 Encryption) × ws/httpupgrade × none —— P2 存量，不得改变。
  - vless/vmess/trojan × 全部五种传输 × tls —— **本期新增合法**（含 trojan×ws/httpupgrade×tls，P2 的 400 引导分支移除）。
  - trojan × security=none —— 仍非法（trojan 不允许 none）。
  - reality × ws/httpupgrade —— 仍非法（xray 官方约束）。
  - vision flow 仅 vless + tcp + (reality|tls)（P2 为 reality-only，本期扩展 tls）；tls+tcp 不默认 vision（默认 vision 仍仅 reality）。
  - security=tls 时 reality 专有字段（short_id/dest/server_names）无意义一律清空；fingerprint 保留（tls 客户端同样下发 uTLS 指纹）。
  - cert_mode 仅 security=tls 有效：selfsign（默认，tls_domain 留空=预设池随机，可自定义伪装域）/ acme（tls_domain 由 panel 从落地服务器公网地址检测填充，用户输入忽略）。
  - ss/socks/http/dokodemo 无传输/安全层选项，显式传 network/security → 400（不变）。
- 证书文件落在 agent `<config 目录>/certs/<inbound tag>/`（node_*/shared_endpoint_* 各自隔离），0700；`PurgeXray`/`ResetForPanelRebind`（`manager.go:66-84, 181-190`）本就不触碰该目录，保持不删（证书随重装/换绑保留，spec §3.3）。
- ACME 证书模式只在落地（出口/直连）服务器上生效：域名来源 = `servers.addresses` JSON 列表中首个 `shared.AddressFamily(a) == "domain"` 的条目（store.ParseServerAddresses 解析）；无域名 panel 前置 400。
- Go 源文件与前端 TS/TSX 文件均为 CRLF 行尾，编辑时保持；shell 脚本与 openapi.yaml 为 LF。
- 提交信息沿用仓库惯例：`type(scope): 中文摘要`。
- 每个 Task 完成后运行对应验证命令，全绿才提交。
- **存量回归红线**：任何 Task 不得改变既有链路在 reality/none 组合下的模板结构、端口分配与订阅输出；Task 7 的全量 e2e 回归（含"编辑存量链路原样保存"用例）必须全绿才算 P3 完成。
- 前端不要重装依赖（main 仓 `src/frontend/node_modules` 已装好；若必须装，用 `npm install --legacy-peer-deps`）。
- ws/grpc/httpupgrade 弃用警告不阻断（xray 仅 warning）；ACME 不做 DNS-01 与通配符；e2e 不做真实 LE 签发（只覆盖到"域名自检失败"路径，spec §6）。

验证命令速查：
- shared: `cd src/shared && go test ./...`
- backend: `cd src/backend && go test ./...`
- agent: `cd src/agent && go test ./...`
- frontend: `cd src/frontend && npm run generate:api && npm test && npm run lint && npm run build`
- e2e（最后统一跑，前置 `export XRAY_BIN=/usr/local/bin/xray`；links.sh 需先 `rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist`；groups.sh 需 `LATX_ALLOW_PRIVATE_OUTBOUND=1`；chains.sh 含外网真实流量）:
  `bash scripts/e2e/protocols.sh` 等 7 个脚本（见 Task 7）。

---

### Task 1: P2 遗留清理（机械项：NETWORKS 顺序 / vmess 空键 / endpoint 收敛 / encryption 保留合并）

**Files:**
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts:27-28`（NETWORKS 顺序与注释）
- Modify: `src/backend/internal/sub/links.go:58-69`（vmess 分享 JSON 空键省略）
- Modify: `src/agent/internal/xray/endpoint.go:41-52`（decryption 预替换收敛到 `preserveTemplate`）
- Modify: `src/backend/internal/store/endpoints.go:139-176`（`SetSharedEndpointActive` encryption 保留合并）
- Test: `src/backend/internal/sub/links_test.go`（追加）、`src/backend/internal/store/endpoints_test.go`（追加）

**Interfaces:**
- Consumes: `preserveTemplate(vc shared.VirtualConfig, prev json.RawMessage) string`（`rebuild.go:40-50`，同包）；既有 `SetSharedEndpointActive(ctx, id, realized)` 签名不变。
- Produces: `func mergeEndpointRealized(next, prev json.RawMessage) json.RawMessage`（store 包级私有）— next 无 encryption 且 prev 有则沿用 prev 值。

**说明（P2 终审 7 项的分布）**：#1（normalize tls 400 移除）在 Task 3、#2（trojan mihomo tls/none 分流）与 #5 的 tls 部分在 Task 5、#3（前端 trojan×ws 放行）在 Task 6——这三项依赖 P3 的 tls 字段/模板/选择器，无法独立于本期功能机械落地；本任务只含 4 个完全独立的机械项（#4 顺序对齐、#5 空键省略、#6 同构收敛、#7 保留合并），每项均带终审裁定。

- [ ] **Step 1: 前端 NETWORKS 顺序对齐 shared.Networks（清理 #4）**

`src/frontend/src/pages/chains/use-chain-form.ts:27-28` 替换为：

```ts
// 与后端 shared 包保持一致（顺序即 shared.Networks：tcp/grpc/xhttp 为 reality 兼容传输，
// ws/httpupgrade 支持 tls/none 安全层）。
export const NETWORKS = ['tcp', 'grpc', 'xhttp', 'ws', 'httpupgrade']
```

（NETWORKS 仅用于渲染选择器选项顺序，纯展示变更，无行为差异。）

- [ ] **Step 2: links.go vmess 分享 JSON 省略空值键（清理 #5，先写失败测试）**

`src/backend/internal/sub/links_test.go` 追加：

```go
// TestVMessShareLinkOmitsEmptySecurityKeys 验证 v2rayN 惯例：security=none 的 vmess
// 分享 JSON 不携带空串 sni/fp/pbk/sid 键（P2 遗留清理 #5）。
func TestVMessShareLinkOmitsEmptySecurityKeys(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	link, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if !ok {
		t.Fatal("vmess link unsupported")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sni", "fp", "pbk", "sid"} {
		if _, exists := m[k]; exists {
			t.Errorf("security=none 不应携带空键 %q: %v", k, m)
		}
	}
	if m["net"] != "ws" || m["host"] != "h.example.com" || m["path"] != "/p" {
		t.Errorf("传输字段回归不符: %v", m)
	}
	// reality 回归：四个键必须仍在且非空。
	rc2 := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP,
		Security: shared.SecurityReality, PublicKey: "pk", ShortID: "sid", ServerName: "dl.google.com"}
	link2, _ := buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc2, "uuid")
	raw2, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link2, "vmess://"))
	var m2 map[string]string
	if err := json.Unmarshal(raw2, &m2); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sni", "fp", "pbk", "sid"} {
		if m2[k] == "" {
			t.Errorf("reality 应携带非空 %q: %v", k, m2)
		}
	}
}
```

Run: `cd src/backend && go test ./internal/sub/ -run TestVMessShareLinkOmitsEmptySecurityKeys -v`
Expected: FAIL（当前实现恒携带空串键）

实现：`src/backend/internal/sub/links.go:58-69` 的 vmess 分支替换为：

```go
	case shared.ProtocolVMess:
		// vmess 分享为 base64(JSON)；空值的 sni/fp/pbk/sid 键按 v2rayN 惯例省略（P2 遗留清理）。
		port := fmt.Sprintf("%d", rc.Port)
		fields := map[string]string{
			"v": "2", "ps": name, "add": n.ServerAddress, "port": port,
			"id": uuid, "aid": "0", "scy": vmessCipher(n.ConfigTemplate),
			"net": rc.Network, "type": "none",
			"host": rc.Host, "path": rc.Path,
			"tls": vmessTLS(rc),
		}
		if rc.EffectiveSecurity() == shared.SecurityReality {
			fields["sni"] = rc.ServerName
			fields["fp"] = rc.Fingerprint
			fields["pbk"] = rc.PublicKey
			fields["sid"] = rc.ShortID
		}
		j, _ := json.Marshal(fields)
		return "vmess://" + base64.StdEncoding.EncodeToString(j), true
```

Run: `cd src/backend && go test ./internal/sub/ -v && go build ./...`
Expected: PASS（既有 TestBuildShareLinkWSPlain 等不回归——其断言不含空键存在性）

- [ ] **Step 3: endpoint.go decryption 预替换收敛到 preserveTemplate（清理 #6）**

背景：`ApplySharedEndpoint`（`endpoint.go:41-52`）手工预替换 `{{PRIVATE_KEY}}/{{DECRYPTION}}` 两个占位符，与 `rebuild.go:40-50` 的 `preserveTemplate` 语义同构。可机械收敛的依据：`state.ChainPiece` 的 `PrivateKey` 字段在写入时即 `endpointPrivateKey(inbound)`（`endpoint.go:65,79`——从同一条 Inbound 提取），二者必然一致；`preserveTemplate` 从 `prev.Inbound` 提取等价值，语义不变（prev.Inbound 为空时 extractPrevInbound 返回零值、不做替换，与现状 `prev.PrivateKey==""` 跳过等价）。

`endpoint.go:42-51` 的两个密钥预替换块（`if prev.PrivateKey != "" {...}` 与 `if _, _, decryption := extractPrevInbound(...); ...`）整体替换为：

```go
		// 密钥对保留（重建同构，rebuild.go preserveTemplate）：占位符预替换后
		// fillTemplate 不再轮换 Reality/VLESS Encryption 密钥对，既有订阅不失效。
		config.Template = json.RawMessage(preserveTemplate(config, prev.Inbound))
```

（`:39-41` 的端口保留块与外层 `if prev != nil` 大括号保持不变。）

Run: `cd src/agent && go test ./internal/xray/ -v && go build ./...`
Expected: PASS（行为保持，由既有 ApplySharedEndpoint 密钥保留用例背书，不新增测试）

- [ ] **Step 4: SetSharedEndpointActive encryption 保留合并（清理 #7，先写失败测试）**

背景：端点重部署（路由变化/重连自愈）时 agent 回写的 realized 若不含 encryption（非 Encryption 模板或旧 agent），当前整体覆盖 `realized_config` 会清空库中已有 encryption，订阅 vless 链接的 encryption 字段丢失。

`src/backend/internal/store/endpoints_test.go` 追加（夹具沿用本文件既有模式：`Open(":memory:")` + `CreateServer`）：

```go
// TestSetSharedEndpointActivePreservesEncryption 验证清理 #7：新一轮 realized 未携带
// encryption 时沿用库中已生效值，不清空（订阅 encryption 字段依赖）。
func TestSetSharedEndpointActivePreservesEncryption(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "10.0.0.9",
		BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 14433,
		"hash-enc", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointApplying(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID,
		json.RawMessage(`{"port":14433,"encryption":"mlkem768x25519plus.0rtt.XXX"}`)); err != nil {
		t.Fatal(err)
	}
	// 重部署回写不含 encryption → 库中值必须保留。
	if err := st.SetSharedEndpointApplying(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, json.RawMessage(`{"port":14433}`)); err != nil {
		t.Fatal(err)
	}
	ep, err := st.SharedEndpointByID(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rc struct {
		Encryption string `json:"encryption"`
	}
	if err := json.Unmarshal(ep.RealizedConfig, &rc); err != nil {
		t.Fatal(err)
	}
	if rc.Encryption != "mlkem768x25519plus.0rtt.XXX" {
		t.Errorf("encryption 应保留，实际 %q（realized=%s）", rc.Encryption, ep.RealizedConfig)
	}
}
```

Run: `cd src/backend && go test ./internal/store/ -run TestSetSharedEndpointActivePreservesEncryption -v`
Expected: FAIL（当前整体覆盖清空 encryption）

实现：`src/backend/internal/store/endpoints.go` `SetSharedEndpointActive` 的端口冲突检查之后、`UPDATE` 之前插入：

```go
	// 保留合并（P2 遗留清理）：新一轮 realized 未携带 encryption（非 Encryption 模板重发
	// 或旧 agent 回写）时沿用库中已生效值，避免清空订阅依赖的 encryption 字段。
	var prevRealized string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(realized_config,'') FROM shared_endpoints WHERE id=?`, id).Scan(&prevRealized); err != nil {
		return err
	}
	realized = mergeEndpointRealized(realized, json.RawMessage(prevRealized))
```

同文件追加：

```go
// mergeEndpointRealized 回写端点 realized 时保留既有 encryption：新值为空且旧值非空则沿用。
func mergeEndpointRealized(next, prev json.RawMessage) json.RawMessage {
	var n, p struct {
		Encryption string `json:"encryption"`
	}
	if json.Unmarshal(next, &n) != nil || n.Encryption != "" {
		return next
	}
	if json.Unmarshal(prev, &p) != nil || p.Encryption == "" {
		return next
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(next, &m) != nil {
		return next
	}
	enc, _ := json.Marshal(p.Encryption)
	m["encryption"] = enc
	merged, err := json.Marshal(m)
	if err != nil {
		return next
	}
	return merged
}
```

Run: `cd src/backend && go test ./internal/store/ -v -run 'SharedEndpoint' && go test ./... && go build ./...`
Expected: PASS（既有端点状态机/FSM 用例不回归）

- [ ] **Step 5: Commit**

```bash
git add src/frontend/src/pages/chains/use-chain-form.ts src/backend/internal/sub/links.go src/backend/internal/sub/links_test.go src/agent/internal/xray/endpoint.go src/backend/internal/store/endpoints.go src/backend/internal/store/endpoints_test.go
git commit -m "fix: P2 遗留清理——NETWORKS 顺序对齐、vmess 省略空键、endpoint 收敛 preserveTemplate、端点 encryption 保留合并"
```

---

### Task 2: shared — CertMode/TLSDomain/SNI/CertSHA256 字段 + TLS 证书占位符

**Files:**
- Modify: `src/shared/config.go:48-56`（Security 注释更新）、`:197-216`（占位符区块）、`:221-237`（VirtualConfig）、`:249-265`（RealizedConfig）
- Test: `src/shared/config_test.go`（追加）

**Interfaces:**
- Produces:
  - `const CertModeSelfSign = "selfsign"`、`CertModeACME = "acme"`；`var CertModes = []string{selfsign, acme}`。
  - `const PlaceholderTLSCertFile = "{{TLS_CERT_FILE}}"`、`PlaceholderTLSKeyFile = "{{TLS_KEY_FILE}}"`（纯字符串替换，agent 填证书绝对路径）。
  - `VirtualConfig.CertMode string \`json:"cert_mode,omitempty"\``（仅 security=tls 有效；空 = selfsign）。
  - `VirtualConfig.TLSDomain string \`json:"tls_domain,omitempty"\``（selfsign=伪装域名；acme=panel 检测填充的落地域名）。
  - `RealizedConfig.SNI string \`json:"sni,omitempty"\``（tls 的 serverName；reality 场景继续用 ServerName，不动）。
  - `RealizedConfig.CertSHA256 string \`json:"cert_sha256,omitempty"\``（自签证书 DER 的 sha256 **hex**；ACME 留空——公共 CA 走系统根验证）。

- [ ] **Step 1: 写失败测试**

`src/shared/config_test.go` 追加：

```go
func TestCertModes(t *testing.T) {
	if !ValidValue(CertModeSelfSign, CertModes) || !ValidValue(CertModeACME, CertModes) {
		t.Error("CertModes 应包含 selfsign/acme")
	}
}

// TestTLSConfigFieldsRoundTrip 验证 TLS 证书字段 JSON 往返（panel 模板落库与
// agent realized 上报共用同一结构体，键名是面板/agent 契约）。
func TestTLSConfigFieldsRoundTrip(t *testing.T) {
	vc := VirtualConfig{Protocol: ProtocolVMess, Security: SecurityTLS,
		CertMode: CertModeSelfSign, TLSDomain: "www.example.com"}
	b, err := json.Marshal(vc)
	if err != nil {
		t.Fatal(err)
	}
	var back VirtualConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.CertMode != CertModeSelfSign || back.TLSDomain != "www.example.com" {
		t.Errorf("VirtualConfig TLS 字段往返不符: %+v", back)
	}
	rc := RealizedConfig{Port: 443, Security: SecurityTLS, SNI: "www.example.com",
		CertSHA256: strings.Repeat("ab", 32)}
	b, err = json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	var rcBack RealizedConfig
	if err := json.Unmarshal(b, &rcBack); err != nil {
		t.Fatal(err)
	}
	if rcBack.SNI != "www.example.com" || len(rcBack.CertSHA256) != 64 {
		t.Errorf("RealizedConfig TLS 字段往返不符: %+v", rcBack)
	}
	if PlaceholderTLSCertFile != "{{TLS_CERT_FILE}}" || PlaceholderTLSKeyFile != "{{TLS_KEY_FILE}}" {
		t.Error("TLS 证书占位符与 spec §3.1 不一致")
	}
}
```

（`config_test.go` 当前仅 import `testing`，需补 `encoding/json` 与 `strings`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/shared && go test ./... -run 'TestCertModes|TestTLSConfigFieldsRoundTrip' -v`
Expected: FAIL（undefined: CertModeSelfSign 等）

- [ ] **Step 3: 实现**

a) `src/shared/config.go:48` 的安全层注释更新为：

```go
// 安全层（security）：reality（仅 tcp/grpc/xhttp）/ tls（自签或 ACME 证书，§3.3）/ none。
```

b) 占位符区块（`:201-216`）在 `PlaceholderTag` 后追加：

```go
	// PlaceholderTLSCertFile/PlaceholderTLSKeyFile TLS 证书文件路径占位符（仅
	// security=tls 模板含有）：Agent 按 CertMode 落地证书（selfsign=`xray tls cert`
	// 自签 / acme=acme.sh 签发）后以绝对路径纯字符串替换（§3.3）。
	PlaceholderTLSCertFile = "{{TLS_CERT_FILE}}"
	PlaceholderTLSKeyFile  = "{{TLS_KEY_FILE}}"
```

占位符区块文档注释（`:197-200`）末句"PRIVATE_KEY/TAG 为纯字符串替换"改为"PRIVATE_KEY/TAG/TLS_CERT_FILE/TLS_KEY_FILE 为纯字符串替换"。

c) 证书模式常量（放在 Securities 声明之后）：

```go
// 证书模式（cert_mode）：仅 security=tls 有效。
// selfsign=伪装域名自签 + 证书 pin（默认）；acme=落地服务器域名 + acme.sh 真实签发（§3.3）。
const (
	CertModeSelfSign = "selfsign"
	CertModeACME     = "acme"
)

// CertModes 是向导可选的全部证书模式。
var CertModes = []string{CertModeSelfSign, CertModeACME}
```

d) `VirtualConfig`（`:226` 的 `Security` 行后）追加：

```go
	CertMode      string             `json:"cert_mode,omitempty"`  // security=tls：selfsign（默认）/acme
	TLSDomain     string             `json:"tls_domain,omitempty"` // selfsign=伪装域名；acme=落地服务器域名（panel 检测填充）
```

同时把 `Security` 字段注释改为 `// reality/tls/none；空 = 按 network 推导`。

e) `RealizedConfig`（`:257` 的 `Security` 行后）追加：

```go
	SNI         string `json:"sni,omitempty"`        // security=tls 的 serverName（伪装域名/落地域名）
	CertSHA256  string `json:"cert_sha256,omitempty"` // 自签证书 DER 的 sha256 hex（订阅 pin / xray pinnedPeerCertSha256）；ACME 留空
```

并把 `Security` 注释改为 `// 生效安全层（reality/tls/none）`。

f) `EffectiveSecurity` 文档注释（`:267-268`）更新：旧 realized 回退推导只覆盖 reality/none（tls 是本期新数据，必然显式携带），语义不变，仅注释说明。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/shared && go test ./... && cd ../backend && go build ./... && cd ../agent && go build ./...`
Expected: PASS + 两模块编译通过（纯新增字段/常量，不破坏既有调用）

- [ ] **Step 5: Commit**

```bash
git add src/shared/config.go src/shared/config_test.go
git commit -m "feat(shared): TLS 证书模式常量、SNI/CertSHA256 字段与证书占位符"
```

---

### Task 3: panel — normalize tls 矩阵 + tlsStreamSettings + ACME 域名检测 + 契约（含清理 #1）

**Files:**
- Modify: `src/backend/internal/panel/nodes.go:90`（createNodeRequest 加字段）、`:135-137`（移除 tls 400 引导，**清理 #1**）、`:166-191`（security 三分支）、`:217-219`（vision 扩展 tls）、`:527-533`（streamSettings 接线）、`:543-558`（VirtualConfig 返回）、文件末尾（tlsStreamSettings/serverDomain/applyACMEDomain/tlsCamouflagePool/validateTLSDomain/randomInt）
- Modify: `src/backend/internal/panel/nodes.go:265-273`（handleCreateNode 接 applyACMEDomain）
- Modify: `src/backend/internal/panel/chains.go:301` 后（handleCreateChain 接 applyACMEDomain）、`:586-589` 后（handleEditChain 接 applyACMEDomain）
- Modify: `docs/openapi.yaml:919`（VirtualConfig schema 加 `cert_mode`/`tls_domain`）
- Modify: `src/frontend/src/lib/api-contract.generated.ts`（`npm run generate:api` 产物）
- Test: `src/backend/internal/panel/nodes_test.go`（追加）

**Interfaces:**
- Consumes: `shared.CertMode*/CertModes/PlaceholderTLSCertFile/PlaceholderTLSKeyFile`（Task 2）；`store.ParseServerAddresses`（`store/servers.go:319`）；`shared.AddressFamily/AddressFamilyDomain`（`src/shared/address.go:11-27`）。
- Produces:
  - `createNodeRequest.CertMode string \`json:"cert_mode"\``、`TLSDomain string \`json:"tls_domain"\``。
  - `func tlsStreamSettings(req createNodeRequest) map[string]any` — tls 安全层 streamSettings（tlsSettings.serverName=TLSDomain + certificates 引用占位符路径 + networkSubSettings 传输子段）。
  - `func serverDomain(srv *store.Server) string` — 服务器公网地址列表首个域名条目（无则空串）。
  - `func applyACMEDomain(req *createNodeRequest, srv *store.Server) error` — acme 模式校验并填充 TLSDomain；无域名返回指向性错误（非 acme 直通 nil）。
  - `func validateTLSDomain(d string) error`、`func randomInt(n int) int`（伪装域名池选取）、`var tlsCamouflagePool []string`。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/panel/nodes_test.go` 追加：

```go
// TestNormalizeTLSMatrix 验证 P3 tls 矩阵（含清理 #1：tls 400 引导分支移除）：
// vless/vmess/trojan × 全部传输 × tls 合法；trojan×ws 不显式给 tls 仍 400（推导 none）；
// vision 扩展为 reality|tls；cert_mode 缺省 selfsign、tls_domain 留空随机填充。
func TestNormalizeTLSMatrix(t *testing.T) {
	// 合法：trojan+ws+tls（P2 的 400 组合，本期开放），cert_mode 缺省 selfsign、域名随机
	req := &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS, Security: shared.SecurityTLS}
	if err := req.normalize(); err != nil {
		t.Fatalf("trojan+ws+tls 应合法: %v", err)
	}
	if req.CertMode != shared.CertModeSelfSign || req.TLSDomain == "" {
		t.Errorf("tls 默认值不符: cert_mode=%q tls_domain=%q", req.CertMode, req.TLSDomain)
	}
	if req.ShortID != "" || req.Dest != "" || len(req.ServerNames) != 0 {
		t.Errorf("tls 应清空 reality 专有字段: %+v", req)
	}
	if req.Fingerprint != shared.FingerprintChrome {
		t.Errorf("tls 应保留/默认 fingerprint: %q", req.Fingerprint)
	}
	// 合法：vmess+tcp+tls 自定义伪装域名
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, TLSDomain: "cdn.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+tcp+tls 应合法: %v", err)
	}
	if req.TLSDomain != "cdn.example.com" || req.Flow != "" {
		t.Errorf("自定义伪装域名不符: %+v", req)
	}
	// 合法：vless+tcp+tls+vision（§2：vision 仅 tcp+reality|tls）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityTLS, Flow: shared.FlowVision}
	if err := req.normalize(); err != nil {
		t.Fatalf("vless+tcp+tls+vision 应合法: %v", err)
	}
	// 非法：trojan+ws 不显式给 security（推导 none，trojan 不允许 none）
	req = &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("trojan+ws 推导 none 应 400")
	}
	// 非法：cert_mode 未知值
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, CertMode: "bogus"}
	if err := req.normalize(); err == nil {
		t.Error("cert_mode=bogus 应 400")
	}
	// 非法：伪装域名含端口/路径
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, TLSDomain: "evil.com:443"}
	if err := req.normalize(); err == nil {
		t.Error("伪装域名含端口应 400")
	}
	// acme：normalize 清空用户输入（域名由处理器从服务器地址检测填充）
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS,
		CertMode: shared.CertModeACME, TLSDomain: "user-input.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("acme 模式 normalize 应合法: %v", err)
	}
	if req.TLSDomain != "" {
		t.Errorf("acme 模式 normalize 应清空 tls_domain（由 applyACMEDomain 填充），实际 %q", req.TLSDomain)
	}
	// 回归：vision+none 仍 400；ss 显式 security=tls 仍 400（无安全层选项）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityNone,
		Encryption: shared.VLessEncMLKEM768, Flow: shared.FlowVision}
	if err := req.normalize(); err == nil {
		t.Error("vision+none 应 400")
	}
	req = &createNodeRequest{Protocol: shared.ProtocolShadowsocks, Security: shared.SecurityTLS}
	if err := req.normalize(); err == nil {
		t.Error("ss 显式 security 应 400")
	}
}

// TestBuildVirtualConfigTLS 验证 tlsStreamSettings 模板形状：tlsSettings.serverName
// 为 TLS 域名，certificates 引用占位符路径（agent 落地后替换，§3.2/§3.3）。
func TestBuildVirtualConfigTLS(t *testing.T) {
	req := createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS,
		Security: shared.SecurityTLS, CertMode: shared.CertModeSelfSign,
		TLSDomain: "cdn.example.com", Path: "/p", Host: "h.example.com"}
	vc := buildVirtualConfig(req)
	if vc.Security != shared.SecurityTLS || vc.CertMode != shared.CertModeSelfSign || vc.TLSDomain != "cdn.example.com" {
		t.Errorf("VirtualConfig TLS 字段不符: %+v", vc)
	}
	var tmpl struct {
		StreamSettings struct {
			Network     string `json:"network"`
			Security    string `json:"security"`
			TLSSettings struct {
				ServerName   string `json:"serverName"`
				Certificates []struct {
					CertificateFile string `json:"certificateFile"`
					KeyFile         string `json:"keyFile"`
				} `json:"certificates"`
			} `json:"tlsSettings"`
			WsSettings struct {
				Path string `json:"path"`
			} `json:"wsSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl); err != nil {
		t.Fatal(err)
	}
	ss := tmpl.StreamSettings
	if ss.Network != "ws" || ss.Security != "tls" || ss.TLSSettings.ServerName != "cdn.example.com" {
		t.Errorf("tls streamSettings 不符: %+v", ss)
	}
	if len(ss.TLSSettings.Certificates) != 1 ||
		ss.TLSSettings.Certificates[0].CertificateFile != shared.PlaceholderTLSCertFile ||
		ss.TLSSettings.Certificates[0].KeyFile != shared.PlaceholderTLSKeyFile {
		t.Errorf("证书占位符不符: %+v", ss.TLSSettings.Certificates)
	}
	if ss.WsSettings.Path != "/p" {
		t.Errorf("ws 传输子段不符: %+v", ss.WsSettings)
	}
}

// TestApplyACMEDomain 验证 ACME 域名检测（§3.3 模式 B + §5 错误处理）：
// 落地服务器公网地址无域名条目 → 指向性错误；有 → 沿用该域名填充 TLSDomain。
func TestApplyACMEDomain(t *testing.T) {
	noDomain := &store.Server{Alias: "nat01", Addresses: `["1.2.3.4","2400:cb00::1"]`}
	req := &createNodeRequest{Security: shared.SecurityTLS, CertMode: shared.CertModeACME}
	if err := applyACMEDomain(req, noDomain); err == nil {
		t.Error("无域名服务器的 acme 模式应报错")
	} else if !strings.Contains(err.Error(), "未设置域名") {
		t.Errorf("错误信息应指向域名配置: %v", err)
	}
	withDomain := &store.Server{Alias: "hk01", Addresses: `["1.2.3.4","exit.example.com"]`}
	if err := applyACMEDomain(req, withDomain); err != nil {
		t.Fatal(err)
	}
	if req.TLSDomain != "exit.example.com" {
		t.Errorf("应沿用落地服务器域名，实际 %q", req.TLSDomain)
	}
	// 非 acme 直通（selfsign 的 TLSDomain 不被触碰）
	req = &createNodeRequest{Security: shared.SecurityTLS, CertMode: shared.CertModeSelfSign, TLSDomain: "cdn.example.com"}
	if err := applyACMEDomain(req, noDomain); err != nil || req.TLSDomain != "cdn.example.com" {
		t.Errorf("selfsign 应直通: err=%v domain=%q", err, req.TLSDomain)
	}
}
```

（`nodes_test.go` 若无 `strings`/`store` import 则补上；既有 `encoding/json` 已在。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/panel/ -run 'TestNormalizeTLSMatrix|TestBuildVirtualConfigTLS|TestApplyACMEDomain' -v`
Expected: FAIL（tls 被 400 引导 / undefined: tlsStreamSettings 等）

- [ ] **Step 3: 实现**

a) `createNodeRequest`（`nodes.go:90` 的 `Security` 行后）加：

```go
	Security      string   `json:"security"`       // reality（默认推导）/ tls / none
	CertMode      string   `json:"cert_mode"`      // security=tls：selfsign（默认）/ acme
	TLSDomain     string   `json:"tls_domain"`     // selfsign=伪装域名（留空随机）；acme=落地服务器域名（处理器检测填充）
```

结构体文档注释（`:76-79`）中"security 仅 reality 系协议有效（reality/none，tls 属 P3）"改为"security 仅 reality 系协议有效（reality/tls/none）；tls 的 cert_mode/tls_domain 见 §3.3 证书策略"。

b) 移除 tls 400 引导（**清理 #1**）：删除 `:135-137` 整块：

```go
		if req.Security == shared.SecurityTLS {
			return fmt.Errorf("security=tls 将在 P3 阶段提供，当前请选择 reality 或 none")
		}
```

c) security 分支（`:166-191` 的 `if req.Security == shared.SecurityReality {...} else {...}`）整体替换为三分支：

```go
		switch req.Security {
		case shared.SecurityReality:
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
		case shared.SecurityTLS:
			// reality 专有字段无意义一律清空；fingerprint 保留（tls 客户端同样下发 uTLS 指纹）。
			req.ShortID, req.Dest, req.ServerNames = "", "", nil
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
				// acme：域名由处理器从落地服务器公网地址检测填充（applyACMEDomain），用户输入忽略。
				req.TLSDomain = ""
			}
		default: // none
			// security=none：Reality 专有字段无意义，一律清空；并按协议执行矩阵约束。
			req.ShortID, req.Dest, req.ServerNames, req.Fingerprint = "", "", nil, ""
			if req.Protocol == shared.ProtocolTrojan {
				return fmt.Errorf("trojan 不允许 security=none（protocol 与 security 冲突；trojan 需 reality 或 tls）")
			}
			if req.Protocol == shared.ProtocolVLESS && req.Encryption == "" {
				return fmt.Errorf("vless 在 security=none 下必须启用 VLESS Encryption（security 与 encryption 冲突）")
			}
		}
```

d) vision 校验（`:217-219`）替换为：

```go
		if req.Flow == shared.FlowVision && req.Security == shared.SecurityNone {
			return fmt.Errorf("flow=%s 与 security=none 冲突：vision 仅 reality/tls（§2）", shared.FlowVision)
		}
```

（`:205` 的默认 vision 条件保持 reality-only：tls+tcp 不默认 vision，用户显式选择才启用。）

e) streamSettings 接线（`:527-533`）替换为：

```go
	if shared.IsRealityProtocol(req.Protocol) {
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

f) `buildVirtualConfig` 返回字面量（`:548` 的 `Security: req.Security,` 行后）加：

```go
		CertMode:    req.CertMode,
		TLSDomain:   req.TLSDomain,
```

g) 文件末尾（`networkSubSettings` 之后）追加：

```go
// tlsStreamSettings 构造 tls 安全层的 streamSettings（§3.2）：serverName 为 TLS 域名
// （selfsign=伪装域名 / acme=落地服务器域名），证书文件路径为占位符，由 agent 按
// CertMode 落地后替换（§3.3）；传输子段与 reality/none 共用 networkSubSettings。
func tlsStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  req.Network,
		"security": "tls",
		"tlsSettings": map[string]any{
			"serverName": req.TLSDomain,
			"certificates": []map[string]any{{
				"certificateFile": shared.PlaceholderTLSCertFile,
				"keyFile":         shared.PlaceholderTLSKeyFile,
			}},
		},
	}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// tlsCamouflagePool 是自签证书的伪装域名预设池（§3.3 模式 A，复用 RealityDestPicker
// 预设思路）：仅作 TLS 伪装身份（证书 CN/SAN 与客户端 SNI），不要求指向本机。
var tlsCamouflagePool = []string{
	"dl.google.com", "www.amazon.com", "gateway.icloud.com", "developer.apple.com",
	"cdn.discord.com", "github.com", "www.samsung.com", "www.tesla.com",
	"www.bing.com", "www.yahoo.com",
}

// validateTLSDomain 校验伪装域名形态：纯主机名（不含端口/路径/空白，长度 ≤253）。
func validateTLSDomain(d string) error {
	if d == "" || len(d) > 253 || strings.ContainsAny(d, "/: \t\r\n") {
		return fmt.Errorf("伪装域名不合法: %q（应为纯域名，如 www.example.com）", d)
	}
	return nil
}

// randomInt 返回 [0,n) 的均匀随机数（伪装域名池选取；拒绝采样去偏，
// crypto/rand 失败与 randomHex 同语义 panic）。
func randomInt(n int) int {
	var b [1]byte
	limit := 256 / n * n
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// serverDomain 返回服务器公网地址列表中的首个域名条目（无则空串）。
func serverDomain(srv *store.Server) string {
	for _, a := range store.ParseServerAddresses(srv.Addresses) {
		if shared.AddressFamily(a) == shared.AddressFamilyDomain {
			return a
		}
	}
	return ""
}

// applyACMEDomain 校验并填充 ACME 模式的 TLS 域名（§3.3 模式 B；§4 前端镜像同一判定）：
// 落地服务器公网地址须含域名条目，无则 400 并给出指向性提示；有则沿用该域名。
func applyACMEDomain(req *createNodeRequest, srv *store.Server) error {
	if req.Security != shared.SecurityTLS || req.CertMode != shared.CertModeACME {
		return nil
	}
	d := serverDomain(srv)
	if d == "" {
		return fmt.Errorf("落地服务器 %s 未设置域名，请先在服务器地址中配置域名或改用自签模式", srv.Alias)
	}
	req.TLSDomain = d
	return nil
}
```

（`nodes.go` import 需补 `crypto/rand`——注意与 `panel.go:675` 的 randomHex 同包共用，若 nodes.go 已间接可见无需重复 import 的仅为包级；`rand` 与 `strings`、`store` 已 import。）

h) 三处处理器接线 applyACMEDomain：

- `nodes.go` `handleCreateNode`：`srv` 加载成功（`:265-273`）之后、端口校验之前插入：

```go
	// ACME 证书模式：落地服务器须有域名型公网地址（§3.3 模式 B，无则 400）。
	if err := applyACMEDomain(&req, srv); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```

- `chains.go` `handleCreateChain`：`entrySrv, exitSrv := servers[0], servers[len(servers)-1]`（`:301`）之后插入：

```go
	// ACME 证书模式：落地（出口）服务器须有域名型公网地址（§3.3 模式 B，无则 400）。
	if err := applyACMEDomain(&req.Node, exitSrv); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```

- `chains.go` `handleEditChain`：dokodemo 出口校验（`:586-589`）之后插入：

```go
	// ACME 证书模式：落地（出口）服务器须有域名型公网地址（镜像创建路径）。
	if err := applyACMEDomain(&req.Node, servers[len(servers)-1]); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```

i) `docs/openapi.yaml` VirtualConfig schema（`:919` 的 `cipher:` 行后）加：

```yaml
        cert_mode: {type: string}
        tls_domain: {type: string}
```

j) 重新生成前端契约类型：`cd src/frontend && npm run generate:api`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/panel/ -v -run 'Normalize|VirtualConfig|StreamSettings|ACMEDomain' && go test ./... && cd ../frontend && npm run check:api`
Expected: PASS + 契约一致（既有 TestNormalizeTransportSecurityMatrix 中"security=tls 应 400"用例须同步更新——该用例改为断言合法，见下）

注意：P2 的 `TestNormalizeTransportSecurityMatrix`（`nodes_test.go`）含"非法：security=tls（P3 才开放）"用例（`req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS}` 断言报错）——本期 tls 合法化后该子用例必须改为断言合法（`vmess+tls` 合法、cert_mode 默认 selfsign），与清理 #1 同语义。同步更新该测试。

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/nodes.go src/backend/internal/panel/nodes_test.go src/backend/internal/panel/chains.go docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts
git commit -m "feat(panel): security=tls 矩阵开放与 tlsStreamSettings 模板、ACME 落地域名检测"
```

---

### Task 4: agent — 证书落地（自签/ACME）+ fillTemplate TLS 占位符 + 端点路由 tls pin

**Files:**
- Create: `src/agent/internal/xray/cert.go`
- Modify: `src/agent/internal/xray/fill.go:37-56` 后（TLS 占位符填充块）、`:105-129`（probe 加 TLSSettings）、`:141-157`（realized 加 SNI/CertSHA256）、`:244-259`（templateSecurity 识别 tls）
- Modify: `src/agent/internal/xray/endpoint.go:148-159`（renderSharedEndpointOutbound 安全层分派加 tls 分支）
- Test: `src/agent/internal/xray/cert_test.go`（新建）、`src/agent/internal/xray/fill_test.go`（追加）、`src/agent/internal/xray/endpoint_test.go`（追加）

**Interfaces:**
- Consumes: `shared.PlaceholderTLSCertFile/PlaceholderTLSKeyFile/CertMode*`（Task 2）；`downloadFile(url, path string) error`（`upgrade.go:129`，同包复用——requester 防护的外部下载）；`probePortFree`（`fill.go:405`，同包）。
- Produces:
  - `func (m *Manager) ensureTLSCertificate(tag string, vc shared.VirtualConfig) (certFile, keyFile, pin string, err error)` — 幂等（文件在则复用，不轮换）；selfsign 返回 pin（hex），acme 返回空 pin。
  - `func (m *Manager) issueACMECertificate(domain, certFile, keyFile string) error` — acme.sh 安装 → 域名自检 → 80 端口自检 → 签发 → 安装。
  - 测试缝（包级变量）：`execTLSCert`、`lookupHostIPs`、`localIfaceAddrs`、`acmePort80Free`、`execACMESh`。
  - `templateSecurity` 行为扩展：streamSettings.security=="tls" → `shared.SecurityTLS`。
  - `fillTemplate` 上报的 `RealizedConfig` 新增 `SNI/CertSHA256`（Task 5 订阅与端点路由依赖）。

**证书路径布局**：`<dir(configPath)>/certs/<tag>/cert.pem` + `key.pem`（node 为 `node_<id>`，共享端点为 `shared_endpoint_<id>`，天然隔离）；acme.sh 安装至 `<dir(dir(configPath))>/acme/`（与 config/ 平级，即安装根的 `acme/`）。

- [ ] **Step 1: 写失败测试（cert_test.go）**

新建 `src/agent/internal/xray/cert_test.go`：

```go
package xray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/shared"
)

// writeTestCert 用 Go 标准库生成测试自签证书（execTLSCert 测试桩的落盘实现，
// §6：fill 测试自签证书生成 mock——桩掉 xray 外部命令，证书本身真实可解析）。
func writeTestCert(t *testing.T, domain, prefix string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certOut, err := os.Create(prefix + ".crt")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	certOut.Close()
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyOut, err := os.Create(prefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	keyOut.Close()
}

// stubTLSCert 把 execTLSCert 替换为本地证书生成，返回调用计数（幂等复用断言用）。
func stubTLSCert(t *testing.T) *int {
	t.Helper()
	calls := new(int)
	orig := execTLSCert
	execTLSCert = func(bin, domain, prefix string) error {
		*calls++
		writeTestCert(t, domain, prefix)
		return nil
	}
	t.Cleanup(func() { execTLSCert = orig })
	return calls
}

// TestEnsureTLSCertificateSelfSign 验证自签模式：调用 xray tls cert 落地证书、
// 返回绝对路径与 hex pin；二次调用幂等复用（不再调用外部命令，不轮换 pin）。
func TestEnsureTLSCertificateSelfSign(t *testing.T) {
	calls := stubTLSCert(t)
	m, _ := newRebuildTestManager(t)
	vc := shared.VirtualConfig{Security: shared.SecurityTLS,
		CertMode: shared.CertModeSelfSign, TLSDomain: "www.example.com"}
	certFile, keyFile, pin, err := m.ensureTLSCertificate("node_1", vc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 64 {
		t.Errorf("pin 应为 64 位 hex，实际 %q", pin)
	}
	if !filepath.IsAbs(certFile) || !strings.Contains(certFile, "certs/node_1/") {
		t.Errorf("证书路径布局不符: %s", certFile)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("私钥未落地: %v", err)
	}
	certFile2, _, pin2, err := m.ensureTLSCertificate("node_1", vc)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || certFile2 != certFile || pin2 != pin {
		t.Errorf("二次调用应幂等复用: calls=%d pin=%q→%q", *calls, pin, pin2)
	}
}

// TestACMEDomainSelfCheck 验证 ACME 域名自检（§5：域名解析不含本机 → 指向性错误）。
func TestACMEDomainSelfCheck(t *testing.T) {
	origLookup, origAddrs := lookupHostIPs, localIfaceAddrs
	t.Cleanup(func() { lookupHostIPs, localIfaceAddrs = origLookup, origAddrs })

	// /32 使 ParseCIDR 返回的 ipNet.IP 即主机地址本身（/24 会得到网络地址 .0，与本机 .7 不匹配）。
	_, ipNet, _ := net.ParseCIDR("203.0.113.7/32")
	localIfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{ipNet}, nil }

	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("198.51.100.9")}, nil }
	if err := acmeDomainSelfCheck("exit.example.com"); err == nil ||
		!strings.Contains(err.Error(), "不含本机地址") {
		t.Errorf("解析不含本机应报指向性错误: %v", err)
	}
	lookupHostIPs = func(string) ([]net.IP, error) { return nil, &net.DNSError{IsNotFound: true} }
	if err := acmeDomainSelfCheck("exit.example.com"); err == nil ||
		!strings.Contains(err.Error(), "解析失败") {
		t.Errorf("解析失败应报指向性错误: %v", err)
	}
	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil }
	if err := acmeDomainSelfCheck("exit.example.com"); err != nil {
		t.Errorf("解析含本机应通过: %v", err)
	}
}

// TestIssueACMECertificateGuards 验证签发前置自检顺序：80 端口被占 → 指向性错误，
// 且不进入签发（execACMESh 计数为 0）。（真实 LE 签发不做，spec §6。）
func TestIssueACMECertificateGuards(t *testing.T) {
	origLookup, origAddrs, origPort, origExec := lookupHostIPs, localIfaceAddrs, acmePort80Free, execACMESh
	t.Cleanup(func() {
		lookupHostIPs, localIfaceAddrs, acmePort80Free, execACMESh = origLookup, origAddrs, origPort, origExec
	})
	// /32 同上：ParseCIDR 的 ipNet.IP 须为主机地址本身。
	_, ipNet, _ := net.ParseCIDR("203.0.113.7/32")
	localIfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{ipNet}, nil }
	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil }
	issued := 0
	execACMESh = func(home string, args ...string) error { issued++; return nil }
	acmePort80Free = func() error {
		return os.ErrExist // 模拟占用（实现包装为指向性错误文案）
	}
	m, _ := newRebuildTestManager(t)
	dir := t.TempDir()
	// acme.sh 已安装（跳过下载）：放一个假 acme.sh。
	home := m.acmeHome()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "acme.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := m.issueACMECertificate("exit.example.com", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err == nil {
		t.Fatal("80 端口被占应报错")
	}
	if issued != 0 {
		t.Errorf("端口自检失败不应进入签发，execACMESh 调用 %d 次", issued)
	}
	acmePort80Free = func() error { return nil }
	if err := m.issueACMECertificate("exit.example.com",
		filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if issued != 2 {
		t.Errorf("应依次调用 --issue 与 --install-cert，实际 %d 次", issued)
	}
}
```

（`acmePort80Free` 桩返回 `os.ErrExist` 要求实现把任意探测错误包装为指向性文案；断言只看 error 非 nil 与调用计数，文案由实现注释约定。）

- [ ] **Step 2: 写失败测试（fill/endpoint）**

`src/agent/internal/xray/fill_test.go` 追加：

```go
// TestFillTemplateTLSSelfSign 验证 tls 模板填充：占位符替换为证书绝对路径、
// realized 上报 security=tls + SNI + CertSHA256（hex pin），且无 reality 字段。
func TestFillTemplateTLSSelfSign(t *testing.T) {
	stubTLSCert(t)
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS,
		CertMode: shared.CertModeSelfSign, TLSDomain: "cdn.example.com",
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vmess", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "ws", "security": "tls",
				"tlsSettings": {"serverName": "cdn.example.com",
					"certificates": [{"certificateFile": "{{TLS_CERT_FILE}}", "keyFile": "{{TLS_KEY_FILE}}"}]},
				"wsSettings": {"path": "/p"}}
		}`),
	}
	m, _ := newRebuildTestManager(t)
	inbound, realized, err := m.fillTemplate(23411, "node_11", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Security != shared.SecurityTLS {
		t.Errorf("security 应为 tls，实际 %q", realized.Security)
	}
	if realized.SNI != "cdn.example.com" || len(realized.CertSHA256) != 64 {
		t.Errorf("SNI/pin 上报不符: %+v", realized)
	}
	if realized.PublicKey != "" || realized.ShortID != "" {
		t.Errorf("tls 模板不应有 reality 字段: %+v", realized)
	}
	if realized.Network != "ws" || realized.Path != "/p" {
		t.Errorf("ws 传输提取回归: %+v", realized)
	}
	s := string(inbound)
	if strings.Contains(s, "{{TLS_") || !strings.Contains(s, "certs/node_11/cert.pem") {
		t.Errorf("证书占位符未被绝对路径替换: %s", s)
	}
}

// TestTemplateSecurityTLS 验证 templateSecurity 识别 tls（streamSettings.security=="tls"）。
func TestTemplateSecurityTLS(t *testing.T) {
	tlsT := map[string]json.RawMessage{
		"streamSettings": json.RawMessage(`{"network":"tcp","security":"tls","tlsSettings":{}}`),
	}
	if got := templateSecurity(tlsT); got != shared.SecurityTLS {
		t.Errorf("应为 tls，实际 %q", got)
	}
}
```

（`fill_test.go` 若无 `strings` import 则补上。）

`src/agent/internal/xray/endpoint_test.go` 追加：

```go
// TestRenderSharedEndpointOutboundTLS 验证隧道段 tls 分派：自签出口（CertSHA256 非空）
// 用 pinnedPeerCertSha256 钉住证书（xray 26.x hex 字符串，allowInsecure 已移除）；
// ACME 出口（CertSHA256 空）走系统根验证，不带 pin。
func TestRenderSharedEndpointOutboundTLS(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 1, TargetAddress: "127.0.0.1", TargetPort: 1443, TunnelUUID: "t-uuid",
		Target: shared.RealizedConfig{Network: shared.NetworkTCP, Security: shared.SecurityTLS,
			SNI: "exit.example.com", CertSHA256: strings.Repeat("ab", 32)},
	}
	ob := renderSharedEndpointOutbound(route, "shared_endpoint_route_1_1")
	stream := nested(ob, "streamSettings")
	if stream["security"] != "tls" {
		t.Fatalf("tls 出口应为 security=tls: %v", stream)
	}
	tlsSettings, ok := stream["tlsSettings"].(map[string]any)
	if !ok || tlsSettings["serverName"] != "exit.example.com" {
		t.Fatalf("tlsSettings 不符: %v", stream)
	}
	if tlsSettings["pinnedPeerCertSha256"] != strings.Repeat("ab", 32) {
		t.Errorf("自签出口应带 hex pin: %v", tlsSettings)
	}
	if _, ok := stream["realitySettings"]; ok {
		t.Error("tls 分支不应输出 realitySettings")
	}
	// ACME：无 pin 键
	route.Target.CertSHA256 = ""
	ob = renderSharedEndpointOutbound(route, "shared_endpoint_route_1_1")
	tlsSettings, _ = nested(ob, "streamSettings")["tlsSettings"].(map[string]any)
	if _, ok := tlsSettings["pinnedPeerCertSha256"]; ok {
		t.Errorf("ACME 出口不应带 pin: %v", tlsSettings)
	}
}
```

（`endpoint_test.go` 若无 `strings` import 则补上；`nested` 助手见 `chain_test.go`，同包可直接用。）

- [ ] **Step 3: 跑测试确认失败**

Run: `cd src/agent && go test ./internal/xray/ -run 'TestEnsureTLSCertificate|TestACMEDomainSelfCheck|TestIssueACMECertificateGuards|TestFillTemplateTLSSelfSign|TestTemplateSecurityTLS|TestRenderSharedEndpointOutboundTLS' -v`
Expected: FAIL（undefined: execTLSCert / acmeDomainSelfCheck / ensureTLSCertificate 等）

- [ ] **Step 4: 实现（cert.go 新建）**

```go
package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lattix/shared"
)

// 证书布局（§3.3）：证书文件在 <config 目录>/certs/<inbound tag>/ 下
//（node_*/shared_endpoint_* 各自隔离）；acme.sh 安装在 <安装根>/acme/（与 config/ 平级）。
// PurgeXray/ResetForPanelRebind 不触碰这两个目录——证书随重装/换绑保留，避免订阅 pin 失效。

func (m *Manager) certsDir(tag string) string {
	return filepath.Join(filepath.Dir(m.configPath), "certs", tag)
}

func (m *Manager) acmeHome() string {
	return filepath.Join(filepath.Dir(filepath.Dir(m.configPath)), "acme")
}

// ensureTLSCertificate 确保 TLS 证书就位，返回证书/私钥绝对路径与自签 pin
//（证书 DER 的 sha256 hex；ACME 模式为空串——公共 CA 走系统根验证，无需 pin）。
// 幂等：证书文件已存在则直接复用（重新生成会轮换 pin、失效已下发订阅）。
func (m *Manager) ensureTLSCertificate(tag string, vc shared.VirtualConfig) (certFile, keyFile, pin string, err error) {
	dir := m.certsDir(tag)
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if fileExists(certFile) && fileExists(keyFile) {
		if vc.CertMode == shared.CertModeACME {
			return certFile, keyFile, "", nil
		}
		pin, err = certPinHex(certFile)
		return certFile, keyFile, pin, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", err
	}
	if vc.CertMode == shared.CertModeACME {
		if err := m.issueACMECertificate(vc.TLSDomain, certFile, keyFile); err != nil {
			return "", "", "", err
		}
		return certFile, keyFile, "", nil
	}
	// selfsign：xray tls cert -file=<prefix> 输出 <prefix>.crt/<prefix>.key，归一为 cert.pem/key.pem。
	if vc.TLSDomain == "" {
		return "", "", "", fmt.Errorf("自签证书缺少伪装域名（tls_domain）")
	}
	if err := execTLSCert(m.bin, vc.TLSDomain, filepath.Join(dir, "server")); err != nil {
		return "", "", "", err
	}
	if err := os.Rename(filepath.Join(dir, "server.crt"), certFile); err != nil {
		return "", "", "", fmt.Errorf("整理自签证书失败: %w", err)
	}
	if err := os.Rename(filepath.Join(dir, "server.key"), keyFile); err != nil {
		return "", "", "", fmt.Errorf("整理自签私钥失败: %w", err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return "", "", "", err
	}
	pin, err = certPinHex(certFile)
	if err != nil {
		return "", "", "", err
	}
	return certFile, keyFile, pin, nil
}

// execTLSCert 执行 `xray tls cert` 生成自签证书（-name 使 CN=伪装域名，SAN 由 -domain 提供；
// xray CLI 无 CA 签发下级证书能力，单张自签证书兼作服务器证书，pin 该证书）。
// 包级变量 = 测试缝（§6：fill 测试自签证书生成 mock）。
var execTLSCert = func(bin, domain, prefix string) error {
	out, err := exec.Command(bin, "tls", "cert", "-domain="+domain, "-name="+domain, "-file="+prefix).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray tls cert 执行失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// certPinHex 计算 PEM 证书 DER 的 sha256（hex）——订阅 pin（mihomo fingerprint）与
// xray 客户端 pinnedPeerCertSha256（26.x 起为 hex 字符串，allowInsecure 已移除）共用。
func certPinHex(certFile string) (string, error) {
	b, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", fmt.Errorf("证书 %s 不是合法 PEM", certFile)
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// issueACMECertificate 走 acme.sh standalone 全流程（§3.3 模式 B）：
// 安装 acme.sh（首次）→ 域名解析自检（须含本机地址）→ 80 端口自检 → 签发 → 安装到目标路径。
// acme.sh 自注册续期 cron；xray 每小时热重载证书文件，续期后自动生效，无需 reloadcmd。
func (m *Manager) issueACMECertificate(domain, certFile, keyFile string) error {
	home := m.acmeHome()
	if err := ensureACMESh(home); err != nil {
		return err
	}
	if err := acmeDomainSelfCheck(domain); err != nil {
		return err
	}
	if err := acmePort80Free(); err != nil {
		return err
	}
	if err := execACMESh(home, "--issue", "--standalone", "-d", domain); err != nil {
		return fmt.Errorf("acme.sh 签发证书失败（域名 %s）: %w", domain, err)
	}
	if err := execACMESh(home, "--install-cert", "-d", domain,
		"--key-file", keyFile, "--fullchain-file", certFile); err != nil {
		return fmt.Errorf("acme.sh 安装证书失败（域名 %s）: %w", domain, err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return err
	}
	return nil
}

// ACME 测试缝（§6：仅覆盖到"域名自检失败/80 端口被占"等路径，不做真实 LE 签发）。
var (
	lookupHostIPs   = net.LookupIP
	localIfaceAddrs = net.InterfaceAddrs
	acmePort80Free  = func() error {
		if err := probePortFree("tcp", 80); err != nil {
			return fmt.Errorf("80 端口被占用（acme.sh standalone 签发需要），请释放后重试或改用自签模式: %w", err)
		}
		return nil
	}
	execACMESh = func(home string, args ...string) error {
		full := append([]string{"--home", home}, args...)
		out, err := exec.Command(filepath.Join(home, "acme.sh"), full...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

// acmeDomainSelfCheck 校验域名解析结果包含本机网卡地址（standalone 签发要求 CA 回调本机）。
func acmeDomainSelfCheck(domain string) error {
	ips, err := lookupHostIPs(domain)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("域名 %s 解析失败，请确认域名已指向本服务器或改用自签模式", domain)
	}
	addrs, err := localIfaceAddrs()
	if err != nil {
		return fmt.Errorf("读取本机网卡地址失败: %w", err)
	}
	local := map[string]bool{}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil {
			local[ip.String()] = true
		}
	}
	resolved := make([]string, 0, len(ips))
	for _, ip := range ips {
		if local[ip.String()] {
			return nil
		}
		resolved = append(resolved, ip.String())
	}
	return fmt.Errorf("域名 %s 解析结果（%s）不含本机地址，请确认域名指向本服务器或改用自签模式",
		domain, strings.Join(resolved, ","))
}

// ensureACMESh 首次使用时安装 acme.sh 至 home（官方安装脚本；离线给出指向性错误）。
// 下载复用 upgrade.go 的 downloadFile（requester 外部下载防护）。
func ensureACMESh(home string) error {
	if fileExists(filepath.Join(home, "acme.sh")) {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	script := filepath.Join(home, "get.acme.sh")
	if err := downloadFile("https://get.acme.sh", script); err != nil {
		return fmt.Errorf("下载 acme.sh 安装脚本失败（服务器需可访问外网，或改用自签模式）: %w", err)
	}
	out, err := exec.Command("sh", script, "--install", "--home", home).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装 acme.sh 失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
```

- [ ] **Step 5: 实现（fill.go 接线）**

a) TLS 占位符填充块：vlessenc 块（`:56` 的 `}` 之后）插入：

```go
	// TLS 证书占位符（仅 security=tls 模板含有）：按 CertMode 落地证书后以绝对路径替换；
	// 自签 pin（hex）随 realized 上报（订阅与隧道段 pinnedPeerCertSha256 用）。
	certPin := ""
	if strings.Contains(t, shared.PlaceholderTLSCertFile) {
		certFile, keyFile, pin, err := m.ensureTLSCertificate(tag, vc)
		if err != nil {
			return nil, nil, err
		}
		certPin = pin
		t = strings.ReplaceAll(t, shared.PlaceholderTLSCertFile, certFile)
		t = strings.ReplaceAll(t, shared.PlaceholderTLSKeyFile, keyFile)
	}
```

b) probe 结构（`:105-129` 的 `StreamSettings` 内、`RealitySettings` 之后）加：

```go
			TLSSettings struct {
				ServerName string `json:"serverName"`
			} `json:"tlsSettings"`
```

c) realized 字面量（`:156` 的 `Security: templateSecurity(tmpl),` 行后）加：

```go
		SNI:         probe.StreamSettings.TLSSettings.ServerName,
		CertSHA256:  certPin,
```

d) `templateSecurity`（`:244-259`）替换为：

```go
// templateSecurity 从填充后的模板推导安全层：streamSettings 含 realitySettings → reality；
// 否则按 streamSettings.security 字段（tls → tls）；其余有 streamSettings → none；
// 无 streamSettings（ss/socks/http/dokodemo）→ ""。
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
	var sec string
	if err := json.Unmarshal(ss["security"], &sec); err == nil && sec == shared.SecurityTLS {
		return shared.SecurityTLS
	}
	return shared.SecurityNone
}
```

- [ ] **Step 6: 实现（endpoint.go 隧道段 tls 分派）**

`endpoint.go` `renderSharedEndpointOutbound` 的安全层块（`:148-159`）替换为：

```go
	// 隧道段安全层跟随出口业务 inbound：reality 出口（公钥非空）→ reality；
	// tls 出口（SNI 非空）→ tls（自签用 pinnedPeerCertSha256 钉住证书——xray 26.x 起为
	// hex 字符串，allowInsecure 已移除；ACME 走系统根验证）；其余 → 明文（spec §3.2/P3）。
	stream := map[string]any{"network": network}
	switch {
	case route.Target.PublicKey != "":
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName": route.Target.ServerName, "publicKey": route.Target.PublicKey,
			"shortId": route.Target.ShortID, "fingerprint": shared.FingerprintChrome,
		}
	case route.Target.SNI != "":
		stream["security"] = "tls"
		tlsSettings := map[string]any{
			"serverName":  route.Target.SNI,
			"fingerprint": shared.FingerprintChrome,
		}
		if route.Target.CertSHA256 != "" {
			tlsSettings["pinnedPeerCertSha256"] = route.Target.CertSHA256
		}
		stream["tlsSettings"] = tlsSettings
	default:
		stream["security"] = "none"
	}
```

（`:144-147` 的 network 归一化与 `:160-179` 的传输子段 switch 保持不变——tls 出口走 ws/httpupgrade 时子段照常拼装。）

- [ ] **Step 7: 跑测试确认通过**

Run: `cd src/agent && go test ./internal/xray/ -v && go test ./...`
Expected: PASS（含既有 fill/endpoint/rebuild 用例全绿；vless 共享端点的 TLS 链路 = endpointConfig 复用 vc（含 cert_mode/tls_domain 与占位符模板），经 `ApplySharedEndpoint → fillTemplate` 同路径落地，无需特判）

- [ ] **Step 8: Commit**

```bash
git add src/agent/internal/xray/cert.go src/agent/internal/xray/cert_test.go src/agent/internal/xray/fill.go src/agent/internal/xray/fill_test.go src/agent/internal/xray/endpoint.go src/agent/internal/xray/endpoint_test.go
git commit -m "feat(agent): TLS 证书落地（xray tls cert 自签 / acme.sh 签发）与隧道段证书 pin"
```

---

### Task 5: sub — tls 的 links/mihomo/singbox/quanx 输出（含清理 #2：trojan mihomo 分流）

**Files:**
- Modify: `src/backend/internal/sub/links.go:27-47`（vless 分支加 tls）、`:48-57`（trojan 分支分流）、`:101-107`（vmessTLS 加 tls）
- Modify: `src/backend/internal/sub/sub.go:273-313`（clashProxy 加 `Fingerprint`）、`:790-820`（vless/vmess/trojan 分支 tls 分流，**清理 #2**）、`:856-878` 后（新增 `applyTLS`）
- Modify: `src/backend/internal/sub/singbox.go:23-28`（sbTLS 加 `Insecure`）、`:121-141`（buildSbTLS 拆 reality/tls 两变体）
- Modify: `src/backend/internal/sub/quanx.go:52-66`（trojan tls 分流）
- Test: `src/backend/internal/sub/links_test.go`（追加）

**Interfaces:**
- Consumes: `RealizedConfig.SNI/CertSHA256/EffectiveSecurity()`（Task 2/4）；`applyPlainTransport`（P2）。
- Produces:
  - `clashProxy.Fingerprint string \`yaml:"fingerprint,omitempty"\``（mihomo 证书 sha256 pin）。
  - `func applyTLS(p *clashProxy, rc shared.RealizedConfig)`（sub.go 包级私有）— 普通 tls 输出：tls+servername/sni+uTLS 指纹+传输选项；自签（CertSHA256 非空）输出 pin。
  - `sbTLS.Insecure bool \`json:"insecure,omitempty"\``（sing-box 无 cert pin 表达能力，自签回退 insecure，§3.4）。

**输出约定（spec §3.4/§3.5）**：自签 = pin 优先（mihomo `fingerprint`），无 pin 表达能力的格式回退 insecure（links URI `allowInsecure=1`、sing-box `insecure:true`、QuanX 跳过）；ACME = 普通 TLS（系统根验证，无 pin/insecure）。vmess 分享 JSON 无 pin/insecure 表达，仅输出 `tls=tls`+`sni`（以 mihomo/singbox 格式为自签主通道）。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/sub/links_test.go` 追加：

```go
// TestBuildShareLinkTLS 验证 tls 分享链接：vless/trojan URI 带 security=tls+sni，
// 自签（CertSHA256 非空）回退 allowInsecure=1（URI 无 pin 表达）；ACME 无 insecure。
func TestBuildShareLinkTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "cdn.example.com", CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	link, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid")
	if !ok {
		t.Fatal("vless tls link unsupported")
	}
	for _, want := range []string{"security=tls", "sni=cdn.example.com", "allowInsecure=1"} {
		if !strings.Contains(link, want) {
			t.Errorf("vless tls 自签链接缺 %q: %s", want, link)
		}
	}
	if strings.Contains(link, "pbk=") {
		t.Errorf("tls 链接不应含 reality 参数: %s", link)
	}
	// trojan + ws + tls（P3 合法化组合）
	rc.Network = shared.NetworkWS
	rc.Path = "/tw"
	link, ok = buildShareLink(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if !ok {
		t.Fatal("trojan tls link unsupported")
	}
	for _, want := range []string{"trojan://", "security=tls", "sni=cdn.example.com", "type=ws", "path=%2Ftw", "allowInsecure=1"} {
		if !strings.Contains(link, want) {
			t.Errorf("trojan ws tls 链接缺 %q: %s", want, link)
		}
	}
	// ACME：无 allowInsecure
	rc.CertSHA256 = ""
	link, _ = buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid")
	if strings.Contains(link, "allowInsecure") {
		t.Errorf("ACME 链接不应含 allowInsecure: %s", link)
	}
	// vmess JSON：tls=tls + sni（无 pin/insecure 表达）
	rc.Security = shared.SecurityTLS
	rc.CertSHA256 = strings.Repeat("ab", 32)
	link, ok = buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if !ok {
		t.Fatal("vmess tls link unsupported")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["tls"] != "tls" || m["sni"] != "cdn.example.com" {
		t.Errorf("vmess tls JSON 不符: %v", m)
	}
	if _, exists := m["pbk"]; exists {
		t.Errorf("tls 的 vmess JSON 不应含 pbk: %v", m)
	}
}

// TestBuildProxyTLS 验证 mihomo 输出：tls + servername + client-fingerprint；
// 自签输出证书 pin（fingerprint）；trojan 走 sni 字段（清理 #2 分流）。
func TestBuildProxyTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS, Path: "/p",
		Security: shared.SecurityTLS, SNI: "cdn.example.com",
		CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if !p.TLS || p.Servername != "cdn.example.com" || p.RealityOpts != nil {
		t.Errorf("vmess tls 代理项不符: %+v", p)
	}
	if p.Fingerprint != strings.Repeat("ab", 32) {
		t.Errorf("自签应输出 pin: %+v", p)
	}
	if p.WsOpts == nil || p.WsOpts.Path != "/p" {
		t.Errorf("tls+ws 传输选项不符: %+v", p.WsOpts)
	}
	// trojan：sni 字段 + pin（清理 #2：不再无条件 applyReality）
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if !p.TLS || p.SNI != "cdn.example.com" || p.RealityOpts != nil || p.Fingerprint == "" {
		t.Errorf("trojan tls 代理项不符: %+v", p)
	}
	// ACME：无 pin、无 skip-cert-verify
	rc.CertSHA256 = ""
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.Fingerprint != "" || p.SkipCertVerify != nil {
		t.Errorf("ACME 应走系统根验证: %+v", p)
	}
}

// TestBuildSbOutboundTLS 验证 sing-box 输出：普通 tls 变体（server_name=SNI，无 reality 块）；
// 自签回退 insecure:true（sing-box 无 cert pin 表达，§3.4）。
func TestBuildSbOutboundTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "cdn.example.com", CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS == nil || ob.TLS.ServerName != "cdn.example.com" || ob.TLS.Reality != nil {
		t.Fatalf("sing-box tls 不符: %+v", ob.TLS)
	}
	if !ob.TLS.Insecure {
		t.Errorf("自签应回退 insecure: %+v", ob.TLS)
	}
	rc.CertSHA256 = ""
	ob, err = buildSbOutbound(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS == nil || ob.TLS.Insecure {
		t.Errorf("ACME 不应带 insecure: %+v", ob.TLS)
	}
}

// TestQuanXTrojanTLS 验证 QuanX 尽力而为：trojan×tls（ACME）输出 over-tls；
// 自签无 pin/insecure 表达 → 跳过（§3.4）。
func TestQuanXTrojanTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "exit.example.com"}
	line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if !strings.Contains(line, "obfs=over-tls") || !strings.Contains(line, "obfs-host=exit.example.com") {
		t.Errorf("trojan tls（ACME）应输出 over-tls: %q", line)
	}
	if strings.Contains(line, "reality-pubkey") {
		t.Errorf("tls 不应含 reality 参数: %q", line)
	}
	rc.CertSHA256 = strings.Repeat("ab", 32)
	if line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid"); line != "" {
		t.Errorf("自签 trojan QuanX 无 pin 表达，应跳过: %q", line)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/sub/ -run 'TLS' -v`
Expected: FAIL（现有实现 reality 硬编码 / trojan 无条件 applyReality）

- [ ] **Step 3: 实现**

a) `links.go` vless 分支（`:30-37` 的 security 块）替换为：

```go
		security := rc.EffectiveSecurity()
		q.Set("security", security)
		switch security {
		case shared.SecurityReality:
			q.Set("pbk", rc.PublicKey)
			q.Set("sid", rc.ShortID)
			q.Set("sni", rc.ServerName)
			q.Set("fp", rc.Fingerprint)
		case shared.SecurityTLS:
			q.Set("sni", rc.SNI)
			q.Set("fp", rc.Fingerprint)
			if rc.CertSHA256 != "" {
				// 自签：URI 无 pin 表达，回退 allowInsecure（§3.4 开箱即用）。
				q.Set("allowInsecure", "1")
			}
		}
```

b) `links.go` trojan 分支（`:48-57`）替换为（不再硬编码 reality——tls 合法化后 trojan 有 reality/tls 两形态）：

```go
	case shared.ProtocolTrojan:
		q := url.Values{}
		q.Set("type", rc.Network)
		security := rc.EffectiveSecurity()
		q.Set("security", security)
		if security == shared.SecurityReality {
			q.Set("pbk", rc.PublicKey)
			q.Set("sid", rc.ShortID)
			q.Set("sni", rc.ServerName)
			q.Set("fp", rc.Fingerprint)
		} else {
			// tls（矩阵不允许 trojan×none）：sni=SNI；自签回退 allowInsecure。
			q.Set("sni", rc.SNI)
			q.Set("fp", rc.Fingerprint)
			if rc.CertSHA256 != "" {
				q.Set("allowInsecure", "1")
			}
		}
		setTransportQuery(q, rc)
		return fmt.Sprintf("trojan://%s@%s?%s#%s", uuid, addr, q.Encode(), name), true
```

c) `links.go` `vmessTLS`（`:101-107`）替换为：

```go
// vmessTLS 是 vmess 分享 JSON 的 tls 字段：reality/tls 原样，无安全层 → 空串。
func vmessTLS(rc shared.RealizedConfig) string {
	switch rc.EffectiveSecurity() {
	case shared.SecurityReality:
		return "reality"
	case shared.SecurityTLS:
		return "tls"
	}
	return ""
}
```

d) `links.go` vmess 分支（Task 1 已改为条件键）：在 `if rc.EffectiveSecurity() == shared.SecurityReality {...}` 之后补 tls 分支：

```go
		if rc.EffectiveSecurity() == shared.SecurityTLS {
			fields["sni"] = rc.SNI
			fields["fp"] = rc.Fingerprint
		}
```

e) `sub.go` clashProxy（`:295` 的 `ClientFingerprint` 行后）加：

```go
	Fingerprint       string            `yaml:"fingerprint,omitempty"`       // 证书 sha256 pin（自签 tls）
```

f) `sub.go` vless 分支（`:796-802`）与 vmess 分支（`:809-815`）的 `if reality {...} else {applyPlainTransport}` 均替换为三分支：

```go
		switch rc.EffectiveSecurity() {
		case shared.SecurityReality:
			p.TLS = true
			p.Servername = rc.ServerName
			applyReality(&p, rc)
		case shared.SecurityTLS:
			applyTLS(&p, rc)
		default:
			applyPlainTransport(&p, rc)
		}
```

g) `sub.go` trojan 分支（`:816-820`）替换为（**清理 #2**）：

```go
	case shared.ProtocolTrojan:
		p.Password = uuid
		p.Network = rc.Network
		if rc.EffectiveSecurity() == shared.SecurityReality {
			p.SNI = rc.ServerName
			applyReality(&p, rc)
		} else {
			// tls（矩阵不允许 trojan×none）：普通 TLS 输出（清理 #2：不再无条件 applyReality）。
			applyTLS(&p, rc)
		}
```

h) `sub.go` `applyPlainTransport` 之后追加：

```go
// applyTLS 填充普通 tls（非 reality）安全层：servername/sni=SNI + uTLS 指纹 + 传输选项；
// 自签（CertSHA256 非空）输出证书 pin（mihomo fingerprint），ACME 走系统根验证（§3.4）。
func applyTLS(p *clashProxy, rc shared.RealizedConfig) {
	p.TLS = true
	if p.Type == shared.ProtocolTrojan {
		p.SNI = rc.SNI // mihomo trojan 的 SNI 字段名为 sni
	} else {
		p.Servername = rc.SNI
	}
	p.ClientFingerprint = rc.Fingerprint
	if rc.CertSHA256 != "" {
		p.Fingerprint = rc.CertSHA256
	}
	applyPlainTransport(p, rc)
}
```

i) `singbox.go` `sbTLS`（`:23-28`）加字段：

```go
	Insecure   bool          `json:"insecure,omitempty"`   // 自签回退（sing-box 无 cert pin 表达，§3.4）
```

`buildSbTLS`（`:121-141`）整体替换为（spec §3.4 拆分落地）：

```go
// buildSbTLS 构造 sing-box TLS 配置：reality / 普通 tls 两个变体；security=none 返回 nil。
// 普通 tls：server_name=SNI + uTLS；自签（CertSHA256 非空）回退 insecure（无 pin 表达），
// ACME 走系统根验证。
func buildSbTLS(rc shared.RealizedConfig) *sbTLS {
	switch rc.EffectiveSecurity() {
	case shared.SecurityReality:
		return &sbTLS{
			Enabled:    true,
			ServerName: rc.ServerName,
			Reality: &sbTLSReality{
				Enabled:   true,
				PublicKey: rc.PublicKey,
				ShortID:   rc.ShortID,
			},
			UTLS: &sbUTLS{
				Enabled:     true,
				Fingerprint: rc.Fingerprint,
			},
		}
	case shared.SecurityTLS:
		return &sbTLS{
			Enabled:    true,
			ServerName: rc.SNI,
			Insecure:   rc.CertSHA256 != "",
			UTLS: &sbUTLS{
				Enabled:     true,
				Fingerprint: rc.Fingerprint,
			},
		}
	}
	return nil
}
```

j) `quanx.go` trojan 分支（`:52-66`）替换为：

```go
	case shared.ProtocolTrojan:
		security := rc.EffectiveSecurity()
		if security == shared.SecurityTLS && rc.CertSHA256 != "" {
			return "" // 自签无 pin/insecure 表达，跳过（§3.4 尽力而为）
		}
		if security != shared.SecurityReality && security != shared.SecurityTLS {
			return "" // QuanX 仅输出 reality/tls 形态（none 跳过）
		}
		sni := rc.ServerName
		if security == shared.SecurityTLS {
			sni = rc.SNI
		}
		parts := []string{
			fmt.Sprintf("trojan=%s", addr),
			fmt.Sprintf("password=%s", uuid),
			"obfs=over-tls",
			fmt.Sprintf("obfs-host=%s", sni),
			"tls13=true",
			"fast-open=false",
			"udp-relay=true",
			fmt.Sprintf("tag=%s", name),
		}
		if security == shared.SecurityReality {
			if rc.PublicKey != "" {
				parts = append(parts, fmt.Sprintf("reality-pubkey=%s", rc.PublicKey))
			}
			if rc.ShortID != "" {
				parts = append(parts, fmt.Sprintf("reality-hexid=%s", rc.ShortID))
			}
		}
		return strings.Join(parts, ", ")
```

（vless 分支的 reality-only 跳过保持不变——QuanX 的 vless tls 形态不做，§3.4 尽力而为。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/sub/ -v && go build ./...`
Expected: PASS（既有 reality/ws 明文用例不回归）

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/sub/links.go src/backend/internal/sub/sub.go src/backend/internal/sub/singbox.go src/backend/internal/sub/quanx.go src/backend/internal/sub/links_test.go
git commit -m "feat(sub): tls 的 links/mihomo/singbox/quanx 输出（自签 pin / insecure 回退 / ACME 普通 TLS）"
```

---

### Task 6: frontend — 安全层选择器 + TLS 区域（证书模式单选 + ACME 域名实时检测）（含清理 #3）

**Files:**
- Modify: `src/frontend/src/lib/types.ts:411-431`（CreateNodeRequest 加 `security/cert_mode/tls_domain`）
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts:74-77` 后（securityOptions）、`:107-136`（ChainFormState 加 security/certMode/tlsDomain）、`:138-167`（initialChainForm）、`:281-310`（openEdit 回填）、`:330-343`（onNetworkChange 纠偏）、`:345-359`（onProtocolChange 纠偏）、`:414-471`（onSubmit 映射，**清理 #3**）、`:532-554`（return 加 onSecurityChange）
- Modify: `src/frontend/src/pages/chains/ChainFormDialog.tsx:146-163`（解构）、`:164-166`（落地服务器域名派生）、`:395-409`（network 选择器后插 security 选择器）、`:458-487`（ws/httpupgrade 提示更新）、`:488-571`（reality 字段按 security 显隐 + TLS 区域）

**Interfaces:**
- Consumes: 生成的 VirtualConfig 类型含 `cert_mode/tls_domain`（Task 3 契约）；`addressFamily`（`@/lib/address`）；后端 `createNodeRequest` 接受 `security/cert_mode/tls_domain`（Task 3）。
- Produces:
  - `export function securityOptions(protocol: string, network: string): string[]`（镜像后端矩阵）。
  - `ChainFormState.security/certMode/tlsDomain`；`ChainFormController.onSecurityChange`。

**交互规格（spec §4）**：reality 系协议显示"安全层"选择器，可选项随协议×传输即时变化（镜像后端矩阵）；选 tls 出现 TLS 区域：证书模式单选——"伪装域名自签"（默认，可自定义伪装域，留空=预设池随机）/"使用落地服务器域名（ACME）"——后者实时检测落地服务器（直连=入口机，中转=出口机）`addresses` 是否含域名条目，无则禁用并提示原因。**清理 #3**：trojan×ws/httpupgrade 的提交期报错删除，改为选择器即时纠正（trojan + ws/httpupgrade 时 security 只剩 tls 可选并自动选中）。

- [ ] **Step 1: 表单状态与提交（use-chain-form.ts）**

a) `isPlainNetwork`（`:74-77`）之后追加：

```ts
// 与后端 shared 包保持一致（RealityNetworks）。
const REALITY_NETWORKS = ['tcp', 'grpc', 'xhttp']

/** 安全层可选项（镜像后端 normalize 矩阵）：trojan 不允许 none；reality 仅 tcp/grpc/xhttp。 */
export function securityOptions(protocol: string, network: string): string[] {
  const realityOK = REALITY_NETWORKS.includes(network)
  if (protocol === 'trojan') return realityOK ? ['reality', 'tls'] : ['tls']
  return realityOK ? ['reality', 'tls', 'none'] : ['tls', 'none']
}

/** 安全层纠偏：当前值不在可选项中时回退到首个（reality 兼容传输首选 reality，否则 tls 系首选 none/trojan 强制 tls）。 */
export function coerceSecurity(protocol: string, network: string, security: string): string {
  const options = securityOptions(protocol, network)
  if (options.includes(security)) return security
  return options.includes('reality') ? 'reality' : options.includes('none') ? 'none' : 'tls'
}
```

b) `ChainFormState`（`:130` 的 `serviceName` 行前）加：

```ts
  security: string
  certMode: string
  tlsDomain: string
```

`initialChainForm`（`:160` 的 `encryption` 行后）加：

```ts
  security: 'reality',
  certMode: 'selfsign',
  tlsDomain: '',
```

c) `onNetworkChange`（`:330-343`）替换为：

```ts
  const onNetworkChange = (value: string | null) => {
    if (!value) return
    setForm((current) => ({
      ...current,
      network: value,
      // 跨传输纠偏：vision flow 仅 tcp；security 按矩阵即时纠正（清理 #3）
      flow: value === 'tcp' ? current.flow : 'none',
      security: coerceSecurity(current.protocol, value, current.security),
      // vless 明文传输必须有 VLESS Encryption 兜底（后端矩阵，前端即时纠正）
      encryption:
        current.protocol === 'vless' && isPlainNetwork(value) && current.encryption === 'none'
          ? 'mlkem768'
          : current.encryption,
    }))
  }

  const onSecurityChange = (value: string | null) => {
    if (!value) return
    setForm((current) => ({ ...current, security: value }))
  }
```

d) `onProtocolChange`（`:345-359`）的 setForm 内 `encryption` 行前加一行 security 纠偏：

```ts
      security: coerceSecurity(value, current.network, current.security),
```

e) `onSubmit` 的 `if (isReality)` 块（`:422-471`）整体替换为：

```ts
    if (isReality) {
      nodeBody.network = form.network
      nodeBody.security = form.security
      if (form.network === 'ws' || form.network === 'httpupgrade') {
        nodeBody.path = form.path.trim() || '/'
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
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
      if (form.security === 'reality') {
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
      if (form.security === 'tls') {
        nodeBody.fingerprint = form.fingerprint
        nodeBody.cert_mode = form.certMode
        // acme 的 tls_domain 由后端从落地服务器地址检测填充（前端不传）
        if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
          nodeBody.tls_domain = form.tlsDomain.trim()
        }
      }
      if (form.security === 'none' && form.protocol === 'vless' && form.encryption === 'none') {
        setCreateError('vless 在安全层 none 下必须启用 VLESS Encryption')
        return
      }
      if (form.protocol === 'vless') {
        // vision 仅 tcp；vision + Encryption 允许组合（§15）；tls 下 vision 同样合法（§2）
        nodeBody.flow = form.network === 'tcp' ? form.flow : 'none'
        if (form.encryption !== 'none') {
          nodeBody.encryption = form.encryption
        }
      }
    }
```

（原 `:430-433` 的 trojan×ws/httpupgrade 提交期报错整体删除——**清理 #3**，由 securityOptions/coerceSecurity 在选择器层即时保证合法。）

f) `openEdit` 的 setForm（`:294` 的 `network` 行后）加：

```ts
      security: String(
        virtual.security || (isPlainNetwork(String(virtual.network || 'tcp')) ? 'none' : 'reality'),
      ),
      certMode: String(virtual.cert_mode || 'selfsign'),
      tlsDomain: String(virtual.tls_domain || ''),
```

g) return 对象（`:548` 的 `onNetworkChange` 行后）加 `onSecurityChange`。

h) `src/frontend/src/lib/types.ts` `CreateNodeRequest`（`:420` 的 `network?` 行后）加：

```ts
  security?: string
  cert_mode?: string
  tls_domain?: string
```

- [ ] **Step 2: 对话框渲染（ChainFormDialog.tsx）**

a) import（`:29-43`）加 `coerceSecurity` 不需要——仅需 `securityOptions`；解构（`:146-163`）加 `onSecurityChange`。

b) `:164-166` 的派生区替换为：

```tsx
  const serverSelectItems = servers.map((s) => ({ value: String(s.id), label: serverLabel(s) }))
  const plainNetwork = isPlainNetwork(form.network)
  // 落地服务器（ACME 域名检测对象，§4）：直连=唯一服务器，中转=出口服务器。
  const landingServer =
    form.chainType === 'direct'
      ? servers.find((s) => String(s.id) === form.entryId)
      : servers.find((s) => String(s.id) === form.exitId)
  const landingDomain = landingServer?.addresses.find((a) => addressFamily(a) === 'domain')
```

c) network 选择器块（`:395-409`）之后插入 security 选择器：

```tsx
              <div className="space-y-2">
                <Label>安全层（security）</Label>
                <Select
                  value={form.security}
                  onValueChange={onSecurityChange}
                  items={securityOptions(form.protocol, form.network).map((s) => ({
                    value: s,
                    label:
                      s === 'reality'
                        ? 'Reality（推荐 · 抗封锁最强）'
                        : s === 'tls'
                          ? 'TLS（证书：自签伪装 / ACME 真实域名）'
                          : 'none（明文 · 仅适合套 CDN）',
                  }))}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {securityOptions(form.protocol, form.network).map((s) => (
                      <SelectItem key={s} value={s}>
                        {s === 'reality'
                          ? 'Reality（推荐 · 抗封锁最强）'
                          : s === 'tls'
                            ? 'TLS（证书：自签伪装 / ACME 真实域名）'
                            : 'none（明文 · 仅适合套 CDN）'}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
```

d) TLS 区域：security 选择器之后插入：

```tsx
              {form.security === 'tls' && (
                <div className="space-y-2">
                  <Label id="cert-mode-label">证书模式</Label>
                  <div
                    role="radiogroup"
                    aria-labelledby="cert-mode-label"
                    className="grid grid-cols-2 gap-2"
                  >
                    <label className={cn('cg-chain-type', form.certMode === 'selfsign' && 'is-selected')}>
                      <input
                        type="radio"
                        name="cert-mode"
                        value="selfsign"
                        checked={form.certMode === 'selfsign'}
                        onChange={() => patch({ certMode: 'selfsign' })}
                        className="sr-only"
                      />
                      伪装域名自签
                    </label>
                    <label
                      className={cn(
                        'cg-chain-type',
                        form.certMode === 'acme' && 'is-selected',
                        !landingDomain && 'opacity-50 pointer-events-none',
                      )}
                    >
                      <input
                        type="radio"
                        name="cert-mode"
                        value="acme"
                        checked={form.certMode === 'acme'}
                        disabled={!landingDomain}
                        onChange={() => patch({ certMode: 'acme' })}
                        className="sr-only"
                      />
                      使用落地服务器域名（ACME）
                    </label>
                  </div>
                  {form.certMode === 'selfsign' ? (
                    <>
                      <Label htmlFor="tlsDomain">伪装域名（可空）</Label>
                      <Input
                        id="tlsDomain"
                        value={form.tlsDomain}
                        onChange={(e) => patch({ tlsDomain: e.target.value })}
                        placeholder="留空从常见域名预设池随机选取"
                      />
                      <p className="cg-chain-hint">
                        仅作 TLS 伪装身份（证书 CN/SAN 与客户端 SNI），不要求指向本机；
                        订阅以证书指纹（pin）校验，客户端无需信任系统 CA。
                      </p>
                    </>
                  ) : (
                    <p className="cg-chain-hint">
                      {landingDomain
                        ? `将沿用落地服务器域名 ${landingDomain}，由节点自动安装 acme.sh 签发并续期（需域名解析指向本机、80 端口空闲）。`
                        : '落地服务器未设置域名，请先在服务器地址中配置域名或改用自签模式。'}
                    </p>
                  )}
                </div>
              )}
```

e) reality 专有字段显隐：`:530`、`:550`、`:561` 三处的 `{!plainNetwork && (...)}` 条件改为 `{form.security === 'reality' && (...)}`（uTLS 指纹块、shortId 块、RealityDestPicker）。uTLS 指纹块例外：fingerprint 对 tls 同样有效——把 uTLS 指纹块（`:530-549`）条件改为 `{form.security !== 'none' && (...)}`。

f) ws/httpupgrade 提示（`:482-486`）更新为：

```tsx
                  <p className="cg-chain-hint">
                    ws/httpupgrade 支持 none（明文 · 套 CDN）与 tls（证书）安全层；
                    vless 选 none 时需启用 VLESS Encryption。
                  </p>
```

（ws/httpupgrade 输入块显隐条件 `plainNetwork` 不变——path/host 与传输绑定，与 security 无关。）

g) flow 块（`:509` 的 `form.protocol === 'vless' && form.network === 'tcp'`）保持不变（vision 在 tls+tcp 下同样合法，后端校验；前端选择器保持 tcp 才显示）。

- [ ] **Step 3: 验证**

Run: `cd src/frontend && npm run lint && npm test && npm run build`
Expected: 全绿（本目录无 vitest 用例，lint+build 为准；build 内含契约 `--check`）

- [ ] **Step 4: Commit**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/pages/chains/
git commit -m "feat(frontend): 链路表单安全层选择器与 TLS 区域（自签伪装域 / ACME 落地域名检测）"
```

---

### Task 7: e2e — tls 自签数据面 + ACME 失败路径 + 矩阵用例更新 + 全量存量回归

**Files:**
- Modify: `scripts/e2e/protocols.sh:185-191`（矩阵 400 用例更新）、`:173-183` 后（tls 节点用例）、`:218-258`（订阅断言与计数）、尾部（tls pin 数据面 + ACME 域名自检失败路径）
- Modify: `scripts/e2e/chains.sh`（链5：vless+tcp+tls 中继真实流量 + 删链计数）

- [ ] **Step 1: protocols.sh — tls 自签节点（vless tcp / trojan ws）**

`:177-183` 的 vless httpupgrade 段之后插入：

```bash
echo ">> vless tls 自签（tcp；伪装域名留空=预设池随机）"
R="$(create_node '{"server_id":1,"protocol":"vless","security":"tls","flow":"none"}')"
python3 -c 'import json,sys,re; rc=json.loads(sys.argv[1]); assert rc.get("security")=="tls" and rc.get("sni") and re.fullmatch(r"[0-9a-f]{64}", rc.get("cert_sha256","")) and rc.get("public_key","")=="" , rc' "$R" && check_port "$R"

echo ">> trojan ws + tls 自签（自定义伪装域名；P3 起合法）"
R="$(create_node '{"server_id":1,"protocol":"trojan","network":"ws","path":"/tw","security":"tls","tls_domain":"cdn.example.com"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="ws" and rc["path"]=="/tw" and rc.get("security")=="tls" and rc.get("sni")=="cdn.example.com" and len(rc.get("cert_sha256",""))==64, rc' "$R" && check_port "$R"
TLS_NODE_ID="$(db "SELECT id FROM nodes WHERE protocol='vless' AND json_extract(realized_config,'$.security')='tls' ORDER BY id LIMIT 1")"
TLS_SNI="$(db "SELECT json_extract(realized_config,'$.sni') FROM nodes WHERE id=$TLS_NODE_ID")"
TLS_PIN="$(db "SELECT json_extract(realized_config,'$.cert_sha256') FROM nodes WHERE id=$TLS_NODE_ID")"
```

（db() 输出的 realized_config 为 JSON 文本，sqlite json_extract 可用——modernc.org/sqlite 内置 JSON1。）

- [ ] **Step 2: protocols.sh — 矩阵 400 用例更新（清理 #1 的 e2e 面）**

`:185-191` 替换为：

```bash
echo ">> 矩阵外组合 400（reality×ws / trojan×ws 推导 none / vless+ws 无 Encryption / ss 带传输层 / acme 无域名 / cert_mode 未知）"
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","network":"ws","security":"reality"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"trojan","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vless","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"shadowsocks","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"acme"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"bogus"}'
echo "   矩阵外组合均被 400 拦截 OK（trojan×ws 仍需显式 tls；acme 在无域名服务器上前置 400）"
```

（原 `{"server_id":1,"protocol":"vmess","security":"tls"}` 的 expect_fail 删除——tls 已合法；trojan×ws 保留但语义变为"推导 none 被 trojan 拒绝"。）

- [ ] **Step 3: protocols.sh — 订阅断言与计数**

订阅校验段（`:218-258`）追加与调整。

links 断言：在既有 `LINKS_OUT` 获取（`:238`）之后追加：

```bash
grep -q "security=tls" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 security=tls"; echo "$LINKS_OUT"; exit 1; }
grep -q "allowInsecure=1" <<<"$LINKS_OUT" || { echo "FAIL: links 缺自签 allowInsecure=1"; echo "$LINKS_OUT"; exit 1; }
```

mihomo pin 精确断言（64 位 hex fingerprint 与 realized 一致；勿用 `grep "fingerprint: "`——
reality 节点的 `client-fingerprint: chrome` 行含该子串，grep 恒真）：

```bash
python3 - "$SUB" "$TLS_PIN" <<'PY'
import sys, yaml
doc = yaml.safe_load(sys.argv[1])
pin = sys.argv[2]
pins = [p.get("fingerprint") for p in doc["proxies"]]
assert pin in pins, (pin, pins)
PY
```

vmess JSON tls 断言并入既有 python 块（`:240-249` 的 assert 后追加）：

```python
assert any(v.get("tls") == "tls" and v.get("sni") for v in vmess), links
# security=none 的 vmess 不携带空值 sni/fp/pbk/sid 键（清理 #5）
plain = [v for v in vmess if v.get("tls") in ("", None)]
assert all(not v.get("sni") and "pbk" not in v for v in plain), plain
```

`PROXY_COUNT` 断言改为：

```bash
PROXY_COUNT="$(grep -c 'server: ' <<<"$SUB")"
EXPECTED=14
[[ "$HAS_VLESSENC" == "true" ]] && EXPECTED=15
```

- [ ] **Step 4: protocols.sh — tls 自签数据面（pinned 真实流量）**

订阅断言段之后追加（前置：u1 已分配全部非 dokodemo 节点，tls 节点在其中）：

```bash
echo ">> vless tls 自签数据面（pinnedPeerCertSha256 钉住证书）"
TLS_PORT="$(db "SELECT json_extract(realized_config,'$.port') FROM nodes WHERE id=$TLS_NODE_ID")"
UUID1="$(db "SELECT uuid FROM users WHERE id=1")"
python3 - "$WORK/client-tls.json" "$TLS_PORT" "$UUID1" "$TLS_SNI" "$TLS_PIN" <<'PY'
import json, sys
path, port, uuid, sni, pin = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11812,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": "none"}]}]},
        "streamSettings": {"network": "tcp", "security": "tls",
                           "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                                           "pinnedPeerCertSha256": pin}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-tls.json" >/dev/null || { echo "FAIL: tls 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-tls.json" >"$WORK/client-tls.log" 2>&1 &
TLSXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11812" --max-time 8 https://example.com/ || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $TLSXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless tls 自签链路 200（pin 校验通过）" \
    || { echo "FAIL: vless tls 链路未通"; tail -n 5 "$WORK/client-tls.log"; exit 1; }
```

cleanup() 的 pkill 列表维持按 `xray run -config $XRAY_CONFIG` 前缀（本用例配置在 `$WORK` 下，进程已即时 kill，cleanup 兜底无需改）。

- [ ] **Step 5: protocols.sh — ACME 域名自检失败路径（spec §6：不做真实 LE 签发）**

数据面用例之后追加：

```bash
echo ">> ACME 模式：域名自检失败路径（落地服务器有域名但解析不含本机 → 节点 failed，错误指向性）"
rpc_data POST /api/server/update '{"id":1,"address":"127.0.0.1","addresses":["127.0.0.1","acme-e2e.invalid"]}' >/dev/null
ACME_RES="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"acme"}')"
ACME_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$ACME_RES")"
ACME_OUT="$(wait_node "$ACME_ID")"
[[ "$ACME_OUT" == failed\|* ]] || { echo "FAIL: acme 节点应 failed: $ACME_OUT"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import sys; err=sys.argv[1].split("|")[2]; assert ("解析" in err) or ("acme" in err.lower()), err' "$ACME_OUT" \
    && echo "OK: acme 节点 failed，错误指向域名解析/acme.sh（$(cut -d'|' -f3 <<<"$ACME_OUT" | head -c 80)…）"
# 清理：删除失败节点并还原服务器地址，避免污染后续断言
rpc_data POST /api/node/delete "{\"node_id\":$ACME_ID}" >/dev/null
rpc_data POST /api/server/update '{"id":1,"address":"127.0.0.1","addresses":["127.0.0.1"]}' >/dev/null
```

（失败原因取决于环境：离线环境停在 acme.sh 下载失败、有网环境停在域名解析失败——两类错误均为指向性文案，断言用宽松包含匹配。）

- [ ] **Step 6: chains.sh — 链5（vless+tcp+tls 中继，共享端点 tls + 隧道段 pin）真实流量**

在 P2 链4 段之后、删链段之前插入：

```bash
echo ">> 链5（vless+tcp+tls 中继，共享端点 tls 自签 + 出口 tls pin 隧道）→ active → 真实流量"
CHAIN5="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\",\"security\":\"tls\",\"flow\":\"none\"}}")"
CH5="$(py "d['id']" "$CHAIN5")"
wait_chain "$CH5" active 90
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH5" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP5_PORT="$(chain_field "$CH5" "c['entry_port']")"
EP5_ID="$(chain_field "$CH5" "c['endpoint_id']")"
[[ -n "$EP5_PORT" && "$EP5_PORT" != "0" ]] || { echo "FAIL: 链5 端点未就绪: $(chain_field "$CH5" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH5" active 30
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH1,$CH3,$CH5]}" >/dev/null
ACCESS_UUID5=""
for _ in $(seq 1 15); do
    ACCESS_UUID5="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=[a for a in (u.get('chain_assignments') or []) if a.get('chain_id')==$CH5]
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID5" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID5" ]] || { echo "FAIL: 未取到链5 assignment"; exit 1; }
EP5_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP5_ID")"
EP5_SNI="$(py "d.get('sni') or ''" "$EP5_RC")"
EP5_PIN="$(py "d.get('cert_sha256') or ''" "$EP5_RC")"
[[ -n "$EP5_SNI" && "${#EP5_PIN}" == "64" ]] || { echo "FAIL: 链5 端点 tls realized 缺失: $EP5_RC"; exit 1; }
# 出口 realized 也是 tls（隧道段 outbound 的 pin 来源）
CH5_EXIT_SNI="$(chain_field "$CH5" "json.loads(c['hops'][-1].get('service_realized') or '{}').get('sni','')" 2>/dev/null || true)"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-tls-chain.json" "$EP5_PORT" "$ACCESS_UUID5" "$EP5_SNI" "$EP5_PIN" <<'PY'
import json, sys
path, port, uuid, sni, pin = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11813,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": "none"}]}]},
        "streamSettings": {"network": "tcp", "security": "tls",
                           "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                                           "pinnedPeerCertSha256": pin}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-tls-chain.json" >/dev/null || { echo "FAIL: 链5 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-tls-chain.json" >"$WORK/client-tls-chain.log" 2>&1 &
TCXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11813" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $TCXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless tls 中继链路 200（client→tls 端点→pin 隧道段→tls 出口）" \
    || { echo "FAIL: vless tls 中继链路未通"; tail -n 5 "$WORK/client-tls-chain.log"; exit 1; }
fi
```

（`chain_field` 的字段路径以 chains.sh 既有助手为准；若 hop 行不携带 service_realized，改为查询 DB：`db "SELECT realized_config FROM nodes WHERE id=$(py "d['hops'][-1]['node_id']" "$CHAIN5")"`。`EP5_*` 断言已覆盖端点侧，出口侧 sni 仅作观察。）

- [ ] **Step 7: chains.sh — 删链段计数更新与存量编辑回归确认**

删链段：删 CH3/CH4 的既有行之后加：

```bash
rpc_data POST /api/chain/delete "{\"chain_id\":$CH5}" >/dev/null
EXPECT_REMOVE=12
[[ "$HAS_VLESSENC" == "true" ]] && EXPECT_REMOVE=15
```

（P2 基线为 9/12；链5 与链1 同为两跳中继 = +3 件 chain-hop.remove。）

存量编辑回归段（P2 既有"编辑存量链路原样保存"用例）保持不动——tls 字段对 reality 链路完全不可见（模板 key 集不变），由该用例背书。

- [ ] **Step 8: 全量 e2e 回归（存量链路不失效的证明）**

```bash
export XRAY_BIN=/usr/local/bin/xray
bash scripts/e2e/protocols.sh    # 全协议 + tls 自签 + 矩阵 400 + acme 失败路径 + pin 数据面
bash scripts/e2e/chains.sh       # 链生命周期 + 存量编辑回归 + UDP 管道 + tls 中继真实流量
rm -rf src/backend/internal/web/dist && cp -r src/frontend/dist src/backend/internal/web/dist
bash scripts/e2e/links.sh        # 订阅/分享链接输出一致性（需前端 dist 就位）
LATX_ALLOW_PRIVATE_OUTBOUND=1 bash scripts/e2e/groups.sh   # 分组订阅与派生链
bash scripts/e2e/usernodes.sh    # 按用户节点操作
bash scripts/e2e/reconcile.sh    # 漂移自愈
bash scripts/e2e/vlessenc.sh     # vless Encryption 数据面
```

Expected: 每个脚本结尾均输出对应 `PASS`。任一失败即回退对应 Task 排查，不允许带病提交。

- [ ] **Step 9: 全仓回归 + Commit**

```bash
cd src/shared && go test ./... && cd ../backend && go test ./... && cd ../agent && go test ./...
cd ../frontend && npm run lint && npm test && npm run build
git add scripts/e2e/protocols.sh scripts/e2e/chains.sh
git commit -m "test(e2e): protocols 覆盖 tls 自签/ACME 失败路径与 pin 数据面；chains 覆盖 vless tls 中继真实流量"
```

---

## 自检记录

- **Spec 覆盖**：P3 范围 = TLS 安全层两模式（Task 2 shared 字段/占位符 → Task 3 panel 矩阵/模板/ACME 域名检测 → Task 4 agent 证书落地/隧道段 pin → Task 5 订阅四格式 pin/insecure/普通 TLS → Task 6 前端 TLS 区域 → Task 7 e2e）+ P2 终审遗留 7 项（#4/#5/#6/#7 在 Task 1；#1 在 Task 3；#2 在 Task 5；#3 在 Task 6——后三项依赖 tls 字段/模板/选择器，无法独立机械落地，已在各 Task 标题与步骤中标注裁定编号）+ 存量回归红线（Global Constraints + Task 7）。Hysteria2 与入口协议区块属 P4，不在本计划。
- **矩阵落地**：normalize tls 分支（合法：vless/vmess/trojan × 五传输 × tls；非法：trojan 推导 none、cert_mode 未知、伪装域名含端口、ss 显式 security；vision 扩展 reality|tls）逐项有测试（Task 3 Step 1）；前端镜像（Task 6 securityOptions/coerceSecurity）。
- **占位符扫描**：无 TBD/"适当处理"；全部测试与实现代码完整给出。一处有意保留的"以既有代码为准"：Task 7 Step 6 的 `chain_field` 字段路径（以 chains.sh 既有助手为准，含 DB 查询备选）。其余无执行分叉。
- **类型一致性**：`CertMode*/CertModes/PlaceholderTLSCertFile/PlaceholderTLSKeyFile`（Task 2）↔ Task 3/4 引用一致；`RealizedConfig.SNI/CertSHA256`（Task 2）↔ agent 上报（Task 4）↔ links/mihomo/singbox/quanx/endpoint outbound（Task 4/5）一致；`templateSecurity` tls 识别（Task 4）↔ `EffectiveSecurity` 三值（Task 2）↔ 订阅 switch（Task 5）一致；`applyACMEDomain/serverDomain`（Task 3）↔ 三处调用点（nodes.go:265 后、chains.go:301 后、chains.go:589 后）一致；`securityOptions/coerceSecurity`（Task 6）↔ 后端矩阵（Task 3）逐项对应。
- **证书占位符贯穿**（设计结论）：panel `tlsStreamSettings` 模板嵌入 `{{TLS_CERT_FILE}}/{{TLS_KEY_FILE}}` + VirtualConfig 携带 `cert_mode/tls_domain`（落库 nodes.config_template）→ agent `fillTemplate` 检测占位符 → `ensureTLSCertificate(tag, vc)` 按 tag 隔离目录幂等落地（selfsign=`xray tls cert`、acme=acme.sh）→ 绝对路径替换 → realized 上报 `SNI`（tlsSettings.serverName 提取）+ `CertSHA256`（自签 DER sha256 hex）→ 订阅按格式输出 pin/insecure/普通 TLS；链隧道段 `renderSharedEndpointOutbound` 以 `pinnedPeerCertSha256`（hex）钉住自签出口。重建/端点重发复用同一 fillTemplate 路径，证书文件持久 → 无 pin 轮换。
- **ACME 域名检测数据来源**（设计结论）：`servers.addresses` JSON 列表（`store.ParseServerAddresses` 解析）中首个 `shared.AddressFamily(a)=="domain"` 的条目；panel 三处处理器前置校验（无域名 400），前端用 `lib/address.ts addressFamily` 对已加载的 servers 列表做同判定实时禁用选项——无需新增 API。
- **存量兼容**：旧 realized（无 security）经 `EffectiveSecurity` 回退 reality/none 不变；tls 为新组合，无存量数据迁移；reality/none 模板 key 集不变（tlsStreamSettings 仅新分支）；`allowInsecure` 仅出现在自签 tls 链接（新增组合），reality 链接不变。
- **xray 版本事实**（本机 26.3.27 实测）：`xray tls cert -domain -name -file=<prefix>` 输出 `<prefix>.crt/.key`；客户端 `pinnedPeerCertSha256` 为 hex 字符串（`allowInsecure` 已移除）——CertSHA256 存 hex 一处两用（xray pin + mihomo fingerprint）。ws/grpc/httpupgrade 弃用警告不阻断（spec §5）。
