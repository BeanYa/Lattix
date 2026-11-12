package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// entryIPv6 是入口跳在画布上显式选择的 IPv6 公网地址（§9 地址引用）。
const entryIPv6 = "2605:52c0:2:12ba:be24:11ff:fe85:146f"

// TestSharedEndpointSubscriptionUsesSelectedEntryAddress 验证共享入口链的订阅条目
// 取入口跳所选公网地址（§9 实时解析），而不是服务器默认地址：画布入口节点显式选中
// IPv6 后，发布修订里的 address 引用必须进入订阅（全部格式同源）。
func TestSharedEndpointSubscriptionUsesSelectedEntryAddress(t *testing.T) {
	ctx := context.Background()
	st, endpoint, chainID := sharedEndpointChainFixture(t, "entry.example.com", []string{"entry.example.com", entryIPv6}, "")

	// 模拟编辑：入口跳地址引用从「跟随默认」改为显式 IPv6 后重新发布修订。
	published, err := st.PublishedChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := published.Snapshot
	snapshot.Hops[0].Address = entryIPv6
	revision, err := st.CreateChainRevision(ctx, chainID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}

	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, nil, nil)
	item, err := srv.chainSubscriptionItem(ctx, *chain, map[int64]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if item.node.ServerAddress != entryIPv6 {
		t.Fatalf("订阅入口地址 = %q, want 所选 IPv6 %q（默认地址 %q，端点 %d）",
			item.node.ServerAddress, entryIPv6, "entry.example.com", endpoint.ID)
	}

	// 订阅文件各格式都从 node.ServerAddress 渲染：mihomo 原样、分享链接/QuanX 加 []。
	compiled, _, err := srv.compileNodes(ctx, []proxyItem{*item}, "user-uuid")
	if err != nil || len(compiled) != 1 {
		t.Fatalf("compileNodes = %d nodes, err %v", len(compiled), err)
	}
	if compiled[0].Clash.Server != entryIPv6 {
		t.Fatalf("mihomo server = %q, want %q", compiled[0].Clash.Server, entryIPv6)
	}
	if !strings.Contains(compiled[0].QuanX, "["+entryIPv6+"]:443") {
		t.Fatalf("QuanX 行未按 IPv6 字面量加 []: %s", compiled[0].QuanX)
	}
	links, err := renderLinks([]proxyItem{*item}, "user-uuid")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(links)))
	if err != nil || !strings.Contains(string(decoded), "["+entryIPv6+"]:443") {
		t.Fatalf("分享链接未按 IPv6 字面量加 []: %q, err %v", decoded, err)
	}
}

// TestSharedEndpointSubscriptionResolvesSelectedAddressWithoutDefault 验证默认公网
// 地址为空（auto 模式待学习）时，入口跳已选中的地址仍能解析：订阅不应因默认地址
// 缺失而丢弃整条链。
func TestSharedEndpointSubscriptionResolvesSelectedAddressWithoutDefault(t *testing.T) {
	ctx := context.Background()
	st, _, chainID := sharedEndpointChainFixture(t, "", []string{entryIPv6}, entryIPv6)

	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := New(st, nil, nil).chainSubscriptionItem(ctx, *chain, map[int64]bool{})
	if err != nil {
		t.Fatalf("默认地址为空但入口跳已选地址时不应报错: %v", err)
	}
	if item.node.ServerAddress != entryIPv6 {
		t.Fatalf("订阅入口地址 = %q, want %q", item.node.ServerAddress, entryIPv6)
	}
}

// TestChainSubscriptionResolvesSelectedAddressWithoutDefault 覆盖非共享入口链：
// 入口跳地址引用优先于（可能为空的）服务器默认地址，与共享入口链同一解析语义。
func TestChainSubscriptionResolvesSelectedAddressWithoutDefault(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, entryID, []string{entryIPv6}, ""); err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "tok-exit", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "exit-node", exitID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	chainID, err := st.InsertChain(ctx, "relay-chain")
	if err != nil {
		t.Fatal(err)
	}
	entryHopID, err := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 10001, "", entryIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChainServiceNode(ctx, chainID, nodeID); err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20001, Network: shared.NetworkTCP,
		PublicKey: "pub-key", ShortID: "ab", ServerName: "www.example.com"})
	published, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "relay-chain", ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 10001, Address: entryIPv6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, published.ID, false); err != nil {
		t.Fatal(err)
	}

	chain, err := st.ChainByID(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := New(st, nil, nil).chainSubscriptionItem(ctx, *chain, map[int64]bool{nodeID: true})
	if err != nil {
		t.Fatalf("默认地址为空但入口跳已选地址时不应报错: %v", err)
	}
	if item.node.ServerAddress != entryIPv6 {
		t.Fatalf("订阅入口地址 = %q, want %q", item.node.ServerAddress, entryIPv6)
	}
}

// sharedEndpointChainFixture 搭建一条共享 VLESS 入口端点链并发布首个修订：
// 入口服务器默认地址 defaultAddr、地址列表 addresses；入口跳发布地址 entryAddr
// （空 = 跟随服务器默认地址）。返回 store、入口端点与链 ID。
func sharedEndpointChainFixture(t *testing.T, defaultAddr string, addresses []string, entryAddr string) (*store.Store, *store.SharedEndpoint, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", BootstrapToken: "token-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, entryID, addresses, defaultAddr); err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "token-exit", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "JP共享Test", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Address: entryAddr},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	return st, endpoint, deployment.ChainID
}

// TestSharedEndpointSubscriptionKeepsPerChainEntryAddress 验证同一共享入口端点上的
// 多条链各自按自己的入口跳地址输出订阅：混合族共存时 IPv4/域名链保持原地址、IPv6
// 链输出 IPv6，二者端口同为端点端口（443）。
func TestSharedEndpointSubscriptionKeepsPerChainEntryAddress(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.example.com", BootstrapToken: "token-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, entryID, []string{"entry.example.com", entryIPv6}, "entry.example.com"); err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.example.com", BootstrapToken: "token-exit", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	deploy := func(name, entryAddr string) int64 {
		t.Helper()
		deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
			Name: name, ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
			ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
			TrafficMultiplierMilli: 1000,
			Hops: []store.InitialChainHop{
				{ServerID: entryID, Role: store.HopRoleEntry, Address: entryAddr},
				{ServerID: exitID, Role: store.HopRoleExit},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
			t.Fatal(err)
		}
		return deployment.ChainID
	}
	chainV4 := deploy("mixed-v4", "")
	chainV6 := deploy("mixed-v6", entryIPv6)
	userID, err := st.InsertUser(ctx, "user", "global-user-uuid", "bean-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainV4, chainV6}); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	items, warnings, _ := New(st, nil, nil).subscriptionItems(httptest.NewRequest("GET", "/sub/bean-token", nil), user, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(items) != 2 {
		t.Fatalf("订阅条目 = %d, want 2（同一共享端点的两条链）", len(items))
	}
	got := map[string]string{}
	for _, item := range items {
		got[item.node.Name] = item.node.ServerAddress
		if item.rc.Port != 443 {
			t.Fatalf("链 %q 端点端口 = %d, want 443", item.node.Name, item.rc.Port)
		}
	}
	if got["mixed-v4"] != "entry.example.com" || got["mixed-v6"] != entryIPv6 {
		t.Fatalf("共享端点订阅地址 = %+v, want v4 链默认域名 / v6 链所选 IPv6", got)
	}
}
