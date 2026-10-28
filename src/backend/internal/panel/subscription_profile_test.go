package panel

import (
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

func TestRoutingSlotsSuggestedCategories(t *testing.T) {
	slots, err := routingSlotsFromInput(&subscriptionProfileInput{
		Mode: store.SubscriptionModeSuggested, Categories: []string{"ai", "gaming"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slots.assignedSuggested != `["ai","gaming"]` || slots.assignedPortable != "" {
		t.Fatalf("suggested slots = %+v", slots)
	}
	if slots.forcedPortable {
		t.Fatalf("forced defaulted true: %+v", slots)
	}
}

func TestRoutingSlotsEmptyCategoriesClearsPanelMain(t *testing.T) {
	// 建议模式 + 空分组 = 清除面板级主策略（用户回落默认级），不是错误。
	slots, err := routingSlotsFromInput(&subscriptionProfileInput{
		Mode: store.SubscriptionModeSuggested, Categories: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slots.assignedSuggested != "" || slots.assignedPortable != "" {
		t.Fatalf("expected cleared main slot, got %+v", slots)
	}
}

func TestRoutingSlotsForcedFlagFollowsCheckbox(t *testing.T) {
	slots, err := routingSlotsFromInput(&subscriptionProfileInput{
		Mode: store.SubscriptionModeTemplate, PortableTemplateID: "tpl-1", Forced: true,
		MihomoTemplateID: "tpl-mihomo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slots.assignedPortable != "tpl-1" || slots.assignedSuggested != "" {
		t.Fatalf("template slots = %+v", slots)
	}
	if !slots.forcedPortable || !slots.forcedMihomo {
		t.Fatalf("forced flags not set: %+v", slots)
	}
}

func TestRoutingSlotsRejectsUnknownCategoryAndMode(t *testing.T) {
	for _, input := range []subscriptionProfileInput{
		{Mode: "weird"},
		{Mode: store.SubscriptionModeSuggested, Categories: []string{"unknown"}},
	} {
		if _, err := routingSlotsFromInput(&input); err == nil {
			t.Fatalf("invalid routing accepted: %+v", input)
		}
	}
}

func TestApplyRoutingSlotsPreservesUserLevel(t *testing.T) {
	profile := store.SubscriptionProfile{
		Mode: store.SubscriptionModeTemplate, PortableTemplateID: "user-own",
		UserConfigured: true,
	}
	slots := routingSlotValues{
		assignedPortable: "panel-tpl", forcedPortable: true,
		disableRegionGroups: true, regionGroupsTouched: true,
	}
	applyRoutingSlots(&profile, slots)
	if profile.PortableTemplateID != "user-own" || !profile.UserConfigured {
		t.Fatalf("user-level fields touched by panel save: %+v", profile)
	}
	if profile.AssignedPortableTemplateID != "panel-tpl" || !profile.AssignForcedPortable {
		t.Fatalf("panel slots not applied: %+v", profile)
	}
	if !profile.DisableRegionGroups {
		t.Fatalf("region groups flag not applied: %+v", profile)
	}
}

// TestSubscriptionProfileDTOSerializesEmptySuggestedCategoriesAsArray 回归：
// 未指派建议规则的用户（列空串）DTO 必须输出 [] 而非 null，
// 否则前端 TemplateAssignmentTab 读取 .length 崩溃。
func TestSubscriptionProfileDTOSerializesEmptySuggestedCategoriesAsArray(t *testing.T) {
	dto := subscriptionProfileToDTO(store.SubscriptionProfile{})
	if dto.AssignedSuggestedCategories == nil {
		t.Fatal("assigned_suggested_categories = nil, want empty non-nil slice")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"assigned_suggested_categories":null`) {
		t.Fatalf("DTO serializes assigned_suggested_categories as null: %s", raw)
	}
}
