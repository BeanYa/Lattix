package sub

import (
	"context"
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
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
	warnings, err := expandPolicy(&policy, nodes, "Lattix")
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
	if _, err := expandPolicy(&policy, nodes, "Lattix"); err != nil {
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
	}, "Lattix")
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
	warnings, err := expandPolicy(&policy, nil, "Lattix")
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
	warnings, err := expandPolicy(&policy, nodes, "Lattix")
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

func TestInferNodeCountry(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"🇯🇵 东京 01", "JP"},
		{"🇭🇰 HK IPLC", "HK"},
		{"东京 01", ""},      // 关键词表不含「东京」，靠旗帜或地区名
		{"香港 03", "HK"},    // 关键词命中
		{"美国-洛杉矶", "US"},   // 关键词命中
		{"Relay Node", ""}, // 无法推断
		{"", ""},
	}
	for _, tc := range cases {
		if got := inferNodeCountry(tc.name); got != tc.want {
			t.Errorf("inferNodeCountry(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// 外部订阅节点与面板节点同级参与区域分组：名称可推断国家时进入对应
// __LATTIX_REGION_XX__ 组，而不只出现在 __LATTIX_ALL__（自动选择）里。
func TestExpandPolicyGroupsExternalNodesByInferredCountry(t *testing.T) {
	ext := extsub.Node{
		Name: "🇯🇵 东京 01", Type: "vless", Server: "1.2.3.4", Port: 443,
		Extra: map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
			"security": "reality", "pbk": "pub", "sid": "abcd",
		},
	}
	s := &Server{}
	nodes, warnings, err := s.compileNodes(context.Background(), []proxyItem{{external: &ext}}, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("compile warnings: %v", warnings)
	}
	if len(nodes) != 1 || nodes[0].CountryCode != "JP" {
		t.Fatalf("external node country = %+v, want JP", nodes)
	}
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"♻️ 自动选择", "🇯🇵 日本节点"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
			{Name: "🇯🇵 日本节点", Type: "url-test", Options: []string{"__LATTIX_REGION_JP__"}},
		},
	}
	if _, err := expandPolicy(&policy, nodes, "Lattix"); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, group := range policy.Groups {
		groups[group.Name] = group.Options
	}
	jp, exists := groups["🇯🇵 日本节点"]
	if !exists || len(jp) != 1 || jp[0] != "🇯🇵 东京 01" {
		t.Fatalf("external node missing from region group: %+v", groups)
	}
	auto := groups["♻️ 自动选择"]
	if len(auto) != 1 || auto[0] != "🇯🇵 东京 01" {
		t.Fatalf("external node missing from all-nodes group: %+v", auto)
	}
	if _, exists := groups[noRegionGroupName]; exists {
		t.Fatalf("no-region group should not exist when every node has a country: %+v", groups)
	}
}

// 无地区标识的节点收进固定的「无地区」分组，并随 __LATTIX_REGIONS__ 展开，
// 保证所有节点（含无法推断国家的外部订阅节点）在分组层都可达。
func TestExpandPolicyGroupsNodesWithoutCountryIntoNoRegionGroup(t *testing.T) {
	ext := extsub.Node{
		Name: "Relay 01", Type: "ss", Server: "1.2.3.4", Port: 8388,
		Extra: map[string]any{"method": "aes-128-gcm", "password": "p"},
	}
	s := &Server{}
	compiled, warnings, err := s.compileNodes(context.Background(), []proxyItem{{external: &ext}}, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("compile warnings: %v", warnings)
	}
	nodes := append([]compiledNode{
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1"}},
	}, compiled...)
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"♻️ 自动选择", "__LATTIX_REGIONS__"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
		},
	}
	if _, err := expandPolicy(&policy, nodes, "Lattix"); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, group := range policy.Groups {
		groups[group.Name] = group.Options
	}
	noRegion, exists := groups[noRegionGroupName]
	if !exists || len(noRegion) != 1 || noRegion[0] != "Relay 01" {
		t.Fatalf("no-region group = %+v, want [Relay 01]: %+v", noRegion, groups)
	}
	main := groups["🚀 节点选择"]
	if len(main) != 3 || main[0] != "♻️ 自动选择" || main[1] != "🇺🇸 US" || main[2] != noRegionGroupName {
		t.Fatalf("regions expansion = %+v, want [自动选择, 🇺🇸 US, 无地区]", main)
	}
	auto := groups["♻️ 自动选择"]
	if len(auto) != 2 || auto[0] != "US-1" || auto[1] != "Relay 01" {
		t.Fatalf("all-nodes group = %+v", auto)
	}
}

