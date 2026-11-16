package xray

import (
	"encoding/json"
	"testing"

	"lattix/shared"
)

func decodeOutbound(t *testing.T, raw map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode outbound: %v", err)
	}
	return decoded
}

func TestRenderExternalOutboundSocks(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 7, TargetAddress: "127.0.0.1", TargetPort: 32001,
		External: &shared.ExternalExit{Protocol: shared.ProtocolSocks, Username: "u", Password: "p"},
	}
	rendered, err := renderExternalOutbound(route, "route_7")
	if err != nil {
		t.Fatalf("renderExternalOutbound: %v", err)
	}
	out := decodeOutbound(t, rendered)
	if out["protocol"] != "socks" || out["tag"] != "route_7" {
		t.Fatalf("unexpected outbound: %v", out)
	}
	servers := out["settings"].(map[string]any)["servers"].([]any)
	server := servers[0].(map[string]any)
	if server["address"] != "127.0.0.1" || server["port"] != float64(32001) {
		t.Fatalf("unexpected server: %v", server)
	}
}

func TestRenderExternalOutboundVLESSReality(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 9, TargetAddress: "127.0.0.1", TargetPort: 32002,
		External: &shared.ExternalExit{
			Protocol: shared.ProtocolVLESS, UUID: "uuid-9", Flow: "xtls-rprx-vision",
			Network: shared.NetworkTCP, Security: shared.SecurityReality,
			SNI: "www.cloudflare.com", PublicKey: "pbk", ShortID: "sid", Fingerprint: "chrome",
		},
	}
	rendered, err := renderExternalOutbound(route, "route_9")
	if err != nil {
		t.Fatalf("renderExternalOutbound: %v", err)
	}
	out := decodeOutbound(t, rendered)
	if out["protocol"] != "vless" {
		t.Fatalf("protocol = %v", out["protocol"])
	}
	stream := out["streamSettings"].(map[string]any)
	if stream["network"] != "tcp" || stream["security"] != "reality" {
		t.Fatalf("stream: %v", stream)
	}
	reality := stream["realitySettings"].(map[string]any)
	if reality["publicKey"] != "pbk" || reality["serverName"] != "www.cloudflare.com" {
		t.Fatalf("reality: %v", reality)
	}
	vnext := out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	user := vnext["users"].([]any)[0].(map[string]any)
	if user["id"] != "uuid-9" || user["flow"] != "xtls-rprx-vision" {
		t.Fatalf("user: %v", user)
	}
}

func TestRenderExternalOutboundWebSocketTLS(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 11, TargetAddress: "127.0.0.1", TargetPort: 32003,
		External: &shared.ExternalExit{
			Protocol: shared.ProtocolVMess, UUID: "uuid-11", Network: shared.NetworkWS,
			Security: shared.SecurityTLS, SNI: "cdn.example.com", Path: "/ws", Host: "cdn.example.com",
		},
	}
	rendered, err := renderExternalOutbound(route, "route_11")
	if err != nil {
		t.Fatalf("renderExternalOutbound: %v", err)
	}
	stream := decodeOutbound(t, rendered)["streamSettings"].(map[string]any)
	if stream["security"] != "tls" {
		t.Fatalf("stream: %v", stream)
	}
	ws := stream["wsSettings"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Fatalf("ws: %v", ws)
	}
}

func TestRenderExternalOutboundRejectsUnknownProtocol(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 1, TargetAddress: "127.0.0.1", TargetPort: 1,
		External: &shared.ExternalExit{Protocol: "hysteria"},
	}
	if _, err := renderExternalOutbound(route, "x"); err == nil {
		t.Fatal("want error for unsupported external protocol")
	}
}
