package sub

import (
	"reflect"
	"testing"
)

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

func TestBuildExternalSingboxALPNIdleAndUnknown(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("any", "anytls", "1.2.3.4", 443, map[string]any{
		"password": "pw", "sni": "a.example.com", "alpn": []any{"h2"},
		"idle-session-check-interval": 30, "auth": "token-9",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	tls := m["tls"].(map[string]any)
	if !reflect.DeepEqual(tls["alpn"], []string{"h2"}) {
		t.Fatalf("alpn = %+v", tls["alpn"])
	}
	if m["idle_session_check_interval"] != 30 {
		t.Fatalf("idle_session_check_interval = %+v", m["idle_session_check_interval"])
	}
	if m["auth"] != "token-9" {
		t.Fatalf("unknown key not preserved: %+v", m)
	}
	if _, dup := m["sni"]; dup {
		t.Fatalf("consumed sni leaked: %+v", m)
	}
}

func TestBuildExternalSingboxWGIPv6(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("wg", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "priv", "ip": "10.0.0.2", "ipv6": "fd00::1, fd00::2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	addr := m["local_address"].([]string)
	if len(addr) != 3 || addr[1] != "fd00::1" || addr[2] != "fd00::2" {
		t.Fatalf("local_address = %+v", addr)
	}
}

func TestBuildExternalSingboxVmessTypeNotOverwritten(t *testing.T) {
	// v2rayN 的 vmess 分享链接 JSON payload 带 "type":"none"、"net":"tcp"，
	// 落入 Extra 后不得覆盖出站 type（回归：applySingboxRaw 曾把 type 覆写为 none）。
	ob, err := buildExternalSingbox(extNode("vm", "vmess", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "none", "net": "tcp",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	if m["type"] != "vmess" {
		t.Fatalf("type overwritten = %+v", m)
	}
	if _, dup := m["net"]; dup {
		t.Fatalf("net leaked into outbound = %+v", m)
	}
}

func TestBuildExternalSingboxSSPlugin(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("ss-plug", "ss", "1.2.3.4", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "p", "plugin": "obfs-local",
		"plugin-opts": map[string]any{"obfs": "http"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	if m["plugin"] != "obfs-local" {
		t.Fatalf("plugin = %+v", m["plugin"])
	}
	opts, ok := m["plugin_opts"].(map[string]any)
	if !ok || opts["obfs"] != "http" {
		t.Fatalf("plugin_opts = %+v", m["plugin_opts"])
	}
}
