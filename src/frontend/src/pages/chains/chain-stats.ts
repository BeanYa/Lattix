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
  // 段探测按段所属的上游跳索引；同一段可含 tcp（复用链路监听端口）与 icmp（回退）
  // 两条来源样本，segmentForLink 取首选（tcp 优先）。
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

// segmentForLink 返回相邻两跳之间物理段的最新测速样本：来源优先 tcp（复用链路
// 端口、口径与业务路径一致，失败样本如实显示"超时"以凸显业务路径故障），
// 无 tcp 行回退 icmp。
export function segmentForLink(
  index: ChainStatsIndex,
  upstreamHopId: number,
  _downstreamHopId?: number,
): ChainSegmentStat | null {
  const candidates = index.byHop.get(upstreamHopId) ?? []
  if (candidates.length === 0) return null
  const byKind = (kind: ChainSegmentStat['kind']) => candidates.find((s) => s.kind === kind)
  return byKind('tcp') ?? byKind('icmp') ?? candidates[0]
}

// chainTodayBytes 是链路今日负载（折算口径上/下行之和）。
export function chainTodayBytes(stats?: ChainStats): number {
  if (!stats?.today) return 0
  return stats.today.effective_up + stats.today.effective_down
}
