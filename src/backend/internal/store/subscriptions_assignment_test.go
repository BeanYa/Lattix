package store

import (
	"context"
	"testing"
)

var noDefault = SubscriptionDefaultConfig{}

func TestEffectiveProfileUnassignedKeepsUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		MihomoTemplateID: "my-mihomo",
	}
	got := EffectiveProfile(profile, noDefault)
	if got.PortableTemplateID != "mine" || got.MihomoTemplateID != "my-mihomo" {
		t.Fatalf("unassigned profile changed: %+v", got)
	}
	if got.Mode != SubscriptionModeTemplate {
		t.Fatalf("mode = %q", got.Mode)
	}
}

func TestEffectiveProfileAssignedAppliesWhenUserHasNoChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode:                       SubscriptionModeSuggested, // 默认建议模式 = 未自选
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
	}
	got := EffectiveProfile(profile, noDefault)
	if got.Mode != SubscriptionModeTemplate || got.PortableTemplateID != "acl4ssr-standard" {
		t.Fatalf("assigned portable not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("assigned mihomo not applied: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileUserChoiceWinsWhenNotForced(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		UserConfigured:             true,
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
		MihomoTemplateID:           "my-mihomo",
	}
	got := EffectiveProfile(profile, noDefault)
	if got.PortableTemplateID != "mine" {
		t.Fatalf("user portable choice overridden: %q", got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "my-mihomo" {
		t.Fatalf("user mihomo choice overridden: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileForcedOverridesUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		MihomoTemplateID:           "my-mihomo",
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
		AssignedMihomoTemplateID: "builtin-mihomo", AssignForcedMihomo: true,
	}
	got := EffectiveProfile(profile, noDefault)
	if got.PortableTemplateID != "acl4ssr-standard" || got.Mode != SubscriptionModeTemplate {
		t.Fatalf("forced portable not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("forced mihomo not applied: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileAssignedSuggestedCategoriesApplies(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:              `["ai"]`,
		AssignedSuggestedCategories: `["ads","ai","gaming"]`,
		AssignForcedPortable:        false,
	}
	got := EffectiveProfile(profile, noDefault)
	if got.Mode != SubscriptionModeSuggested || got.Preset != "balanced" {
		t.Fatalf("assigned suggested not applied: mode=%q preset=%q", got.Mode, got.Preset)
	}
	if got.CategoriesJSON != `["ads","ai","gaming"]` {
		t.Fatalf("assigned categories not applied: %q", got.CategoriesJSON)
	}
}

func TestEffectiveProfileUserConfiguredBeatsPanelAndDefault(t *testing.T) {
	def := SubscriptionDefaultConfig{PortableTemplateID: "default-tpl"}
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "minimal",
		CategoriesJSON:              `["ai","gaming"]`,
		UserConfigured:              true,
		AssignedSuggestedCategories: `["private","domestic","overseas"]`,
	}
	got := EffectiveProfile(profile, def)
	if got.Mode != SubscriptionModeSuggested || got.CategoriesJSON != `["ai","gaming"]` {
		t.Fatalf("user-level overridden: mode=%q categories=%q", got.Mode, got.CategoriesJSON)
	}
}

func TestEffectiveProfilePanelLevelBeatsDefaultLevel(t *testing.T) {
	def := SubscriptionDefaultConfig{SuggestedCategories: `["ai"]`}
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:             `["ai"]`,
		AssignedPortableTemplateID: "acl4ssr-standard",
	}
	got := EffectiveProfile(profile, def)
	if got.Mode != SubscriptionModeTemplate || got.PortableTemplateID != "acl4ssr-standard" {
		t.Fatalf("panel-level not applied over default: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
}

func TestEffectiveProfileDefaultLevelAppliesLast(t *testing.T) {
	def := SubscriptionDefaultConfig{PortableTemplateID: "default-tpl"}
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`,
	}
	got := EffectiveProfile(profile, def)
	if got.Mode != SubscriptionModeTemplate || got.PortableTemplateID != "default-tpl" {
		t.Fatalf("default level not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.CategoriesJSON != `["ai"]` {
		t.Fatalf("user columns mutated by default level: %q", got.CategoriesJSON)
	}

	suggestedDef := SubscriptionDefaultConfig{SuggestedCategories: `["private","domestic"]`}
	got = EffectiveProfile(profile, suggestedDef)
	if got.Mode != SubscriptionModeSuggested || got.CategoriesJSON != `["private","domestic"]` {
		t.Fatalf("default suggested not applied: mode=%q categories=%q", got.Mode, got.CategoriesJSON)
	}
}

func TestEffectiveProfileNoDefaultKeepsProfile(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`,
	}
	got := EffectiveProfile(profile, noDefault)
	if got.Mode != SubscriptionModeSuggested || got.CategoriesJSON != `["ai"]` {
		t.Fatalf("profile changed without any level: %+v", got)
	}
}

func TestEffectiveProfileUserConfiguredNativeSlotGapFill(t *testing.T) {
	// 客户端原生槽位保持兜底语义：用户未选（跟随主策略）时非强制指派仍生效。
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		UserConfigured:           true,
		AssignedMihomoTemplateID: "builtin-mihomo",
	}
	got := EffectiveProfile(profile, noDefault)
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("assigned mihomo gap-fill lost: %q", got.MihomoTemplateID)
	}
}

func TestSubscriptionDefaultConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg, err := st.SubscriptionDefaultConfig(ctx)
	if err != nil || !cfg.Empty() {
		t.Fatalf("fresh default config = %+v err %v", cfg, err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, SubscriptionDefaultConfig{
		SuggestedCategories: `["ai","gaming"]`,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = st.SubscriptionDefaultConfig(ctx)
	if err != nil || cfg.SuggestedCategories != `["ai","gaming"]` || cfg.PortableTemplateID != "" {
		t.Fatalf("default config round trip = %+v err %v", cfg, err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, SubscriptionDefaultConfig{
		PortableTemplateID: "acl4ssr-standard",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = st.SubscriptionDefaultConfig(ctx)
	if err != nil || cfg.PortableTemplateID != "acl4ssr-standard" || cfg.SuggestedCategories != "" {
		t.Fatalf("default config overwrite = %+v err %v", cfg, err)
	}
	if err := st.SaveSubscriptionDefaultConfig(ctx, SubscriptionDefaultConfig{}); err != nil {
		t.Fatal(err)
	}
	if cfg, err = st.SubscriptionDefaultConfig(ctx); err != nil || !cfg.Empty() {
		t.Fatalf("default config clear = %+v err %v", cfg, err)
	}
}

func TestSubscriptionProfilePersistsAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000001", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:             `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		AssignedPortableTemplateID: "acl4ssr-standard", AssignForcedPortable: true,
		AssignedMihomoTemplateID: "builtin-mihomo",
		GenerationStatus:         SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedPortableTemplateID != "acl4ssr-standard" || !got.AssignForcedPortable {
		t.Fatalf("portable assignment lost: %+v", got)
	}
	if got.AssignedMihomoTemplateID != "builtin-mihomo" || got.AssignForcedMihomo {
		t.Fatalf("mihomo assignment wrong: %+v", got)
	}
	if got.AssignForcedSingbox || got.AssignForcedQuanX {
		t.Fatalf("unrelated forced flags set: %+v", got)
	}
}

func TestSubscriptionProfilePersistsSuggestedCategoriesAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000003", "tok3", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:              `["ai"]`,
		AssignedSuggestedCategories: `["private","domestic","overseas"]`,
		AssignForcedPortable:        true,
		GenerationStatus:            SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedSuggestedCategories != `["private","domestic","overseas"]` || !got.AssignForcedPortable {
		t.Fatalf("suggested assignment lost: %+v", got)
	}
	if got.AssignedPortableTemplateID != "" {
		t.Fatalf("template slot unexpectedly set: %+v", got)
	}
}

func TestSubscriptionProfilePersistsUserConfigured(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000004", "tok4", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "minimal",
		CategoriesJSON:   `["ai","gaming"]`,
		UserConfigured:   true,
		GenerationStatus: SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UserConfigured {
		t.Fatalf("user_configured lost: %+v", got)
	}
	if got.Mode != SubscriptionModeSuggested || got.Preset != "minimal" || got.CategoriesJSON != `["ai","gaming"]` {
		t.Fatalf("user routing fields lost: %+v", got)
	}
}

func TestDeleteSubscriptionTemplateRejectsAssigned(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertSubscriptionTemplate(ctx, SubscriptionTemplate{
		ID: "tpl-1", Name: "Tpl", Kind: "portable", Origin: "local", Content: "x", ContentSHA256: "sha",
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000002", "tok2", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:             `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		AssignedPortableTemplateID: "tpl-1",
		GenerationStatus:           SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSubscriptionTemplate(ctx, "tpl-1"); err == nil {
		t.Fatal("assigned template deletion unexpectedly succeeded")
	}
}

func TestEffectiveProfileDefaultLevelAppliesRegionGroups(t *testing.T) {
	def := SubscriptionDefaultConfig{
		SuggestedCategories: `["private","domestic"]`,
		DisableRegionGroups: true,
	}
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`,
	}
	got := EffectiveProfile(profile, def)
	if got.CategoriesJSON != `["private","domestic"]` || !got.DisableRegionGroups {
		t.Fatalf("default region groups not applied: categories=%q disable=%v",
			got.CategoriesJSON, got.DisableRegionGroups)
	}
}
