package shared

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 协议范围（§14 全协议向导）：xray 全部 inbound 协议。
// Reality 仅与 RAW(tcp)/gRPC/XHTTP 三种传输组合（xray 官方约束）；
// ss/socks/http 无 Reality，dokodemo 为端口转发（无用户概念，不进订阅）。
const (
	ProtocolVLESS       = "vless"
	ProtocolVMess       = "vmess"
	ProtocolTrojan      = "trojan"
	ProtocolShadowsocks = "shadowsocks"
	ProtocolSocks       = "socks"
	ProtocolHTTP        = "http"
	ProtocolDokodemo    = "dokodemo-door" // xray inbound 协议名
	ProtocolHysteria2   = "hysteria"      // xray 26.x hy2 入站协议名（非 "hysteria2"；订阅层再映射客户端类型名）
)

// Protocols 是向导可选的全部协议。
var Protocols = []string{
	ProtocolVLESS, ProtocolVMess, ProtocolTrojan, ProtocolShadowsocks, ProtocolHysteria2,
	ProtocolSocks, ProtocolHTTP, ProtocolDokodemo,
}

// 传输方式（network）：ws/httpupgrade 在 xray 25.x+ 有弃用警告但仍可用；
// Reality 仅与 tcp/grpc/xhttp 组合（xray 官方约束，见 RealityNetworks）。
const (
	NetworkTCP         = "tcp"
	NetworkGRPC        = "grpc"
	NetworkXHTTP       = "xhttp"
	NetworkWS          = "ws"
	NetworkHTTPUpgrade = "httpupgrade"
)

// Networks 是向导可选的全部传输方式。
var Networks = []string{NetworkTCP, NetworkGRPC, NetworkXHTTP, NetworkWS, NetworkHTTPUpgrade}

// RealityNetworks 是 Reality 安全层兼容的传输子集（xray 官方约束）。
var RealityNetworks = []string{NetworkTCP, NetworkGRPC, NetworkXHTTP}

// 安全层（security）：reality（仅 tcp/grpc/xhttp）/ tls（自签或 ACME 证书，§3.3）/ none。
const (
	SecurityReality = "reality"
	SecurityTLS     = "tls"
	SecurityNone    = "none"
)

// Securities 是设计矩阵中的全部安全层取值（normalize 对 tls 单独 400 引导）。
var Securities = []string{SecurityReality, SecurityTLS, SecurityNone}

// 证书模式（cert_mode）：仅 security=tls 有效。
// selfsign=伪装域名自签 + 证书 pin（默认）；acme=落地服务器域名 + acme.sh 真实签发（§3.3）。
const (
	CertModeSelfSign = "selfsign"
	CertModeACME     = "acme"
)

// CertModes 是向导可选的全部证书模式。
var CertModes = []string{CertModeSelfSign, CertModeACME}

// XHTTP 的 mode 可选值（xray xhttpSettings.mode）。
var XHTTPModes = []string{"auto", "packet-up", "stream-up"}

const (
	FlowVision        = "xtls-rprx-vision" // vless flow，仅 tcp
	FingerprintChrome = "chrome"           // 默认 uTLS 指纹
)

// VLESS Encryption 认证方式（xray `vlessenc` 生成 decryption/encryption 对，
// 替代 "decryption": "none"）。可与 flow vision 组合（§15，组合时按 1-RTT 下发）。
const (
	VLessEncX25519   = "x25519"   // X25519 认证（非后量子）
	VLessEncMLKEM768 = "mlkem768" // ML-KEM-768 认证（后量子，xray 推荐）
)

// VLessEncMethods 是向导可选的 VLESS Encryption 认证方式。
var VLessEncMethods = []string{VLessEncX25519, VLessEncMLKEM768}

// Fingerprints 是客户端 uTLS 指纹可选值（纯订阅侧参数，mihomo client-fingerprint）。
var Fingerprints = []string{
	"chrome", "firefox", "safari", "edge", "ios", "android", "360", "qq", "random", "randomized",
}

// Shadowsocks 加密方式：旧式 AEAD（密码任意）与 2022-blake3（定长 base64 密钥，多用户）。
const (
	SSMethodAES128GCM            = "aes-128-gcm"
	SSMethodAES256GCM            = "aes-256-gcm"
	SSMethodChacha20IETFPoly1305 = "chacha20-ietf-poly1305"
	SSMethod2022AES128GCM        = "2022-blake3-aes-128-gcm"
	SSMethod2022AES256GCM        = "2022-blake3-aes-256-gcm"
	SSMethod2022Chacha20         = "2022-blake3-chacha20-poly1305"
)

