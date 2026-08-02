# 外部订阅字段保真实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 外部订阅节点在 4 种输出格式（clash YAML / sing-box / URI / QuanX）下按"字段存在"回填：类型化结构覆盖完整 mihomo schema，未知字段透传兜底。

**Architecture:** 解析层（`extsub`，`Node.Extra` 已保留全部字段）不动；重做生成层。`clashProxy` 扩展为完整 mihomo proxy schema（类型化 + falsy 指针化），新增 `Raw map[string]any yaml:",inline"` 透传未消费键；`buildExternalClash` 每协议 case 声明消费键集合，`Raw = Extra ∖ 消费键`。sing-box / URI 同思路（各自字段约定 + 消费键抑制）。面板自建节点路径（`buildProxy`）输出零变化。

**Tech Stack:** Go 1.26（backend 模块 `lattix/backend`，go.work 包含 agent/backend/shared），`gopkg.in/yaml.v3`（已依赖）。

**Spec:** `docs/superpowers/specs/2026-08-03-external-subscription-field-fidelity-design.md`

## Global Constraints

- 所有 go 命令在 **WSL** 内执行：`wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && <cmd>"`（Windows 侧 Go 无法访问 WSL 路径）。
- **不改动** `src/backend/internal/extsub/`（解析层与 `Node` 结构体）。
- 面板自建节点（`sub.go` 的 `buildProxy`）输出必须零变化（不填 Raw、不设新字段）。
- 消费键集合必须覆盖"规范键 + 别名键 + opts 键"全部读取的键，否则会输出重复键。
- `clashProxy` 的 `UDP` 保持 `yaml:"udp"`（无 omitempty，外部节点恒为 true）。
- 代码风格：现有中文注释、助手函数复用；新增助手放 `external.go`。
- 提交信息使用现有风格（`feat:`/`fix:`/`test:` 前缀，中文或英文均可）。

---

### Task 1: 数据模型：clashProxy schema 扩展 + presence 助手

**Files:**
- Modify: `src/backend/internal/sub/sub.go`（clashProxy 及 opts 结构体）
- Modify: `src/backend/internal/sub/external.go`（presence 助手 + 编译修复）
- Test: `src/backend/internal/sub/external_clash_test.go`

**Interfaces:**
- Produces: `extStrings(extra map[string]any, keys ...string) []string`、`extBoolPtr(extra map[string]any, keys ...string) *bool`、`extIntPtr(extra map[string]any, keys ...string) *int`（后续 Task 2 直接复用）
- Produces: `clashProxy` 新增 `Raw map[string]any yaml:",inline"` 与全部 schema 字段；`SkipCertVerify`/`ReduceRTT`/`Version`/`MTU` 改为指针

- [ ] **Step 1: 扩展 clashProxy 与 opts 结构体（sub.go:206-278）**

替换 `clashRealityOpts`/`clashGrpcOpts`/`clashXHTTPOpts`/`clashProxy`/`clashWsOpts`/`clashHTTPOpts` 定义，并在其后新增 `clashH2Opts`：

