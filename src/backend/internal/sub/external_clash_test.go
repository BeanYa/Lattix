package sub

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/extsub"
)

func extNode(name, typ, server string, port int, extra map[string]any) extsub.Node {
	return extsub.Node{Name: name, Type: typ, Server: server, Port: port, Extra: extra}
}

func TestBuildExternalClash(t *testing.T) {
	vless := extNode("东京", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	})
	p, err := buildExternalClash(vless)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "vless" || p.UUID != "11111111-2222-3333-4444-555555555555" ||
		!p.TLS || p.RealityOpts == nil || p.RealityOpts.PublicKey != "pub" ||
		p.RealityOpts.ShortID != "abcd" || p.ClientFingerprint != "chrome" ||
		p.Servername != "cdn.example.com" {
		t.Fatalf("vless = %+v", p)
	}

	hy2 := extNode("香港", "hysteria2", "hk.example.com", 443, map[string]any{
		"password": "p1", "obfs": "salamander", "obfs-password": "op", "sni": "hk.example.com",
	})
	p, err = buildExternalClash(hy2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "hysteria2" || p.Password != "p1" || p.Obfs != "salamander" || p.ObfsPassword != "op" {
		t.Fatalf("hy2 = %+v", p)
	}

	wg := extNode("WG", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "pk", "ip": "10.0.0.2", "public_key": "peer", "mtu": "1420",
	})
	p, err = buildExternalClash(wg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "wireguard" || p.PrivateKey != "pk" || p.IP != "10.0.0.2" || p.PublicKey != "peer" || p.MTU == nil || *p.MTU != 1420 {
		t.Fatalf("wg = %+v", p)
	}

	if _, err := buildExternalClash(extNode("未知", "hysteria", "x", 1, nil)); err == nil {
		t.Fatal("unknown protocol unexpectedly accepted")
	}
	if _, err := buildExternalClash(extNode("缺凭据", "vless", "x", 1, nil)); err == nil {
		t.Fatal("missing credential unexpectedly accepted")
	}
}

