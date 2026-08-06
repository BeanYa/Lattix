package panel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// TestFrontendSubSettingsPayloadAccepted reproduces the exact JSON body the
// frontend sends from Users.tsx onSaveSubSettings (routing round-trips the GET
// DTO including assigned_* echo fields). Regression: readJSON uses
// DisallowUnknownFields, and unknown echo fields used to reject the whole
// request with 400, so routing changes never reached the subscription.
func TestFrontendSubSettingsPayloadAccepted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	userID := mustCreateUser(t, st, "alice", nil)

	if _, err := server.subscriptions.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}

	payload := fmt.Sprintf(`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"mode":"suggested","preset":"balanced","categories":["ai","youtube","google","private","domestic","telegram","github","overseas"],"portable_template_id":"","mihomo_template_id":"","singbox_template_id":"","quanx_template_id":"","assigned_portable_template_id":"","assign_forced_portable":false,"assigned_mihomo_template_id":"","assign_forced_mihomo":false,"assigned_singbox_template_id":"","assign_forced_singbox":false,"assigned_quanx_template_id":"","assign_forced_quanx":false,"assigned_suggested_categories":[]},"expires_at":null}`, userID)

	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings", strings.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Mode != store.SubscriptionModeSuggested || profile.Preset != "balanced" ||
		!strings.Contains(profile.CategoriesJSON, "youtube") {
		t.Fatalf("routing not saved: mode=%s preset=%s categories=%s", profile.Mode, profile.Preset, profile.CategoriesJSON)
	}

	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil {
		t.Fatal(err)
	}
	if file.Revision <= 1 {
		t.Fatalf("snapshot revision = %d, want > 1 (republished after save)", file.Revision)
	}
	if !strings.Contains(string(file.Content), "AI 服务") {
		t.Fatalf("suggested rules missing from republished snapshot: %s", file.Content)
	}
}