```go
type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

type clashGrpcOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
	GrpcMode    string `yaml:"grpc-mode,omitempty"`
}

type clashXHTTPOpts struct {
	Path string `yaml:"path,omitempty"`
	Mode string `yaml:"mode,omitempty"`
	Host string `yaml:"host,omitempty"`
}

type clashH2Opts struct {
	Path string   `yaml:"path,omitempty"`
	Host []string `yaml:"host,omitempty"`
}

// clashProxy 是 mihomo 代理项并集结构：覆盖完整 proxy schema，
// 按协议填充相关字段（omitempty 裁剪）；Raw 透传未被类型化字段消费的
// Extra 键（yaml:",inline" 直接并入生成的 YAML 映射）。
type clashProxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`

	UUID           string `yaml:"uuid,omitempty"`            // vless / vmess
	AlterID        *int   `yaml:"alterId,omitempty"`         // vmess
	Cipher         string `yaml:"cipher,omitempty"`          // vmess=auto / ss=method
	Password       string `yaml:"password,omitempty"`        // trojan / ss / socks / http / anytls
	Username       string `yaml:"username,omitempty"`        // socks / http
	Network        string `yaml:"network,omitempty"`
	PacketEncoding string `yaml:"packet-encoding,omitempty"` // vless
	TLS            bool   `yaml:"tls,omitempty"`
	Servername     string `yaml:"servername,omitempty"`      // vless / vmess / anytls
	SNI            string `yaml:"sni,omitempty"`             // trojan
	Flow           string `yaml:"flow,omitempty"`            // vless
	Encryption     string `yaml:"encryption,omitempty"`      // vless
	ALPN           []string `yaml:"alpn,omitempty"`
	UDP            bool   `yaml:"udp"`

	RealityOpts       *clashRealityOpts `yaml:"reality-opts,omitempty"`
	ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
	GrpcOpts          *clashGrpcOpts    `yaml:"grpc-opts,omitempty"`
	XhttpOpts         *clashXHTTPOpts   `yaml:"xhttp-opts,omitempty"`
	H2Opts            *clashH2Opts      `yaml:"h2-opts,omitempty"`
	HTTPOpts          *clashHTTPOpts    `yaml:"http-opts,omitempty"`
	WsOpts            *clashWsOpts      `yaml:"ws-opts,omitempty"`
	Plugin            string            `yaml:"plugin,omitempty"`      // ss
	PluginOpts        map[string]any    `yaml:"plugin-opts,omitempty"` // ss
	Smux              map[string]any    `yaml:"smux,omitempty"`
	ObfsOpts          map[string]any    `yaml:"obfs-opts,omitempty"` // snell
	Fragment          map[string]any    `yaml:"fragment,omitempty"`
	DialerProxy       string            `yaml:"dialer-proxy,omitempty"`
	IPVersion         string            `yaml:"ip-version,omitempty"`

	Ports                string `yaml:"ports,omitempty"`                  // hysteria2 多端口
	SkipCertVerify       *bool  `yaml:"skip-cert-verify,omitempty"`
	Obfs                 string `yaml:"obfs,omitempty"`                   // hysteria2 / snell
	ObfsPassword         string `yaml:"obfs-password,omitempty"`
	Up                   string `yaml:"up,omitempty"`                     // hysteria2
	Down                 string `yaml:"down,omitempty"`                   // hysteria2
	Protocol             string `yaml:"protocol,omitempty"`               // ssr
	ProtocolParam        string `yaml:"protocol-param,omitempty"`
	ObfsParam            string `yaml:"obfs-param,omitempty"`
	PSK                  string `yaml:"psk,omitempty"`                    // snell
	Version              *int   `yaml:"version,omitempty"`                // snell
	IP                   string `yaml:"ip,omitempty"`                     // wireguard
	IPv6                 string `yaml:"ipv6,omitempty"`                   // wireguard
	Reserved             string `yaml:"reserved,omitempty"`               // wireguard
	PrivateKey           string `yaml:"private-key,omitempty"`
	PublicKey            string `yaml:"public-key,omitempty"`
	PresharedKey         string `yaml:"preshared-key,omitempty"`
	MTU                  *int   `yaml:"mtu,omitempty"`
	CongestionController string `yaml:"congestion-controller,omitempty"`  // tuic
	UDPRelayMode         string `yaml:"udp-relay-mode,omitempty"`         // tuic
	ReduceRTT            *bool  `yaml:"reduce-rtt,omitempty"`             // tuic
	IdleSessionCheckInterval int `yaml:"idle-session-check-interval,omitempty"` // anytls
	IdleSessionTimeout      int `yaml:"idle-session-timeout,omitempty"`   // anytls
	MinIdleSession          int `yaml:"min-idle-session,omitempty"`       // anytls

	Raw map[string]any `yaml:",inline"` // 未消费的 Extra 键原样回填
}

type clashWsOpts struct {
	Path                  string            `yaml:"path,omitempty"`
	Headers               map[string]string `yaml:"headers,omitempty"`
	MaxEarlyData          int               `yaml:"max-early-data,omitempty"`
	EarlyDataHeaderName   string            `yaml:"early-data-header-name,omitempty"`
	V2rayHTTPUpgrade      bool              `yaml:"v2ray-http-upgrade,omitempty"`
	V2rayHTTPUpgradeHost  string            `yaml:"v2ray-http-upgrade-host,omitempty"`
	V2rayHTTPUpgradePath  string            `yaml:"v2ray-http-upgrade-path,omitempty"`
}

