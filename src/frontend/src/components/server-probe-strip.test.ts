import { describe, expect, it } from 'vitest'

import type { CatalogProbeRound } from '@/lib/types'
import { PROBE_ROUND_SLOTS, probeRoundTone, probeStripRounds } from './server-probe-strip'

function round(ms: number | null): CatalogProbeRound {
  return { latency_ms: ms, measured_at: '2026-10-29T00:00:00Z' }
}

describe('probeRoundTone', () => {
  it('阈值与链路统计一致：<100 正常，100–300 警告，≥300 严重，null（失败）归严重', () => {
    expect(probeRoundTone(0)).toBe('ok')
    expect(probeRoundTone(99.9)).toBe('ok')
    expect(probeRoundTone(100)).toBe('warn')
    expect(probeRoundTone(299)).toBe('warn')
    expect(probeRoundTone(300)).toBe('fail')
    expect(probeRoundTone(null)).toBe('fail')
  })
})

describe('probeStripRounds', () => {
  it('取最近 slots 轮，时间升序原样保留', () => {
    const rounds = Array.from({ length: 20 }, (_, index) => round(index))
    const strip = probeStripRounds(rounds, PROBE_ROUND_SLOTS)
    expect(strip).toHaveLength(PROBE_ROUND_SLOTS)
    expect(strip.map((item) => item?.latency_ms)).toEqual([
      8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
    ])
  })

  it('不足 slots 时左侧补空槽，最新轮仍靠右', () => {
    const strip = probeStripRounds([round(12), round(48)], 4)
    expect(strip).toEqual([
      null,
      null,
      { latency_ms: 12, measured_at: '2026-10-29T00:00:00Z' },
      round(48),
    ])
  })

  it('无样本时全部为空槽', () => {
    expect(probeStripRounds(undefined, 3)).toEqual([null, null, null])
    expect(probeStripRounds([], 3)).toEqual([null, null, null])
  })
})
