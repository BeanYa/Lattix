import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'

import type { ChainSegmentStat } from '@/lib/types'
import { cn } from '@/lib/utils'

import { formatLatency, latencyTone } from './chain-stats'

// HopLatency 是跳间连线上的段延迟标注（§链路统计）：来源优先 tcp（复用链路
// 监听端口）、回退 icmp；失败/超时样本保留"超时"字样作连通性参考。
function HopLatency({ segment }: { segment: ChainSegmentStat | null }) {
  if (!segment) return null
  if (segment.latency_ms === null) {
    return (
      <span className="cg-hop-latency" data-tone="fail" title="最近一次段探测失败/超时">
        超时
      </span>
    )
  }
  return (
    <span
      className="cg-hop-latency"
      data-tone={latencyTone(segment.latency_ms)}
      title={`跳间 ${segment.kind.toUpperCase()} 探测 RTT`}
    >
      {formatLatency(segment.latency_ms)}
    </span>
  )
}

// 外部直链出口段固定展示两个来源：ICMP Ping 与 TCP 握手（外部节点无 Agent，
// 由上游托管跳直接拨外部节点 address:port / ping 同一地址）。
const EXTERNAL_LATENCY_SOURCES = [
  { kind: 'icmp', label: 'Ping', title: '入口 → 外部节点 ICMP Ping RTT' },
  { kind: 'tcp', label: 'TCP', title: '入口 → 外部节点 TCP 握手 RTT' },
] as const

function sourceTone(segment: ChainSegmentStat | null): string {
  if (!segment) return 'idle'
  return segment.latency_ms === null ? 'fail' : latencyTone(segment.latency_ms)
}

function sourceText(segment: ChainSegmentStat | null): string {
  if (!segment) return '—'
  return segment.latency_ms === null ? '超时' : formatLatency(segment.latency_ms)
}

function HopLatencySources({ segments }: { segments: ChainSegmentStat[] }) {
  return (
    <span className="cg-hop-latency-group" aria-label="入口到外部节点延迟">
      {EXTERNAL_LATENCY_SOURCES.map(({ kind, label, title }) => {
        const sample = segments.find((segment) => segment.kind === kind) ?? null
        return (
          <span key={kind} className="cg-hop-latency" data-tone={sourceTone(sample)} title={title}>
            {label} {sourceText(sample)}
          </span>
        )
      })}
    </span>
  )
}

// HopLink 是相邻两跳之间的连线：长箭头 + 段延迟标注。有延迟样本时整条箭头
// （线体 + 箭头头）跟随延迟状态色，与标注一致；无样本保持中性/线路色。
// 外部直链出口段（调用方提供 segments）展示 Ping 与 TCP 两个来源，其余段沿用首选来源。
export function HopLink({
  segment,
  segments,
  flowing,
}: {
  segment: ChainSegmentStat | null
  segments?: ChainSegmentStat[]
  flowing: boolean
}) {
  const tone = segment
    ? segment.latency_ms === null
      ? 'fail'
      : latencyTone(segment.latency_ms)
    : null
  return (
    <span className={cn('cg-hop-link', flowing && 'is-flowing')} data-tone={tone}>
      {segments ? <HopLatencySources segments={segments} /> : <HopLatency segment={segment} />}
      <span className="cg-hop-link-track" aria-hidden="true">
        <i />
        <ChevronDownIcon className="cg-hop-link-arrow-v" strokeWidth={3} />
        <ChevronRightIcon className="cg-hop-link-arrow-h" strokeWidth={3} />
      </span>
    </span>
  )
}
