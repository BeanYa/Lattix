import { Fragment, useMemo, useState } from 'react'
import { ArrowRightIcon, ChevronRightIcon, EyeIcon, SearchIcon } from 'lucide-react'

import { humanizeBytes } from '@/lib/format'
import { chainStatusStyle } from '@/lib/status'
import { cn } from '@/lib/utils'
import type { Chain } from '@/lib/types'

import { chainTodayBytes, formatLatency, latencyTone, type ChainStatsIndex } from './chain-stats'

interface ChainOverviewPanelProps {
  chains: Chain[]
  index: ChainStatsIndex
  statsLoaded: boolean
  // 入口（第一跳）服务器被 Ban 判定的链路 id 集合（§告警分类 Ban告警）：卡片打「Banned」标。
  unreachableChainIds: Set<number>
  // 标签地域文案：三网测速目标省市（市级优先，否则省）。
  unreachableLabel: string
  onSelect: (chainId: number) => void
}

type OverviewFilter = 'all' | 'active' | 'issue' | 'relay' | 'direct'

function statusTone(tone: string): string {
  return tone === 'is-lime' ? 'is-ok' : tone
}

/** hasChainIssue 与详情 tab 的异常口径一致：链路或任一跳失败/降级。 */
function hasChainIssue(chain: Chain): boolean {
  return (
    chain.status === 'failed' ||
    chain.status === 'active_failed' ||
    chain.status === 'degraded' ||
    chain.hops.some((hop) => hop.status === 'failed')
  )
}

/** ChainOverviewCard 是链路一览的卡片：身份、状态、跳点路径、延迟与今日负载都收在
 * 一张卡里，点击前往详情 tab 查看拓扑与维护操作。 */
