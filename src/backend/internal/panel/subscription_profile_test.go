package panel

import (
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
