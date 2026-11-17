package shared

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ExternalExit 是未托管服务器（外部直链，如 vless://、vmess://、socks://、http://）
// 作为链路最后一跳时使用的拨号规格（§未托管服务器与外部直链出口链路）：
// 入口 Endpoint 的链路路由以该规格渲染外部协议 outbound，经链跳纯 TCP 管道拨到该节点。
//
// Address/Port 是外部节点的公网监听（链路末位托管跳的 forward 目标）；
// 渲染 outbound 时拨号地址由调用方决定（Endpoint 路由拨入口 forward 回环端口），
// 因此本结构同时描述「目标」与「协议参数」。
type ExternalExit struct {
	Protocol string `json:"protocol"` // vless|vmess|trojan|shadowsocks|socks|http
	Address  string `json:"address"`
	Port     int    `json:"port"`

	// 凭据
	UUID       string `json:"uuid,omitempty"`       // vless/vmess
	Username   string `json:"username,omitempty"`   // socks/http
	Password   string `json:"password,omitempty"`   // trojan/shadowsocks/socks/http
	Method     string `json:"method,omitempty"`     // shadowsocks
	Cipher     string `json:"cipher,omitempty"`     // vmess（客户端提示，默认 auto）
	Flow       string `json:"flow,omitempty"`       // vless
	Encryption string `json:"encryption,omitempty"` // vless（VLESS Encryption 客户端字符串）

	// 传输/安全层（ss/socks/http 为空）
	Network     string `json:"network,omitempty"`     // tcp|ws|grpc|xhttp|httpupgrade
	Security    string `json:"security,omitempty"`    // none|tls|reality
	SNI         string `json:"sni,omitempty"`         // tls/reality 的 serverName
	Fingerprint string `json:"fingerprint,omitempty"` // 客户端 uTLS 指纹
	PublicKey   string `json:"public_key,omitempty"`  // reality 公钥
	ShortID     string `json:"short_id,omitempty"`    // reality shortId
	Path        string `json:"path,omitempty"`        // ws/xhttp/httpupgrade
	Host        string `json:"host,omitempty"`        // ws/httpupgrade/xhttp Host
	ServiceName string `json:"service_name,omitempty"` // grpc
	Mode        string `json:"mode,omitempty"`        // xhttp
}

// ExternalExitProtocols 是可用作链路外部出口的协议（全部为 TCP 承载，可被链跳
// dokodemo 纯 TCP 管道透传）。hysteria2/tuic/wireguard 等 UDP 协议不在其中。
var ExternalExitProtocols = []string{
	ProtocolVLESS, ProtocolVMess, ProtocolTrojan, ProtocolShadowsocks, ProtocolSocks, ProtocolHTTP,
}

// IsExternalExitProtocol 报告协议是否可作为外部出口。
func IsExternalExitProtocol(protocol string) bool {
	return ValidValue(protocol, ExternalExitProtocols)
}

// SupportsTransport 报告外部出口协议是否使用传输/安全层参数。
func (e ExternalExit) SupportsTransport() bool {
	switch e.Protocol {
	case ProtocolVLESS, ProtocolVMess, ProtocolTrojan:
		return true
	}
	return false
}

// Validate 校验外部出口规格（创建服务器与链路创建/编辑共用）。
func (e ExternalExit) Validate() error {
	if !IsExternalExitProtocol(e.Protocol) {
		return fmt.Errorf("外部出口协议 %q 暂不支持（支持 %s）", e.Protocol, strings.Join(ExternalExitProtocols, "/"))
	}
	host := strings.TrimSpace(e.Address)
	if host == "" {
		return fmt.Errorf("外部出口缺少服务器地址")
	}
	if strings.ContainsAny(host, "/ ") || strings.ContainsRune(host, '\\') {
		return fmt.Errorf("外部出口服务器地址非法: %q", e.Address)
	}
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("外部出口端口须为 1-65535")
	}
	switch e.Protocol {
	case ProtocolVLESS:
		if strings.TrimSpace(e.UUID) == "" {
			return fmt.Errorf("VLESS 外部节点缺少 UUID")
		}
	case ProtocolVMess:
		if strings.TrimSpace(e.UUID) == "" {
			return fmt.Errorf("VMess 外部节点缺少 UUID")
		}
	case ProtocolShadowsocks:
		if e.Method == "" || e.Password == "" {
			return fmt.Errorf("Shadowsocks 外部节点缺少加密方式或密码")
		}
	case ProtocolTrojan:
		if e.Password == "" {
			return fmt.Errorf("Trojan 外部节点缺少密码")
		}
	}
	if e.SupportsTransport() {
		if e.Network != "" && !ValidValue(e.Network, Networks) {
			return fmt.Errorf("外部出口传输方式非法: %q", e.Network)
		}
		if e.Security != "" && !ValidValue(e.Security, Securities) {
			return fmt.Errorf("外部出口安全层非法: %q", e.Security)
		}
	}
	return nil
}

// ValidateForSave 校验可保存到服务器的外部出口字段（服务器创建/编辑用）：
// 协议、端口范围与地址字符集。地址与凭据允许留空（尚未配置的占位行），完整
// 要求由 Validate 在链路创建/编辑时执行——服务器页因此没有必填项。
func (e ExternalExit) ValidateForSave() error {
	if !IsExternalExitProtocol(e.Protocol) {
		return fmt.Errorf("外部出口协议 %q 暂不支持（支持 %s）", e.Protocol, strings.Join(ExternalExitProtocols, "/"))
	}
	host := strings.TrimSpace(e.Address)
	if strings.ContainsAny(host, "/ ") || strings.ContainsRune(host, '\\') {
		return fmt.Errorf("外部出口服务器地址非法: %q", e.Address)
	}
	if e.Port < 0 || e.Port > 65535 {
		return fmt.Errorf("外部出口端口须为 1-65535")
	}
	return nil
}

// HostPort 返回外部节点的 host:port（IPv6 字面量自动加方括号）。
func (e ExternalExit) HostPort() string {
	return net.JoinHostPort(e.Address, strconv.Itoa(e.Port))
}

// ExternalExitSpecKey 返回规格的内容指纹来源（链路修订下游哈希用）：去掉
// Address/Port 之外的展示字段已包含在结构本身，直接序列化由调用方处理。
func (e ExternalExit) DisplayName() string {
	if e.Protocol == ProtocolShadowsocks {
		return fmt.Sprintf("%s %s", e.Protocol, e.Method)
	}
	return e.Protocol
}
