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
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 0); err == nil {
		t.Error("trojan 撞 vless(tcp) 应冲突")
	}
	// ss(tcp,udp) 撞 vless(tcp) → 冲突
	if err := findPortConflict(occupants, shared.ProtocolShadowsocks, 443, 0); err == nil {
		t.Error("ss 撞 vless(tcp) 应冲突")
	}
	// vless(tcp) 撞 ss(tcp,udp) → 冲突（方向对称）
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 8443, 0); err == nil {
		t.Error("vless 撞 ss(tcp,udp) 应冲突")
	}
	// 不同端口 → 放行
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 9999, 0); err != nil {
		t.Errorf("不同端口不应冲突: %v", err)
	}
	// 编辑链路排除自身
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 1); err != nil {
		t.Errorf("排除自身链后不应冲突: %v", err)
	}
	// vless 加入共享端点 → 放行（共享语义）
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 10080, 0); err != nil {
		t.Errorf("vless 共享端点合并不应冲突: %v", err)
	}
	// 非 vless 撞共享端点 → 冲突
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 10080, 0); err == nil {
		t.Error("trojan 撞共享端点应冲突")
	}
}
