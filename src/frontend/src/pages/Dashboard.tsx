import { useCallback, useRef, useState } from 'react'
import { Link } from 'wouter'
import {
  ActivityIcon,
  AlertTriangleIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CpuIcon,
  GaugeIcon,
  HardDriveIcon,
  MapPinIcon,
  RefreshCwIcon,
  RouteIcon,
  ServerIcon,
  TimerIcon,
  UsersIcon,
} from 'lucide-react'

import { Notice } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { DEMO_CHAINS, DEMO_DASHBOARD_STATS, DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatByteRate, humanizeBytes } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import type { Chain, ChainHop, ChainStatus, DashboardStats, NodeStatus, Server } from '@/lib/types'
import { usePolling } from '@/lib/use-polling'
import { cn } from '@/lib/utils'

import './dashboard.css'

const CHAIN_STATUS_LABEL: Record<ChainStatus, string> = {
  pending: '待部署',
  applying: '部署中',
  active: '运行中',
  degraded: '降级',
  failed: '失败',
  waiting_for_agent: '等待 Agent',
  active_unconfirmed: '待确认',
  active_failed: '生效失败',
  cleanup_pending: '清理中',
  invalid: '配置无效',
  deleted: '已删除',
}

const HOP_STATUS_LABEL: Record<NodeStatus, string> = {
  pending: '待生效',
  applying: '部署中',
  active: '已生效',
  failed: '失败',
}

const HOP_ROLE_LABEL = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
} as const

function pct(value: number, total: number) {
  if (!Number.isFinite(total) || total <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((value / total) * 100)))
}

function formatUptime(seconds: number | undefined) {
  if (seconds === undefined) return '--'
  const days = Math.floor(seconds / 86400)
  if (days > 0) return `${days} 天 ${Math.floor((seconds % 86400) / 3600)} 小时`
  return `${Math.floor(seconds / 3600)} 小时`
}

function chainTone(status: ChainStatus) {
  if (status === 'active') return 'ok'
  if (status === 'deleted') return 'muted'
  if (
    status === 'degraded' ||
    status === 'failed' ||
    status === 'active_failed' ||
    status === 'invalid'
  ) {
    return 'bad'
  }
  return 'warn'
}

function serverTone(server: Server) {
  if (!isServerOnline(server)) return 'bad'
  if (server.config_drift || server.agent_settings_status === 'failed') return 'warn'
  return 'ok'
}

function hopTone(status: NodeStatus) {
  if (status === 'active') return 'ok'
  if (status === 'failed') return 'bad'
  return 'warn'
}

interface DashEvent {
  key: string
  tone: 'bad' | 'warn' | 'ok'
  title: string
  detail: string
  href: string
}

function SummaryCell({
  label,
  value,
  detail,
  tone,
}: {
  label: string
  value: string
  detail?: string
  tone?: 'bad' | 'warn'
}) {
  return (
    <div className="dash-summary-cell">
      <span className="dash-summary-label">{label}</span>
      <span className={cn('dash-summary-value', tone && `is-${tone}`)}>{value}</span>
      {detail ? <span className="dash-summary-detail">{detail}</span> : null}
    </div>
  )
}

