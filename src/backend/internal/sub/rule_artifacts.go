package sub

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
)

const maxRuleBytes = 8 << 20

type parsedRule struct {
	Kind  string
	Value string
	Extra []string
}

func (s *Server) fetchTemplateRules(
	ctx context.Context, template store.SubscriptionTemplate, policy portablePolicy,
) ([]store.SubscriptionTemplateRule, error) {
	return s.fetchTemplateRulesCached(ctx, template, policy, nil)
}

func (s *Server) fetchTemplateRulesCached(
	ctx context.Context, template store.SubscriptionTemplate, policy portablePolicy, downloads map[string]string,
) ([]store.SubscriptionTemplateRule, error) {
	rules := make([]store.SubscriptionTemplateRule, 0, len(policy.RemoteRule))
	for _, remote := range policy.RemoteRule {
		sourceURL, err := normalizeTemplateRuleSourceURL(template, remote.URL)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", remote.Name, err)
		}
		content, ok := downloads[sourceURL]
		if !ok {
			content, err = s.files.GetText(ctx, sourceURL, maxRuleBytes)
			if err != nil {
				return nil, fmt.Errorf("fetch rule %s: %w", remote.Name, err)
			}
			if downloads != nil {
				downloads[sourceURL] = content
			}
		}
		if _, err := parseRuleContent(content); err != nil {
			return nil, fmt.Errorf("rule %s: %w", remote.Name, err)
		}
		rules = append(rules, store.SubscriptionTemplateRule{
			TemplateID: template.ID, TemplateSHA256: template.ContentSHA256,
			Name: remote.Name, SourceURL: sourceURL, Content: []byte(content), ContentSHA256: contentSHA(content),
		})
	}
	return rules, nil
}

func (s *Server) cachedTemplateRules(
	ctx context.Context, template *store.SubscriptionTemplate, policy portablePolicy,
) ([]store.SubscriptionTemplateRule, error) {
	if len(policy.RemoteRule) == 0 {
		return nil, nil
	}
	if template == nil {
		return nil, errors.New("remote rules require a cached template source")
	}
	cached, err := s.st.SubscriptionTemplateRules(ctx, template.ID, template.ContentSHA256)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]store.SubscriptionTemplateRule, len(cached))
	for _, rule := range cached {
		byName[rule.Name] = rule
	}
	ordered := make([]store.SubscriptionTemplateRule, 0, len(policy.RemoteRule))
	for _, remote := range policy.RemoteRule {
		rule, ok := byName[remote.Name]
		if !ok {
			return nil, fmt.Errorf("template rule %s is not cached; refresh the template first", remote.Name)
		}
		normalized, err := normalizeTemplateRuleSourceURL(*template, remote.URL)
		if err != nil || normalized != rule.SourceURL {
			return nil, fmt.Errorf("template rule %s cache does not match its source", remote.Name)
		}
		ordered = append(ordered, rule)
	}
	return ordered, nil
}

func buildRuleArtifacts(cached []store.SubscriptionTemplateRule) ([]store.SubscriptionRuleFile, error) {
	files := make([]store.SubscriptionRuleFile, 0, len(cached)*3)
	for _, cachedRule := range cached {
		rules, err := parseRuleContent(string(cachedRule.Content))
		if err != nil {
			return nil, fmt.Errorf("parse cached rule %s: %w", cachedRule.Name, err)
		}
		mihomo, err := renderMihomoRuleSet(rules)
		if err != nil {
			return nil, err
		}
		singbox, err := renderSingboxRuleSet(rules)
		if err != nil {
			return nil, err
		}
		quanx := renderQuanXRuleSet(rules)
		files = append(files,
			store.SubscriptionRuleFile{Name: cachedRule.Name, Format: "mihomo", SourceSHA256: cachedRule.ContentSHA256, ContentType: "text/yaml; charset=utf-8", Content: mihomo},
			store.SubscriptionRuleFile{Name: cachedRule.Name, Format: "singbox", SourceSHA256: cachedRule.ContentSHA256, ContentType: "application/json; charset=utf-8", Content: singbox},
			store.SubscriptionRuleFile{Name: cachedRule.Name, Format: "quanx", SourceSHA256: cachedRule.ContentSHA256, ContentType: "text/plain; charset=utf-8", Content: quanx},
		)
	}
	return files, nil
}

