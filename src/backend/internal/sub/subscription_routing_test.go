package sub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestParseACL4SSROfficialRoutingSyntax(t *testing.T) {
	policy, err := parseACL4SSR(`[custom]
ruleset=Direct,[]GEOIP,CN
ruleset=Proxy,https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/ProxyLite.list
ruleset=Final,[]FINAL
custom_proxy_group=Proxy` + "`select`[]Auto`[]DIRECT`.*" + `
custom_proxy_group=Auto` + "`url-test`.*`https://www.gstatic.com/generate_204`300,,50" + `
custom_proxy_group=Direct` + "`select`[]DIRECT`[]Proxy" + `
custom_proxy_group=Final` + "`select`[]Proxy`[]Direct" + `
enable_rule_generator=true
overwrite_original_rules=true`)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Final != "Final" || len(policy.Rules) != 1 || policy.Rules[0].Kind != "GEOIP" || policy.Rules[0].Value != "CN" {
		t.Fatalf("local routing = final %q, rules %+v", policy.Final, policy.Rules)
	}
	if len(policy.RemoteRule) != 1 || policy.RemoteRule[0].Name != "acl-001" {
		t.Fatalf("remote rules = %+v", policy.RemoteRule)
	}
	if got := policy.Groups[0].Options; len(got) != 2 || got[0] != "Auto" || got[1] != "DIRECT" {
		t.Fatalf("select options = %#v", got)
	}
	if policy.Groups[1].Interval != 300 || policy.Groups[1].Tolerance != 50 {
		t.Fatalf("url-test timing = interval %d tolerance %d", policy.Groups[1].Interval, policy.Groups[1].Tolerance)
	}
}

func TestACL4SSRRelativeRulesStayInAuthorRepository(t *testing.T) {
	policy, err := parseACL4SSR("ruleset=Direct,rules/ACL4SSR/Clash/LocalAreaNetwork.list\n" +
		"ruleset=Final,[]FINAL\ncustom_proxy_group=Direct`select`[]DIRECT\n" +
		"custom_proxy_group=Final`select`[]Direct\n")
	if err != nil {
		t.Fatal(err)
	}
	template := store.SubscriptionTemplate{
		SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR.ini",
	}
	got, err := normalizeTemplateRuleSourceURL(template, policy.RemoteRule[0].URL)
	want := "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/LocalAreaNetwork.list"
	if err != nil || got != want {
		t.Fatalf("relative rule URL = %q, err %v", got, err)
	}
	for _, raw := range []string{
		"rules/OtherProject/Clash/rule.list",
		"rules/ACL4SSR/../secret.list",
		"../rules/ACL4SSR/Clash/rule.list",
	} {
		if _, err := normalizeTemplateRuleSourceURL(template, raw); err == nil {
			t.Fatalf("unsafe relative rule %q accepted", raw)
		}
	}
}

func TestParseACL4SSRRejectsUnknownRoutingDirectiveWithLine(t *testing.T) {
	_, err := parseACL4SSR("custom_rule_generator=true\n")
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %v, want line-numbered routing error", err)
	}
}

func TestGitHubTemplateURLRestrictions(t *testing.T) {
	got, err := normalizeGitHubURL("https://github.com/ACL4SSR/ACL4SSR/blob/master/Clash/config/ACL4SSR.ini")
	if err != nil || got != "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR.ini" {
		t.Fatalf("normalized URL = %q, err %v", got, err)
	}
	cdn := "clash-domain:https://testingcf.jsdelivr.net/gh/Aethersailor/Custom_OpenClash_Rules@main/rule/Custom_Direct_Domain.mrs"
	want := "https://raw.githubusercontent.com/Aethersailor/Custom_OpenClash_Rules/main/rule/Custom_Direct_Domain.yaml"
	if got, err := normalizeRuleSourceURL(cdn); err != nil || got != want {
		t.Fatalf("normalized official CDN rule = %q, err %v", got, err)
	}
	for _, raw := range []string{"http://github.com/a/b/blob/main/x", "https://example.com/a", "https://github.com/a/b"} {
		if _, err := normalizeGitHubURL(raw); err == nil {
			t.Fatalf("unsafe URL %q accepted", raw)
		}
	}
}