function ModuleHeader({
  tab,
  title,
  meta,
  href,
  hrefLabel,
}: {
  tab: string
  title: string
  meta?: string
  href?: string
  hrefLabel?: string
}) {
  return (
    <header className="dash-mod-head">
      <span className="dash-tab">{tab}</span>
      <h2 className="dash-mod-title">{title}</h2>
      {meta ? <span className="dash-mod-meta">{meta}</span> : null}
      {href ? (
        <Link href={href} className="dash-mod-link">
          {hrefLabel ?? '查看全部'}
          <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
        </Link>
      ) : null}
    </header>
  )
}

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [activeServerId, setActiveServerId] = useState<number | null>(null)
  const [selectedChainId, setSelectedChainId] = useState<number | null>(null)
  const [lastSyncedAt, setLastSyncedAt] = useState<Date | null>(null)
  const [error, setError] = useState('')
  const loadRequest = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
    try {
      const [dashboardStats, serverList, chainList] = await Promise.all([
        api.dashboard(options),
        api.servers(options),
        api.chains(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setStats(dashboardStats)
      setServers(serverList)
      setChains(chainList)
      setLastSyncedAt(new Date())
      setError('')
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(err))
    }
  }, [])

  usePolling(load, loadRequest)

  if (error) {
    return (
      <div className="dash dash-empty-page">
        <Notice tone="danger" title="控制面板连接失败">
          {error}
        </Notice>
      </div>
    )
  }

  if (!stats) {
    return (
      <div className="dash dash-loading" role="status" aria-label="仪表盘加载中">
        <div className="dash-loading-grid" aria-hidden="true">
          {Array.from({ length: 8 }).map((_, index) => (
            <span key={index} className="dash-loading-block" />
          ))}
        </div>
        <span className="dash-loading-label">正在载入机群读数</span>
      </div>
    )
  }

  const isDemoData = import.meta.env.DEV && servers.length === 0
  const displayStats = isDemoData ? DEMO_DASHBOARD_STATS : stats
  const displayServers = isDemoData ? DEMO_SERVERS : servers
  const displayChains = (isDemoData ? DEMO_CHAINS : chains).filter(
    (chain) => chain.status !== 'deleted',
  )

  const selectedServer =
    displayServers.find((server) => server.id === activeServerId) ?? displayServers[0] ?? null
  const selectedMetrics = selectedServer?.metrics ?? null
  const selectedChain =
    displayChains.find((chain) => chain.id === selectedChainId) ??
    displayChains.find((chain) => chain.status === 'failed' || chain.status === 'degraded') ??
    displayChains[0] ??
    null

  const onlineServers = displayServers.filter(isServerOnline)
  const offlineCount = displayServers.length - onlineServers.length
  const driftCount = displayServers.filter((server) => server.config_drift).length
  const totalTxBps = onlineServers.reduce(
    (sum, server) => sum + (server.metrics?.network_tx_bps ?? 0),
    0,
  )
  const totalRxBps = onlineServers.reduce(
    (sum, server) => sum + (server.metrics?.network_rx_bps ?? 0),
    0,
  )
  const throughput = totalTxBps + totalRxBps
  const peakRate = Math.max(totalTxBps, totalRxBps, 1)
  const topServer = [...displayServers].sort(
    (left, right) =>
      (right.metrics?.network_tx_bps ?? 0) +
      (right.metrics?.network_rx_bps ?? 0) -
      ((left.metrics?.network_tx_bps ?? 0) + (left.metrics?.network_rx_bps ?? 0)),
  )[0]
  const activeChains = displayChains.filter((chain) => chain.status === 'active').length
  const degenerateChains = displayChains.filter(
    (chain) => chain.status === 'degraded' || chain.status === 'failed',
  )
  const exceptionCount = degenerateChains.length + offlineCount + driftCount

  const events: DashEvent[] = []
  for (const server of displayServers) {
    if (!isServerOnline(server)) {
      events.push({
        key: `server-offline-${server.id}`,
        tone: 'bad',
        title: `${server.alias} ${serverConnectionLabel(server.connection_state)}`,
        detail: `${server.country_code || '--'} · ${server.location || '位置待补充'}`,
        href: '/servers',
      })
    } else if (server.config_drift) {
      events.push({
        key: `server-drift-${server.id}`,
        tone: 'warn',
        title: `${server.alias} 配置漂移`,
        detail: 'Agent 上报值与期望配置不一致',
        href: '/servers',
      })
    }
  }
  for (const chain of degenerateChains) {
    events.push({
      key: `chain-${chain.id}`,
      tone: 'bad',
      title: `${chain.name} ${CHAIN_STATUS_LABEL[chain.status]}`,
      detail: chain.error || `${chain.hops.length} 跳 · 需要重新部署或排查`,
      href: '/chains',
    })
  }
  const visibleEvents = events.slice(0, 6)
  const toneRank = { bad: 0, warn: 1, ok: 2 } as const
  const agentPreview = [...displayServers]
    .sort((left, right) => toneRank[serverTone(left)] - toneRank[serverTone(right)])
    .slice(0, 5)

  const selectedCpu = pct(selectedMetrics?.cpu_percent ?? 0, 100)
  const selectedMem =
    selectedMetrics && selectedMetrics.mem_total > 0
      ? pct(selectedMetrics.mem_used, selectedMetrics.mem_total)
      : 0
  const selectedDisk =
    selectedMetrics && selectedMetrics.disk_total > 0
      ? pct(selectedMetrics.disk_used, selectedMetrics.disk_total)
      : 0
  const today = new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date())

  return (
    <div className="dash">
      <header className="dash-head">
        <div className="dash-head-copy">
          <h1 className="dash-title">仪表盘</h1>
          <p className="dash-sub">
            {today}
            <span className="dash-sep">/</span>
            {lastSyncedAt
              ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour12: false })} 已同步`
              : '正在同步'}
            {isDemoData ? (
              <>
                <span className="dash-sep">/</span>演示数据
              </>
            ) : null}
          </p>
        </div>
        <div className="dash-actions">
          <span className={cn('dash-health', exceptionCount > 0 ? 'is-bad' : 'is-ok')}>
            {exceptionCount > 0 ? (
              <>
                <AlertTriangleIcon size={12} strokeWidth={2} aria-hidden="true" />
                {exceptionCount} 项需要处理
              </>
            ) : (
              <>
                <CheckCircle2Icon size={12} strokeWidth={2} aria-hidden="true" />
                所有系统正常
              </>
            )}
          </span>
          <button
            type="button"
            className="cg-button"
            onClick={() => void load()}
            aria-label="立即刷新读数"
          >
            <RefreshCwIcon size={13} strokeWidth={2} aria-hidden="true" />
            刷新
          </button>
          <Link href="/chains" className="cg-button">
            链路管理
          </Link>
        </div>
      </header>

      <div className="dash-mosaic">
        <section className="dash-mod dash-span-4" aria-labelledby="dash-fleet-title">
          <ModuleHeader
            tab="FLEET"
            title="机群总计"
            meta={`${displayServers.length} 台服务器 / ${displayChains.length} 条链路`}
          />
          <div className="dash-mod-body">
            <div className="dash-summary">
              <SummaryCell
                label="服务器"
                value={String(displayServers.length)}
                detail={`${onlineServers.length} 台在线`}
              />
              <SummaryCell
                label="链路"
                value={String(displayChains.length)}
                detail={`${activeChains} 条活跃`}
              />
              <SummaryCell label="订阅用户" value={displayStats.users.toLocaleString('zh-CN')} />
              <SummaryCell
                label="异常"
                value={String(exceptionCount)}
                tone={exceptionCount > 0 ? 'bad' : undefined}
                detail={
                  exceptionCount > 0
                    ? `${degenerateChains.length} 链路 / ${offlineCount} 离线 / ${driftCount} 漂移`
                    : '暂无异常'
                }
              />
              <SummaryCell
                label="实时吞吐"
                value={formatByteRate(throughput)}
                detail={`↑${humanizeBytes(totalTxBps)} · ↓${humanizeBytes(totalRxBps)}`}
              />
              <SummaryCell
                label="Agent 在线"
                value={`${onlineServers.length}`}
                detail={`共 ${displayServers.length} 台已登记`}
              />
            </div>
          </div>
        </section>

        <section className="dash-mod dash-span-4" aria-labelledby="dash-agents-title">
          <ModuleHeader
            tab="AGENTS"
            title="Agent 状态"
            meta={`${onlineServers.length}/${displayServers.length} 在线`}
            href="/servers"
            hrefLabel="服务器"
          />
          <div className="dash-mod-body dash-list-body">
            {displayServers.length ? (
              <ul className="dash-agent-list">
                {agentPreview.map((server) => {
                  const tone = serverTone(server)
                  const metrics = server.metrics
                  return (
                    <li key={server.id}>
                      <Link href="/servers" className="dash-agent-row">
                        <span className={cn('dash-dot', `is-${tone}`)} aria-hidden="true" />
                        <span className="dash-agent-copy">
                          <strong>{server.alias}</strong>
                          <span>
                            {server.country_code || '--'} · {server.location || '位置待补充'}
                          </span>
                        </span>
                        <span className={cn('dash-agent-state', `is-${tone}`)}>
                          {serverConnectionLabel(server.connection_state)}
                        </span>
                        <span className="dash-agent-latency">
                          {metrics?.latency_ms === null || metrics?.latency_ms === undefined
                            ? '--'
                            : `${metrics.latency_ms} ms`}
                        </span>
                      </Link>
                    </li>
                  )
                })}
              </ul>
            ) : (
              <div className="dash-empty">
                <ServerIcon size={18} strokeWidth={1.6} />
                <strong>等待服务器接入</strong>
                <span>添加服务器后，这里会显示 Agent 状态。</span>
                <Link href="/servers" className="cg-button is-primary">
                  添加服务器
                </Link>
              </div>
            )}
          </div>
        </section>

        <section className="dash-mod dash-span-4" aria-labelledby="dash-chains-title">
          <ModuleHeader
            tab="CHAINS"
            title="链路总览"
            meta={selectedChain ? selectedChain.name : '未选择链路'}
            href="/chains"
            hrefLabel="链路"
          />
          <div className="dash-mod-body dash-chain-body">
            {displayChains.length ? (
              <>
                <ul className="dash-chain-list">
                  {displayChains.slice(0, 3).map((chain) => {
                    const isSelected = selectedChain?.id === chain.id
                    return (
                      <li key={chain.id}>
                        <button
                          type="button"
                          className={cn('dash-chain-row', isSelected && 'is-selected')}
                          aria-pressed={isSelected}
                          onClick={() => setSelectedChainId(chain.id)}
                        >
                          <span className="dash-chain-name">{chain.name}</span>
                          <span className="dash-chain-hops">{chain.hops.length} 跳</span>
                          <span className={cn('dash-tag', `is-${chainTone(chain.status)}`)}>
                            {CHAIN_STATUS_LABEL[chain.status]}
                          </span>
                        </button>
                      </li>
                    )
                  })}
                </ul>
                {selectedChain ? (
                  <div className="dash-hop" key={selectedChain.id}>
                    <ol className="dash-hop-strip">
                      {selectedChain.hops.map((hop: ChainHop) => (
                        <li
                          key={hop.id}
                          className={cn('dash-hop-cell', `is-${hopTone(hop.status)}`)}
                        >
                          <span className="dash-hop-seq">{String(hop.seq).padStart(2, '0')}</span>
                          <span className="dash-hop-alias">{hop.server_alias}</span>
                          <span className="dash-hop-role">{HOP_ROLE_LABEL[hop.role]}</span>
                          <span className="dash-hop-state">{HOP_STATUS_LABEL[hop.status]}</span>
                        </li>
                      ))}
                    </ol>
                    <p className="dash-hop-note">
                      {selectedChain.error
                        ? selectedChain.error
                        : `${selectedChain.hops.length} 跳有序拓扑 · 入口端口 ${selectedChain.entry_port}`}
                    </p>
                  </div>
                ) : null}
              </>
            ) : (
              <div className="dash-empty">
                <RouteIcon size={18} strokeWidth={1.6} />
                <strong>尚无链路</strong>
                <span>创建链路后，这里显示逐跳状态。</span>
                <Link href="/chains" className="cg-button is-primary">
                  创建链路
                </Link>
              </div>
            )}
          </div>
        </section>

        <section className="dash-mod dash-span-8" aria-labelledby="dash-readings-title">
          <ModuleHeader
            tab="SERVERS"
            title="服务器读数"
            meta={`${displayServers.length} 台 · 点击行选择运行时`}
            href="/servers"
            hrefLabel="全部服务器"
          />
          <div className="dash-mod-body dash-table-body">
            {displayServers.length ? (
              <div className="dash-table-wrap">
                <table className="dash-table">
                  <thead>
                    <tr>
                      <th scope="col">服务器</th>
                      <th scope="col">地区</th>
                      <th scope="col">连接</th>
                      <th scope="col">CPU</th>
                      <th scope="col">内存</th>
                      <th scope="col">磁盘</th>
                      <th scope="col" className="dash-col-extra">
                        延迟
                      </th>
                      <th scope="col" className="dash-col-extra">
                        上行
                      </th>
                      <th scope="col" className="dash-col-extra">
                        下行
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {displayServers.map((server) => {
                      const metrics = server.metrics
                      const isSelected = selectedServer?.id === server.id
                      return (
                        <tr
                          key={server.id}
                          className={cn(isSelected && 'is-selected')}
                          onClick={() => setActiveServerId(server.id)}
                        >
                          <td>
                            <button
                              type="button"
                              className="dash-server-select"
                              aria-pressed={isSelected}
                              onClick={(event) => {
                                event.stopPropagation()
                                setActiveServerId(server.id)
                              }}
                            >
                              <span
                                className={cn('dash-dot', `is-${serverTone(server)}`)}
                                aria-hidden="true"
                              />
                              {server.alias}
                            </button>
                          </td>
                          <td className="dash-cell-muted">
                            <span className="dash-country">{server.country_code || '--'}</span>
                            {server.location ? ` ${server.location}` : ''}
                          </td>
                          <td>
                            <span className={cn('dash-tag', `is-${serverTone(server)}`)}>
                              {serverConnectionLabel(server.connection_state)}
                            </span>
                          </td>
                          <td className="dash-cell-num">
                            {metrics?.cpu_percent === null || metrics?.cpu_percent === undefined
                              ? '--'
                              : `${Math.round(metrics.cpu_percent)}%`}
                          </td>
                          <td className="dash-cell-num">
                            {metrics && metrics.mem_total > 0
                              ? `${pct(metrics.mem_used, metrics.mem_total)}%`
                              : '--'}
                          </td>
                          <td className="dash-cell-num">
                            {metrics && metrics.disk_total > 0
                              ? `${pct(metrics.disk_used, metrics.disk_total)}%`
                              : '--'}
                          </td>
                          <td className="dash-cell-num dash-col-extra">
                            {metrics?.latency_ms === null || metrics?.latency_ms === undefined
                              ? '--'
                              : `${metrics.latency_ms} ms`}
                          </td>
                          <td className="dash-cell-num dash-col-extra">
                            {formatByteRate(metrics?.network_tx_bps ?? null)}
                          </td>
                          <td className="dash-cell-num dash-col-extra">
                            {formatByteRate(metrics?.network_rx_bps ?? null)}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="dash-empty">
                <ServerIcon size={18} strokeWidth={1.6} />
                <strong>没有服务器读数</strong>
                <span>接入服务器后可在此查看逐台指标。</span>
              </div>
            )}
          </div>
        </section>

        <section className="dash-mod dash-span-4" aria-labelledby="dash-events-title">
          <ModuleHeader tab="EVENTS" title="事件" meta={`${events.length} 项`} />
          <div className="dash-mod-body dash-list-body">
            {visibleEvents.length ? (
              <ul className="dash-event-list">
                {visibleEvents.map((event) => (
                  <li key={event.key}>
                    <Link href={event.href} className="dash-event-row">
                      <span
                        className={cn('dash-event-mark', `is-${event.tone}`)}
                        aria-hidden="true"
                      />
                      <span className="dash-event-copy">
                        <strong>{event.title}</strong>
                        <span>{event.detail}</span>
                      </span>
                      <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
                    </Link>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="dash-empty">
                <CheckCircle2Icon size={18} strokeWidth={1.6} />
                <strong>当前没有异常</strong>
                <span>服务器、链路与配置均处于正常状态。</span>
              </div>
            )}
          </div>
        </section>

        <section className="dash-mod dash-span-3" aria-labelledby="dash-rate-title">
          <ModuleHeader tab="RATE" title="实时吞吐" meta="瞬时速率" />
          <div className="dash-mod-body">
            <div className="dash-rate">
              <div className="dash-rate-row">
                <span className="dash-rate-label">
                  <ArrowUpIcon size={12} strokeWidth={2} aria-hidden="true" /> 上行
                </span>
                <strong className="dash-rate-value">{formatByteRate(totalTxBps)}</strong>
                <span className="dash-rate-bar">
                  <span style={{ width: `${(totalTxBps / peakRate) * 100}%` }} />
                </span>
              </div>
              <div className="dash-rate-row">
                <span className="dash-rate-label">
                  <ArrowDownIcon size={12} strokeWidth={2} aria-hidden="true" /> 下行
                </span>
                <strong className="dash-rate-value">{formatByteRate(totalRxBps)}</strong>
                <span className="dash-rate-bar">
                  <span style={{ width: `${(totalRxBps / peakRate) * 100}%` }} />
                </span>
              </div>
              <p className="dash-rate-note">
                合计 <span className="dash-mono">{formatByteRate(throughput)}</span> ·{' '}
                {onlineServers.length} 台在线服务器
              </p>
              {topServer ? (
                <p className="dash-rate-note">
                  峰值服务器 <span className="dash-rate-top">{topServer.alias}</span>
                  {' · '}
                  <span className="dash-mono">
                    {formatByteRate(
                      (topServer.metrics?.network_tx_bps ?? 0) +
                        (topServer.metrics?.network_rx_bps ?? 0),
                    )}
                  </span>
                </p>
              ) : null}
            </div>
          </div>
        </section>

        <section className="dash-mod dash-span-6" aria-labelledby="dash-runtime-title">
          <ModuleHeader
            tab="RUNTIME"
            title={selectedServer ? `${selectedServer.alias} 运行时` : '服务器运行时'}
            meta={
              selectedServer
                ? `${selectedServer.country_code || '--'} · ${selectedServer.location || '位置待补充'}`
                : undefined
            }
            href="/servers"
            hrefLabel="服务器"
          />
          <div className="dash-mod-body">
            {selectedServer ? (
              <div className="dash-runtime">
                <dl className="dash-runtime-grid">
                  <div>
                    <dt>
                      <CpuIcon size={11} strokeWidth={2} aria-hidden="true" /> CPU
                    </dt>
                    <dd>
                      {selectedMetrics ? `${Math.round(selectedMetrics.cpu_percent ?? 0)}%` : '--'}
                    </dd>
                    <span className="dash-meter">
                      <span style={{ width: `${selectedCpu}%` }} />
                    </span>
                  </div>
                  <div>
                    <dt>
                      <HardDriveIcon size={11} strokeWidth={2} aria-hidden="true" /> 内存
                    </dt>
                    <dd>{selectedMetrics ? `${selectedMem}%` : '--'}</dd>
                    <span className="dash-meter">
                      <span style={{ width: `${selectedMem}%` }} />
                    </span>
                  </div>
                  <div>
                    <dt>
                      <HardDriveIcon size={11} strokeWidth={2} aria-hidden="true" /> 磁盘
                    </dt>
                    <dd>
                      {selectedMetrics
                        ? `${humanizeBytes(selectedMetrics.disk_used)} / ${humanizeBytes(selectedMetrics.disk_total)}`
                        : '--'}
                    </dd>
                    <span className="dash-meter">
                      <span style={{ width: `${selectedDisk}%` }} />
                    </span>
                  </div>
                  <div>
                    <dt>
                      <ArrowUpIcon size={11} strokeWidth={2} aria-hidden="true" /> 上行
                    </dt>
                    <dd>{formatByteRate(selectedMetrics?.network_tx_bps ?? null)}</dd>
                  </div>
                  <div>
                    <dt>
                      <ArrowDownIcon size={11} strokeWidth={2} aria-hidden="true" /> 下行
                    </dt>
                    <dd>{formatByteRate(selectedMetrics?.network_rx_bps ?? null)}</dd>
                  </div>
                  <div>
                    <dt>
                      <TimerIcon size={11} strokeWidth={2} aria-hidden="true" /> 运行时间
                    </dt>
                    <dd>{formatUptime(selectedMetrics?.uptime_seconds)}</dd>
                  </div>
                  <div>
                    <dt>
                      <GaugeIcon size={11} strokeWidth={2} aria-hidden="true" /> Agent
                    </dt>
                    <dd>{selectedServer.agent_version || '--'}</dd>
                  </div>
                  <div>
                    <dt>
                      <MapPinIcon size={11} strokeWidth={2} aria-hidden="true" /> 入口地址
                    </dt>
                    <dd className="dash-mono dash-runtime-addr">
                      {selectedServer.learned_addr || selectedServer.address || '--'}
                    </dd>
                  </div>
                  <div>
                    <dt>
                      <ServerIcon size={11} strokeWidth={2} aria-hidden="true" /> 机器类型
                    </dt>
                    <dd>{selectedServer.machine_type === 'nat' ? 'NAT' : '独立 IP'}</dd>
                  </div>
                  <div>
                    <dt>
                      <TimerIcon size={11} strokeWidth={2} aria-hidden="true" /> 最近上报
                    </dt>
                    <dd>
                      {selectedMetrics?.updated_at
                        ? new Date(selectedMetrics.updated_at).toLocaleTimeString('zh-CN', {
                            hour12: false,
                          })
                        : '--'}
                    </dd>
                  </div>
                  <div>
                    <dt>
                      <GaugeIcon size={11} strokeWidth={2} aria-hidden="true" /> Xray
                    </dt>
                    <dd>{selectedServer.xray_version || '--'}</dd>
                  </div>
                  <div>
                    <dt>
                      <CpuIcon size={11} strokeWidth={2} aria-hidden="true" /> 标签
                    </dt>
                    <dd className="dash-runtime-tags">
                      {selectedServer.tags.length ? selectedServer.tags.join(' / ') : '--'}
                    </dd>
                  </div>
                </dl>
              </div>
            ) : (
              <div className="dash-empty">
                <ServerIcon size={18} strokeWidth={1.6} />
                <strong>选择一台服务器</strong>
                <span>在服务器读数表中选择一行，查看其运行时。</span>
              </div>
            )}
          </div>
        </section>

        <section className="dash-mod dash-span-3" aria-labelledby="dash-users-title">
          <ModuleHeader tab="USERS" title="订阅用户" href="/users" hrefLabel="用户" />
          <div className="dash-mod-body">
            <div className="dash-users">
              <strong className="dash-users-value">
                {displayStats.users.toLocaleString('zh-CN')}
              </strong>
              <span className="dash-users-label">已登记订阅用户</span>
              <span className="dash-users-note">
                <UsersIcon size={12} strokeWidth={2} aria-hidden="true" /> 访问状态正常
              </span>
              <Link href="/users" className="dash-users-link">
                管理用户与订阅
                <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
              </Link>
              <nav className="dash-users-links" aria-label="用户与订阅快捷入口">
                <Link href="/groups">
                  用户分组
                  <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
                </Link>
                <Link href="/subscription-templates">
                  订阅模板
                  <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
                </Link>
                <Link href="/external-subscriptions">
                  外部订阅
                  <ChevronRightIcon size={12} strokeWidth={2} aria-hidden="true" />
                </Link>
              </nav>
            </div>
          </div>
        </section>
      </div>

      <footer className="dash-foot" aria-label="仪表盘状态">
        <span>
          <ActivityIcon size={11} strokeWidth={2} aria-hidden="true" /> 自动刷新 5 秒
        </span>
        <span>
          <TimerIcon size={11} strokeWidth={2} aria-hidden="true" /> 最后同步{' '}
          {lastSyncedAt ? lastSyncedAt.toLocaleTimeString('zh-CN', { hour12: false }) : '--'}
        </span>
        <span>
          <ServerIcon size={11} strokeWidth={2} aria-hidden="true" /> {onlineServers.length} 台
          Agent 在线
        </span>
        <span className="dash-foot-end">
          {degenerateChains.length > 0
            ? `${degenerateChains.length} 条链路降级或失败`
            : '链路全部正常'}
        </span>
      </footer>
    </div>
  )
}
