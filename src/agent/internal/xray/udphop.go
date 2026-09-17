package xray

import (
	"fmt"
	"os/exec"
	"strings"

	"lattix/shared"
)

// hy2 端口跳跃的 DNAT 治理（spec §3.2 DNAT 路径）：xray hy2 服务端只监听单端口，
// udpHop 仅客户端实现——服务端用 iptables nat 把跳跃段流量 REDIRECT 到 hy2 监听端口。
// 规则以 --comment "lattix:<tag>" 标识，生命周期跟随对应 inbound（建立幂等=先清后加；
// 清理按注释匹配删除）。仅有 IPv4 治理（v1；IPv6 跳跃段后续版本补 ip6tables）。

// runIPTables 测试缝：单测捕获调用序列，不执行真实 iptables。
var runIPTables = runIPTablesImpl

func runIPTablesImpl(bin string, args ...string) error {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// listIPTablesRules 列出某表某链的规则（-S 输出）。列举失败（iptables 缺失/无权限）
// 返回空串：规则只可能由 ensure 成功建立（彼时 iptables 必然可用），列举不出即视为无规则，
// 清理幂等成功。包级变量 = 测试缝。
var listIPTablesRules = func(table, chain string) string {
	out, err := exec.Command("iptables", "-t", table, "-S", chain).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// ensureUdpHopDNAT 把 tag 对应 inbound 的跳跃段 DNAT 收敛到期望状态：
// portHop 非空 = 先清后加（幂等）；portHop 空 = 清理旧规则（port_hop 改 off 的
// 原地变更不得留陈旧规则把跳跃段 REDIRECT 到旧监听）。
func (m *Manager) ensureUdpHopDNAT(tag string, portHop string, listenPort int) error {
	if portHop == "" {
		return m.removeUdpHopDNAT(tag)
	}
	start, end, err := shared.ParsePortHop(portHop)
	if err != nil {
		return err
	}
	if err := m.removeUdpHopDNAT(tag); err != nil {
		return err
	}
	comment := "lattix:" + tag
	dport := fmt.Sprintf("%d:%d", start, end)
	toPorts := fmt.Sprintf("%d", listenPort)
	if err := runIPTables("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp",
		"--dport", dport, "-m", "comment", "--comment", comment,
		"-j", "REDIRECT", "--to-ports", toPorts); err != nil {
		return fmt.Errorf("端口跳跃需要 iptables DNAT 权限（或以 root 运行 agent / 关闭端口跳跃）: %w", err)
	}
	return nil
}

// removeUdpHopDNAT 按 tag 注释清理 DNAT 规则（逐条列出后删除；无规则 = 幂等成功）。
// comment 精确匹配（ruleComment）：子串匹配会把 lattix:node_11/node_100 误判为
// lattix:node_1 的规则连带删除，且他节点无自愈路径（评审 Critical #1）。
func (m *Manager) removeUdpHopDNAT(tag string) error {
	comment := "lattix:" + tag
	// -S 列出规则，逐条把 -A 替换为 -D 删除。
	out := listIPTablesRules("nat", "PREROUTING")
	for _, rule := range strings.Split(out, "\n") {
		if ruleComment(rule) != comment {
			continue
		}
		del := strings.Replace(rule, "-A PREROUTING", "-D PREROUTING", 1)
		args := strings.Fields(del)
		if err := runIPTables("iptables", append([]string{"-t", "nat"}, args...)...); err != nil {
			return fmt.Errorf("清理端口跳跃 DNAT 规则失败: %w", err)
		}
	}
	return nil
}

// ruleComment 提取 -S 规则行中 --comment 后的值（容忍引号包裹）；无注释返回空串。
func ruleComment(rule string) string {
	fields := strings.Fields(rule)
	for i, f := range fields {
		if f == "--comment" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], `"`)
		}
	}
	return ""
}
