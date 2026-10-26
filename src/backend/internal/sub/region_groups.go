package sub

import "strings"

// regionOnlyOptions distinguishes a template's regional node pool from the
// aggregate __LATTIX_REGIONS__ selector and ordinary service/source groups.
func regionOnlyOptions(options []string) bool {
	if len(options) == 0 {
		return false
	}
	for _, option := range options {
		if !strings.HasPrefix(option, placeholderRegionPrefix) || !strings.HasSuffix(option, "__") {
			return false
		}
	}
	return true
}

// regionFallback keeps routing through the selected nonregional policy. If the
// selected final itself was regional, create a regular node selector instead.
func regionFallback(groups []policyGroup, removed map[string]bool, preferred string) (string, bool) {
	if !removed[preferred] && (preferred == "DIRECT" || preferred == "REJECT") {
		return preferred, false
	}
	for _, group := range groups {
		if !removed[group.Name] && group.Name == preferred {
			return preferred, false
		}
	}
	for _, group := range groups {
		if !removed[group.Name] && (group.Type == "select" || group.Type == "selector") {
			return group.Name, false
		}
	}
	return "🚀 节点选择", true
}

func removePolicyRegionGroups(policy *portablePolicy) {
	removed := map[string]bool{}
	for _, group := range policy.Groups {
		if group.Source == "" && regionOnlyOptions(group.Options) {
			removed[group.Name] = true
		}
	}
	if len(removed) == 0 {
		return
	}
	fallback, create := regionFallback(policy.Groups, removed, policy.Final)
	kept := make([]policyGroup, 0, len(policy.Groups))
	for _, group := range policy.Groups {
		if removed[group.Name] {
			continue
		}
		options := make([]string, 0, len(group.Options))
		for _, option := range group.Options {
			if !removed[option] {
				options = append(options, option)
			}
		}
		group.Options = options
		kept = append(kept, group)
	}
	if create {
		kept = append(kept, policyGroup{Name: fallback, Type: "select", Options: []string{placeholderAllNodes, placeholderLeafGroups}})
	}
	policy.Groups = kept
	if removed[policy.Final] {
		policy.Final = fallback
	}
	for i := range policy.Rules {
		if removed[policy.Rules[i].Outbound] {
			policy.Rules[i].Outbound = fallback
		}
	}
	for i := range policy.RemoteRule {
		if removed[policy.RemoteRule[i].Outbound] {
			policy.RemoteRule[i].Outbound = fallback
		}
	}
}

// removeNativeRegionGroups runs before native placeholders are expanded, when
// regional pools still carry their meaning rather than concrete node names.
func removeNativeRegionGroups(document map[string]any, format, preferred string) {
	groupKey, nameKey, optionKey, selectorType := "proxy-groups", "name", "proxies", "select"
	if format == "singbox" {
		groupKey, nameKey, optionKey, selectorType = "outbounds", "tag", "outbounds", "selector"
		if route, ok := document["route"].(map[string]any); ok {
			if final, ok := route["final"].(string); ok {
				preferred = final
			}
		}
	}
	entries, ok := document[groupKey].([]any)
	if !ok {
		return
	}
	groups := make([]policyGroup, 0, len(entries))
	removed := map[string]bool{}
	for _, entry := range entries {
		group, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := group[nameKey].(string)
		kind, _ := group["type"].(string)
		var options []string
		if values, ok := group[optionKey].([]any); ok {
			for _, value := range values {
				option, _ := value.(string)
				options = append(options, option)
			}
		}
		groups = append(groups, policyGroup{Name: name, Type: kind})
		if regionOnlyOptions(options) {
			removed[name] = true
		}
	}
	if len(removed) == 0 {
		return
	}
	fallback, create := regionFallback(groups, removed, preferred)
	kept := make([]any, 0, len(entries))
	for _, entry := range entries {
		if group, ok := entry.(map[string]any); ok {
			name, _ := group[nameKey].(string)
			if removed[name] {
				continue
			}
			if values, ok := group[optionKey].([]any); ok {
				options := make([]any, 0, len(values))
				for _, value := range values {
					option, _ := value.(string)
					if !removed[option] {
						options = append(options, value)
					}
				}
				if len(options) == 0 {
					if name == fallback {
						options = append(options, placeholderAllNodes)
					} else {
						options = append(options, fallback)
					}
				}
				group[optionKey] = options
			}
		}
		kept = append(kept, entry)
	}
	if create {
		kept = append(kept, map[string]any{nameKey: fallback, "type": selectorType, optionKey: []any{placeholderAllNodes}})
	}
	document[groupKey] = kept
	if format == "clash" {
		if rules, ok := document["rules"].([]any); ok {
			for i, rule := range rules {
				if text, ok := rule.(string); ok {
					parts := strings.Split(text, ",")
					target := len(parts) - 1
					if target > 0 && strings.TrimSpace(parts[target]) == "no-resolve" {
						target--
					}
					if target > 0 && removed[strings.TrimSpace(parts[target])] {
						parts[target] = fallback
						rules[i] = strings.Join(parts, ",")
					}
				}
			}
		}
	} else {
		rewriteNativeRegionTargets(document["route"], removed, fallback)
		rewriteNativeRegionTargets(document["dns"], removed, fallback)
	}
}

func rewriteNativeRegionTargets(value any, removed map[string]bool, fallback string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if target, ok := child.(string); ok && removed[target] && (key == "outbound" || key == "final" || key == "detour") {
				value[key] = fallback
			} else {
				rewriteNativeRegionTargets(child, removed, fallback)
			}
		}
	case []any:
		for _, child := range value {
			rewriteNativeRegionTargets(child, removed, fallback)
		}
	}
}
