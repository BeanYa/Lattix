package panel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxNameTemplateRunes = 200
	maxResolvedNameRunes = 100
	maxNameTags          = 10
)

var nameVariablePattern = regexp.MustCompile(`\{\{\s*([A-Z][A-Z0-9_]*)\s*\}\}`)

// nameTemplateValues 是创建节点/链路时可用于名称模板的内置参数。
// LOCATION/SERVER 对节点指所在服务器，对链路指入口服务器。
type nameTemplateValues struct {
	Location string
	ServerID int64
	Protocol string
	Port     *int
	Entry    string
	EntryID  int64
	Exit     string
	ExitID   int64
	Hops     int
	Tags     []string
}

// resolveNameTemplate 将名称模板解析成最终管理名称。空模板用于兼容旧 API，
// 由展示/订阅层继续采用历史自动命名。
func resolveNameTemplate(tmpl string, values nameTemplateValues) (string, error) {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return "", nil
	}
	if utf8.RuneCountInString(tmpl) > maxNameTemplateRunes {
		return "", fmt.Errorf("名称模板不能超过 %d 个字符", maxNameTemplateRunes)
	}
	if strings.Contains(tmpl, "{{") && !nameVariablePattern.MatchString(tmpl) {
		return "", fmt.Errorf("名称模板包含无效参数格式")
	}

	tags, err := normalizeNameTags(values.Tags)
	if err != nil {
		return "", err
	}
	var resolveErr error
	result := nameVariablePattern.ReplaceAllStringFunc(tmpl, func(token string) string {
		if resolveErr != nil {
			return ""
		}
		match := nameVariablePattern.FindStringSubmatch(token)
		key := match[1]
		switch key {
		case "LOCATION", "SERVER":
			return values.Location
		case "SERVER_ID":
			return strconv.FormatInt(values.ServerID, 10)
		case "PROTOCOL":
			return values.Protocol
		case "PORT":
			if values.Port == nil {
				return "auto"
			}
			return strconv.Itoa(*values.Port)
		case "ENTRY":
			if values.Entry == "" {
				resolveErr = fmt.Errorf("参数 {{ENTRY}} 仅适用于链路名称")
				return ""
			}
			return values.Entry
		case "ENTRY_ID":
			if values.EntryID == 0 {
				resolveErr = fmt.Errorf("参数 {{ENTRY_ID}} 仅适用于链路名称")
				return ""
			}
			return strconv.FormatInt(values.EntryID, 10)
		case "EXIT":
			if values.Exit == "" {
				resolveErr = fmt.Errorf("参数 {{EXIT}} 仅适用于链路名称")
				return ""
			}
			return values.Exit
		case "EXIT_ID":
			if values.ExitID == 0 {
				resolveErr = fmt.Errorf("参数 {{EXIT_ID}} 仅适用于链路名称")
				return ""
			}
			return strconv.FormatInt(values.ExitID, 10)
		case "HOPS":
			if values.Hops == 0 {
				resolveErr = fmt.Errorf("参数 {{HOPS}} 仅适用于链路名称")
				return ""
			}
			return strconv.Itoa(values.Hops)
		default:
			if strings.HasPrefix(key, "TAG_") {
				index, parseErr := strconv.Atoi(strings.TrimPrefix(key, "TAG_"))
				if parseErr == nil && index >= 1 {
					if index > len(tags) {
						resolveErr = fmt.Errorf("参数 {{%s}} 缺少对应的自定义标签", key)
						return ""
					}
					return tags[index-1]
				}
			}
			resolveErr = fmt.Errorf("不支持的名称参数 {{%s}}", key)
			return ""
		}
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	if strings.Contains(result, "{{") || strings.Contains(result, "}}") {
		return "", fmt.Errorf("名称模板包含未解析的参数")
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return "", fmt.Errorf("名称模板解析结果不能为空")
	}
	if utf8.RuneCountInString(result) > maxResolvedNameRunes {
		return "", fmt.Errorf("名称解析结果不能超过 %d 个字符", maxResolvedNameRunes)
	}
	return result, nil
}

func normalizeNameTags(tags []string) ([]string, error) {
	if len(tags) > maxNameTags {
		return nil, fmt.Errorf("自定义标签不能超过 %d 个", maxNameTags)
	}
	out := make([]string, len(tags))
	for i, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, fmt.Errorf("第 %d 个自定义标签不能为空", i+1)
		}
		if utf8.RuneCountInString(tag) > 50 {
			return nil, fmt.Errorf("第 %d 个自定义标签不能超过 50 个字符", i+1)
		}
		out[i] = tag
	}
	return out, nil
}

func decodeServerTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	return tags
}

func encodeServerTags(tags []string) (string, error) {
	tags, err := normalizeNameTags(tags)
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", nil
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
