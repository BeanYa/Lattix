package sub

import (
	"encoding/base64"
	"encoding/json"
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

// TestVMessShareLinkCipher 验证 vmess 分享链接的 scy 取自 VirtualConfig.Cipher，
// 模板为空/损坏时回退 auto。
func TestVMessShareLinkCipher(t *testing.T) {
	rc := testRealized()
	decode := func(t *testing.T, link string) map[string]string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			t.Fatalf("vmess link 解码失败: %v", err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("vmess JSON 解析失败: %v", err)
		}
		return m
	}

	n := testNode("1.2.3.4", shared.ProtocolVMess)
	n.ConfigTemplate = json.RawMessage(`{"protocol":"vmess","cipher":"chacha20-poly1305"}`)
	link, ok := buildShareLink(n, rc, "uuid")
	if !ok {
		t.Fatal("vmess link unsupported")
	}
	if got := decode(t, link)["scy"]; got != "chacha20-poly1305" {
		t.Errorf("scy 应为 chacha20-poly1305，实际 %q", got)
	}

	n.ConfigTemplate = nil
	link, ok = buildShareLink(n, rc, "uuid")
	if !ok {
		t.Fatal("vmess link unsupported")
	}
	if got := decode(t, link)["scy"]; got != "auto" {
		t.Errorf("空模板 scy 应回退 auto，实际 %q", got)
	}
}
