package sub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
)

var subscriptionFormats = []string{"clash", "singbox", "quanx", "quanx-config", "links"}

type PublishResult struct {
	store.SubscriptionSnapshotStatus
	Files map[string][]byte `json:"files,omitempty"`
}

func (s *Server) PublishUser(ctx context.Context, userID int64, baseURL string) (PublishResult, error) {
	s.rememberBaseURL(baseURL)
	s.publish.Lock()
	defer s.publish.Unlock()
	user, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return PublishResult{}, err
	}
	profile, err := s.st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		return PublishResult{}, err
	}
	policy, sourceLabel, template, err := s.resolvePolicy(ctx, profile)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	items, err := s.itemsForUser(ctx, user)
	if err != nil {
		return PublishResult{}, err
	}
	if user.Expired || user.Disabled {
		items = nil
	}
	nodes, err := s.compileNodes(ctx, items, user.UUID)
	if err != nil {
		return PublishResult{}, err
	}
	expandPolicy(&policy, nodes)
	cachedRules, err := s.cachedTemplateRules(ctx, template, policy)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	ruleFiles, err := buildRuleArtifacts(cachedRules)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	mihomoPolicy, err := rewriteRemoteRuleURLs(policy, cachedRules, "mihomo", baseURL, user.SubToken)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	singboxPolicy, err := rewriteRemoteRuleURLs(policy, cachedRules, "singbox", baseURL, user.SubToken)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	quanxPolicy, err := rewriteRemoteRuleURLs(policy, cachedRules, "quanx", baseURL, user.SubToken)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}

	files := make(map[string]store.SubscriptionFile, len(subscriptionFormats))
	visible := make(map[string][]byte, len(subscriptionFormats))
	add := func(format, contentType string, content []byte, err error) error {
		if err != nil {
			return fmt.Errorf("generate %s: %w", format, err)
		}
		files[format] = store.SubscriptionFile{Format: format, ContentType: contentType, Content: content}
		visible[format] = content
		return nil
	}
	content, renderErr := renderMihomo(mihomoPolicy, nodes)
	if err := add("clash", "text/yaml; charset=utf-8", content, renderErr); err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	content, renderErr = renderSingbox(singboxPolicy, nodes)
	if err := add("singbox", "application/json; charset=utf-8", content, renderErr); err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	quanxLines := renderQuanXNodes(nodes)
	if err := add("quanx", "text/plain; charset=utf-8", []byte(quanxLines), nil); err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	content, renderErr = renderQuanXConfig(quanxPolicy, nodes)
	if err := add("quanx-config", "text/plain; charset=utf-8", content, renderErr); err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	content, renderErr = renderLinks(items, user.UUID)
	if err := add("links", "text/plain; charset=utf-8", content, renderErr); err != nil {
		return s.publishFailure(ctx, userID, err)
	}

	// Native templates replace only their matching renderer. Other formats keep
	// the portable policy output from the same snapshot.
	sourceSHA := snapshotSourceSHA(policy, cachedRules)
	for _, override := range []struct{ format, templateID string }{
		{"clash", profile.MihomoTemplateID},
		{"singbox", profile.SingboxTemplateID},
		{"quanx-config", profile.QuanXTemplateID},
	} {
		format, templateID := override.format, override.templateID
		if templateID == "" {
			continue
		}
		template, templateErr := s.st.SubscriptionTemplateByID(ctx, templateID)
		if templateErr != nil {
			return s.publishFailure(ctx, userID, fmt.Errorf("load native template %s: %w", templateID, templateErr))
		}
		nativePolicy := mihomoPolicy
		if format == "singbox" {
			nativePolicy = singboxPolicy
		} else if format == "quanx-config" {
			nativePolicy = quanxPolicy
		}
		content, templateErr := applyNativeTemplate(format, template.Content, nativePolicy, nodes)
		if templateErr != nil {
			return s.publishFailure(ctx, userID, templateErr)
		}
		file := files[format]
		file.Content = content
		files[format] = file
		visible[format] = content
		sourceSHA = contentSHA(sourceSHA + "\n" + format + ":" + template.ContentSHA256)
	}

	status, err := s.st.PublishSubscriptionSnapshot(ctx, userID, sourceLabel, sourceSHA, files, ruleFiles)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	return PublishResult{SubscriptionSnapshotStatus: status, Files: visible}, nil
}

