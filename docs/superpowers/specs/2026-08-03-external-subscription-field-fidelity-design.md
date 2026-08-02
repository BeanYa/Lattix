# 外部订阅字段保真设计

日期：2026-08-03

## 背景与问题

外部订阅（YAML/URI/v2rayN）解析后生成 clash YAML 时字段丢失。示例：源条目

```yaml
- { name: '🇭🇰 香港01', type: anytls, server: ..., port: 443, password: ...,
    alpn: [h2, http/1.1], skip-cert-verify: false, udp: true, sni: moe233-... }
```

生成结果为：

```yaml
- name: "🇭🇰 香港01"
  type: anytls
  server: ...
  port: 443
  password: ...
  sni: moe233-...
  udp: true
```

丢失 `alpn: [h2, http/1.1]`（功能性字段）与 `skip-cert-verify: false`（falsy 值被 omitempty 裁剪）。

**根因**：解析层（`extsub/parse_yaml.go`）已把全部字段保留进 `Node.Extra`，丢失全部发生在生成层——`clashProxy` 结构体字段不全，且 `omitempty` 语义无法区分"字段为 false"与"字段不存在"。

## 约定更正（2026-08-04）

原假设"anytls 的 mihomo 字段为 `servername`"有误。实测 mihomo 源码（Meta 分支 `adapter/outbound/`）的 proxy 标签：

| 协议 | mihomo 字段 |
|---|---|
| vless / vmess | `servername` |
| trojan / anytls / hysteria2 / tuic / http(tls) | `sni` |

mihomo 对未知字段静默忽略：此前输出 `servername` 的 anytls/hy2/tuic 节点在 mihomo 系客户端 SNI 为空，对 SNI 路由的中继（如 `relay.moe233.org`）TLS 握手失败、测速超时。已修正 `buildExternalClash`：上述协议 SNI 统一输出 `sni`（vless/vmess 保持 `servername`）。

## 目标

- 数据模型（`clashProxy`）覆盖完整 mihomo proxy schema，Lattix 自产订阅输出符合 mihomo YAML 约定。
- 生成时按"字段存在"回填（`skip-cert-verify: false` 等 falsy 值可保留）。
- 第三方站点的额外/认证字段（schema 外，如 `auth`/`token`/`api-key`）保留并回填（类型原样：列表/布尔/数字）。
- 覆盖全部 4 种生成格式：clash YAML、sing-box JSON、分享链接 URI、Quantumult X（尽力而为）。

## 设计决策

- **类型化结构体 + 未知键透传**：`clashProxy` 扩展为完整 mihomo schema（类型化字段，保证自产输出合规）；新增 `Raw map[string]any yaml:",inline"` 兜底，未被消费的 Extra 键并入生成 YAML。
- **消费键集合**：每个协议 case 声明它读取的全部键（规范键 + 别名键 + opts 键），填充完成后 `Raw = Extra ∖ 消费键`，避免 `sni`/`id`/`ws-opts` 子键重复输出。
- **presence 指针化**：`skip-cert-verify`/`reduce-rtt`/snell `version`/wg `mtu` 改为指针（`*bool`/`*int`），仅当源中存在该键才设置。
- **内部路径零变化**：面板自建节点（`buildProxy`）不填 Raw、不设新字段，输出与现状一致。
- **opts 嵌套优先**：YAML 源的嵌套对象（`ws-opts` 等）原样转换进类型化 opts（保留子字段），URI 源的扁平别名（`path`/`host`）回退构建。
- **sing-box/URI 同思路**：按 sing-box/URI 各自的字段约定映射消费键，未知键透传兜底（Go json 忽略未知字段，不破坏 sing-box 配置加载）。
- **QuanX**：单行格式天然受限，维持现有映射，文档注明局限。

## 数据模型（`clashProxy` 扩展）

