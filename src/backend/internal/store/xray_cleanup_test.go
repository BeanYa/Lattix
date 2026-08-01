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

	serverA, err := st.CreateServer(ctx, "entry", "", "token-a", MachineTypeDirect, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	serverB, err := st.CreateServer(ctx, "exit", "", "token-b", MachineTypeDirect, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 直连节点（独立，非链出口）。
	nodePort := 10001
	nodeID, err := st.InsertNode(ctx, "standalone", serverA, "vless", &nodePort, json.RawMessage(`{"tag":"{{TAG}}"}`))
	if err != nil {
		t.Fatal(err)
	}

	// 链：A(entry, reverse tunnel) → B(exit)。
	svcPort := 20001
	deploy, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "chain", ServiceServerID: serverB, ServiceProtocol: "vless", ServicePort: &svcPort,
		ServiceConfig: json.RawMessage(`{"tag":"{{TAG}}"}`), ServiceUUID: "svc-uuid",
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

	// 共享端点（A 机）。
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverA, "vless", 0, "profile-hash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	// 服务器 A：直连节点 + 入口跳（forward + portal）+ 共享端点。
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

	// 删除链后：hops 从 DB 移除，期望集合不再含该链配置件。
	if err := st.DeleteChain(ctx, deploy.ChainID); err != nil {
		t.Fatal(err)
	}
	tagsA2, piecesA2, err := st.ExpectedXrayState(ctx, serverA)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, "删链后服务器 A inbound", tagsA2,
		[]string{shared.NodeTag(nodeID), shared.SharedEndpointTag(endpoint.ID)})
	assertStringSet(t, "删链后服务器 A piece", piecesA2, []string{"shared-endpoint/" + itoa(endpoint.ID)})
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
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