func TestBuildExternalClashWS(t *testing.T) {
	p, err := buildExternalClash(extNode("ws", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "ws", "path": "/ws", "host": "h.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != "ws" || p.WsOpts == nil || p.WsOpts.Path != "/ws" || p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Fatalf("ws = %+v", p)
	}
	if strings.Contains(p.Name, "h.example.com") {
		t.Fatalf("name must stay from config: %q", p.Name)
	}
}

func TestBuildExternalClashYAMLKeys(t *testing.T) {
	// mihomo YAML 订阅的键名（uuid/network/client-fingerprint + 嵌套 reality-opts）
	p, err := buildExternalClash(extNode("yaml-vless", "vless", "1.2.3.4", 443, map[string]any{
		"uuid": "11111111-2222-3333-4444-555555555555", "network": "tcp",
		"client-fingerprint": "chrome", "sni": "cdn.example.com",
		"reality-opts": map[string]any{"public-key": "pub", "short-id": "abcd"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID != "11111111-2222-3333-4444-555555555555" || !p.TLS ||
		p.RealityOpts == nil || p.RealityOpts.PublicKey != "pub" || p.RealityOpts.ShortID != "abcd" ||
		p.ClientFingerprint != "chrome" || p.Servername != "cdn.example.com" {
		t.Fatalf("yaml vless = %+v", p)
	}
}

func TestBuildExternalClashYAMLBoolInt(t *testing.T) {
	// yaml 原生 bool / int 类型
	p, err := buildExternalClash(extNode("yaml-hy2", "hysteria2", "hk.example.com", 443, map[string]any{
		"password": "p1", "sni": "hk.example.com", "skip-cert-verify": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.SkipCertVerify == nil || !*p.SkipCertVerify {
		t.Fatalf("yaml bool not read: %+v", p)
	}
	wg, err := buildExternalClash(extNode("yaml-wg", "wireguard", "wg.example.com", 51820, map[string]any{
		"private-key": "pk", "ip": "10.0.0.2", "mtu": 1420,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if wg.MTU == nil || *wg.MTU != 1420 {
		t.Fatalf("yaml int not read: %+v", wg)
	}
}

func TestBuildExternalClashTUICCongestionControl(t *testing.T) {
	p, err := buildExternalClash(extNode("tuic", "tuic", "1.2.3.4", 443, map[string]any{
		"uuid": "u", "password": "p", "congestion_control": "bbr",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.CongestionController != "bbr" {
		t.Fatalf("congestion_control not mapped: %+v", p)
	}
}

func TestBuildExternalClashYAMLServerNameAndKeys(t *testing.T) {
	vless, err := buildExternalClash(extNode("yaml-vless2", "vless", "1.2.3.4", 443, map[string]any{
		"uuid": "11111111-2222-3333-4444-555555555555", "servername": "cdn.example.com",
		"reality-opts": map[string]any{"public-key": "pub", "short-id": "abcd"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if vless.Servername != "cdn.example.com" {
		t.Fatalf("servername not mapped: %+v", vless)
	}
	wg, err := buildExternalClash(extNode("yaml-wg2", "wireguard", "wg.example.com", 51820, map[string]any{
		"private-key": "priv", "public-key": "pub", "ip": "10.0.0.2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if wg.PublicKey != "pub" {
		t.Fatalf("public-key not mapped: %+v", wg)
	}
	vmess, err := buildExternalClash(extNode("yaml-vmess", "vmess", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "network": "ws", "path": "/ws", "host": "h.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if vmess.Network != "ws" || vmess.WsOpts == nil || vmess.WsOpts.Path != "/ws" {
		t.Fatalf("vmess network not mapped: %+v", vmess)
	}
}

func TestBuildExternalClashHTTPTransportNoEmptyPath(t *testing.T) {
	p, err := buildExternalClash(extNode("v", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "http",
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(clashConfig{Proxies: []clashProxy{p}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "path:") {
		t.Fatalf("empty path emitted:\n%s", out)
	}
}

func TestClashProxyInlineRawMarshal(t *testing.T) {
	p := clashProxy{
		Name: "n", Type: "anytls", Server: "s", Port: 443, UDP: true,
		Raw: map[string]any{"auth": "token-123", "tfo": []any{"h2", "http/1.1"}},
	}
	out, err := yaml.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"auth: token-123", "tfo:", "- h2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("raw not inlined, missing %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "name:") != 1 {
		t.Fatalf("duplicate name key:\n%s", s)
	}
}

func TestBuildExternalClashAnyTLSFullFidelity(t *testing.T) {
	nodes, _, err := extsub.ParseSubscription([]byte(`
proxies:
  - name: '🇭🇰 香港01'
    type: anytls
    server: relay.moe233.org
    port: 443
    password: pass-123
    alpn: [h2, http/1.1]
    skip-cert-verify: false
    udp: true
    sni: sni.moe233.org
    auth: token-abc
`))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse = %+v err %v", nodes, err)
	}
	p, err := buildExternalClash(nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if p.SNI != "sni.moe233.org" {
		t.Fatalf("sni = %q", p.SNI)
	}
	if p.SkipCertVerify == nil || *p.SkipCertVerify {
		t.Fatalf("skip-cert-verify: false presence lost: %+v", p.SkipCertVerify)
	}
	if len(p.ALPN) != 2 || p.ALPN[0] != "h2" || p.ALPN[1] != "http/1.1" {
		t.Fatalf("alpn = %+v", p.ALPN)
	}
	if p.Raw["auth"] != "token-abc" {
		t.Fatalf("unknown key not preserved: %+v", p.Raw)
	}
	if _, dup := p.Raw["sni"]; dup {
		t.Fatalf("consumed sni leaked to raw: %+v", p.Raw)
	}
	out, err := yaml.Marshal(clashConfig{Proxies: []clashProxy{p}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"alpn:", "- h2", "skip-cert-verify: false", "sni: sni.moe233.org", "auth: token-abc"} {
		if !strings.Contains(s, want) {
			t.Fatalf("yaml missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "servername:") || strings.Contains(s, "password: pass-123") && strings.Count(s, "auth:") != 1 {
		t.Fatalf("yaml leakage:\n%s", s)
	}
}

func TestBuildExternalClashAnyTLSServerNameSource(t *testing.T) {
	// mihomo anytls 规范字段为 sni（adapter/outbound/anytls.go: `proxy:"sni"`）；
	// 源键为 servername（如 lattix 旧约定导出再导入）时经 externalYAMLFallback
	// 折算，输出仍只带 sni 一个键，避免 mihomo 系客户端 SNI 为空导致握手失败。
	p, err := buildExternalClash(extNode("anytls-sn", "anytls", "relay.example.org", 443, map[string]any{
		"password": "p1", "servername": "node1.qchwnd.example",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.SNI != "node1.qchwnd.example" {
		t.Fatalf("sni not mapped from servername source: %+v", p)
	}
	out, err := yaml.Marshal(clashConfig{Proxies: []clashProxy{p}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "sni: node1.qchwnd.example") || strings.Contains(s, "servername:") {
		t.Fatalf("anytls must emit mihomo canonical sni only:\n%s", s)
	}
}

func TestBuildExternalClashWSOptsSubfields(t *testing.T) {
	p, err := buildExternalClash(extNode("yaml-ws", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "network": "ws",
		"ws-opts": map[string]any{
			"path": "/ws", "max-early-data": 1024,
			"headers": map[string]any{"Host": "h.example.com"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.WsOpts == nil || p.WsOpts.Path != "/ws" || p.WsOpts.MaxEarlyData != 1024 ||
		p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Fatalf("ws-opts subfields lost: %+v", p.WsOpts)
	}
	if _, dup := p.Raw["ws-opts"]; dup {
		t.Fatalf("consumed ws-opts leaked to raw: %+v", p.Raw)
	}
}

func TestBuildExternalClashNoRawForKnownKeys(t *testing.T) {
	p, err := buildExternalClash(extNode("clean", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Raw != nil && len(p.Raw) != 0 {
		t.Fatalf("known keys leaked to raw: %+v", p.Raw)
	}
}
