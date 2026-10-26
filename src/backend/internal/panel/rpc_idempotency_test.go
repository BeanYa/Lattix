package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

const idemTestRoute = "POST /api/idem/test"

type idemHarness struct {
	s     *Server
	st    *store.Store
	calls int
}

func newIdemHarness(t *testing.T) *idemHarness {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &idemHarness{s: &Server{st: st}, st: st}
}

// post 以指定 key 调用被测中间件；handler 行为由各用例注入。
func (h *idemHarness) post(t *testing.T, key, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/idem/test", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.s.requireIdempotency(idemTestRoute, handler)(rec, req)
	return rec
}

func envelopeCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not an RPC envelope: %v\n%s", err, rec.Body.String())
	}
	return envelope.Code
}

// 悬空预留（上次进程中断遗留：只有预留、没有结果）必须被接管并重新执行，
// 而不是把同键重试锁死在 "the previous operation outcome is unknown" 上。
func TestIdempotencyStaleReservationIsTakenOver(t *testing.T) {
	h := newIdemHarness(t)
	ctx := context.Background()
	if err := h.st.ReserveIdempotencyRecord(ctx, "", idemTestRoute, "stale-key-1", "hash-of-dead-request"); err != nil {
		t.Fatal(err)
	}

	calls := 0
	handler := func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(w, http.StatusOK, map[string]string{"n": "1"})
	}
	rec := h.post(t, "stale-key-1", `{"v":1}`, handler)
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("first retry code = %q, want OK（悬空预留应被接管）", code)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}

	// 接管后记录已按本次请求完成：同键重试回放缓存结果，不再执行 handler。
	rec = h.post(t, "stale-key-1", `{"v":1}`, handler)
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("replay code = %q, want OK", code)
	}
	if calls != 1 {
		t.Fatalf("handler calls after replay = %d, want 1", calls)
	}
	record, err := h.st.IdempotencyRecord(ctx, "", idemTestRoute, "stale-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.ResponseJSON == "" {
		t.Fatal("record was not completed after takeover")
	}
}

// 悬空预留即使请求体不同也必须接管：原执行结果已无从追踪，
// 卡死调用方比重新执行一次整体替换型操作更糟。
func TestIdempotencyStaleReservationWithDifferentBodyIsTakenOver(t *testing.T) {
	h := newIdemHarness(t)
	ctx := context.Background()
	if err := h.st.ReserveIdempotencyRecord(ctx, "", idemTestRoute, "stale-key-2", "hash-of-dead-request"); err != nil {
		t.Fatal(err)
	}

	calls := 0
	rec := h.post(t, "stale-key-2", `{"v":2}`, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(w, http.StatusOK, nil)
	})
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("code = %q, want OK（不同请求体的悬空预留也应接管）", code)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

// 失败请求必须释放预留，且释放不依赖请求 context（客户端可能已断开取消）。
func TestIdempotencyFailedRequestReleasesReservation(t *testing.T) {
	h := newIdemHarness(t)

	calls := 0
	reqCtx, cancelReq := context.WithCancel(context.Background())
	// 只有真正的非 200（协议错误）走释放分支；业务错误是 200 信封，会作为结果缓存。
	fail := func(w http.ResponseWriter, _ *http.Request) {
		calls++
		cancelReq() // 模拟 handler 执行中客户端断开
		writeProtocolError(w, http.StatusInternalServerError, "boom")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/idem/test", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "failed-key-1")
	req = req.WithContext(reqCtx)
	rec := httptest.NewRecorder()
	h.s.requireIdempotency(idemTestRoute, fail)(rec, req)
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}

	rec = h.post(t, "failed-key-1", `{}`, fail)
	if calls != 2 {
		t.Fatalf("handler was not re-run after failed attempt (calls = %d)", calls)
	}
	if code := envelopeCode(t, rec); code != "HTTP_500" {
		t.Fatalf("retry code = %q, want HTTP_500", code)
	}
}

// handler panic：先释放预留再向外抛，交给外层恢复中间件。
func TestIdempotencyPanicReleasesReservation(t *testing.T) {
	h := newIdemHarness(t)

	calls := 0
	boom := func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			panic("handler exploded")
		}
		writeJSON(w, http.StatusOK, nil)
	}
	func() {
		defer func() { _ = recover() }()
		h.post(t, "panic-key-1", `{}`, boom)
	}()
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}

	rec := h.post(t, "panic-key-1", `{}`, boom)
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("retry after panic code = %q, want OK", code)
	}
	if calls != 2 {
		t.Fatalf("handler calls after panic = %d, want 2", calls)
	}
}

// 已完成记录的既有语义保持不变：同键同体回放缓存，同键异体冲突。
func TestIdempotencyCompletedRecordReplayAndConflict(t *testing.T) {
	h := newIdemHarness(t)

	var mu sync.Mutex
	calls := 0
	handler := func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"n": "1"})
	}
	rec := h.post(t, "done-key-1", `{"v":1}`, handler)
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("first code = %q, want OK", code)
	}

	rec = h.post(t, "done-key-1", `{"v":1}`, handler)
	if code := envelopeCode(t, rec); code != shared.CodeOK {
		t.Fatalf("replay code = %q, want OK", code)
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("handler calls after replay = %d, want 1", calls)
	}
	mu.Unlock()

	rec = h.post(t, "done-key-1", `{"v":2}`, handler)
	if code := envelopeCode(t, rec); code != shared.CodeConflict {
		t.Fatalf("conflict code = %q, want CONFLICT", code)
	}
}
