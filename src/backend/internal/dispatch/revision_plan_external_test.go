package dispatch

import (
	"encoding/json"
	"testing"

	"lattix/shared"
)

// TestMaterializeExternalExitNoServicePiece 验证外部直链出口：无 service piece，
// 外部规格参与下游哈希（规格变化使全部 forward piece 重发）。
func TestMaterializeExternalExitNoServicePiece(t *testing.T) {
	external := &shared.ExternalExit{Protocol: shared.ProtocolSocks, Address: "exit.example.com", Port: 1080,
		Username: "u", Password: "p"}
	topo := RevisionTopology{RevisionID: 1, ServiceID: 0,
		Hops: []RevisionHopSpec{
			{HopID: 1, ServerID: 1, Transport: "direct"},
			{HopID: 2, ServerID: 9, Transport: "direct"},
		},
		External: external}
	pieces, err := materializeRevision(topo)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]RevisionPiece{}
	for _, piece := range pieces {
		if piece.Kind == RevisionPieceService {
			t.Fatalf("外部出口链不应有 service piece: %+v", pieces)
		}
		seen[piece.Key] = piece
	}
	if _, ok := seen["forward/1"]; !ok {
		t.Fatalf("末位托管跳应有 forward piece: %+v", pieces)
	}

	changed := *external
	changed.Password = "p2"
	topo.External = &changed
	pieces2, err := materializeRevision(topo)
	if err != nil {
		t.Fatal(err)
	}
	for _, piece := range pieces2 {
		if piece.Key == "forward/1" && piece.DependsHash == seen["forward/1"].DependsHash {
			t.Fatal("外部规格变化应使 forward piece 下游依赖哈希变化（触发重发）")
		}
	}
}

// TestValidateTopologyExternalRequiresTwoHops 验证外部出口链至少两跳（外部节点不能作入口）。
func TestValidateTopologyExternalRequiresTwoHops(t *testing.T) {
	external := &shared.ExternalExit{Protocol: shared.ProtocolVLESS, Address: "e.example.com", Port: 443, UUID: "uuid"}
	topo := RevisionTopology{RevisionID: 1,
		Hops:     []RevisionHopSpec{{HopID: 1, ServerID: 1}},
		External: external}
	if err := validateTopology(topo); err == nil {
		t.Fatal("单跳外部出口链应拒绝")
	}
	topo.Hops = append(topo.Hops, RevisionHopSpec{HopID: 2, ServerID: 2})
	if err := validateTopology(topo); err != nil {
		t.Fatalf("两跳外部出口链应合法: %v", err)
	}
	bad := *external
	bad.Protocol = "hysteria2"
	topo.External = &bad
	if err := validateTopology(topo); err == nil {
		t.Fatal("不支持的外部协议应拒绝")
	}
}

var _ = json.Marshal
