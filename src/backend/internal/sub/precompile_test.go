package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/shared"
	"lattix/backend/internal/store"
)

// 预编译测试共用策略：节点选择 + 自动选择，经 expandPolicy 展开后含来源分组。
func precompileTestPolicy(t *testing.T, nodes []compiledNode) portablePolicy {
	t.Helper()
	policy := portablePolicy{
		Final: "🚀 节点选择",
		Groups: []policyGroup{
			{Name: "🚀 节点选择", Type: "select", Options: []string{"__LATTIX_REGIONS__", "__LATTIX_ALL__"}},
			{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}},
		},
	}
	if _, err := expandPolicy(&policy, nodes, "Lattix"); err != nil {
		t.Fatal(err)
	}
	return policy
}

// precompileTestNodes 构造 1 个面板节点 + 3 条外部订阅各 10 个节点。
func precompileTestNodes() []compiledNode {
	nodes := []compiledNode{{Name: "链-香港", CountryCode: "HK", Clash: clashProxy{Name: "链-香港", Type: "vless", Server: "hk.example.com", Port: 443}}}
	for _, sub := range []string{"机场A", "机场B", "机场C"} {
		for i := 1; i <= 10; i++ {
			name := sub + "-节点" + strings.Repeat("x", i)
			nodes = append(nodes, compiledNode{Name: name, Group: sub, Clash: clashProxy{Name: name, Type: "ss", Server: "outer.example.com", Port: 8388}})
		}
	}
	return nodes
}

