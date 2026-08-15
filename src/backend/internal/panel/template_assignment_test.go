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
			Content: "name: U\ngroups:\n  - name: UGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UGroup\n",
			ContentSHA256: "sha-3"},
	} {
		upsertTestTemplate(t, st, template)
	}
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000013", "tok-d", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-own",
		GenerationStatus: store.SubscriptionGenerationMissing,
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
		Content: "name: U\ngroups:\n  - name: UGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UGroup\n",
		ContentSHA256: "sha-3",
	})
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000017", "tok-h", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-own",
		GenerationStatus: store.SubscriptionGenerationMissing,
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

func TestUpdateUserSubSettingsPreservesAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "a", "00000000-0000-0000-0000-000000000014", "tok-e", nil)
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

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
	if profile.AssignedPortableTemplateID != "acl4ssr-standard" || !profile.AssignForcedPortable {
		t.Fatalf("assignment lost after sub-settings save: %+v", profile)
	}
}
