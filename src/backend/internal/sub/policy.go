package sub

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ruleCategory struct {
	ID        string   `json:"id" yaml:"id"`
	Label     string   `json:"label" yaml:"label"`
	Icon      string   `json:"icon" yaml:"icon"`
	SiteRules []string `json:"site_rules" yaml:"site_rules"`
	IPRules   []string `json:"ip_rules" yaml:"ip_rules"`
	Default   string   `json:"default" yaml:"default"`
}

var builtInCategories = []ruleCategory{
	{ID: "ads", Label: "广告拦截", Icon: "🛑", SiteRules: []string{"category-ads-all"}, Default: "REJECT"},
	{ID: "ai", Label: "AI 服务", Icon: "💬", SiteRules: []string{"category-ai-!cn"}, Default: "proxy"},
	{ID: "bilibili", Label: "哔哩哔哩", Icon: "📺", SiteRules: []string{"bilibili"}, Default: "DIRECT"},
	{ID: "youtube", Label: "油管视频", Icon: "📹", SiteRules: []string{"youtube"}, Default: "proxy"},
	{ID: "google", Label: "谷歌服务", Icon: "🔍", SiteRules: []string{"google"}, IPRules: []string{"google"}, Default: "proxy"},
	{ID: "private", Label: "私有网络", Icon: "🏠", IPRules: []string{"private"}, Default: "DIRECT"},
	{ID: "domestic", Label: "国内服务", Icon: "🔒", SiteRules: []string{"geolocation-cn", "cn"}, IPRules: []string{"cn"}, Default: "DIRECT"},
	{ID: "telegram", Label: "电报消息", Icon: "📲", IPRules: []string{"telegram"}, Default: "proxy"},
	{ID: "github", Label: "Github", Icon: "🐱", SiteRules: []string{"github", "gitlab"}, Default: "proxy"},
	{ID: "microsoft", Label: "微软服务", Icon: "Ⓜ️", SiteRules: []string{"microsoft"}, Default: "proxy"},
	{ID: "apple", Label: "苹果服务", Icon: "🍏", SiteRules: []string{"apple"}, Default: "proxy"},
	{ID: "social", Label: "社交媒体", Icon: "🌐", SiteRules: []string{"facebook", "instagram", "twitter", "tiktok", "linkedin"}, Default: "proxy"},
	{ID: "streaming", Label: "流媒体", Icon: "🎬", SiteRules: []string{"netflix", "hulu", "disney", "hbo", "amazon", "bahamut"}, Default: "proxy"},
	{ID: "gaming", Label: "游戏平台", Icon: "🎮", SiteRules: []string{"steam", "epicgames", "ea", "ubisoft", "blizzard"}, Default: "proxy"},
	{ID: "education", Label: "教育资源", Icon: "📚", SiteRules: []string{"coursera", "edx", "udemy", "khanacademy", "category-scholar-!cn"}, Default: "proxy"},
	{ID: "finance", Label: "金融服务", Icon: "💰", SiteRules: []string{"paypal", "visa", "mastercard", "stripe", "wise"}, Default: "proxy"},
	{ID: "cloud", Label: "云服务", Icon: "☁️", SiteRules: []string{"aws", "azure", "digitalocean", "heroku", "dropbox"}, Default: "proxy"},
	{ID: "overseas", Label: "非中国", Icon: "🌐", SiteRules: []string{"geolocation-!cn"}, Default: "proxy"},
}

var presetCategories = map[string][]string{
	"minimal":       {"private", "domestic", "overseas"},
	"balanced":      {"ai", "youtube", "google", "private", "domestic", "telegram", "github", "overseas"},
	"comprehensive": categoryIDs(),
}

func categoryIDs() []string {
	out := make([]string, 0, len(builtInCategories))
	for _, category := range builtInCategories {
		out = append(out, category.ID)
	}
	return out
}

type policyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Options   []string `yaml:"options,omitempty"`
	Filter    string   `yaml:"filter,omitempty"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
	// Source 标记发布时自动生成的来源分组（"panel"=面板分组 / "outer"=外部订阅
	// 分组），模板解析出的分组为空。预编译中间态据此把分组条目替换为
	// __LATTIX-GROUP__ / __OUTER-SUBS-GROUP__ 占位符。
	Source string `yaml:"-"`
}

type policyRule struct {
	Kind     string `yaml:"kind"`
	Value    string `yaml:"value"`
	Outbound string `yaml:"outbound"`
}

type remoteRuleSet struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	Behavior string `yaml:"behavior,omitempty"`
	Outbound string `yaml:"outbound"`
}

type portablePolicy struct {
	Name       string          `yaml:"name"`
	Groups     []policyGroup   `yaml:"groups"`
	Rules      []policyRule    `yaml:"rules"`
	RemoteRule []remoteRuleSet `yaml:"remote_rules,omitempty"`
	Final      string          `yaml:"final"`
}

func suggestedPolicy(selected []string) (portablePolicy, error) {
	known := make(map[string]ruleCategory, len(builtInCategories))
	for _, category := range builtInCategories {
		known[category.ID] = category
	}
	policy := portablePolicy{Name: "Lattix Balanced", Final: "🚀 节点选择"}
	policy.Groups = []policyGroup{
		{Name: "♻️ 自动选择", Type: "url-test", Options: []string{"__LATTIX_ALL__"}, URL: "https://www.gstatic.com/generate_204", Interval: 300, Tolerance: 50},
		{Name: "🛟 故障转移", Type: "fallback", Options: []string{"__LATTIX_ALL__"}, URL: "https://www.gstatic.com/generate_204", Interval: 300},
		// 节点选择/自动选择仅能包含节点与地区分组（展开时强制），否则其他组对它们的引用会形成引用环。
		{Name: "🚀 节点选择", Type: "select", Options: []string{"__LATTIX_REGIONS__", "__LATTIX_ALL__"}},
	}
	seen := map[string]bool{}
	for _, id := range selected {
		if seen[id] {
			continue
		}
		category, ok := known[id]
		if !ok {
			return portablePolicy{}, fmt.Errorf("unknown rule category %q", id)
		}
		seen[id] = true
		outbound := category.Icon + " " + category.Label
		defaultOption := "🚀 节点选择"
		if category.Default == "DIRECT" || category.Default == "REJECT" {
			defaultOption = category.Default
		}
		policy.Groups = append(policy.Groups, policyGroup{
			Name: outbound, Type: "select",
			Options: []string{defaultOption, "🚀 节点选择", "♻️ 自动选择", "DIRECT", "REJECT", "__LATTIX_REGIONS__"},
		})
		for _, site := range category.SiteRules {
			policy.Rules = append(policy.Rules, policyRule{Kind: "GEOSITE", Value: site, Outbound: outbound})
		}
		for _, ip := range category.IPRules {
			policy.Rules = append(policy.Rules, policyRule{Kind: "GEOIP", Value: ip, Outbound: outbound})
		}
	}
	return policy, nil
}

func parsePortablePolicy(content string) (portablePolicy, error) {
	var policy portablePolicy
	if err := yaml.Unmarshal([]byte(content), &policy); err != nil {
		return portablePolicy{}, fmt.Errorf("parse portable policy: %w", err)
	}
	if strings.TrimSpace(policy.Name) == "" || len(policy.Groups) == 0 {
		return portablePolicy{}, errors.New("portable policy requires name and groups")
	}
	if policy.Final == "" {
		policy.Final = "🚀 节点选择"
	}
	return policy, validatePolicy(policy)
}

func parseACL4SSR(content string) (portablePolicy, error) {
	policy := portablePolicy{Name: "ACL4SSR", Final: "🚀 节点选择"}
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	ruleIndex := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "custom_proxy_group":
			parts := strings.Split(value, "`")
			if len(parts) < 2 {
				return portablePolicy{}, fmt.Errorf("line %d: invalid custom_proxy_group", lineNo)
			}
			group := policyGroup{Name: strings.TrimSpace(parts[0]), Type: strings.TrimSpace(parts[1])}
			for _, part := range parts[2:] {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "[]") {
					part = strings.TrimSpace(strings.TrimPrefix(part, "[]"))
				}
				if part == "" || strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") || regexpMeta(part) {
					continue
				}
				if strings.Contains(part, ",") {
					for _, numberText := range strings.Split(part, ",") {
						if number, err := strconv.Atoi(strings.TrimSpace(numberText)); err == nil {
							if group.Interval == 0 {
								group.Interval = number
							} else if group.Tolerance == 0 {
								group.Tolerance = number
							}
						}
					}
					continue
				}
				if number, err := strconv.Atoi(part); err == nil {
					if group.Interval == 0 {
						group.Interval = number
					} else if group.Tolerance == 0 {
						group.Tolerance = number
					}
					continue
				}
				group.Options = append(group.Options, part)
			}
			if group.Type == "url-test" || group.Type == "fallback" || group.Type == "load-balance" {
				if country := inferredGroupCountry(group.Name); country != "" {
					group.Options = append(group.Options, "__LATTIX_REGION_"+country+"__")
				} else {
					group.Options = append(group.Options, "__LATTIX_ALL__")
				}
				group.URL = firstHTTP(parts)
			}
			policy.Groups = append(policy.Groups, group)
		case "ruleset":
			outbound, source, ok := splitACLRule(value)
			if !ok {
				return portablePolicy{}, fmt.Errorf("line %d: invalid ruleset", lineNo)
			}
			source = strings.TrimPrefix(source, "[]")
			upper := strings.ToUpper(source)
			switch {
			case upper == "FINAL":
				policy.Final = outbound
			case strings.HasPrefix(upper, "GEOSITE,") || strings.HasPrefix(upper, "GEOIP,"):
				kind, ruleValue, _ := strings.Cut(source, ",")
				policy.Rules = append(policy.Rules, policyRule{Kind: strings.ToUpper(kind), Value: ruleValue, Outbound: outbound})
			case isACLRemoteSource(source):
				ruleIndex++
				policy.RemoteRule = append(policy.RemoteRule, remoteRuleSet{
					Name: fmt.Sprintf("acl-%03d", ruleIndex), URL: source, Behavior: "classical", Outbound: outbound,
				})
			default:
				return portablePolicy{}, fmt.Errorf("line %d: unsupported ruleset source %q", lineNo, source)
			}
		case "enable_rule_generator", "overwrite_original_rules":
			// These flags describe how subconverter combines its input. Lattix owns
			// the node set and always generates the selected policy, so no action is required.
		default:
			if strings.Contains(strings.ToLower(strings.TrimSpace(key)), "rule") ||
				strings.Contains(strings.ToLower(strings.TrimSpace(key)), "proxy_group") {
				return portablePolicy{}, fmt.Errorf("line %d: unsupported routing directive %q", lineNo, strings.TrimSpace(key))
			}
			// General subconverter options do not change routing semantics and are ignored.
		}
	}
	if err := scanner.Err(); err != nil {
		return portablePolicy{}, err
	}
	if len(policy.Groups) == 0 {
		return portablePolicy{}, errors.New("ACL4SSR template contains no proxy groups")
	}
	return policy, validatePolicy(policy)
}

