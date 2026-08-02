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
