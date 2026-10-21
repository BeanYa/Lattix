package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

// TestServeHTTPRejectsExpiredAndScheduled 订阅更新的到期拦截（§9）：
// 已到期（含试用流量用尽）与尚未生效的用户拒绝订阅内容（403，旧链接无法再更新），
// 浏览器状态页与 info API 保持可见；生效用户不受影响（未发布快照走 503 路径）。
func TestServeHTTPRejectsExpiredAndScheduled(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	if _, err := st.InsertUser(ctx, "active", "a-uuid", "tok-active", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUser(ctx, "expired", "e-uuid", "tok-expired", &past); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUserFull(ctx, store.User{
		Name: "later", UUID: "l-uuid", SubToken: "tok-later", EffectiveAt: &future, IsTrial: true,
	}); err != nil {
		t.Fatal(err)
	}
	quotaID, err := st.InsertUserFull(ctx, store.User{
		Name: "quota", UUID: "q-uuid", SubToken: "tok-quota", IsTrial: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExpired(ctx, quotaID, true); err != nil {
		t.Fatal(err)
	}
	quotaStopID, err := st.InsertUserFull(ctx, store.User{
		Name: "quota-stop", UUID: "qs-uuid", SubToken: "tok-quota-stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserQuotaExhausted(ctx, quotaStopID, true); err != nil {
		t.Fatal(err)
	}

	server := New(st, nil, nil)
	server.spaHTML = []byte("<html>spa</html>")
	serve := func(token, format string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/sub/"+token, nil)
		req.SetPathValue("token", token)
		req.URL.RawQuery = "format=" + format
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	// 到期用户：订阅内容 403（客户端无法再经旧链接更新）。
	if rec := serve("tok-expired", "clash"); rec.Code != http.StatusForbidden || rec.Body.String() != "subscription expired\n\n" {
		t.Fatalf("expired: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 未生效用户：同样 403。
	if rec := serve("tok-later", "links"); rec.Code != http.StatusForbidden || rec.Body.String() != "subscription not yet effective\n\n" {
		t.Fatalf("not yet effective: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 试用流量用尽（expired 标记）：同样 403。
	if rec := serve("tok-quota", "singbox"); rec.Code != http.StatusForbidden {
		t.Fatalf("quota exhausted: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 流量配额停权（quota_exhausted，试用/普通一致）：403 且文案区分。
	if rec := serve("tok-quota-stop", "clash"); rec.Code != http.StatusForbidden || rec.Body.String() != "subscription quota exhausted\n\n" {
		t.Fatalf("quota stop: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 浏览器状态页不受限（可查看到期状态）。
	if rec := serve("tok-expired", "browser"); rec.Code != http.StatusOK {
		t.Fatalf("browser status page: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 生效用户未被误伤：内容路径正常走到快照逻辑（未发布 → 503 + Retry-After）。
	rec := serve("tok-active", "clash")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("active: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("active: 缺少 Retry-After 头")
	}

	// info API 对未生效用户仍可见并标注 not_effective。
	infoReq := httptest.NewRequest("GET", "/api/sub/tok-later/info", nil)
	infoReq.SetPathValue("token", "tok-later")
	infoRec := httptest.NewRecorder()
	server.HandleSubInfo(infoRec, infoReq)
	if infoRec.Code != http.StatusOK {
		t.Fatalf("info: status = %d", infoRec.Code)
	}
}