func TestMihomoPrecompilePlaceholdersAndExpansion(t *testing.T) {
	nodes := precompileTestNodes()
	policy := precompileTestPolicy(t, nodes)
	pre := renderMihomoPre(policy)
	artifact, err := pre.artifact()
	if err != nil {
		t.Fatal(err)
	}
	text := string(artifact)
	for _, token := range precompiledPlaceholders {
		if !strings.Contains(text, token) {
			t.Fatalf("pre-compiled artifact missing %s:\n%s", token, text)
		}
	}
	// 选项级占位符在中间态保持原样，最终渲染才展开为节点/分组名。
	for _, token := range []string{placeholderAllNodes, placeholderLeafGroups} {
		if !strings.Contains(text, token) {
			t.Fatalf("pre-compiled artifact missing option placeholder %s:\n%s", token, text)
		}
	}
	final, err := renderMihomo(policy, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertNoPrecompiledPlaceholders("clash", final); err != nil {
		t.Fatalf("final content has residue: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(final, &doc); err != nil {
		t.Fatal(err)
	}
	proxies, _ := doc["proxies"].([]any)
	if len(proxies) != 31 {
		t.Fatalf("proxies = %d, want 31 (1 panel + 3*10 outer)", len(proxies))
	}
	groups, _ := doc["proxy-groups"].([]any)
	names := map[string]bool{}
	for _, entry := range groups {
		group, _ := entry.(map[string]any)
		names[group["name"].(string)] = true
	}
	for _, want := range []string{"Lattix 分组", "机场A", "机场B", "机场C"} {
		if !names[want] {
			t.Fatalf("final proxy-groups missing %s: %v", want, names)
		}
	}
}

func TestSingboxPrecompilePlaceholdersAndExpansion(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", Clash: clashProxy{Name: "链-香港"}, Singbox: map[string]any{"type": "vless", "tag": "链-香港"}},
		{Name: "外部-1", Group: "机场A", Clash: clashProxy{Name: "外部-1"}, Singbox: map[string]any{"type": "ss", "tag": "外部-1"}},
	}
	policy := precompileTestPolicy(t, nodes)
	pre := renderSingboxPre(policy)
	artifact, err := pre.artifact()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range precompiledPlaceholders {
		if !strings.Contains(string(artifact), token) {
			t.Fatalf("singbox pre-compiled artifact missing %s:\n%s", token, artifact)
		}
	}
	final, err := renderSingbox(policy, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertNoPrecompiledPlaceholders("singbox", final); err != nil {
		t.Fatalf("final content has residue: %v", err)
	}
	if !strings.Contains(string(final), "机场A") || !strings.Contains(string(final), "Lattix 分组") {
		t.Fatalf("final missing source groups: %s", final)
	}
}

func TestQuanXPrecompilePlaceholdersAndExpansion(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", Clash: clashProxy{Name: "链-香港"}, QuanX: "vless=hk.example.com:443"},
		{Name: "外部-1", Group: "机场A", Clash: clashProxy{Name: "外部-1"}, QuanX: "shadowsocks=outer.example.com:8388"},
	}
	policy := precompileTestPolicy(t, nodes)
	pre := renderQuanXConfigPre(policy)
	text := pre.artifact()
	for _, token := range precompiledPlaceholders {
		if !strings.Contains(string(text), token) {
			t.Fatalf("quanx-config pre-compiled artifact missing %s:\n%s", token, text)
		}
	}
	final := pre.expand(nodes)
	if err := assertNoPrecompiledPlaceholders("quanx-config", final); err != nil {
		t.Fatalf("final content has residue: %v", err)
	}
	if !strings.Contains(string(final), "vless=hk.example.com:443") ||
		!strings.Contains(string(final), "shadowsocks=outer.example.com:8388") ||
		!strings.Contains(string(final), "static=机场A, 外部-1") {
		t.Fatalf("expanded quanx-config wrong:\n%s", final)
	}
	// quanx 纯节点格式同样两阶段。
	if !strings.Contains(renderQuanXNodesPre(), placeholderLattixNodes) {
		t.Fatal("quanx nodes pre missing placeholder")
	}
	if err := assertNoPrecompiledPlaceholders("quanx", []byte(renderQuanXNodes(nodes))); err != nil {
		t.Fatal(err)
	}
}

// 空来源（无外部订阅）时占位符条目删除，不产出空分组。
func TestPrecompileDropsEmptySource(t *testing.T) {
	nodes := []compiledNode{{Name: "链-香港", Clash: clashProxy{Name: "链-香港"}, QuanX: "vless=hk:443"}}
	policy := precompileTestPolicy(t, nodes)
	final, err := renderMihomo(policy, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(final), placeholderOuterSubsGroup) || strings.Contains(string(final), placeholderOuterSubsNodes) {
		t.Fatalf("outer placeholders should be dropped:\n%s", final)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(final, &doc); err != nil {
		t.Fatal(err)
	}
	if proxies, _ := doc["proxies"].([]any); len(proxies) != 1 {
		t.Fatalf("proxies = %d, want 1", len(proxies))
	}
	// quanx-config 中 __OUTER-SUBS-GROUP__ 占位符行被删除。
	quanxFinal := renderQuanXConfigPre(policy).expand(nodes)
	if strings.Contains(string(quanxFinal), placeholderOuterSubsGroup) {
		t.Fatalf("quanx outer group placeholder not dropped:\n%s", quanxFinal)
	}
}

// 原生模板手写条目占位符控制注入位置。
func TestNativeTemplateEntryPlaceholders(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", Clash: clashProxy{Name: "链-香港", Type: "vless", Server: "hk", Port: 443}},
		{Name: "外部-1", Group: "机场A", Clash: clashProxy{Name: "外部-1", Type: "ss", Server: "o", Port: 8388}},
	}
	template := `proxies:
  - __OUTTER-SUBS-NODES__
  - __LATTIX-NODES__
proxy-groups:
  - {name: Main, type: select, proxies: [__LATTIX_ALL__]}
  - __OUTER-SUBS-GROUP__
  - __LATTIX-GROUP__
rules:
  - MATCH,Main
`
	pre, final, err := applyNativeTemplateFull("clash", template, portablePolicy{Final: "Main"}, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pre), placeholderLattixNodes) {
		t.Fatalf("pre should keep placeholders:\n%s", pre)
	}
	if err := assertNoPrecompiledPlaceholders("clash", final); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(final, &doc); err != nil {
		t.Fatal(err)
	}
	proxies, _ := doc["proxies"].([]any)
	if len(proxies) != 2 {
		t.Fatalf("proxies = %d, want 2", len(proxies))
	}
	// 模板手写的顺序：外部节点在前、面板节点在后。
	first, _ := proxies[0].(map[string]any)
	if first["name"] != "外部-1" {
		t.Fatalf("proxies[0] = %v, want 外部-1（模板占位符顺序生效）", first["name"])
	}
	groups, _ := doc["proxy-groups"].([]any)
	var names []string
	for _, entry := range groups {
		group, _ := entry.(map[string]any)
		names = append(names, group["name"].(string))
	}
	// __OUTER-SUBS-GROUP__ 在 __LATTIX-GROUP__ 之前 → 机场A 先于 Lattix 分组。
	idxA, idxL := -1, -1
	for i, name := range names {
		if name == "机场A" {
			idxA = i
		}
		if name == "Lattix 分组" {
			idxL = i
		}
	}
	if idxA < 0 || idxL < 0 || idxA > idxL {
		t.Fatalf("group order = %v, want 机场A before Lattix 分组", names)
	}
}

