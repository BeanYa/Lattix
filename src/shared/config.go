package shared

import "encoding/json"

// MVP 协议范围（§1）：仅 VLESS + Reality。
const (
	ProtocolVLESS     = "vless"
	FlowVision        = "xtls-rprx-vision" // flow 固定
	FingerprintChrome = "chrome"           // uTLS 指纹固定
)

// 模板占位符（§7）：Agent 填值后原样写入，不存在任何"翻译层"。
const (
	// PlaceholderPort 端口占位符；向导中端口留空时由 Agent 挑空闲端口并随 apply_result 上报。
	PlaceholderPort = "{{PORT}}"
	// PlaceholderRealityPrivateKey Reality 私钥占位符；由 Agent 执行 `xray x25519` 生成，
	// 私钥不出服务器，public_key 随 apply_result 上报。
	PlaceholderRealityPrivateKey = "{{PRIVATE_KEY}}"
)

// VirtualConfig 是面板侧虚拟配置（nodes.config_template）：xray inbound JSON 模板 + 占位符。
type VirtualConfig struct {
	Protocol string          `json:"protocol"`       // MVP 恒为 vless
	Port     int             `json:"port,omitempty"` // 0 = Agent 自动挑选空闲端口
	Template json.RawMessage `json:"template"`       // xray inbound JSON 模板，含占位符
}

// RealizedConfig 是 Agent 上报的实际生效值（nodes.realized_config），
// 面板生成订阅（§9）依赖这些字段。
type RealizedConfig struct {
	Port        int    `json:"port"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	ServerName  string `json:"server_name"`
	Flow        string `json:"flow"`
	Fingerprint string `json:"fingerprint"`
}
