import type { ChainSegmentStat, ChainStats, ChainStatsResponse } from '@/lib/types'

export type LatencyTone = 'ok' | 'warn' | 'fail'

// 延迟健康阈值与服务器探针监控一致：<100ms 正常，100–300ms 警告，>300ms 严重。
export function latencyTone(ms: number): LatencyTone {
  if (ms < 100) return 'ok'
  if (ms < 300) return 'warn'
  return 'fail'
}

export function formatLatency(ms: number): string {
  if (ms < 10) return `${Math.round(ms * 10) / 10} ms`
  return `${Math.round(ms)} ms`
}

export interface ChainStatsIndex {
  byChain: Map<number, ChainStats>
  // 段探测按"执行探测的本跳"索引：直连段由上游 forward 探测、反向段由下游
  // bridge 探测，完整链路的每个相邻跳对恰好一条。
  byHop: Map<number, ChainSegmentStat[]>
}

export function buildChainStatsIndex(response: ChainStatsResponse | null): ChainStatsIndex {
  const byChain = new Map<number, ChainStats>()
  const byHop = new Map<number, ChainSegmentStat[]>()
  for (const stats of response?.chains ?? []) {
    byChain.set(stats.chain_id, stats)
    for (const segment of stats.segments) {
      const list = byHop.get(segment.hop_id)
      if (list) list.push(segment)
      else byHop.set(segment.hop_id, [segment])
    }
  }
  return { byChain, byHop }
}

// segmentForLink 返回相邻两跳之间物理段的最新探测：上游 forward 探测或
// 下游 bridge 探测任一存在即用（完整链路恰一条；带 RTT 的优先，失败样本兜底展示）。
export function segmentForLink(
  index: ChainStatsIndex,
  upstreamHopId: number,
  downstreamHopId: number,
): ChainSegmentStat | null {
  const candidates = [
    ...(index.byHop.get(upstreamHopId) ?? []).filter((s) => s.kind === 'forward'),
    ...(index.byHop.get(downstreamHopId) ?? []).filter((s) => s.kind === 'bridge'),
  ]
  if (candidates.length === 0) return null
  return candidates.find((s) => s.latency_ms !== null) ?? candidates[0]
}

// chainTodayBytes 是链路今日负载（折算口径上/下行之和）。
export function chainTodayBytes(stats?: ChainStats): number {
  if (!stats?.today) return 0
  return stats.today.effective_up + stats.today.effective_down
}
