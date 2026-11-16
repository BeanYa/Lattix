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

// HopLink 是相邻两跳之间的连线：长箭头 + 段延迟标注。有延迟样本时整条箭头
// （线体 + 箭头头）跟随延迟状态色，与标注一致；无样本保持中性/线路色。
export function HopLink({
  segment,
  flowing,
}: {
  segment: ChainSegmentStat | null
  flowing: boolean
}) {
  const tone = segment
    ? segment.latency_ms === null
      ? 'fail'
      : latencyTone(segment.latency_ms)
    : null
  return (
    <span className={cn('cg-hop-link', flowing && 'is-flowing')} data-tone={tone}>
      <HopLatency segment={segment} />
      <span className="cg-hop-link-track" aria-hidden="true">
        <i />
        <ChevronDownIcon className="cg-hop-link-arrow-v" strokeWidth={3} />
        <ChevronRightIcon className="cg-hop-link-arrow-h" strokeWidth={3} />
      </span>
    </span>
  )
}
