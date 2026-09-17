package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lattix/shared"
)

// newVersionedTestManager 构造假 xray 脚本输出指定版本的 Manager
// （version 子命令输出版本行，其余子命令恒成功；hy2 版本门控测试用）。
func newVersionedTestManager(t *testing.T, version string) *Manager {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  version) echo "Xray %s (Xray, Penetrates Everything.)";;
  *) exit 0;;
esac
`, version)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewManager(bin, filepath.Join(dir, "xray.json"), "127.0.0.1:19085", &telemetryTestRunner{})
}

// TestXrayVersionAtLeast 验证三段数字版本比较（hy2 门控 ≥ 26.3.27）。
func TestXrayVersionAtLeast(t *testing.T) {
	cases := map[string]bool{
		"26.3.27": true, "26.3.28": true, "26.10.1": true, "27.0.0": true,
		"26.3.26": false, "26.2.99": false, "25.12.8": false,
		"": false, "unknown": false, "26.3": false,
	}
	for v, want := range cases {
		if got := xrayVersionAtLeast(v, shared.XrayMinVersionHy2); got != want {
			t.Errorf("xrayVersionAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestApplyNodeHysteriaVersionGate 验证低版本 xray 拒绝 hy2 节点（指向性错误文案）。
func TestApplyNodeHysteriaVersionGate(t *testing.T) {
	m := newVersionedTestManager(t, "25.8.3")
	vc := shared.VirtualConfig{Protocol: shared.ProtocolHysteria2, Template: json.RawMessage(`{}`)}
	if _, err := m.ApplyNode(1, vc, nil, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "节点 xray 版本过低") {
		t.Fatalf("低版本应拒绝并提示升级: %v", err)
	}
}

// TestApplyNodeHysteriaDNATLifecycle 验证节点级 hy2 的 DNAT 与 inbound 同生共死：
// apply 带 port_hop → 建立 lattix:node_<id> 注释的跳跃段 DNAT 且 realized 回显段；
// remove → 按注释清除（全局约束：清理路径覆盖删除）。
func TestApplyNodeHysteriaDNATLifecycle(t *testing.T) {
	calls := stubIPTables(t, "")
	m := newVersionedTestManager(t, "26.10.1")
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolHysteria2, Security: shared.SecurityTLS,
		TLSDomain: "www.example.com", PortHop: "20000-20031",
		Template: json.RawMessage(`{"tag":"{{TAG}}","protocol":"hysteria","port":"{{PORT}}",
			"settings":{"version":2,"clients":"{{CLIENTS}}"},
			"streamSettings":{"network":"hysteria","security":"tls",
			"tlsSettings":{"serverName":"www.example.com","alpn":["h3"]},
			"hysteriaSettings":{"version":2}}}`),
	}
	realized, err := m.ApplyNode(1, vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.PortHop != "20000-20031" {
		t.Fatalf("realized 应回显跳跃段: %+v", realized)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "--dport 20000:20031") ||
		!strings.Contains(joined, fmt.Sprintf("--to-ports %d", realized.Port)) ||
		!strings.Contains(joined, "lattix:node_1") {
		t.Fatalf("apply 应建立跳跃段 DNAT:\n%s", joined)
	}
	// 删除节点：DNAT 规则随 inbound 一并清除。
	*calls = nil
	listIPTablesRules = func(table, chain string) string {
		return fmt.Sprintf("-A PREROUTING -p udp --dport 20000:20031 -m comment --comment lattix:node_1 -j REDIRECT --to-ports %d\n", realized.Port)
	}
	if err := m.RemoveNode(1); err != nil {
		t.Fatal(err)
	}
	if joined = strings.Join(*calls, "\n"); !strings.Contains(joined, "-D PREROUTING") {
		t.Fatalf("remove 应清除 DNAT 规则:\n%s", joined)
	}
}

// TestApplyNodeHysteriaDNATFailure 验证 DNAT 建立失败时整体报错（不上报 realized）——
// 不变式：客户端声明 udpHop ⟺ DNAT 已就绪。
func TestApplyNodeHysteriaDNATFailure(t *testing.T) {
	origRun, origList := runIPTables, listIPTablesRules
	t.Cleanup(func() { runIPTables, listIPTablesRules = origRun, origList })
	runIPTables = func(bin string, args ...string) error { return errTestIPTables }
	listIPTablesRules = func(table, chain string) string { return "" }
	m := newVersionedTestManager(t, "26.10.1")
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolHysteria2, PortHop: "20000-20031",
		Template: json.RawMessage(`{"tag":"{{TAG}}","protocol":"hysteria","port":"{{PORT}}",
			"settings":{"version":2,"clients":"{{CLIENTS}}"}}`),
	}
	if _, err := m.ApplyNode(1, vc, nil, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "端口跳跃需要 iptables DNAT 权限") {
		t.Fatalf("DNAT 失败应报指向性错误: %v", err)
	}
}