func rewriteRemoteRuleURLs(
	policy portablePolicy, cached []store.SubscriptionTemplateRule, format, baseURL, token string,
) (portablePolicy, error) {
	byName := make(map[string]store.SubscriptionTemplateRule, len(cached))
	for _, rule := range cached {
		byName[rule.Name] = rule
	}
	for index := range policy.RemoteRule {
		rule, ok := byName[policy.RemoteRule[index].Name]
		if !ok {
			return portablePolicy{}, fmt.Errorf("rule artifact %s is missing", policy.RemoteRule[index].Name)
		}
		policy.RemoteRule[index].URL = strings.TrimRight(baseURL, "/") + "/sub/" + url.PathEscape(token) +
			"/rules/" + rule.ContentSHA256 + "/" + format + "/" + url.PathEscape(rule.Name)
	}
	return policy, nil
}

func snapshotSourceSHA(policy portablePolicy, rules []store.SubscriptionTemplateRule) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(policySHA(policy)))
	for _, rule := range rules {
		_, _ = hash.Write([]byte("\n" + rule.Name + ":" + rule.ContentSHA256))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func parseRuleContent(content string) ([]parsedRule, error) {
	var document struct {
		Payload []string `yaml:"payload"`
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	lines := []string(nil)
	if strings.Contains(trimmed, "payload:") {
		if err := yaml.Unmarshal([]byte(trimmed), &document); err != nil {
			return nil, fmt.Errorf("parse rule-provider YAML: %w", err)
		}
		if document.Payload != nil {
			lines = document.Payload
		}
	} else {
		scanner := bufio.NewScanner(strings.NewReader(trimmed))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			lines = append(lines, line)
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	if lines == nil {
		return nil, errors.New("rule-provider YAML contains no payload")
	}
	rules := make([]parsedRule, 0, len(lines))
	for index, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			rule, err := inferProviderRule(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", index+1, err)
			}
			rules = append(rules, rule)
			continue
		}
		kind := strings.ToUpper(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if value == "" || !supportedRuleKind(kind) {
			return nil, fmt.Errorf("line %d: unsupported or empty rule type %q", index+1, kind)
		}
		extra := make([]string, 0, len(parts)-2)
		for _, item := range parts[2:] {
			item = strings.TrimSpace(item)
			if item != "" {
				extra = append(extra, item)
			}
		}
		rules = append(rules, parsedRule{Kind: kind, Value: value, Extra: extra})
	}
	return rules, nil
}

func inferProviderRule(value string) (parsedRule, error) {
	value = strings.TrimSpace(value)
	if _, _, err := net.ParseCIDR(value); err == nil {
		kind := "IP-CIDR"
		if strings.Contains(value, ":") {
			kind = "IP-CIDR6"
		}
		return parsedRule{Kind: kind, Value: value}, nil
	}
	if strings.HasPrefix(value, "+.") {
		return parsedRule{Kind: "DOMAIN-SUFFIX", Value: strings.TrimPrefix(value, "+.")}, nil
	}
	if strings.HasPrefix(value, "*.") && strings.Count(value, "*") == 1 {
		return parsedRule{Kind: "DOMAIN-SUFFIX", Value: strings.TrimPrefix(value, "*.")}, nil
	}
	if strings.HasPrefix(value, "*") && strings.HasSuffix(value, "*") && strings.Count(value, "*") == 2 {
		return parsedRule{Kind: "DOMAIN-KEYWORD", Value: strings.Trim(value, "*")}, nil
	}
	if strings.Contains(value, "*") {
		pattern := regexp.QuoteMeta(value)
		pattern = strings.ReplaceAll(pattern, `\*`, ".*")
		return parsedRule{Kind: "DOMAIN-REGEX", Value: "^" + pattern + "$"}, nil
	}
	if value != "" && !strings.ContainsAny(value, " \t/") {
		return parsedRule{Kind: "DOMAIN", Value: value}, nil
	}
	return parsedRule{}, fmt.Errorf("rule %q requires a supported type and value", value)
}

func supportedRuleKind(kind string) bool {
	switch kind {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX",
		"IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "GEOIP", "GEOSITE",
		"PROCESS-NAME", "PROCESS-PATH", "USER-AGENT", "URL-REGEX", "DST-PORT", "SRC-PORT", "IP-ASN":
		return true
	default:
		return false
	}
}

func renderMihomoRuleSet(rules []parsedRule) ([]byte, error) {
	payload := make([]string, 0, len(rules))
	for _, rule := range rules {
		line := rule.Kind + "," + rule.Value
		if len(rule.Extra) > 0 {
			line += "," + strings.Join(rule.Extra, ",")
		}
		payload = append(payload, line)
	}
	return yaml.Marshal(map[string]any{"payload": payload})
}

func renderSingboxRuleSet(rules []parsedRule) ([]byte, error) {
	entries := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		entry := map[string]any{}
		switch rule.Kind {
		case "DOMAIN":
			entry["domain"] = []string{rule.Value}
		case "DOMAIN-SUFFIX":
			entry["domain_suffix"] = []string{rule.Value}
		case "DOMAIN-KEYWORD":
			entry["domain_keyword"] = []string{rule.Value}
		case "DOMAIN-REGEX":
			entry["domain_regex"] = []string{rule.Value}
		case "URL-REGEX":
			entry["domain_regex"] = []string{rule.Value}
		case "IP-CIDR", "IP-CIDR6":
			entry["ip_cidr"] = []string{rule.Value}
		case "SRC-IP-CIDR":
			entry["source_ip_cidr"] = []string{rule.Value}
		case "GEOIP":
			entry["geoip"] = []string{strings.ToLower(rule.Value)}
		case "GEOSITE":
			entry["geosite"] = []string{rule.Value}
		case "PROCESS-NAME":
			entry["process_name"] = []string{rule.Value}
		case "PROCESS-PATH":
			entry["process_path"] = []string{rule.Value}
		case "USER-AGENT":
			entry["user_agent"] = []string{rule.Value}
		case "IP-ASN":
			entry["ip_asn"] = []int{parseASN(rule.Value)}
		case "DST-PORT", "SRC-PORT":
			key := "port"
			if rule.Kind == "SRC-PORT" {
				key = "source_port"
			}
			if strings.Contains(rule.Value, "-") {
				entry[key+"_range"] = []string{rule.Value}
			} else if port, err := strconv.Atoi(rule.Value); err == nil {
				entry[key] = []int{port}
			} else {
				return nil, fmt.Errorf("invalid %s value %q", rule.Kind, rule.Value)
			}
		}
		entries = append(entries, entry)
	}
	return json.MarshalIndent(map[string]any{"version": 3, "rules": entries}, "", "  ")
}

func parseASN(value string) int {
	value = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "AS")
	number, _ := strconv.Atoi(value)
	return number
}

func renderQuanXRuleSet(rules []parsedRule) []byte {
	var body strings.Builder
	for _, rule := range rules {
		kind := rule.Kind
		switch kind {
		case "DOMAIN":
			kind = "HOST"
		case "DOMAIN-SUFFIX":
			kind = "HOST-SUFFIX"
		case "DOMAIN-KEYWORD":
			kind = "HOST-KEYWORD"
		case "IP-CIDR6":
			kind = "IP6-CIDR"
		}
		body.WriteString(kind + "," + rule.Value)
		for _, extra := range rule.Extra {
			if strings.EqualFold(extra, "no-resolve") {
				body.WriteString(",no-resolve")
			}
		}
		body.WriteByte('\n')
	}
	return []byte(body.String())
}
