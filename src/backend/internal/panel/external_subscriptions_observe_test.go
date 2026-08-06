package panel

import (
	"context"
	"net/http"
	"testing"

	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

// subObserveServer 复用外部订阅网络桩（upstream TLS 服务器），并附加观察注册表。
func subObserveServer(t *testing.T) *Server {
	t.Helper()
	server, _ := newExternalSubscriptionTestServer(t)
	server.observes = progress.NewRegistry()
	return server
}

// seedExternalSub 经真实 extsub.Create 落库一条外部订阅（走 stub 网络拉取）。
func seedExternalSub(t *testing.T, srv *Server, ctx context.Context) int64 {
	t.Helper()
	sub, err := srv.extSubs.Create(ctx, "机场", "https://sub.example.com/a?token=1", "", false, true, 12)
	if err != nil {
		t.Fatal(err)
	}
	return sub.ID
}

// assertSubObserveDone 断言 envelope 携带合法 observe_id 且观察已 done、阶段完整。
func assertSubObserveDone(t *testing.T, srv *Server, env groupEnv, kind string, wantStages []string) {
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
	if len(obs.Stages) != len(wantStages) {
		t.Fatalf("stages = %+v, want %d 个阶段", obs.Stages, len(wantStages))
	}
	for i, want := range wantStages {
		if obs.Stages[i].Key != want {
			t.Fatalf("stage[%d].key = %s, want %s", i, obs.Stages[i].Key, want)
		}
	}
}

func TestSyncExternalSubscriptionCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	srv := subObserveServer(t)
	id := seedExternalSub(t, srv, ctx)
	env, rec := postGroupObserve(t, srv, srv.handleSyncExternalSubscription,
		`{"id":`+itoa(id)+`}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("sync status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertSubObserveDone(t, srv, env, "external_subscription.sync",
		[]string{"fetch", "parse", "db", "regenerate"})
}

func TestCreateExternalSubscriptionCarriesObserveID(t *testing.T) {
	srv := subObserveServer(t)
	env, rec := postGroupObserve(t, srv, srv.handleCreateExternalSubscription,
		`{"name":"机场","url":"https://sub.example.com/a?token=1","auto_update":true,"update_interval_hours":12}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertSubObserveDone(t, srv, env, "external_subscription.create",
		[]string{"fetch", "parse", "db"})
}

func TestUpdateExternalSubscriptionCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	srv := subObserveServer(t)
	id := seedExternalSub(t, srv, ctx)
	env, rec := postGroupObserve(t, srv, srv.handleUpdateExternalSubscription,
		`{"id":`+itoa(id)+`,"name":"改名","url":"https://sub.example.com/b?token=2","update_interval_hours":12}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("update status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertSubObserveDone(t, srv, env, "external_subscription.update",
		[]string{"db"})
}

func TestDeleteExternalSubscriptionCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	srv := subObserveServer(t)
	id := seedExternalSub(t, srv, ctx)
	env, rec := postGroupObserve(t, srv, srv.handleDeleteExternalSubscription,
		`{"id":`+itoa(id)+`}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("delete status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertSubObserveDone(t, srv, env, "external_subscription.delete",
		[]string{"db", "regenerate"})
}

func TestRefreshSubscriptionTemplatesCarriesObserveID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	env, rec := postGroupObserve(t, srv, srv.handleRefreshSubscriptionTemplates,
		`{"id":"no-such-github-template"}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("refresh status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertSubObserveDone(t, srv, env, "subscription.template.refresh",
		[]string{"fetch", "parse", "db"})
}
