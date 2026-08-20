package requester

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// reservedCIDRs 是拨号防护同样拒绝的保留/特殊用途网段：
// RFC 6598 运营商级 NAT（100.64/10）、TEST-NET-1/2/3（192.0.2/24、
// 198.51.100/24、203.0.113/24）与基准测试网段（198.18/15）。
var reservedCIDRs = []*net.IPNet{
	mustParseCIDR("100.64.0.0/10"),
	mustParseCIDR("192.0.2.0/24"),
	mustParseCIDR("198.51.100.0/24"),
	mustParseCIDR("203.0.113.0/24"),
	mustParseCIDR("198.18.0.0/15"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// IsBlockedIP 报告 ip 是否为本机、内网或保留/特殊用途地址（SSRF 防护判定）。
func IsBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, n := range reservedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ipResolver 抽象主机名解析，便于测试注入假解析器；*net.Resolver 满足该接口。
type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// GuardedDialContext 返回带 SSRF 防护的 DialContext：先解析目标主机名，
// 任一解析 IP 命中 IsBlockedIP 即拒绝连接，防止域名解析到内网绕过 URL 层校验。
// resolver 为 nil 时使用系统默认解析器。
func GuardedDialContext(resolver ipResolver) func(context.Context, string, string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := checkDialTarget(ctx, resolver, addr); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, addr)
	}
}

// NewExternalHTTPClient 返回带 SSRF 拨号防护的外部拉取客户端，
// 其余传输行为沿用 http.DefaultTransport。
func NewExternalHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = GuardedDialContext(nil)
	return &http.Client{Transport: transport, Timeout: timeout}
}

// checkDialTarget 校验 addr 的主机部分：IP 字面量直接判定，主机名解析后
// 逐个校验候选 IP，任一命中内网/保留段即报错。
func checkDialTarget(ctx context.Context, resolver ipResolver, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return fmt.Errorf("refusing to dial blocked address %s", host)
		}
		return nil
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, ipAddr := range ips {
		if IsBlockedIP(ipAddr.IP) {
			return fmt.Errorf("refusing to dial %s: resolves to blocked address %s", host, ipAddr.IP)
		}
	}
	return nil
}
