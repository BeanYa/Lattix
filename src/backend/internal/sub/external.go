package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
	"lattix/shared"
)

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
	switch n.Type {
	case "vless":
		p.Type = "vless"
		p.UUID = extStr(e, "id")
		p.Network = externalNetwork(e, "type", "network")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		p.Flow = extStr(e, "flow")
		p.Encryption = extStr(e, "encryption")
		switch extStr(e, "security") {
		case "reality":
			p.TLS = true
			p.RealityOpts = &clashRealityOpts{PublicKey: extStr(e, "pbk"), ShortID: extStr(e, "sid")}
			p.ClientFingerprint = extStr(e, "fp")
		case "tls":
			p.TLS = true
			p.ClientFingerprint = extStr(e, "fp")
		default:
			if _, ok := e["reality-opts"]; ok || extStr(e, "pbk") != "" {
				p.TLS = true
				p.RealityOpts = &clashRealityOpts{PublicKey: extStr(e, "pbk"), ShortID: extStr(e, "sid")}
				p.ClientFingerprint = extStr(e, "fp")
			}
		}
		p.Servername = extStr(e, "sni")
		applyExternalTransport(&p, e)
	case "vmess":
		zero := 0
		p.Type = "vmess"
		p.UUID = extStr(e, "id")
		p.AlterID = &zero
		p.Cipher = "auto"
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
		p.Type = "ss"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
	case "ssr":
		p.Type = "ssr"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
		p.Protocol = extStr(e, "protocol")
		p.ProtocolParam = extStr(e, "protocol_param", "protocol-param")
		p.Obfs = extStr(e, "obfs")
		p.ObfsParam = extStr(e, "obfs_param", "obfs-param")
	case "hysteria2":
		p.Type = "hysteria2"
		p.Password = extStr(e, "password")
		p.Ports = extStr(e, "mport", "ports")
		p.Obfs = extStr(e, "obfs")
		p.ObfsPassword = extStr(e, "obfs-password", "obfs_password")
		p.Up = extStr(e, "up")
		p.Down = extStr(e, "down")
		p.Servername = extStr(e, "sni", "peername")
	case "tuic":
		p.Type = "tuic"
		p.UUID = extStr(e, "uuid")
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
		p.CongestionController = extStr(e, "congestion_controller", "congestion-controller", "congestion_control")
		p.UDPRelayMode = extStr(e, "udp_relay_mode", "udp-relay-mode")
		p.ReduceRTT = extBoolPtr(e, "reduce_rtt", "reduce-rtt")
	case "wireguard":
		p.Type = "wireguard"
		p.IP = extStr(e, "ip", "address")
		p.PrivateKey = extStr(e, "private_key", "private-key")
		p.PublicKey = extStr(e, "public_key", "pk")
		p.PresharedKey = extStr(e, "preshared_key", "preshared-key", "psk")
		p.MTU = extIntPtr(e, "mtu")
	case "anytls":
		p.Type = "anytls"
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
	case "snell":
		p.Type = "snell"
		p.PSK = extStr(e, "psk")
		p.Obfs = extStr(e, "obfs")
		p.Version = extIntPtr(e, "version")
	case "socks":
		p.Type = "socks5"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
	case "http":
		p.Type = "http"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
		p.UDP = false
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
	return p, nil
}

// applyExternalTransport 填充外部节点 ws/grpc/xhttp/http 传输层选项。
func applyExternalTransport(p *clashProxy, e map[string]any) {
	switch p.Network {
	case "ws":
		p.WsOpts = &clashWsOpts{Path: extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			p.WsOpts.Headers = map[string]string{"Host": host}
		}
	case shared.NetworkGRPC:
		p.GrpcOpts = &clashGrpcOpts{ServiceName: extStr(e, "serviceName", "service_name")}
	case shared.NetworkXHTTP:
		p.XhttpOpts = &clashXHTTPOpts{
			Path: extStr(e, "path"), Mode: extStr(e, "mode"), Host: extStr(e, "host"),
		}
	case "http", "h2":
		p.HTTPOpts = &clashHTTPOpts{Path: []string{extStr(e, "path")}}
		if host := extStr(e, "host"); host != "" {
			p.HTTPOpts.Headers = map[string]string{"Host": host}
		}
	}
}
