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

// TestBuildShareLinkWSPlain 验证 ws/httpupgrade + security=none 的分享链接：
// vmess JSON 的 tls 为空、net/host/path 正确；vless 链接无 pbk/sid/sni、带 type/encryption。
func TestBuildShareLinkWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	n := testNode("1.2.3.4", shared.ProtocolVMess)
	link, ok := buildShareLink(n, rc, "uuid")
	if !ok {
		t.Fatal("vmess ws link unsupported")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["net"] != "ws" || m["host"] != "h.example.com" || m["path"] != "/p" {
		t.Errorf("vmess ws 字段不符: %v", m)
	}
	if m["tls"] != "" {
		t.Errorf("security=none 的 vmess tls 字段应为空，实际 %q", m["tls"])
	}

	rc2 := shared.RealizedConfig{Port: 8443, Network: shared.NetworkHTTPUpgrade, Path: "/hu",
		Security: shared.SecurityNone, Encryption: "mlkem768x25519plus.0rtt.XXX"}
	link2, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc2, "uuid")
	if !ok {
		t.Fatal("vless httpupgrade link unsupported")
	}
	for _, want := range []string{"type=httpupgrade", "security=none", "encryption=mlkem768x25519plus", "path=%2Fhu"} {
		if !strings.Contains(link2, want) {
			t.Errorf("vless httpupgrade 链接缺 %q: %s", want, link2)
		}
	}
	for _, absent := range []string{"pbk=", "sid=", "sni="} {
		if strings.Contains(link2, absent) {
			t.Errorf("security=none 链接不应含 %q: %s", absent, link2)
		}
	}
}

// TestBuildProxyWSPlain 验证 mihomo 输出：security=none 无 tls/reality-opts；
// ws → ws-opts；httpupgrade → network=ws + v2ray-http-upgrade（mihomo 惯例）。
func TestBuildProxyWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.TLS || p.RealityOpts != nil || p.Servername != "" {
		t.Errorf("明文 ws 不应带 tls/reality-opts/servername: %+v", p)
	}
	if p.Network != "ws" || p.WsOpts == nil || p.WsOpts.Path != "/p" || p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Errorf("ws-opts 不符: %+v", p.WsOpts)
	}

	rc.Network = shared.NetworkHTTPUpgrade
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != "ws" || p.WsOpts == nil || !p.WsOpts.V2rayHTTPUpgrade {
		t.Errorf("httpupgrade 应映射为 network=ws + v2ray-http-upgrade: network=%q opts=%+v", p.Network, p.WsOpts)
	}
}

// TestBuildProxyGRPCXHTTPPlain 验证 mihomo 输出：security=none 下 grpc/xhttp
// 也要填传输选项（panel normalize 放行 tcp/grpc/xhttp × none），否则
// serviceName/path 丢失。写法与 reality 分支一致。
func TestBuildProxyGRPCXHTTPPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkGRPC,
		ServiceName: "svc", Security: shared.SecurityNone}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.TLS || p.RealityOpts != nil {
		t.Errorf("明文 grpc 不应带 tls/reality-opts: %+v", p)
	}
	if p.Network != "grpc" || p.GrpcOpts == nil || p.GrpcOpts.ServiceName != "svc" {
		t.Errorf("grpc-opts 不符: network=%q opts=%+v", p.Network, p.GrpcOpts)
	}

	rc = shared.RealizedConfig{Port: 8443, Network: shared.NetworkXHTTP,
		Path: "/xh", Mode: "auto", Host: "h.example.com", Security: shared.SecurityNone}
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != "xhttp" || p.XhttpOpts == nil || p.XhttpOpts.Path != "/xh" ||
		p.XhttpOpts.Mode != "auto" || p.XhttpOpts.Host != "h.example.com" {
		t.Errorf("xhttp-opts 不符: network=%q opts=%+v", p.Network, p.XhttpOpts)
	}
}

