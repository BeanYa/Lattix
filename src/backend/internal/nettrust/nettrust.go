// Package nettrust 集中管理"哪些对端的 X-Forwarded-* 头可信"。
// 默认仅信任回环（本机反代）；设置页 trusted_proxies 可追加 CIDR，供 TLS 终止在
// 外部反代（1panel/openresty、nginx、CDN 回源）的部署采纳协议与真实客户端 IP。
// 此前 panel.isSecure、ws.clientIP、sub.trustedForwardedProto、logging.ClientIP
// 各自复制"仅回环"判定（评审 P2 防伪造），统一收敛到这里。
package nettrust

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// MaxCIDRs 限制单次配置条数，避免误粘贴大段文本撑爆逐请求匹配。
const MaxCIDRs = 32

// Default 是进程级单例：main 启动时与设置页保存后调用 Configure。
var Default = &Trust{}

// Trust 持有已配置的可信代理网段；读取无锁（atomic 指针），Configure 可并发调用。
type Trust struct {
	cidrs atomic.Pointer[[]*net.IPNet]
}

// Validate 校验一段逗号/空白分隔的 CIDR（或裸 IP，按主机掩码对待）配置文本。
func Validate(raw string) error {
	_, err := parse(raw)
	return err
}

// Configure 校验并原子替换可信网段；空串清空配置（回到仅回环）。
// 回环地址无需显式配置，恒可信。
func (t *Trust) Configure(raw string) error {
	networks, err := parse(raw)
	if err != nil {
		return err
	}
	t.cidrs.Store(&networks)
	return nil
}

func parse(raw string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, 8)
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if !strings.Contains(item, "/") {
			// 裸 IP：视为主机掩码（v4 /32、v6 /128）。
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("trusted_proxies 含无效 IP/CIDR: %q", item)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies 含无效 CIDR: %q", item)
		}
		networks = append(networks, network)
	}
	if len(networks) > MaxCIDRs {
		return nil, fmt.Errorf("trusted_proxies 最多 %d 条", MaxCIDRs)
	}
	return networks, nil
}

func peerHost(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	return host
}

func (t *Trust) contains(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if networks := t.cidrs.Load(); networks != nil {
		for _, network := range *networks {
			if network.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// TrustedPeer 报告请求对端（TCP 直连方）是否为可信反代：回环或已配置网段。
func (t *Trust) TrustedPeer(r *http.Request) bool {
	ip := net.ParseIP(peerHost(r))
	return ip != nil && t.contains(ip)
}

// ForwardedHTTPS 报告是否应采纳 X-Forwarded-Proto 的 https 声明。
// 仅在可信对端时成立；头允许携带多值（逗号分隔），取首个判断。
func (t *Trust) ForwardedHTTPS(r *http.Request) bool {
	if !t.TrustedPeer(r) {
		return false
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// ClientIP 解析真实客户端 IP：对端可信时从右向左遍历 X-Forwarded-For，
// 跳过可信代理地址，第一个不可信地址即客户端——代理追加语义下客户端伪造的
// 左侧条目不会污染结果。对端不可信、无 XFF 或链条解析失败时返回对端地址。
func (t *Trust) ClientIP(r *http.Request) string {
	remote := peerHost(r)
	if !t.TrustedPeer(r) {
		return remote
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			return remote // 链条中出现非 IP：不可采信，回落对端地址
		}
		if t.contains(ip) {
			continue
		}
		return candidate
	}
	return remote // 整条链都是可信代理：对端即最右侧代理声明的来源
}
