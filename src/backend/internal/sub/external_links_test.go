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

func TestBuildExternalLinkSSWithSlashPassword(t *testing.T) {
	// "00?" 使 StdEncoding("aes-128-gcm:00?") = "YWVzLTEyOC1nY206MDA/" 含 "/"，
	// url.Parse 以 "/" 终止 authority 导致旧实现无法回环；RawURLEncoding 输出为
	// "YWVzLTEyOC1nY206MDA_"（URL-safe），回环必须成功。
	link, ok := buildExternalLink(extNode("ss-slash", "ss", "5.6.7.8", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "00?",
	}))
	if !ok {
		t.Fatal("ss link failed")
	}
	userinfo := strings.SplitN(strings.TrimPrefix(link, "ss://"), "@", 2)[0]
	if strings.ContainsAny(userinfo, "/+") {
		t.Fatalf("ss userinfo must be url-safe base64, got %q in %q", userinfo, link)
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Type != "ss" || back.Server != "5.6.7.8" || back.Port != 8388 ||
		back.Extra["method"] != "aes-128-gcm" || back.Extra["password"] != "00?" {
		t.Fatalf("round trip = %+v", back)
	}
}

func TestBuildExternalLinkWireguardKeepsPublicKey(t *testing.T) {
	link, ok := buildExternalLink(extNode("wg", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "priv", "public_key": "pub", "ip": "10.0.0.2",
	}))
	if !ok {
		t.Fatal("wg link failed")
	}
	if !strings.Contains(link, "public_key=pub") {
		t.Fatalf("wg link missing public_key: %q", link)
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	if nodes[0].Extra["public_key"] != "pub" {
		t.Fatalf("round trip lost public_key: %+v", nodes[0])
	}
}

func TestBuildExternalLinkYAMLKeySuppression(t *testing.T) {
	link, ok := buildExternalLink(extNode("yaml-v", "vless", "1.2.3.4", 443, map[string]any{
		"uuid": "11111111-2222-3333-4444-555555555555", "network": "tcp",
		"servername": "cdn.example.com", "client-fingerprint": "chrome", "auth": "k9",
	}))
	if !ok {
		t.Fatal("link failed")
	}
	for _, leaked := range []string{"servername=", "client-fingerprint=", "network=", "reality-opts="} {
		if strings.Contains(link, leaked) {
			t.Fatalf("consumed key leaked %q: %q", leaked, link)
		}
	}
	if !strings.Contains(link, "sni=cdn.example.com") || !strings.Contains(link, "auth=k9") {
		t.Fatalf("expected sni/auth in link: %q", link)
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Extra["sni"] != "cdn.example.com" || back.Extra["auth"] != "k9" {
		t.Fatalf("round trip lost keys: %+v", back.Extra)
	}
}

func TestBuildExternalLinkInsecurePassthrough(t *testing.T) {
	// insecure/allowInsecure/allow_insecure 是 URI 约定参数（v2rayN 发
	// allowInsecure=1，hy2 客户端发 insecure=1），抑制后自签/SNI 不符证书
	// 的节点链接会 TLS 校验失败（回归：skip 列表曾误抑这三个键）。
	link, ok := buildExternalLink(extNode("v-ins", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "insecure": "1",
	}))
	if !ok {
		t.Fatal("vless link failed")
	}
	if !strings.Contains(link, "insecure=1") {
		t.Fatalf("link lost insecure: %q", link)
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	if nodes[0].Extra["insecure"] != "1" {
		t.Fatalf("round trip lost insecure: %+v", nodes[0].Extra)
	}
}

func TestBuildExternalLinkWireguardAddressPsk(t *testing.T) {
	// params 只重建 endpoint/private_key/public_key；address/preshared_key/mtu/
	// reserved/ipv6 必须作为标准 wireguard:// 参数透传（回归：曾全被 skip 抑制）。
	link, ok := buildExternalLink(extNode("wg", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "priv", "ip": "10.0.0.2", "address": "10.0.0.2",
		"preshared_key": "ps", "mtu": "1420", "reserved": "1,2,3", "ipv6": "fd00::1",
	}))
	if !ok {
		t.Fatal("wg link failed")
	}
	for _, want := range []string{
		"address=10.0.0.2", "preshared_key=ps", "mtu=1420",
		"reserved=1%2C2%2C3", "ipv6=fd00%3A%3A1",
	} {
		if !strings.Contains(link, want) {
			t.Fatalf("wg link missing %q: %q", want, link)
		}
	}
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Type != "wireguard" || back.Server != "wg.example.com" || back.Port != 51820 {
		t.Fatalf("round trip = %+v", back)
	}
	for key, want := range map[string]string{
		"private_key": "priv", "address": "10.0.0.2", "preshared_key": "ps",
		"mtu": "1420", "reserved": "1,2,3", "ipv6": "fd00::1",
	} {
		if back.Extra[key] != want {
			t.Fatalf("round trip lost %s: %+v", key, back.Extra)
		}
	}
}

func TestBuildExternalLinkSkipsNonScalar(t *testing.T) {
	// 嵌套对象无法在 query 中表达：必须跳过；标量未知键仍透传。
	link, ok := buildExternalLink(extNode("v-ns", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "auth": "k9",
		"custom-map": map[string]any{"a": "b"},
	}))
	if !ok {
		t.Fatal("vless link failed")
	}
	if strings.Contains(link, "custom-map=") {
		t.Fatalf("non-scalar leaked: %q", link)
	}
	if !strings.Contains(link, "auth=k9") {
		t.Fatalf("scalar unknown key lost: %q", link)
	}
}
