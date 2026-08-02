package sub

import (
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
)

func TestBuildExternalQuanX(t *testing.T) {
	if got := buildExternalQuanX(extNode("reality", "vless", "1.2.3.4", 443, map[string]any{
		"id": "x", "security": "reality", "pbk": "p", "sid": "s",
	})); got != "" {
		t.Fatalf("reality must be skipped: %q", got)
	}
	got := buildExternalQuanX(extNode("ss", "ss", "1.2.3.4", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "pw",
	}))
	if !strings.Contains(got, "shadowsocks=1.2.3.4:8388") || !strings.Contains(got, "method=aes-128-gcm") || !strings.Contains(got, "tag=ss") {
		t.Fatalf("ss quanx = %q", got)
	}
	if got := buildExternalQuanX(extNode("wg", "wireguard", "1.2.3.4", 51820, map[string]any{"private_key": "k"})); got != "" {
		t.Fatalf("wireguard should be skipped: %q", got)
	}
}

func TestBuildExternalLink(t *testing.T) {
	link, ok := buildExternalLink(extNode("东京 01", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome",
	}))
	if !ok {
		t.Fatal("vless link failed")
	}
	if !strings.HasPrefix(link, "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?") ||
		!strings.Contains(link, "pbk=pub") || !strings.Contains(link, "type=tcp") ||
		!strings.Contains(link, "#%E4%B8%9C%E4%BA%AC") {
		t.Fatalf("vless link = %q", link)
	}
	// 回环：序列化 → 重新解析 → 关键字段一致。
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Type != "vless" || back.Server != "1.2.3.4" || back.Port != 443 ||
		back.Extra["id"] != "11111111-2222-3333-4444-555555555555" || back.Extra["pbk"] != "pub" {
		t.Fatalf("round trip = %+v", back)
	}

	ssLink, ok := buildExternalLink(extNode("ss-01", "ss", "5.6.7.8", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "pass",
	}))
	if !ok {
		t.Fatal("ss link failed")
	}
	if !strings.HasPrefix(ssLink, "ss://") {
		t.Fatalf("ss link = %q", ssLink)
	}
	if _, _, err := extsub.ParseSubscription([]byte(ssLink)); err != nil {
		t.Fatalf("ss reparse err = %v", err)
	}

	if _, ok := buildExternalLink(extNode("x", "unknown", "1.2.3.4", 1, nil)); ok {
		t.Fatal("unknown protocol unexpectedly serialized")
	}
}