type clashHTTPOpts struct {
	Method  string            `yaml:"method,omitempty"`
	Path    []string          `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}
```

- [ ] **Step 2: 新增 presence 助手（external.go:13-75 区域，放在 extInt 之后）**

```go
// extStrings 取 Extra 字符串列表（YAML 列表或 URI 逗号串两种形态）。
func extStrings(extra map[string]any, keys ...string) []string {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		if t == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(t, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return nil
}

// extBoolPtr 仅当键存在时返回布尔指针（presence 感知，false 也可保留）。
func extBoolPtr(extra map[string]any, keys ...string) *bool {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return nil
	}
	b := false
	switch t := v.(type) {
	case bool:
		b = t
	case string:
		switch strings.ToLower(t) {
		case "1", "true", "yes", "on":
			b = true
		}
	case float64:
		b = t != 0
	case int:
		b = t != 0
	}
	return &b
}

// extIntPtr 仅当键存在时返回整型指针。
func extIntPtr(extra map[string]any, keys ...string) *int {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return &n
		}
	case int:
		return &t
	case int64:
		n := int(t)
		return &n
	case uint64:
		n := int(t)
		return &n
	case float64:
		n := int(t)
		return &n
	}
	return nil
}
```

- [ ] **Step 3: 编译修复 buildExternalClash（external.go）**

- 第 135 行附近，clashProxy 字面量中 `SkipCertVerify: extBool(e, "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")` 改为 `SkipCertVerify: extBoolPtr(e, "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure")`。
- `case "tuic":` 中 `p.ReduceRTT = extBool(e, "reduce_rtt", "reduce-rtt")` 改为 `p.ReduceRTT = extBoolPtr(e, "reduce_rtt", "reduce-rtt")`。
- `case "wireguard":` 中 `p.MTU = extInt(e, "mtu")` 改为 `p.MTU = extIntPtr(e, "mtu")`。
- `case "snell":` 中 `p.Version = extInt(e, "version")` 改为 `p.Version = extIntPtr(e, "version")`。
- `applyExternalTransport` 中 `case "http", "h2":` 的 `p.HTTPOpts = &clashHTTPOpts{Path: extStr(e, "path")}` 改为 `p.HTTPOpts = &clashHTTPOpts{Path: []string{extStr(e, "path")}}`。

- [ ] **Step 4: 更新 external_clash_test.go 指针断言**

- `TestBuildExternalClash` 第 48 行：`p.MTU != 1420` → `p.MTU == nil || *p.MTU != 1420`。
- `TestBuildExternalClashYAMLBoolInt` 第 100 行：`if !p.SkipCertVerify {` → `if p.SkipCertVerify == nil || !*p.SkipCertVerify {`；第 109 行：`if wg.MTU != 1420 {` → `if wg.MTU == nil || *wg.MTU != 1420 {`。

- [ ] **Step 5: 新增 inline 透传 marshal 测试（external_clash_test.go 末尾）**

注意：yaml.v3 的 inline map 与类型化字段**同名键会 panic**（`cannot have key "alpn" in inlined map: conflicts with struct field`）。测试的 Raw 键必须用不在 clashProxy 字段中的真实 mihomo 键（如 `tfo`），不能用 `alpn`（它已被类型化字段 ALPN 消费）。

在文件顶部 import 增加 `"gopkg.in/yaml.v3"`，末尾追加：

```go
func TestClashProxyInlineRawMarshal(t *testing.T) {
	p := clashProxy{
		Name: "n", Type: "anytls", Server: "s", Port: 443, UDP: true,
		Raw: map[string]any{"tfo": true, "x-extra": []any{"h2", "http/1.1"}},
	}
	out, err := yaml.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"tfo: true", "x-extra:", "- h2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("raw not inlined, missing %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "name:") != 1 {
		t.Fatalf("duplicate name key:\n%s", s)
	}
}
```

- [ ] **Step 6: 运行测试**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && go test ./backend/internal/sub/... ./backend/internal/extsub/... 2>&1 | tail -10"
```

预期：全部 PASS。

- [ ] **Step 7: 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity && git add -A && git commit -m 'feat(sub): extend clashProxy to full mihomo proxy schema'"
```

---

### Task 2: buildExternalClash 回填重构：消费键集合 + Raw 透传 + opts 嵌套保真

**Files:**
- Modify: `src/backend/internal/sub/external.go`
- Test: `src/backend/internal/sub/external_clash_test.go`

**Interfaces:**
- Consumes: Task 1 的 `extStrings`/`extBoolPtr`/`extIntPtr` 与扩展后的 `clashProxy`
- Produces: `externalTLSKeys(extra ...string) map[string]bool`、`externalKeys(keys ...string) map[string]bool`、`externalRaw(extra map[string]any, consumed map[string]bool) map[string]any`、`optsStruct[T any](e map[string]any, key string) (T, bool)`、`rawMap(v any) map[string]any`、`externalRealityOpts(e map[string]any) *clashRealityOpts`、`applyExternalCommons(p *clashProxy, e map[string]any)`（后续 Task 3/4 不依赖，仅本包使用）

- [ ] **Step 1: 新增消费键与转换助手（external.go 顶部，extBoolPtr 之后）**

文件 import 增加 `"encoding/json"`。新增：

```go
// tlsConsumedKeys 是 TLS 系协议消费的公共 Extra 键（规范键 + 别名 + opts 键）。
var tlsConsumedKeys = []string{
	"udp", "alpn", "tls", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure",
	"sni", "servername", "client-fingerprint", "fragment", "dialer-proxy", "ip-version", "smux",
	"reality-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts",
	"path", "host", "mode", "serviceName", "service_name",
}

// clashProxyYamlKeys 是 clashProxy 全部类型化字段的 yaml 键名。
// 用途：inline Raw 与类型化字段同名键会在 yaml.v3 marshal 时 panic，
// 因此 Raw 回填必须排除这些键（它们未被协议消费时按 schema 字段处理，静默丢弃）。
var clashProxyYamlKeys = func() map[string]bool {
	keys := []string{
		"name", "type", "server", "port",
		"uuid", "alterId", "cipher", "password", "username",
		"network", "packet-encoding", "tls", "servername", "sni", "flow", "encryption",
		"alpn", "udp", "reality-opts", "client-fingerprint", "grpc-opts", "xhttp-opts",
		"h2-opts", "http-opts", "ws-opts", "plugin", "plugin-opts", "smux", "obfs-opts",
		"fragment", "dialer-proxy", "ip-version", "ports", "skip-cert-verify", "obfs",
		"obfs-password", "up", "down", "protocol", "protocol-param", "obfs-param", "psk",
		"version", "ip", "ipv6", "reserved", "private-key", "public-key", "preshared-key",
		"mtu", "congestion-controller", "udp-relay-mode", "reduce-rtt",
		"idle-session-check-interval", "idle-session-timeout", "min-idle-session",
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}()

// externalKeys 构建消费键集合。
func externalKeys(keys ...string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// externalTLSKeys 构建 TLS 系协议消费键集合（公共键 + 协议专有键）。
func externalTLSKeys(extra ...string) map[string]bool {
	keys := append([]string{}, tlsConsumedKeys...)
	keys = append(keys, extra...)
	return externalKeys(keys...)
}

// externalRaw 返回 Extra 中未被消费的键（未知字段透传回填）。
// 排除与类型化字段同名的键：yaml.v3 inline 对同名键直接 panic，
// 未被协议消费的 schema 字段键按 schema 字段处理（静默丢弃）。
func externalRaw(extra map[string]any, consumed map[string]bool) map[string]any {
	raw := make(map[string]any)
	for key, value := range extra {
		if !consumed[key] && !clashProxyYamlKeys[key] {
			raw[key] = value
		}
	}
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// rawMap 把任意值转换为 map[string]any（YAML 嵌套对象），否则返回 nil。
func rawMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// optsStruct 把 Extra 中的嵌套 opts 对象转换为类型化结构；
// 键不存在或类型不符时返回零值结构与 false。
func optsStruct[T any](e map[string]any, key string) (T, bool) {
	var out T
	raw, ok := e[key].(map[string]any)
	if !ok {
		return out, false
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return out, false
	}
	if err := json.Unmarshal(buf, &out); err != nil {
		return out, false
	}
	return out, true
}
```

- [ ] **Step 2: 重写 buildExternalClash（external.go:125-261 整段替换）**

```go
// buildExternalClash 把外部订阅节点编译为 mihomo 代理项。
// 凭据取自 config（外部节点没有面板派发的用户 UUID）。
// 每个协议 case 声明消费键集合；类型化字段填充完后，未被消费的 Extra 键
// 原样回填到 Raw（yaml inline 并入输出），实现未知字段透传。
func buildExternalClash(n extsub.Node) (clashProxy, error) {
	if n.Name == "" {
		return clashProxy{}, fmt.Errorf("外部节点未命名：缺少名称/地址/端口")
	}
	if n.Server == "" || n.Port == 0 {
		return clashProxy{}, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := externalYAMLFallback(n.Extra)
	p := clashProxy{
		Name: n.Name, Server: n.Server, Port: n.Port, UDP: true,
		SkipCertVerify: extBoolPtr(e, "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure"),
	}
	var consumed map[string]bool
	switch n.Type {
	case "vless":
		consumed = externalTLSKeys("id", "uuid", "type", "network", "flow", "encryption", "security", "pbk", "sid", "fp", "packet-encoding")
		p.Type = "vless"
		p.UUID = extStr(e, "id")
		p.PacketEncoding = extStr(e, "packet-encoding")
		p.Network = externalNetwork(e, "type", "network")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		p.Flow = extStr(e, "flow")
		p.Encryption = extStr(e, "encryption")
		switch extStr(e, "security") {
		case "reality":
			p.TLS = true
			p.RealityOpts = externalRealityOpts(e)
			p.ClientFingerprint = extStr(e, "fp")
		case "tls":
			p.TLS = true
			p.ClientFingerprint = extStr(e, "fp")
		default:
			if _, ok := e["reality-opts"]; ok || extStr(e, "pbk") != "" {
				p.TLS = true
				p.RealityOpts = externalRealityOpts(e)
				p.ClientFingerprint = extStr(e, "fp")
			}
		}
		p.Servername = extStr(e, "sni")
		applyExternalTransport(&p, e)
	case "vmess":
		zero := 0
		consumed = externalTLSKeys("id", "uuid", "net", "aid", "scy", "tls", "cipher")
		p.Type = "vmess"
		p.UUID = extStr(e, "id")
		p.AlterID = &zero
		p.Cipher = "auto"
		if c := extStr(e, "cipher"); c != "" {
			p.Cipher = c
		}
		p.Network = externalNetwork(e, "net")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		if extStr(e, "tls") == "tls" {
			p.TLS = true
			p.Servername = extStr(e, "sni")
		}
		applyExternalTransport(&p, e)
	case "trojan":
		consumed = externalTLSKeys("password", "type", "network")
		p.Type = "trojan"
		p.Password = extStr(e, "password")
		p.TLS = true
		p.SNI = extStr(e, "sni")
		p.Network = externalNetwork(e, "type")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		applyExternalTransport(&p, e)
	case "ss":
		consumed = externalKeys("method", "password", "plugin", "plugin-opts", "smux", "udp", "fragment", "dialer-proxy", "ip-version", "alpn")
		p.Type = "ss"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
		p.Plugin = extStr(e, "plugin")
		if m := rawMap(e["plugin-opts"]); m != nil {
			p.PluginOpts = m
		}
	case "ssr":
		consumed = externalKeys("method", "password", "protocol", "protocol_param", "protocol-param", "obfs", "obfs_param", "obfs-param", "udp", "fragment", "dialer-proxy", "ip-version", "alpn", "smux")
		p.Type = "ssr"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
		p.Protocol = extStr(e, "protocol")
		p.ProtocolParam = extStr(e, "protocol_param", "protocol-param")
		p.Obfs = extStr(e, "obfs")
		p.ObfsParam = extStr(e, "obfs_param", "obfs-param")
	case "hysteria2":
		consumed = externalTLSKeys("password", "mport", "ports", "up", "down", "obfs", "obfs-password", "obfs_password", "peername")
		p.Type = "hysteria2"
		p.Password = extStr(e, "password")
		p.Ports = extStr(e, "mport", "ports")
		p.Obfs = extStr(e, "obfs")
		p.ObfsPassword = extStr(e, "obfs-password", "obfs_password")
		p.Up = extStr(e, "up")
		p.Down = extStr(e, "down")
		p.Servername = extStr(e, "sni", "peername")
	case "tuic":
		consumed = externalTLSKeys("uuid", "password", "congestion_controller", "congestion-controller", "congestion_control", "udp_relay_mode", "udp-relay-mode", "reduce_rtt", "reduce-rtt")
		p.Type = "tuic"
		p.UUID = extStr(e, "uuid")
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
		p.CongestionController = extStr(e, "congestion_controller", "congestion-controller", "congestion_control")
		p.UDPRelayMode = extStr(e, "udp_relay_mode", "udp-relay-mode")
		p.ReduceRTT = extBoolPtr(e, "reduce_rtt", "reduce-rtt")
	case "wireguard":
		consumed = externalKeys("ip", "address", "ipv6", "private_key", "private-key", "public_key", "pk", "public-key", "preshared_key", "preshared-key", "psk", "mtu", "reserved", "udp", "fragment", "dialer-proxy", "ip-version", "alpn", "smux")
		p.Type = "wireguard"
		p.IP = extStr(e, "ip", "address")
		p.IPv6 = extStr(e, "ipv6")
		p.Reserved = extStr(e, "reserved")
		p.PrivateKey = extStr(e, "private_key", "private-key")
		p.PublicKey = extStr(e, "public_key", "pk")
		p.PresharedKey = extStr(e, "preshared_key", "preshared-key", "psk")
		p.MTU = extIntPtr(e, "mtu")
	case "anytls":
		consumed = externalTLSKeys("password", "idle-session-check-interval", "idle-session-timeout", "min-idle-session")
		p.Type = "anytls"
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
		p.IdleSessionCheckInterval = extInt(e, "idle-session-check-interval")
		p.IdleSessionTimeout = extInt(e, "idle-session-timeout")
		p.MinIdleSession = extInt(e, "min-idle-session")
	case "snell":
		consumed = externalKeys("psk", "obfs", "obfs-opts", "version", "udp", "fragment", "dialer-proxy", "ip-version", "alpn", "smux")
		p.Type = "snell"
		p.PSK = extStr(e, "psk")
		p.Obfs = extStr(e, "obfs")
		p.ObfsOpts = rawMap(e["obfs-opts"])
		p.Version = extIntPtr(e, "version")
	case "socks":
		consumed = externalKeys("username", "password", "tls", "udp", "fragment", "dialer-proxy", "ip-version", "alpn", "smux")
		p.Type = "socks5"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
	case "http":
		consumed = externalTLSKeys("username", "password", "tls")
		p.Type = "http"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
		p.UDP = false
		if extStr(e, "tls") == "tls" {
			p.TLS = true
			p.Servername = extStr(e, "sni")
		}
	default:
		return clashProxy{}, fmt.Errorf("外部节点「%s」未知协议 %s", n.Name, n.Type)
	}
	switch p.Type {
	case "vless", "vmess", "tuic":
		if p.UUID == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少凭据", n.Name)
		}
	case "trojan", "ss", "ssr", "hysteria2", "anytls", "snell":
		if p.Password == "" && p.PSK == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少凭据", n.Name)
		}
	case "wireguard":
		if p.PrivateKey == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少 private_key", n.Name)
		}
	}
	applyExternalCommons(&p, e)
	p.Raw = externalRaw(n.Extra, consumed)
	return p, nil
}

// applyExternalCommons 填充跨协议通用字段（alpn/smux/fragment/dialer-proxy/ip-version/tls）。
func applyExternalCommons(p *clashProxy, e map[string]any) {
	if alpn := extStrings(e, "alpn"); len(alpn) > 0 {
		p.ALPN = alpn
	}
	if extBool(e, "tls") {
		p.TLS = true
	}
	if m := rawMap(e["smux"]); m != nil {
		p.Smux = m
	}
	if m := rawMap(e["fragment"]); m != nil {
		p.Fragment = m
	}
	p.DialerProxy = extStr(e, "dialer-proxy")
	p.IPVersion = extStr(e, "ip-version")
}

// externalRealityOpts 优先取嵌套 reality-opts 原样转换，缺省时用扁平 pbk/sid 回退。
func externalRealityOpts(e map[string]any) *clashRealityOpts {
	if ro, ok := optsStruct[clashRealityOpts](e, "reality-opts"); ok {
		return &ro
	}
	if extStr(e, "pbk") != "" {
		return &clashRealityOpts{PublicKey: extStr(e, "pbk"), ShortID: extStr(e, "sid")}
	}
	return &clashRealityOpts{}
}
```

- [ ] **Step 3: 重写 applyExternalTransport（external.go:264-283 整段替换）**

```go
// applyExternalTransport 填充外部节点传输层选项：优先从 Extra 嵌套 opts
// 对象原样转换（YAML 源保子字段），缺省时用扁平别名（path/host 等）回退构建。
func applyExternalTransport(p *clashProxy, e map[string]any) {
	switch p.Network {
	case "ws":
		opts, ok := optsStruct[clashWsOpts](e, "ws-opts")
		if !ok {
			opts = clashWsOpts{Path: extStr(e, "path")}
			if host := extStr(e, "host"); host != "" {
				opts.Headers = map[string]string{"Host": host}
			}
		}
		p.WsOpts = &opts
	case shared.NetworkGRPC:
		opts, ok := optsStruct[clashGrpcOpts](e, "grpc-opts")
		if !ok {
			opts = clashGrpcOpts{ServiceName: extStr(e, "serviceName", "service_name")}
		}
		p.GrpcOpts = &opts
	case shared.NetworkXHTTP:
		opts, ok := optsStruct[clashXHTTPOpts](e, "xhttp-opts")
		if !ok {
			opts = clashXHTTPOpts{
				Path: extStr(e, "path"), Mode: extStr(e, "mode"), Host: extStr(e, "host"),
			}
		}
		p.XhttpOpts = &opts
	case "h2":
		opts, ok := optsStruct[clashH2Opts](e, "h2-opts")
		if !ok {
			opts = clashH2Opts{Path: extStr(e, "path")}
			if host := extStr(e, "host"); host != "" {
				opts.Host = []string{host}
			}
		}
		p.H2Opts = &opts
	case "http":
		opts, ok := optsStruct[clashHTTPOpts](e, "http-opts")
		if !ok {
			opts = clashHTTPOpts{Path: []string{extStr(e, "path")}}
			if host := extStr(e, "host"); host != "" {
				opts.Headers = map[string]string{"Host": host}
			}
		}
		p.HTTPOpts = &opts
	}
}
```

- [ ] **Step 4: 新增保真测试（external_clash_test.go 末尾追加 3 个测试）**

```go
func TestBuildExternalClashAnyTLSFullFidelity(t *testing.T) {
	nodes, _, err := extsub.ParseSubscription([]byte(`
proxies:
  - name: '🇭🇰 香港01'
    type: anytls
    server: relay.moe233.org
    port: 443
    password: pass-123
    alpn: [h2, http/1.1]
    skip-cert-verify: false
    udp: true
    sni: sni.moe233.org
    auth: token-abc
`))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse = %+v err %v", nodes, err)
	}
	p, err := buildExternalClash(nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if p.Servername != "sni.moe233.org" {
		t.Fatalf("servername = %q", p.Servername)
	}
	if p.SkipCertVerify == nil || *p.SkipCertVerify {
		t.Fatalf("skip-cert-verify: false presence lost: %+v", p.SkipCertVerify)
	}
	if len(p.ALPN) != 2 || p.ALPN[0] != "h2" || p.ALPN[1] != "http/1.1" {
		t.Fatalf("alpn = %+v", p.ALPN)
	}
	if p.Raw["auth"] != "token-abc" {
		t.Fatalf("unknown key not preserved: %+v", p.Raw)
	}
	if _, dup := p.Raw["sni"]; dup {
		t.Fatalf("consumed sni leaked to raw: %+v", p.Raw)
	}
	out, err := yaml.Marshal(clashConfig{Proxies: []clashProxy{p}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"alpn:", "- h2", "skip-cert-verify: false", "servername: sni.moe233.org", "auth: token-abc"} {
		if !strings.Contains(s, want) {
			t.Fatalf("yaml missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "sni:") || strings.Contains(s, "password: pass-123") && strings.Count(s, "auth:") != 1 {
		t.Fatalf("yaml leakage:\n%s", s)
	}
}

func TestBuildExternalClashWSOptsSubfields(t *testing.T) {
	p, err := buildExternalClash(extNode("yaml-ws", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "network": "ws",
		"ws-opts": map[string]any{
			"path": "/ws", "max-early-data": 1024,
			"headers": map[string]any{"Host": "h.example.com"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.WsOpts == nil || p.WsOpts.Path != "/ws" || p.WsOpts.MaxEarlyData != 1024 ||
		p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Fatalf("ws-opts subfields lost: %+v", p.WsOpts)
	}
	if _, dup := p.Raw["ws-opts"]; dup {
		t.Fatalf("consumed ws-opts leaked to raw: %+v", p.Raw)
	}
}

func TestBuildExternalClashNoRawForKnownKeys(t *testing.T) {
	p, err := buildExternalClash(extNode("clean", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Raw != nil && len(p.Raw) != 0 {
		t.Fatalf("known keys leaked to raw: %+v", p.Raw)
	}
}
```

- [ ] **Step 5: 运行测试**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && go test ./backend/internal/sub/... 2>&1 | tail -15"
```

预期：全部 PASS（现有测试 + 新增 3 个）。注意 `TestBuildExternalClashAnyTLSFullFidelity` 末尾对 `sni:` 的断言：YAML 输出中不允许出现 `sni:` 键（anytls 用 servername）。

- [ ] **Step 6: 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity && git add -A && git commit -m 'feat(sub): preserve full field fidelity in external clash yaml generation'"
```

---

### Task 3: sing-box 保真：alpn/idle-session/plugin/ipv6 + 未知键透传

**Files:**
- Modify: `src/backend/internal/sub/external_singbox.go`
- Test: `src/backend/internal/sub/external_singbox_test.go`

**Interfaces:**
- Consumes: Task 1 的 `extStrings`；`rawMap`（Task 2 定义，同包复用）
- Produces: `externalSingboxKeys(keys ...string) map[string]bool`、`applySingboxRaw(base map[string]any, extra map[string]any, consumed map[string]bool)`（本包使用）

- [ ] **Step 1: 新增助手（external_singbox.go 顶部）**

```go
// externalSingboxKeys 构建 sing-box 消费键集合。
func externalSingboxKeys(keys ...string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// applySingboxRaw 把未被消费的 Extra 键透传进 sing-box 出站 JSON
// （Go json 忽略未知字段，兜底不破坏配置加载）。
func applySingboxRaw(base map[string]any, extra map[string]any, consumed map[string]bool) {
	for key, value := range extra {
		if !consumed[key] {
			base[key] = value
		}
	}
}
```

- [ ] **Step 2: 改造 buildExternalSingbox（external_singbox.go:13-113）**

在每个 case 开头声明 `consumed := externalSingboxKeys(...)`，并在 switch 之后统一调用 `applySingboxRaw(base, n.Extra, consumed)`。各 case 消费键与新增字段：

- `vless`：`consumed = externalSingboxKeys("id", "uuid", "type", "network", "flow", "security", "pbk", "sid", "fp", "client-fingerprint", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")`
- `vmess`：`consumed = externalSingboxKeys("id", "uuid", "net", "aid", "scy", "tls", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")`
- `trojan`：`consumed = externalSingboxKeys("password", "type", "network", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")`
- `ss`：`consumed = externalSingboxKeys("method", "password", "plugin", "plugin-opts")`，并在 case 内新增：

```go
	case "ss":
		base["method"] = extStr(e, "method")
		base["password"] = extStr(e, "password")
		if plugin := extStr(e, "plugin"); plugin != "" {
			base["plugin"] = plugin
			if opts := rawMap(e["plugin-opts"]); opts != nil {
				base["plugin_opts"] = opts
			}
		}
```

- `hysteria2`：`consumed = externalSingboxKeys("password", "obfs", "obfs-password", "obfs_password", "sni", "peername", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")`（up/down/ports/mport 未被 sing-box 字段消费，不列入，随未知键透传）
- `tuic`：`consumed = externalSingboxKeys("uuid", "password", "congestion_controller", "congestion-controller", "congestion_control", "udp_relay_mode", "udp-relay-mode", "reduce_rtt", "reduce-rtt", "sni", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")`
- `wireguard`：`consumed = externalSingboxKeys("ip", "address", "ipv6", "private_key", "private-key", "public_key", "pk", "public-key", "preshared_key", "preshared-key", "psk", "mtu", "reserved")`，case 内 `base["local_address"] = []string{extStr(e, "ip", "address")}` 替换为：

```go
		addr := []string{extStr(e, "ip", "address")}
		if v6 := extStr(e, "ipv6"); v6 != "" {
			for _, part := range strings.FieldsFunc(v6, func(r rune) bool { return r == ',' || r == ' ' }) {
				addr = append(addr, part)
			}
		}
		base["local_address"] = addr
```

- `socks`, `http`：`consumed = externalSingboxKeys("username", "password")`
- `anytls`：`consumed = externalSingboxKeys("password", "sni", "servername", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify", "idle-session-check-interval", "idle-session-timeout", "min-idle-session")`，case 内 `base["tls"] = externalSingboxTLSSimple(e)` 之后追加：

```go
		if v := extInt(e, "idle-session-check-interval"); v > 0 {
			base["idle_session_check_interval"] = v
		}
		if v := extInt(e, "idle-session-timeout"); v > 0 {
			base["idle_session_timeout"] = v
		}
		if v := extInt(e, "min-idle-session"); v > 0 {
			base["min_idle_session"] = v
		}
```

- [ ] **Step 3: externalSingboxTLSSimple 补 alpn（external_singbox.go:130-135）**

```go
// externalSingboxTLSSimple 构造普通 TLS 对象。
func externalSingboxTLSSimple(e map[string]any) map[string]any {
	tls := map[string]any{
		"enabled": true, "server_name": extStr(e, "sni"),
		"insecure": extBool(e, "insecure", "allowInsecure", "allow_insecure"),
	}
	if alpn := extStrings(e, "alpn"); len(alpn) > 0 {
		tls["alpn"] = alpn
	}
	return tls
}
```

- [ ] **Step 4: 新增测试（external_singbox_test.go 末尾追加）**

文件顶部 import 改为 `import ("reflect"; "testing")`，末尾追加：

```go
func TestBuildExternalSingboxALPNIdleAndUnknown(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("any", "anytls", "1.2.3.4", 443, map[string]any{
		"password": "pw", "sni": "a.example.com", "alpn": []any{"h2"},
		"idle-session-check-interval": 30, "auth": "token-9",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	tls := m["tls"].(map[string]any)
	if !reflect.DeepEqual(tls["alpn"], []any{"h2"}) {
		t.Fatalf("alpn = %+v", tls["alpn"])
	}
	if m["idle_session_check_interval"] != 30 {
		t.Fatalf("idle_session_check_interval = %+v", m["idle_session_check_interval"])
	}
	if m["auth"] != "token-9" {
		t.Fatalf("unknown key not preserved: %+v", m)
	}
	if _, dup := m["sni"]; dup {
		t.Fatalf("consumed sni leaked: %+v", m)
	}
}

func TestBuildExternalSingboxWGIPv6(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("wg", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "priv", "ip": "10.0.0.2", "ipv6": "fd00::1, fd00::2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	addr := m["local_address"].([]string)
	if len(addr) != 3 || addr[1] != "fd00::1" || addr[2] != "fd00::2" {
		t.Fatalf("local_address = %+v", addr)
	}
}
```

- [ ] **Step 5: 运行测试**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && go test ./backend/internal/sub/... 2>&1 | tail -15"
```

预期：全部 PASS。

- [ ] **Step 6: 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity && git add -A && git commit -m 'feat(sub): preserve external fields in sing-box outbound generation'"
```

---

### Task 4: URI 保真：已消费键抑制 + 未知标量透传

**Files:**
- Modify: `src/backend/internal/sub/external_links.go`
- Test: `src/backend/internal/sub/external_links_test.go`

**Interfaces:**
- Consumes: 无新助手依赖（`externalQuery` 保持签名 `externalQuery(extra map[string]any, skip ...string) string`）

- [ ] **Step 1: externalQuery 跳过非标量值（external_links.go:95-112）**

URI query 无法表达 map/列表，非标量值（YAML 嵌套对象）不再输出空参数；标量未知键继续透传。替换函数体：

```go
// externalQuery 把 Extra 剩余键值重建为 URL query（排序保证确定性）；
// 抑制已消费键（skip），非标量值（嵌套对象/列表）无法在 query 中表达故跳过。
func externalQuery(extra map[string]any, skip ...string) string {
	skipped := map[string]bool{}
	for _, key := range skip {
		skipped[key] = true
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		if !skipped[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := extStr(extra, key)
		if value == "" {
			continue
		}
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	return strings.Join(parts, "&")
}
```

- [ ] **Step 2: 各协议调用点扩展 skip 列表（external_links.go:22-89）**

把各 `externalQuery` 调用点的 skip 参数替换为（覆盖 YAML 规范键 + 已折算别名）：

- `vless`（第 23 行）：`externalQuery(e, "id", "uuid", "network", "servername", "client-fingerprint", "reality-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure", "fragment", "dialer-proxy", "ip-version", "smux")`
- `trojan`（第 25 行）：`externalQuery(e, "password", "network", "servername", "client-fingerprint", "reality-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure", "fragment", "dialer-proxy", "ip-version", "smux")`
- `hysteria2`（第 27 行）：`externalQuery(e, "password", "servername", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure", "fragment", "dialer-proxy", "ip-version", "smux", "obfs-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts")`
- `tuic`（第 29 行）：`externalQuery(e, "uuid", "password", "servername", "client-fingerprint", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure", "fragment", "dialer-proxy", "ip-version", "smux", "reality-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts")`
- `anytls`（第 31 行）：`externalQuery(e, "password", "servername", "client-fingerprint", "skip-cert-verify", "insecure", "allowInsecure", "allow_insecure", "fragment", "dialer-proxy", "ip-version", "smux", "reality-opts", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts")`
- `snell`（第 33 行）：`externalQuery(e, "psk", "obfs-opts", "smux")`
- `socks`（第 38 行）：`externalQuery(e, "username", "password", "servername", "tls", "sni")`
- `http`（第 42 行）：`externalQuery(e, "username", "password", "servername", "sni")`
- `ss`（第 60 行）：`externalQuery(e, "method", "password", "smux", "udp")`
- `ssr`（第 69 行）：`externalQuery(e, "protocol", "method", "obfs", "password", "remarks", "protocol_param", "protocol-param", "obfs_param", "obfs-param", "udp")`
- `wireguard`（第 78 行）：`externalQuery(e, "private_key", "endpoint", "pk", "public_key", "private-key", "public-key", "preshared_key", "preshared-key", "psk", "address", "mtu", "reserved", "ipv6")`（保留 `ip` 与未知键透传）
- `vmess`（第 50-56 行）：保持现状不动（JSON payload 原样携带全部键）。

注意：不要动 `externalQuery` 调用点的**返回值赋值**逻辑，只替换 skip 参数列表。

- [ ] **Step 3: 新增测试（external_links_test.go 末尾追加）**

```go
func TestBuildExternalLinkYAMLKeySuppression(t *testing.T) {
	link, ok := buildExternalLink(extNode("yaml-v", "vless", "1.2.3.4", 443, map[string]any{
		"uuid": "11111111-2222-3333-4444-555555555555", "network": "tcp",
		"servername": "cdn.example.com", "client-fingerprint": "chrome", "auth": "k9",
	}))
	if !ok {
		t.Fatal("link failed")
	}
	for _, leaked := range []string{"servername=", "client-fingerprint=", "network=", "reality-opts="} {
		if strings.Contains(link, leaked) {
			t.Fatalf("consumed key leaked %q: %q", leaked, link)
		}
	}
	if !strings.Contains(link, "sni=cdn.example.com") || !strings.Contains(link, "auth=k9") {
		t.Fatalf("expected sni/auth in link: %q", link)
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Extra["sni"] != "cdn.example.com" || back.Extra["auth"] != "k9" {
		t.Fatalf("round trip lost keys: %+v", back.Extra)
	}
}
```

- [ ] **Step 4: 运行测试**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && go test ./backend/internal/sub/... 2>&1 | tail -15"
```

预期：全部 PASS（现有 `TestBuildExternalLink` 的 `pbk=pub`/`type=tcp` 断言不受影响）。

- [ ] **Step 5: 提交**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity && git add -A && git commit -m 'feat(sub): suppress consumed keys and pass through scalars in external share links'"
```

---

### Task 5: 全量验证与收尾

**Files:**
- 无代码改动（如 Task 1-4 全部通过）

- [ ] **Step 1: 运行 backend 全量测试**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity/src && go build ./... && go test ./... 2>&1 | tail -25"
```

预期：所有包 PASS（含 panel/store/agent 等）。

- [ ] **Step 2: 确认 worktree 无遗留文件**

```bash
wsl -d Ubuntu -- bash -lc "cd /home/bean/workspace/Lattix-codex/.worktree/field-fidelity && git status --short && git log --oneline -6"
```

预期：工作区干净，4 个 feat 提交（Task 1-4 各一个）。
