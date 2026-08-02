package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
	"lattix/shared"
)

// extStr 按序取 Extra 中第一个存在的字符串值。
func extStr(extra map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := extra[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// extBool 判断 Extra 布尔值（1/true/yes/on）。
func extBool(extra map[string]any, keys ...string) bool {
	switch strings.ToLower(extStr(extra, keys...)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// extInt 取 Extra 整数值。
func extInt(extra map[string]any, keys ...string) int {
	for _, key := range keys {
		if v, ok := extra[key]; ok {
			switch t := v.(type) {
			case string:
				if n, err := strconv.Atoi(t); err == nil {
					return n
				}
			case float64:
				return int(t)
			}
		}
	}
	return 0
}

// externalNetwork 归一化外部节点传输层字段（tcp/ws/grpc/xhttp/http/h2）。
func externalNetwork(extra map[string]any, keys ...string) string {
	return strings.ToLower(extStr(extra, keys...))
}

// buildExternalClash 把外部订阅节点编译为 mihomo 代理项。
// 凭据取自 config（外部节点没有面板派发的用户 UUID）。
func buildExternalClash(n extsub.Node) (clashProxy, error) {
	if n.Name == "" || n.Server == "" || n.Port == 0 {
		return clashProxy{}, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := n.Extra
	p := clashProxy{
		Name: n.Name, Server: n.Server, Port: n.Port, UDP: true,
		SkipCertVerify: extBool(e, "insecure", "allowInsecure", "allow_insecure"),
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
		p.CongestionController = extStr(e, "congestion_controller", "congestion-controller")
		p.UDPRelayMode = extStr(e, "udp_relay_mode", "udp-relay-mode")
		p.ReduceRTT = extBool(e, "reduce_rtt", "reduce-rtt")
	case "wireguard":
		p.Type = "wireguard"
		p.IP = extStr(e, "ip", "address")
		p.PrivateKey = extStr(e, "private_key", "private-key")
		p.PublicKey = extStr(e, "public_key", "pk")
		p.PresharedKey = extStr(e, "preshared_key", "preshared-key", "psk")
		p.MTU = extInt(e, "mtu")
	case "anytls":
		p.Type = "anytls"
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
	case "snell":
		p.Type = "snell"
		p.PSK = extStr(e, "psk")
		p.Obfs = extStr(e, "obfs")
		p.Version = extInt(e, "version")
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
		p.HTTPOpts = &clashHTTPOpts{Path: extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			p.HTTPOpts.Headers = map[string]string{"Host": host}
		}
	}
}
