package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
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

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
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

func TestReconcileSharedEndpointGroupUsersGetDistinctIdentities(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	chainA := createDirectSharedChain(t, st, serverID, endpoint.ID, "g-a")
	chainB := createDirectSharedChain(t, st, serverID, endpoint.ID, "g-b")
	memberA, _ := st.InsertUser(ctx, "member-a", "00000000-0000-0000-0000-0000000000aa", "sub-ga", nil)
	memberB, _ := st.InsertUser(ctx, "member-b", "00000000-0000-0000-0000-0000000000bb", "sub-gb", nil)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA.ChainID, chainB.ChainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜", []int64{memberA, memberB}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	// 回归：分组派生身份不得退化为 access:0（xray 以 email 为键，重复即
	// "User access:0 already exists"，导致共享入口部署失败）
	if len(payload.Clients) != 4 {
		t.Fatalf("clients = %d, want 4: %+v", len(payload.Clients), payload.Clients)
	}
	seen := map[string]bool{}
	for _, client := range payload.Clients {
		if seen[client.Email] {
			t.Fatalf("duplicate client email %q", client.Email)
		}
		seen[client.Email] = true
		if client.Email == "access:0" || !strings.HasPrefix(client.Email, "group:") {
			t.Fatalf("client email %q is not a distinct group identity", client.Email)
		}
		if !strings.Contains(client.Email, "00000000-0000-0000-0000-0000000000aa") &&
			!strings.Contains(client.Email, "00000000-0000-0000-0000-0000000000bb") {
			t.Fatalf("client email %q does not embed a member user uuid", client.Email)
		}
	}
	if len(payload.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(payload.Routes))
	}
	for _, route := range payload.Routes {
		if len(route.Users) != 2 {
			t.Fatalf("route %d users = %d, want 2", route.ChainID, len(route.Users))
		}
		for _, user := range route.Users {
			if !seen[user] {
				t.Fatalf("route user %q not present in clients", user)
			}
		}
	}
}

func TestPublishReconcilesPreviousAndNewSharedEndpoints(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
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
	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	d.publishDesiredRevision(ctx, deployment.ChainID, hops, *node)

	commands, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(commands) != 2 {
		t.Fatalf("endpoint apply commands: len=%d err=%v（旧+新端点各一次）", len(commands), err)
	}
	seen := map[int64]bool{}
	for _, cmd := range commands {
		var applyPayload shared.ApplySharedEndpointPayload
		if err := json.Unmarshal(cmd.Data, &applyPayload); err != nil {
			t.Fatal(err)
		}
		seen[applyPayload.EndpointID] = true
	}
	if !seen[endpointA.ID] || !seen[endpointB.ID] {
		t.Fatalf("apply 应覆盖旧端点 %d 与新端点 %d，实际 = %v", endpointA.ID, endpointB.ID, seen)
	}
	// 建链即部署：切换端点时旧端点 reconcile 为 apply（可空 clients），不再 remove。
	removes, err := st.CommandsByType(ctx, shared.TypeRemoveSharedEndpoint)
	if err != nil || len(removes) != 0 {
		t.Fatalf("不应下发 remove 命令，实际有 %d 条", len(removes))
	}
}

