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

// TestNormalizeVMessCipher 验证 vmess cipher：显式值保留、缺省回退 auto、
// 非法值报错、非 vmess 协议强制清空。
func TestNormalizeVMessCipher(t *testing.T) {
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "aes-128-gcm" {
		t.Errorf("cipher 应保留 aes-128-gcm，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "auto" {
		t.Errorf("cipher 默认应为 auto，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "none"}
	if err := req.normalize(); err == nil {
		t.Error("非法 cipher 应报错")
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "" {
		t.Errorf("非 vmess 协议 cipher 应清空，实际 %q", req.Cipher)
	}
}