func (s *Server) publishFailure(ctx context.Context, userID int64, err error) (PublishResult, error) {
	_ = s.st.SetSubscriptionGenerationError(ctx, userID, err.Error())
	return PublishResult{}, err
}

func (s *Server) resolvePolicy(ctx context.Context, profile store.SubscriptionProfile) (portablePolicy, string, *store.SubscriptionTemplate, error) {
	if profile.Mode != store.SubscriptionModeTemplate {
		var selected []string
		if err := json.Unmarshal([]byte(profile.CategoriesJSON), &selected); err != nil {
			selected = append([]string(nil), presetCategories[profile.Preset]...)
		}
		policy, err := suggestedPolicy(sortedSelectedCategories(selected))
		return policy, "内置 " + strings.Title(profile.Preset), nil, err
	}
	if profile.PortableTemplateID == "" {
		return portablePolicy{}, "", nil, errors.New("custom template mode requires a portable template")
	}
	template, err := s.st.SubscriptionTemplateByID(ctx, profile.PortableTemplateID)
	if err != nil {
		return portablePolicy{}, "", nil, err
	}
	switch template.Kind {
	case "portable":
		policy, err := parsePortablePolicy(template.Content)
		return policy, template.Name, &template, err
	case "acl4ssr":
		policy, err := parseACL4SSR(template.Content)
		return policy, template.Name, &template, err
	default:
		return portablePolicy{}, "", nil, fmt.Errorf("template %s is not portable", template.Name)
	}
}

func (s *Server) itemsForUser(ctx context.Context, user *store.User) ([]proxyItem, error) {
	r := httptest.NewRequest("GET", "http://lattix.invalid/sub/compile", nil).WithContext(ctx)
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	assigned, err := s.st.UserNodeIDs(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[int64]bool, len(assigned))
	for _, id := range assigned {
		allowed[id] = true
	}
	active := make([]store.Node, 0, len(assigned))
	for _, node := range nodes {
		if allowed[node.ID] && node.Status == store.NodeStatusActive && len(node.RealizedConfig) > 0 {
			active = append(active, node)
		}
	}
	return s.subscriptionItems(r, user, active), nil
}

type compiledNode struct {
	Name        string
	CountryCode string
	Clash       clashProxy
	Singbox     sbOutbound
	QuanX       string
}

func (s *Server) compileNodes(ctx context.Context, items []proxyItem, uuid string) ([]compiledNode, error) {
	out := make([]compiledNode, 0, len(items))
	for _, item := range items {
		credential := item.credential
		if credential == "" {
			credential = uuid
		}
		clash, err := buildProxy(item.node, item.rc, credential)
		if err != nil {
			continue
		}
		singbox, err := buildSbOutbound(item.node, item.rc, credential)
		if err != nil {
			continue
		}
		country := ""
		if server, serverErr := s.st.ServerByID(ctx, item.node.ServerID); serverErr == nil {
			country = strings.ToUpper(server.CountryCode)
		}
		out = append(out, compiledNode{
			Name: clash.Name, CountryCode: country, Clash: clash, Singbox: singbox,
			QuanX: buildQuanXLine(item.node, item.rc, credential),
		})
	}
	return out, nil
}

