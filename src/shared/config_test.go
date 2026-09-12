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
