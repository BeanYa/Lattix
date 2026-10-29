import type { CatalogProbeRound } from '@/lib/types'

// PROBE_ROUND_SLOTS 是三网测速色块条的槽位数：与面板 rounds 下发上限一致
//（探测 5 分钟一轮，12 槽 ≈ 最近 1 小时）。
export const PROBE_ROUND_SLOTS = 12

// ProbeRoundTone 是单轮结果的评级色。阈值与链路统计 chain-stats.latencyTone 一致
//（<100 正常 / 100–300 警告 / ≥300 严重）；components 层不反向依赖 pages，本地实现。
export type ProbeRoundTone = 'ok' | 'warn' | 'fail'

export function probeRoundTone(ms: number | null): ProbeRoundTone {
  if (ms === null) return 'fail'
  if (ms < 100) return 'ok'
  if (ms < 300) return 'warn'
  return 'fail'
}

// probeStripRounds 取最近 slots 轮并左侧补空槽位：最新轮恒靠右、条宽不随
// 样本积累跳动；空槽表示尚无样本（agent 新上线/区域刚切换）。
export function probeStripRounds(
  rounds: CatalogProbeRound[] | undefined,
  slots: number,
): Array<CatalogProbeRound | null> {
  const recent = (rounds ?? []).slice(-slots)
  return [...Array<null>(slots - recent.length).fill(null), ...recent]
}
