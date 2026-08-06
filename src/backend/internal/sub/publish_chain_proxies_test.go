package sub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// setupChainFixture 搭建一条共享 VLESS 端点链路及其发布修订，并分配给用户。
// activate 控制共享端点是否置为 active（模拟 agent 回执前后的两种状态）。
func setupChainFixture(t *testing.T, activate bool) (*store.Store, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "token-entry", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "token-exit", store.MachineTypeDirect, "", "", "JP", "")

	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if activate {
		realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
		if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
			t.Fatal(err)
		}
	}

	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "JP直连Test", ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: entryID, Role: store.HopRoleEntry},
			{ServerID: exitID, Role: store.HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}

	userID, _ := st.InsertUser(ctx, "Bean", "global-user-uuid", "bean-token", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID}); err != nil {
		t.Fatal(err)
	}
	return st, userID
}

// TestPublishUserIncludesAssignedChainProxies 验证已分配且部署完成的链路会出现在
// 订阅快照中（proxies 非空），且 clash 输出携带可生效的 DNS 与 geodata 配置。
func TestPublishUserIncludesAssignedChainProxies(t *testing.T) {
	st, userID := setupChainFixture(t, true)

	result, err := New(st, nil, nil).PublishUser(context.Background(), userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "JP直连Test") {
		t.Fatalf("published clash snapshot missing chain proxy:\n%s", clash)
	}
	if strings.Contains(clash, "proxies: []") {
		t.Fatalf("published clash snapshot has empty proxies:\n%s", clash)
	}
	// GEOSITE/GEOIP 规则需配合 geodata-mode 与 geox-url 才能在客户端生效。
	for _, want := range []string{
		"geodata-mode: true", "geox-url:", "geo-auto-update: true", "enhanced-mode: fake-ip",
		"GEOSITE,", "GEOIP,cn", "MATCH,",
	} {
		if !strings.Contains(clash, want) {
			t.Errorf("published clash snapshot missing %q:\n%s", want, clash)
		}
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings for healthy chain: %v", result.Warnings)
	}
}

// TestPublishUserSurfacesSkippedChainWarnings 验证当已分配链路因共享端点未生效而被
// 跳过时，proxies 为空且发布结果携带可读的跳过原因（而非静默丢弃）。
func TestPublishUserSurfacesSkippedChainWarnings(t *testing.T) {
	st, userID := setupChainFixture(t, false)

	result, err := New(st, nil, nil).PublishUser(context.Background(), userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "proxies: []") {
		t.Fatalf("expected empty proxies for inactive endpoint, got:\n%s", clash)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected skip warnings for inactive endpoint, got none; clash:\n%s", clash)
	}
	joined := strings.Join(result.Warnings, "\n")
	if !strings.Contains(joined, "JP直连Test") {
		t.Errorf("warnings should name the skipped chain, got: %v", result.Warnings)
	}
	t.Logf("warnings: %v", result.Warnings)
}

// TestPublishUserIncludesJoinedChainsOnSharedEndpoint 验证跨 profile 加入同一
// 共享端点的两条链都出现在订阅中（条目参数取自端点 realized_config，与链自身
// 的 service config 无关）。
func TestPublishUserIncludesJoinedChainsOnSharedEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "token-entry", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "token-exit", store.MachineTypeDirect, "", "", "JP", "")

	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"a.example.com"}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile-a", configA)
	if err != nil {
		t.Fatal(err)
	}
	configB := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"b.example.com"}}`)
	joined, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile-b", configB)
	if err != nil || joined.ID != endpoint.ID {
		t.Fatalf("join: id=%d err=%v, want endpoint %d", joined.ID, err, endpoint.ID)
	}
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}

	deploy := func(name string, config json.RawMessage) int64 {
		t.Helper()
		deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
			Name: name, ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
			ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
			TrafficMultiplierMilli: 1000,
			Hops: []store.InitialChainHop{
				{ServerID: entryID, Role: store.HopRoleEntry},
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
	chainA := deploy("JP共享A", configA)
	chainB := deploy("JP共享B", configB)

	userID, _ := st.InsertUser(ctx, "Bean", "global-user-uuid", "bean-token", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainA, chainB}); err != nil {
		t.Fatal(err)
	}

	result, err := New(st, nil, nil).PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	for _, want := range []string{"JP共享A", "JP共享B"} {
		if !strings.Contains(clash, want) {
			t.Fatalf("published clash snapshot missing %q:\n%s", want, clash)
		}
	}
	// 共享端点条目只渲染端点 realized 参数：链 B 自身的 dest（b.example.com）不得泄漏进订阅。
	if strings.Contains(clash, "b.example.com") {
		t.Fatalf("shared-endpoint entry must render endpoint params only, chain B config leaked:\n%s", clash)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
}
