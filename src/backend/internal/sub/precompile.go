package sub

import (
	"fmt"
	"strings"
)

// 预编译中间态占位符：发布流程先生成含占位符的中间文档（节点区与来源分组区
// 以占位符表示），选项展开/分组增强/剪枝等全部处理在中间态上完成后，最终交付
// 前再把占位符解压替换为目标格式的真实节点/分组条目。
// 节点条目占位符：
const (
	placeholderLattixNodes    = "__LATTIX-NODES__"      // 面板管理节点条目
	placeholderOuterSubsNodes = "__OUTTER-SUBS-NODES__" // 外部订阅节点条目
)

// 分组条目占位符：
const (
	placeholderLattixGroup    = "__LATTIX-GROUP__"     // 「<panelShort> 分组」条目
	placeholderOuterSubsGroup = "__OUTER-SUBS-GROUP__" // 外部订阅分组条目（解压为每订阅一组）
)

// 选项级占位符（组选项内引用节点/分组集合）：
const (
	placeholderAllNodes     = "__LATTIX_ALL__"         // 全部可用节点
	placeholderLeafGroups   = "__LATTIX_REGIONS__"     // 叶子分组（来源分组 + 地区分组 + 无地区分组）
	placeholderRegionPrefix = "__LATTIX_REGION_"       // 单地区节点，形如 __LATTIX_REGION_US__
	placeholderRegionNone   = "__LATTIX_REGION_NONE__" // 无地区节点（内部生成，模板亦可使用）
)

// precompiledPlaceholders 是全部预编译占位符，用于交付前残留扫描。
var precompiledPlaceholders = []string{
	placeholderLattixNodes, placeholderOuterSubsNodes,
	placeholderLattixGroup, placeholderOuterSubsGroup,
}

// isOptionPlaceholder 判断选项是否为选项级占位符（延迟到最终渲染展开）。
func isOptionPlaceholder(option string) bool {
	if option == placeholderAllNodes || option == placeholderLeafGroups || option == placeholderRegionNone {
		return true
	}
	return strings.HasPrefix(option, placeholderRegionPrefix) && strings.HasSuffix(option, "__")
}

// policyExpansion 是策略的最终展开上下文：选项级占位符在中间态保持原样，
// 到最后渲染阶段才按它解析为节点/分组名字。
type policyExpansion struct {
	all        []string
	byCountry  map[string][]string
	noRegion   []string
	leafGroups []string
}

// resolveOption 把单个选项解析为名字列表：占位符展开，普通选项原样返回。
func (x *policyExpansion) resolveOption(option string) []string {
	switch option {
	case placeholderAllNodes:
		return x.all
	case placeholderLeafGroups:
		return x.leafGroups
	case placeholderRegionNone:
		return x.noRegion
	}
	if strings.HasPrefix(option, placeholderRegionPrefix) && strings.HasSuffix(option, "__") {
		country := strings.TrimSuffix(strings.TrimPrefix(option, placeholderRegionPrefix), "__")
		return x.byCountry[country]
	}
	return []string{option}
}

// expandPolicyOptions 最终渲染阶段展开组选项并去重（保持顺序）；
// 无展开上下文（未经过 expandPolicy 的直接渲染）时仅去重。
func expandPolicyOptions(options []string, x *policyExpansion) []string {
	if x == nil {
		return uniqueStrings(options)
	}
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, x.resolveOption(option)...)
	}
	return uniqueStrings(out)
}

// splitNodesBySource 按来源拆分编译节点：Group 为空 = 面板管理节点，否则为
// 外部订阅节点。两类各自保持原有编译顺序（条目顺序本来就是面板在前、外部在后，
// 拆分再按占位符原位拼回不改变最终顺序）。
func splitNodesBySource(nodes []compiledNode) (panel, outer []compiledNode) {
	for _, node := range nodes {
		if node.Group == "" {
			panel = append(panel, node)
		} else {
			outer = append(outer, node)
		}
	}
	return panel, outer
}

// containsEntryPlaceholder 判断列表是否含有指定的条目级占位符字符串。
func containsEntryPlaceholder(list []any, tokens ...string) bool {
	for _, entry := range list {
		s, ok := entry.(string)
		if !ok {
			continue
		}
		for _, token := range tokens {
			if s == token {
				return true
			}
		}
	}
	return false
}

// spliceEntryPlaceholders 把列表中的占位符字符串原位替换为对应条目；载荷为空
// 时删除该占位符条目（无节点的来源不产出空分组/空节点段）。非字符串条目与
// 非占位符字符串原样保留。
func spliceEntryPlaceholders(list []any, replacements map[string][]any) []any {
	out := make([]any, 0, len(list))
	for _, entry := range list {
		s, ok := entry.(string)
		if !ok {
			out = append(out, entry)
			continue
		}
		payload, isPlaceholder := replacements[s]
		if !isPlaceholder {
			out = append(out, entry)
			continue
		}
		out = append(out, payload...)
	}
	return out
}

// assertNoPrecompiledPlaceholders 保证最终交付内容不残留任何预编译占位符：
// 条目级占位符（__LATTIX-NODES__ 等）逐个精确匹配，选项级占位符以
// __LATTIX_ 为前缀统一扫描；残留说明解压环节遗漏，交付出去会被客户端
// 当作非法节点/分组，直接判发布失败。
func assertNoPrecompiledPlaceholders(format string, content []byte) error {
	text := string(content)
	for _, token := range precompiledPlaceholders {
		if strings.Contains(text, token) {
			return fmt.Errorf("generate %s: 预编译占位符 %s 未被解压", format, token)
		}
	}
	if idx := strings.Index(text, "__LATTIX_"); idx >= 0 {
		end := idx + 2
		for end < len(text) && (text[end] == '_' || text[end] == '-' || text[end] >= 'A' && text[end] <= 'Z' || text[end] >= '0' && text[end] <= '9') {
			end++
		}
		return fmt.Errorf("generate %s: 预编译占位符 %s 未被解压", format, text[idx:end])
	}
	return nil
}

// expandTextPlaceholders 按行解压文本型中间态（QuanX/links）：占位符行原位
// 替换为对应内容行，空载荷删除该行。非占位符行原样保留。
func expandTextPlaceholders(pre string, replacements map[string][]string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(pre, "\n"), "\n") {
		if payload, ok := replacements[line]; ok {
			out = append(out, payload...)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}
