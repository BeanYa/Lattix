package sub

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultClashDNSReachableOnly 验证内置 DNS 不引用国内不可达的境外回退服务器，
// 且节点域名解析走 proxy-server-nameserver 直查（节点测速超时的根因是回退查询
// 被转发到 dns.google / tls://8.8.4.4 等被墙服务器后超时）。
func TestDefaultClashDNSReachableOnly(t *testing.T) {
	body, err := yaml.Marshal(defaultClashDNS())
	if err != nil {
		t.Fatal(err)
	}
	config := string(body)
	for _, blocked := range []string{"dns.google", "8.8.4.4", "dns.cloudflare.com", "fallback"} {
		if strings.Contains(config, blocked) {
			t.Errorf("default DNS must not reference %q:\n%s", blocked, config)
		}
	}
	if !strings.Contains(config, "proxy-server-nameserver") {
		t.Errorf("default DNS must include proxy-server-nameserver:\n%s", config)
	}
	for _, reachable := range []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"} {
		if !strings.Contains(config, reachable) {
			t.Errorf("default DNS must keep reachable resolver %q:\n%s", reachable, config)
		}
	}
}

// TestPublishClashDNSReachableOnly 验证发布出的 clash 快照同样不携带境外回退 DNS。
func TestPublishClashDNSReachableOnly(t *testing.T) {
	st, userID := setupChainFixture(t, true)

	result, err := New(st, nil, nil).PublishUser(context.Background(), userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	for _, blocked := range []string{"dns.google", "8.8.4.4", "dns.cloudflare.com", "fallback-filter"} {
		if strings.Contains(clash, blocked) {
			t.Errorf("published clash must not reference %q:\n%s", blocked, clash)
		}
	}
	if !strings.Contains(clash, "proxy-server-nameserver:") {
		t.Errorf("published clash must include proxy-server-nameserver:\n%s", clash)
	}
	if !strings.Contains(clash, "https://doh.pub/dns-query") {
		t.Errorf("published clash must keep reachable resolvers:\n%s", clash)
	}
}
