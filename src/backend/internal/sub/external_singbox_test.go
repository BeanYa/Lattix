package sub

import "testing"

func TestBuildExternalSingbox(t *testing.T) {
	vless := extNode("东京", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "ws", "path": "/ws",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	})
	ob, err := buildExternalSingbox(vless)
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	if m["type"] != "vless" || m["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("vless = %+v", m)
	}
	tls := m["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "pub" || reality["short_id"] != "abcd" {
		t.Fatalf("reality = %+v", reality)
	}
	tr := m["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Fatalf("transport = %+v", tr)
	}

	hy2 := extNode("香港", "hysteria2", "hk.example.com", 443, map[string]any{
		"password": "p1", "obfs": "salamander", "obfs-password": "op", "sni": "hk.example.com",
	})
	ob, err = buildExternalSingbox(hy2)
	if err != nil {
		t.Fatal(err)
	}
	m = ob.(map[string]any)
	if m["password"] != "p1" {
		t.Fatalf("hy2 = %+v", m)
	}
	if obfs := m["obfs"].(map[string]any); obfs["password"] != "op" {
		t.Fatalf("hy2 obfs = %+v", obfs)
	}

	ssr := extNode("SSR", "ssr", "1.2.3.4", 8388, map[string]any{"protocol": "auth_sha1_v4"})
	if _, err := buildExternalSingbox(ssr); err == nil {
		t.Fatal("ssr should be unsupported in sing-box")
	}
}

func TestBuildExternalSingboxTrojanTLS(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("tr", "trojan", "1.2.3.4", 443, map[string]any{
		"password": "pw", "sni": "t.example.com", "allowInsecure": "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	tls := m["tls"].(map[string]any)
	if tls["server_name"] != "t.example.com" || tls["insecure"] != true {
		t.Fatalf("tls = %+v", tls)
	}
}
