package sub

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/extsub"
	"lattix/shared"
)

// optsStruct 把 Extra 中指定键的嵌套对象解码为 clash 选项结构体。
func optsStruct[T any](extra map[string]any, key string) (T, bool) {
	var out T
	raw, ok := extra[key]
	if !ok {
		return out, false
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return out, false
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return out, false
	}
	return out, true
}

// firstValue 返回 Extra 中第一个存在键的值。
func firstValue(extra map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if v, ok := extra[key]; ok {
			return v, true
		}
	}
	return nil, false
}

// extStr 按序取 Extra 中第一个存在的字符串值。
func extStr(extra map[string]any, keys ...string) string {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// extBool 判断 Extra 布尔值（1/true/yes/on，或原生 bool/数值）。
func extBool(extra map[string]any, keys ...string) bool {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(t) {
		case "1", "true", "yes", "on":
			return true
		}
	case float64:
		return t != 0
	}
	return false
}

// extInt 取 Extra 整数值。
func extInt(extra map[string]any, keys ...string) int {
	v, ok := firstValue(extra, keys...)
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	case int:
		return t
	case int64:
		return int(t)
	case uint64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

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

// externalYAMLFallback 把 mihomo YAML 订阅的 Extra 键补齐为分享链接约定键
// （uuid→id、network→type、client-fingerprint→fp，并展开 reality-opts/ws-opts/
// grpc-opts/http-opts 嵌套对象）。返回浅拷贝，不修改调用方 map。
func externalYAMLFallback(extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra))
	for key, value := range extra {
		out[key] = value
	}
	fill := func(key string, value any) {
		if _, exists := out[key]; !exists && value != nil && value != "" {
			out[key] = value
		}
	}
	fill("id", extStr(out, "uuid"))
	fill("type", extStr(out, "network"))
	fill("net", extStr(out, "network"))          // vmess YAML 的 network
	fill("sni", extStr(out, "servername"))       // vless YAML 的 reality SNI
	fill("public_key", extStr(out, "public-key")) // wireguard YAML
	fill("fp", extStr(out, "client-fingerprint"))
	if opts, ok := out["reality-opts"].(map[string]any); ok {
		fill("pbk", opts["public-key"])
		fill("sid", opts["short-id"])
	}
	if opts, ok := out["ws-opts"].(map[string]any); ok {
		fill("path", opts["path"])
		if headers, ok := opts["headers"].(map[string]any); ok {
			fill("host", headers["Host"])
		}
	}
	if opts, ok := out["grpc-opts"].(map[string]any); ok {
		fill("serviceName", opts["service-name"])
	}
	if opts, ok := out["http-opts"].(map[string]any); ok {
		fill("path", opts["path"])
		if headers, ok := opts["headers"].(map[string]any); ok {
			fill("host", headers["Host"])
		}
	}
	return out
}

// externalNetwork 归一化外部节点传输层字段（tcp/ws/grpc/xhttp/http/h2）。
func externalNetwork(extra map[string]any, keys ...string) string {
	return strings.ToLower(extStr(extra, keys...))
}

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
		p.SNI = extStr(e, "sni", "peername")
	case "tuic":
		consumed = externalTLSKeys("uuid", "password", "congestion_controller", "congestion-controller", "congestion_control", "udp_relay_mode", "udp-relay-mode", "reduce_rtt", "reduce-rtt")
		p.Type = "tuic"
		p.UUID = extStr(e, "uuid")
		p.Password = extStr(e, "password")
		p.SNI = extStr(e, "sni")
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
		p.SNI = extStr(e, "sni")
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
			p.SNI = extStr(e, "sni")
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
			opts = clashHTTPOpts{}
			if p := extStr(e, "path"); p != "" {
				opts.Path = []string{p}
			}
			if host := extStr(e, "host"); host != "" {
				opts.Headers = map[string]string{"Host": host}
			}
		}
		p.HTTPOpts = &opts
	}
}
