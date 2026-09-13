package panel

import (
	"encoding/json"
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

// TestNormalizeTransportSecurityMatrix 验证 spec §2 矩阵的 normalize 落地：
// 合法组合放行并补默认值，矩阵外组合 400（报错指明冲突字段）。
func TestNormalizeTransportSecurityMatrix(t *testing.T) {
	// 合法：vmess+ws → security 自动推导 none，path/host 保留
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Path: "/p", Host: "h.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+ws 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Path != "/p" || req.Host != "h.example.com" {
		t.Errorf("vmess+ws 归一化不符: security=%q path=%q host=%q", req.Security, req.Path, req.Host)
	}
	if req.ShortID != "" || req.Dest != "" || len(req.ServerNames) != 0 {
		t.Errorf("security=none 应清空 reality 专有字段: %+v", req)
	}
	// 合法：vless+httpupgrade+VLESS Encryption（spec §2 脚注 1）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkHTTPUpgrade, Encryption: shared.VLessEncMLKEM768}
	if err := req.normalize(); err != nil {
		t.Fatalf("vless+httpupgrade+Encryption 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Flow != "" || req.Path != "/" {
		t.Errorf("vless+httpupgrade 归一化不符: security=%q flow=%q path=%q", req.Security, req.Flow, req.Path)
	}
	// 非法：reality × ws
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Security: shared.SecurityReality}
	if err := req.normalize(); err == nil {
		t.Error("reality×ws 应 400")
	}
	// 非法：trojan × ws（推导 none，trojan 不允许 none）
	req = &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("trojan+ws 应 400")
	}
	// 非法：vless+ws 无 Encryption
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("vless+ws 无 Encryption 应 400")
	}
	// 非法：ss 显式带传输层
	req = &createNodeRequest{Protocol: shared.ProtocolShadowsocks, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("ss 带 network 应 400")
	}
	// 非法：security=tls（P3 才开放）
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS}
	if err := req.normalize(); err == nil {
		t.Error("security=tls 应 400（P3 提供）")
	}
	// 非法：vision × none（vision 仅 vless+tcp+reality）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityNone, Encryption: shared.VLessEncX25519, Flow: shared.FlowVision}
	if err := req.normalize(); err == nil {
		t.Error("vision+none 应 400")
	}
	// 回归：存量默认（vless 空表单）仍 = tcp+reality+vision
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Security != shared.SecurityReality || req.Network != shared.NetworkTCP || req.Flow != shared.FlowVision {
		t.Errorf("存量默认不符: security=%q network=%q flow=%q", req.Security, req.Network, req.Flow)
	}
}

// TestBuildVirtualConfigWSPlain 验证 ws/httpupgrade + security=none 模板形状
// （xray 25.x：ws 在 wsSettings、host 在 headers.Host；httpupgrade 在 httpupgradeSettings）。
func TestBuildVirtualConfigWSPlain(t *testing.T) {
	req := createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS,
		Security: shared.SecurityNone, Path: "/p", Host: "h.example.com"}
	vc := buildVirtualConfig(req)
	if vc.Security != shared.SecurityNone {
		t.Errorf("VirtualConfig.Security 应为 none，实际 %q", vc.Security)
	}
	var tmpl struct {
		StreamSettings struct {
			Network    string `json:"network"`
			Security   string `json:"security"`
			WsSettings struct {
				Path    string            `json:"path"`
				Headers map[string]string `json:"headers"`
			} `json:"wsSettings"`
			RealitySettings json.RawMessage `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl); err != nil {
		t.Fatal(err)
	}
	ss := tmpl.StreamSettings
	if ss.Network != "ws" || ss.Security != "none" {
		t.Errorf("streamSettings 顶层不符: network=%q security=%q", ss.Network, ss.Security)
	}
	if ss.WsSettings.Path != "/p" || ss.WsSettings.Headers["Host"] != "h.example.com" {
		t.Errorf("wsSettings 不符: %+v", ss.WsSettings)
	}
	if len(ss.RealitySettings) != 0 {
		t.Error("security=none 模板不应带 realitySettings")
	}

	req = createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkHTTPUpgrade,
		Security: shared.SecurityNone, Path: "/hu", Host: "cdn.example.com"}
	vc = buildVirtualConfig(req)
	var tmpl2 struct {
		StreamSettings struct {
			Network             string `json:"network"`
			HTTPUpgradeSettings struct {
				Path string `json:"path"`
				Host string `json:"host"`
			} `json:"httpupgradeSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl2); err != nil {
		t.Fatal(err)
	}
	if tmpl2.StreamSettings.Network != "httpupgrade" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Path != "/hu" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Host != "cdn.example.com" {
		t.Errorf("httpupgradeSettings 不符: %+v", tmpl2.StreamSettings)
	}
}
