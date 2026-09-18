package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"lattix/shared"
)

func TestExpectedXrayState(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	serverA, err := st.CreateServer(ctx, ServerDraft{Alias: "entry", BootstrapToken: "token-a", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	serverB, err := st.CreateServer(ctx, ServerDraft{Alias: "exit", BootstrapToken: "token-b", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}

	// 直连节点（独立，非链出口）。
	nodePort := 10001
	nodeID, err := st.InsertNode(ctx, "standalone", serverA, "vless", &nodePort, json.RawMessage(`{"tag":"{{TAG}}"}`))
	if err != nil {
		t.Fatal(err)
	}

	// 共享端点（A 机）：建链前创建，链通过 EndpointID 引用它。
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverA, "vless", 0, "profile-hash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	// 链：A(entry, reverse tunnel) → B(exit)。
	svcPort := 20001
	deploy, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "chain", ServiceServerID: serverB, ServiceProtocol: "vless", ServicePort: &svcPort,
		ServiceConfig: json.RawMessage(`{"tag":"{{TAG}}"}`), ServiceUUID: "svc-uuid",
		EndpointID: endpoint.ID,
		Hops: []InitialChainHop{
			{ServerID: serverA, Role: HopRoleEntry, Transport: "reverse", TunnelUUID: "t-uuid"},
			{ServerID: serverB, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hopEntry := deploy.Hops[0]
	hopExit := deploy.Hops[1]

	// 孤儿共享端点：无任何链引用（链已删除但记录残留等），不应计入期望集合。
	orphan, _, err := st.EnsureSharedEndpoint(ctx, serverA, "vless", 0, "orphan-hash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	// 服务器 A：直连节点 + 入口跳（forward + portal）+ 链引用的共享端点；孤儿端点不计入。
	tagsA, piecesA, err := st.ExpectedXrayState(ctx, serverA)
	if err != nil {
		t.Fatal(err)
	}
	wantTagsA := []string{
		shared.NodeTag(nodeID),
		shared.ChainForwardTag(hopEntry.HopID),
		shared.ChainPortalTag(hopEntry.HopID),
		shared.SharedEndpointTag(endpoint.ID),
	}
	wantPiecesA := []string{
		"forward/" + itoa(hopEntry.HopID),
		"portal/" + itoa(hopEntry.HopID),
		"shared-endpoint/" + itoa(endpoint.ID),
	}
	assertStringSet(t, "服务器 A inbound", tagsA, wantTagsA)
	assertStringSet(t, "服务器 A piece", piecesA, wantPiecesA)

	// 服务器 B（出口）：链出口服务节点 inbound + 下游机 bridge piece；无 forward/portal。
	tagsB, piecesB, err := st.ExpectedXrayState(ctx, serverB)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, "服务器 B inbound", tagsB, []string{shared.NodeTag(deploy.NodeID)})
	assertStringSet(t, "服务器 B piece", piecesB, []string{"bridge/" + itoa(hopExit.HopID)})

	// 删除链后：hops 从 DB 移除，链引用的共享端点失去唯一引用，同样不再计入期望。
	if err := st.DeleteChain(ctx, deploy.ChainID); err != nil {
		t.Fatal(err)
	}
	tagsA2, piecesA2, err := st.ExpectedXrayState(ctx, serverA)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, "删链后服务器 A inbound", tagsA2, []string{shared.NodeTag(nodeID)})
	assertStringSet(t, "删链后服务器 A piece", piecesA2, nil)
	if containsTag(tagsA2, shared.SharedEndpointTag(endpoint.ID)) ||
		containsTag(tagsA2, shared.SharedEndpointTag(orphan.ID)) {
		t.Fatalf("删链后共享端点不应计入期望（可被 xray.cleanup 清理）: %v", tagsA2)
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func assertStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := map[string]bool{}
	for _, v := range got {
		gotSet[v] = true
	}
	wantSet := map[string]bool{}
	for _, v := range want {
		wantSet[v] = true
	}
	if len(gotSet) != len(wantSet) {
		t.Fatalf("%s = %v，期望 %v", label, got, want)
	}
	for v := range wantSet {
		if !gotSet[v] {
			t.Fatalf("%s 缺 %q，实际 %v", label, v, got)
		}
	}
}

// TestExpectedXrayStateHy2 验证 P4 hy2 出口共享链的期望状态：
// 出口共享监听经 service_endpoint_id 引用计入期望（cleanup 不误删、rebuild 重放）；
// 端到端 port_hop 链的 forward 跳附加 <tag>_hop_<port> inbound 计入期望（Task 5 硬约束：
// 缺了会被 agent cleanup 当作孤儿配置件误删）；入口终结 2 跳 hy2 链 hop0 免管道
//（镜像 materializeRevision 的 skipEntryForward，否则 rebuild 自检因缺失误回滚）。
func TestExpectedXrayStateHy2(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	serverA, err := st.CreateServer(ctx, ServerDraft{Alias: "entry", BootstrapToken: "token-a", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	serverC, err := st.CreateServer(ctx, ServerDraft{Alias: "exit", BootstrapToken: "token-c", MachineType: MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}

	// hy2 出口共享监听（C 机，经 service_endpoint_id 被引用）。
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","port_hop":"30000-30003"}`)
	svcEndpoint, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverC, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}

	// 端到端 hy2 链：A(entry, forward 30000, 段长 4) → C(exit)。
	deploy, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "hy2-e2e", ServiceServerID: serverC, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, ServiceEndpointID: svcEndpoint.ID, TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: serverA, Role: HopRoleEntry, Transport: "direct", ForwardPort: 30000},
			{ServerID: serverC, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hopA := deploy.Hops[0]

	// 入口终结 2 跳 hy2 链：入口端点 E2（A 机 vless），hop0 transport=hy2 → 免管道。
	entryEndpoint, _, err := st.EnsureSharedEndpoint(ctx, serverA, "vless", 0, "profile-hash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	deploy2, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "hy2-entry", ServiceServerID: serverC, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, ServiceEndpointID: svcEndpoint.ID, EndpointID: entryEndpoint.ID,
		ServiceUUID: "svc-2", TrafficMultiplierMilli: 1000,
		Hops: []InitialChainHop{
			{ServerID: serverA, Role: HopRoleEntry, Transport: "hy2"},
			{ServerID: serverC, Role: HopRoleExit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hopA2 := deploy2.Hops[0]

	// A 机：链1 forward 主 inbound + 段内附加 _hop_ inbound（30001-30003）+ 入口端点；
	// 链2 hop0 免 forward（tag 与 piece 均不计入）。
	tagsA, piecesA, err := st.ExpectedXrayState(ctx, serverA)
	if err != nil {
		t.Fatal(err)
	}
	fwdTag := shared.ChainForwardTag(hopA.HopID)
	assertStringSet(t, "A inbound", tagsA, []string{
		fwdTag,
		fwdTag + "_hop_30001", fwdTag + "_hop_30002", fwdTag + "_hop_30003",
		shared.SharedEndpointTag(entryEndpoint.ID),
	})
	assertStringSet(t, "A piece", piecesA, []string{
		"forward/" + itoa(hopA.HopID),
		"shared-endpoint/" + itoa(entryEndpoint.ID),
	})
	if containsTag(tagsA, shared.ChainForwardTag(hopA2.HopID)) {
		t.Fatalf("2 跳 hy2 链 hop0 不应期望 forward inbound: %v", tagsA)
	}

	// C 机：出口共享监听经 service_endpoint_id 计入期望（两条链引用同一监听）。
	tagsC, piecesC, err := st.ExpectedXrayState(ctx, serverC)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, "C inbound", tagsC, []string{
		shared.NodeTag(deploy.NodeID), shared.NodeTag(deploy2.NodeID),
		shared.SharedEndpointTag(svcEndpoint.ID),
	})
	assertStringSet(t, "C piece", piecesC, []string{"shared-endpoint/" + itoa(svcEndpoint.ID)})
}