function ChainOverviewCard({
  chain,
  latency,
  today,
  statsLoaded,
  unreachable,
  unreachableLabel,
  onSelect,
}: {
  chain: Chain
  latency: number | null | undefined
  today: number
  statsLoaded: boolean
  unreachable: boolean
  unreachableLabel: string
  onSelect: (chainId: number) => void
}) {
  const style = chainStatusStyle[chain.status] ?? chainStatusStyle.pending
  const lineIdx = chain.id % 9
  const isDirect = chain.hops.length === 1
  const path = chain.hops.map((hop) => hop.server_alias || `#${hop.server_id}`)
  const pathText = path.join(' → ')
  return (
    <button
      type="button"
      className="cg-chain-card"
      data-tone={style.cg}
      data-unreachable={unreachable || undefined}
      aria-label={`查看链路「${chain.name || `中转 #${chain.id}`}」的拓扑与维护操作`}
      onClick={() => onSelect(chain.id)}
    >
      <span className="cg-chain-card-head">
        <span
          className="cg-line-badge"
          style={{ ['--line-color' as string]: `var(--panel-line-${lineIdx})` }}
          aria-hidden="true"
        >
          L{lineIdx}
        </span>
        <span className="cg-chain-card-title">
          <span className="cg-chain-card-name" title={chain.name}>
            {chain.name || `中转 #${chain.id}`}
          </span>
          <span className="cg-chain-card-sub">
            #{chain.id} · {isDirect ? '直连' : '中转'} · {chain.hops.length} 跳
          </span>
        </span>
        <span className={cn('cg-status', statusTone(style.cg))}>{style.label}</span>
      </span>

      {unreachable ? (
        <span className="cg-chain-card-flags">
          <span
            className="cg-chain-tag is-unreachable"
            title={`入口服务器三网测速（${unreachableLabel}）判定 Banned`}
          >
            Banned
          </span>
        </span>
      ) : null}

      <span className="cg-chain-card-path" title={pathText}>
        {path.map((stop, index) => (
          <Fragment key={`${chain.id}-${index}`}>
            {index > 0 ? (
              <ArrowRightIcon className="cg-chain-card-arrow" size={11} aria-hidden="true" />
            ) : null}
            <span
              className="cg-chain-card-stop"
              data-role={chain.hops[index]?.role}
              data-offline={chain.hops[index]?.status === 'failed' || undefined}
            >
              {stop}
            </span>
          </Fragment>
        ))}
      </span>

      <span className="cg-chain-card-metrics">
        <span className="cg-chain-card-metric">
          <span className="cg-chain-card-metric-label">段延迟</span>
          <span
            className={cn('cg-chain-card-metric-value', latency == null && 'is-muted')}
            title="跳间 TCP 握手 RTT 之和；段样本不全时不可用"
          >
            {latency != null ? (
              <>
                <i className="cg-chain-card-tone" data-tone={latencyTone(latency)} />
                {formatLatency(latency)}
              </>
            ) : (
              '--'
            )}
          </span>
        </span>
        <span className="cg-chain-card-metric">
          <span className="cg-chain-card-metric-label">今日负载</span>
          <span className="cg-chain-card-metric-value">
            {statsLoaded && today > 0 ? humanizeBytes(today) : '--'}
          </span>
        </span>
        <span className="cg-chain-card-metric">
          <span className="cg-chain-card-metric-label">流量倍率</span>
          <span className="cg-chain-card-metric-value">
            {chain.traffic_multiplier && chain.traffic_multiplier !== '1'
              ? `x${chain.traffic_multiplier}`
              : 'x1'}
          </span>
        </span>
      </span>

      <span className="cg-chain-card-foot">
        查看拓扑与维护
        <ChevronRightIcon size={13} aria-hidden="true" />
      </span>
    </button>
  )
}

// ChainOverviewPanel 是「概览」tab 的链路卡片一览（§链路统计）：每张卡给出链路身份、
// 状态、跳点路径、段延迟与今日负载；点击卡片切到详情 tab 定位对应链路。
export function ChainOverviewPanel({
  chains,
  index,
  statsLoaded,
  unreachableChainIds,
  unreachableLabel,
  onSelect,
}: ChainOverviewPanelProps) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<OverviewFilter>('all')

  const counts = useMemo(() => {
    let active = 0
    let issue = 0
    let relay = 0
    let direct = 0
    for (const chain of chains) {
      const isDirect = chain.hops.length === 1
      if (isDirect) direct += 1
      else relay += 1
      if (chain.status === 'active') active += 1
      if (hasChainIssue(chain)) issue += 1
    }
    return { all: chains.length, active, issue, relay, direct }
  }, [chains])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return chains.filter((chain) => {
      const isDirect = chain.hops.length === 1
      if (filter === 'active' && chain.status !== 'active') return false
      if (filter === 'issue' && !hasChainIssue(chain)) return false
      if (filter === 'relay' && isDirect) return false
      if (filter === 'direct' && !isDirect) return false
      if (!q) return true
      const hopsText = chain.hops.map((hop) => hop.server_alias).join(' ')
      return `${chain.name} ${hopsText} #${chain.id} L${chain.id % 9}`.toLowerCase().includes(q)
    })
  }, [chains, filter, query])

  return (
    <div className="cg-chain-overview">
      <div className="cg-chain-toolbar">
        <div className="cg-search-wrap">
          <SearchIcon />
          <input
            type="search"
            className="cg-search-input"
            placeholder="搜索链路名称、跳点机器、线路编号 L0–L8…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            aria-label="搜索链路"
          />
        </div>
        <div className="cg-filter-pills" role="group" aria-label="链路筛选">
          {(
            [
              { id: 'all', label: '全部', count: counts.all },
              { id: 'active', label: '正常', count: counts.active },
              { id: 'issue', label: '异常/降级', count: counts.issue },
              { id: 'relay', label: '中转', count: counts.relay },
              { id: 'direct', label: '直连', count: counts.direct },
            ] as const
          ).map((item) => (
            <button
              key={item.id}
              type="button"
              className="cg-filter-pill"
              data-active={filter === item.id ? 'true' : 'false'}
              onClick={() => setFilter(item.id)}
            >
              <span>{item.label}</span>
              <span className="cg-filter-pill-count">{item.count}</span>
            </button>
          ))}
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className="cg-chain-overview-empty">未找到匹配的链路，尝试调整搜索词或筛选条件。</p>
      ) : (
        <div className="cg-chain-cards">
          {filtered.map((chain) => {
            const stats = index.byChain.get(chain.id)
            return (
              <ChainOverviewCard
                key={chain.id}
                chain={chain}
                latency={stats?.latency_ms}
                today={chainTodayBytes(stats)}
                statsLoaded={statsLoaded}
                unreachable={unreachableChainIds.has(chain.id)}
                unreachableLabel={unreachableLabel}
                onSelect={onSelect}
              />
            )
          })}
        </div>
      )}

      <p className="cg-chain-overview-hint">
        <EyeIcon />
        点卡片前往详情查看拓扑与维护操作；延迟为跳间探测之和，负载为今日流量，经 Agent 段探测上报。
      </p>
    </div>
  )
}
