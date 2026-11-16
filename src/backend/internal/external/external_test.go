package external

import (
	"testing"

	"lattix/shared"
)

func TestParseVLESSReality(t *testing.T) {
	link := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=abc123&sid=0123abcd&sni=www.cloudflare.com&fp=chrome&flow=xtls-rprx-vision#HK%20Node"
	exit, name, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exit.Protocol != shared.ProtocolVLESS || exit.Address != "example.com" || exit.Port != 443 {
		t.Fatalf("unexpected exit: %+v", exit)
	}
	if exit.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuid = %q", exit.UUID)
	}
	if exit.Security != shared.SecurityReality || exit.PublicKey != "abc123" || exit.ShortID != "0123abcd" {
		t.Fatalf("reality fields: %+v", exit)
	}
	if exit.SNI != "www.cloudflare.com" || exit.Fingerprint != "chrome" || exit.Flow != "xtls-rprx-vision" {
		t.Fatalf("transport fields: %+v", exit)
	}
	if name != "HK Node" {
		t.Fatalf("name = %q", name)
	}
}

func TestParseVLESSWebSocketTLS(t *testing.T) {
	link := "vless://uuid-1@1.2.3.4:8443?type=ws&security=tls&sni=cdn.example.com&path=%2Fws&host=cdn.example.com#ws"
	exit, _, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exit.Network != shared.NetworkWS || exit.Security != shared.SecurityTLS {
		t.Fatalf("transport: %+v", exit)
	}
	if exit.Path != "/ws" || exit.Host != "cdn.example.com" || exit.SNI != "cdn.example.com" {
		t.Fatalf("ws fields: %+v", exit)
	}
}

func TestParseSocks5(t *testing.T) {
	exit, _, err := Parse("socks5://user:pass@127.0.0.1:1080#local-socks")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exit.Protocol != shared.ProtocolSocks || exit.Address != "127.0.0.1" || exit.Port != 1080 {
		t.Fatalf("exit: %+v", exit)
	}
	if exit.Username != "user" || exit.Password != "pass" {
		t.Fatalf("credentials: %+v", exit)
	}
}

func TestParseVMess(t *testing.T) {
	link := "vmess://eyJ2IjoiMiIsInBzIjoidm0iLCJhZGQiOiJ2bS5leGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6InV1aWQtMiIsImFpZCI6IjAiLCJuZXQiOiJ3cyIsInR5cGUiOiJub25lIiwiaG9zdCI6InZtLmV4YW1wbGUuY29tIiwicGF0aCI6Ii92bSIsInRscyI6InRscyIsInNuaSI6InZtLmV4YW1wbGUuY29tIn0="
	exit, name, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exit.Protocol != shared.ProtocolVMess || exit.Address != "vm.example.com" || exit.Port != 443 {
		t.Fatalf("exit: %+v", exit)
	}
	if exit.Network != shared.NetworkWS || exit.Security != shared.SecurityTLS || exit.Path != "/vm" {
		t.Fatalf("transport: %+v", exit)
	}
	if name != "vm" {
		t.Fatalf("name = %q", name)
	}
}

func TestParseHTTP(t *testing.T) {
	exit, _, err := Parse("http://u:p@proxy.example.com:8080#http-proxy")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if exit.Protocol != shared.ProtocolHTTP || exit.Username != "u" || exit.Password != "p" {
		t.Fatalf("exit: %+v", exit)
	}
}

func TestParseRejectsUnsupportedProtocolsAndLinks(t *testing.T) {
	cases := []string{
		"hysteria2://pass@example.com:443#hy2",
		"not-a-link",
		"",
	}
	for _, link := range cases {
		if _, _, err := Parse(link); err == nil {
			t.Fatalf("Parse(%q) = nil error, want error", link)
		}
	}
}

func TestParseRejectsBadPort(t *testing.T) {
	if _, _, err := Parse("socks://u:p@example.com:99999#bad"); err == nil {
		t.Fatal("want error for out-of-range port")
	}
}