func expandPolicy(policy *portablePolicy, nodes []compiledNode) {
	all := make([]string, 0, len(nodes))
	byCountry := map[string][]string{}
	for _, node := range nodes {
		all = append(all, node.Name)
		if node.CountryCode != "" {
			byCountry[node.CountryCode] = append(byCountry[node.CountryCode], node.Name)
		}
	}
	countries := make([]string, 0, len(byCountry))
	for country := range byCountry {
		countries = append(countries, country)
	}
	sort.Strings(countries)
	regions := make([]string, 0, len(countries))
	for _, country := range countries {
		name := countryFlag(country) + " " + country
		regions = append(regions, name)
		policy.Groups = append(policy.Groups, policyGroup{Name: name, Type: "select", Options: byCountry[country]})
	}
	for index := range policy.Groups {
		options := make([]string, 0, len(policy.Groups[index].Options)+len(all)+len(regions))
		for _, option := range policy.Groups[index].Options {
			switch option {
			case "__LATTIX_ALL__":
				options = append(options, all...)
			case "__LATTIX_REGIONS__":
				options = append(options, regions...)
			default:
				if strings.HasPrefix(option, "__LATTIX_REGION_") && strings.HasSuffix(option, "__") {
					country := strings.TrimSuffix(strings.TrimPrefix(option, "__LATTIX_REGION_"), "__")
					if len(byCountry[country]) > 0 {
						options = append(options, byCountry[country]...)
					}
					continue
				}
				options = append(options, option)
			}
		}
		policy.Groups[index].Options = uniqueStrings(options)
	}
}

func countryFlag(code string) string {
	if len(code) != 2 {
		return "🌐"
	}
	runes := []rune(strings.ToUpper(code))
	return string([]rune{runes[0] - 'A' + 0x1F1E6, runes[1] - 'A' + 0x1F1E6})
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func renderMihomo(policy portablePolicy, nodes []compiledNode) ([]byte, error) {
	config := clashConfig{Proxies: []clashProxy{}, RuleProviders: map[string]clashRuleProvider{}}
	for _, node := range nodes {
		config.Proxies = append(config.Proxies, node.Clash)
	}
	for _, group := range policy.Groups {
		config.ProxyGroups = append(config.ProxyGroups, clashProxyGroup{
			Name: group.Name, Type: group.Type, Proxies: group.Options,
			URL: group.URL, Interval: group.Interval, Tolerance: group.Tolerance,
		})
	}
	for _, rule := range policy.Rules {
		config.Rules = append(config.Rules, fmt.Sprintf("%s,%s,%s", rule.Kind, rule.Value, rule.Outbound))
	}
	for _, remote := range policy.RemoteRule {
		config.RuleProviders[remote.Name] = clashRuleProvider{
			Type: "http", Behavior: remote.Behavior, URL: remote.URL,
			Path: "./ruleset/" + remote.Name + ".yaml", Interval: 21600,
		}
		config.Rules = append(config.Rules, "RULE-SET,"+remote.Name+","+remote.Outbound)
	}
	config.Rules = append(config.Rules, "MATCH,"+policy.Final)
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by Lattix. Template source and license are recorded in the panel.\n"), body...), nil
}

func renderSingbox(policy portablePolicy, nodes []compiledNode) ([]byte, error) {
	outbounds := make([]any, 0, len(nodes)+len(policy.Groups)+2)
	for _, node := range nodes {
		outbounds = append(outbounds, node.Singbox)
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "DIRECT"})
	outbounds = append(outbounds, map[string]any{"type": "block", "tag": "REJECT"})
	for _, group := range policy.Groups {
		typeName := group.Type
		if typeName == "select" {
			typeName = "selector"
		} else if typeName == "url-test" {
			typeName = "urltest"
		} else if typeName == "fallback" || typeName == "load-balance" {
			typeName = "selector"
		}
		outbounds = append(outbounds, map[string]any{"type": typeName, "tag": group.Name, "outbounds": group.Options})
	}
	rules := make([]any, 0, len(policy.Rules)+len(policy.RemoteRule))
	for _, rule := range policy.Rules {
		entry := map[string]any{"outbound": rule.Outbound}
		if rule.Kind == "GEOSITE" {
			entry["geosite"] = []string{rule.Value}
		} else if rule.Kind == "GEOIP" {
			entry["geoip"] = []string{strings.ToLower(rule.Value)}
		}
		rules = append(rules, entry)
	}
	ruleSets := make([]any, 0, len(policy.RemoteRule))
	for _, remote := range policy.RemoteRule {
		ruleSets = append(ruleSets, map[string]any{
			"type": "remote", "tag": remote.Name, "format": "source", "url": remote.URL,
			"download_detour": "DIRECT", "update_interval": "6h",
		})
		rules = append(rules, map[string]any{"rule_set": []string{remote.Name}, "outbound": remote.Outbound})
	}
	config := map[string]any{
		"outbounds": outbounds,
		"route":     map[string]any{"rules": rules, "rule_set": ruleSets, "final": policy.Final},
	}
	return json.MarshalIndent(config, "", "  ")
}

