package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalSingbox 把外部订阅节点编译为 sing-box 出站（map JSON）。
// sing-box 不原生支持 ssr/snell，返回错误由调用方记 warning 跳过。
func buildExternalSingbox(n extsub.Node) (any, error) {
	if n.Name == "" || n.Server == "" || n.Port == 0 {
		return nil, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := externalYAMLFallback(n.Extra)
	base := map[string]any{"type": n.Type, "tag": n.Name, "server": n.Server, "server_port": n.Port}
	switch n.Type {
	case "vless":
		base["uuid"] = extStr(e, "id")
		base["packet_encoding"] = "xudp"
		if flow := extStr(e, "flow"); flow != "" {
			base["flow"] = flow
		}
		if tls := externalSingboxTLS(e, extStr(e, "security") == "reality"); tls != nil {
			base["tls"] = tls
		}
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "vmess":
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
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "ss":
		base["method"] = extStr(e, "method")
		base["password"] = extStr(e, "password")
	case "hysteria2":
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
		base["local_address"] = []string{extStr(e, "ip", "address")}
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
		base["username"] = extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			base["password"] = pwd
		}
	case "anytls":
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
	default:
		return nil, fmt.Errorf("外部节点「%s」sing-box 不支持协议 %s", n.Name, n.Type)
	}
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
	return map[string]any{
		"enabled": true, "server_name": extStr(e, "sni"),
		"insecure": extBool(e, "insecure", "allowInsecure", "allow_insecure"),
	}
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
