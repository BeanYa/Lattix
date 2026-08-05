package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEffectiveProfileUnassignedKeepsUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		MihomoTemplateID: "my-mihomo",
	}
	got := EffectiveProfile(profile)
	if got.PortableTemplateID != "mine" || got.MihomoTemplateID != "my-mihomo" {
		t.Fatalf("unassigned profile changed: %+v", got)
	}
	if got.Mode != SubscriptionModeTemplate {
		t.Fatalf("mode = %q", got.Mode)
	}
}

func TestEffectiveProfileAssignedAppliesWhenUserHasNoChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, // 默认建议模式 = 未自选
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
	}
	got := EffectiveProfile(profile)
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
		AssignedPortableTemplateID: "acl4ssr-standard",
		AssignedMihomoTemplateID:   "builtin-mihomo",
		MihomoTemplateID:           "my-mihomo",
	}
	got := EffectiveProfile(profile)
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
	got := EffectiveProfile(profile)
	if got.PortableTemplateID != "acl4ssr-standard" || got.Mode != SubscriptionModeTemplate {
		t.Fatalf("forced portable not applied: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
	if got.MihomoTemplateID != "builtin-mihomo" {
		t.Fatalf("forced mihomo not applied: %q", got.MihomoTemplateID)
	}
}

func TestEffectiveProfileAssignedSuggestedPresetApplies(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:            `["ai"]`,
		AssignedSuggestedPreset:   "comprehensive",
		AssignForcedPortable:      false,
	}
	got := EffectiveProfile(profile)
	if got.Mode != SubscriptionModeSuggested || got.Preset != "comprehensive" {
		t.Fatalf("assigned suggested not applied: mode=%q preset=%q", got.Mode, got.Preset)
	}
	var categories []string
	if err := json.Unmarshal([]byte(got.CategoriesJSON), &categories); err != nil {
		t.Fatal(err)
	}
	if len(categories) != len(presetCategorySets["comprehensive"]) {
		t.Fatalf("preset categories not applied: %v", categories)
	}
	for _, want := range presetCategorySets["comprehensive"] {
		found := false
		for _, category := range categories {
			if category == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("preset categories missing %q: %v", want, categories)
		}
	}
}

func TestEffectiveProfileUserTemplateWinsOverSuggestedAssignment(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		AssignedSuggestedPreset: "minimal",
	}
	got := EffectiveProfile(profile)
	if got.Mode != SubscriptionModeTemplate || got.PortableTemplateID != "mine" {
		t.Fatalf("user choice overridden by suggested assignment: mode=%q portable=%q", got.Mode, got.PortableTemplateID)
	}
}

func TestEffectiveProfileForcedSuggestedOverridesUserChoice(t *testing.T) {
	profile := SubscriptionProfile{
		Mode: SubscriptionModeTemplate, PortableTemplateID: "mine",
		AssignedSuggestedPreset: "minimal",
		AssignForcedPortable:    true,
	}
	got := EffectiveProfile(profile)
	if got.Mode != SubscriptionModeSuggested || got.Preset != "minimal" {
		t.Fatalf("forced suggested not applied: mode=%q preset=%q", got.Mode, got.Preset)
	}
	if got.PortableTemplateID != "mine" {
		t.Fatalf("user portable value lost: %q", got.PortableTemplateID)
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
		GenerationStatus:          SubscriptionGenerationMissing,
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

func TestSubscriptionProfilePersistsSuggestedPresetAssignment(t *testing.T) {
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
		CategoriesJSON:                  `["ai"]`,
		AssignedSuggestedPreset:         "minimal", AssignForcedPortable: true,
		AssignedSuggestedCategories:     `["private","domestic","overseas"]`,
		GenerationStatus: SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedSuggestedPreset != "minimal" || got.AssignedSuggestedCategories != `["private","domestic","overseas"]` || !got.AssignForcedPortable {
		t.Fatalf("suggested assignment lost: %+v", got)
	}
	if got.AssignedPortableTemplateID != "" {
		t.Fatalf("template slot unexpectedly set: %+v", got)
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
		GenerationStatus:          SubscriptionGenerationMissing,
	}
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSubscriptionTemplate(ctx, "tpl-1"); err == nil {
		t.Fatal("assigned template deletion unexpectedly succeeded")
	}
}
