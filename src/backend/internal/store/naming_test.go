package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

func TestServerTagsAndManagedNamesRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir() + "/metadata.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	serverID, err := st.CreateServer(
		ctx, "日本", "jp.example.com", "token", MachineTypeDirect, "", `["主力","移动"]`,
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := st.ServerByID(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if server.Tags != `["主力","移动"]` {
		t.Fatalf("服务器 tags = %q", server.Tags)
	}

	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "日本-vless-inbound", serverID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.NodeByID(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if node.Name != "日本-vless-inbound" {
		t.Fatalf("节点名称 = %q", node.Name)
	}

	chainID, err := st.InsertChain(ctx, "日本-美国-vless-chain")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Name != "日本-美国-vless-chain" {
		t.Fatalf("链路名称 = %q", chain.Name)
	}
}
