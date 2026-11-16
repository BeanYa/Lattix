// Package external 解析外部直链（vless://、vmess://、socks://、http:// 等）为
// 链路外部出口规格（设计：docs/superpowers/specs/2026-11-17-external-exit-chains-design.md）。
// 解析复用 extsub 的分享链接解析器（外部订阅同源），本包只做协议归一与校验。
package external

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
	"lattix/shared"
)

// Parse 把一条外部直链解析为规范化外部出口规格与节点名。
// 仅接受可作链路外部出口的 TCP 协议（shared.ExternalExitProtocols）。
func Parse(link string) (shared.ExternalExit, string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return shared.ExternalExit{}, "", fmt.Errorf("链接不能为空")
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) == 0 {
		return shared.ExternalExit{}, "", fmt.Errorf("无法解析链接：需为 vless:// / vmess:// / trojan:// / ss:// / socks:// / http:// 分享链接")
	}
	exit, name, err := fromNode(nodes[0])
	if err != nil {
		return shared.ExternalExit{}, "", err
	}
	fillUserinfoFromLink(&exit, link)
	if err := exit.Validate(); err != nil {
		return shared.ExternalExit{}, "", err
	}
	if strings.TrimSpace(name) == "" {
		name = exit.HostPort()
	}
	return exit, name, nil
}

// fillUserinfoFromLink 补齐 http 链接的 userinfo（extsub 的凭据解析覆盖
// vless/trojan/socks 等，http 未取 userinfo；这里按原链接兜底）。
func fillUserinfoFromLink(exit *shared.ExternalExit, link string) {
	if exit.Protocol != shared.ProtocolHTTP || exit.Username != "" || exit.Password != "" {
		return
	}
	u, err := url.Parse(link)
	if err != nil || u.User == nil {
		return
	}
	exit.Username = u.User.Username()
	if password, ok := u.User.Password(); ok {
		exit.Password = password
	}
}

// TransportSummary 返回外部出口的传输/安全层摘要（前端解析预览用）。
func TransportSummary(exit shared.ExternalExit) string {
	if !exit.SupportsTransport() {
		return "TCP"
	}
	network := exit.Network
	if network == "" {
		network = shared.NetworkTCP
	}
	security := exit.Security
	if security == "" {
		security = shared.SecurityNone
	}
	return network + " · " + security
}

// fromNode 把 extsub 标准化节点归一为外部出口规格。
func fromNode(node extsub.Node) (shared.ExternalExit, string, error) {
	exit := shared.ExternalExit{
		Protocol: normalizeProtocol(node.Type),
		Address:  strings.TrimSpace(node.Server),
		Port:     node.Port,
	}
	if !shared.IsExternalExitProtocol(exit.Protocol) {
		return shared.ExternalExit{}, "", fmt.Errorf("协议 %s 暂不支持作为外部出口（支持 %s；UDP 承载协议不在其中）",
			node.Type, strings.Join(shared.ExternalExitProtocols, "/"))
	}
	extra := node.Extra
	switch exit.Protocol {
	case shared.ProtocolVLESS:
		exit.UUID = extraString(extra, "id")
		exit.Flow = extraString(extra, "flow")
		exit.Encryption = normalizeEncryption(extraString(extra, "encryption"))
		applyTransport(&exit, extra, extraString(extra, "type"), "")
	case shared.ProtocolVMess:
		exit.UUID = extraString(extra, "id")
		exit.Cipher = normalizeCipher(firstNonEmpty(extraString(extra, "scy"), extraString(extra, "security")))
		// vmess JSON 的 "net" 才是传输方式，"type" 是 header 混淆类型（none/http）。
		applyTransport(&exit, extra, extraString(extra, "net"), extraString(extra, "tls"))
	case shared.ProtocolTrojan:
		exit.Password = extraString(extra, "password")
		applyTransport(&exit, extra, extraString(extra, "type"), "")
	case shared.ProtocolShadowsocks:
		exit.Method = extraString(extra, "method")
		exit.Password = extraString(extra, "password")
		if extraString(extra, "plugin") != "" {
			return shared.ExternalExit{}, "", fmt.Errorf("暂不支持带插件（plugin）的 Shadowsocks 外部节点")
		}
	case shared.ProtocolSocks, shared.ProtocolHTTP:
		exit.Username = extraString(extra, "username")
		exit.Password = extraString(extra, "password")
	}
	return exit, node.Name, nil
}

// applyTransport 归一传输/安全层参数。network 为原始传输字段（vless/trojan 的
// 查询参数 type，vmess JSON 的 net）；vmessTLS 为 vmess JSON 的 tls 字段
// （"tls"/"reality"/空），其余协议取查询参数 security。
func applyTransport(exit *shared.ExternalExit, extra map[string]any, network, vmessTLS string) {
	network = normalizeNetwork(network)
	exit.Network = network
	exit.PublicKey = extraString(extra, "pbk")
	exit.ShortID = extraString(extra, "sid")
	exit.SNI = firstNonEmpty(extraString(extra, "sni"), extraString(extra, "serverName"))
	exit.Fingerprint = extraString(extra, "fp")
	exit.Path = extraString(extra, "path")
	exit.Host = extraString(extra, "host")
	exit.ServiceName = extraString(extra, "serviceName")
	exit.Mode = extraString(extra, "mode")
	if exit.Fingerprint == "" {
		exit.Fingerprint = shared.FingerprintChrome
	}
	security := extraString(extra, "security")
	if security == "" && vmessTLS != "" {
		security = vmessTLS
	}
	switch {
	case exit.PublicKey != "" || security == shared.SecurityReality:
		if exit.PublicKey == "" {
			security = shared.SecurityTLS // 声明 reality 却无公钥：按 TLS 处理（无法做 Reality 认证）
		} else {
			security = shared.SecurityReality
		}
	case security == shared.SecurityTLS || security == "xtls":
		security = shared.SecurityTLS
	default:
		security = shared.SecurityNone
	}
	exit.Security = security
	if network == shared.NetworkTCP && exit.Security == shared.SecurityNone {
		exit.Path, exit.Host, exit.ServiceName, exit.Mode = "", "", "", ""
	}
}

func normalizeProtocol(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "socks", "socks5":
		return shared.ProtocolSocks
	case "http", "https":
		return shared.ProtocolHTTP
	case "ss":
		return shared.ProtocolShadowsocks
	case "vless":
		return shared.ProtocolVLESS
	case "vmess":
		return shared.ProtocolVMess
	case "trojan":
		return shared.ProtocolTrojan
	}
	return strings.ToLower(strings.TrimSpace(typ))
}

// normalizeNetwork 归一传输方式；空 = tcp。不支持的传输返回原值，由链路校验拒绝。
func normalizeNetwork(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "tcp", "raw", "none":
		return shared.NetworkTCP
	case "ws", "websocket":
		return shared.NetworkWS
	case "grpc", "gun":
		return shared.NetworkGRPC
	case "xhttp", "splithttp":
		return shared.NetworkXHTTP
	case "httpupgrade":
		return shared.NetworkHTTPUpgrade
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeEncryption(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" {
		return ""
	}
	return value
}

func normalizeCipher(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return shared.VMessCipherAuto
	case "aes-128-gcm":
		return shared.VMessCipherAES128GCM
	case "chacha20-poly1305", "chacha20-ietf-poly1305":
		return shared.VMessCipherChacha20
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// extraString 读取 extra 中的字符串值（vmess JSON 可能给数字/布尔）。
func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, ok := extra[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	}
	return ""
}
