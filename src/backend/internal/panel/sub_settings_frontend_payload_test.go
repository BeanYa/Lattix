package panel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// TestFrontendSubSettingsPayloadAccepted reproduces the exact JSON body the
// frontend sends from use-user-sub-settings.ts onSave (routing round-trips the
// whole GET DTO: region_groups_enabled / user_configured plus assigned_* echo
// fields, with forced decided by the form). Regression: readJSON uses
// DisallowUnknownFields, and unknown echo fields used to reject the whole
// request with 400 "invalid request body", so settings never reached the user.
func TestFrontendSubSettingsPayloadAccepted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)
	userID := mustCreateUser(t, st, "alice", nil)

	if _, err := server.subscriptions.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}

	payload := fmt.Sprintf(`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"region_groups_enabled":true,"mode":"suggested","preset":"balanced","categories":["ai","youtube","google","private","domestic","telegram","github","overseas"],"portable_template_id":"","mihomo_template_id":"","singbox_template_id":"","quanx_template_id":"","user_configured":false,"assigned_portable_template_id":"","assign_forced_portable":false,"assigned_mihomo_template_id":"","assign_forced_mihomo":false,"assigned_singbox_template_id":"","assign_forced_singbox":false,"assigned_quanx_template_id":"","assign_forced_quanx":false,"assigned_suggested_categories":[],"forced":false},"expires_at":null}`, userID)

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

	// 发布已转异步：等待重发布后修订号递增。
	file := awaitSubscriptionFileRevision(t, st, userID, "clash", 1)
	if !strings.Contains(string(file.Content), "AI 服务") {
		t.Fatalf("suggested rules missing from republished snapshot: %s", file.Content)
	}
}

// jsonFieldNames 返回结构体按 json tag 暴露的字段名（用于契约比对）。
func jsonFieldNames(v any) map[string]bool {
	fields := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}

// TestSubscriptionProfileInputAcceptsDTOEchoFields 锁定路由表单的输入契约：
// 前端会把 GET 返回的 routing DTO 原样回传，因此 DTO 里的每个字段都必须能在
// subscriptionProfileInput 中解码；否则 readJSON 的 DisallowUnknownFields 会让
// 整个保存请求变成 400 "invalid request body"（user_configured 曾因此漏网）。
func TestSubscriptionProfileInputAcceptsDTOEchoFields(t *testing.T) {
	input := jsonFieldNames(subscriptionProfileInput{})
	for field := range jsonFieldNames(subscriptionProfileDTO{}) {
		if !input[field] {
			t.Errorf("routing GET DTO 字段 %q 未在 subscriptionProfileInput 中声明：保存会被 400 invalid request body 拒绝", field)
		}
	}
}
