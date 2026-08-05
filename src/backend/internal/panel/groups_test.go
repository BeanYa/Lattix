package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

// groupsFixture 准备：2 条共享入口链路、1 个外部订阅、2 个用户。
func groupsFixture(t *testing.T) (*store.Store, int64, int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	chainA, _ := newTestEndpointChainStore(t, st, "h-a")
	chainB, _ := newTestEndpointChainStore(t, st, "h-b")
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	u1, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	u2, _ := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "tok-b", nil)
	return st, chainA, chainB, subID, u1, u2
}

// newTestEndpointChainStore 是 store 包测试辅助的 panel 副本（入参同为 *store.Store）。
func newTestEndpointChainStore(t *testing.T, st *store.Store, name string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	entryID, _ := st.CreateServer(ctx, name+"-entry", "entry.example.com", "tok-"+name+"-e", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, name+"-exit", "exit.example.com", "tok-"+name+"-x", store.MachineTypeDirect, "", "", "JP", "")
	config, _ := json.Marshal(map[string]any{})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, "vless", 30002, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, []byte(`{"port":20001}`)); err != nil {
		t.Fatal(err)
	}
	nodeID, _ := st.InsertNode(ctx, name+"-node", exitID, "vless", nil, config)
	chainID, _ := st.InsertChain(ctx, name)
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, store.HopRoleEntry, 0, 10001, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, store.HopRoleExit, nodeID, 0, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	realized, _ := json.Marshal(map[string]any{"port": 20001})
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

func postGroup(t *testing.T, srv *Server, h func(http.ResponseWriter, *http.Request), path, body string) (string, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Code, rec.Code
}

func TestLinkGroupRPCValidationAndList(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, subID, _, _ := groupsFixture(t)
	srv := &Server{st: st}
	// 创建成功
	rec := httptest.NewRecorder()
	srv.handleCreateLinkGroup(rec, httptest.NewRequest(http.MethodPost, "/api/link-group/create",
		strings.NewReader(`{"name":"普通组","chain_ids":[`+itoa(chainA)+`],"external_subscriptions":[{"subscription_id":`+itoa(subID)+`,"mode":"stack"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Code string `json:"code"`
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	// 重名拒绝
	code, _ := postGroup(t, srv, srv.handleCreateLinkGroup, "/api/link-group/create", `{"name":"普通组","chain_ids":[]}`)
	if code != "INVALID_ARGUMENT" {
		t.Fatalf("重名应拒绝, code = %q", code)
	}
	// 无共享入口链路拒绝（chain 999 不存在）
	code, _ = postGroup(t, srv, srv.handleCreateLinkGroup, "/api/link-group/create", `{"name":"坏组","chain_ids":[999]}`)
	if code != "INVALID_ARGUMENT" {
		t.Fatalf("坏链路应拒绝, code = %q", code)
	}
	// 非法 mode 拒绝
	code, _ = postGroup(t, srv, srv.handleCreateLinkGroup, "/api/link-group/create", `{"name":"坏组","external_subscriptions":[{"subscription_id":`+itoa(subID)+`,"mode":"bogus"}]}`)
	if code != "INVALID_ARGUMENT" {
		t.Fatalf("坏 mode 应拒绝, code = %q", code)
	}
	// 列表包含计数与引用
	if _, err := st.CreateUserGroup(ctx, "青铜会员", nil, []int64{created.Data.ID}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleListLinkGroups(rec, httptest.NewRequest(http.MethodGet, "/api/link-group/list", nil))
	var listed struct {
		Data []struct {
			ID                    int64    `json:"id"`
			Name                  string   `json:"name"`
			ChainCount            int      `json:"chain_count"`
			ExtSubCount           int      `json:"external_subscription_count"`
			UserGroupNames        []string `json:"user_group_names"`
			ChainIDs              []int64  `json:"chain_ids"`
			ExternalSubscriptions []struct {
				SubscriptionID int64  `json:"subscription_id"`
				Mode           string `json:"mode"`
			} `json:"external_subscriptions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].Name != "普通组" || listed.Data[0].ChainCount != 1 ||
		listed.Data[0].ExtSubCount != 1 || len(listed.Data[0].UserGroupNames) != 1 ||
		listed.Data[0].UserGroupNames[0] != "青铜会员" {
		t.Fatalf("list = %+v", listed.Data)
	}
	// 契约：external_subscriptions 元素键为 subscription_id / mode
	subs := listed.Data[0].ExternalSubscriptions
	if len(subs) != 1 || subs[0].SubscriptionID != subID || subs[0].Mode != "stack" {
		t.Fatalf("external_subscriptions = %+v", subs)
	}
}

func TestUserGroupRPCAndUserDTOGroups(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, u2 := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	rec := httptest.NewRecorder()
	srv.handleCreateUserGroup(rec, httptest.NewRequest(http.MethodPost, "/api/user-group/create",
		strings.NewReader(`{"name":"青铜会员","user_ids":[`+itoa(u1)+`,`+itoa(u2)+`],"link_group_ids":[`+itoa(lgID)+`]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	code, _ := postGroup(t, srv, srv.handleCreateUserGroup, "/api/user-group/create", `{"name":"青铜会员"}`)
	if code != "INVALID_ARGUMENT" {
		t.Fatalf("重名应拒绝, code = %q", code)
	}
	code, _ = postGroup(t, srv, srv.handleCreateUserGroup, "/api/user-group/create", `{"name":"坏组","user_ids":[999]}`)
	if code != "INVALID_ARGUMENT" {
		t.Fatalf("不存在用户应拒绝, code = %q", code)
	}
	// userDTO 携带 user_group_ids
	u, _ := st.UserByID(ctx, u1)
	dto := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/", nil), *u, nil)
	if len(dto.UserGroupIDs) != 1 {
		t.Fatalf("dto.UserGroupIDs = %v", dto.UserGroupIDs)
	}
	// 删除分组后恢复
	groups, _ := st.ListUserGroups(ctx)
	if err := st.DeleteUserGroup(ctx, groups[0].ID); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByID(ctx, u1)
	dto = srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/", nil), *u, nil)
	if len(dto.UserGroupIDs) != 0 {
		t.Fatalf("删除后 dto.UserGroupIDs = %v", dto.UserGroupIDs)
	}
}

// TestSubURLStableAcrossGroupOps 验证 toUserDTO 的 sub_url 在分组操作前后不变
// （用户硬约束：已有用户的订阅地址不能改变）。
func TestSubURLStableAcrossGroupOps(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, _ := groupsFixture(t)
	user, err := st.UserByID(ctx, u1)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	before := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/", nil), *user, nil)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜会员", []int64{u1}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	user, _ = st.UserByID(ctx, u1)
	mid := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/", nil), *user, nil)
	if mid.SubURL != before.SubURL || mid.SubLinksURL != before.SubLinksURL {
		t.Fatalf("入组后 sub_url 变化: before %s / %s, after %s / %s",
			before.SubURL, before.SubLinksURL, mid.SubURL, mid.SubLinksURL)
	}
	if len(mid.UserGroupIDs) != 1 {
		t.Fatalf("入组后 user_group_ids = %v", mid.UserGroupIDs)
	}
	if err := st.DeleteUserGroup(ctx, mid.UserGroupIDs[0]); err != nil {
		t.Fatal(err)
	}
	user, _ = st.UserByID(ctx, u1)
	after := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/", nil), *user, nil)
	if after.SubURL != before.SubURL || after.SubLinksURL != before.SubLinksURL {
		t.Fatalf("出组后 sub_url 变化: before %s, after %s", before.SubURL, after.SubURL)
	}
}
