package xray

import (
	"testing"

	"lattix/shared"
)

func TestSharedEndpointRouteOutboundUsesTunnelCredential(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 7, TargetAddress: "127.0.0.1", TargetPort: 21000,
		TunnelUUID: "route-uuid",
		Target: shared.RealizedConfig{Network: shared.NetworkTCP, PublicKey: "public",
			ShortID: "short", ServerName: "example.com", Flow: "xtls-rprx-vision"},
	}
	outbound := renderSharedEndpointOutbound(route, "route")
	vnext, _ := nested(outbound, "settings")["vnext"].([]map[string]any)
	if len(vnext) != 1 || vnext[0]["address"] != "127.0.0.1" || vnext[0]["port"] != 21000 {
		t.Fatalf("vnext = %+v", vnext)
	}
	users, _ := vnext[0]["users"].([]map[string]any)
	if len(users) != 1 || users[0]["id"] != "route-uuid" || users[0]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("users = %+v", users)
	}
	if reality := nested(outbound, "streamSettings", "realitySettings"); reality["publicKey"] != "public" {
		t.Fatalf("reality settings = %+v", reality)
	}
}

func TestSharedEntryForwardIsLoopbackOnly(t *testing.T) {
	inbound := renderForwardInbound(&shared.ForwardSpec{LocalOnly: true, TargetAddress: "exit", TargetPort: 443},
		"chain", 12000)
	if inbound["listen"] != "127.0.0.1" {
		t.Fatalf("listen = %v", inbound["listen"])
	}
}
