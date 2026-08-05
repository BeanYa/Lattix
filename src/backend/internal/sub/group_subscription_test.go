package sub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestSubscriptionItemsUseGroups(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, _ := newTestEndpointChain(t, st, "grp-sub-a")
	chainB, _ := newTestEndpointChain(t, st, "grp-sub-b")
	subID, _ := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	// 外部订阅 2 个节点（原子性验证）；extsub.Node JSON 字段：name/type/server/port/extra
	cfgA, _ := json.Marshal(extsub.Node{Name: "ext-a", Type: "vless", Server: "ext-a.example.com", Port: 443, Extra: map[string]any{"id": "x-uuid-a"}})
	cfgB, _ := json.Marshal(extsub.Node{Name: "ext-b", Type: "vless", Server: "ext-b.example.com", Port: 443, Extra: map[string]any{"id": "x-uuid-b"}})
	_, err = st.ReplaceExternalChains(ctx, subID, []store.ExternalChain{
		{SubscriptionID: subID, Name: "ext-a", Protocol: "vless", Server: "ext-a.example.com", Port: 443, Config: cfgA, ConfigSHA256: "sha-a"},
		{SubscriptionID: subID, Name: "ext-b", Protocol: "vless", Server: "ext-b.example.com", Port: 443, Config: cfgB, ConfigSHA256: "sha-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA, chainB},
		[]store.LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: store.ExtSubModeStack}})
	if err != nil {
		t.Fatal(err)
	}
	member, _ := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "tok-m", nil)
	outside, _ := st.InsertUser(ctx, "outside", "00000000-0000-0000-0000-0000000000bb", "tok-o", nil)
	if _, err := st.CreateUserGroup(ctx, "青铜会员", []int64{member}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	// outside 直接分配 chainA（对照：非分组用户只含直接分配）
	if _, _, err := st.SetUserChains(ctx, outside, []int64{chainA}); err != nil {
		t.Fatal(err)
	}
	// 分组用户也预置直接分配（验证遮蔽）
	if _, _, err := st.SetUserChains(ctx, member, []int64{chainA}); err != nil {
		t.Fatal(err)
	}

	nodes, _ := st.ListNodes(ctx)
	memberItems, memberWarnings := New(st, nil, nil).subscriptionItems(
		httptest.NewRequest("GET", "/sub/test", nil), &store.User{ID: member, UUID: "00000000-0000-0000-0000-0000000000aa"}, nodes)
	if len(memberWarnings) != 0 {
		t.Fatalf("member warnings = %v", memberWarnings)
	}
	// 分组用户：2 条链路 + 2 个外部节点 = 4 条
	if len(memberItems) != 4 {
		t.Fatalf("member items = %d, want 4: %+v", len(memberItems), memberItems)
	}
	var chainNames, extCount int
	for _, item := range memberItems {
		if item.external != nil {
			extCount++
		} else {
			chainNames++
		}
	}
	if chainNames != 2 || extCount != 2 {
		t.Fatalf("member chains=%d ext=%d, want 2/2", chainNames, extCount)
	}
	outsideItems, _ := New(st, nil, nil).subscriptionItems(
		httptest.NewRequest("GET", "/sub/test", nil), &store.User{ID: outside, UUID: "00000000-0000-0000-0000-0000000000bb"}, nodes)
	if len(outsideItems) != 1 {
		t.Fatalf("outside items = %d, want 1: %+v", len(outsideItems), outsideItems)
	}

	// 直连节点遮蔽：给 member 直接分配一个普通 vless 节点（非链出口节点，避免被
	// exitIDs 过滤使断言空洞），分组用户订阅仍为 4 条（直连节点不出现）；
	// 非分组用户同一节点照常可见。
	directServer, _ := st.CreateServer(ctx, "direct-srv", "direct.example.com", "tok-d", store.MachineTypeDirect, "", "", "US", "")
	directConfig, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	directNode, _ := st.InsertNode(ctx, "direct-node", directServer, shared.ProtocolVLESS, nil, directConfig)
	if err := st.SetNodeActive(ctx, directNode, []byte(`{"port":20003,"network":"tcp"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, member, []int64{directNode}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, outside, []int64{directNode}); err != nil {
		t.Fatal(err)
	}
	memberItems, _, err = New(st, nil, nil).itemsForUser(ctx, &store.User{ID: member, UUID: "00000000-0000-0000-0000-0000000000aa"})
	if err != nil {
		t.Fatal(err)
	}
	if len(memberItems) != 4 {
		t.Fatalf("分组用户直连节点应被遮蔽: items = %d, want 4: %+v", len(memberItems), memberItems)
	}
	for _, item := range memberItems {
		if item.node.ID == directNode {
			t.Fatalf("分组用户订阅不应含直连节点: %+v", item.node)
		}
	}
	outsideItems, _, err = New(st, nil, nil).itemsForUser(ctx, &store.User{ID: outside, UUID: "00000000-0000-0000-0000-0000000000bb"})
	if err != nil {
		t.Fatal(err)
	}
	var sawDirect bool
	for _, item := range outsideItems {
		if item.node.ID == directNode {
			sawDirect = true
		}
	}
	if !sawDirect {
		t.Fatalf("非分组用户应能看到直连节点: %+v", outsideItems)
	}

	// 原子性：从链路分组移除外部订阅 → 分组用户订阅外部节点消失
	if err := st.UpdateLinkGroup(ctx, lgID, "普通组", []int64{chainA, chainB}, nil); err != nil {
		t.Fatal(err)
	}
	memberItems, _ = New(st, nil, nil).subscriptionItems(
		httptest.NewRequest("GET", "/sub/test", nil), &store.User{ID: member, UUID: "00000000-0000-0000-0000-0000000000aa"}, nodes)
	if len(memberItems) != 2 {
		t.Fatalf("after remove ext sub, items = %d, want 2", len(memberItems))
	}
	for _, item := range memberItems {
		if item.external != nil {
			t.Fatalf("外部订阅应整体移除: %+v", item.external)
		}
	}
}

// newTestEndpointChain 的 sub 包副本（sub 测试不能引用 store 包内未导出测试辅助）：
func newTestEndpointChain(t *testing.T, st *store.Store, name string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	entryID, _ := st.CreateServer(ctx, name+"-entry", "entry.example.com", "tok-"+name+"-e", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, name+"-exit", "exit.example.com", "tok-"+name+"-x", store.MachineTypeDirect, "", "", "JP", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 30001, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, []byte(`{"port":20001}`)); err != nil {
		t.Fatal(err)
	}
	nodeID, _ := st.InsertNode(ctx, name+"-node", exitID, shared.ProtocolVLESS, nil, config)
	chainID, _ := st.InsertChain(ctx, name)
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 10001, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20001, Network: shared.NetworkTCP})
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: name, ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
		Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 10001},
			{HopID: exitHopID, ServerID: exitID, Role: store.HopRoleExit, ForwardPort: 20001},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	return chainID, endpoint.ID
}
