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
		{"unknown field", `{"mode":"suggested","region_groups":true,"categories":["ai"]}`},
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

func subResetTemplateConfig(t *testing.T, server *Server, token string) (int, SubTemplateConfigResponse) {
	t.Helper()
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

// 用户级配置缺席时，模板配置卡展示的地区分组开关必须与订阅文件实际生效值一致：
// 默认级开启地区分组时，即使用户列仍残留关闭标记，响应也应为开启。
func TestSubTemplateConfigRegionGroupsFollowEffectiveLevel(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "rhea", "00000000-0000-0000-0000-0000000000f5", "rhea-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{
		SuggestedCategories: `["ai","private","domestic","overseas"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:      `["ai","private","domestic","overseas"]`,
		DisableRegionGroups: true,
		GenerationStatus:    store.SubscriptionGenerationPending,
	}); err != nil {
		t.Fatal(err)
	}

	status, resp := subConfigRequest(t, server, http.MethodGet, "rhea-token", "")
	if status != http.StatusOK {
		t.Fatalf("GET status = %d", status)
	}
	if resp.UserConfigured {
		t.Fatalf("user_configured 应为 false: %+v", resp)
	}
	if resp.EffectiveSource != "default" {
		t.Fatalf("effective_source = %q, want default", resp.EffectiveSource)
	}
	if !resp.RegionGroupsEnabled {
		t.Fatalf("region_groups_enabled 应与默认级实际生效值一致（开启）: %+v", resp)
	}

	// 默认级关闭地区分组时，用户级缺席的页面同样应显示关闭。
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{
		SuggestedCategories: `["ai","private","domestic","overseas"]`,
		DisableRegionGroups: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, resp = subConfigRequest(t, server, http.MethodGet, "rhea-token", "")
	if resp.RegionGroupsEnabled {
		t.Fatalf("region_groups_enabled 应跟随默认级（关闭）: %+v", resp)
	}
}

// 恢复默认清除用户级配置后，地区分组开关应回到默认级/内置默认（开启），
// 而不是保留用户之前的关闭选择。
func TestSubResetTemplateConfigRestoresDefaultRegionGroups(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "iris", "00000000-0000-0000-0000-0000000000f6", "iris-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, resp := subConfigRequest(t, server, http.MethodPost, "iris-token",
		`{"mode":"suggested","preset":"minimal","categories":["private","domestic","overseas"],"region_groups_enabled":false}`)
	if status != http.StatusOK || !resp.UserConfigured {
		t.Fatalf("save status = %d configured %v", status, resp.UserConfigured)
	}
	if resp.RegionGroupsEnabled {
		t.Fatalf("保存关闭地区分组后页面应为关闭: %+v", resp)
	}

	status, resp = subResetTemplateConfig(t, server, "iris-token")
	if status != http.StatusOK || resp.UserConfigured {
		t.Fatalf("reset status = %d configured %v", status, resp.UserConfigured)
	}
	if !resp.RegionGroupsEnabled {
		t.Fatalf("恢复默认后地区分组应回到内置默认（开启）: %+v", resp)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DisableRegionGroups {
		t.Fatalf("恢复默认未清除用户级地区分组开关: %+v", profile)
	}
}

// 回归守卫：存在面板级/强制级指派时，恢复默认不得把地区分组列无条件重置为
// 默认级的值。该列由管理员指派与用户共用（saveUserRouting 会覆盖），reset 只能
// 保留其当前值；这里让默认级为「开启」而列当前为「关闭」，无条件回落会被本用例抓住。
func TestSubResetTemplateConfigKeepsRegionGroupsColumnWithPanelAssignment(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "juno", "00000000-0000-0000-0000-0000000000f7", "juno-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{
		SuggestedCategories: `["ai","private","domestic","overseas"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:              `["ai"]`,
		AssignedSuggestedCategories: `["private","domestic"]`,
		DisableRegionGroups:         true,
		GenerationStatus:            store.SubscriptionGenerationPending,
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := subConfigRequest(t, server, http.MethodPost, "juno-token",
		`{"mode":"suggested","preset":"balanced","categories":["ai"],"region_groups_enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("save status = %d", status)
	}
	status, resp := subResetTemplateConfig(t, server, "juno-token")
	if status != http.StatusOK {
		t.Fatalf("reset status = %d", status)
	}
	if resp.RegionGroupsEnabled {
		t.Fatalf("存在面板级指派时恢复默认不应无条件回落默认级的地区分组开关: %+v", resp)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.DisableRegionGroups {
		t.Fatalf("恢复默认改动了面板级指派下的地区分组列: %+v", profile)
	}
	if profile.AssignedSuggestedCategories != `["private","domestic"]` {
		t.Fatalf("恢复默认改动了面板级指派槽位: %+v", profile)
	}
}

// 配置了默认级时也要正确回落：默认级「关闭地区分组」时，用户曾开启并保存，
// 恢复默认后生效值应跟随默认级（关闭）；但共享列只写内置默认（开启），不把默认级
// 值固化进去——否则管理员之后清除默认级，用户会残留关闭状态。
func TestSubResetTemplateConfigFollowsConfiguredDefaultRegionGroups(t *testing.T) {
	st, server := startTemplateConfigServer(t)
	ctx := context.Background()
	userID, err := st.InsertUser(ctx, "kylo", "00000000-0000-0000-0000-0000000000f8", "kylo-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{
		SuggestedCategories: `["ai","private"]`,
		DisableRegionGroups: true,
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := subConfigRequest(t, server, http.MethodPost, "kylo-token",
		`{"mode":"suggested","preset":"balanced","categories":["ai","private"],"region_groups_enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("save status = %d", status)
	}
	status, resp := subResetTemplateConfig(t, server, "kylo-token")
	if status != http.StatusOK {
		t.Fatalf("reset status = %d", status)
	}
	if resp.RegionGroupsEnabled {
		t.Fatalf("恢复默认生效值应跟随默认级的地区分组开关（关闭）: %+v", resp)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DisableRegionGroups {
		t.Fatalf("恢复默认不应把默认级的开关固化进共享列: %+v", profile)
	}

	// 清除默认级后应回到内置默认（开启），验证共享列里没有残留默认级的关闭值。
	if err := st.SaveSubscriptionDefaultConfig(ctx, store.SubscriptionDefaultConfig{}); err != nil {
		t.Fatal(err)
	}
	status, resp = subConfigRequest(t, server, http.MethodGet, "kylo-token", "")
	if status != http.StatusOK || resp.EffectiveSource != "unset" {
		t.Fatalf("清除默认级后 source = %q status %d", resp.EffectiveSource, status)
	}
	if !resp.RegionGroupsEnabled {
		t.Fatalf("清除默认级后地区分组应回到内置默认（开启）: %+v", resp)
	}
}

// panelRoutingConfigured 决定恢复默认是否保留地区分组列：管理员任何一级指派槽位
// 或强制标记都算“面板已配置”。
func TestPanelRoutingConfigured(t *testing.T) {
	cases := []struct {
		name    string
		profile store.SubscriptionProfile
		want    bool
	}{
		{"empty", store.SubscriptionProfile{}, false},
		{"panel suggested categories", store.SubscriptionProfile{AssignedSuggestedCategories: `["ai"]`}, true},
		{"panel portable template", store.SubscriptionProfile{AssignedPortableTemplateID: "tpl"}, true},
		{"native mihomo assignment", store.SubscriptionProfile{AssignedMihomoTemplateID: "tpl"}, true},
		{"native singbox assignment", store.SubscriptionProfile{AssignedSingboxTemplateID: "tpl"}, true},
		{"native quanx assignment", store.SubscriptionProfile{AssignedQuanXTemplateID: "tpl"}, true},
		{"forced flag without slot", store.SubscriptionProfile{AssignForcedPortable: true}, true},
		{"forced native flag", store.SubscriptionProfile{AssignForcedQuanX: true}, true},
	}
	for _, testCase := range cases {
		if got := panelRoutingConfigured(testCase.profile); got != testCase.want {
			t.Errorf("%s: panelRoutingConfigured = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