// TestBuildSbOutboundWSPlain 验证 sing-box 输出：security=none 无 tls 块；
// ws 用 headers.Host，httpupgrade 用原生 httpupgrade transport（host 平铺）。
func TestBuildSbOutboundWSPlain(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS != nil {
		t.Errorf("security=none 不应输出 tls 块: %+v", ob.TLS)
	}
	if ob.Transport == nil || ob.Transport.Type != "ws" || ob.Transport.Path != "/p" ||
		ob.Transport.Headers["Host"] != "h.example.com" {
		t.Errorf("sing-box ws transport 不符: %+v", ob.Transport)
	}

	rc.Network = shared.NetworkHTTPUpgrade
	ob, err = buildSbOutbound(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.Transport == nil || ob.Transport.Type != "httpupgrade" || ob.Transport.Host != "h.example.com" {
		t.Errorf("sing-box httpupgrade transport 不符: %+v", ob.Transport)
	}
}

// TestQuanXSkipsPlainVLESS 验证 QuanX 对 security=none 节点尽力而为：跳过不输出。
func TestQuanXSkipsPlainVLESS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS, Security: shared.SecurityNone}
	if line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid"); line != "" {
		t.Errorf("QuanX 应跳过明文 vless，实际输出 %q", line)
	}
}

// TestVMessShareLinkOmitsEmptySecurityKeys 验证 v2rayN 惯例：security=none 的 vmess
// 分享 JSON 不携带空串 sni/fp/pbk/sid 键（P2 遗留清理 #5）。
func TestVMessShareLinkOmitsEmptySecurityKeys(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS,
		Path: "/p", Host: "h.example.com", Security: shared.SecurityNone}
	link, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if !ok {
		t.Fatal("vmess link unsupported")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sni", "fp", "pbk", "sid"} {
		if _, exists := m[k]; exists {
			t.Errorf("security=none 不应携带空键 %q: %v", k, m)
		}
	}
	if m["net"] != "ws" || m["host"] != "h.example.com" || m["path"] != "/p" {
		t.Errorf("传输字段回归不符: %v", m)
	}
	// reality 回归：四个键必须仍在且非空。
	rc2 := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP,
		Security: shared.SecurityReality, PublicKey: "pk", ShortID: "sid", ServerName: "dl.google.com"}
	link2, _ := buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc2, "uuid")
	raw2, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link2, "vmess://"))
	var m2 map[string]string
	if err := json.Unmarshal(raw2, &m2); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sni", "fp", "pbk", "sid"} {
		if m2[k] == "" {
			t.Errorf("reality 应携带非空 %q: %v", k, m2)
		}
	}
}

