package panel

import (
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

func TestProfileFromInputPreservesEmptySuggestedCategories(t *testing.T) {
	profile, err := profileFromInput(1, &subscriptionProfileInput{
		Mode: store.SubscriptionModeSuggested, Preset: "balanced", Categories: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.CategoriesJSON != "[]" {
		t.Fatalf("categories = %s, want []", profile.CategoriesJSON)
	}
}

func TestProfileFromInputRejectsUnknownCategoryAndPreset(t *testing.T) {
	for _, input := range []subscriptionProfileInput{
		{Mode: store.SubscriptionModeSuggested, Preset: "unknown"},
		{Mode: store.SubscriptionModeSuggested, Preset: "balanced", Categories: []string{"unknown"}},
	} {
		if _, err := profileFromInput(1, &input); err == nil {
			t.Fatalf("invalid profile accepted: %+v", input)
		}
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
