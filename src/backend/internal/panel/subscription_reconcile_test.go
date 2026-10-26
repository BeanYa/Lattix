package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// downloadSubscription 以指定 token 拉取 clash 订阅，返回其中定义的策略组名集合。
func downloadSubscription(t *testing.T, srv *Server, token string) map[string]bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/"+token+"?format=clash", nil)
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	srv.subscriptions.ServeHTTP(w, r)
	var config struct {
		ProxyGroups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatalf("parse delivered clash config: %v\n%s", err, w.Body.String())
	}
	names := make(map[string]bool, len(config.ProxyGroups))
	for _, group := range config.ProxyGroups {
		names[group.Name] = true
	}
	return names
}

// 场景复现（线上事故）：用户在旧版本切换地区分组开关后，重生成任务随进程重启
// 丢失（内存队列），交付快照停留在旧内容上。新版本启动的版本对账必须补一次
// 全员重发布，使交付内容由当前二进制的生成逻辑重新产出。
func TestStartupReconcileRepublishesStaleDeliveries(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, userID, _ := groupsFixture(t)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainA}); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.New(st, nil, nil).PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingSubscriptionReconcileVersion, "v0.1.15"); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, cfg: Config{Version: "v0.1.16", PublicURL: "https://panel.example"}, subscriptions: sub.New(st, nil, nil)}
	if !downloadSubscription(t, srv, "tok-a")["🇯🇵 JP"] {
		t.Fatalf("expected delivered snapshot to contain region groups before reconcile")
	}

	// 开关切换直达 DB（模拟触发丢失）：交付内容不应永久滞后于订阅设置。
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.DisableRegionGroups = true
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	srv.reconcileSubscriptionDeliveries(ctx)
	if downloadSubscription(t, srv, "tok-a")["🇯🇵 JP"] {
		t.Fatalf("reconcile failed to regenerate stale delivery")
	}

	// 同版本再次启动：标记命中，跳过重发布（重开开关后交付内容保持旧状态）。
	profile, err = st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.DisableRegionGroups = false
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	srv.reconcileSubscriptionDeliveries(ctx)
	if downloadSubscription(t, srv, "tok-a")["🇯🇵 JP"] {
		t.Fatalf("same-version restart should skip reconcile")
	}
}

// 生成处于错误状态的用户每次启动无条件重发布：底层故障修复后重启即自愈。
func TestStartupReconcileRetriesGenerationErrors(t *testing.T) {
	ctx := context.Background()
	st, _, _, _, userID, _ := groupsFixture(t)
	if err := st.SetSubscriptionGenerationError(ctx, userID, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingSubscriptionReconcileVersion, "v0.1.15"); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, cfg: Config{Version: "v0.1.15", PublicURL: "https://panel.example"}, subscriptions: sub.New(st, nil, nil)}
	srv.reconcileSubscriptionDeliveries(ctx)
	// 入队后经订阅下载触发重发布（生产由后台 worker 或下载清空队列）。
	downloadSubscription(t, srv, "tok-a")
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.GenerationStatus != store.SubscriptionGenerationReady {
		t.Fatalf("errored user was not re-enqueued: status=%s error=%s", profile.GenerationStatus, profile.GenerationError)
	}
}

// 无法确定面板对外地址时跳过对账：重发布可能把错误的主机名烙进交付内容，宁缺毋错。
func TestStartupReconcileSkipsWithoutPublicURL(t *testing.T) {
	ctx := context.Background()
	st, chainA, _, _, userID, _ := groupsFixture(t)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainA}); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.New(st, nil, nil).PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.DisableRegionGroups = true
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, cfg: Config{Version: "v0.1.16"}, subscriptions: sub.New(st, nil, nil)}
	srv.reconcileSubscriptionDeliveries(ctx)
	if !downloadSubscription(t, srv, "tok-a")["🇯🇵 JP"] {
		t.Fatalf("reconcile without public URL must not republish")
	}
	if stored, err := st.GetSetting(ctx, store.SettingSubscriptionReconcileVersion); err != nil || stored == "v0.1.16" {
		t.Fatalf("skipped reconcile must not record version mark: stored=%q err=%v", stored, err)
	}
}

// dev 构建没有可靠的版本身份，不做对账（e2e/本地开发不受影响）。
func TestStartupReconcileSkipsDevBuilds(t *testing.T) {
	ctx := context.Background()
	st, _, _, _, _, _ := groupsFixture(t)
	srv := &Server{st: st, cfg: Config{Version: "dev", PublicURL: "https://panel.example"}, subscriptions: sub.New(st, nil, nil)}
	srv.reconcileSubscriptionDeliveries(ctx)
	if stored, err := st.GetSetting(ctx, store.SettingSubscriptionReconcileVersion); err != nil || stored != "" {
		t.Fatalf("dev build must not reconcile: stored=%q err=%v", stored, err)
	}
}
