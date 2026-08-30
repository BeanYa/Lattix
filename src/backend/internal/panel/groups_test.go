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
	entryID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: name+"-entry", Address: "entry.example.com", BootstrapToken: "tok-"+name+"-e", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	exitID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: name+"-exit", Address: "exit.example.com", BootstrapToken: "tok-"+name+"-x", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
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

// TestGroupInputDedupesDuplicateIDs 重复的 chain_ids / external_subscriptions /
// user_ids / link_group_ids 在入参校验时去重（保持首次出现），不再触发主键冲突 500。
func TestGroupInputDedupesDuplicateIDs(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, subID, u1, _ := groupsFixture(t)
	srv := &Server{st: st}
	code, _ := postGroup(t, srv, srv.handleCreateLinkGroup, "/api/link-group/create",
		`{"name":"去重组","chain_ids":[`+itoa(chainA)+`,`+itoa(chainA)+`],"external_subscriptions":[{"subscription_id":`+itoa(subID)+`,"mode":"stack"},{"subscription_id":`+itoa(subID)+`,"mode":"merge"}]}`)
	if code != "OK" {
		t.Fatalf("重复 id 应去重成功, code = %q", code)
	}
	groups, err := st.ListLinkGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ChainCount != 1 || groups[0].ExtSubCount != 1 ||
		groups[0].ExternalSubscriptions[0].Mode != "stack" {
		t.Fatalf("链路分组应去重存储（保留首次出现）: %+v", groups)
	}
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = postGroup(t, srv, srv.handleCreateUserGroup, "/api/user-group/create",
		`{"name":"去重人组","user_ids":[`+itoa(u1)+`,`+itoa(u1)+`],"link_group_ids":[`+itoa(lgID)+`,`+itoa(lgID)+`]}`)
	if code != "OK" {
		t.Fatalf("重复用户 id 应去重成功, code = %q", code)
	}
	ugroups, err := st.ListUserGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ugroups) != 1 || ugroups[0].MemberCount != 1 || ugroups[0].LinkGroupCount != 1 {
		t.Fatalf("用户分组应去重存储: %+v", ugroups)
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

// TestUserGroupCreateMovesUsersAcrossGroups 一用户一组约束：把已在「青铜会员」的
// 用户分配到「白银会员」时自动移组，旧组不再包含该用户。
func TestUserGroupCreateMovesUsersAcrossGroups(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, u2 := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	code, _ := postGroup(t, srv, srv.handleCreateUserGroup, "/api/user-group/create",
		`{"name":"青铜会员","user_ids":[`+itoa(u1)+`],"link_group_ids":[`+itoa(lgID)+`]}`)
	if code != "OK" {
		t.Fatalf("创建青铜会员失败, code = %q", code)
	}
	code, _ = postGroup(t, srv, srv.handleCreateUserGroup, "/api/user-group/create",
		`{"name":"白银会员","user_ids":[`+itoa(u1)+`,`+itoa(u2)+`],"link_group_ids":[`+itoa(lgID)+`]}`)
	if code != "OK" {
		t.Fatalf("创建白银会员失败, code = %q", code)
	}
	ids, err := st.UserGroupIDsForUser(ctx, u1)
	if err != nil || len(ids) != 1 {
		t.Fatalf("u1 应仅属于一个分组, ids = %v err %v", ids, err)
	}
	groups, err := st.ListUserGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == "青铜会员" && len(g.UserIDs) != 0 {
			t.Fatalf("青铜会员应已移出 u1, members = %v", g.UserIDs)
		}
	}
}

// TestUserDTOEffectiveChainIDs 用户卡片链路数按生效分配展示：
// 分组用户 effective_chain_ids 为分组派生链路（直接分配被遮蔽），
// 非分组用户退化为直接分配（与 chain_ids 一致）。
func TestUserDTOEffectiveChainIDs(t *testing.T) {
	ctx := context.Background()
	st, chainA, chainB, _, u1, u2 := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA, chainB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜会员", []int64{u1}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	grouped, err := st.UserByID(ctx, u1)
	if err != nil {
		t.Fatal(err)
	}
	dto := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/api/user/list", nil), *grouped, nil)
	if len(dto.EffectiveChainIDs) != 2 ||
		dto.EffectiveChainIDs[0] != chainA || dto.EffectiveChainIDs[1] != chainB {
		t.Fatalf("分组用户 effective_chain_ids = %v, want [%d %d]", dto.EffectiveChainIDs, chainA, chainB)
	}
	if len(dto.ChainIDs) != 0 {
		t.Fatalf("分组用户直接 chain_ids 应保持为空, got %v", dto.ChainIDs)
	}
	direct, err := st.UserByID(ctx, u2)
	if err != nil {
		t.Fatal(err)
	}
	dto2 := srv.toUserDTO(httptest.NewRequest(http.MethodGet, "/api/user/list", nil), *direct, nil)
	if len(dto2.EffectiveChainIDs) != 0 {
		t.Fatalf("非分组用户 effective_chain_ids = %v, want []", dto2.EffectiveChainIDs)
	}
}