// SSMethods 是向导可选的 ss 加密方式。
var SSMethods = []string{
	SSMethodAES128GCM, SSMethodAES256GCM, SSMethodChacha20IETFPoly1305,
	SSMethod2022AES128GCM, SSMethod2022AES256GCM, SSMethod2022Chacha20,
}

// VMess cipher（仅作客户端订阅提示，xray vmess inbound 无此字段）。
const (
	VMessCipherAuto      = "auto"
	VMessCipherAES128GCM = "aes-128-gcm"
	VMessCipherChacha20  = "chacha20-poly1305"
)

var VMessCiphers = []string{VMessCipherAuto, VMessCipherAES128GCM, VMessCipherChacha20}

// ValidValue 报告 v 是否在候选集合中。
func ValidValue(v string, candidates []string) bool {
	for _, c := range candidates {
		if v == c {
			return true
		}
	}
	return false
}

// IsRealityProtocol 报告协议是否走 Reality 安全层（含 dest 预检与密钥对）。
func IsRealityProtocol(protocol string) bool {
	switch protocol {
	case ProtocolVLESS, ProtocolVMess, ProtocolTrojan:
		return true
	}
	return false
}

// HasUserList 报告协议是否有用户列表（dokodemo 为端口转发，无用户概念）。
func HasUserList(protocol string) bool {
	return protocol != ProtocolDokodemo
}

// PortLayers 返回协议监听占用的传输层（端口冲突治理的分层依据）：
// shadowsocks/dokodemo-door 同时监听 tcp+udp，hysteria 为 udp-only（QUIC），其余仅 tcp。
func PortLayers(protocol string) string {
	switch protocol {
	case ProtocolShadowsocks, ProtocolDokodemo:
		return "tcp,udp"
	case ProtocolHysteria2:
		return "udp"
	default:
		return "tcp"
	}
}

// LayersOverlap 判定两个层集合是否有交集（"tcp,udp" 与 "udp" 重叠，"udp" 与 "tcp" 不重叠）。
func LayersOverlap(a, b string) bool {
	for _, x := range strings.Split(a, ",") {
		for _, y := range strings.Split(b, ",") {
			if x == y {
				return true
			}
		}
	}
	return false
}

// Is2022Method 报告 ss 加密方式是否为 2022-blake3 系列（定长密钥、多用户 clients 不带 method）。
func Is2022Method(method string) bool {
	return strings.HasPrefix(method, "2022-blake3-")
}

// SSKeyBytes 返回 ss 加密方式要求的密钥字节数（aes-128 系列 16，aes-256/chacha20 系列 32）。
func SSKeyBytes(method string) int {
	if strings.Contains(method, "aes-128") {
		return 16
	}
	return 32
}