// TestEvaluateDoesNotDoubleApplyInFlightEndpoint 验证 Evaluate 自动重试的幂等性：
// 端点已有在途部署命令（applying）时不得重复下发 apply_shared_endpoint。
// 回归场景：publishDesiredRevision → ReconcileSharedEndpoint（端点置 applying）后立即
// fsm.Evaluate，旧实现对 applying 端点再次自动补发，单次建链产生两次 apply
// （第二次在 agent 侧因 xray 已持有端口而失败 → 端点 failed → 链 degraded）。
func TestEvaluateDoesNotDoubleApplyInFlightEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "inflight")
	chainID := deployment.ChainID
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if err := st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
	}

	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}}, Options{}, Events{})
	// 首次部署（pending → apply #1），端点进入 applying。
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		cmds, _ := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
		return len(cmds)
	}
	if got := count(); got != 1 {
		t.Fatalf("基线 apply 命令数 = %d，期望 1", got)
	}

	// 端点 applying（在途命令未回执）时重算——旧实现会重复下发。
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 1 {
		t.Fatalf("端点 applying 时 Evaluate 不应重复下发 apply（幂等），实际 %d 条", got)
	}

	// 端点 pending（无在途命令）时仍应自动补发（首次部署/重试语义不变）。
	if err := st.SetSharedEndpointPending(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 2 {
		t.Fatalf("端点 pending 时 Evaluate 应补发 apply，实际 %d 条", got)
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
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
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
	d := New(st, req, Options{}, Events{})
	d.fsm.Evaluate(ctx, chainID)
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
	d.fsm.Evaluate(ctx, chainID)
	chain, _ = st.ChainByID(ctx, chainID)
	if chain.Status != store.ChainStatusActive {
		t.Fatalf("端点生效后链状态 = %s，期望 active", chain.Status)
	}
	if chain.Error != "" {
		t.Fatalf("恢复后链 error 应为空，实际 = %q", chain.Error)
	}
}

// TestReconcileDeploysEndpointEvenWithNoUsers 验证建链即部署监听（即使 routes/clients 为空也 apply）。
// 用户分配仅增量添加用户，不再是部署前提。删除最后一条链后端点记录保留。
func TestReconcileDeploysEndpointEvenWithNoUsers(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
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

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	// 有用户时 reconcile 应下发 apply。
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

	// 删除链（模拟面板删链流程）——端点记录应保留。
	if err := st.DeleteChain(ctx, deployment.ChainID); err != nil {
		t.Fatal(err)
	}
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}

	// 不应下发 remove；删链后仍 apply（建链即部署语义下端点记录常驻复用）。
	removes, err := st.CommandsByType(ctx, shared.TypeRemoveSharedEndpoint)
	if err != nil || len(removes) != 0 {
		t.Fatalf("不应下发 remove 命令，实际有 %d 条", len(removes))
	}
	applies, _ = st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if len(applies) < 2 {
		t.Fatalf("删链后应再次下发 apply，实际 %d 条", len(applies))
	}

	ep, err := st.SharedEndpointByID(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	// reconcile 会置 applying；realized 在 ack 前仍保留上次生效值。
	if ep.Status != store.EndpointStatusApplying {
		t.Fatalf("端点状态 = %s，期望 applying", ep.Status)
	}
	if ep.RealizedConfig == nil {
		t.Fatal("端点 realized_config 应保留")
	}
}

// TestEvaluateEndpointAutoRetryBackoff 验证端点自动重试退避：间隔内不重复补发、
// 超间隔后补发、端点生效（ack 清除记录）后再次失败可立即补发。
func TestEvaluateEndpointAutoRetryBackoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "backoff")
	chainID := deployment.ChainID
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if err := st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
	}

	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		cmds, _ := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
		return len(cmds)
	}
	if got := count(); got != 1 {
		t.Fatalf("基线 apply 命令数 = %d，期望 1", got)
	}

	origInterval := endpointAutoRetryMinInterval
	defer func() { endpointAutoRetryMinInterval = origInterval }()

	// 端点 failed 后首次重算：无退避记录 → 允许自动补发（记录本次时间）。
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 2 {
		t.Fatalf("首次失败后应补发，实际 %d 条", got)
	}

	// 再次置 failed 并重算：间隔内（1h）自动补发被抑制。
	endpointAutoRetryMinInterval = time.Hour
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 2 {
		t.Fatalf("退避期间不应重复补发，实际 %d 条", got)
	}

	// 间隔缩短后重算：自动补发恢复。sleep 保证墙上时钟确实越过 1ms 间隔（快机否则 <1ms 不生效）。
	endpointAutoRetryMinInterval = time.Millisecond
	time.Sleep(10 * time.Millisecond)
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 3 {
		t.Fatalf("超间隔后应补发，实际 %d 条", got)
	}

	// 端点生效（ack 路径会调用 clearEndpointRetry）清除退避记录：再次失败可立即补发。
	realized := json.RawMessage(`{"port":443}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	d.clearEndpointRetry(endpoint.ID)
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.fsm.Evaluate(ctx, chainID)
	if got := count(); got != 4 {
		t.Fatalf("ack 清除退避后应立即补发，实际 %d 条", got)
	}
}

// TestReconcileSharedEndpointAggregatesJoinedChains 验证不同 profile 的链加入
// 同一端点后，reconcile 聚合两链的路由与用户（join 经 store 语义，端点行唯一）。
func TestReconcileSharedEndpointAggregatesJoinedChains(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"a.example.com"}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", configA)
	if err != nil {
		t.Fatal(err)
	}
	configB := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"b.example.com"}}`)
	joined, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", configB)
	if err != nil || joined.ID != endpoint.ID {
		t.Fatalf("join: id=%d err=%v, want endpoint %d", joined.ID, err, endpoint.ID)
	}
	chainA := createDirectSharedChain(t, st, serverID, endpoint.ID, "a")
	chainB := createDirectSharedChain(t, st, serverID, endpoint.ID, "b")
	userA, _ := st.InsertUser(ctx, "user-a", "global-a", "sub-a", nil)
	userB, _ := st.InsertUser(ctx, "user-b", "global-b", "sub-b", nil)
	if _, _, err := st.SetUserChains(ctx, userA, []int64{chainA.ChainID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userB, []int64{chainB.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if len(payload.Routes) != 2 || len(payload.Clients) != 2 {
		t.Fatalf("payload routes/clients = %d/%d, want 2/2", len(payload.Routes), len(payload.Clients))
	}
	seen := map[int64]bool{}
	for _, route := range payload.Routes {
		seen[route.ChainID] = true
	}
	if !seen[chainA.ChainID] || !seen[chainB.ChainID] {
		t.Fatalf("routes missing chains: %+v", seen)
	}
}

// TestReconcileHy2ServiceEndpoint 验证出口侧 hy2 端点 reconcile：routes 为空；
// clients = 入口终结链的 tunnel 身份 ∪ 端到端链的业务用户 UUID（spec §3.2 出口共享）。
func TestReconcileHy2ServiceEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","port_hop":"30000-30031","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	epE2, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	// 链1：端到端（无入口端点），业务用户 u1 直接分配。
	dep1, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "e2e", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, ServiceEndpointID: epE.ID,
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 25000},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 链2：入口终结（挂入口端点 E2，service_uuid=svc2 → tunnel 身份）。
	if _, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "entry-term", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, EndpointID: epE2.ID, ServiceEndpointID: epE.ID, ServiceUUID: "svc2",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "hy2"},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	}); err != nil {
		t.Fatal(err)
	}
	u1uuid := "11111111-1111-1111-1111-111111111111"
	u1, _ := st.InsertUser(ctx, "u1", u1uuid, "sub1", nil)
	if _, _, err := st.SetUserChains(ctx, u1, []int64{dep1.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{exitID: true}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, epE.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if payload.EndpointID != epE.ID {
		t.Fatalf("endpoint = %d", payload.EndpointID)
	}
	if len(payload.Routes) != 0 {
		t.Fatalf("出口侧 hy2 端点不应有路由: %+v", payload.Routes)
	}
	want := map[string]string{u1uuid: u1uuid, "svc2": "tunnel:svc2"}
	if len(payload.Clients) != len(want) {
		t.Fatalf("clients = %+v，期望 %v", payload.Clients, want)
	}
	for _, c := range payload.Clients {
		if want[c.ID] != c.Email {
			t.Fatalf("client 不符: %+v（期望 %v）", c, want)
		}
		delete(want, c.ID)
	}
	if len(want) != 0 {
		t.Fatalf("缺少 clients: %v", want)
	}
	if ep, _ := st.SharedEndpointByID(ctx, epE.ID); ep.Status != store.EndpointStatusApplying {
		t.Fatalf("reconcile 后端点应为 applying，实际 %s", ep.Status)
	}
}

// TestReconcileEntryEndpointHy2Route 验证入口端点 route 的出口协议分派：
// 2 跳入口终结 hy2 链 → route.ExitProtocol=hysteria、TargetAddress=出口公网地址、
// TargetPort=出口 realized 公网端口、Target=出口 realized（spec §3.2）；
// vless 链 route 保持 127.0.0.1 回环（回归）。
func TestReconcileEntryEndpointHy2Route(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "tok1", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "tok2", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
	epE2, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, exitID, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	hy2Realized := json.RawMessage(`{"port":1443,"sni":"cdn.example.com","port_hop":"30000-30031"}`)

	// 2 跳入口终结 hy2 链（hop0 transport=hy2，端点直拨出口）。
	depHy2, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-2hop", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, EndpointID: epE2.ID, ServiceEndpointID: epE.ID, ServiceUUID: "svc-r",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "hy2"},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 存量 vless 多跳链（回归对照）：入口 forward 回环。
	vlessCfg := json.RawMessage(`{"protocol":"vless","template":{}}`)
	depVless, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "vless-2hop", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: vlessCfg, EndpointID: epE2.ID, ServiceUUID: "svc-v",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 18443},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 发布两条链的 revision（携带出口 realized）。
	publishWithRealized := func(revisionID int64, realized json.RawMessage) {
		t.Helper()
		rev, err := st.ChainRevisionByID(ctx, revisionID)
		if err != nil {
			t.Fatal(err)
		}
		rev.Snapshot.ServiceRealized = realized
		if err := st.UpdateChainRevision(ctx, rev.ID, store.RevisionStatusActive, "", rev.Snapshot); err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, rev.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	publishWithRealized(depHy2.RevisionID, hy2Realized)
	publishWithRealized(depVless.RevisionID, json.RawMessage(`{"port":4433,"public_key":"k"}`))
	u1, _ := st.InsertUser(ctx, "u1", "11111111-1111-1111-1111-111111111111", "sub1", nil)
	if _, _, err := st.SetUserChains(ctx, u1, []int64{depHy2.ChainID, depVless.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{entryID: true}}, Options{}, Events{})
	if err := d.ReconcileSharedEndpoint(ctx, epE2.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	routes := map[int64]shared.SharedEndpointRoute{}
	for _, r := range payload.Routes {
		routes[r.ChainID] = r
	}
	hy2Route, ok := routes[depHy2.ChainID]
	if !ok {
		t.Fatalf("缺少 hy2 链路由: %+v", payload.Routes)
	}
	if hy2Route.ExitProtocol != shared.ProtocolHysteria2 {
		t.Fatalf("ExitProtocol = %q，期望 hysteria", hy2Route.ExitProtocol)
	}
	if hy2Route.TargetAddress != "exit.test" || hy2Route.TargetPort != 1443 {
		t.Fatalf("hy2 route 目标 = %s:%d，期望 exit.test:1443", hy2Route.TargetAddress, hy2Route.TargetPort)
	}
	if hy2Route.Target.Port != 1443 || hy2Route.Target.SNI != "cdn.example.com" || hy2Route.Target.PortHop != "30000-30031" {
		t.Fatalf("hy2 route Target realized 不符: %+v", hy2Route.Target)
	}
	vlessRoute, ok := routes[depVless.ChainID]
	if !ok {
		t.Fatalf("缺少 vless 链路由: %+v", payload.Routes)
	}
	if vlessRoute.ExitProtocol != "" || vlessRoute.TargetAddress != "127.0.0.1" || vlessRoute.TargetPort != 18443 {
		t.Fatalf("vless route 应保持回环现状: %+v", vlessRoute)
	}
}
