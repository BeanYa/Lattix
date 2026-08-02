package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
)

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
// 已存在于 base 的键（type/tag/server/server_port 等）永不覆盖：
// 外部载荷（如 v2rayN vmess JSON 的 "type":"none"）可能携带同名冲突键。
func applySingboxRaw(base map[string]any, extra map[string]any, consumed map[string]bool) {
	for key, value := range extra {
		if _, exists := base[key]; exists {
			continue
		}
		if !consumed[key] {
			base[key] = value
		}
	}
}

// buildExternalSingbox 把外部订阅节点编译为 sing-box 出站（map JSON）。
// sing-box 不原生支持 ssr/snell，返回错误由调用方记 warning 跳过。
func buildExternalSingbox(n extsub.Node) (any, error) {
	if n.Name == "" || n.Server == "" || n.Port == 0 {
		return nil, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := externalYAMLFallback(n.Extra)
	base := map[string]any{"type": n.Type, "tag": n.Name, "server": n.Server, "server_port": n.Port}
	var consumed map[string]bool
	switch n.Type {
	case "vless":
		consumed = externalSingboxKeys("id", "uuid", "type", "network", "flow", "security", "pbk", "sid", "fp", "client-fingerprint", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")
		base["uuid"] = extStr(e, "id")
		base["packet_encoding"] = "xudp"
		if flow := extStr(e, "flow"); flow != "" {
			base["flow"] = flow
		}
		if tls := externalSingboxTLS(e, extStr(e, "security") == "reality" || extStr(e, "pbk") != ""); tls != nil {
			base["tls"] = tls
		}
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "vmess":
		consumed = externalSingboxKeys("id", "uuid", "type", "network", "net", "aid", "scy", "tls", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")
		base["uuid"] = extStr(e, "id")
		base["alter_id"] = extInt(e, "aid")
		base["security"] = extStr(e, "scy", "auto")
		if extStr(e, "tls") == "tls" {
			base["tls"] = externalSingboxTLSSimple(e)
		}
		if tr := externalSingboxTransport(e, externalNetwork(e, "net")); tr != nil {
			base["transport"] = tr
		}
	case "trojan":
		consumed = externalSingboxKeys("password", "type", "network", "sni", "servername", "path", "host", "mode", "serviceName", "service_name", "ws-opts", "grpc-opts", "xhttp-opts", "http-opts", "h2-opts", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "ss":
		consumed = externalSingboxKeys("method", "password", "plugin", "plugin-opts")
		base["method"] = extStr(e, "method")
		base["password"] = extStr(e, "password")
		if plugin := extStr(e, "plugin"); plugin != "" {
			base["plugin"] = plugin
			if opts := rawMap(e["plugin-opts"]); opts != nil {
				base["plugin_opts"] = opts
			}
		}
	case "hysteria2":
		consumed = externalSingboxKeys("password", "obfs", "obfs-password", "obfs_password", "sni", "peername", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")
		base["password"] = extStr(e, "password")
		if extStr(e, "obfs") != "" {
			base["obfs"] = map[string]any{
				"type": "salamander", "password": extStr(e, "obfs-password", "obfs_password"),
			}
		}
		base["tls"] = map[string]any{
			"enabled": true, "server_name": extStr(e, "sni", "peername"),
			"insecure": extBool(e, "insecure"),
		}
	case "tuic":
		consumed = externalSingboxKeys("uuid", "password", "congestion_controller", "congestion-controller", "congestion_control", "udp_relay_mode", "udp-relay-mode", "reduce_rtt", "reduce-rtt", "sni", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify")
		base["uuid"] = extStr(e, "uuid")
		if pwd := extStr(e, "password"); pwd != "" {
			base["password"] = pwd
		}
		if cc := extStr(e, "congestion_controller", "congestion-controller"); cc != "" {
			base["congestion_control"] = cc
		}
		if udp := extStr(e, "udp_relay_mode", "udp-relay-mode"); udp != "" {
			base["udp_relay_mode"] = udp
		}
		base["zero_rtt_handshake"] = extBool(e, "reduce_rtt", "reduce-rtt")
		base["tls"] = map[string]any{
			"enabled": true, "server_name": extStr(e, "sni"),
			"insecure": extBool(e, "allow_insecure"),
		}
	case "wireguard":
		consumed = externalSingboxKeys("ip", "address", "ipv6", "private_key", "private-key", "public_key", "pk", "public-key", "preshared_key", "preshared-key", "psk", "mtu", "reserved")
		addr := []string{extStr(e, "ip", "address")}
		if v6 := extStr(e, "ipv6"); v6 != "" {
			for _, part := range strings.FieldsFunc(v6, func(r rune) bool { return r == ',' || r == ' ' }) {
				addr = append(addr, part)
			}
		}
		base["local_address"] = addr
		base["private_key"] = extStr(e, "private_key")
		if pk := extStr(e, "public_key", "pk"); pk != "" {
			base["peer_public_key"] = pk
		}
		if psk := extStr(e, "preshared_key", "preshared-key", "psk"); psk != "" {
			base["preshared_key"] = psk
		}
		if mtu := extInt(e, "mtu"); mtu > 0 {
			base["mtu"] = mtu
		}
		if reserved := extStr(e, "reserved"); reserved != "" {
			var values []int
			for _, part := range strings.Split(reserved, ",") {
				if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
					values = append(values, v)
				}
			}
			if len(values) > 0 {
				base["reserved"] = values
			}
		}
	case "socks", "http":
		consumed = externalSingboxKeys("username", "password")
		base["username"] = extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			base["password"] = pwd
		}
	case "anytls":
		consumed = externalSingboxKeys("password", "sni", "servername", "alpn", "insecure", "allowInsecure", "allow_insecure", "skip-cert-verify", "idle-session-check-interval", "idle-session-timeout", "min-idle-session")
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
		if v := extInt(e, "idle-session-check-interval"); v > 0 {
			base["idle_session_check_interval"] = v
		}
		if v := extInt(e, "idle-session-timeout"); v > 0 {
			base["idle_session_timeout"] = v
		}
		if v := extInt(e, "min-idle-session"); v > 0 {
			base["min_idle_session"] = v
		}
	default:
		return nil, fmt.Errorf("外部节点「%s」sing-box 不支持协议 %s", n.Name, n.Type)
	}
	applySingboxRaw(base, n.Extra, consumed)
	return base, nil
}

// externalSingboxTLS 构造 vless 的 TLS 对象（reality 时带 reality/utls）。
func externalSingboxTLS(e map[string]any, reality bool) map[string]any {
	tls := externalSingboxTLSSimple(e)
	if reality {
		tls["reality"] = map[string]any{
			"enabled": true, "public_key": extStr(e, "pbk"), "short_id": extStr(e, "sid"),
		}
		if fp := extStr(e, "fp"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
	}
	return tls
}

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

// externalSingboxTransport 构造传输层对象；不支持/缺省返回 nil。
func externalSingboxTransport(e map[string]any, network string) map[string]any {
	switch network {
	case "ws":
		tr := map[string]any{"type": "ws", "path": extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		return tr
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": extStr(e, "serviceName", "service_name")}
	case "xhttp":
		tr := map[string]any{"type": "xhttp", "path": extStr(e, "path")}
		if mode := extStr(e, "mode"); mode != "" {
			tr["mode"] = mode
		}
		return tr
	case "http", "h2":
		tr := map[string]any{"type": "http", "path": []string{extStr(e, "path")}}
		if host := extStr(e, "host"); host != "" {
			tr["host"] = []string{host}
		}
		return tr
	}
	return nil
}