func renderQuanXNodes(nodes []compiledNode) string {
	lines := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.QuanX != "" {
			lines = append(lines, node.QuanX)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderQuanXConfig(policy portablePolicy, nodes []compiledNode) ([]byte, error) {
	var body strings.Builder
	body.WriteString("# Generated by Lattix\n[server_local]\n")
	body.WriteString(renderQuanXNodes(nodes))
	body.WriteString("\n[policy]\n")
	for _, group := range policy.Groups {
		mode := "static"
		if group.Type == "url-test" {
			mode = "url-latency-benchmark"
		}
		body.WriteString(mode + "=" + group.Name + ", " + strings.Join(group.Options, ", ") + "\n")
	}
	body.WriteString("\n[filter_remote]\n")
	for _, remote := range policy.RemoteRule {
		body.WriteString(remote.URL + ", tag=" + remote.Name + ", force-policy=" + remote.Outbound + ", enabled=true\n")
	}
	body.WriteString("\n[filter_local]\n")
	for _, rule := range policy.Rules {
		body.WriteString(rule.Kind + "," + rule.Value + "," + rule.Outbound + "\n")
	}
	body.WriteString("FINAL," + policy.Final + "\n")
	return []byte(body.String()), nil
}

func renderLinks(items []proxyItem, uuid string) ([]byte, error) {
	links := make([]string, 0, len(items))
	for _, item := range items {
		credential := item.credential
		if credential == "" {
			credential = uuid
		}
		if link, ok := buildShareLink(item.node, item.rc, credential); ok {
			links = append(links, link)
		}
	}
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	return []byte(body + "\n"), nil
}

func applyNativeTemplate(format, content string, policy portablePolicy, nodes []compiledNode) ([]byte, error) {
	switch format {
	case "clash":
		var document map[string]any
		if err := yaml.Unmarshal([]byte(content), &document); err != nil {
			return nil, fmt.Errorf("parse Mihomo native template: %w", err)
		}
		generated, err := renderMihomo(policy, nodes)
		if err != nil {
			return nil, err
		}
		var values map[string]any
		if err := yaml.Unmarshal(generated, &values); err != nil {
			return nil, err
		}
		document["proxies"] = values["proxies"]
		if groups, ok := document["proxy-groups"].([]any); ok {
			document["proxy-groups"] = expandNativeGroups(groups, "proxies", nodes, "name", "select")
		} else {
			document["proxy-groups"] = values["proxy-groups"]
		}
		if _, ok := document["rules"]; !ok {
			document["rules"] = values["rules"]
		}
		return yaml.Marshal(document)
	case "singbox":
		var document map[string]any
		if err := json.Unmarshal([]byte(content), &document); err != nil {
			return nil, fmt.Errorf("parse sing-box native template: %w", err)
		}
		generated, err := renderSingbox(policy, nodes)
		if err != nil {
			return nil, err
		}
		var values map[string]any
		if err := json.Unmarshal(generated, &values); err != nil {
			return nil, err
		}
		if outbounds, ok := document["outbounds"].([]any); ok {
			generated, _ := values["outbounds"].([]any)
			injected := append([]any(nil), generated[:min(len(nodes)+2, len(generated))]...)
			injected = append(injected, expandNativeGroups(outbounds, "outbounds", nodes, "tag", "selector")...)
			document["outbounds"] = uniqueNativeGroups(injected, "tag")
		} else {
			document["outbounds"] = values["outbounds"]
		}
		if _, ok := document["route"]; !ok {
			document["route"] = values["route"]
		}
		return json.MarshalIndent(document, "", "  ")
	case "quanx-config":
		servers := renderQuanXNodes(nodes)
		if !strings.Contains(content, "__LATTIX_SERVERS__") {
			return nil, errors.New("Quantumult X native template requires __LATTIX_SERVERS__")
		}
		return []byte(strings.ReplaceAll(content, "__LATTIX_SERVERS__", strings.TrimSpace(servers))), nil
	default:
		return nil, fmt.Errorf("unsupported native template format %q", format)
	}
}

func uniqueNativeGroups(groups []any, nameKey string) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(groups))
	for _, value := range groups {
		group, ok := value.(map[string]any)
		if !ok {
			out = append(out, value)
			continue
		}
		name, _ := group[nameKey].(string)
		if name != "" && seen[name] {
			continue
		}
		seen[name] = name != ""
		out = append(out, group)
	}
	return out
}