func validatePolicy(policy portablePolicy) error {
	names := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, group := range policy.Groups {
		if strings.TrimSpace(group.Name) == "" {
			return errors.New("policy group name is required")
		}
		if names[group.Name] {
			return fmt.Errorf("duplicate policy group %q", group.Name)
		}
		names[group.Name] = true
		switch group.Type {
		case "select", "url-test", "fallback", "load-balance":
		default:
			return fmt.Errorf("unsupported policy group type %q", group.Type)
		}
	}
	for _, rule := range policy.Rules {
		if !names[rule.Outbound] {
			return fmt.Errorf("rule references unknown outbound %q", rule.Outbound)
		}
	}
	remoteNames := map[string]bool{}
	for _, rule := range policy.RemoteRule {
		if !safeTemplateID.MatchString(rule.Name) {
			return fmt.Errorf("remote rule has unsafe name %q", rule.Name)
		}
		if remoteNames[rule.Name] {
			return fmt.Errorf("duplicate remote rule %q", rule.Name)
		}
		if !names[rule.Outbound] {
			return fmt.Errorf("remote rule references unknown outbound %q", rule.Outbound)
		}
		if !validRelativeRuleSource(rule.URL) {
			if _, err := normalizeRuleSourceURL(rule.URL); err != nil {
				return fmt.Errorf("remote rule %s: %w", rule.Name, err)
			}
		}
		remoteNames[rule.Name] = true
	}
	if !names[policy.Final] {
		return fmt.Errorf("final references unknown outbound %q", policy.Final)
	}
	return nil
}