type fakeFileRequester struct {
	content map[string]string
	errors  map[string]error
}

func (f *fakeFileRequester) GetText(_ context.Context, url string, _ int64) (string, error) {
	if err := f.errors[url]; err != nil {
		return "", err
	}
	content, ok := f.content[url]
	if !ok {
		return "", errors.New("unexpected URL: " + url)
	}
	return content, nil
}

func (*fakeFileRequester) Download(context.Context, string, string, func(float64)) error { return nil }

func (*fakeFileRequester) DownloadLimited(context.Context, string, string, int64, func(float64)) error {
	return nil
}

func TestTemplateRefreshPreservesLastCompleteCache(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := New(st, nil, nil)
	templateURL := "https://raw.githubusercontent.com/example/project/main/template.yaml"
	ruleURL := "https://raw.githubusercontent.com/example/project/main/rule.list"
	templateBody := `name: Cached
groups:
  - name: Proxy
    type: select
    options: [DIRECT]
rules: []
remote_rules:
  - name: sample
    url: ` + ruleURL + `
    behavior: classical
    outbound: Proxy
final: Proxy
`
	fake := &fakeFileRequester{content: map[string]string{
		templateURL: templateBody,
		ruleURL:     "DOMAIN-SUFFIX,example.com\nIP-CIDR,192.0.2.0/24,no-resolve\n",
	}, errors: map[string]error{}}
	server.files = fake
	if _, err := server.SaveTemplate(ctx, store.SubscriptionTemplate{
		ID: "refresh-test", Name: "refresh", Kind: "portable", SourceURL: templateURL,
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.RefreshTemplates(ctx, "refresh-test"); err != nil {
		t.Fatal(err)
	}
	first, _ := st.SubscriptionTemplateByID(ctx, "refresh-test")
	firstRules, _ := st.SubscriptionTemplateRules(ctx, first.ID, first.ContentSHA256)
	if first.Content != templateBody || len(firstRules) != 1 {
		t.Fatalf("initial cache = template hash %q, rules %d", first.ContentSHA256, len(firstRules))
	}
	fake.errors[ruleURL] = errors.New("upstream unavailable")
	fake.content[templateURL] = strings.Replace(templateBody, "name: Cached", "name: Updated", 1)
	if err := server.RefreshTemplates(ctx, "refresh-test"); err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	after, _ := st.SubscriptionTemplateByID(ctx, "refresh-test")
	afterRules, _ := st.SubscriptionTemplateRules(ctx, after.ID, after.ContentSHA256)
	if after.ContentSHA256 != first.ContentSHA256 || after.Content != first.Content || len(afterRules) != 1 {
		t.Fatalf("last good cache changed: template hash %q rules %d", after.ContentSHA256, len(afterRules))
	}
	if !strings.Contains(after.LastError, "upstream unavailable") {
		t.Fatalf("last error = %q", after.LastError)
	}
}

func TestSaveRemoteTemplateFetchesAtomically(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := New(st, nil, nil)
	templateURL := "https://raw.githubusercontent.com/example/project/main/template.yaml"
	templateBody := "name: Remote\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n"
	fake := &fakeFileRequester{content: map[string]string{templateURL: templateBody}, errors: map[string]error{}}
	server.files = fake

	saved, err := server.SaveTemplate(ctx, store.SubscriptionTemplate{
		ID: "remote-atomic", Name: "remote", Kind: "portable", SourceURL: templateURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Content != templateBody || saved.FetchedAt == nil || saved.ContentSHA256 == "" {
		t.Fatalf("remote template was not cached completely: %+v", saved)
	}

	fake.errors[templateURL] = errors.New("upstream unavailable")
	if _, err := server.SaveTemplate(ctx, store.SubscriptionTemplate{
		ID: saved.ID, Name: "changed", Kind: saved.Kind, SourceURL: templateURL,
	}); err == nil {
		t.Fatal("failed remote update unexpectedly succeeded")
	}
	after, err := st.SubscriptionTemplateByID(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != saved.Name || after.ContentSHA256 != saved.ContentSHA256 {
		t.Fatalf("failed remote update changed cached template: %+v", after)
	}
}

func TestRuleArtifactsAreClientNative(t *testing.T) {
	files, err := buildRuleArtifacts([]store.SubscriptionTemplateRule{{
		Name: "sample", ContentSHA256: strings.Repeat("a", 64),
		Content: []byte("# generated provider\npayload:\n  - '+.example.com'\n  - '*service*'\n  - 192.0.2.0/24\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("artifact count = %d", len(files))
	}
	joined := string(files[0].Content) + string(files[1].Content) + string(files[2].Content)
	for _, want := range []string{"payload:", "domain_suffix", "domain_keyword", "ip_cidr", "HOST-SUFFIX"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("artifacts missing %q: %s", want, joined)
		}
	}
}

func TestPublishUserCreatesAllFormatsAndDisabledSnapshot(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "edge", Address: "edge.example.com", BootstrapToken: "server-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
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
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000002", "user-token", nil)
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
	if len(result.Files) != 5 {
		t.Fatalf("published formats = %d", len(result.Files))
	}
	if !strings.Contains(string(result.Files["clash"]), "US node") {
		t.Fatalf("active snapshot missing node: %s", result.Files["clash"])
	}
	if err := st.SetUserDisabled(ctx, userID, true); err != nil {
		t.Fatal(err)
	}
	disabled, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disabled.Files["clash"]), "US node") {
		t.Fatalf("disabled snapshot retained node: %s", disabled.Files["clash"])
	}
}

// 回归：socks/http 面板节点曾因 buildSbOutbound 不支持协议被 compileNodes 连坐丢弃
// （全格式消失）；sing-box 原生支持两者，修复后各格式均须保留。
func TestPublishUserKeepsSocksHTTPNodesInSingbox(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "edge", Address: "edge.example.com", BootstrapToken: "server-token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeIDs := make([]int64, 0, 2)
	for _, protocol := range []string{shared.ProtocolSocks, shared.ProtocolHTTP} {
		virtual, _ := json.Marshal(shared.VirtualConfig{Protocol: protocol, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
		nodeID, err := st.InsertNode(ctx, protocol+" node", serverID, protocol, nil, virtual)
		if err != nil {
			t.Fatal(err)
		}
		realized, _ := json.Marshal(shared.RealizedConfig{Port: 1080, Network: shared.NetworkTCP})
		if err := st.SetNodeActive(ctx, nodeID, realized); err != nil {
			t.Fatal(err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000008", "user-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, nodeIDs); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected publish warnings: %v", result.Warnings)
	}
	singbox := string(result.Files["singbox"])
	for _, want := range []string{`"type": "socks"`, `"type": "http"`, `"username": "00000000-0000-0000-0000-000000000008"`} {
		if !strings.Contains(singbox, want) {
			t.Fatalf("singbox snapshot missing %s: %s", want, singbox)
		}
	}
	clash := string(result.Files["clash"])
	for _, want := range []string{"type: socks5", "type: http"} {
		if !strings.Contains(clash, want) {
			t.Fatalf("clash snapshot missing %s: %s", want, clash)
		}
	}
}

func TestPublishFailureKeepsPreviousSnapshot(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "stable", "00000000-0000-0000-0000-000000000004", "stable-token", nil)
	server := New(st, nil, nil)
	if _, err := server.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced", CategoriesJSON: "[]",
		PortableTemplateID: "missing", GenerationStatus: store.SubscriptionGenerationPending,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.PublishUser(ctx, userID, "https://panel.example"); err == nil {
		t.Fatal("invalid template publication unexpectedly succeeded")
	}
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil || file.Revision != 1 {
		t.Fatalf("published snapshot = revision %d, err %v", file.Revision, err)
	}
	status, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil || status.Status != store.SubscriptionGenerationError || status.Revision != 1 {
		t.Fatalf("generation status = %+v, err %v", status, err)
	}
}

func TestPublicSubscriptionAndRuleEndpointsReadSnapshotsOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "public", "00000000-0000-0000-0000-000000000003", "public-token", nil)
	server := New(st, nil, []byte("<html></html>"))
	mux := http.NewServeMux()
	mux.Handle("GET /sub/{token}", server)
	mux.HandleFunc("GET /sub/{token}/rules/{version}/{format}/{name}", server.ServeRuleHTTP)
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest("GET", "/sub/public-token?format=clash", nil))
	if missing.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing snapshot status = %d", missing.Code)
	}
	version := strings.Repeat("b", 64)
	_, err = st.PublishSubscriptionSnapshot(ctx, userID, "test", "source", map[string]store.SubscriptionFile{
		"clash": {Format: "clash", ContentType: "text/yaml", Content: []byte("proxies: []\n")},
	}, []store.SubscriptionRuleFile{{Name: "sample", Format: "mihomo", SourceSHA256: version, ContentType: "text/yaml", Content: []byte("payload: []\n")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := httptest.NewRecorder()
	mux.ServeHTTP(config, httptest.NewRequest("GET", "/sub/public-token?format=clash", nil))
	if config.Code != http.StatusOK || config.Header().Get("X-Lattix-Subscription-Revision") != "1" {
		t.Fatalf("config response = %d revision %q", config.Code, config.Header().Get("X-Lattix-Subscription-Revision"))
	}
	rule := httptest.NewRecorder()
	mux.ServeHTTP(rule, httptest.NewRequest("GET", "/sub/public-token/rules/"+version+"/mihomo/sample", nil))
	body, _ := io.ReadAll(rule.Result().Body)
	if rule.Code != http.StatusOK || string(body) != "payload: []\n" {
		t.Fatalf("rule response = %d %q", rule.Code, body)
	}
}

func TestDangerousNativeTemplateFieldsRejected(t *testing.T) {
	for _, test := range []struct{ kind, content string }{
		{"mihomo", "external-controller: 0.0.0.0:9090\n"},
		{"singbox", `{"experimental":{"clash_api":{"external_controller":"0.0.0.0:9090"}}}`},
		{"quanx", "[http_backend]\n__LATTIX_SERVERS__\n"},
	} {
		if err := validateTemplate(test.kind, test.content); err == nil {
			t.Fatalf("dangerous %s template accepted", test.kind)
		}
	}
}

func TestBuiltInTemplatesCannotBeOverwritten(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := New(st, nil, nil)
	_, err = server.SaveTemplate(context.Background(), store.SubscriptionTemplate{
		ID: "acl4ssr-standard", Name: "overwritten", Kind: "acl4ssr", Content: "[custom]\n",
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("overwrite error = %v", err)
	}
}

func TestMihomoNativeTemplateExpandsNodeAndRegionPlaceholders(t *testing.T) {
	content := `proxy-groups:
  - name: Main
    type: select
    proxies: [__LATTIX_ALL__, __LATTIX_REGIONS__, __LATTIX_REGION_US__]
rules: [MATCH,Main]
`
	nodes := []compiledNode{
		{Name: "US-1", CountryCode: "US", Clash: clashProxy{Name: "US-1", Type: "vless"}},
		{Name: "JP-1", CountryCode: "JP", Clash: clashProxy{Name: "JP-1", Type: "vless"}},
	}
	result, err := applyNativeTemplate("clash", content, portablePolicy{Final: "Main"}, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	if strings.Contains(text, "__LATTIX_") {
		t.Fatalf("placeholder remained in native template: %s", text)
	}
	var document struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(result, &document); err != nil {
		t.Fatal(err)
	}
	groupNames := map[string]bool{}
	for _, group := range document.Groups {
		groupNames[group.Name] = true
	}
	for _, want := range []string{"Main", "🇺🇸 US", "🇯🇵 JP"} {
		if !groupNames[want] {
			t.Fatalf("expanded template missing group %q: %+v", want, document.Groups)
		}
	}
}

func TestSingboxNativeTemplateMergesNodesAndPreservesRoute(t *testing.T) {
	content := `{
  "outbounds": [
    {"type":"direct","tag":"DIRECT","mark":"template-direct"},
    {"type":"block","tag":"REJECT","mark":"template-reject"},
    {"type":"selector","tag":"Main","outbounds":["__LATTIX_ALL__","__LATTIX_REGIONS__","__LATTIX_REGION_US__"]}
  ],
  "route": {"rules":[{"protocol":"dns","outbound":"DIRECT"}],"final":"Main"}
}`
	nodes := []compiledNode{
		{Name: "US-1", CountryCode: "US", Singbox: sbOutbound{Type: "vless", Tag: "US-1"}},
		{Name: "JP-1", CountryCode: "JP", Singbox: sbOutbound{Type: "vless", Tag: "JP-1"}},
	}
	result, err := applyNativeTemplate("singbox", content, portablePolicy{Final: "Main"}, nodes, "Lattix")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "__LATTIX_") {
		t.Fatalf("placeholder remained in native template: %s", result)
	}
	var document struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal(result, &document); err != nil {
		t.Fatal(err)
	}
	tags := map[string]map[string]any{}
	for _, outbound := range document.Outbounds {
		tag, _ := outbound["tag"].(string)
		if _, exists := tags[tag]; exists {
			t.Fatalf("duplicate outbound tag %q: %+v", tag, document.Outbounds)
		}
		tags[tag] = outbound
	}
	for _, want := range []string{"US-1", "JP-1", "DIRECT", "REJECT", "Main", "🇺🇸 US", "🇯🇵 JP"} {
		if tags[want] == nil {
			t.Fatalf("merged template missing outbound %q: %+v", want, document.Outbounds)
		}
	}
	if _, exists := tags["DIRECT"]["mark"]; exists {
		t.Fatalf("template DIRECT replaced generated builtin: %+v", tags["DIRECT"])
	}
	rules, _ := document.Route["rules"].([]any)
	if len(rules) != 1 || document.Route["final"] != "Main" {
		t.Fatalf("native route was replaced: %+v", document.Route)
	}
}

func TestPublishUserAppliesAssignedPortableTemplate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertSubscriptionTemplate(ctx, store.SubscriptionTemplate{
		ID: "assigned-tpl", Name: "Assigned", Kind: "portable", Origin: "local",
		Content:       "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n",
		ContentSHA256: "assigned-sha",
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000005", "assigned-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户未自选（默认建议模式），仅被指派模板。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "assigned-tpl",
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Files["clash"]), "Proxy") {
		t.Fatalf("assigned template group missing from clash output: %s", result.Files["clash"])
	}
}

func TestPublishUserForcedAssignmentOverridesUserChoice(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	body := "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: Proxy\n"
	for _, template := range []store.SubscriptionTemplate{
		{ID: "assigned-tpl", Name: "Assigned", Kind: "portable", Origin: "local", Content: body, ContentSHA256: "assigned-sha"},
		{ID: "user-tpl", Name: "User", Kind: "portable", Origin: "local",
			Content:       "name: User\ngroups:\n  - name: UserGroup\n    type: select\n    options: [DIRECT]\nrules: []\nfinal: UserGroup\n",
			ContentSHA256: "user-sha"},
	} {
		if err := st.UpsertSubscriptionTemplate(ctx, template); err != nil {
			t.Fatal(err)
		}
	}
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000006", "forced-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户自选 user-tpl，但被强制指派 assigned-tpl。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeTemplate, Preset: "balanced",
		CategoriesJSON: `[]`, PortableTemplateID: "user-tpl",
		GenerationStatus:           store.SubscriptionGenerationMissing,
		AssignedPortableTemplateID: "assigned-tpl", AssignForcedPortable: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "Proxy") || strings.Contains(clash, "UserGroup") {
		t.Fatalf("forced assignment not applied: %s", clash)
	}
}

func TestPublishUserAssignedSuggestedCategoriesProducesUsableRules(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "00000000-0000-0000-0000-000000000007", "suggested-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用户默认（建议规则/balanced/单分类），被指派 ads+ai+gaming 分组。
	if err := st.SaveUserSubscriptionProfile(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON: `["ai"]`, GenerationStatus: store.SubscriptionGenerationMissing,
		AssignedSuggestedCategories: `["ads","ai","gaming"]`,
	}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	result, err := server.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	if !strings.Contains(clash, "AI 服务") || !strings.Contains(clash, "广告拦截") || !strings.Contains(clash, "游戏平台") {
		t.Fatalf("assigned suggested categories rules missing: %s", clash)
	}
	if strings.Contains(clash, "油管视频") || strings.Contains(clash, "电报消息") {
		t.Fatalf("unassigned categories leaked into rules: %s", clash)
	}
}
