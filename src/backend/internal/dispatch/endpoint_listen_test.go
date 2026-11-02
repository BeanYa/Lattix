package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// endpointListenIPv6 是入口/出口跳在链路画布上显式选择的 IPv6 公网地址（§9）。
const endpointListenIPv6 = "2605:52c0:2:12ba:be24:11ff:fe85:146f"

// endpointTemplateListen 解析端点下发的虚拟配置模板，返回其 listen 值（缺失 = ""）。
func endpointTemplateListen(t *testing.T, payload shared.ApplySharedEndpointPayload) string {
	t.Helper()
	var template map[string]any
	if err := json.Unmarshal(payload.Config.Template, &template); err != nil {
		t.Fatalf("decode endpoint template: %v", err)
	}
	listen, _ := template["listen"].(string)
	return listen
}

// TestReconcileEntryEndpointListensOnSelectedIPv6Family 验证共享入口端点的监听族按
// 入口跳所选公网地址推导（§9）：入口跳显式选 IPv6 时端点须监听 "::"（双栈同听），
// 否则订阅按 IPv6 地址交付后客户端无法连入。
func TestReconcileEntryEndpointListensOnSelectedIPv6Family(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, entryID, []string{"entry.test", endpointListenIPv6}, "entry.test"); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "v6-entry", ServiceServerID: entryID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service",
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: entryID, Role: store.HopRoleExit, Address: endpointListenIPv6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if listen := endpointTemplateListen(t, payload); listen != "::" {
		t.Fatalf("入口端点 listen = %q, want \"::\"（入口跳所选 IPv6）", listen)
	}
}

// TestReconcileEntryEndpointKeepsDefaultListenForIPv4 验证未选 IPv6 时端点模板不下发
// listen 覆盖（保持 xray 默认 0.0.0.0），既有部署行为不变。
func TestReconcileEntryEndpointKeepsDefaultListenForIPv4(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "v4-entry", ServiceServerID: entryID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service",
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: entryID, Role: store.HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if listen := endpointTemplateListen(t, payload); listen != "" {
		t.Fatalf("IPv4 入口端点不应覆盖 listen，实际 %q", listen)
	}
}

// TestReconcileHy2ServiceEndpointListensOnExitIPv6Family 验证 hy2 出口共享监听的监听族
// 按拨入方实际使用的地址推导：多跳链由入口端点按出口跳所选 IPv6 直拨出口时，出口
// 监听须为 "::"。
func TestReconcileHy2ServiceEndpointListensOnExitIPv6Family(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exitID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok-exit", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, exitID, []string{"exit.test", endpointListenIPv6}, "exit.test"); err != nil {
		t.Fatal(err)
	}
	hy2Config := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	hy2Endpoint, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, hy2Config)
	if err != nil {
		t.Fatal(err)
	}
	entryEndpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-v6-exit", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2Config, EndpointID: entryEndpoint.ID, ServiceEndpointID: hy2Endpoint.ID, ServiceUUID: "svc",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "hy2"},
			{ServerID: exitID, Role: store.HopRoleExit, Address: endpointListenIPv6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	realized := json.RawMessage(`{"port":1443}`)
	revision, err := st.ChainRevisionByID(ctx, deployment.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.Snapshot.ServiceRealized = realized
	if err := st.UpdateChainRevision(ctx, revision.ID, store.RevisionStatusActive, "", revision.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{exitID: true}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, hy2Endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if listen := endpointTemplateListen(t, payload); listen != "::" {
		t.Fatalf("hy2 出口端点 listen = %q, want \"::\"（出口跳所选 IPv6）", listen)
	}
}

// TestReconcileSharedEndpointMixedFamiliesListensDualStack 验证混合族共存：同一
// server:443 共享入口既有 IPv4（跟随默认地址）链、又有 IPv6 入口链时，监听族按任一
// 成员链的 IPv6 入口跳推导为 "::" 双栈，两条链的路由/客户端同时保留；删除 IPv4 链
// 后监听仍为 "::"（IPv6 链仍引用端点），只保留 IPv6 链的路由。
func TestReconcileSharedEndpointMixedFamiliesListensDualStack(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok-entry", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddresses(ctx, entryID, []string{"entry.test", endpointListenIPv6}, "entry.test"); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(name string, address string) store.InitialChainDeploymentResult {
		t.Helper()
		deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
			Name: name, ServiceServerID: entryID, ServiceProtocol: shared.ProtocolVLESS,
			ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
			TrafficMultiplierMilli: 1000,
			Hops:                   []store.InitialChainHop{{ServerID: entryID, Role: store.HopRoleExit, Address: address}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	chainV4 := deploy("mixed-v4", "")
	chainV6 := deploy("mixed-v6", endpointListenIPv6)
	userID, err := st.InsertUser(ctx, "user", "global-user", "sub", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainV4.ChainID, chainV6.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if listen := endpointTemplateListen(t, payload); listen != "::" {
		t.Fatalf("混合族入口端点 listen = %q, want \"::\"（任一成员链入口选 IPv6）", listen)
	}
	if len(payload.Routes) != 2 || len(payload.Clients) != 2 {
		t.Fatalf("混合族端点 routes/clients = %d/%d, want 2/2", len(payload.Routes), len(payload.Clients))
	}

	// 删除 IPv4 链后：端点仍被 IPv6 链引用，监听保持 "::"，只留 IPv6 链路由。
	if err := st.DeleteChain(ctx, chainV4.ChainID); err != nil {
		t.Fatal(err)
	}
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload = latestEndpointPayload(t, st)
	if listen := endpointTemplateListen(t, payload); listen != "::" {
		t.Fatalf("仅剩 IPv6 链时 listen = %q, want \"::\"", listen)
	}
	if len(payload.Routes) != 1 || payload.Routes[0].ChainID != chainV6.ChainID {
		t.Fatalf("删除 IPv4 链后 routes = %+v, want 仅链 %d", payload.Routes, chainV6.ChainID)
	}
}
