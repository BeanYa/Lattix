package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// 面板缩写变更触发全量用户订阅重发布（快照模型下不触发则维持旧组名）。
func TestPanelShortChangeRepublishesSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "bob-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	subSrv := sub.New(st, nil, nil)
	subSrv.StartRegenerator(ctx)
	defer func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		_ = subSrv.WaitRegenerator(waitCtx)
	}()
	serverAPI := &Server{st: st, subscriptions: subSrv}

	// 基线：无已发布快照。
	status, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status == store.SubscriptionGenerationReady {
		t.Fatalf("unexpected published snapshot before change: %+v", status)
	}

	rec := httptest.NewRecorder()
	serverAPI.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/setting/update",
		strings.NewReader(`{"panel_short":"MyPanel"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetSetting(ctx, store.SettingPanelShort); got != "MyPanel" {
		t.Fatalf("panel_short = %q, want MyPanel", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err = st.SubscriptionSnapshotStatus(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == store.SubscriptionGenerationReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot not republished after panel_short change, status = %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 再保存一次相同的 panel_short：不产生新的发布（revision 不变）。
	before := status.Revision
	rec = httptest.NewRecorder()
	serverAPI.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/setting/update",
		strings.NewReader(`{"panel_short":"MyPanel"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("second update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	time.Sleep(3 * 150 * time.Millisecond) // 超过重发布防抖窗口
	status, err = st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != before {
		t.Fatalf("unchanged panel_short triggered republish: revision %d -> %d", before, status.Revision)
	}
}
