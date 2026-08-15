// Package nettrust 集中管理"哪些对端的 X-Forwarded-* 头可信"。
// 默认信任回环与内网/容器网段（RFC1918、CGNAT、ULA、链路本地）：1panel/OpenResty、
// docker compose、k8s、Tailscale 等部署里反代与面板之间的跳数全部落在这些网段，
// 无需配置即可采纳 X-Forwarded-Proto/For（安装命令协议、订阅链接、日志 IP 与
// agent 地址学习随之正确）。设置页 trusted_proxies 用于追加公网网段（如 CDN 回源）。
// 安全边界：穿过反代的客户端无法伪造——XFF 从右向左解析、代理亲见的真实地址
// 恒在伪造条目右侧；只有能与面板同网直连的设备才可能伪造，而内网即此类面板的
// 信任边界。公网对端直连时一律不采信。
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

// builtinNetworks 是恒定可信的内建网段（回环之外）：
//   - RFC1918：局域网、docker/k8s 桥接与 overlay、1panel 容器互访；
//   - IPv4 链路本地：云 LB 健康检查、cilium 等 CNI；
//   - CGNAT 100.64/10：Tailscale/ZeroTier 组网反代；
//   - IPv6 ULA 与链路本地：容器 v6 网络对应物。
//
// 面板容器经宿主机 openresty 反代时对端是桥接网关或代理容器 IP（172.x/10.x），
// 全部落在此处，因此 TLS 终止在反代的部署开箱即被识别为 https。
var builtinNetworks = mustCIDRs(
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "100.64.0.0/10",
	"fc00::/7", "fe80::/10",
)

func mustCIDRs(items ...string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(items))
	for _, item := range items {
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			panic("nettrust: invalid builtin CIDR " + item)
		}
		networks = append(networks, network)
	}
	return networks
}

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

// Configure 校验并原子替换可信网段；空串清空配置（回到内建默认）。
// 回环与内网/容器网段为内建默认，无需显式配置；此处配置用于追加公网网段（CDN 回源等）。
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
	for _, network := range builtinNetworks {
		if network.Contains(ip) {
			return true
		}
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

// TrustedPeer 报告请求对端（TCP 直连方）是否为可信反代：回环、内建内网/
// 容器网段，或已配置网段。
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
