package shared

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
)

// Protocols 是向导可选的全部协议。
var Protocols = []string{
	ProtocolVLESS, ProtocolVMess, ProtocolTrojan, ProtocolShadowsocks,
	ProtocolSocks, ProtocolHTTP, ProtocolDokodemo,
}

// 传输方式（network）：Reality 仅支持这三种。
const (
	NetworkTCP   = "tcp"
	NetworkGRPC  = "grpc"
	NetworkXHTTP = "xhttp"
)

// Networks 是 reality 协议可选的传输方式。
var Networks = []string{NetworkTCP, NetworkGRPC, NetworkXHTTP}

// XHTTP 的 mode 可选值（xray xhttpSettings.mode）。
var XHTTPModes = []string{"auto", "packet-up", "stream-up"}

const (
	FlowVision        = "xtls-rprx-vision" // vless flow，仅 tcp
	FingerprintChrome = "chrome"           // 默认 uTLS 指纹
)

// VLESS Encryption 认证方式（xray `vlessenc` 生成 decryption/encryption 对，
// 替代 "decryption": "none"）。与 flow vision 互斥。
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
// PRIVATE_KEY/TAG 为纯字符串替换。
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
)

// VirtualConfig 是面板侧虚拟配置（nodes.config_template）：xray inbound JSON 模板 + 占位符。
// Flow/Network/ServiceName/Path/Mode/Host/Method 为协议参数，
// Agent 据此构造用户条目并随 realized_config 回显（订阅生成依赖）。
type VirtualConfig struct {
	Protocol    string          `json:"protocol"`       // vless/vmess/trojan/shadowsocks/socks/http/dokodemo
	Port        int             `json:"port,omitempty"` // 0 = Agent 自动挑选空闲端口
	Flow        string          `json:"flow,omitempty"` // vless：xtls-rprx-vision 或空（仅 tcp）
	Network     string          `json:"network,omitempty"`
	ServiceName string          `json:"service_name,omitempty"` // grpc
	Path        string          `json:"path,omitempty"`         // xhttp
	Mode        string          `json:"mode,omitempty"`         // xhttp
	Host        string          `json:"host,omitempty"`         // xhttp
	Method      string          `json:"method,omitempty"`       // shadowsocks
	Fingerprint string          `json:"fingerprint,omitempty"`  // 客户端 uTLS 指纹（订阅侧参数，Agent 回显）
	Encryption  string          `json:"encryption,omitempty"`   // vless：VLESS Encryption 认证方式（x25519/mlkem768）
	Template    json.RawMessage `json:"template"`               // xray inbound JSON 模板，含占位符
}

// RealizedConfig 是 Agent 上报的实际生效值（nodes.realized_config），
// 面板生成订阅（§9）依赖这些字段；reality 字段对非 reality 协议为空。
type RealizedConfig struct {
	Port        int    `json:"port"`
	PublicKey   string `json:"public_key,omitempty"`
	ShortID     string `json:"short_id,omitempty"`
	ServerName  string `json:"server_name,omitempty"`
	Flow        string `json:"flow,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Network     string `json:"network,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Path        string `json:"path,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Host        string `json:"host,omitempty"`
	Method      string `json:"method,omitempty"`
	PSK         string `json:"psk,omitempty"`        // ss 2022-blake3 节点级 PSK（订阅拼接 "PSK:用户密钥"）
	Encryption  string `json:"encryption,omitempty"` // vless：VLESS Encryption 客户端字符串（订阅 encryption 字段）
}
