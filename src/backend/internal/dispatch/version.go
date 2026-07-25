package dispatch

import (
	"fmt"
	"strconv"
	"strings"
)

// 兼容窗口（§18 版本迭代）：panel 允许领先 agent 一个发布位次。
// 项目版本为 v0.0.x 形态（0.0.1→0.0.2 是 patch 递进），因此窗口作用于
// 主版本一致下的"首个差异位"：minor 不同比 minor 差，minor 相同比 patch 差，
// 差值超过 maxAgentVersionLag 即出窗口。出窗口的 agent 仍可连接（hello 通过），
// 但置 upgrade_needed：常规命令暂停下发，仅放行 upgrade_agent / uninstall。
// 改窗口是显式代码变更，需与 CI compat matrix 同步。
const maxAgentVersionLag = 1

// parseVersion 解析 vMAJOR.MINOR.PATCH[-suffix]（允许省略 PATCH）；
// "dev"、空串或非法格式返回 ok=false（dev 构建不参与版本门控）。
func parseVersion(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, 0, false
	}
	nums := make([]int, 3)
	for i := 0; i < len(nums) && i < len(parts); i++ {
		s, _, _ := strings.Cut(parts[i], "-") // 允许预发布后缀（如 "2-rc1"）
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// evaluateAgentVersion 在 hello 认证时比对 agent 与面板版本：
// 返回 rejectReason（非空 = 拒绝连接）与 upgradeNeeded（置位升级需求标志）。
// 设计要点：
//   - 主版本不一致（无论谁新）：拒绝——跨主版本无兼容承诺，明说比静默诡异行为好；
//   - agent 落后超窗口：连接保留但 upgradeNeeded=true（留升级/卸载通道）；
//   - agent 比面板新（正常被安装命令 pinning 排除）：允许，仅日志提示；
//   - 任一端为 dev 构建：不做门控。
func evaluateAgentVersion(panelVersion, agentVersion string) (rejectReason string, upgradeNeeded bool) {
	pMajor, pMinor, pPatch, pok := parseVersion(panelVersion)
	aMajor, aMinor, aPatch, aok := parseVersion(agentVersion)
	if !pok || !aok {
		return "", false
	}
	if pMajor != aMajor {
		return fmt.Sprintf("agent 版本 %s 与面板版本 %s 主版本不兼容，请升级 agent（或面板）", agentVersion, panelVersion), false
	}
	lag := pPatch - aPatch
	if pMinor != aMinor {
		lag = pMinor - aMinor
	}
	if lag > maxAgentVersionLag {
		return "", true
	}
	return "", false
}
