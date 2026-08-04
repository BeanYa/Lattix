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
	if len(got.Data.SubToken) != 16 {
		t.Fatalf("sub_token length = %d, want 16", len(got.Data.SubToken))
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

func TestHandleListUsersOnlineConnectionsAccessIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
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
