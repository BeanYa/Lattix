import { EyeIcon } from 'lucide-react'

import { humanizeBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Chain } from '@/lib/types'

import { chainTodayBytes, formatLatency, latencyTone, type ChainStatsIndex } from './chain-stats'

interface ChainOverviewPanelProps {
  chains: Chain[]
  index: ChainStatsIndex
  todayUp: number
  todayDown: number
  statsLoaded: boolean
  onSelect: (chainId: number) => void
}

// ChainOverviewPanel 是链路一览侧栏（§链路统计）：每链跳间延迟与今日负载，
// 点行定位到下方链路卡。chains 由页面按列表同序传入（仅链路条目，直连节点无段探测）。
export function ChainOverviewPanel({
  chains,
  index,
  todayUp,
  todayDown,
  statsLoaded,
  onSelect,
}: ChainOverviewPanelProps) {
  if (chains.length === 0) return null
  return (
    <aside className="cg-chain-overview" aria-label="链路一览">
      <header className="cg-chain-overview-head">
        <span className="cg-chain-overview-title">链路一览</span>
        <span className="cg-chain-overview-total" aria-label="今日流量合计">
          今日共 {humanizeBytes(todayUp + todayDown)}
        </span>
      </header>
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
        点行定位下方链路卡；延迟为跳间探测之和，负载为今日流量，经 Agent 段探测上报。
      </p>
    </aside>
  )
}
