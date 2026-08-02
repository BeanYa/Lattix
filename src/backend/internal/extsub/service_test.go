package extsub

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

const testLink = "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Pbk&sid=abcd&sni=cdn.example.com&fp=chrome#Tokyo"

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	client := requester.ExternalFileRequester{Doer: &http.Client{Timeout: 5 * time.Second}}
	return New(st, client, client)
}

// newTestServiceTo 将所有连接拨号到 addr（httptest 服务器地址），
// 使通过 validateSubscriptionURL 校验的外部主机名也能命中本地测试服务器。
func newTestServiceTo(t *testing.T, addr string) *Service {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	client := requester.ExternalFileRequester{Doer: &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}}
	return New(st, client, client)
}

func TestValidateSubscriptionURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://sub.example.com/a?token=1", true},
		{"http://sub.example.com/a", false},
		{"https://localhost/a", false},
		{"https://127.0.0.1/a", false},
		{"https://192.168.1.1/a", false},
		{"https://10.0.0.1/a", false},
		{"https://172.16.0.1/a", false},
		{"https://169.254.169.254/a", false},
		{"https://foo.internal/a", false},
		{"https://foo.local/a", false},
		{"https://[::1]/a", false},
		{"https://100.64.0.1/a", false},
		{"https://100.127.255.254/a", false},
		{"https://192.0.2.1/a", false},
		{"https://198.51.100.1/a", false},
		{"https://203.0.113.1/a", false},
		{"https://198.18.0.1/a", false},
		{"https://8.8.8.8/a", true},
		{"not a url", false},
		{"https://", false},
	}
	for _, c := range cases {
		err := validateSubscriptionURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("validateSubscriptionURL(%q) err = %v, want ok=%v", c.url, err, c.want)
		}
	}
}

func TestParseTrafficUserinfo(t *testing.T) {
	upload, download, total, expire := parseTrafficUserinfo(
		"upload=1024.5; download=2048; total=1073741824; expire=1700000000")
	if upload != 1024 || download != 2048 || total != 1073741824 {
		t.Fatalf("traffic = %d %d %d", upload, download, total)
	}
	if expire == nil || *expire != 1700000000 {
		t.Fatalf("expire = %v", expire)
	}
	_, _, _, expire = parseTrafficUserinfo("expire=0")
	if expire != nil {
		t.Fatalf("expire=0 should be ignored, got %v", *expire)
	}
	_, _, _, expire = parseTrafficUserinfo("total=5")
	if expire != nil {
		t.Fatalf("missing expire should stay nil")
	}
}

func TestCreateSyncsAndStoresChains(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=100; download=200; total=1000; expire=1900000000")
		body := base64.StdEncoding.EncodeToString([]byte(testLink + "\n" + testLink + "\n"))
		w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := newTestServiceTo(t, srv.Listener.Addr().String())
	ctx := context.Background()
	sub, err := svc.Create(ctx, "测试机场", "https://sub.example.com/a?token=1", "", false, true, 24)
	if err != nil {
		t.Fatal(err)
	}
	if sub.NodeCount != 1 || sub.Format != "v2ray" {
		t.Fatalf("sub = %+v", sub)
	}
	if sub.Upload != 100 || sub.Download != 200 || sub.Total != 1000 {
		t.Fatalf("traffic = %+v", sub)
	}
	if sub.Expire == nil || *sub.Expire != 1900000000 {
		t.Fatalf("expire = %v", sub.Expire)
	}
	if sub.LastSyncAt == nil || sub.LastError != "" {
		t.Fatalf("sync fields = %+v", sub)
	}
	chains, err := svc.st.ListExternalChains(ctx, sub.ID)
	if err != nil || len(chains) != 1 {
		t.Fatalf("chains = %+v err %v", chains, err)
	}
	if chains[0].Name != "Tokyo" || chains[0].Protocol != "vless" || chains[0].Server != "example.com" {
		t.Fatalf("chain = %+v", chains[0])
	}
}

func TestCreateDuplicateURLCallsUpdate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(testLink))
	}))
	defer srv.Close()

	svc := newTestServiceTo(t, srv.Listener.Addr().String())
	ctx := context.Background()
	first, err := svc.Create(ctx, "第一次", "https://sub.example.com/a?token=1", "", false, true, 24)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, "第二次", "https://sub.example.com/a?token=1", "", false, false, 12)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %d vs %d", first.ID, second.ID)
	}
	if second.Name != "第二次" || second.AutoUpdate {
		t.Fatalf("second = %+v", second)
	}
	all, err := svc.st.ListExternalSubscriptions(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %v err %v", all, err)
	}
}

func TestCreateKeepsRecordWhenFetchFails(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sub, err := svc.Create(ctx, "坏订阅", "https://sub.invalid.example/x", "", false, true, 24)
	if err == nil {
		t.Fatal("create should surface fetch error")
	}
	got, getErr := svc.st.ExternalSubscriptionByID(ctx, sub.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Name != "坏订阅" || got.LastError == "" {
		t.Fatalf("record kept without error = %+v", got)
	}
}

func TestSyncDueOnlySyncsDueSubscriptions(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Write([]byte(testLink))
	}))
	defer srv.Close()

	svc := newTestServiceTo(t, srv.Listener.Addr().String())
	ctx := context.Background()
	auto, err := svc.Create(ctx, "自动", "https://sub.example.com/a?token=1", "", false, true, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 手动订阅同样立即同步一次（dial-all 直达本地测试服务器），也会计入 hits
	if _, err := svc.Create(ctx, "手动", "https://sub.manual.example/x", "", false, false, 1); err != nil {
		t.Fatal(err)
	}
	// 回拨 last_attempt_at，使自动订阅到期（Create 刚同步过，否则 SyncDue 会跳过）
	back := time.Now().UTC().Add(-2 * time.Hour)
	auto.LastAttemptAt = &back
	if err := svc.st.UpdateExternalSubscription(ctx, auto); err != nil {
		t.Fatal(err)
	}

	hitsBefore := hits
	synced, err := svc.SyncDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hits != hitsBefore+1 {
		t.Fatalf("SyncDue hits = %d (before %d), want exactly one more", hits, hitsBefore)
	}
	if len(synced) != 1 || synced[0] != auto.ID {
		t.Fatalf("synced = %v, want [%d]", synced, auto.ID)
	}
}

func TestUpdateRejectsBadURL(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "n", "https://ok.example.com/x", "", false, true, 24); err == nil {
		t.Fatal("unreachable fetch should fail")
	}
	subs, err := svc.st.ListExternalSubscriptions(ctx)
	if err != nil || len(subs) != 1 {
		t.Fatalf("subs = %v err %v", subs, err)
	}
	if _, err := svc.Update(ctx, subs[0].ID, "n", "http://bad.example.com/x", "", false, true, 24); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("update err = %v", err)
	}
}
