package sub

import (
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func testNode(addr, protocol string) store.Node {
	return store.Node{ID: 1, Name: "n", ServerAddress: addr, Protocol: protocol, Status: store.NodeStatusActive}
}

func testRealized() shared.RealizedConfig {
	return shared.RealizedConfig{Port: 443, Network: shared.NetworkTCP,
		PublicKey: "pk", ShortID: "ab", ServerName: "example.com"}
}

// TestBuildShareLinkIPv6Bracket 验证分享链接对 IPv6 字面量加 []（§9），IPv4/域名不变。
func TestBuildShareLinkIPv6Bracket(t *testing.T) {
	rc := testRealized()
	cases := []struct {
		protocol string
		addr     string
		want     string
	}{
		{shared.ProtocolVLESS, "2400:cb00::1", "vless://uuid@[2400:cb00::1]:443?"},
		{shared.ProtocolVLESS, "1.2.3.4", "vless://uuid@1.2.3.4:443?"},
		{shared.ProtocolVLESS, "example.com", "vless://uuid@example.com:443?"},
		{shared.ProtocolTrojan, "2400:cb00::1", "trojan://uuid@[2400:cb00::1]:443?"},
		{shared.ProtocolShadowsocks, "2400:cb00::1", "@[2400:cb00::1]:443#"},
	}
	for _, c := range cases {
		link, ok := buildShareLink(testNode(c.addr, c.protocol), rc, "uuid")
		if !ok {
			t.Fatalf("%s link unsupported", c.protocol)
		}
		if !strings.Contains(link, c.want) {
			t.Errorf("%s %s link = %q, want substring %q", c.protocol, c.addr, link, c.want)
		}
	}
}

// TestBuildQuanXLineIPv6Bracket 验证 Quantumult X 行对 IPv6 字面量加 []。
func TestBuildQuanXLineIPv6Bracket(t *testing.T) {
	rc := testRealized()
	line := buildQuanXLine(testNode("2400:cb00::1", shared.ProtocolVLESS), rc, "uuid")
	if !strings.HasPrefix(line, "vless=[2400:cb00::1]:443,") {
		t.Errorf("quanx v6 line = %q, want prefix vless=[2400:cb00::1]:443,", line)
	}
	line = buildQuanXLine(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if !strings.HasPrefix(line, "trojan=1.2.3.4:443,") {
		t.Errorf("quanx v4 line = %q, want prefix trojan=1.2.3.4:443,", line)
	}
}
