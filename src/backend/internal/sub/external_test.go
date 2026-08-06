package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	var resp SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.UsedUp != 100 || resp.UsedDown != 50 || resp.TrafficLimit != 700 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.NodesCount != 1 {
		t.Fatalf("resp.NodesCount = %d, want 1 (external node)", resp.NodesCount)
	}
}

// TestUnlimitedUserIgnoresExternalSubscriptionTraffic 无限流量 + 长期用户
// （TrafficLimit=0、ExpiresAt=nil）不合并任何外部订阅数据：订阅保持「无限流量/长期」，
// 外部订阅的额度与到期不得出现在用户自己的订阅状态里。
func TestUnlimitedUserIgnoresExternalSubscriptionTraffic(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	userID, err := st.InsertUser(ctx, "carol", "00000000-0000-0000-0000-0000000000cc", "carol-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x",
		Upload: 100, Download: 50, Total: 200, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(24 * time.Hour).Unix()
	sub, err := st.ExternalSubscriptionByID(ctx, subID)
	if err != nil {
		t.Fatal(err)
	}
	sub.Expire = &future
	if err := st.UpdateExternalSubscription(ctx, sub); err != nil {
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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sub/carol-token/info", nil)
	req.SetPathValue("token", "carol-token")
	server.HandleSubInfo(rec, req)
	var resp SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TrafficLimit != 0 || resp.ExpiresAt != nil || resp.UsedUp != 0 || resp.UsedDown != 0 {
		t.Fatalf("unlimited user resp = %+v, want traffic_limit=0 / no expiry / panel-only usage", resp)
	}

	rec = httptest.NewRecorder()
	server.setSubHeaders(rec, httptest.NewRequest("GET", "/sub/carol-token", nil), user)
	got := rec.Header().Get("Subscription-Userinfo")
	if strings.Contains(got, "total=") || strings.Contains(got, "expire=") {
		t.Fatalf("unlimited user userinfo must not carry external total/expire: %q", got)
	}
}

// TestSetSubHeadersSkipsExpiredStackSub 叠加订阅到期后不再参与合并：
// 额度与已用一并移出（恢复管理员设定的配额视图），到期也不写入 userinfo。
func TestSetSubHeadersSkipsExpiredStackSub(t *testing.T) {
	ctx := context.Background()
	st, server, user := newExternalFixture(t)
	sub, err := st.ExternalSubscriptionByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).Unix()
	sub.Expire = &past
	if err := st.UpdateExternalSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	server.setSubHeaders(rec, httptest.NewRequest("GET", "/sub/alice-token", nil), user)
	got := rec.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, "total=500") {
		t.Fatalf("expired stack sub must not add total, userinfo = %q", got)
	}
	if strings.Contains(got, "expire=") {
		t.Fatalf("expired stack sub must not add expire, userinfo = %q", got)
	}
}

// TestSetSubHeadersKeepsPanelExpiryOnly 外部订阅到期（即使早于面板）不合并：
// userinfo 的 expire 只反映面板自身到期。
func TestSetSubHeadersKeepsPanelExpiryOnly(t *testing.T) {
	ctx := context.Background()
	st, server, user := newExternalFixture(t)
	sub, err := st.ExternalSubscriptionByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	panelExpire := now.Add(30 * 24 * time.Hour)
	subExpire := now.Add(24 * time.Hour).Unix()
	sub.Expire = &subExpire
	if err := st.UpdateExternalSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExpiry(ctx, user.ID, &panelExpire, now); err != nil {
		t.Fatal(err)
	}
	user, err = st.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	server.setSubHeaders(rec, httptest.NewRequest("GET", "/sub/alice-token", nil), user)
	got := rec.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, fmt.Sprintf("expire=%d", panelExpire.Unix())) {
		t.Fatalf("expire must be panel's own expiry, userinfo = %q", got)
	}
	if strings.Contains(got, fmt.Sprintf("expire=%d", subExpire)) {
		t.Fatalf("external expire must not be merged, userinfo = %q", got)
	}
}