// 原生模板展开与便携策略一致：无地区节点收进「无地区」分组，排在地区分组最后。
func TestNativeNodeOptionsIncludeNoRegionGroup(t *testing.T) {
	nodes := []compiledNode{
		{Name: "US-1", CountryCode: "US"},
		{Name: "Relay 01"},
	}
	all, regions, byCountry := nativeNodeOptions(nodes)
	if len(all) != 2 {
		t.Fatalf("all = %+v, want 2 nodes", all)
	}
	if len(regions) != 2 || regions[0] != "🇺🇸 US" || regions[1] != noRegionGroupName {
		t.Fatalf("regions = %+v, want [🇺🇸 US, 无地区]", regions)
	}
	if got := byCountry[noRegionGroupName]; len(got) != 1 || got[0] != "Relay 01" {
		t.Fatalf("byCountry[无地区] = %+v, want [Relay 01]", got)
	}
}

// 来源分组：面板管理节点进「<panelShort> 分组」，每个外部订阅按订阅名成组；
// 无节点的来源不生成组，与既有策略组重名时跳过并告警。
func TestExpandPolicySourceGroups(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", CountryCode: "HK", Clash: clashProxy{Name: "链-香港"}},
		{Name: "🇯🇵 东京 01", CountryCode: "JP", Group: "机场X", Clash: clashProxy{Name: "🇯🇵 东京 01"}},
		{Name: "Relay 01", Group: "机场X", Clash: clashProxy{Name: "Relay 01"}},
		{Name: "🇺🇸 美西 01", CountryCode: "US", Group: "机场Y", Clash: clashProxy{Name: "🇺🇸 美西 01"}},
	}
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"♻️ 自动选择"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
		},
	}
	warnings, err := expandPolicy(&policy, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	groups := map[string][]string{}
	order := []string{}
	for _, group := range policy.Groups {
		groups[group.Name] = group.Options
		order = append(order, group.Name)
	}
	if got := groups["Lattix 分组"]; len(got) != 1 || got[0] != "链-香港" {
		t.Fatalf("panel group = %+v, want [链-香港]", got)
	}
	if got := groups["机场X"]; len(got) != 2 || got[0] != "🇯🇵 东京 01" || got[1] != "Relay 01" {
		t.Fatalf("outer group 机场X = %+v", got)
	}
	if got := groups["机场Y"]; len(got) != 1 || got[0] != "🇺🇸 美西 01" {
		t.Fatalf("outer group 机场Y = %+v", got)
	}
	// 来源分组排在地区分组之前，模板组之后；Relay 01 无地区标识，末尾再跟无地区分组。
	wantOrder := []string{"🚀 节点选择", "♻️ 自动选择", "Lattix 分组", "机场X", "机场Y", "🇭🇰 HK", "🇯🇵 JP", "🇺🇸 US", noRegionGroupName}
	if strings.Join(order, "|") != strings.Join(wantOrder, "|") {
		t.Fatalf("group order = %v, want %v", order, wantOrder)
	}
}

// 来源分组名与既有策略组重名时跳过自动生成（避免非法重复组名）。
func TestExpandPolicySourceGroupNameCollision(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", CountryCode: "HK", Clash: clashProxy{Name: "链-香港"}},
	}
	policy := portablePolicy{
		Final: "Lattix 分组",
		Groups: []policyGroup{
			{Name: "Lattix 分组", Type: "select", Options: []string{"DIRECT"}},
		},
	}
	warnings, err := expandPolicy(&policy, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, group := range policy.Groups {
		if group.Name == "Lattix 分组" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate source group appended: %+v", policy.Groups)
	}
	if !strings.Contains(strings.Join(warnings, "; "), "Lattix 分组") {
		t.Fatalf("collision warning missing: %v", warnings)
	}
}
