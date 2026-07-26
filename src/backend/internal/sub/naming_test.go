package sub

import (
	"net/url"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestSubscriptionUsesManagedNodeName(t *testing.T) {
	node := store.Node{
		ID:            1,
		Name:          "东京主线路",
		ServerAlias:   "tokyo",
		ServerAddress: "tokyo.example.com",
		Protocol:      shared.ProtocolVLESS,
	}
	realized := shared.RealizedConfig{
		Port:        443,
		PublicKey:   "public-key",
		ShortID:     "0123456789abcdef",
		ServerName:  "example.com",
		Network:     shared.NetworkTCP,
		Fingerprint: shared.FingerprintChrome,
	}

	proxy, err := buildProxy(node, realized, "user-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Name != node.Name {
		t.Fatalf("代理名称 = %q，期望 %q", proxy.Name, node.Name)
	}
	if got, err := url.PathUnescape(shareName(node, realized)); err != nil || got != node.Name {
		t.Fatalf("分享链接名称 = %q, err=%v，期望 %q", got, err, node.Name)
	}
}

func TestSubscriptionNameFallsBackForLegacyNode(t *testing.T) {
	node := store.Node{
		ID:            1,
		ServerAlias:   "tokyo",
		ServerAddress: "tokyo.example.com",
		Protocol:      shared.ProtocolVLESS,
	}
	realized := shared.RealizedConfig{
		Port:        443,
		PublicKey:   "public-key",
		ShortID:     "0123456789abcdef",
		ServerName:  "example.com",
		Network:     shared.NetworkTCP,
		Fingerprint: shared.FingerprintChrome,
	}
	want := "tokyo-vless-443"

	proxy, err := buildProxy(node, realized, "user-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Name != want {
		t.Fatalf("代理名称 = %q，期望 %q", proxy.Name, want)
	}
	if got, err := url.PathUnescape(shareName(node, realized)); err != nil || got != want {
		t.Fatalf("分享链接名称 = %q, err=%v，期望 %q", got, err, want)
	}
}
