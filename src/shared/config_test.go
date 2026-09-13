package shared

import "testing"

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
