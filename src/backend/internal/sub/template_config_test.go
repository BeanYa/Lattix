package sub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func startTemplateConfigServer(t *testing.T) (*store.Store, *Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	server := NewWithCacheDir(st, nil, nil, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	server.StartRegenerator(ctx)
	t.Cleanup(func() {
		cancel()
		_ = server.WaitRegenerator(context.Background())
	})
	return st, server
}

func upsertTemplateConfigTemplate(t *testing.T, st *store.Store, id, name, kind string) {
	t.Helper()
	if err := st.UpsertSubscriptionTemplate(context.Background(), store.SubscriptionTemplate{
		ID: id, Name: name, Kind: kind, Origin: "local",
		Content:       "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n",
		ContentSHA256: "sha-" + id,
	}); err != nil {
		t.Fatal(err)
	}
}

func subConfigRequest(t *testing.T, server *Server, method, token, body string) (int, SubTemplateConfigResponse) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/sub/"+token+"/template-config", strings.NewReader(body))
	req.SetPathValue("token", token)
	rec := httptest.NewRecorder()
	if method == http.MethodPost {
		server.HandleSubUpdateTemplateConfig(rec, req)
	} else {
		server.HandleSubTemplateConfig(rec, req)
	}
	var resp SubTemplateConfigResponse
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, resp
}

