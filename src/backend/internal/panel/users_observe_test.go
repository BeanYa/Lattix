package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// 用户/模板指派类操作的旁路观察测试：发布已转异步，handler 返回后观察停在
// regenerate 阶段等待 regenerator 完成回调（NotifyUserPublished 驱动收口）。
// 观察注入复用 groups_observe_test.go 的 observeTestRecorder / postGroupObserve /
// assertObserveAwaitingRegenerate。

// createUserObserve 调用创建用户 handler 并返回 envelope、新用户 id。
func createUserObserve(t *testing.T, srv *Server, body string) (groupEnv, int64) {
	t.Helper()
	env, rec := postGroupObserve(t, srv, srv.handleCreateUser, body)
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("create status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if resp.Data.ID <= 0 {
		t.Fatalf("create 未返回用户 id: %s", rec.Body.String())
	}
	return env, resp.Data.ID
}

func TestCreateUserCarriesObserveID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	env, userID := createUserObserve(t, srv, `{"name":"alice"}`)
	assertObserveAwaitingRegenerate(t, srv, env, "user.create", []int64{userID})
}

func TestUpdateUserSubSettingsCarriesObserveID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	userID, _ := st.InsertUser(context.Background(), "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	env, rec := postGroupObserve(t, srv, srv.handleUpdateUserSubSettings, fmt.Sprintf(
		`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","expires_at":null}`, userID))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("sub-settings status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user.sub_settings", []int64{userID})
}

func TestSetUserNodesCarriesObserveID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	userID, _ := st.InsertUser(context.Background(), "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	env, rec := postGroupObserve(t, srv, srv.handleSetUserNodes, fmt.Sprintf(
		`{"user_id":%d,"node_ids":[],"chain_ids":[]}`, userID))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("set-nodes status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user.set_nodes", []int64{userID})
}

func TestSetUserExternalSubscriptionsCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	userID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	env, rec := postGroupObserve(t, srv, srv.handleSetUserExternalSubscriptions, fmt.Sprintf(
		`{"user_id":%d,"items":[{"subscription_id":%d,"mode":"stack"}]}`, userID, subID))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("set-external-subscriptions status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user.set_external_subscriptions", []int64{userID})
}

// 停权跃迁触发重发布 → 携带观察；无跃迁的变更（如仅改有效期）不携带观察。
func TestUpdateUserDisableTransitionObserved(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	userID, _ := st.InsertUser(context.Background(), "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)

	env, rec := postGroupObserve(t, srv, srv.handleUpdateUser, fmt.Sprintf(
		`{"user_id":%d,"disabled":true}`, userID))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("disable status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user.update", []int64{userID})

	// 已停权再停权：无跃迁、无重发布，envelope 不带 observe_id。
	env2, rec2 := postGroupObserve(t, srv, srv.handleUpdateUser, fmt.Sprintf(
		`{"user_id":%d,"disabled":true}`, userID))
	if rec2.Code != http.StatusOK || env2.Code != "OK" {
		t.Fatalf("re-disable status = %d code = %q body = %s", rec2.Code, env2.Code, rec2.Body.String())
	}
	if env2.ObserveID != "" {
		t.Fatalf("无停权跃迁不应携带 observe_id: %q", env2.ObserveID)
	}
}

func TestRegenerateUserSubscriptionCarriesObserveID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	userID, _ := st.InsertUser(context.Background(), "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	env, rec := postGroupObserve(t, srv, srv.handleRegenerateUserSubscription, fmt.Sprintf(
		`{"user_id":%d}`, userID))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("regenerate status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "user.regenerate", []int64{userID})
}

func TestAssignSubscriptionTemplateCarriesObserveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	u1, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000010", "tok-a", nil)
	u2, _ := st.InsertUser(ctx, "b", "00000000-0000-0000-0000-000000000011", "tok-b", nil)
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}

	env, rec := postGroupObserve(t, srv, srv.handleAssignSubscriptionTemplate, fmt.Sprintf(
		`{"user_ids":[%d,%d],"template_id":"tpl-portable","forced":true}`, u1, u2))
	if rec.Code != http.StatusOK || env.Code != "OK" {
		t.Fatalf("assign status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env, "subscription.template.assign", []int64{u1, u2})

	env2, rec2 := postGroupObserve(t, srv, srv.handleUnassignSubscriptionTemplate, fmt.Sprintf(
		`{"user_ids":[%d,%d],"template_id":"tpl-portable"}`, u1, u2))
	if rec2.Code != http.StatusOK || env2.Code != "OK" {
		t.Fatalf("unassign status = %d code = %q body = %s", rec2.Code, env2.Code, rec2.Body.String())
	}
	assertObserveAwaitingRegenerate(t, srv, env2, "subscription.template.unassign", []int64{u1, u2})
}

// 发布失败经完成回调计入观察警告并收口（前端弹窗呈现警告、不自动关闭）。
func TestCreateUserObservePublishFailureWarns(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil), observes: progress.NewRegistry()}
	env, userID := createUserObserve(t, srv, `{"name":"alice"}`)
	if env.ObserveID == "" {
		t.Fatalf("envelope 未携带 observe_id")
	}
	srv.observes.NotifyUserPublished(userID, context.DeadlineExceeded)
	obs, ok := srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 不存在", env.ObserveID)
	}
	if obs.Status != progress.StatusDone {
		t.Fatalf("观察状态 = %s, want done", obs.Status)
	}
	if len(obs.Warnings) == 0 {
		t.Fatalf("发布失败应计入警告, obs = %+v", obs)
	}
}
