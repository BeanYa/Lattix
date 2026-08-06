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
	if len(obs.Stages) != 3 {
		t.Fatalf("stages = %+v, want 3 个阶段", obs.Stages)
	}
	for i, want := range []string{"db", "reconcile", "regenerate"} {
		if obs.Stages[i].Key != want {
			t.Fatalf("stage[%d].key = %s, want %s", i, obs.Stages[i].Key, want)
		}
	}
}

// assertObserveAwaitingRegenerate 断言观察处于 running 的 regenerate 阶段（0%），
// 随后逐个 NotifyUserPublished 驱动收口：全部 watched 用户处理完后观察自动 done。
// watched 用户集由测试按 fixture/请求内容推导。
func assertObserveAwaitingRegenerate(t *testing.T, srv *Server, env groupEnv, kind string, watched []int64) {
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
	if obs.Status != progress.StatusRunning {
		t.Fatalf("handler 返回后观察应为 running（等待重生成回调）, status = %s (obs = %+v)", obs.Status, obs)
	}
	if obs.Stage != "regenerate" || obs.Percent != 0 {
		t.Fatalf("观察应停在 regenerate 0%%: stage = %s percent = %d", obs.Stage, obs.Percent)
	}
	for _, id := range watched {
		srv.observes.NotifyUserPublished(id, nil)
	}
	obs, ok = srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 在通知后消失", env.ObserveID)
	}
	if obs.Status != progress.StatusDone {
		t.Fatalf("全部 watched 用户通知后观察应 done, status = %s (obs = %+v)", obs.Status, obs)
	}
	if obs.Percent != 100 || obs.Stage != "regenerate" {
		t.Fatalf("完成观察 percent/stage = %d/%s, want 100/regenerate", obs.Percent, obs.Stage)
	}
}

func TestCreateUserGroupCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, u2 := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleCreateUserGroup,
		`{"name":"青铜会员","user_ids":[`+itoa(u1)+`,`+itoa(u2)+`],"link_group_ids":[`+itoa(lgID)+`]}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user_group.create", []int64{u1, u2})
}

func TestUpdateUserGroupCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, u2 := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ugID, err := st.CreateUserGroup(ctx, "青铜会员", []int64{u1}, []int64{lgID})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleUpdateUserGroup,
		`{"id":`+itoa(ugID)+`,"name":"白银会员","user_ids":[`+itoa(u2)+`],"link_group_ids":[`+itoa(lgID)+`]}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("update status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	// 新旧成员并集都会被重发布：before=[u1] ∪ new=[u2]
	assertObserveAwaitingRegenerate(t, srv, env, "user_group.update", []int64{u1, u2})
}

func TestDeleteUserGroupCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, _ := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ugID, err := st.CreateUserGroup(ctx, "青铜会员", []int64{u1}, []int64{lgID})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleDeleteUserGroup, `{"id":`+itoa(ugID)+`}`)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("delete status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user_group.delete", []int64{u1})
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

func TestUserGroupValidationFailureNoObserve(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, u1, _ := groupsFixture(t)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleCreateUserGroup,
		`{"name":"青铜会员","user_ids":[`+itoa(u1)+`],"link_group_ids":[`+itoa(lgID)+`]}`)
	if env.Code != "OK" {
		t.Fatalf("首建应成功, code = %q body = %s", env.Code, rec.Body.String())
	}
	if env.ObserveID == "" {
		t.Fatalf("成功创建应携带 observe_id")
	}
	env2, _ := postGroupObserve(t, srv, srv.handleCreateUserGroup, `{"name":"青铜会员"}`)
	if env2.Code != "INVALID_ARGUMENT" {
		t.Fatalf("重名应拒绝, code = %q", env2.Code)
	}
	if env2.ObserveID != "" {
		t.Fatalf("校验失败不应创建观察, observe_id = %q", env2.ObserveID)
	}
}
