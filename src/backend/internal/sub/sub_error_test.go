package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

// TestSubEndpointsHideInternalErrors 订阅公开端点的错误回显收敛（安全评审 L3）：
// 内部错误（DB 失败）对外只返回通用文案、不泄露细节；业务错误（4xx）文案保持不变。
func TestSubEndpointsHideInternalErrors(t *testing.T) {
	ctx := context.Background()
	newMux := func(server *Server) *http.ServeMux {
		mux := http.NewServeMux()
		mux.Handle("GET /sub/{token}", server)
		mux.HandleFunc("GET /sub/{token}/rules/{version}/{format}/{name}", server.ServeRuleHTTP)
		mux.HandleFunc("GET /api/sub/{token}/info", server.HandleSubInfo)
		mux.HandleFunc("GET /api/sub/{token}/clients", server.HandleSubClients)
		mux.HandleFunc("GET /api/sub/{token}/status", server.HandleSubStatus)
		mux.HandleFunc("GET /api/sub/{token}/history", server.HandleSubHistory)
		return mux
	}
	rulePath := "/sub/err-token/rules/" + strings.Repeat("a", 64) + "/mihomo/sample"

	// 内部错误：DB 关闭后查询必然失败，响应体只能是通用文案。
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	mux := newMux(server)
	st.Close()
	for _, target := range []string{"/sub/err-token?format=clash", rulePath,
		"/api/sub/err-token/info", "/api/sub/err-token/clients",
		"/api/sub/err-token/status", "/api/sub/err-token/history"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "internal error\n\n" {
			t.Fatalf("%s: status = %d body = %q", target, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "database is closed") {
			t.Fatalf("%s: 响应体泄露内部错误: %q", target, rec.Body.String())
		}
	}
	// 客户端下载端点的内部错误同样收敛（经 subDownloadUser → subDownloadError）。
	rec := httptest.NewRecorder()
	downloadReq := httptest.NewRequest("GET", "/api/sub/err-token/client-download/start?variant=flclash-android-arm64", nil)
	downloadReq.SetPathValue("token", "err-token")
	server.HandleSubClientDownloadStart(rec, downloadReq)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "internal error\n\n" {
		t.Fatalf("download start internal error: status = %d body = %q", rec.Code, rec.Body.String())
	}

	// 业务错误：状态码与回显文案保持原样。
	st2, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := st2.InsertUser(ctx, "err", "00000000-0000-0000-0000-0000000000e9", "err-token", nil); err != nil {
		t.Fatal(err)
	}
	server2 := New(st2, nil, nil)
	mux2 := newMux(server2)

	// 订阅不存在 → 404 store.ErrNotFound 原文。
	for _, target := range []string{"/sub/no-such-token?format=clash", strings.Replace(rulePath, "err-token", "no-such-token", 1),
		"/api/sub/no-such-token/info", "/api/sub/no-such-token/clients",
		"/api/sub/no-such-token/status", "/api/sub/no-such-token/history"} {
		rec := httptest.NewRecorder()
		mux2.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		if rec.Code != http.StatusNotFound || rec.Body.String() != "store: not found\n\n" {
			t.Fatalf("%s: status = %d body = %q", target, rec.Code, rec.Body.String())
		}
	}
	// 规则产物路径非法 → 400 原文。
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest("GET", "/sub/err-token/rules/nothex/mihomo/sample", nil))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "invalid rule artifact path\n\n" {
		t.Fatalf("invalid rule path: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 规则产物不存在 → 404 store.ErrNotFound 原文。
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest("GET", rulePath, nil))
	if rec.Code != http.StatusNotFound || rec.Body.String() != "store: not found\n\n" {
		t.Fatalf("missing rule artifact: status = %d body = %q", rec.Code, rec.Body.String())
	}
	// 下载端点订阅不存在 → 400 中文原文。
	rec = httptest.NewRecorder()
	missingReq := httptest.NewRequest("GET", "/api/sub/no-such-token/client-download/start?variant=flclash-android-arm64", nil)
	missingReq.SetPathValue("token", "no-such-token")
	server2.HandleSubClientDownloadStart(rec, missingReq)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "订阅不存在\n\n" {
		t.Fatalf("download start not found: status = %d body = %q", rec.Code, rec.Body.String())
	}
}
