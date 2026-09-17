package xray

import (
	"errors"
	"strings"
	"testing"
)

var errTestIPTables = errors.New("iptables: Permission denied")

// stubIPTables 桩住 DNAT 测试缝：runIPTables 记录调用序列，listIPTablesRules 返回
// 预设的 -S 规则文本（remove 路径的列举来源）。
func stubIPTables(t *testing.T, listed string) *[]string {
	t.Helper()
	calls := &[]string{}
	origRun, origList := runIPTables, listIPTablesRules
	runIPTables = func(bin string, args ...string) error {
		*calls = append(*calls, bin+" "+strings.Join(args, " "))
		return nil
	}
	listIPTablesRules = func(table, chain string) string { return listed }
	t.Cleanup(func() { runIPTables, listIPTablesRules = origRun, origList })
	return calls
}

// TestEnsureUdpHopDNAT 验证 DNAT 规则建立/清理的 iptables 调用序列（命令经测试缝捕获）。
func TestEnsureUdpHopDNAT(t *testing.T) {
	calls := stubIPTables(t, "")
	m := &Manager{}
	if err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "--dport 20000:20031") || !strings.Contains(joined, "--to-ports 14439") {
		t.Fatalf("DNAT 规则不符:\n%s", joined)
	}
	if !strings.Contains(joined, "lattix:node_1") {
		t.Fatalf("规则缺 tag 注释（清理依据）:\n%s", joined)
	}
	// 幂等：重复建立不产生重复规则（先按 tag 清再加）。
	*calls = nil
	if err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439); err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 {
		t.Fatal("幂等重放应走清+加序列")
	}
	// 清理：按 tag 注释删除全部规则（-S 列举到含注释的规则 → 逐条 -D）。
	*calls = nil
	listIPTablesRules = func(table, chain string) string {
		return "-A PREROUTING -p udp --dport 20000:20031 -m comment --comment lattix:node_1 -j REDIRECT --to-ports 14439\n" +
			"-A PREROUTING -p udp --dport 30000:30031 -m comment --comment lattix:node_2 -j REDIRECT --to-ports 21000\n"
	}
	if err := m.removeUdpHopDNAT("node_1"); err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(*calls, "\n")
	if !strings.Contains(joined, "-D PREROUTING") || !strings.Contains(joined, "lattix:node_1") {
		t.Fatalf("清理应删除本 tag 规则:\n%s", joined)
	}
	if strings.Contains(joined, "lattix:node_2") {
		t.Fatalf("不得删除其他 tag 的规则:\n%s", joined)
	}
	// portHop 空 = 清理旧规则（评审 Important #3：port_hop 改 off 不得留陈旧
	// DNAT 把跳跃段 REDIRECT 到旧监听）。
	*calls = nil
	if err := m.ensureUdpHopDNAT("node_1", "", 14439); err != nil {
		t.Fatal(err)
	}
	if joined = strings.Join(*calls, "\n"); !strings.Contains(joined, "-D PREROUTING") {
		t.Fatalf("空跳跃段应清理旧规则:\n%s", joined)
	}
	// 无旧规则时清理幂等（无任何 iptables 调用）。
	*calls = nil
	listIPTablesRules = func(table, chain string) string { return "" }
	if err := m.ensureUdpHopDNAT("node_1", "", 14439); err != nil || len(*calls) != 0 {
		t.Fatal("空跳跃段且无旧规则应无调用")
	}
}

// TestRemoveUdpHopDNATExactTag 回归：comment 精确匹配——清理 lattix:node_1 不得
// 子串命中 lattix:node_11/node_100（评审 Critical #1：误删他节点规则且无法自愈）。
func TestRemoveUdpHopDNATExactTag(t *testing.T) {
	calls := stubIPTables(t,
		"-A PREROUTING -p udp --dport 20000:20031 -m comment --comment lattix:node_1 -j REDIRECT --to-ports 14439\n"+
			"-A PREROUTING -p udp --dport 21000:21031 -m comment --comment lattix:node_11 -j REDIRECT --to-ports 14440\n"+
			"-A PREROUTING -p udp --dport 22000:22031 -m comment --comment lattix:node_100 -j REDIRECT --to-ports 14441\n")
	m := &Manager{}
	if err := m.removeUdpHopDNAT("node_1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "-D PREROUTING") || !strings.Contains(joined, "--comment lattix:node_1 ") {
		t.Fatalf("应删除 node_1 的规则:\n%s", joined)
	}
	if strings.Contains(joined, "node_11") || strings.Contains(joined, "node_100") {
		t.Fatalf("子串匹配误删他节点规则:\n%s", joined)
	}
}

// TestEnsureUdpHopDNATPermissionError 验证无权限时的指向性错误（spec §5/全局约束）。
func TestEnsureUdpHopDNATPermissionError(t *testing.T) {
	origRun, origList := runIPTables, listIPTablesRules
	t.Cleanup(func() { runIPTables, listIPTablesRules = origRun, origList })
	runIPTables = func(bin string, args ...string) error { return errTestIPTables }
	listIPTablesRules = func(table, chain string) string { return "" }
	m := &Manager{}
	err := m.ensureUdpHopDNAT("node_1", "20000-20031", 14439)
	if err == nil || !strings.Contains(err.Error(), "端口跳跃需要 iptables DNAT 权限") {
		t.Fatalf("应报指向性错误: %v", err)
	}
}
