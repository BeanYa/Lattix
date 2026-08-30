package requester

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "224.0.0.1", "ff02::1",
		"100.64.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "198.18.0.1",
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil || !IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if ip := net.ParseIP(s); ip == nil || IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%s) = true, want false", s)
		}
	}
}

type fakeResolver map[string][]net.IPAddr

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return ips, nil
}

func TestCheckDialTarget(t *testing.T) {
	resolver := fakeResolver{
		"internal.example.com":  {{IP: net.ParseIP("10.1.2.3")}},
		"loopback.example.com":  {{IP: net.ParseIP("127.0.0.1")}},
		"linklocal.example.com": {{IP: net.ParseIP("169.254.1.1")}},
		"mixed.example.com":     {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("192.168.0.1")}},
		"public.example.com":    {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}},
	}
	blocked := []string{
		"internal.example.com:443",
		"loopback.example.com:443",
		"linklocal.example.com:443",
		"mixed.example.com:443", // 任一解析 IP 命中即拒
		"127.0.0.1:443",
		"[::1]:443",
		"10.0.0.1:443",
		"169.254.169.254:443",
		"100.64.0.1:443",
	}
	for _, addr := range blocked {
		if err := checkDialTarget(context.Background(), resolver, addr); err == nil {
			t.Errorf("checkDialTarget(%q) = nil, want blocked", addr)
		}
	}
	allowed := []string{"public.example.com:443", "8.8.8.8:443"}
	for _, addr := range allowed {
		if err := checkDialTarget(context.Background(), resolver, addr); err != nil {
			t.Errorf("checkDialTarget(%q) = %v, want nil", addr, err)
		}
	}
}

// 防护 DialContext 命中内网地址时必须先于任何真实拨号失败。
func TestGuardedDialContextBlocksBeforeDialing(t *testing.T) {
	dial := GuardedDialContext(fakeResolver{
		"internal.example.com": {{IP: net.ParseIP("10.1.2.3")}},
	})
	for _, addr := range []string{"internal.example.com:443", "127.0.0.1:443"} {
		_, err := dial(context.Background(), "tcp", addr)
		if err == nil || !strings.Contains(err.Error(), "refusing to dial") {
			t.Errorf("dial(%q) error = %v, want guard rejection", addr, err)
		}
	}
}

// e2e 测试钩子：LATX_ALLOW_PRIVATE_OUTBOUND=1 放行内网/保留地址拨号；
// 其他取值（含未设置，由 TestCheckDialTarget 覆盖）仍拒绝。
func TestCheckDialTargetAllowPrivateOutboundHook(t *testing.T) {
	resolver := fakeResolver{
		"internal.example.com": {{IP: net.ParseIP("10.1.2.3")}},
	}
	t.Setenv(allowPrivateOutboundEnv, "1")
	for _, addr := range []string{"internal.example.com:443", "127.0.0.1:443", "[::1]:443"} {
		if err := checkDialTarget(context.Background(), resolver, addr); err != nil {
			t.Errorf("hook enabled: checkDialTarget(%q) = %v, want nil", addr, err)
		}
	}
}

func TestNewExternalHTTPClient(t *testing.T) {
	client := NewExternalHTTPClient(5 * time.Second)
	if client == nil || client.Timeout != 5*time.Second {
		t.Fatalf("client = %+v, want timeout 5s", client)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatalf("transport = %T, want guarded *http.Transport", client.Transport)
	}
}
