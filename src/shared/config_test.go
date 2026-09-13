package shared

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPortLayers(t *testing.T) {
	cases := map[string]string{
		ProtocolVLESS:       "tcp",
		ProtocolVMess:       "tcp",
		ProtocolTrojan:      "tcp",
		ProtocolSocks:       "tcp",
		ProtocolHTTP:        "tcp",
		ProtocolShadowsocks: "tcp,udp",
		ProtocolDokodemo:    "tcp,udp",
	}
	for protocol, want := range cases {
		if got := PortLayers(protocol); got != want {
			t.Errorf("PortLayers(%s) = %q, want %q", protocol, got, want)
		}
	}
}

func TestLayersOverlap(t *testing.T) {
	if !LayersOverlap("tcp,udp", "tcp") {
		t.Error("tcp,udp 与 tcp 应重叠")
	}
	if LayersOverlap("udp", "tcp") {
		t.Error("udp 与 tcp 不应重叠")
	}
	if !LayersOverlap("tcp,udp", "udp") {
		t.Error("tcp,udp 与 udp 应重叠")
	}
}

func TestVMessCiphers(t *testing.T) {
	if !ValidValue(VMessCipherAuto, VMessCiphers) ||
		!ValidValue(VMessCipherAES128GCM, VMessCiphers) ||
		!ValidValue(VMessCipherChacha20, VMessCiphers) {
		t.Error("VMessCiphers 应包含 auto/aes-128-gcm/chacha20-poly1305")
	}
	if ValidValue("none", VMessCiphers) {
		t.Error("none 不是合法 vmess cipher")
	}
}

func TestNetworksSplit(t *testing.T) {
	for _, n := range []string{"tcp", "grpc", "xhttp", "ws", "httpupgrade"} {
		if !ValidValue(n, Networks) {
			t.Errorf("Networks 应包含 %s", n)
		}
	}
	for _, n := range []string{NetworkTCP, NetworkGRPC, NetworkXHTTP} {
		if !ValidValue(n, RealityNetworks) {
			t.Errorf("RealityNetworks 应包含 %s", n)
		}
	}
	for _, n := range []string{NetworkWS, NetworkHTTPUpgrade} {
		if ValidValue(n, RealityNetworks) {
			t.Errorf("RealityNetworks 不应包含 %s（xray 官方约束）", n)
		}
	}
}

func TestSecurities(t *testing.T) {
	if !ValidValue(SecurityReality, Securities) || !ValidValue(SecurityTLS, Securities) || !ValidValue(SecurityNone, Securities) {
		t.Error("Securities 应包含 reality/tls/none")
	}
}

func TestEffectiveSecurity(t *testing.T) {
	if got := (RealizedConfig{Security: SecurityNone}).EffectiveSecurity(); got != SecurityNone {
		t.Errorf("显式 none 应原样返回，实际 %q", got)
	}
	if got := (RealizedConfig{PublicKey: "pk"}).EffectiveSecurity(); got != SecurityReality {
		t.Errorf("旧 realized（有公钥无 security 字段）应回退 reality，实际 %q", got)
	}
	if got := (RealizedConfig{}).EffectiveSecurity(); got != SecurityNone {
		t.Errorf("空 realized 应为 none，实际 %q", got)
	}
}

func TestCertModes(t *testing.T) {
	if !ValidValue(CertModeSelfSign, CertModes) || !ValidValue(CertModeACME, CertModes) {
		t.Error("CertModes 应包含 selfsign/acme")
	}
}

// TestTLSConfigFieldsRoundTrip 验证 TLS 证书字段 JSON 往返（panel 模板落库与
// agent realized 上报共用同一结构体，键名是面板/agent 契约）。
func TestTLSConfigFieldsRoundTrip(t *testing.T) {
	vc := VirtualConfig{Protocol: ProtocolVMess, Security: SecurityTLS,
		CertMode: CertModeSelfSign, TLSDomain: "www.example.com"}
	b, err := json.Marshal(vc)
	if err != nil {
		t.Fatal(err)
	}
	var back VirtualConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.CertMode != CertModeSelfSign || back.TLSDomain != "www.example.com" {
		t.Errorf("VirtualConfig TLS 字段往返不符: %+v", back)
	}
	rc := RealizedConfig{Port: 443, Security: SecurityTLS, SNI: "www.example.com",
		CertSHA256: strings.Repeat("ab", 32)}
	b, err = json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	var rcBack RealizedConfig
	if err := json.Unmarshal(b, &rcBack); err != nil {
		t.Fatal(err)
	}
	if rcBack.SNI != "www.example.com" || len(rcBack.CertSHA256) != 64 {
		t.Errorf("RealizedConfig TLS 字段往返不符: %+v", rcBack)
	}
	if PlaceholderTLSCertFile != "{{TLS_CERT_FILE}}" || PlaceholderTLSKeyFile != "{{TLS_KEY_FILE}}" {
		t.Error("TLS 证书占位符与 spec §3.1 不一致")
	}
}

// TestHysteria2Protocol 验证 hy2 协议常量入向导集合且分层为 udp-only（P4）。
func TestHysteria2Protocol(t *testing.T) {
	if !ValidValue(ProtocolHysteria2, Protocols) {
		t.Fatal("Protocols 缺少 hysteria")
	}
	if got := PortLayers(ProtocolHysteria2); got != "udp" {
		t.Fatalf("PortLayers(hysteria) = %q, want udp", got)
	}
	// 存量回归：ss/dokodemo tcp,udp；vless tcp。
	if got := PortLayers(ProtocolShadowsocks); got != "tcp,udp" {
		t.Fatalf("PortLayers(ss) 回归: %q", got)
	}
	if got := PortLayers(ProtocolVLESS); got != "tcp" {
		t.Fatalf("PortLayers(vless) 回归: %q", got)
	}
	// hy2 不是 reality 协议（无 dest/密钥对），但有用户列表。
	if IsRealityProtocol(ProtocolHysteria2) {
		t.Fatal("hysteria 不应为 reality 协议")
	}
	if !HasUserList(ProtocolHysteria2) {
		t.Fatal("hysteria 应有用户列表")
	}
}

// TestHy2UserPassword 验证口令派生确定性与两端一致（agent 填充与订阅共用）。
func TestHy2UserPassword(t *testing.T) {
	a := Hy2UserPassword("11111111-2222-3333-4444-555555555555")
	b := Hy2UserPassword("11111111-2222-3333-4444-555555555555")
	c := Hy2UserPassword("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if a == "" || a != b || a == c {
		t.Fatalf("派生应确定且随输入变化: %q %q %q", a, b, c)
	}
}

// TestParsePortHop 验证端口段解析/格式化往返与非法输入。
func TestParsePortHop(t *testing.T) {
	start, end, err := ParsePortHop("20000-20031")
	if err != nil || start != 20000 || end != 20031 {
		t.Fatalf("ParsePortHop = %d,%d,%v", start, end, err)
	}
	if got := FormatPortHop(start, end); got != "20000-20031" {
		t.Fatalf("FormatPortHop = %q", got)
	}
	for _, bad := range []string{"", "abc", "20000", "20031-20000", "0-100", "20000-70000", "20000-20000"} {
		if _, _, err := ParsePortHop(bad); err == nil {
			t.Fatalf("ParsePortHop(%q) 应报错", bad)
		}
	}
}
