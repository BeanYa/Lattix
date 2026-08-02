package sub

import (
	"fmt"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalQuanX 把外部订阅节点编译为 Quantumult X [server_local] 行；
// 客户端无法表达的协议返回空串（调用方跳过）。
func buildExternalQuanX(n extsub.Node) string {
	e := externalYAMLFallback(n.Extra)
	server := fmt.Sprintf("%s:%d", n.Server, n.Port)
	add := func(fields []string, key, value string) []string {
		if value != "" {
			return append(fields, key+"="+value)
		}
		return fields
	}
	var fields []string
	switch n.Type {
	case "vless":
		if extStr(e, "security") == "reality" {
			return "" // quanx 不支持 reality
		}
		fields = append(fields, "vless="+server)
		fields = add(fields, "method", "chacha20-poly1305")
		fields = add(fields, "password", extStr(e, "id"))
		if externalNetwork(e, "type") == "ws" {
			fields = add(fields, "obfs", "wss")
			fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
			fields = add(fields, "obfs-uri", extStr(e, "path"))
		} else if extStr(e, "security") == "tls" {
			fields = add(fields, "obfs", "over-tls")
			fields = add(fields, "obfs-host", extStr(e, "sni"))
		}
	case "vmess":
		fields = append(fields, "vmess="+server)
		fields = add(fields, "method", "chacha20-ietf-poly1305")
		fields = add(fields, "password", extStr(e, "id"))
		net := externalNetwork(e, "net")
		switch {
		case net == "ws" && extStr(e, "tls") == "tls":
			fields = add(fields, "obfs", "wss")
		case net == "ws":
			fields = add(fields, "obfs", "ws")
		case extStr(e, "tls") == "tls":
			fields = add(fields, "obfs", "over-tls")
		}
		fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
		fields = add(fields, "obfs-uri", extStr(e, "path"))
	case "trojan":
		fields = append(fields, "trojan="+server)
		fields = add(fields, "password", extStr(e, "password"))
		if externalNetwork(e, "type") == "ws" {
			fields = add(fields, "obfs", "wss")
			fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
			fields = add(fields, "obfs-uri", extStr(e, "path"))
		}
	case "ss":
		fields = append(fields, "shadowsocks="+server)
		fields = add(fields, "method", extStr(e, "method"))
		fields = add(fields, "password", extStr(e, "password"))
	case "hysteria2":
		fields = append(fields, "hysteria2="+server)
		fields = add(fields, "password", extStr(e, "password"))
		fields = add(fields, "obfs", extStr(e, "obfs"))
		fields = add(fields, "obfs-password", extStr(e, "obfs-password", "obfs_password"))
		fields = add(fields, "sni", extStr(e, "sni"))
	case "tuic":
		fields = append(fields, "tuic="+server)
		fields = add(fields, "uuid", extStr(e, "uuid"))
		fields = add(fields, "password", extStr(e, "password"))
		fields = add(fields, "congestion-controller", extStr(e, "congestion_controller", "congestion-controller"))
		fields = add(fields, "udp-relay-mode", extStr(e, "udp_relay_mode", "udp-relay-mode"))
		fields = add(fields, "sni", extStr(e, "sni"))
	case "socks":
		fields = append(fields, "socks5="+server)
		fields = add(fields, "username", extStr(e, "username"))
		fields = add(fields, "password", extStr(e, "password"))
	case "http":
		fields = append(fields, "http="+server)
		fields = add(fields, "username", extStr(e, "username"))
		fields = add(fields, "password", extStr(e, "password"))
	default:
		return ""
	}
	fields = add(fields, "tag", n.Name)
	return strings.Join(fields, ", ")
}
