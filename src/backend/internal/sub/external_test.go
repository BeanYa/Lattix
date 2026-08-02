package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

// newExternalFixture 建一个带外部订阅关联的用户：
// 面板配额 500/已用 300（traffic 表 node_id=0），外部订阅 叠加 total=200 up=100 down=50。
func newExternalFixture(t *testing.T) (*store.Store, *Server, *store.User) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	userID, err := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "alice-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, userID, 500, 0, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x",
		Upload: 100, Download: 50, Total: 200, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(extsub.Node{
		Name: "东京 01", Type: "vless", Server: "1.2.3.4", Port: 443,
		Extra: map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
			"security": "reality", "pbk": "pub", "sid": "abcd",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceExternalChains(ctx, subID, []store.ExternalChain{{
		SubscriptionID: subID, Name: "东京 01", Protocol: "vless",
		Server: "1.2.3.4", Port: 443, Config: cfg, ConfigSHA256: "sha-1",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, userID, []store.UserExternalSubscription{
		{UserID: userID, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	return st, server, user
}

func TestSubscriptionItemsIncludesExternalChains(t *testing.T) {
	st, server, user := newExternalFixture(t)
	items, warnings := server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	if len(items) != 1 || items[0].external == nil {
		t.Fatalf("items = %+v warnings = %v", items, warnings)
	}
	if items[0].external.Name != "东京 01" || items[0].external.Type != "vless" {
		t.Fatalf("external = %+v", items[0].external)
	}

	// 过期/禁用用户不出外部节点。
	user.Expired = true
	items, _ = server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	if len(items) != 0 {
		t.Fatalf("expired user items = %+v", items)
	}
	_ = st
}

func TestSetSubHeadersMergesTraffic(t *testing.T) {
	_, server, user := newExternalFixture(t)
	rec := httptest.NewRecorder()
	server.setSubHeaders(rec, httptest.NewRequest("GET", "/sub/alice-token", nil), user)
	got := rec.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, "upload=100; download=50; total=700") {
		t.Fatalf("userinfo = %q", got)
	}
	if !strings.Contains(got, "reset_day=") {
		t.Fatalf("userinfo missing reset_day: %q", got)
	}
}

func TestCompileNodesAndRenderIncludeExternal(t *testing.T) {
	ctx := context.Background()
	_, server, user := newExternalFixture(t)
	items, _ := server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	compiled, warnings, err := server.compileNodes(ctx, items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 {
		t.Fatalf("compiled = %+v warnings = %v", compiled, warnings)
	}
	if compiled[0].Clash.Type != "vless" || compiled[0].Clash.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("clash = %+v", compiled[0].Clash)
	}
	sb, ok := compiled[0].Singbox.(map[string]any)
	if !ok || sb["type"] != "vless" {
		t.Fatalf("singbox = %+v", compiled[0].Singbox)
	}

	links, err := renderLinks(items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(links)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443") {
		t.Fatalf("links = %s", decoded)
	}
}

func TestHandleSubInfoMergesTraffic(t *testing.T) {
	_, server, _ := newExternalFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sub/alice-token/info", nil)
	req.SetPathValue("token", "alice-token")
	server.HandleSubInfo(rec, req)
	var resp SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.UsedUp != 100 || resp.UsedDown != 50 || resp.TrafficLimit != 700 {
		t.Fatalf("resp = %+v", resp)
	}
}
