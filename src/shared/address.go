package shared

import (
	"net"
	"strconv"
	"strings"
)

// 公网地址族（§9）：地址在面板侧统一按等价字符串存储，族信息在用处处现场解析派生，
// 不落库。域名无法从字符串判定 A/AAAA 记录，归为 domain，由 xray/客户端 DNS 解析。
const (
	AddressFamilyIPv4   = "ipv4"
	AddressFamilyIPv6   = "ipv6"
	AddressFamilyDomain = "domain"
)

// AddressFamily 判定地址字面量的族：IPv4/IPv6 字面量按解析结果，其余视为域名。
func AddressFamily(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return AddressFamilyDomain
	}
	if ip.To4() != nil {
		return AddressFamilyIPv4
	}
	return AddressFamilyIPv6
}

// HostPort 拼接 host:port；IPv6 字面量自动加 []（net.JoinHostPort），域名/IPv4 原样。
func HostPort(addr string, port int) string {
	return net.JoinHostPort(addr, strconv.Itoa(port))
}

// DedupeAddresses 去重保序，并剔除空白项。
func DedupeAddresses(addrs []string) []string {
	seen := make(map[string]bool, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// NormalizeIP 归一化地址字面量以便等值比较：去除空白与 IPv6 方括号，
// 解映射 IPv4-in-IPv6（::ffff:a.b.c.d → a.b.c.d），返回 net.IP 的规范字符串
// （IPv4 点分十进制、IPv6 小写压缩）。非 IP 字面量（域名等）原样返回（已去空白）。
// xray 上报的源地址 IPv6 带方括号（net.Address.String 形式），与面板落库的
// 无括号地址直接比较会漏判，归一化后等价。
func NormalizeIP(addr string) string {
	raw := strings.TrimSpace(addr)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}