// TestBuildShareLinkTLS 验证 tls 分享链接：vless/trojan URI 带 security=tls+sni，
// 自签（CertSHA256 非空）回退 allowInsecure=1（URI 无 pin 表达）；ACME 无 insecure。
func TestBuildShareLinkTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "cdn.example.com", CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	link, ok := buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid")
	if !ok {
		t.Fatal("vless tls link unsupported")
	}
	for _, want := range []string{"security=tls", "sni=cdn.example.com", "allowInsecure=1"} {
		if !strings.Contains(link, want) {
			t.Errorf("vless tls 自签链接缺 %q: %s", want, link)
		}
	}
	if strings.Contains(link, "pbk=") {
		t.Errorf("tls 链接不应含 reality 参数: %s", link)
	}
	// trojan + ws + tls（P3 合法化组合）
	rc.Network = shared.NetworkWS
	rc.Path = "/tw"
	link, ok = buildShareLink(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if !ok {
		t.Fatal("trojan tls link unsupported")
	}
	for _, want := range []string{"trojan://", "security=tls", "sni=cdn.example.com", "type=ws", "path=%2Ftw", "allowInsecure=1"} {
		if !strings.Contains(link, want) {
			t.Errorf("trojan ws tls 链接缺 %q: %s", want, link)
		}
	}
	// ACME：无 allowInsecure
	rc.CertSHA256 = ""
	link, _ = buildShareLink(testNode("1.2.3.4", shared.ProtocolVLESS), rc, "uuid")
	if strings.Contains(link, "allowInsecure") {
		t.Errorf("ACME 链接不应含 allowInsecure: %s", link)
	}
	// vmess JSON：tls=tls + sni（无 pin/insecure 表达）
	rc.Security = shared.SecurityTLS
	rc.CertSHA256 = strings.Repeat("ab", 32)
	link, ok = buildShareLink(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if !ok {
		t.Fatal("vmess tls link unsupported")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["tls"] != "tls" || m["sni"] != "cdn.example.com" {
		t.Errorf("vmess tls JSON 不符: %v", m)
	}
	if _, exists := m["pbk"]; exists {
		t.Errorf("tls 的 vmess JSON 不应含 pbk: %v", m)
	}
}

// TestBuildProxyTLS 验证 mihomo 输出：tls + servername + client-fingerprint；
// 自签输出证书 pin（fingerprint）；trojan 走 sni 字段（清理 #2 分流）。
func TestBuildProxyTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkWS, Path: "/p",
		Security: shared.SecurityTLS, SNI: "cdn.example.com",
		CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if !p.TLS || p.Servername != "cdn.example.com" || p.RealityOpts != nil {
		t.Errorf("vmess tls 代理项不符: %+v", p)
	}
	if p.Fingerprint != strings.Repeat("ab", 32) {
		t.Errorf("自签应输出 pin: %+v", p)
	}
	if p.WsOpts == nil || p.WsOpts.Path != "/p" {
		t.Errorf("tls+ws 传输选项不符: %+v", p.WsOpts)
	}
	// trojan：sni 字段 + pin（清理 #2：不再无条件 applyReality）
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if !p.TLS || p.SNI != "cdn.example.com" || p.RealityOpts != nil || p.Fingerprint == "" {
		t.Errorf("trojan tls 代理项不符: %+v", p)
	}
	// ACME：无 pin、无 skip-cert-verify
	rc.CertSHA256 = ""
	p, err = buildProxy(testNode("1.2.3.4", shared.ProtocolVMess), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if p.Fingerprint != "" || p.SkipCertVerify != nil {
		t.Errorf("ACME 应走系统根验证: %+v", p)
	}
}

// TestBuildSbOutboundTLS 验证 sing-box 输出：普通 tls 变体（server_name=SNI，无 reality 块）；
// 自签回退 insecure:true（sing-box 无 cert pin 表达，§3.4）。
func TestBuildSbOutboundTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "cdn.example.com", CertSHA256: strings.Repeat("ab", 32), Fingerprint: shared.FingerprintChrome}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS == nil || ob.TLS.ServerName != "cdn.example.com" || ob.TLS.Reality != nil {
		t.Fatalf("sing-box tls 不符: %+v", ob.TLS)
	}
	if !ob.TLS.Insecure {
		t.Errorf("自签应回退 insecure: %+v", ob.TLS)
	}
	rc.CertSHA256 = ""
	ob, err = buildSbOutbound(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if ob.TLS == nil || ob.TLS.Insecure {
		t.Errorf("ACME 不应带 insecure: %+v", ob.TLS)
	}
}

// TestQuanXTrojanTLS 验证 QuanX 尽力而为：trojan×tls（ACME）输出 over-tls；
// 自签无 pin/insecure 表达 → 跳过（§3.4）。
func TestQuanXTrojanTLS(t *testing.T) {
	rc := shared.RealizedConfig{Port: 8443, Network: shared.NetworkTCP, Security: shared.SecurityTLS,
		SNI: "exit.example.com"}
	line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid")
	if !strings.Contains(line, "obfs=over-tls") || !strings.Contains(line, "obfs-host=exit.example.com") {
		t.Errorf("trojan tls（ACME）应输出 over-tls: %q", line)
	}
	if strings.Contains(line, "reality-pubkey") {
		t.Errorf("tls 不应含 reality 参数: %q", line)
	}
	rc.CertSHA256 = strings.Repeat("ab", 32)
	if line := buildQuanXLine(testNode("1.2.3.4", shared.ProtocolTrojan), rc, "uuid"); line != "" {
		t.Errorf("自签 trojan QuanX 无 pin 表达，应跳过: %q", line)
	}
}
