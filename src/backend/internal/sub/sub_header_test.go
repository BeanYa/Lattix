package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestDaysUntilReset(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name      string
		resetDay  int
		createdAt time.Time
		now       time.Time
		want      int
	}{
		{"本月重置日未到", 14, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 15},
		{"当天即重置日→计入下月", 30, time.Date(2026, 1, 30, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 31},
		{"31 日在短月份取月末", 31, time.Date(2026, 1, 31, 0, 0, 0, 0, loc), time.Date(2026, 2, 1, 10, 0, 0, 0, loc), 27},
		{"31 日跨入二月", 31, time.Date(2026, 1, 31, 0, 0, 0, 0, loc), time.Date(2026, 1, 31, 10, 0, 0, 0, loc), 28},
		{"reset_day=0 取创建日", 0, time.Date(2026, 3, 5, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 6},
		{"跨年", 10, time.Date(2025, 1, 10, 0, 0, 0, 0, loc), time.Date(2026, 12, 20, 0, 0, 0, 0, loc), 21},
	}
	for _, c := range cases {
		u := &store.User{TrafficResetDay: c.resetDay, CreatedAt: c.createdAt}
		if got := daysUntilReset(u, c.now); got != c.want {
			t.Errorf("%s: daysUntilReset = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestRfc5987Encode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"VIP1-alice.yaml", "VIP1-alice.yaml"},
		{"中文 套餐.yaml", "%E4%B8%AD%E6%96%87%20%E5%A5%97%E9%A4%90.yaml"},
		{"a.b!c_d-e+f", "a.b!c_d-e+f"},
	}
	for _, c := range cases {
		if got := rfc5987Encode(c.in); got != c.want {
			t.Errorf("rfc5987Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeSubName(t *testing.T) {
	if got := sanitizeSubName("VIP1\n\r"); got != "VIP1" {
		t.Errorf("control chars not stripped: %q", got)
	}
	long := strings.Repeat("名", 60)
	if got := sanitizeSubName(long); len([]rune(got)) != 40 {
		t.Errorf("sanitizeSubName length = %d, want 40", len([]rune(got)))
	}
}

func TestSubContentDisposition(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "alice-token", nil)
	if err := st.SetUserSubSettings(ctx, userID, 0, 0, "", "", "VIP1", ""); err != nil {
		t.Fatal(err)
	}
	user, _ := st.UserByID(ctx, userID)
	server := New(st, nil, nil)
	r := httptest.NewRequest("GET", "/sub/alice-token", nil)

	if got := server.subContentDisposition(r, user, "yaml"); got != `attachment; filename="VIP1-alice.yaml"; filename*=UTF-8''VIP1-alice.yaml` {
		t.Errorf("user plan disposition = %q", got)
	}

	if err := st.SetUserSubSettings(ctx, userID, 0, 0, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	user, _ = st.UserByID(ctx, userID)
	if got := server.subContentDisposition(r, user, "yaml"); got != `attachment; filename="Lattix-alice.yaml"; filename*=UTF-8''Lattix-alice.yaml` {
		t.Errorf("default disposition = %q", got)
	}

	if err := st.SetSetting(ctx, store.SettingSubPlanName, "全球"); err != nil {
		t.Fatal(err)
	}
	user, _ = st.UserByID(ctx, userID)
	got := server.subContentDisposition(r, user, "yaml")
	if !strings.Contains(got, "filename*=UTF-8''%E5%85%A8%E7%90%83-alice.yaml") {
		t.Errorf("global plan disposition missing encoded name: %q", got)
	}
	if strings.Contains(got, `filename="全球`) {
		t.Errorf("plain filename param must stay ASCII: %q", got)
	}

	bobID, _ := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "bob-token", nil)
	if err := st.SetUserSubSettings(ctx, bobID, 0, 0, "", "", "VIP1", ""); err != nil {
		t.Fatal(err)
	}
	bob, _ := st.UserByID(ctx, bobID)
	if got := server.subContentDisposition(r, bob, "yaml"); !strings.Contains(got, "VIP1-bob.yaml") {
		t.Errorf("same plan users must not share filename: %q", got)
	}

	dirty, _ := st.InsertUser(ctx, "eve\nx", "00000000-0000-0000-0000-0000000000ee", "eve-token", nil)
	if err := st.SetUserSubSettings(ctx, dirty, 0, 0, "", "", "A;B\nC", ""); err != nil {
		t.Fatal(err)
	}
	dirtyUser, _ := st.UserByID(ctx, dirty)
	got = server.subContentDisposition(r, dirtyUser, "yaml")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("disposition contains control chars: %q", got)
	}
}

func TestServeHTTPContentDisposition(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000cc", "alice-token", nil)
	if err := st.SetUserSubSettings(ctx, userID, 0, 0, "", "", "VIP1", ""); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, []byte("<html></html>"))
	if _, err := server.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /sub/{token}", server)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/sub/alice-token?format=clash", nil))
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="VIP1-alice.yaml"; filename*=UTF-8''VIP1-alice.yaml` {
		t.Errorf("clash disposition = %q", got)
	}
	if !strings.Contains(rec.Header().Get("Subscription-Userinfo"), "plan_name=VIP1") {
		t.Errorf("subscription-userinfo missing plan_name: %q", rec.Header().Get("Subscription-Userinfo"))
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/sub/alice-token?format=links", nil))
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "VIP1-alice.txt") {
		t.Errorf("links disposition = %q", got)
	}

	rec = httptest.NewRecorder()
	browserReq := httptest.NewRequest("GET", "/sub/alice-token", nil)
	browserReq.Header.Set("User-Agent", "Mozilla/5.0")
	browserReq.Header.Set("Accept", "text/html")
	mux.ServeHTTP(rec, browserReq)
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("browser landing must not set Content-Disposition: %q", got)
	}
}
