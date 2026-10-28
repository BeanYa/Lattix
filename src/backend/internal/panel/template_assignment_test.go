package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

const portableTemplateBody = "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n"

func upsertTestTemplate(t *testing.T, st *store.Store, template store.SubscriptionTemplate) {
	t.Helper()
	if err := st.UpsertSubscriptionTemplate(context.Background(), template); err != nil {
		t.Fatal(err)
	}
}

func assignRequest(t *testing.T, server *Server, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.handleAssignSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/assign", strings.NewReader(body)))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return rec.Code, resp.Code
}

func TestAssignSubscriptionTemplateWritesSlotAndPublishes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	userA, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000010", "tok-a", nil)
	userB, _ := st.InsertUser(ctx, "b", "00000000-0000-0000-0000-000000000011", "tok-b", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d,%d],"template_id":"tpl-portable","forced":true}`, userA, userB))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	for _, userID := range []int64{userA, userB} {
		profile, err := st.UserSubscriptionProfile(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if profile.AssignedPortableTemplateID != "tpl-portable" || !profile.AssignForcedPortable {
			t.Fatalf("user %d assignment = %+v", userID, profile)
		}
		// 发布已转异步（regenerator 去抖执行），等待快照就绪。
		awaitSnapshotReady(t, st, userID)
	}
}

func TestAssignSubscriptionTemplateKindMapping(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-mihomo", Name: "Mihomo", Kind: "mihomo", Origin: "local",
		Content: "proxies: []\n", ContentSHA256: "sha-2",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000012", "tok-c", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-mihomo"}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedMihomoTemplateID != "tpl-mihomo" {
		t.Fatalf("mihomo slot = %q", profile.AssignedMihomoTemplateID)
	}
	if profile.AssignedPortableTemplateID != "" || profile.AssignedSingboxTemplateID != "" || profile.AssignedQuanXTemplateID != "" {
		t.Fatalf("unrelated slots written: %+v", profile)
	}
	if profile.AssignForcedMihomo {
		t.Fatalf("forced defaulted true: %+v", profile)
	}
}

func TestAssignSubscriptionTemplateValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	if _, code := assignRequest(t, server, `{"user_ids":[],"template_id":"tpl"}`); code != shared.CodeInvalidArgument {
		t.Fatalf("empty user_ids: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"template_id":""}`); code != shared.CodeInvalidArgument {
		t.Fatalf("empty template_id: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"template_id":"missing"}`); code != shared.CodeNotFound {
		t.Fatalf("missing template: code=%q", code)
	}
}

func TestAssignSubscriptionTemplateRejectsMissingUser(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	if _, code := assignRequest(t, server, `{"user_ids":[9999],"template_id":"tpl-portable"}`); code != shared.CodeNotFound {
		t.Fatalf("code=%q", code)
	}
}

func TestUnassignSubscriptionTemplateClearsSlotKeepsUserChoice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, template := range []store.SubscriptionTemplate{
		{ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
			Content: portableTemplateBody, ContentSHA256: "sha-1"},
		{ID: "user-own", Name: "UserOwn", Kind: "portable", Origin: "local",
			Content:       "name: U\ngroups:\n  - name: UGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UGroup\n",
			ContentSHA256: "sha-3"},
	} {
		upsertTestTemplate(t, st, template)
	}
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000013", "tok-d", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-own",
		GenerationStatus:           store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	rec := httptest.NewRecorder()
	server.handleUnassignSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/unassign", strings.NewReader(fmt.Sprintf(
			`{"user_ids":[%d],"template_id":"tpl-portable"}`, userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "" || profile.AssignForcedPortable {
		t.Fatalf("assignment not cleared: %+v", profile)
	}
	if profile.PortableTemplateID != "user-own" || profile.Mode != store.SubscriptionModeTemplate {
		t.Fatalf("user choice lost: %+v", profile)
	}
	// 发布已转异步：等待重发布完成（恢复用户自选模板的快照）。
	awaitSnapshotReady(t, st, userID)
}

func TestAssignSubscriptionTemplateSuggestedCategories(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000015", "tok-f", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	// 乱序 + 重复 → 按内置顺序去重存储。
	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"suggested_categories":["gaming","ads","ai","gaming"],"forced":true}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != `["ads","ai","gaming"]` || !profile.AssignForcedPortable {
		t.Fatalf("suggested assignment = %+v", profile)
	}
	if profile.AssignedPortableTemplateID != "" {
		t.Fatalf("template slot unexpectedly set: %+v", profile)
	}
	// 发布已转异步：等待快照就绪。
	awaitSnapshotReady(t, st, userID)
}