// 原生模板不写占位符时保持既有注入行为（节点整体替换、来源分组追加）。
func TestNativeTemplateWithoutPlaceholdersKeepsDefault(t *testing.T) {
	nodes := []compiledNode{
		{Name: "链-香港", Clash: clashProxy{Name: "链-香港", Type: "vless", Server: "hk", Port: 443}},
		{Name: "外部-1", Group: "机场A", Clash: clashProxy{Name: "外部-1", Type: "ss", Server: "o", Port: 8388}},
	}
	template := `proxy-groups:
  - {name: Main, type: select, proxies: [__LATTIX_ALL__]}
rules:
  - MATCH,Main
`
	final, err := applyNativeTemplate("clash", template, portablePolicy{Final: "Main"}, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	if err := assertNoPrecompiledPlaceholders("clash", final); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(final), "链-香港") || !strings.Contains(string(final), "机场A") {
		t.Fatalf("default injection missing nodes/groups:\n%s", final)
	}
}

func TestLinksPrecompile(t *testing.T) {
	pre := linksPre{text: placeholderLattixNodes + "\n" + placeholderOuterSubsNodes + "\n", panelLinks: []string{"vless://a"}, outerLinks: []string{"ss://b"}}
	final := pre.expand()
	if err := assertNoPrecompiledPlaceholders("links", final); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(final)))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "vless://a\nss://b" {
		t.Fatalf("links = %q, want panel link then outer link", decoded)
	}
}

// 发布快照同时落盘 <format>-pre 中间态文件，交付文件不含占位符。
func TestPublishUserStoresPrecompiledArtifacts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, "edge", "edge.example.com", "server-token", store.MachineTypeDirect, "", "", "US", "")
	if err != nil {
		t.Fatal(err)
	}
	virtual, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "US node", serverID, shared.ProtocolVLESS, nil, virtual)
	if err != nil {
		t.Fatal(err)
	}
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 443, Network: shared.NetworkTCP, PublicKey: "public-key", ShortID: "abcd", ServerName: "edge.example.com"})
	if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000009", "user-token-pre", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != len(subscriptionFormats) {
		t.Fatalf("visible files = %d, want %d（-pre 不进入交付集）", len(result.Files), len(subscriptionFormats))
	}
	for _, format := range subscriptionFormats {
		if err := assertNoPrecompiledPlaceholders(format, result.Files[format]); err != nil {
			t.Fatal(err)
		}
		preFile, err := st.PublishedSubscriptionFile(ctx, userID, format+"-pre")
		if err != nil {
			t.Fatalf("pre-compiled artifact for %s missing: %v", format, err)
		}
		if !strings.Contains(string(preFile.Content), placeholderLattixNodes) {
			t.Fatalf("pre-compiled %s missing node placeholder:\n%s", format, preFile.Content)
		}
	}
}
