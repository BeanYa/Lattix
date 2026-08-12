package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

// TestSubInfoNodesCountReflectsAssignments 复现线上问题：订阅页节点数应只含
// 用户实际分配的链路 + 其外部订阅节点，而不是面板全量链路 + 外部订阅。
func TestSubInfoNodesCountReflectsAssignments(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// 面板链路池：5 条共享入口链路。
	chainIDs := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		chainID, _ := newTestEndpointChain(t, st, fmt.Sprintf("pool-%d", i))
		chainIDs = append(chainIDs, chainID)
	}
	// 外部订阅：3 个节点，直接分配给 u1。
	subID, _ := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场Y", URL: "https://sub.example.com/y", UpdateIntervalHours: 24,
	})
	extChains := make([]store.ExternalChain, 0, 3)
	for i := 0; i < 3; i++ {
		cfg, _ := json.Marshal(extsub.Node{
			Name: fmt.Sprintf("ext-%d", i), Type: "vless",
			Server: fmt.Sprintf("ext-%d.example.com", i), Port: 443,
			Extra: map[string]any{"id": fmt.Sprintf("x-uuid-%d", i)},
		})
		extChains = append(extChains, store.ExternalChain{
			SubscriptionID: subID, Name: fmt.Sprintf("ext-%d", i), Protocol: "vless",
			Server: fmt.Sprintf("ext-%d.example.com", i), Port: 443,
			Config: cfg, ConfigSHA256: fmt.Sprintf("sha-%d", i),
		})
	}
	if _, err := st.ReplaceExternalChains(ctx, subID, extChains); err != nil {
		t.Fatal(err)
	}

	u1, _ := st.InsertUser(ctx, "u1", "00000000-0000-0000-0000-0000000000u1", "tok-u1", nil)
	// u1 仅分配 2 条链路 + 外部订阅。
	if _, _, err := st.SetUserChains(ctx, u1, chainIDs[:2]); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, u1, []store.UserExternalSubscription{
		{UserID: u1, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}

	server := New(st, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sub/tok-u1/info", nil)
	req.SetPathValue("token", "tok-u1")
	server.HandleSubInfo(rec, req)
	if rec.Code != 200 {
		t.Fatalf("info status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// 期望：2 条分配链路 + 3 个外部节点 = 5；若复现线上 bug 则为 8（5+3）。
	if resp.NodesCount != 5 {
		t.Fatalf("nodes_count = %d, want 5 (2 assigned chains + 3 external nodes)", resp.NodesCount)
	}

	// 对照：未分配任何链路的用户应只看到 0（未挂外部订阅）。
	u2, _ := st.InsertUser(ctx, "u2", "00000000-0000-0000-0000-0000000000u2", "tok-u2", nil)
	_ = u2
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/sub/tok-u2/info", nil)
	req.SetPathValue("token", "tok-u2")
	server.HandleSubInfo(rec, req)
	var resp2 SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.NodesCount != 0 {
		t.Fatalf("u2 nodes_count = %d, want 0", resp2.NodesCount)
	}

	// 分组场景：u3 加入用户分组（链路分组含全部 5 条链路 + 外部订阅），
	// 同时直接分配 2 条链路（按设计被遮蔽）。
	lgID, err := st.CreateLinkGroup(ctx, "全量组", chainIDs,
		[]store.LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: store.ExtSubModeStack}})
	if err != nil {
		t.Fatal(err)
	}
	u3, _ := st.InsertUser(ctx, "u3", "00000000-0000-0000-0000-0000000000u3", "tok-u3", nil)
	if _, err := st.CreateUserGroup(ctx, "vip", []int64{u3}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, u3, chainIDs[:2]); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/sub/tok-u3/info", nil)
	req.SetPathValue("token", "tok-u3")
	server.HandleSubInfo(rec, req)
	var resp3 SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp3); err != nil {
		t.Fatal(err)
	}
	// 分组派生取并集：5 条链路 + 3 个外部节点 = 8（直接分配的 2 条被遮蔽）。
	if resp3.NodesCount != 8 {
		t.Fatalf("u3 nodes_count = %d, want 8 (group-derived 5 chains + 3 external nodes)", resp3.NodesCount)
	}

	// 线上精确场景（SecondBoy）：链路分组 Default 只勾 2 条链路、不勾外部订阅；
	// 用户在 Default 用户分组内；外部订阅由管理员直接挂在用户身上。
	// 期望：计数 = 分组派生 2 条链路（直接外挂的外部订阅被遮蔽），
	// 若复现线上 bug 则为 2 + 3 = 5（外部订阅泄漏）。
	lg2ID, err := st.CreateLinkGroup(ctx, "Default", chainIDs[:2], nil)
	if err != nil {
		t.Fatal(err)
	}
	u4, _ := st.InsertUser(ctx, "u4", "00000000-0000-0000-0000-0000000000u4", "tok-u4", nil)
	if _, err := st.CreateUserGroup(ctx, "Default", []int64{u4}, []int64{lg2ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, u4, []store.UserExternalSubscription{
		{UserID: u4, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/sub/tok-u4/info", nil)
	req.SetPathValue("token", "tok-u4")
	server.HandleSubInfo(rec, req)
	var resp4 SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp4); err != nil {
		t.Fatal(err)
	}
	t.Logf("u4 (SecondBoy 场景) nodes_count = %d", resp4.NodesCount)
	if resp4.NodesCount != 2 {
		t.Fatalf("u4 nodes_count = %d, want 2 (group-derived chains only)", resp4.NodesCount)
	}

	// 并集语义（线上 53 的最可能成因）：用户分组可同时关联多个链路分组，
	// 生效集合 = 各链路分组的并集。u5 的用户分组关联「2 条链路」与
	// 「仅外部订阅」两个链路分组 → 计数 = 2 + 3 = 5。
	lg3ID, err := st.CreateLinkGroup(ctx, "仅外部订阅", nil,
		[]store.LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: store.ExtSubModeStack}})
	if err != nil {
		t.Fatal(err)
	}
	u5, _ := st.InsertUser(ctx, "u5", "00000000-0000-0000-0000-0000000000u5", "tok-u5", nil)
	if _, err := st.CreateUserGroup(ctx, "multi", []int64{u5}, []int64{lg2ID, lg3ID}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/sub/tok-u5/info", nil)
	req.SetPathValue("token", "tok-u5")
	server.HandleSubInfo(rec, req)
	var resp5 SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp5); err != nil {
		t.Fatal(err)
	}
	if resp5.NodesCount != 5 {
		t.Fatalf("u5 nodes_count = %d, want 5 (union: 2 chains + 3 external nodes)", resp5.NodesCount)
	}
}