func TestAssignSubscriptionTemplateSuggestedMutualExclusion(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-1",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000016", "tok-g", nil)
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	// 先指派建议规则，再指派模板 → 建议规则被清除。
	if status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"suggested_categories":["ai","youtube","google","private","domestic","telegram","github","overseas"]}`, userID)); status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("assign suggested: status=%d code=%q", status, code)
	}
	if status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-portable"}`, userID)); status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("assign template: status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != "" || profile.AssignedPortableTemplateID != "tpl-portable" {
		t.Fatalf("template assign did not clear suggested: %+v", profile)
	}

	// 再指派建议规则 → 模板指派被清除。
	if status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"suggested_categories":["private","domestic","overseas"],"forced":true}`, userID)); status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("reassign suggested: status=%d code=%q", status, code)
	}
	profile, err = st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != `["private","domestic","overseas"]` || profile.AssignedPortableTemplateID != "" || !profile.AssignForcedPortable {
		t.Fatalf("suggested assign did not clear template: %+v", profile)
	}
}

func TestAssignSubscriptionTemplateSuggestedValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	if _, code := assignRequest(t, server, `{"user_ids":[1],"suggested_categories":["unknown-category"]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("unknown category: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"suggested_categories":[]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("empty categories: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1],"template_id":"tpl","suggested_categories":["ai"]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("both targets: code=%q", code)
	}
	if _, code := assignRequest(t, server, `{"user_ids":[1]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("no target: code=%q", code)
	}
}

func TestUnassignSubscriptionTemplateSuggestedKeepsUserChoice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "user-own", Name: "UserOwn", Kind: "portable", Origin: "local",
		Content:       "name: U\ngroups:\n  - name: UGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UGroup\n",
		ContentSHA256: "sha-3",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000017", "tok-h", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-own",
		GenerationStatus:            store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["ai","youtube","google","private","domestic","telegram","github","overseas"]`, AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	rec := httptest.NewRecorder()
	server.handleUnassignSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/unassign", strings.NewReader(fmt.Sprintf(
			`{"user_ids":[%d],"suggested_categories":["ai","youtube","google","private","domestic","telegram","github","overseas"]}`, userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != "" || profile.AssignForcedPortable {
		t.Fatalf("suggested assignment not cleared: %+v", profile)
	}
	if profile.PortableTemplateID != "user-own" || profile.Mode != store.SubscriptionModeTemplate {
		t.Fatalf("user choice lost: %+v", profile)
	}
	// 发布已转异步：等待重发布完成。
	awaitSnapshotReady(t, st, userID)
}

func TestUpdateUserSubSettingsWritesPanelSlotsKeepsUserLevel(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-ss1",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000014", "tok-e", nil)
	// 用户级自选（订阅页保存）：面板侧保存不得触碰。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "minimal",
		CategoriesJSON: `["ai","gaming"]`, UserConfigured: true,
		GenerationStatus:            store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["private","domestic"]`,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	// 不勾选覆盖：表单写入面板级槽位（suggested → 替换原建议规则指派），用户级原样。
	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/sub-settings", strings.NewReader(fmt.Sprintf(
			`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"mode":"suggested","preset":"balanced","categories":["ai"],"portable_template_id":"","mihomo_template_id":"","singbox_template_id":"","quanx_template_id":""}}`,
			userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedSuggestedCategories != `["ai"]` || profile.AssignedPortableTemplateID != "" {
		t.Fatalf("panel slots not replaced by form: %+v", profile)
	}
	if profile.AssignForcedPortable {
		t.Fatalf("forced flag set without override checkbox: %+v", profile)
	}
	if !profile.UserConfigured || profile.Preset != "minimal" || profile.CategoriesJSON != `["ai","gaming"]` {
		t.Fatalf("user-level fields touched by panel save: %+v", profile)
	}

	// 勾选覆盖：表单写入强制级。
	rec = httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/sub-settings", strings.NewReader(fmt.Sprintf(
			`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"mode":"template","portable_template_id":"tpl-portable","forced":true,"categories":[],"mihomo_template_id":"","singbox_template_id":"","quanx_template_id":""}}`,
			userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("forced save status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err = st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "tpl-portable" || !profile.AssignForcedPortable {
		t.Fatalf("forced slots not applied: %+v", profile)
	}
	if profile.AssignedSuggestedCategories != "" {
		t.Fatalf("stale suggested assignment kept: %+v", profile)
	}
	if !profile.UserConfigured {
		t.Fatalf("user_configured cleared by panel save: %+v", profile)
	}

	// 建议模式 + 空分组：清除面板级主策略，用户级仍保留。
	rec = httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/sub-settings", strings.NewReader(fmt.Sprintf(
			`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":"","routing":{"mode":"suggested","categories":[],"portable_template_id":"","mihomo_template_id":"","singbox_template_id":"","quanx_template_id":""}}`,
			userID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear save status=%d body=%s", rec.Code, rec.Body.String())
	}
	profile, err = st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignedPortableTemplateID != "" || profile.AssignForcedPortable {
		t.Fatalf("panel main not cleared: %+v", profile)
	}
	if !profile.UserConfigured {
		t.Fatalf("user_configured cleared: %+v", profile)
	}
}

