package sub

import (
	"strings"
	"testing"

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
	if p.Type != "wireguard" || p.PrivateKey != "pk" || p.IP != "10.0.0.2" || p.PublicKey != "peer" || p.MTU != 1420 {
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
