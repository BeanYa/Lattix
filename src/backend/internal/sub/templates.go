package sub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
)

const maxTemplateBytes = 2 << 20

var builtInTemplateSources = []store.SubscriptionTemplate{
	{ID: "acl4ssr-standard", Name: "ACL4SSR 标准版", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR.ini"},
	{ID: "acl4ssr-online", Name: "ACL4SSR 在线版", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online.ini"},
	{ID: "acl4ssr-online-full", Name: "ACL4SSR 在线全分组", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online_Full.ini"},
	{ID: "acl4ssr-online-full-adblock", Name: "ACL4SSR 在线全分组去广告", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online_Full_AdblockPlus.ini"},
	{ID: "acl4ssr-online-mini", Name: "ACL4SSR 在线精简版", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/master/Clash/config/ACL4SSR_Online_Mini.ini"},
	{ID: "aethersailor-standard", Name: "Aethersailor 标准", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/Aethersailor/Custom_OpenClash_Rules/main/cfg/Custom_Clash.ini"},
	{ID: "aethersailor-full", Name: "Aethersailor 全分组", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/Aethersailor/Custom_OpenClash_Rules/main/cfg/Custom_Clash_Full.ini"},
	{ID: "aethersailor-lite", Name: "Aethersailor 轻量", Kind: "acl4ssr", Origin: "github", Readonly: true, License: "CC BY-SA 4.0", SourceURL: "https://raw.githubusercontent.com/Aethersailor/Custom_OpenClash_Rules/main/cfg/Custom_Clash_Lite.ini"},
}

type CategoryDTO struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Icon       string `json:"icon"`
	Default    string `json:"default"`
	InMinimal  bool   `json:"in_minimal"`
	InBalanced bool   `json:"in_balanced"`
}

func Categories() []CategoryDTO {
	minimal, balanced := stringSet(presetCategories["minimal"]), stringSet(presetCategories["balanced"])
	out := make([]CategoryDTO, 0, len(builtInCategories))
	for _, category := range builtInCategories {
		out = append(out, CategoryDTO{
			ID: category.ID, Label: category.Label, Icon: category.Icon, Default: category.Default,
			InMinimal: minimal[category.ID], InBalanced: balanced[category.ID],
		})
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func (s *Server) ensureBuiltInTemplateSources(ctx context.Context) {
	for _, template := range builtInTemplateSources {
		_ = s.st.EnsureSubscriptionTemplate(ctx, template)
	}
}

func (s *Server) Templates(ctx context.Context) ([]store.SubscriptionTemplate, error) {
	return s.st.ListSubscriptionTemplates(ctx)
}

func (s *Server) SaveTemplate(ctx context.Context, template store.SubscriptionTemplate) (store.SubscriptionTemplate, error) {
	template.Name = strings.TrimSpace(template.Name)
	template.Kind = strings.ToLower(strings.TrimSpace(template.Kind))
	template.SourceURL = strings.TrimSpace(template.SourceURL)
	if template.Name == "" {
		return store.SubscriptionTemplate{}, errors.New("template name is required")
	}
	if template.ID == "" {
		template.ID = "local-" + contentSHA(template.Name + time.Now().String())[:16]
	} else if !validTemplateID(template.ID) {
		return store.SubscriptionTemplate{}, errors.New("template id contains unsupported characters")
	} else if existing, err := s.st.SubscriptionTemplateByID(ctx, template.ID); err == nil && existing.Readonly {
		return store.SubscriptionTemplate{}, errors.New("built-in templates are read-only; clone before editing")
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.SubscriptionTemplate{}, err
	}
	if !validTemplateKind(template.Kind) {
		return store.SubscriptionTemplate{}, fmt.Errorf("unsupported template kind %q", template.Kind)
	}
	if template.SourceURL != "" {
		normalized, err := normalizeGitHubURL(template.SourceURL)
		if err != nil {
			return store.SubscriptionTemplate{}, err
		}
		template.SourceURL = normalized
		template.Origin = "github"
		now := time.Now().UTC()
		content, err := s.files.GetText(ctx, template.SourceURL, maxTemplateBytes)
		if err != nil {
			return store.SubscriptionTemplate{}, fmt.Errorf("fetch template: %w", err)
		}
		if err := validateTemplate(template.Kind, content); err != nil {
			return store.SubscriptionTemplate{}, err
		}
		template.Content = content
		template.ContentSHA256 = contentSHA(content)
		template.FetchedAt = &now
		template.LastAttemptAt = &now
	} else {
		template.Origin = "local"
		if err := validateTemplate(template.Kind, template.Content); err != nil {
			return store.SubscriptionTemplate{}, err
		}
		template.ContentSHA256 = contentSHA(template.Content)
	}
	policy, err := routingPolicyForTemplate(template)
	if err != nil {
		return store.SubscriptionTemplate{}, err
	}
	rules, err := s.fetchTemplateRules(ctx, template, policy)
	if err != nil {
		return store.SubscriptionTemplate{}, err
	}
	template.Readonly = false
	if err := s.st.UpsertSubscriptionTemplateWithRules(ctx, template, rules, true); err != nil {
		return store.SubscriptionTemplate{}, err
	}
	return s.st.SubscriptionTemplateByID(ctx, template.ID)
}

func validTemplateKind(kind string) bool {
	switch kind {
	case "portable", "acl4ssr", "mihomo", "singbox", "quanx":
		return true
	default:
		return false
	}
}

func (s *Server) CloneTemplate(ctx context.Context, id, name string) (store.SubscriptionTemplate, error) {
	source, err := s.st.SubscriptionTemplateByID(ctx, id)
	if err != nil {
		return store.SubscriptionTemplate{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = source.Name + " 副本"
	}
	return s.SaveTemplate(ctx, store.SubscriptionTemplate{Name: name, Kind: source.Kind, Content: source.Content, License: source.License})
}

func (s *Server) DeleteTemplate(ctx context.Context, id string) error {
	return s.st.DeleteSubscriptionTemplate(ctx, id)
}

func (s *Server) RefreshTemplates(ctx context.Context, onlyID string) error {
	s.refresh.Lock()
	defer s.refresh.Unlock()
	templates, err := s.st.ListSubscriptionTemplates(ctx)
	if err != nil {
		return err
	}
	var failures []string
	downloads := make(map[string]string)
	for _, template := range templates {
		if template.Origin != "github" || (onlyID != "" && template.ID != onlyID) {
			continue
		}
		now := time.Now().UTC()
		content, fetchErr := s.files.GetText(ctx, template.SourceURL, maxTemplateBytes)
		if fetchErr == nil {
			fetchErr = validateTemplate(template.Kind, content)
		}
		template.LastAttemptAt = &now
		if fetchErr != nil {
			template.LastError = fetchErr.Error()
			failures = append(failures, template.Name+": "+fetchErr.Error())
			if saveErr := s.st.UpsertSubscriptionTemplate(ctx, template); saveErr != nil {
				return saveErr
			}
		} else {
			candidate := template
			candidate.Content = content
			candidate.ContentSHA256 = contentSHA(content)
			candidate.FetchedAt = &now
			candidate.LastError = ""
			policy, policyErr := routingPolicyForTemplate(candidate)
			var rules []store.SubscriptionTemplateRule
			if policyErr == nil {
				rules, policyErr = s.fetchTemplateRulesCached(ctx, candidate, policy, downloads)
			}
			if policyErr != nil {
				template.LastError = policyErr.Error()
				failures = append(failures, template.Name+": "+policyErr.Error())
				if saveErr := s.st.UpsertSubscriptionTemplate(ctx, template); saveErr != nil {
					return saveErr
				}
				continue
			}
			if saveErr := s.st.UpsertSubscriptionTemplateWithRules(ctx, candidate, rules, true); saveErr != nil {
				return saveErr
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func routingPolicyForTemplate(template store.SubscriptionTemplate) (portablePolicy, error) {
	switch template.Kind {
	case "portable":
		return parsePortablePolicy(template.Content)
	case "acl4ssr":
		return parseACL4SSR(template.Content)
	default:
		return portablePolicy{}, nil
	}
}

func validateTemplate(kind, content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("template content is empty")
	}
	switch kind {
	case "portable":
		_, err := parsePortablePolicy(content)
		return err
	case "acl4ssr":
		_, err := parseACL4SSR(content)
		return err
	case "mihomo":
		var document map[string]any
		if err := yaml.Unmarshal([]byte(content), &document); err != nil {
			return err
		}
		for key := range document {
			lower := strings.ToLower(key)
			switch lower {
			case "external-controller", "external-controller-tls", "secret", "authentication", "script":
				return fmt.Errorf("Mihomo template contains forbidden field %q", key)
			}
		}
		return nil
	case "singbox":
		var document map[string]any
		if err := json.Unmarshal([]byte(content), &document); err != nil {
			return fmt.Errorf("sing-box template must be one JSON object: %w", err)
		}
		if experimental, ok := document["experimental"].(map[string]any); ok {
			if clashAPI, ok := experimental["clash_api"].(map[string]any); ok && nonemptyJSONValue(clashAPI["external_controller"]) {
				return errors.New("sing-box template contains forbidden experimental.clash_api.external_controller")
			}
			if v2rayAPI, ok := experimental["v2ray_api"].(map[string]any); ok && nonemptyJSONValue(v2rayAPI["listen"]) {
				return errors.New("sing-box template contains forbidden experimental.v2ray_api.listen")
			}
		}
		return nil
	case "quanx":
		lower := strings.ToLower(content)
		for _, forbidden := range []string{"[http_backend]", "resource_parser_url"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("Quantumult X template contains forbidden field %q", forbidden)
			}
		}
		if !strings.Contains(content, "__LATTIX_SERVERS__") {
			return errors.New("Quantumult X native template requires __LATTIX_SERVERS__")
		}
		return nil
	default:
		return fmt.Errorf("unsupported template kind %q", kind)
	}
}

func nonemptyJSONValue(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}

func normalizeGitHubURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return "", errors.New("template source must be an HTTPS GitHub file URL")
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "raw.githubusercontent.com":
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 4 {
			return "", errors.New("raw GitHub URL must identify a file")
		}
		parsed.RawQuery, parsed.Fragment = "", ""
		return parsed.String(), nil
	case "github.com":
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 5 || parts[2] != "blob" {
			return "", errors.New("GitHub URL must point to one file using /blob/<ref>/...")
		}
		return "https://raw.githubusercontent.com/" + parts[0] + "/" + parts[1] + "/" + parts[3] + "/" + path.Join(parts[4:]...), nil
	default:
		return "", errors.New("only github.com and raw.githubusercontent.com template sources are allowed")
	}
}

func normalizeRuleSourceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"clash-domain:", "clash-classic:", "clash-ipcidr:"} {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			raw = strings.TrimSpace(raw[len(prefix):])
			break
		}
	}
	parsed, err := url.Parse(raw)
	if err == nil && (strings.EqualFold(parsed.Hostname(), "cdn.jsdelivr.net") || strings.EqualFold(parsed.Hostname(), "testingcf.jsdelivr.net")) {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 4 || parts[0] != "gh" {
			return "", errors.New("jsDelivr rule URL must identify a GitHub file")
		}
		repository, ref, ok := strings.Cut(parts[2], "@")
		if !ok || repository == "" || ref == "" {
			return "", errors.New("jsDelivr rule URL must pin a GitHub ref")
		}
		filePath := path.Join(parts[3:]...)
		if strings.EqualFold(path.Ext(filePath), ".mrs") {
			filePath = strings.TrimSuffix(filePath, path.Ext(filePath)) + ".yaml"
		}
		return "https://raw.githubusercontent.com/" + parts[1] + "/" + repository + "/" + ref + "/" + filePath, nil
	}
	return normalizeGitHubURL(raw)
}

func normalizeTemplateRuleSourceURL(template store.SubscriptionTemplate, raw string) (string, error) {
	if !validRelativeRuleSource(raw) {
		return normalizeRuleSourceURL(raw)
	}
	if template.SourceURL == "" {
		return "", errors.New("relative rule source requires a GitHub template source")
	}
	source, err := url.Parse(template.SourceURL)
	if err != nil || !strings.EqualFold(source.Hostname(), "raw.githubusercontent.com") {
		return "", errors.New("relative rule source requires a raw GitHub template source")
	}
	sourceParts := strings.Split(strings.Trim(source.Path, "/"), "/")
	ruleParts := strings.Split(path.Clean(strings.TrimSpace(raw)), "/")
	if len(sourceParts) < 4 || len(ruleParts) < 3 ||
		(!strings.EqualFold(ruleParts[1], sourceParts[0]) && !strings.EqualFold(ruleParts[1], sourceParts[1])) {
		return "", errors.New("relative rule source must stay in the template repository")
	}
	resolved := "https://raw.githubusercontent.com/" + sourceParts[0] + "/" + sourceParts[1] + "/" +
		sourceParts[2] + "/" + path.Join(ruleParts[2:]...)
	return normalizeGitHubURL(resolved)
}

func validRelativeRuleSource(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "%") || strings.Contains(raw, `\`) {
		return false
	}
	cleaned := path.Clean(raw)
	parts := strings.Split(cleaned, "/")
	return cleaned == raw && len(parts) >= 3 && parts[0] == "rules" &&
		parts[1] != "" && parts[1] != "." && parts[1] != ".."
}

var safeTemplateID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validTemplateID(value string) bool { return safeTemplateID.MatchString(value) }
