package panel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"lattix/backend/internal/store"
)

const (
	maxNameTemplateRunes = 200
	maxResolvedNameRunes = 100
	maxNameTags          = 10
)

var (
	nameVariablePattern  = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
	simpleNameKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	scopedNameKeyPattern = regexp.MustCompile(`^(ENTRY|EXIT|HOP\[(\d+)\])\.([A-Z][A-Z0-9_]*)(?:\[(\d+)\])?$`)
	globalTagPattern     = regexp.MustCompile(`^TAG\[(\d+)\]$`)
)

type nameTemplateServer struct {
	ID          int64
	Name        string
	CountryCode string
	Location    string
	Address     string
	Tags        []string
}

func nameServer(srv *store.Server) nameTemplateServer {
	return nameTemplateServer{
		ID:          srv.ID,
		Name:        srv.Alias,
		CountryCode: srv.CountryCode,
		Location:    srv.Location,
		Address:     srv.Address,
		Tags:        decodeServerTags(srv.Tags),
	}
}

// nameTemplateValues 是创建直连/中转链路时可用于名称模板的上下文。
// Servers 按客户端到 Internet 的顺序排列；无作用域服务器变量仅用于单跳链路。
type nameTemplateValues struct {
	Protocol   string
	Port       *int
	Servers    []nameTemplateServer
	PanelShort string // {{PANEL_SHORT}}：面板缩写（panel_short 设置，调用方已归一化）
}

// resolveNameTemplate 将名称模板解析成最终管理名称。空模板使用自动命名。
func resolveNameTemplate(tmpl string, values nameTemplateValues) (string, error) {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return "", nil
	}
	if utf8.RuneCountInString(tmpl) > maxNameTemplateRunes {
		return "", fmt.Errorf("名称模板不能超过 %d 个字符", maxNameTemplateRunes)
	}
	if strings.Count(tmpl, "{{") != strings.Count(tmpl, "}}") {
		return "", fmt.Errorf("名称模板包含无效参数格式")
	}
	for _, server := range values.Servers {
		if _, err := normalizeNameTags(server.Tags); err != nil {
			return "", err
		}
	}
	var resolveErr error
	result := nameVariablePattern.ReplaceAllStringFunc(tmpl, func(token string) string {
		if resolveErr != nil {
			return ""
		}
		match := nameVariablePattern.FindStringSubmatch(token)
		key := strings.TrimSpace(match[1])
		value, err := resolveNameVariable(key, values)
		if err != nil {
			resolveErr = err
			return ""
		}
		return value
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

func resolveNameVariable(key string, values nameTemplateValues) (string, error) {
	if len(values.Servers) == 0 {
		return "", fmt.Errorf("名称模板缺少服务器上下文")
	}
	global := values.Servers[0]
	if match := globalTagPattern.FindStringSubmatch(key); match != nil {
		if len(values.Servers) > 1 {
			return "", fmt.Errorf("参数 {{%s}} 在多跳链路中必须使用 ENTRY、EXIT 或 HOP[n] 作用域", key)
		}
		index, _ := strconv.Atoi(match[1])
		return resolveServerTag(global, index, key)
	}
	if match := scopedNameKeyPattern.FindStringSubmatch(key); match != nil {
		serverIndex := 0
		switch match[1] {
		case "ENTRY":
			serverIndex = 0
		case "EXIT":
			serverIndex = len(values.Servers) - 1
		default:
			serverIndex, _ = strconv.Atoi(match[2])
		}
		if serverIndex < 0 || serverIndex >= len(values.Servers) {
			return "", fmt.Errorf("参数 {{%s}} 数组越界：当前链路共 %d 跳", key, len(values.Servers))
		}
		server := values.Servers[serverIndex]
		if match[3] == "TAG" {
			if match[4] == "" {
				return "", fmt.Errorf("参数 {{%s}} 缺少标签索引", key)
			}
			tagIndex, _ := strconv.Atoi(match[4])
			return resolveServerTag(server, tagIndex, key)
		}
		if match[4] != "" {
			return "", fmt.Errorf("参数 {{%s}} 的属性不支持数组索引", key)
		}
		return resolveServerAttribute(server, match[3], key)
	}
	if !simpleNameKeyPattern.MatchString(key) {
		return "", fmt.Errorf("名称模板包含无效参数 {{%s}}", key)
	}
	switch key {
	case "PANEL_SHORT":
		return values.PanelShort, nil
	case "PROTOCOL":
		return values.Protocol, nil
	case "PORT":
		if values.Port == nil {
			return "auto", nil
		}
		return strconv.Itoa(*values.Port), nil
	case "HOPS":
		return strconv.Itoa(len(values.Servers)), nil
	case "ENTRY":
		return values.Servers[0].Name, nil
	case "EXIT":
		return values.Servers[len(values.Servers)-1].Name, nil
	case "SERVER":
		if len(values.Servers) > 1 {
			return "", fmt.Errorf("参数 {{%s}} 在多跳链路中必须使用 ENTRY、EXIT 或 HOP[n] 作用域", key)
		}
		return global.Name, nil
	case "SERVER_ID":
		if len(values.Servers) > 1 {
			return "", fmt.Errorf("参数 {{%s}} 在多跳链路中必须使用 ENTRY、EXIT 或 HOP[n] 作用域", key)
		}
		return strconv.FormatInt(global.ID, 10), nil
	case "NAME", "ID", "COUNTRY", "COUNTRY_CODE", "COUNTRY_FLAG", "LOCATION", "ADDRESS":
		if len(values.Servers) > 1 {
			return "", fmt.Errorf("参数 {{%s}} 在多跳链路中必须使用 ENTRY、EXIT 或 HOP[n] 作用域", key)
		}
		return resolveServerAttribute(global, key, key)
	default:
		return "", fmt.Errorf("不支持的名称参数 {{%s}}", key)
	}
}

func resolveServerAttribute(server nameTemplateServer, attribute, key string) (string, error) {
	var value string
	switch attribute {
	case "ID":
		value = strconv.FormatInt(server.ID, 10)
	case "NAME":
		value = server.Name
	case "COUNTRY":
		value = countryName(server.CountryCode)
	case "COUNTRY_CODE":
		value = server.CountryCode
	case "COUNTRY_FLAG":
		value = countryFlag(server.CountryCode)
	case "LOCATION":
		value = server.Location
	case "ADDRESS":
		value = server.Address
	default:
		return "", fmt.Errorf("参数 {{%s}} 的属性不存在", key)
	}
	if value == "" {
		return "", fmt.Errorf("参数 {{%s}} 缺少对应的服务器资料", key)
	}
	return value, nil
}

func resolveServerTag(server nameTemplateServer, index int, key string) (string, error) {
	tags, err := normalizeNameTags(server.Tags)
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(tags) {
		return "", fmt.Errorf("参数 {{%s}} 数组越界：当前服务器共 %d 个标签", key, len(tags))
	}
	return tags[index], nil
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