func expandNativeGroups(groups []any, optionKey string, nodes []compiledNode, nameKey, regionType string) []any {
	all, regions, byCountry := nativeNodeOptions(nodes)
	expanded := make([]any, 0, len(groups)+len(regions))
	existing := map[string]bool{}
	for _, value := range groups {
		group, ok := value.(map[string]any)
		if !ok {
			expanded = append(expanded, value)
			continue
		}
		if name, _ := group[nameKey].(string); name != "" {
			existing[name] = true
		}
		if options, ok := group[optionKey].([]any); ok {
			group[optionKey] = expandNativeOptions(options, all, regions, byCountry)
		}
		expanded = append(expanded, group)
	}
	for _, region := range regions {
		if existing[region] {
			continue
		}
		options := make([]any, 0, len(byCountry[region]))
		for _, option := range byCountry[region] {
			options = append(options, option)
		}
		expanded = append(expanded, map[string]any{nameKey: region, "type": regionType, optionKey: options})
	}
	return expanded
}

func nativeNodeOptions(nodes []compiledNode) ([]string, []string, map[string][]string) {
	all := make([]string, 0, len(nodes))
	byCountry := map[string][]string{}
	for _, node := range nodes {
		all = append(all, node.Name)
		if node.CountryCode != "" {
			region := countryFlag(node.CountryCode) + " " + strings.ToUpper(node.CountryCode)
			byCountry[region] = append(byCountry[region], node.Name)
		}
	}
	regions := make([]string, 0, len(byCountry))
	for region := range byCountry {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return all, regions, byCountry
}

func expandNativeOptions(options []any, all, regions []string, byCountry map[string][]string) []any {
	var expanded []any
	appendStrings := func(values []string) {
		for _, value := range values {
			expanded = append(expanded, value)
		}
	}
	for _, raw := range options {
		option, ok := raw.(string)
		if !ok {
			expanded = append(expanded, raw)
			continue
		}
		switch option {
		case "__LATTIX_ALL__":
			appendStrings(all)
		case "__LATTIX_REGIONS__":
			appendStrings(regions)
		default:
			if strings.HasPrefix(option, "__LATTIX_REGION_") && strings.HasSuffix(option, "__") {
				country := strings.TrimSuffix(strings.TrimPrefix(option, "__LATTIX_REGION_"), "__")
				appendStrings(byCountry[countryFlag(country)+" "+strings.ToUpper(country)])
				continue
			}
			expanded = append(expanded, option)
		}
	}
	return expanded
}

func contentSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func nowPointer() *time.Time {
	now := time.Now().UTC()
	return &now
}