func isACLRemoteSource(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "clash-domain:") || strings.HasPrefix(lower, "clash-classic:") ||
		strings.HasPrefix(lower, "clash-ipcidr:") || validRelativeRuleSource(source)
}

// inferNodeCountry 从节点名称推断国家/地区代码：外部订阅节点没有面板服务器的
// CountryCode 字段，只能依靠命名——优先识别旗帜 emoji 的区域指示符对（直接映射
// 为两位代码），其次复用 inferredGroupCountry 的关键词表。无法推断时返回空串，
// 该节点只进入 __LATTIX_ALL__，不参与区域分组。
func inferNodeCountry(name string) string {
	runes := []rune(name)
	for i := 0; i+1 < len(runes); i++ {
		if runes[i] >= 0x1F1E6 && runes[i] <= 0x1F1FF && runes[i+1] >= 0x1F1E6 && runes[i+1] <= 0x1F1FF {
			return string([]rune{runes[i] - 0x1F1E6 + 'A', runes[i+1] - 0x1F1E6 + 'A'})
		}
	}
	return inferredGroupCountry(name)
}

func inferredGroupCountry(name string) string {
	for marker, country := range map[string]string{
		"香港": "HK", "🇭🇰": "HK", "美国": "US", "🇺🇸": "US", "日本": "JP", "🇯🇵": "JP",
		"新加坡": "SG", "🇸🇬": "SG", "台湾": "TW", "🇹🇼": "TW", "韩国": "KR", "🇰🇷": "KR",
		"加拿大": "CA", "🇨🇦": "CA", "英国": "GB", "🇬🇧": "GB", "法国": "FR", "🇫🇷": "FR",
		"德国": "DE", "🇩🇪": "DE", "荷兰": "NL", "🇳🇱": "NL", "土耳其": "TR", "🇹🇷": "TR",
		"俄罗斯": "RU", "🇷🇺": "RU", "澳大利亚": "AU", "🇦🇺": "AU", "印度": "IN", "🇮🇳": "IN",
		"巴西": "BR", "🇧🇷": "BR", "泰国": "TH", "🇹🇭": "TH", "马来西亚": "MY", "🇲🇾": "MY",
		"越南": "VN", "🇻🇳": "VN", "意大利": "IT", "🇮🇹": "IT", "西班牙": "ES", "🇪🇸": "ES",
		"瑞士": "CH", "🇨🇭": "CH", "波兰": "PL", "🇵🇱": "PL", "阿联酋": "AE", "🇦🇪": "AE",
	} {
		if strings.Contains(name, marker) {
			return country
		}
	}
	return ""
}

func splitACLRule(value string) (string, string, bool) {
	parts := strings.Split(value, ",")
	if len(parts) < 2 {
		return "", "", false
	}
	outbound := strings.TrimSpace(parts[0])
	source := strings.TrimSpace(parts[1])
	upper := strings.ToUpper(strings.TrimPrefix(source, "[]"))
	if (upper == "GEOSITE" || upper == "GEOIP") && len(parts) >= 3 {
		source += "," + strings.TrimSpace(parts[2])
	}
	if source == "" || outbound == "" {
		return "", "", false
	}
	return outbound, source, true
}

func firstHTTP(parts []string) string {
	for _, part := range parts {
		if strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") {
			return part
		}
	}
	return "https://www.gstatic.com/generate_204"
}

func regexpMeta(value string) bool {
	return strings.ContainsAny(value, "()[]|?+*^$") || strings.HasPrefix(value, "(?")
}

func policySHA(policy portablePolicy) string {
	raw, _ := yaml.Marshal(policy)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sortedSelectedCategories(selected []string) []string {
	order := map[string]int{}
	for index, category := range builtInCategories {
		order[category.ID] = index
	}
	sort.SliceStable(selected, func(i, j int) bool { return order[selected[i]] < order[selected[j]] })
	return selected
}
