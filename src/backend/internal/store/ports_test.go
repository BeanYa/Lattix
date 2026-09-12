package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

func TestPortOccupants(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 服务器 1：一个 vless 节点（tcp）、一个 ss 节点（tcp,udp）、一个共享端点、一条链的 forward/portal
	nodePort := 10001
	if _, err := st.InsertNode(ctx, "n-vless", 1, shared.ProtocolVLESS, &nodePort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	ssPort := 10002
	if _, err := st.InsertNode(ctx, "n-ss", 1, shared.ProtocolShadowsocks, &ssPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// 服务器 2 的节点不应出现
	otherPort := 10003
	if _, err := st.InsertNode(ctx, "n-other", 2, shared.ProtocolVLESS, &otherPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, 1, shared.ProtocolVLESS, 10004, "profile-x", json.RawMessage(`{"protocol":"vless"}`)); err != nil {
		t.Fatal(err)
	}

	occupants, err := st.PortOccupants(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]PortOccupant{}
	for _, o := range occupants {
		byPort[o.Port] = o
	}
	vless, ok := byPort[10001]
	if !ok || vless.Source != "node" || vless.Layers != "tcp" || vless.RefName != "n-vless" {
		t.Errorf("vless 节点占用不符: %+v", vless)
	}
	ss, ok := byPort[10002]
	if !ok || ss.Layers != "tcp,udp" {
		t.Errorf("ss 节点应为 tcp,udp 双层: %+v", ss)
	}
	ep, ok := byPort[10004]
	if !ok || ep.Source != "endpoint" {
		t.Errorf("共享端点占用缺失: %+v", ep)
	}
	if _, ok := byPort[10003]; ok {
		t.Error("其他服务器的端口不应出现")
	}
}
