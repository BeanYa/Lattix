package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

func TestValidateTrafficResetDay(t *testing.T) {
	for _, day := range []int{0, 1, 28, 29, 30, 31} {
		if err := validateTrafficResetDay(day); err != nil {
			t.Errorf("day %d rejected: %v", day, err)
		}
	}
	for _, day := range []int{-1, 32} {
		if err := validateTrafficResetDay(day); err == nil {
			t.Errorf("day %d accepted", day)
		}
	}
}

func TestResetUserSubscriptionToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "user-uuid", "old-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(fmt.Sprintf(`{"user_id": %d}`, userID))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Code string `json:"code"`
		Data struct {
			SubToken    string `json:"sub_token"`
			SubURL      string `json:"sub_url"`
			SubLinksURL string `json:"sub_links_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != shared.CodeOK {
		t.Fatalf("code = %q, body = %s", got.Code, rec.Body.String())
	}
	if got.Data.SubToken == "" || got.Data.SubToken == "old-token" {
		t.Fatalf("sub_token = %q", got.Data.SubToken)
	}
	if len(got.Data.SubToken) != 32 {
		t.Fatalf("sub_token length = %d, want 32", len(got.Data.SubToken))
	}
	if !strings.Contains(got.Data.SubURL, got.Data.SubToken) || !strings.Contains(got.Data.SubLinksURL, got.Data.SubToken) {
		t.Fatalf("urls do not contain new token: %s %s", got.Data.SubURL, got.Data.SubLinksURL)
	}
	if _, err := st.UserBySubToken(ctx, got.Data.SubToken); err != nil {
		t.Fatalf("new token not persisted: %v", err)
	}
	if _, err := st.UserBySubToken(ctx, "old-token"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old token still resolves: %v", err)
	}
}

func TestResetUserSubscriptionTokenRejectsInvalidUserID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(`{"user_id": 0}`)))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeInvalidArgument)
	}
}

func TestResetUserSubscriptionTokenMissingSubscriptionService(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "user-uuid", "old-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st}
	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(fmt.Sprintf(`{"user_id": %d}`, userID))))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeInvalidArgument)
	}
}

func TestResetUserSubscriptionTokenMissingUser(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(`{"user_id": 9999}`)))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeNotFound {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeNotFound)
	}
}

func TestExpirySweepRepublishesUserSubscription(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(-time.Hour)
	userID, err := st.InsertUser(ctx, "u1", "u1-uuid", "u1-token", &expires)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	subSrv := sub.New(st, nil, nil)
	subSrv.StartRegenerator(ctx)
	requester := &chainEditRequester{online: map[int64]bool{serverID: false}}
	server := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester, subscriptions: subSrv}

	server.sweepExpiredUsers(ctx)

	// 到期停权后必须触发订阅重发布：发布文件出现且节点清空（评审发现的原缺失路径）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := st.PublishedSubscriptionFile(ctx, userID, "links")
		if err == nil {
			if strings.TrimSpace(string(file.Content)) != "" {
				t.Fatalf("过期用户订阅仍含内容: %q", file.Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("到期停权后订阅未重发布（PublishedSubscriptionFile 缺失）")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func TestHandleListUsersOnlineConnectionsAccessIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: serverID, Role: store.HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "alice", "11111111-2222-3333-4444-555555555555", "alice-token", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 1 {
		t.Fatalf("assignment: added=%+v err=%v", added, err)
	}
	server := &Server{st: st, onlineUsers: &OnlineUsersTracker{resolve: onlineUserResolver(st)}}
	server.onlineUsers.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "access:" + strconv.FormatInt(added[0].ID, 10), IPs: []string{"1.1.1.1", "2.2.2.2"}},
	}, time.Now().UTC())
	rec := httptest.NewRecorder()
	server.handleListUsers(rec, httptest.NewRequest(http.MethodGet, "/api/user/list", nil))
	var resp struct {
		Code string `json:"code"`
		Data []struct {
			OnlineConnections int `json:"online_connections"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeOK || len(resp.Data) != 1 {
		t.Fatalf("code = %q data = %+v", resp.Code, resp.Data)
	}
	if resp.Data[0].OnlineConnections != 2 {
		t.Fatalf("alice online_connections via access identity = %d, want 2", resp.Data[0].OnlineConnections)
	}
}

