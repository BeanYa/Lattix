package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/progress"
	"lattix/shared"
)

// observeTestRecorder 模拟生产中间件的 responseRecorder：observe_id 从请求
// context 读取（observeStart 原地替换 context，包装器持有的同一指针可见）。
type observeTestRecorder struct {
	*httptest.ResponseRecorder
	reg *progress.Registry
	req *http.Request
}

func (w *observeTestRecorder) SetRPCOutcome(code, safeMessage string) {}
func (w *observeTestRecorder) RPCIDs() (string, string)               { return "", "" }
func (w *observeTestRecorder) ObserveID() string {
	return w.reg.ObserveIDFromContext(w.req.Context())
}

// groupEnv 是响应 envelope 中本组测试关心的字段。
type groupEnv struct {
	Code      string `json:"code"`
	ObserveID string `json:"observe_id"`
}

// postGroupObserve 直接调用分组 handler，并携带 envelope 观察注入能力。
func postGroupObserve(t *testing.T, srv *Server, h func(http.ResponseWriter, *http.Request), body string) (groupEnv, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/group", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := &observeTestRecorder{ResponseRecorder: rec, reg: srv.observes, req: req}
	h(w, req)
	var env groupEnv
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v, body=%s", err, rec.Body.String())
	}
	return env, rec
}

// assertObserveDone 断言 envelope 携带合法 observe_id 且观察已 done、阶段完整。
func assertObserveDone(t *testing.T, srv *Server, env groupEnv, kind string) {
	t.Helper()
	if env.ObserveID == "" {
		t.Fatalf("envelope 未携带 observe_id: %+v", env)
	}
	if !shared.ValidMessageID(env.ObserveID) {
		t.Fatalf("observe_id 非法: %q", env.ObserveID)
	}
	obs, ok := srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 不存在", env.ObserveID)
	}
	if obs.Kind != kind {
		t.Fatalf("观察 kind = %s, want %s", obs.Kind, kind)
	}
	if obs.Status != progress.StatusDone {
		t.Fatalf("观察状态 = %s, want %s (obs = %+v)", obs.Status, progress.StatusDone, obs)
	}
	if len(obs.Stages) != 1 || obs.Stages[0].Key != "db" {
		t.Fatalf("stages = %+v, want only db", obs.Stages)
	}
}

func TestCreateLinkGroupCarriesObserveID(t *testing.T) {
	st, chainA, _, _, _, _ := groupsFixture(t)
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleCreateLinkGroup,
		`{"name":"链路组","chain_ids":[`+itoa(chainA)+`]}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveDone(t, srv, env, "link_group.create")
}

func TestUpdateLinkGroupCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, _, _ := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleUpdateLinkGroup,
		`{"id":`+itoa(lgID)+`,"name":"改后组","chain_ids":[`+itoa(chainA)+`]}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("update status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveDone(t, srv, env, "link_group.update")
}

func TestDeleteLinkGroupCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, _, _ := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleDeleteLinkGroup, `{"id":`+itoa(lgID)+`}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("delete status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveDone(t, srv, env, "link_group.delete")
}
