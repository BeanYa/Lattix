package sub

import (
	"strings"
	"testing"
)

func TestExpandPolicyPrunesEmptyRegionGroups(t *testing.T) {
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"♻️ 自动选择", "🇰🇷 韩国节点", "DIRECT"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
			{Name: "🇰🇷 韩国节点", Type: "url-test", Options: []string{"__LATTIX_REGION_KR__"}},
			{Name: "🇺🇸 美国节点", Type: "url-test", Options: []string{"__LATTIX_REGION_US__"}},
		},
		Rules: []policyRule{
			{Kind: "GEOSITE", Value: "youtube", Outbound: "🚀 节点选择"},
			{Kind: "GEOSITE", Value: "korea-only", Outbound: "🇰🇷 韩国节点"},
		},
	}
	nodes := []compiledNode{
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1"}},
	}
	warnings, err := expandPolicy(&policy, nodes)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, group := range policy.Groups {
		groups[group.Name] = group.Options
	}
	if _, exists := groups["🇰🇷 韩国节点"]; exists {
		t.Fatalf("empty region group was not pruned: %+v", groups)
	}
	if _, exists := groups["🇺🇸 美国节点"]; !exists {
		t.Fatalf("populated region group was pruned: %+v", groups)
	}
	main := groups["🚀 节点选择"]
	if len(main) != 2 || main[0] != "♻️ 自动选择" || main[1] != "DIRECT" {
		t.Fatalf("dangling reference kept: %+v", main)
	}
	if len(policy.Rules) != 1 || policy.Rules[0].Outbound != "🚀 节点选择" {
		t.Fatalf("rule to pruned group kept: %+v", policy.Rules)
	}
	joined := strings.Join(warnings, "; ")
	if !strings.Contains(joined, "🇰🇷 韩国节点") {
		t.Fatalf("prune warning missing: %q", joined)
	}
}

func TestExpandPolicyKeepsMatchingRegionGroup(t *testing.T) {
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"🇩🇪 德国节点", "DIRECT"}},
			{Name: "🇩🇪 德国节点", Type: "url-test", Options: []string{"__LATTIX_REGION_DE__"}},
		},
	}
	nodes := []compiledNode{
		{Name: "DE-1", CountryCode: "DE", Clash: clashProxy{Name: "DE-1"}},
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1"}},
	}
	if _, err := expandPolicy(&policy, nodes); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, group := range policy.Groups {
		groups[group.Name] = group.Options
	}
	de, exists := groups["🇩🇪 德国节点"]
	if !exists {
		t.Fatalf("region group with nodes was pruned: %+v", groups)
	}
	if len(de) != 1 || de[0] != "DE-1" {
		t.Fatalf("region group options = %+v, want [DE-1]", de)
	}
}

func TestExpandPolicyErrorsWhenFinalIsPruned(t *testing.T) {
	policy := portablePolicy{
		Final: "🇰🇷 韩国节点",
		Groups: []policyGroup{
			{Name: "🇰🇷 韩国节点", Type: "url-test", Options: []string{"__LATTIX_REGION_KR__"}},
		},
	}
	_, err := expandPolicy(&policy, []compiledNode{
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "🇰🇷 韩国节点") {
		t.Fatalf("error = %v, want pruned final error", err)
	}
}

func TestExpandPolicySkipsPruneWithoutNodes(t *testing.T) {
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"♻️ 自动选择"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
		},
	}
	warnings, err := expandPolicy(&policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(policy.Groups) != 2 {
		t.Fatalf("node-less policy was pruned: warnings=%v groups=%d", warnings, len(policy.Groups))
	}
}

func TestExpandPolicyMapsGermanRegionNotAllNodes(t *testing.T) {
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"🇩🇪 德国节点", "DIRECT"}},
			{Name: "🇩🇪 德国节点", Type: "url-test", Options: []string{"__LATTIX_REGION_DE__"}},
		},
	}
	nodes := []compiledNode{
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1"}},
	}
	warnings, err := expandPolicy(&policy, nodes)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range policy.Groups {
		if group.Name == "🇩🇪 德国节点" {
			t.Fatalf("empty DE group should be pruned, got %+v", group)
		}
	}
	if !strings.Contains(strings.Join(warnings, "; "), "🇩🇪 德国节点") {
		t.Fatalf("DE prune warning missing: %v", warnings)
	}
}