func awaitTemplateConfigSnapshot(t *testing.T, st *store.Store, userID int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := st.SubscriptionSnapshotStatus(context.Background(), userID)
		if err == nil && snapshot.Status == store.SubscriptionGenerationReady {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("user %d 订阅快照未及时发布: %+v (err %v)", userID, snapshot, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSubTemplateConfigForbiddenOverridesUserConfig(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	upsertTemplateConfigTemplate(t, st, "tpl-portable", "Portable", "portable")
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "dave", "00000000-0000-0000-0000-0000000000f1", "dave-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}

	// 强制级生效中：GET 标注 forced 摘要，effective 跟随强制指派。
	status, resp := subConfigRequest(t, server, http.MethodGet, "dave-token", "")
	if status != http.StatusOK {
		t.Fatalf("GET status = %d", status)
	}
	if !resp.ForcedPortable || resp.ForcedSummary == "" {
		t.Fatalf("forced summary missing: %+v", resp)
	}
	if resp.Effective.Mode != store.SubscriptionModeTemplate || resp.Effective.PortableTemplateID != "tpl-portable" {
		t.Fatalf("effective not following forced assignment: %+v", resp.Effective)
	}
	if !containsTemplate(resp.Templates, "tpl-portable") || len(resp.Categories) == 0 {
		t.Fatalf("catalog missing: templates=%d categories=%d", len(resp.Templates), len(resp.Categories))
	}

	// 用户保存配置仍写入用户级（强制级在，实际生效保持跟随强制指派）。
	status, resp = subConfigRequest(t, server, http.MethodPost, "dave-token",
		`{"mode":"suggested","preset":"minimal","categories":["ai"]}`)
	if status != http.StatusOK {
		t.Fatalf("POST status = %d", status)
	}
	if !resp.UserConfigured {
		t.Fatalf("user_configured not set after save: %+v", resp)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.UserConfigured || profile.Mode != store.SubscriptionModeSuggested || profile.Preset != "minimal" {
		t.Fatalf("user-level config not saved: %+v", profile)
	}
	if !profile.AssignForcedPortable || profile.AssignedPortableTemplateID != "tpl-portable" {
		t.Fatalf("forced assignment touched by user save: %+v", profile)
	}
	awaitTemplateConfigSnapshot(t, st, userID)
}

func TestSubTemplateConfigUserLevelBeatsPanelLevel(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	upsertTemplateConfigTemplate(t, st, "tpl-portable", "Portable", "portable")
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "eve", "00000000-0000-0000-0000-0000000000f2", "eve-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["private","domestic","overseas"]`,
	}); err != nil {
		t.Fatal(err)
	}

	// 面板级非强制指派：摘要提示用户自选优先。
	status, resp := subConfigRequest(t, server, http.MethodGet, "eve-token", "")
	if status != http.StatusOK {
		t.Fatalf("GET status = %d", status)
	}
	if resp.ForcedPortable || resp.PanelAssignedSummary == "" {
		t.Fatalf("panel-level summary missing: %+v", resp)
	}

	// 用户自选模板（用户级）优先于面板级建议规则。
	status, resp = subConfigRequest(t, server, http.MethodPost, "eve-token",
		`{"mode":"template","portable_template_id":"tpl-portable","categories":["ai"]}`)
	if status != http.StatusOK {
		t.Fatalf("POST status = %d", status)
	}
	if resp.Effective.Mode != store.SubscriptionModeTemplate || resp.Effective.PortableTemplateID != "tpl-portable" {
		t.Fatalf("user-level template not effective: %+v", resp.Effective)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != `["private","domestic","overseas"]` {
		t.Fatalf("panel assignment dropped: %+v", profile)
	}
	awaitTemplateConfigSnapshot(t, st, userID)
}

func TestSubTemplateConfigValidation(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	upsertTemplateConfigTemplate(t, st, "tpl-mihomo", "Mihomo", "mihomo")
	if _, err := st.InsertUser(context.Background(), "carl", "00000000-0000-0000-0000-0000000000f3", "carl-token", nil); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"invalid mode", `{"mode":"weird","categories":["ai"]}`},
		{"template without portable", `{"mode":"template","categories":["ai"]}`},
		{"invalid preset", `{"mode":"suggested","preset":"extreme","categories":["ai"]}`},
		{"unknown category", `{"mode":"suggested","preset":"balanced","categories":["nope"]}`},
		{"unknown template", `{"mode":"template","portable_template_id":"missing","categories":["ai"]}`},
		{"kind mismatch", `{"mode":"template","portable_template_id":"tpl-mihomo","categories":["ai"]}`},
		{"unknown field", `{"mode":"suggested","region_groups_enabled":true,"categories":["ai"]}`},
	}
	for _, testCase := range cases {
		status, _ := subConfigRequest(t, server, http.MethodPost, "carl-token", testCase.body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", testCase.name, status)
		}
	}

	// 错误 token 沿用订阅公开 API 的 404 语义。
	status, _ := subConfigRequest(t, server, http.MethodGet, "wrong-token", "")
	if status != http.StatusNotFound {
		t.Fatalf("bad token status = %d, want 404", status)
	}
}

func containsTemplate(templates []SubTemplateOption, id string) bool {
	for _, template := range templates {
		if template.ID == id {
			return true
		}
	}
	return false
}

func TestSubTemplateConfigEffectiveSourceAndReset(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	upsertTemplateConfigTemplate(t, st, "tpl-portable", "Portable", "portable")
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "dave", "00000000-0000-0000-0000-0000000000f4", "dave2-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	resetRequest := func(token string) (int, SubTemplateConfigResponse) {
		req := httptest.NewRequest(http.MethodPost, "/api/sub/"+token+"/template-config/reset", strings.NewReader(""))
		req.SetPathValue("token", token)
		rec := httptest.NewRecorder()
		server.HandleSubResetTemplateConfig(rec, req)
		var resp SubTemplateConfigResponse
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, resp
	}

	// 无任何配置：回落 unset；配置默认级后为 default。
	status, resp := subConfigRequest(t, server, http.MethodGet, "dave2-token", "")
	if status != http.StatusOK || resp.EffectiveSource != "unset" {
		t.Fatalf("fresh source = %q status %d", resp.EffectiveSource, status)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{
		SuggestedCategories: `["private","domestic"]`,
	}); err != nil {
		t.Fatal(err)
	}
	status, resp = subConfigRequest(t, server, http.MethodGet, "dave2-token", "")
	if status != http.StatusOK || resp.EffectiveSource != "default" || resp.DefaultSummary == "" {
		t.Fatalf("default source = %q summary %q", resp.EffectiveSource, resp.DefaultSummary)
	}
	if len(resp.Effective.Categories) != 2 {
		t.Fatalf("default categories not effective: %+v", resp.Effective.Categories)
	}

	// 面板级非强制指派：source=panel 且摘要提示用户自选优先。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable",
	}); err != nil {
		t.Fatal(err)
	}
	status, resp = subConfigRequest(t, server, http.MethodGet, "dave2-token", "")
	if status != http.StatusOK || resp.EffectiveSource != "panel" || resp.PanelAssignedSummary == "" {
		t.Fatalf("panel source = %q summary %q", resp.EffectiveSource, resp.PanelAssignedSummary)
	}

	// 用户保存：source=user；reset 后清除用户级回落 panel，且不触碰面板级槽位。
	status, resp = subConfigRequest(t, server, http.MethodPost, "dave2-token",
		`{"mode":"suggested","preset":"minimal","categories":["ai"]}`)
	if status != http.StatusOK || resp.EffectiveSource != "user" || !resp.UserConfigured {
		t.Fatalf("user source = %q configured %v", resp.EffectiveSource, resp.UserConfigured)
	}
	status, resp = resetRequest("dave2-token")
	if status != http.StatusOK || resp.UserConfigured {
		t.Fatalf("reset source = %q configured %v", resp.EffectiveSource, resp.UserConfigured)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "tpl-portable" {
		t.Fatalf("panel slots touched by reset: %+v", profile)
	}
	if resp.EffectiveSource != "panel" {
		t.Fatalf("after reset source = %q, want panel", resp.EffectiveSource)
	}
}