```go
type clashProxy struct {
    Name   string `yaml:"name"`
    Type   string `yaml:"type"`
    Server string `yaml:"server"`
    Port   int    `yaml:"port"`

    UUID     string `yaml:"uuid,omitempty"`
    AlterID  *int   `yaml:"alterId,omitempty"`
    Cipher   string `yaml:"cipher,omitempty"`
    Password string `yaml:"password,omitempty"`
    Username string `yaml:"username,omitempty"`

    Network    string `yaml:"network,omitempty"`
    TLS        bool   `yaml:"tls,omitempty"`
    Servername string `yaml:"servername,omitempty"` // vless / vmess
    SNI        string `yaml:"sni,omitempty"`        // trojan / anytls / hysteria2 / tuic / http(tls)
    Flow       string `yaml:"flow,omitempty"`
    Encryption string `yaml:"encryption,omitempty"`
    PacketEncoding string `yaml:"packet-encoding,omitempty"` // vless
    ALPN       []string `yaml:"alpn,omitempty"`
    UDP        bool   `yaml:"udp"`

    RealityOpts       *clashRealityOpts `yaml:"reality-opts,omitempty"`
    ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
    GrpcOpts          *clashGrpcOpts    `yaml:"grpc-opts,omitempty"`
    XhttpOpts         *clashXHTTPOpts   `yaml:"xhttp-opts,omitempty"`
    H2Opts            *clashH2Opts      `yaml:"h2-opts,omitempty"`
    HTTPOpts          *clashHTTPOpts    `yaml:"http-opts,omitempty"`
    WsOpts            *clashWsOpts      `yaml:"ws-opts,omitempty"`
    Plugin            string            `yaml:"plugin,omitempty"`       // ss
    PluginOpts        map[string]any    `yaml:"plugin-opts,omitempty"`  // ss
    Smux              map[string]any    `yaml:"smux,omitempty"`
    ObfsOpts          map[string]any    `yaml:"obfs-opts,omitempty"`    // snell
    Fragment          map[string]any    `yaml:"fragment,omitempty"`
    DialerProxy       string            `yaml:"dialer-proxy,omitempty"`
    IPVersion         string            `yaml:"ip-version,omitempty"`

    Ports                string `yaml:"ports,omitempty"`
    SkipCertVerify       *bool  `yaml:"skip-cert-verify,omitempty"`
    Obfs                 string `yaml:"obfs,omitempty"`
    ObfsPassword         string `yaml:"obfs-password,omitempty"`
    Up                   string `yaml:"up,omitempty"`
    Down                 string `yaml:"down,omitempty"`
    Protocol             string `yaml:"protocol,omitempty"`
    ProtocolParam        string `yaml:"protocol-param,omitempty"`
    ObfsParam            string `yaml:"obfs-param,omitempty"`
    PSK                  string `yaml:"psk,omitempty"`
    Version              *int   `yaml:"version,omitempty"`  // snell
    IP                   string `yaml:"ip,omitempty"`
    IPv6                 string `yaml:"ipv6,omitempty"`     // wireguard
    Reserved             string `yaml:"reserved,omitempty"` // wireguard
    PrivateKey           string `yaml:"private-key,omitempty"`
    PublicKey            string `yaml:"public-key,omitempty"`
    PresharedKey         string `yaml:"preshared-key,omitempty"`
    MTU                  *int   `yaml:"mtu,omitempty"`
    CongestionController string `yaml:"congestion-controller,omitempty"`
    UDPRelayMode         string `yaml:"udp-relay-mode,omitempty"`
    ReduceRTT            *bool  `yaml:"reduce-rtt,omitempty"`  // tuic
    IdleSessionCheckInterval int `yaml:"idle-session-check-interval,omitempty"` // anytls
    IdleSessionTimeout      int `yaml:"idle-session-timeout,omitempty"`
    MinIdleSession          int `yaml:"min-idle-session,omitempty"`

    Raw map[string]any `yaml:",inline"` // 未消费的 Extra 键原样回填
}
```

opts 子结构补全：`ws-opts` 增加 `max-early-data`/`early-data-header-name`/`v2ray-http-upgrade`/`v2ray-http-upgrade-host`/`v2ray-http-upgrade-path`；`grpc-opts` 增加 `grpc-mode`；`http-opts` 增加 `method`；新增 `h2-opts`（host/path）。

## 回填逻辑（`buildExternalClash`）

1. 各协议 case 开头声明消费键集合（该协议读取的规范键 + 别名键 + opts 键）。
2. 现有映射逻辑保持，补充新字段：
   - `alpn`：新增 `extStrings` 助手（YAML 列表 `[]any` / URI 逗号串两种形态）。
   - `skip-cert-verify`：`extBoolPtr`，仅键存在时设置指针。
   - opts 结构：Extra 中嵌套对象存在时 JSON 往返转换进类型化结构（保子字段）；否则按现有扁平别名（path/host/mode/serviceName）回退构建。
3. 类型化字段填充完成后：`p.Raw = Extra 中不在消费键集合的键`。

## sing-box / URI / QuanX

- **sing-box**（`buildExternalSingbox`）：各协议 case 声明消费键集合；TLS 对象补 `alpn`；anytls 补 `idle_session`/`min_idle_session`；ss 补 `plugin`；wg 补 `ipv6`；其余按 sing-box 字段名映射。未知键透传进 base JSON。
- **URI**（`buildExternalLink`/`externalQuery`）：按协议抑制已消费键（`servername`/`public-key`/`client-fingerprint`/`ws-opts` 等不再重复出现），未知键继续透传（字符串化）。
- **QuanX**（`buildExternalQuanX`）：维持现有映射，不新增字段透传（格式限制）。

## 测试

- `external_clash_test.go`：指针字段断言更新（`*p.SkipCertVerify` 等）；新增完整回填用例：
  - anytls YAML 往返：`alpn: [h2, http/1.1]`、`skip-cert-verify: false`、`sni`（mihomo 规范字段）、无重复键；
  - 未知键透传：`auth: xxx` 原样出现在 YAML；
  - `ws-opts` 子字段（max-early-data 等）保真；
  - 面板自建节点（`buildProxy`）输出零变化。
- `external_singbox_test.go`：TLS alpn、anytls idle_session、未知键透传。
- `external_links_test.go`：已消费键抑制（不出现 servername 与 sni 并存）、未知键透传。
