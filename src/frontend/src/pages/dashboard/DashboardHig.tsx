import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
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
  PauseIcon,
  PlayIcon,
  RefreshCwIcon,
  RouteIcon,
  ServerIcon,
  TimerIcon,
  UsersIcon,
  WifiIcon,
} from 'lucide-react'

import { Notice } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { DEMO_DASHBOARD_STATS, DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatByteRate, humanizeBytes } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import type { Chain, DashboardStats, Server } from '@/lib/types'
import { cn } from '@/lib/utils'

import '../dashboard.css'

const GlobeTopology = lazy(() => import('@/components/GlobeTopology'))

function percentage(value: number, total: number) {
  if (total <= 0) return 0
  return Math.min(100, Math.round((value / total) * 100))
}

function formatUptime(seconds: number | undefined) {
  if (seconds === undefined) return '--'
  const days = Math.floor(seconds / 86400)
  if (days > 0) return `${days} 天 ${Math.floor((seconds % 86400) / 3600)} 小时`
  return `${Math.floor(seconds / 3600)} 小时`
}

type NodeTone = 'online' | 'warning' | 'offline'

function nodeStatusTone(server: Server): NodeTone {
  if (!isServerOnline(server)) return 'offline'
  if (server.config_drift) return 'warning'
  return 'online'
}

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [activeIndex, setActiveIndex] = useState(0)
  const [motionEnabled, setMotionEnabled] = useState(true)
  const [lastSyncedAt, setLastSyncedAt] = useState<Date | null>(null)
  const [error, setError] = useState('')
  const loadRequest = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent ? { display: 'silent' as const } : undefined
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

  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    let timer: number | undefined
    const poll = async (initial: boolean) => {
      await load(!initial, controller.signal)
      if (!stopped) timer = window.setTimeout(() => void poll(false), 5000)
    }
    void poll(true)
    return () => {
      stopped = true
      loadRequest.current += 1
      controller.abort()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [load])

  if (error) {
    return (
      <div className="db">
        <Notice tone="danger" title="控制面板连接失败" className="max-w-xl">{error}</Notice>
      </div>
    )
  }

  if (!stats) {
    return (
      <div className="db db-loading" role="status" aria-label="仪表盘加载中">
        <span className="db-spinner" aria-hidden="true" />
        <span className="db-loading-label">正在载入</span>
      </div>
    )
  }

  const isDemoData = import.meta.env.DEV && servers.length === 0
  const displayStats = isDemoData ? DEMO_DASHBOARD_STATS : stats
  const displayServers = isDemoData ? DEMO_SERVERS : servers
  const activeServerIndex = Math.min(activeIndex, Math.max(displayServers.length - 1, 0))
  const selectedServer = displayServers[activeServerIndex] ?? null
  const selectedMetrics = selectedServer?.metrics ?? null
  const serverHealth = percentage(displayStats.servers_online, displayStats.servers)
  const linkHealth = percentage(displayStats.links_active, displayStats.links)
  const availability = Math.max(serverHealth, linkHealth)
  const activeChains = isDemoData
    ? displayStats.links_active
    : chains.filter((chain) => chain.status === 'active').length
  const degradedChains = isDemoData
    ? []
    : chains.filter((chain) => chain.status === 'degraded' || chain.status === 'failed')
  const today = new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date())
  const systemHealthy = displayStats.links_degraded === 0

  const metricItems = [
    { label: '服务器', value: displayStats.servers, detail: `${displayStats.servers_online} 台在线`, icon: ServerIcon, tint: 'blue' as const },
    { label: '链路', value: displayStats.links, detail: `${activeChains} 条活跃`, icon: RouteIcon, tint: 'indigo' as const },
    { label: '订阅用户', value: displayStats.users, detail: '访问状态正常', icon: UsersIcon, tint: 'purple' as const },
    { label: '待处理', value: displayStats.links_degraded, detail: displayStats.links_degraded ? '存在降级链路' : '暂无异常', icon: ActivityIcon, tint: 'orange' as const },
  ]

  return (
    <div className="db">
      {/* 大标题导航区 */}
      <header className="db-header">
        <div className="db-header-copy">
          <h1 className="db-title">仪表盘</h1>
          <p className="db-subtitle">
            {today}
            <span aria-hidden="true" className="db-dot-sep" />
            {lastSyncedAt
              ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} 已同步`
              : '正在同步'}
            {isDemoData ? ' · 演示数据' : ''}
          </p>
        </div>
        <div className="db-header-actions">
          <span className={cn('db-health-pill', systemHealthy ? 'is-healthy' : 'is-degraded')}>
            <span className="db-health-dot" aria-hidden="true" />
            {systemHealthy ? '所有系统正常' : `${displayStats.links_degraded} 条链路降级`}
          </span>
          <button
            type="button"
            className="db-icon-button"
            onClick={() => void load()}
            aria-label="立即刷新"
            title="立即刷新"
          >
            <RefreshCwIcon size={16} strokeWidth={2.2} />
          </button>
        </div>
      </header>

      {/* 核心指标 */}
      <section className="db-metrics" aria-label="核心指标">
        {metricItems.map((item) => (
          <article key={item.label} className="db-metric-card">
            <span className={cn('db-metric-icon', `is-${item.tint}`)}>
              <item.icon size={16} strokeWidth={2.4} />
            </span>
            <span className="db-metric-value">{item.value}</span>
            <span className="db-metric-label">{item.label}</span>
            <span className="db-metric-detail">{item.detail}</span>
          </article>
        ))}
      </section>

      {/* 节点列表 + 全球拓扑 */}
      <div className="db-split">
        <section className="db-card db-nodes" aria-labelledby="db-nodes-heading">
          <header className="db-card-head">
            <h2 className="db-card-title" id="db-nodes-heading">节点</h2>
            <span className="db-card-meta">{displayServers.length} 台</span>
          </header>

          <div className="db-availability">
            <strong>{availability}<small>%</small></strong>
            <span>整体可用性 · 服务器与链路健康度</span>
          </div>

          {displayServers.length ? (
            <ul className="db-node-list">
              {displayServers.map((server, index) => {
                const tone = nodeStatusTone(server)
                return (
                  <li key={server.id}>
                    <button
                      type="button"
                      className={cn('db-node-row', index === activeServerIndex && 'is-selected')}
                      onClick={() => setActiveIndex(index)}
                      aria-pressed={index === activeServerIndex}
                    >
                      <span className={cn('db-node-dot', `is-${tone}`)} aria-hidden="true" />
                      <span className="db-node-copy">
                        <strong>{server.alias}</strong>
                        <small>{server.country_code || '--'} · {server.location || '位置待补充'}</small>
                      </span>
                      <span className={cn('db-node-status', `is-${tone}`)}>
                        {serverConnectionLabel(server.connection_state)}
                      </span>
                      <ChevronRightIcon className="db-node-chevron" size={14} strokeWidth={2.4} aria-hidden="true" />
                    </button>
                  </li>
                )
              })}
            </ul>
          ) : (
            <div className="db-empty">
              <ServerIcon size={28} strokeWidth={1.8} />
              <strong>等待服务器接入</strong>
              <span>添加服务器后，这里会显示节点状态。</span>
              <Link href="/servers" className="db-button-primary">添加服务器</Link>
            </div>
          )}
        </section>

        <section className="db-topo" aria-labelledby="db-topo-heading">
          <header className="db-topo-head">
            <div className="db-topo-head-copy">
              <h2 className="db-topo-title" id="db-topo-heading">全球拓扑</h2>
              <span className="db-topo-meta">{displayServers.length} 个节点</span>
            </div>
            <button
              type="button"
              className="db-topo-button"
              onClick={() => setMotionEnabled((current) => !current)}
              aria-pressed={motionEnabled}
            >
              {motionEnabled ? <PauseIcon size={13} /> : <PlayIcon size={13} />}
              {motionEnabled ? '暂停' : '巡航'}
            </button>
          </header>

          {displayServers.length ? (
            <Suspense fallback={<div className="dashboard-globe-loading" role="status" aria-label="正在加载全球节点" />}>
              <GlobeTopology
                servers={displayServers}
                chains={chains}
                activeServerId={selectedServer?.id}
                demoMode={isDemoData}
                motionEnabled={motionEnabled}
                onServerSelect={(serverId) => {
                  const index = displayServers.findIndex((server) => server.id === serverId)
                  if (index >= 0) setActiveIndex(index)
                }}
              />
            </Suspense>
          ) : (
            <div className="db-empty is-dark">
              <WifiIcon size={28} strokeWidth={1.8} />
              <strong>暂无节点数据</strong>
              <span>添加服务器后，这里会显示全球节点拓扑。</span>
            </div>
          )}
        </section>
      </div>

      {/* 选中节点运行时 */}
      {selectedServer ? (
        <section className="db-card db-focus" aria-label="选中服务器运行时">
          <header className="db-focus-head">
            <div className="db-focus-title">
              <h2>{selectedServer.alias}</h2>
              <span className="db-focus-sub">
                <MapPinIcon size={12} strokeWidth={2.4} aria-hidden="true" />
                {selectedServer.country_code || '--'} · {selectedServer.location || '位置待补充'}
              </span>
            </div>
            <span className={cn('db-health-pill is-small', isServerOnline(selectedServer) ? 'is-healthy' : 'is-degraded')}>
              <span className="db-health-dot" aria-hidden="true" />
              {serverConnectionLabel(selectedServer.connection_state)}
            </span>
          </header>

          <dl className="db-focus-grid">
            <div>
              <dt><CpuIcon size={12} strokeWidth={2.4} aria-hidden="true" />CPU</dt>
              <dd>{selectedMetrics ? `${Math.round(selectedMetrics.cpu_percent ?? 0)}%` : '--'}</dd>
            </div>
            <div>
              <dt><ArrowUpIcon size={12} strokeWidth={2.4} aria-hidden="true" />上行</dt>
              <dd>{formatByteRate(selectedMetrics?.network_tx_bps ?? null)}</dd>
            </div>
            <div>
              <dt><ArrowDownIcon size={12} strokeWidth={2.4} aria-hidden="true" />下行</dt>
              <dd>{formatByteRate(selectedMetrics?.network_rx_bps ?? null)}</dd>
            </div>
            <div>
              <dt><HardDriveIcon size={12} strokeWidth={2.4} aria-hidden="true" />磁盘</dt>
              <dd>{selectedMetrics ? `${humanizeBytes(selectedMetrics.disk_used)} / ${humanizeBytes(selectedMetrics.disk_total)}` : '--'}</dd>
            </div>
            <div>
              <dt><TimerIcon size={12} strokeWidth={2.4} aria-hidden="true" />运行时间</dt>
              <dd>{formatUptime(selectedMetrics?.uptime_seconds)}</dd>
            </div>
            <div>
              <dt><GaugeIcon size={12} strokeWidth={2.4} aria-hidden="true" />Agent</dt>
              <dd>{selectedServer.agent_version || '--'}</dd>
            </div>
          </dl>

          <div className="db-focus-addr">
            <span className="db-focus-addr-label">入口地址</span>
            <code>{selectedServer.learned_addr || selectedServer.address || '--'}</code>
            <Link href="/servers" className="db-text-link">
              查看服务器 <ChevronRightIcon size={13} strokeWidth={2.4} />
            </Link>
          </div>
        </section>
      ) : null}

      {/* 链路健康 */}
      <section className={cn('db-banner', systemHealthy ? 'is-good' : 'is-bad')} aria-label="链路健康">
        <span className="db-banner-icon" aria-hidden="true">
          {systemHealthy ? <CheckCircle2Icon size={18} strokeWidth={2.2} /> : <AlertTriangleIcon size={18} strokeWidth={2.2} />}
        </span>
        <div className="db-banner-copy">
          <strong>{systemHealthy ? '所有系统稳定' : '降级链路需要关注'}</strong>
          <span>
            {systemHealthy
              ? `当前没有降级或失败的链路，${displayStats.links} 条链路中 ${displayStats.links_active} 条保持活跃。`
              : '部分链路处于降级或失败状态，建议尽快处理。'}
          </span>
        </div>
        {!systemHealthy && degradedChains.length ? (
          <div className="db-banner-list">
            {degradedChains.map((chain) => (
              <Link key={chain.id} href="/chains" className="db-banner-item">
                <strong>{chain.name}</strong>
                <span>{chain.error || chain.status}</span>
                <ChevronRightIcon size={14} strokeWidth={2.4} aria-hidden="true" />
              </Link>
            ))}
          </div>
        ) : null}
        {!systemHealthy && !degradedChains.length ? (
          <Link href="/chains" className="db-text-link">
            查看链路 <ChevronRightIcon size={13} strokeWidth={2.4} />
          </Link>
        ) : null}
      </section>
    </div>
  )
}