func unforceRequest(t *testing.T, server *Server, body string) (int, string, []int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.handleUnforceSubscriptionTemplate(rec, httptest.NewRequest(http.MethodPost,
		"/api/subscription/template/unforce", strings.NewReader(body)))
	var resp struct {
		Code string          `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &payload); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, resp.Code, payload.UserIDs
}

func TestUnforceSubscriptionTemplateKeepsAssignmentClearsForced(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-u1",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000020", "tok-u1", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	status, code, unforced := unforceRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-portable"}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	if len(unforced) != 1 || unforced[0] != userID {
		t.Fatalf("unforced user_ids = %v", unforced)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignForcedPortable {
		t.Fatalf("forced flag still set: %+v", profile)
	}
	if profile.AssignedPortableTemplateID != "tpl-portable" {
		t.Fatalf("assignment dropped by unforce: %+v", profile)
	}
	// 强制解除后重发订阅快照。
	awaitSnapshotReady(t, st, userID)
}

func TestUnforceSubscriptionTemplateSuggestedSlot(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000021", "tok-u2", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["private","domestic"]`, AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	status, code, unforced := unforceRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"suggested_categories":["private","domestic"]}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	if len(unforced) != 1 {
		t.Fatalf("unforced user_ids = %v", unforced)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AssignForcedPortable || profile.AssignedSuggestedCategories != `["private","domestic"]` {
		t.Fatalf("suggested slot after unforce: %+v", profile)
	}
}

func TestUnforceSubscriptionTemplateSkipsNonForced(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-u3",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000022", "tok-u3", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "tpl-portable",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	// 未被强制的用户：成功返回但无变更、不进入重发布名单。
	status, code, unforced := unforceRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-portable"}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	if len(unforced) != 0 {
		t.Fatalf("non-forced user reported unforced: %v", unforced)
	}
}

func TestUnforceSubscriptionTemplateValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	if _, code, _ := unforceRequest(t, server, `{"user_ids":[1]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("missing target: code=%q", code)
	}
	if _, code, _ := unforceRequest(t, server, `{"user_ids":[]}`); code != shared.CodeInvalidArgument {
		t.Fatalf("empty user_ids: code=%q", code)
	}
}

func TestAssignSubscriptionTemplateCopyToUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertTestTemplate(t, st, store.SubscriptionTemplate{
		ID: "tpl-portable", Name: "Portable", Kind: "portable", Origin: "local",
		Content: portableTemplateBody, ContentSHA256: "sha-copy",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000023", "tok-cu", nil)
	// 原用户级非空：拷贝应整体替换。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "minimal",
		CategoriesJSON: `["ai","gaming"]`, UserConfigured: true,
		MihomoTemplateID: "user-mihomo", GenerationStatus: store.SubscriptionGenerationMissing,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	startTestRegenerator(t, server.subscriptions)

	status, code := assignRequest(t, server, fmt.Sprintf(
		`{"user_ids":[%d],"template_id":"tpl-portable","forced":true,"copy_to_user":true}`, userID))
	if status != http.StatusOK || code != shared.CodeOK {
		t.Fatalf("status=%d code=%q", status, code)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.UserConfigured {
		t.Fatalf("copy_to_user did not mark user_configured: %+v", profile)
	}
	if profile.Mode != store.SubscriptionModeTemplate || profile.PortableTemplateID != "tpl-portable" {
		t.Fatalf("user config not copied from assignment: %+v", profile)
	}
	if profile.MihomoTemplateID != "" {
		t.Fatalf("user native not cleared by copy: %+v", profile)
	}
	awaitSnapshotReady(t, st, userID)
}
