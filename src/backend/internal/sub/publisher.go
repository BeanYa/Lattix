package sub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	Files    map[string][]byte `json:"files,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
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
	profile = store.EffectiveProfile(profile)
	policy, sourceLabel, template, err := s.resolvePolicy(ctx, profile)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	items, chainWarnings, err := s.itemsForUser(ctx, user)
	if err != nil {
		return PublishResult{}, err
	}
	if user.Expired || user.Disabled {
		items = nil
	}
	nodes, compileWarnings, err := s.compileNodes(ctx, items, user.UUID)
	if err != nil {
		return PublishResult{}, err
	}
	panelShort := s.panelShort(ctx)
	warnings := append(append([]string{}, chainWarnings...), compileWarnings...)
	policyWarnings, err := expandPolicy(&policy, nodes, panelShort)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	warnings = append(warnings, policyWarnings...)
	if len(warnings) > 0 {
		log.Printf("sub: publish user %d (%s): %d item(s) skipped: %s",
			user.ID, user.Name, len(warnings), strings.Join(warnings, "; "))
	}
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
		content, templateErr := applyNativeTemplate(format, template.Content, nativePolicy, nodes, panelShort)
		if templateErr != nil {
			return s.publishFailure(ctx, userID, templateErr)
		}
		file := files[format]
		file.Content = content
		files[format] = file
		visible[format] = content
		sourceSHA = contentSHA(sourceSHA + "\n" + format + ":" + template.ContentSHA256)
	}

	status, err := s.st.PublishSubscriptionSnapshot(ctx, userID, sourceLabel, sourceSHA, files, ruleFiles, warnings)
	if err != nil {
		return s.publishFailure(ctx, userID, err)
	}
	return PublishResult{SubscriptionSnapshotStatus: status, Files: visible, Warnings: warnings}, nil
}

func (s *Server) publishFailure(ctx context.Context, userID int64, err error) (PublishResult, error) {
	_ = s.st.SetSubscriptionGenerationError(ctx, userID, err.Error())
	return PublishResult{}, err
}

// panelShort 读取面板缩写设置（读取失败按未设置处理，回退默认值）。
func (s *Server) panelShort(ctx context.Context) string {
	raw, _ := s.st.GetSetting(ctx, store.SettingPanelShort)
	return store.EffectivePanelShort(raw)
}

func (s *Server) resolvePolicy(ctx context.Context, profile store.SubscriptionProfile) (portablePolicy, string, *store.SubscriptionTemplate, error) {
	if profile.Mode != store.SubscriptionModeTemplate {
		var selected []string
		if err := json.Unmarshal([]byte(profile.CategoriesJSON), &selected); err != nil {
			selected = append([]string(nil), presetCategories[profile.Preset]...)
		}
		policy, err := suggestedPolicy(sortedSelectedCategories(selected))
		return policy, "内置建议规则", nil, err
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

func (s *Server) itemsForUser(ctx context.Context, user *store.User) ([]proxyItem, []string, error) {
	r := httptest.NewRequest("GET", "http://lattix.invalid/sub/compile", nil).WithContext(ctx)
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	active := []store.Node{}
	// 分组用户的直连 user_nodes 被遮蔽：订阅 = 分组派生链路 + 外部订阅节点。
	groupIDs, err := s.st.UserGroupIDsForUser(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(groupIDs) == 0 {
		assigned, err := s.st.UserNodeIDs(ctx, user.ID)
		if err != nil {
			return nil, nil, err
		}
		allowed := make(map[int64]bool, len(assigned))
		for _, id := range assigned {
			allowed[id] = true
		}
		active = make([]store.Node, 0, len(assigned))
		for _, node := range nodes {
			if allowed[node.ID] && node.Status == store.NodeStatusActive && len(node.RealizedConfig) > 0 {
				active = append(active, node)
			}
		}
	}
	items, warnings := s.subscriptionItems(r, user, active)
	return items, warnings, nil
}

type compiledNode struct {
	Name        string
	CountryCode string
	Group       string // 来源分组名：面板管理节点为空，外部订阅节点为其订阅名称
	Clash       clashProxy
	Singbox     any // 面板节点为 sbOutbound；外部节点为 map[string]any
	QuanX       string
}

func (s *Server) compileNodes(ctx context.Context, items []proxyItem, uuid string) ([]compiledNode, []string, error) {
	out := make([]compiledNode, 0, len(items))
	var warnings []string
	for _, item := range items {
		if item.external != nil {
			clash, err := buildExternalClash(*item.external)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			singbox, err := buildExternalSingbox(*item.external)
			if err != nil {
				warnings = append(warnings, err.Error())
			}
			out = append(out, compiledNode{
				Name: clash.Name, CountryCode: inferNodeCountry(clash.Name), // 与面板节点同级参与区域分组
				Group: item.group, // 来源分组：外部订阅名
				Clash: clash, Singbox: singbox, // 失败时 nil，renderSingbox 跳过
				QuanX: buildExternalQuanX(*item.external),
			})
			continue
		}
		credential := item.credential
		if credential == "" {
			credential = uuid
		}
		clash, err := buildProxy(item.node, item.rc, credential)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("节点「%s」构造 clash 代理失败：%v", item.node.Name, err))
			continue
		}
		singbox, err := buildSbOutbound(item.node, item.rc, credential)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("节点「%s」构造 sing-box 出站失败：%v", item.node.Name, err))
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
	return out, warnings, nil
}

func expandPolicy(policy *portablePolicy, nodes []compiledNode, panelShort string) ([]string, error) {
	all := make([]string, 0, len(nodes))
	byCountry := map[string][]string{}
	var noRegion []string
	for _, node := range nodes {
		all = append(all, node.Name)
		if node.CountryCode != "" {
			byCountry[node.CountryCode] = append(byCountry[node.CountryCode], node.Name)
		} else {
			noRegion = append(noRegion, node.Name)
		}
	}
	countries := make([]string, 0, len(byCountry))
	for country := range byCountry {
		countries = append(countries, country)
	}
	sort.Strings(countries)
	regions := make([]string, 0, len(countries)+1)
	var warnings []string
	existing := make(map[string]bool, len(policy.Groups))
	for _, group := range policy.Groups {
		existing[group.Name] = true
	}
	// 按来源分组：「<panelShort> 分组」含全部面板管理节点，每个外部订阅一组
	// （组名 = 订阅名）含其解析节点；与既有组重名时跳过，避免非法的重复组名。
	for _, group := range sourcePolicyGroups(nodes, panelShort) {
		if existing[group.Name] {
			warnings = append(warnings, fmt.Sprintf("来源分组「%s」与既有策略组重名，已跳过自动生成", group.Name))
			continue
		}
		existing[group.Name] = true
		policy.Groups = append(policy.Groups, group)
	}
	for _, country := range countries {
		name := countryFlag(country) + " " + country
		regions = append(regions, name)
		policy.Groups = append(policy.Groups, policyGroup{Name: name, Type: "select", Options: byCountry[country]})
	}
	// 无地区标识的节点（如名称无法推断国家的外部订阅节点）收进固定的无地区分组，
	// 随 __LATTIX_REGIONS__ 一起展开，保证所有节点在分组层都可达。
	if len(noRegion) > 0 {
		regions = append(regions, noRegionGroupName)
		policy.Groups = append(policy.Groups, policyGroup{Name: noRegionGroupName, Type: "select", Options: noRegion})
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
	pruneWarnings, err := pruneEmptyGroups(policy, all)
	return append(warnings, pruneWarnings...), err
}

// sourcePolicyGroups 生成按节点来源划分的策略组：「<panelShort> 分组」包含全部面板
// 管理节点；每个外部订阅一组（组名 = 订阅名，按首次出现排序）包含其解析出的节点。
// 无节点的来源不生成组。
func sourcePolicyGroups(nodes []compiledNode, panelShort string) []policyGroup {
	var panel []string
	outerOrder := []string{}
	outer := map[string][]string{}
	for _, node := range nodes {
		if node.Group == "" {
			panel = append(panel, node.Name)
			continue
		}
		if _, ok := outer[node.Group]; !ok {
			outerOrder = append(outerOrder, node.Group)
		}
		outer[node.Group] = append(outer[node.Group], node.Name)
	}
	groups := make([]policyGroup, 0, len(outerOrder)+1)
	if len(panel) > 0 {
		groups = append(groups, policyGroup{Name: panelShort + " 分组", Type: "select", Options: panel})
	}
	for _, name := range outerOrder {
		groups = append(groups, policyGroup{Name: name, Type: "select", Options: outer[name]})
	}
	return groups
}

// pruneEmptyGroups 删除展开后无可选出站的策略组，并清除其余组的悬空引用：
// 区域分组（如 🇰🇷 韩国节点）在无对应国家节点时 options 为空，Mihomo/sing-box
// 会因空组或悬空引用拒绝整份配置。规则若指向被剪除的组则跳过并给出警告。
// 无任何节点时跳过剪除（订阅本身退化为空配置，维持既有发布契约）。
func pruneEmptyGroups(policy *portablePolicy, all []string) ([]string, error) {
	if len(all) == 0 {
		return nil, nil
	}
	nodeSet := make(map[string]bool, len(all))
	for _, name := range all {
		nodeSet[name] = true
	}
	alive := map[string]bool{}
	for {
		changed := false
		for _, group := range policy.Groups {
			if alive[group.Name] {
				continue
			}
			for _, option := range group.Options {
				if option == "DIRECT" || option == "REJECT" || nodeSet[option] || alive[option] {
					alive[group.Name] = true
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	if len(alive) == len(policy.Groups) {
		if policy.Final == "DIRECT" || policy.Final == "REJECT" || alive[policy.Final] {
			return nil, nil
		}
	}
	var warnings []string
	kept := policy.Groups[:0]
	for _, group := range policy.Groups {
		if !alive[group.Name] {
			warnings = append(warnings, fmt.Sprintf("策略组「%s」无可用节点或引用，已从订阅移除", group.Name))
			continue
		}
		options := make([]string, 0, len(group.Options))
		for _, option := range group.Options {
			if option == "DIRECT" || option == "REJECT" || nodeSet[option] || alive[option] {
				options = append(options, option)
			}
		}
		group.Options = options
		kept = append(kept, group)
	}
	policy.Groups = kept

	keptRules := policy.Rules[:0]
	for _, rule := range policy.Rules {
		if rule.Outbound != "DIRECT" && rule.Outbound != "REJECT" && !alive[rule.Outbound] {
			warnings = append(warnings, fmt.Sprintf("规则 %s,%s 指向已移除的策略组「%s」，已跳过", rule.Kind, rule.Value, rule.Outbound))
			continue
		}
		keptRules = append(keptRules, rule)
	}
	policy.Rules = keptRules

	keptRemote := policy.RemoteRule[:0]
	for _, remote := range policy.RemoteRule {
		if remote.Outbound != "DIRECT" && remote.Outbound != "REJECT" && !alive[remote.Outbound] {
			warnings = append(warnings, fmt.Sprintf("远程规则 %s 指向已移除的策略组「%s」，已跳过", remote.Name, remote.Outbound))
			continue
		}
		keptRemote = append(keptRemote, remote)
	}
	policy.RemoteRule = keptRemote

	if policy.Final != "DIRECT" && policy.Final != "REJECT" && !alive[policy.Final] {
		return warnings, fmt.Errorf("final 指向已移除的策略组 %q", policy.Final)
	}
	return warnings, nil
}

// noRegionGroupName 是无地区标识节点的固定分组名：随地区分组一起生成并排在最后，
// 同时随 __LATTIX_REGIONS__ 展开，保证无国家代码的节点在分组层也可达。
const noRegionGroupName = "🌐 无地区"

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
	config.DNS = defaultClashDNS()
	for _, node := range nodes {
		config.Proxies = append(config.Proxies, node.Clash)
	}
	for _, group := range policy.Groups {
		config.ProxyGroups = append(config.ProxyGroups, clashProxyGroup{
			Name: group.Name, Type: group.Type, Proxies: group.Options,
			URL: group.URL, Interval: group.Interval, Tolerance: group.Tolerance,
		})
	}
	needsGeodata := false
	for _, rule := range policy.Rules {
		config.Rules = append(config.Rules, fmt.Sprintf("%s,%s,%s", rule.Kind, rule.Value, rule.Outbound))
		if rule.Kind == "GEOSITE" || rule.Kind == "GEOIP" {
			needsGeodata = true
		}
	}
	for _, remote := range policy.RemoteRule {
		config.RuleProviders[remote.Name] = clashRuleProvider{
			Type: "http", Behavior: remote.Behavior, URL: remote.URL,
			Path: "./ruleset/" + remote.Name + ".yaml", Interval: 21600,
		}
		config.Rules = append(config.Rules, "RULE-SET,"+remote.Name+","+remote.Outbound)
	}
	config.Rules = append(config.Rules, "MATCH,"+policy.Final)
	if needsGeodata {
		config.GeodataMode = true
		config.GeoAutoUpdate = true
		config.GeodataLoader = "standard"
		config.GeoUpdateInterval = 24
		config.GeoXURL = &clashGeoxURL{
			Geoip:   "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip-lite.dat",
			Geosite: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
			MMDB:    "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip-lite.mmdb",
			ASN:     "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb",
		}
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by Lattix. Template source and license are recorded in the panel.\n"), body...), nil
}

// defaultClashDNS 返回内置 fake-ip DNS 配置，使 GEOSITE/GEOIP 与 RULE-SET 规则
// 在客户端无需额外配置即可生效。节点域名（如 hk.whoisbean.com）经
// proxy-server-nameserver 用国内可达解析器直查，不经过境外交互，避免节点测速/连接超时。
func defaultClashDNS() *clashDNS {
	return &clashDNS{
		Enable:       true,
		IPv6:         false,
		EnhancedMode: "fake-ip",
		FakeIPRange:  "198.18.0.1/16",
		FakeIPFilter: []string{
			"*.lan", "*.localdomain", "*.example", "*.invalid", "*.local",
			"*.home.arpa", "time.*.com", "ntp.*.com", "+.pool.ntp.org", "+.mcdn.bilivideo.cn",
		},
		DefaultNameserver:     []string{"223.5.5.5", "119.29.29.29"},
		Nameserver:            []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
		ProxyServerNameserver: []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
	}
}

func renderSingbox(policy portablePolicy, nodes []compiledNode) ([]byte, error) {
	outbounds := make([]any, 0, len(nodes)+len(policy.Groups)+2)
	for _, node := range nodes {
		if node.Singbox == nil {
			continue
		}
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
		if item.external != nil {
			if link, ok := buildExternalLink(*item.external); ok {
				links = append(links, link)
			}
			continue
		}
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

func applyNativeTemplate(format, content string, policy portablePolicy, nodes []compiledNode, panelShort string) ([]byte, error) {
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
			document["proxy-groups"] = expandNativeGroups(groups, "proxies", nodes, "name", "select", panelShort)
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
			injected = append(injected, expandNativeGroups(outbounds, "outbounds", nodes, "tag", "selector", panelShort)...)
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

func expandNativeGroups(groups []any, optionKey string, nodes []compiledNode, nameKey, regionType, panelShort string) []any {
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
	appendGroup := func(name string, options []string) {
		if existing[name] {
			return
		}
		existing[name] = true
		values := make([]any, 0, len(options))
		for _, option := range options {
			values = append(values, option)
		}
		expanded = append(expanded, map[string]any{nameKey: name, "type": regionType, optionKey: values})
	}
	// 来源分组（面板分组 + 各外部订阅分组）与便携策略路径保持一致。
	for _, group := range sourcePolicyGroups(nodes, panelShort) {
		appendGroup(group.Name, group.Options)
	}
	for _, region := range regions {
		appendGroup(region, byCountry[region])
	}
	return expanded
}

func nativeNodeOptions(nodes []compiledNode) ([]string, []string, map[string][]string) {
	all := make([]string, 0, len(nodes))
	byCountry := map[string][]string{}
	var noRegion []string
	for _, node := range nodes {
		all = append(all, node.Name)
		if node.CountryCode != "" {
			region := countryFlag(node.CountryCode) + " " + strings.ToUpper(node.CountryCode)
			byCountry[region] = append(byCountry[region], node.Name)
		} else {
			noRegion = append(noRegion, node.Name)
		}
	}
	regions := make([]string, 0, len(byCountry)+1)
	for region := range byCountry {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	if len(noRegion) > 0 {
		byCountry[noRegionGroupName] = noRegion
		regions = append(regions, noRegionGroupName)
	}
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
