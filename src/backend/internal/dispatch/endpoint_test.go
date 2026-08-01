package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func createDirectSharedChain(t *testing.T, st *store.Store, serverID, endpointID int64, name string) store.InitialChainDeploymentResult {
	t.Helper()
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	deployment, err := st.CreateInitialChainDeployment(context.Background(), store.InitialChainDeployment{
		Name: name, ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpointID, ServiceUUID: "service-" + name,
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: serverID, Role: store.HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(context.Background(), deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func latestEndpointPayload(t *testing.T, st *store.Store) shared.ApplySharedEndpointPayload {
	t.Helper()
	commands, err := st.CommandsByType(context.Background(), shared.TypeApplySharedEndpoint)
	if err != nil || len(commands) == 0 {
		t.Fatalf("shared endpoint commands: len=%d err=%v", len(commands), err)
	}
	var payload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(commands[len(commands)-1].Data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestReconcileSharedEndpointGroupsUsersByChain(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	chainA := createDirectSharedChain(t, st, serverID, endpoint.ID, "a")
	chainB := createDirectSharedChain(t, st, serverID, endpoint.ID, "b")
	userA, _ := st.InsertUser(ctx, "user-a", "global-a", "sub-a", nil)
	userB, _ := st.InsertUser(ctx, "user-b", "global-b", "sub-b", nil)
	if _, _, err := st.SetUserChains(ctx, userA, []int64{chainA.ChainID, chainB.ChainID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userB, []int64{chainA.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if len(payload.Clients) != 3 || len(payload.Routes) != 2 {
		t.Fatalf("payload clients/routes = %d/%d", len(payload.Clients), len(payload.Routes))
	}
	usersByChain := map[int64]int{}
	for _, route := range payload.Routes {
		usersByChain[route.ChainID] = len(route.Users)
	}
	if usersByChain[chainA.ChainID] != 2 || usersByChain[chainB.ChainID] != 1 {
		t.Fatalf("route user counts = %+v", usersByChain)
	}

	if err := st.SetUserDisabled(ctx, userB, true); err != nil {
		t.Fatal(err)
	}
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload = latestEndpointPayload(t, st)
	if len(payload.Clients) != 2 {
		t.Fatalf("disabled user still present: %+v", payload.Clients)
	}
}

func TestPublishReconcilesPreviousAndNewSharedEndpoints(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	configB := json.RawMessage(`{"protocol":"vless","port":8443,"template":{}}`)
	endpointA, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", configA)
	endpointB, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 8443, "profile-b", configB)
	deployment := createDirectSharedChain(t, st, serverID, endpointA.ID, "move")
	userID, _ := st.InsertUser(ctx, "user", "global", "sub", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID}); err != nil {
		t.Fatal(err)
	}
	oldRevision, _ := st.PublishedChainRevision(ctx, deployment.ChainID)
	desiredSnapshot := oldRevision.Snapshot
	desiredSnapshot.EndpointID = endpointB.ID
	_, err = st.CreateChainRevision(ctx, deployment.ChainID, desiredSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	hops, _ := st.ChainHops(ctx, deployment.ChainID)
	node, _ := st.NodeByID(ctx, deployment.NodeID)
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	d.publishDesiredRevision(ctx, deployment.ChainID, hops, *node)

	commands, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(commands) != 1 {
		t.Fatalf("endpoint apply commands: len=%d err=%v", len(commands), err)
	}
	var applyPayload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(commands[0].Data, &applyPayload); err != nil {
		t.Fatal(err)
	}
	if applyPayload.EndpointID != endpointB.ID {
		t.Fatalf("apply 应发往新端点 %d，实际 = %d", endpointB.ID, applyPayload.EndpointID)
	}
	// 旧端点无剩余路由，应收到 remove 命令。
	removes, err := st.CommandsByType(ctx, shared.TypeRemoveSharedEndpoint)
	if err != nil || len(removes) != 1 {
		t.Fatalf("endpoint remove commands: len=%d err=%v", len(removes), err)
	}
	var removePayload shared.RemoveSharedEndpointPayload
	if err := json.Unmarshal(removes[0].Data, &removePayload); err != nil {
		t.Fatal(err)
	}
	if removePayload.EndpointID != endpointA.ID {
		t.Fatalf("remove 应发往旧端点 %d，实际 = %d", endpointA.ID, removePayload.EndpointID)
	}
}

// TestChainDegradedWhenEndpointNotActive 验证链状态与共享端点状态联动：
// 端点未 active 时链应为 degraded，端点生效后链恢复 active。
func TestChainDegradedWhenEndpointNotActive(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "ep-chain")
	chainID := deployment.ChainID
	// 模拟编排完成：跳置 active（createDirectSharedChain 跳过了编排流程）。
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if err := st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
	}

	// createDirectSharedChain 已 PublishChainRevision → 链 active。
	chain, _ := st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("初始链状态 = %s，期望 active", chain.Status)
	}

	// 端点仍为 pending，重算后链应进入 degraded。
	req := &fakeRequester{online: map[int64]bool{serverID: true}}
	d := New(st, req)
	d.recomputeChain(ctx, chainID)
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusDegraded {
		t.Fatalf("端点未生效时链状态 = %s，期望 degraded", chain.Status)
	}
	if chain.Error == "" {
		t.Fatal("链 degraded 时应有错误描述")
	}

	// 端点置 active → 重算后链应恢复 active。
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	d.recomputeChain(ctx, chainID)
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("端点生效后链状态 = %s，期望 active", chain.Status)
	}
	if chain.Error != "" {
		t.Fatalf("恢复后链 error 应为空，实际 = %q", chain.Error)
	}
}

// TestReconcileRemovesEndpointWhenNoRoutes 验证最后一条链删除后，
// reconcile 下发 shared-endpoint.remove（而非空 apply），端点状态重置为 pending。
func TestReconcileRemovesEndpointWhenNoRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "only")
	userID, _ := st.InsertUser(ctx, "user", "global", "sub", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}})
	// 有路由时 reconcile 应下发 apply。
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	applies, _ := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if len(applies) != 1 {
		t.Fatalf("apply 命令数 = %d，期望 1", len(applies))
	}
	realized := json.RawMessage(`{"port":443}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}

	// 删除链（模拟面板删链流程）。
	if err := st.DeleteChain(ctx, deployment.ChainID); err != nil {
		t.Fatal(err)
	}
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}

	// 应下发 remove 命令，而非再次 apply。
	removes, err := st.CommandsByType(ctx, shared.TypeRemoveSharedEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(removes) != 1 {
		t.Fatalf("remove 命令数 = %d，期望 1", len(removes))
	}
	var payload shared.RemoveSharedEndpointPayload
	if err := json.Unmarshal(removes[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EndpointID != endpoint.ID {
		t.Fatalf("remove 载荷 endpoint_id = %d，期望 %d", payload.EndpointID, endpoint.ID)
	}
	applies, _ = st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if len(applies) != 1 {
		t.Fatalf("删链后 apply 命令数 = %d，不应新增", len(applies))
	}

	// 端点状态应重置为 pending，realized 清空。
	ep, err := st.SharedEndpointByID(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Status != store.EndpointStatusPending {
		t.Fatalf("端点状态 = %s，期望 pending", ep.Status)
	}
	if ep.RealizedConfig != nil {
		t.Fatalf("端点 realized_config 应清空，实际 = %s", ep.RealizedConfig)
	}
}