func TestHandleListUsersOnlineConnectionsNoData(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.InsertUser(ctx, "alice", "11111111-2222-3333-4444-555555555555", "alice-token", nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, onlineUsers: &OnlineUsersTracker{}}
	rec := httptest.NewRecorder()
	server.handleListUsers(rec, httptest.NewRequest(http.MethodGet, "/api/user/list", nil))
	var resp struct {
		Code string `json:"code"`
		Data []struct {
			OnlineConnections int `json:"online_connections"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeOK || len(resp.Data) != 1 {
		t.Fatalf("code = %q data = %+v", resp.Code, resp.Data)
	}
	if resp.Data[0].OnlineConnections != 0 {
		t.Fatalf("online_connections without tracker data = %d, want 0", resp.Data[0].OnlineConnections)
	}
}

func TestHandleListUsersOnlineConnectionsWithSnapshot(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.InsertUser(ctx, "alice", "11111111-2222-3333-4444-555555555555", "alice-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUser(ctx, "carol", "11111111-2222-3333-4444-555555555557", "carol-token", nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, onlineUsers: &OnlineUsersTracker{}}
	server.onlineUsers.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "11111111-2222-3333-4444-555555555555", IPs: []string{"1.1.1.1", "2.2.2.2"}},
	}, time.Now().UTC())
	rec := httptest.NewRecorder()
	server.handleListUsers(rec, httptest.NewRequest(http.MethodGet, "/api/user/list", nil))
	var resp struct {
		Code string `json:"code"`
		Data []struct {
			UUID              string `json:"uuid"`
			OnlineConnections int    `json:"online_connections"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeOK || len(resp.Data) != 2 {
		t.Fatalf("code = %q data = %+v", resp.Code, resp.Data)
	}
	got := map[string]int{}
	for _, u := range resp.Data {
		got[u.UUID] = u.OnlineConnections
	}
	if got["11111111-2222-3333-4444-555555555555"] != 2 {
		t.Fatalf("alice online_connections = %d, want 2", got["11111111-2222-3333-4444-555555555555"])
	}
	if got["11111111-2222-3333-4444-555555555557"] != 0 {
		t.Fatalf("carol online_connections = %d, want 0", got["11111111-2222-3333-4444-555555555557"])
	}
}

// TestHy2ServiceSharedUserFanout 验证 hy2 出口共享链的用户扇出改道（P4）：
// 出口监听由共享端点承载，add/remove_user 不扇出到出口节点；分配变更改为
// reconcile 出口共享端点（业务用户 UUID 进入端点 clients）。
func TestHy2ServiceSharedUserFanout(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, _, cID := hy2ChainFixture(t)
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, cID, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name: "hy2-e2e", ServiceServerID: cID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: hy2cfg, ServiceEndpointID: epE.ID,
		TrafficMultiplierMilli: 1000,
		Hops:                   []store.InitialChainHop{{ServerID: cID, Role: store.HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userUUID := "11111111-1111-1111-1111-111111111111"
	userID, _ := st.InsertUser(ctx, "u1", userUUID, "sub1", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{dep.ChainID})
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.NodeByID(ctx, dep.NodeID)
	if err != nil {
		t.Fatal(err)
	}

	// 扇出改道：add_user 不下发到 hy2 出口共享链的出口节点。
	serverAPI.fanoutUserDiff(ctx, userUUID, []store.Node{*node}, []int64{node.ID}, nil)
	if cmds, err := st.CommandsByType(ctx, shared.TypeAddUser); err != nil || len(cmds) != 0 {
		t.Fatalf("hy2 出口共享节点不应收到 add_user: %d 条 err=%v", len(cmds), err)
	}

	// 分配变更 reconcile 出口共享监听（业务用户 UUID 进入 clients）。
	serverAPI.reconcileAssignmentEndpoints(ctx, added, nil)
	cmds, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("应 reconcile 出口共享端点一次: %d 条 err=%v", len(cmds), err)
	}
	var payload shared.ApplySharedEndpointPayload
	if err := json.Unmarshal(cmds[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EndpointID != epE.ID {
		t.Fatalf("reconcile 端点 = %d, want %d", payload.EndpointID, epE.ID)
	}
	found := false
	for _, c := range payload.Clients {
		if c.ID == userUUID {
			found = true
		}
	}
	if !found {
		t.Fatalf("出口端点 clients 应含业务用户 %s: %+v", userUUID, payload.Clients)
	}

	// 对照回归：普通（非出口共享）链的出口节点仍收到 add_user。
	vlessCfg := json.RawMessage(`{"protocol":"vless","template":{}}`)
	plainNode, err := st.InsertNode(ctx, "plain", cID, shared.ProtocolVLESS, nil, vlessCfg)
	if err != nil {
		t.Fatal(err)
	}
	node2, _ := st.NodeByID(ctx, plainNode)
	serverAPI.fanoutUserDiff(ctx, userUUID, []store.Node{*node2}, []int64{plainNode}, nil)
	if cmds, _ := st.CommandsByType(ctx, shared.TypeAddUser); len(cmds) != 1 {
		t.Fatalf("普通节点应收到 add_user: %d 条", len(cmds))
	}
}
