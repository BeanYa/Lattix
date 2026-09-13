package panel

import (
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestFindPortConflict(t *testing.T) {
	occupants := []store.PortOccupant{
		{Port: 443, Layers: "tcp", Source: "node", Protocol: shared.ProtocolVLESS, ChainID: 1, RefName: "链A"},
		{Port: 8443, Layers: "tcp,udp", Source: "node", Protocol: shared.ProtocolShadowsocks, ChainID: 2, RefName: "链B"},
		{Port: 10080, Layers: "tcp", Source: "endpoint", Protocol: shared.ProtocolVLESS, ChainID: 3, RefName: "shared-endpoint #1"},
	}

	// 同层同端口不同协议 → 冲突
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 0, 0); err == nil {
		t.Error("trojan 撞 vless(tcp) 应冲突")
	}
	// ss(tcp,udp) 撞 vless(tcp) → 冲突
	if err := findPortConflict(occupants, shared.ProtocolShadowsocks, 443, 0, 0); err == nil {
		t.Error("ss 撞 vless(tcp) 应冲突")
	}
	// vless(tcp) 撞 ss(tcp,udp) → 冲突（方向对称）
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 8443, 0, 0); err == nil {
		t.Error("vless 撞 ss(tcp,udp) 应冲突")
	}
	// 不同端口 → 放行
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 9999, 0, 0); err != nil {
		t.Errorf("不同端口不应冲突: %v", err)
	}
	// 编辑链路排除自身
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 0, 1); err != nil {
		t.Errorf("排除自身链后不应冲突: %v", err)
	}
	// vless 撞共享端点 → 冲突：共享合并语义仅存在于链路入口路径（由 chains.go 调用点
	// 按 protocol != vless 门控，不进本函数）；节点创建/出口节点路径不得放行。
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 10080, 0, 0); err == nil {
		t.Error("vless 撞共享端点应冲突（共享合并门控在 chains.go 调用点，不在此函数）")
	}
	// 非 vless 撞共享端点 → 冲突
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 10080, 0, 0); err == nil {
		t.Error("trojan 撞共享端点应冲突")
	}
}

// TestFindPortConflictSpan 验证段-段/段-点/跨层语义（P4 §3.2 端口冲突治理）。
func TestFindPortConflictSpan(t *testing.T) {
	occ := []store.PortOccupant{
		{Port: 20000, PortEnd: 20031, Layers: "udp", Source: "node", Protocol: "hysteria", RefName: "hy2甲"},
		{Port: 21000, Layers: "udp", Source: "node", Protocol: "hysteria", RefName: "hy2乙"},
		{Port: 20010, Layers: "tcp", Source: "node", Protocol: "vless", RefName: "vless丙"},
	}
	// 段与段重叠（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20020, 20051, 0); err == nil {
		t.Fatal("段 [20020,20051] 与既有段 [20000,20031] 重叠应冲突")
	}
	// 点落入段（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20010, 0, 0); err == nil {
		t.Fatal("点 20010 落入既有段应冲突")
	}
	// 段覆盖点（同层）→ 冲突。
	if err := findPortConflict(occ, "hysteria", 20990, 21010, 0); err == nil {
		t.Fatal("段 [20990,21010] 覆盖单端口 21000 应冲突")
	}
	// TCP/UDP 独立空间：tcp 点与 udp 段同号不冲突（20011 落在 udp 段 [20000,20031] 号段内，
	// 但 tcp 层无占用；tcp 占用点 20010 证明同层同号仍会冲突）。
	if err := findPortConflict(occ, "vless", 20011, 0, 0); err != nil {
		t.Fatalf("tcp 20011 与 udp 段同号应共存: %v", err)
	}
	if err := findPortConflict(occ, "vless", 20010, 0, 0); err == nil {
		t.Fatal("tcp 20010 撞 tcp 占用点应冲突")
	}
	// 同层不重叠 → 通过；excludeChainID 排除自身。
	if err := findPortConflict(occ, "hysteria", 20100, 20131, 0); err != nil {
		t.Fatalf("不相交段应通过: %v", err)
	}
}

// TestAllocUDPPortHop 验证空闲段自动分配避开已保留段/点且落在 NAT 段内。
func TestAllocUDPPortHop(t *testing.T) {
	occ := []store.PortOccupant{{Port: 40000, PortEnd: 40031, Layers: "udp", Source: "node"}}
	start, end, ok := allocUDPPortHop(occ, nil, 32)
	if !ok || start != 40032 || end != 40063 {
		t.Fatalf("direct 机应分配到 40032-40063: %d,%d,%v", start, end, ok)
	}
	rs := []shared.PortRange{{PubStart: 50000, PubEnd: 50099}}
	start, end, ok = allocUDPPortHop(occ, rs, 32)
	if !ok || start != 50000 || end != 50031 {
		t.Fatalf("NAT 机应在段内分配 50000-50031: %d,%d,%v", start, end, ok)
	}
	if _, _, ok = allocUDPPortHop(occ, []shared.PortRange{{PubStart: 50000, PubEnd: 50003}}, 32); ok {
		t.Fatal("段长不足应 ok=false（调用方据此关跳跃或报错）")
	}
}
