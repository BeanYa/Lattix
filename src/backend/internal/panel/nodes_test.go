package panel

import (
	"testing"

	"lattix/shared"
)

// TestRealityStreamSettingsPinsMinClientVer 验证 REALITY inbound 模板显式声明
// minClientVer=0：xray 26.7.11+ 在字段缺省时默认要求客户端版本 ≥ 26.3.27，
// 会拒绝版本声明较旧的客户端（如 mihomo/clash），显式 0 恢复不限版本行为。
func TestRealityStreamSettingsPinsMinClientVer(t *testing.T) {
	req := createNodeRequest{
		Protocol:    shared.ProtocolVLESS,
		Network:     shared.NetworkTCP,
		Fingerprint: shared.FingerprintChrome,
		Dest:        "dl.google.com:443",
		ServerNames: []string{"dl.google.com"},
		ShortID:     "0123abcd",
	}
	ss := realityStreamSettings(req)
	rs, ok := ss["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 realitySettings: %v", ss)
	}
	if rs["minClientVer"] != "0" {
		t.Fatalf("minClientVer 应显式为 0（xray 26.7.11+ 缺省默认 26.3.27 会拒绝旧客户端）: %v", rs["minClientVer"])
	}
}