// GenerateSSKey 生成 ss 2022-blake3 节点级 PSK（base64 定长密钥，面板建节点时嵌入模板）。
func GenerateSSKey(method string) (string, error) {
	raw := make([]byte, SSKeyBytes(method))
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// SSUserPassword 从用户 UUID 确定性派生 ss 密码（panel 订阅与 agent clients 两端共用）：
// 旧式 cipher 密码为 UUID 原文；2022-blake3 须为定长 base64 密钥——
// aes-128-gcm 取 UUID 原始 16 字节，aes-256-gcm/chacha20 取 sha256(UUID) 32 字节。
func SSUserPassword(uuid, method string) string {
	switch method {
	case SSMethod2022AES128GCM:
		raw, err := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
		if err != nil || len(raw) != 16 {
			sum := sha256.Sum256([]byte(uuid)) // 非法 UUID 兜底，仍确定性
			return base64.StdEncoding.EncodeToString(sum[:16])
		}
		return base64.StdEncoding.EncodeToString(raw)
	case SSMethod2022AES256GCM, SSMethod2022Chacha20:
		sum := sha256.Sum256([]byte(uuid))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	return uuid
}

// NodeTag 是节点 inbound 的 tag（热操作与配置文件的关联键）。
func NodeTag(nodeID int64) string { return fmt.Sprintf("node_%d", nodeID) }

// 模板占位符（§7）：Agent 填值后原样写入，不存在任何"翻译层"。
// 约定：PORT/CLIENTS 在模板中以带引号的字符串形式出现（如 "port": "{{PORT}}"、
// "clients": "{{CLIENTS}}"），Agent 连引号一起替换为对应的 JSON 值；
// PRIVATE_KEY/TAG/TLS_CERT_FILE/TLS_KEY_FILE 为纯字符串替换。
const (
	// PlaceholderPort 端口占位符；向导中端口留空时由 Agent 挑空闲端口并随 apply_result 上报。
	PlaceholderPort = "{{PORT}}"
	// PlaceholderRealityPrivateKey Reality 私钥占位符；由 Agent 执行 `xray x25519` 生成，
	// 私钥不出服务器，public_key 随 apply_result 上报（仅 reality 协议模板含有）。
	PlaceholderRealityPrivateKey = "{{PRIVATE_KEY}}"
	// PlaceholderClients 用户列表占位符；Agent 以全量用户 UUID 按协议生成
	// clients/accounts JSON 数组替换（§8）。dokodemo 模板不含此占位符。
	PlaceholderClients = "{{CLIENTS}}"
	// PlaceholderVLessDecryption VLESS Encryption 私钥侧占位符；Agent 执行
	// `xray vlessenc` 生成 decryption/encryption 对，decryption 填入模板，
	// encryption（客户端字符串）随 apply_result 上报（订阅用）。
	PlaceholderVLessDecryption = "{{DECRYPTION}}"
	// PlaceholderTag inbound tag 占位符；Agent 替换为 NodeTag(nodeID)。
	PlaceholderTag = "{{TAG}}"
	// PlaceholderTLSCertFile/PlaceholderTLSKeyFile TLS 证书文件路径占位符（仅
	// security=tls 模板含有）：Agent 按 CertMode 落地证书（selfsign=`xray tls cert`
	// 自签 / acme=acme.sh 签发）后以绝对路径纯字符串替换（§3.3）。
	PlaceholderTLSCertFile = "{{TLS_CERT_FILE}}"
	PlaceholderTLSKeyFile  = "{{TLS_KEY_FILE}}"
)

// VirtualConfig 是面板侧虚拟配置（nodes.config_template）：xray inbound JSON 模板 + 占位符。
// Flow/Network/ServiceName/Path/Mode/Host/Method 为协议参数，
// Agent 据此构造用户条目并随 realized_config 回显（订阅生成依赖）。
type VirtualConfig struct {
	Protocol      string             `json:"protocol"`       // vless/vmess/trojan/shadowsocks/hysteria/socks/http/dokodemo
	Port          int                `json:"port,omitempty"` // 0 = Agent 自动挑选空闲端口
	Flow          string             `json:"flow,omitempty"` // vless：xtls-rprx-vision 或空（仅 tcp）
	Network       string             `json:"network,omitempty"`
	Security      string             `json:"security,omitempty"`       // reality/tls/none；空 = 按 network 推导
	CertMode      string             `json:"cert_mode,omitempty"`      // security=tls：selfsign（默认）/acme
	TLSDomain     string             `json:"tls_domain,omitempty"`     // selfsign=伪装域名；acme=落地服务器域名（panel 检测填充）
	ServiceName   string             `json:"service_name,omitempty"`   // grpc
	Path          string             `json:"path,omitempty"`           // xhttp/ws/httpupgrade
	Mode          string             `json:"mode,omitempty"`           // xhttp
	Host          string             `json:"host,omitempty"`           // xhttp/ws/httpupgrade
	Method        string             `json:"method,omitempty"`         // shadowsocks
	Fingerprint   string             `json:"fingerprint,omitempty"`    // 客户端 uTLS 指纹（订阅侧参数，Agent 回显）
	Encryption    string             `json:"encryption,omitempty"`     // vless：VLESS Encryption 认证方式（x25519/mlkem768）
	Cipher        string             `json:"cipher,omitempty"`         // vmess 客户端 cipher 提示（默认 auto）
	ObfsPassword  string             `json:"obfs_password,omitempty"`  // hy2 salamander 混淆密码（panel 生成，模板内固定值）
	UpMbps        int                `json:"up_mbps,omitempty"`        // hy2 brutal 上行声明（0=不输出 brutalUp，回退 BBR）
	DownMbps      int                `json:"down_mbps,omitempty"`      // hy2 brutal 下行声明
	PortHop       string             `json:"port_hop,omitempty"`       // hy2 udpHop 段 "a-b"（空=关闭跳跃；DNAT 治理见 spec §3.2）
	StaticClients []ClientCredential `json:"static_clients,omitempty"` // 技术隧道身份，不属于业务用户
	Template      json.RawMessage    `json:"template"`                 // xray inbound JSON 模板，含占位符
}

// ClientCredential separates the protocol credential from the Xray stats
// identity. Business access uses access:<assignment_id>; internal transport
// uses tunnel:<route_id>, so neither needs a synthetic business user.
type ClientCredential struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// RealizedConfig 是 Agent 上报的实际生效值（nodes.realized_config），
// 面板生成订阅（§9）依赖这些字段；reality 字段对非 reality 协议为空。
type RealizedConfig struct {
	Port         int    `json:"port"`
	PublicKey    string `json:"public_key,omitempty"`
	ShortID      string `json:"short_id,omitempty"`
	ServerName   string `json:"server_name,omitempty"`
	Flow         string `json:"flow,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	Network      string `json:"network,omitempty"`
	Security     string `json:"security,omitempty"`    // 生效安全层（reality/tls/none）
	SNI          string `json:"sni,omitempty"`         // security=tls 的 serverName（伪装域名/落地域名）
	CertSHA256   string `json:"cert_sha256,omitempty"` // 自签证书 DER 的 sha256 hex（订阅 pin / xray pinnedPeerCertSha256）；ACME 留空
	ServiceName  string `json:"service_name,omitempty"`
	Path         string `json:"path,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Host         string `json:"host,omitempty"`
	Method       string `json:"method,omitempty"`
	PSK          string `json:"psk,omitempty"`           // ss 2022-blake3 节点级 PSK（订阅拼接 "PSK:用户密钥"）
	Encryption   string `json:"encryption,omitempty"`    // vless：VLESS Encryption 客户端字符串（订阅 encryption 字段）
	ObfsPassword string `json:"obfs_password,omitempty"` // hy2 salamander 混淆密码（回显，订阅 obfs-password）
	UpMbps       int    `json:"up_mbps,omitempty"`       // hy2 brutal 上行（回显，订阅 up/upmbps）
	DownMbps     int    `json:"down_mbps,omitempty"`
	PortHop      string `json:"port_hop,omitempty"` // hy2 跳跃段回显（订阅 ports/mport）
}

// EffectiveSecurity 返回生效安全层：显式值优先；旧 realized（无 security 字段）
// 按 reality 指纹（public_key 非空）回退推导（订阅输出兼容存量数据）。
// 回退推导只覆盖 reality/none——tls 是 P3 新数据，必然显式携带 security 字段。
func (rc RealizedConfig) EffectiveSecurity() string {
	if rc.Security != "" {
		return rc.Security
	}
	if rc.PublicKey != "" {
		return SecurityReality
	}
	return SecurityNone
}

// hy2 端口跳跃段长边界（spec §2）：默认 32，可用公共端口不足最小段长（8）时允许关闭
// 跳跃退回固定单端口，上限 200（配置膨胀可控）。XrayMinVersionHy2 为 hy2 inbound 引入
// 版本（agent 版本门控）；Hy2PortHopInterval 为 udpHop.interval 默认区间（秒，字符串区间）。
const (
	Hy2PortHopDefaultLen = 32
	Hy2PortHopMinLen     = 8
	Hy2PortHopMaxLen     = 200
	Hy2PortHopInterval   = "10-30"
	XrayMinVersionHy2    = "26.3.27"
)

// Hy2UserPassword 从用户 UUID 确定性派生 hy2 口令（auth 字符串）：
// panel 订阅、agent clients 填充、入口终结 outbound 三端共用（仿 SSUserPassword）。
func Hy2UserPassword(uuid string) string {
	sum := sha256.Sum256([]byte("lattix/hy2:" + uuid))
	return base64.RawURLEncoding.EncodeToString(sum[:24])
}

// ParsePortHop 解析 hy2 端口跳跃段 "a-b"（finalmask quicParams udpHop.ports 同形）；
// 1-65535 且 a<b。FormatPortHop 为其逆操作。
func ParsePortHop(s string) (start, end int, err error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("端口跳跃段应为 \"a-b\" 形式: %q", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(a))
	end, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || start < 1 || end > 65535 || start >= end {
		return 0, 0, fmt.Errorf("端口跳跃段非法: %q（须 1-65535 且 a<b）", s)
	}
	return start, end, nil
}

func FormatPortHop(start, end int) string { return fmt.Sprintf("%d-%d", start, end) }
