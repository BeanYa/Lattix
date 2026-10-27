import { EyeIcon } from 'lucide-react'

import { humanizeBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Chain } from '@/lib/types'

import { chainTodayBytes, formatLatency, latencyTone, type ChainStatsIndex } from './chain-stats'

interface ChainOverviewPanelProps {
  chains: Chain[]
  index: ChainStatsIndex
  statsLoaded: boolean
  onSelect: (chainId: number) => void
}

// ChainOverviewPanel 是「概览」tab 的链路一览列表（§链路统计）：每链跳间延迟与
// 今日负载；点行切到详情 tab 定位对应链路卡。chains 由页面按列表同序传入
//（直连节点无段探测不列入）。标题与今日合计由外层 board 头承载。
export function ChainOverviewPanel({
  chains,
  index,
  statsLoaded,
  onSelect,
}: ChainOverviewPanelProps) {
  return (
    <div className="cg-chain-overview">
      <div className="cg-chain-overview-rows">
        {chains.map((chain) => {
          const stats = index.byChain.get(chain.id)
          const today = chainTodayBytes(stats)
          const lineIdx = chain.id % 9
          return (
            <button
              key={chain.id}
              type="button"
              className="cg-chain-overview-row"
              onClick={() => onSelect(chain.id)}
            >
              <span
                className="cg-line-badge"
                style={{ background: `var(--panel-line-${lineIdx})` }}
              >
                L{lineIdx}
              </span>
              <span className="cg-chain-overview-name" title={chain.name}>
                {chain.name || `中转 #${chain.id}`}
              </span>
              <span
                className={cn('cg-chain-overview-latency', !stats?.latency_ms && 'is-muted')}
                title="跳间 TCP 握手 RTT 之和；段样本不全时不可用"
              >
                {stats?.latency_ms != null ? (
                  <>
                    <i data-tone={latencyTone(stats.latency_ms)} />
                    {formatLatency(stats.latency_ms)}
                  </>
                ) : (
                  '--'
                )}
              </span>
              <span className="cg-chain-overview-today" title="今日流量（折算口径）">
                {statsLoaded && today > 0 ? humanizeBytes(today) : '--'}
              </span>
            </button>
          )
        })}
      </div>
      <p className="cg-chain-overview-hint">
        <EyeIcon />
        点行前往详情查看拓扑与维护操作；延迟为跳间探测之和，负载为今日流量，经 Agent 段探测上报。
      </p>
    </div>
  )
}
