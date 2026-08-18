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

// precompiledPlaceholders 是全部预编译占位符，用于交付前残留扫描。
var precompiledPlaceholders = []string{
	placeholderLattixNodes, placeholderOuterSubsNodes,
	placeholderLattixGroup, placeholderOuterSubsGroup,
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

// assertNoPrecompiledPlaceholders 保证最终交付内容不残留预编译占位符：
// 残留说明解压环节遗漏，交付出去会被客户端当作非法节点/分组，直接判发布失败。
func assertNoPrecompiledPlaceholders(format string, content []byte) error {
	for _, token := range precompiledPlaceholders {
		if strings.Contains(string(content), token) {
			return fmt.Errorf("generate %s: 预编译占位符 %s 未被解压", format, token)
		}
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
