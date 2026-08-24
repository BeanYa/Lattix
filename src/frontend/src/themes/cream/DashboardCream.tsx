import { lazy, Suspense, useCallback, useRef, useState } from 'react'
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
  LayoutDashboardIcon,
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
import { usePolling } from '@/lib/use-polling'
import { cn } from '@/lib/utils'

import './dashboard.css'

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

function nodeStatusTone(server: Server) {
  if (!isServerOnline(server)) return 'is-offline'
  if (server.config_drift) return 'is-warning'
  return 'is-online'
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
      <Notice tone="danger" title="控制面板连接失败" className="max-w-xl">
        {error}
      </Notice>
    )
  }

  if (!stats) {
    return (
      <div className="cg-dashboard-loading" role="status" aria-label="仪表盘加载中">
        <span className="cg-micro">LOADING / PANEL</span>
        <div className="cg-dashboard-loading-bar">
          <i />
          <i />
          <i />
        </div>
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

  const metricItems = [
    {
      label: '服务器',
      mono: 'SERVERS',
      value: displayStats.servers,
      detail: `${displayStats.servers_online} 台在线`,
      icon: ServerIcon,
    },
    {
      label: '链路',
      mono: 'LINKS',
      value: displayStats.links,
      detail: `${activeChains} 条活跃`,
      icon: RouteIcon,
    },
    {
      label: '订阅用户',
      mono: 'USERS',
      value: displayStats.users,
      detail: '访问状态正常',
      icon: UsersIcon,
    },
    {
      label: '待处理',
      mono: 'PENDING',
      value: displayStats.links_degraded,
      detail: displayStats.links_degraded ? '存在降级链路' : '暂无异常',
      icon: ActivityIcon,
    },
  ]

  return (
    <div className="cg-dashboard">
      {/* Eyebrow + 环境信息 */}
      <div className="cg-dash-topline">
        <span className="cg-eyebrow">PANEL / OVERVIEW</span>
        <div className="cg-dash-topline-side">
          <span className="cg-pill">{today}</span>
          <span className={cn('cg-pill', lastSyncedAt && 'is-active')}>
            {lastSyncedAt
              ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} 已同步`
              : '正在同步'}
          </span>
          <button
            type="button"
            className="cg-button is-icon"
            onClick={() => void load()}
            aria-label="立即刷新"
            title="立即刷新"
          >
            <RefreshCwIcon />
          </button>
        </div>
      </div>

      {/* Header Card */}
      <header className="cg-card-raised cg-dash-header">
        <div className="cg-dash-header-main">
          <span className="cg-dash-header-icon">
            <LayoutDashboardIcon />
          </span>
          <div className="min-w-0">
            <span className="cg-micro" style={{ color: 'var(--cg-muted)' }}>
              CONTROL PANEL / 实时运行图景
            </span>
            <h1 className="cg-title cg-dash-title">仪表盘</h1>
            <p className="cg-dash-subtitle">
              服务器、链路与订阅访问的实时运行图景，集中查看可用性与资源负载。
            </p>
          </div>
        </div>
        <div className="cg-dash-header-side">
          {isDemoData ? <span className="cg-status is-muted">DEMO DATA</span> : null}
          <span className={cn('cg-status', displayStats.links_degraded ? 'is-red' : 'is-lime')}>
            {displayStats.links_degraded
              ? `${displayStats.links_degraded} DEGRADED`
              : 'ALL SYSTEMS STABLE'}
          </span>
        </div>
      </header>

      {/* Metric Tiles */}
      <section className="cg-dash-metrics" aria-label="核心指标">
        {metricItems.map((item) => (
          <article key={item.label} className="cg-metric">
            <span className="cg-metric-value">{String(item.value).padStart(2, '0')}</span>
            <span className="cg-metric-copy">
              <span className="cg-metric-label">
                <item.icon size={14} strokeWidth={2.2} />
                {item.label}
              </span>
              <span className="cg-metric-detail">
                {item.mono} / {item.detail}
              </span>
            </span>
          </article>
        ))}
      </section>

      {/* Main Split：节点摘要 + 深色拓扑面板 */}
      <div className="cg-dash-split">
        <section className="cg-card cg-dash-nodes" aria-labelledby="cg-dash-node-heading">
          <header className="cg-dash-section-head">
            <div>
              <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>
                NODE / SUMMARY
              </span>
              <h2 className="cg-title cg-dash-section-title" id="cg-dash-node-heading">
                节点摘要
              </h2>
            </div>
            <span className="cg-status is-blue">
              {String(displayServers.length).padStart(2, '0')} NODES
            </span>
          </header>

          <div className="cg-dash-availability">
            <span className="cg-micro" style={{ color: 'var(--cg-muted)' }}>
              AVAILABILITY
            </span>
            <strong className="cg-title">{availability}%</strong>
            <span className="cg-dash-availability-note">整体可用性 · 服务器与链路健康度</span>
          </div>

          {displayServers.length ? (
            <ul className="cg-dash-node-list">
              {displayServers.map((server, index) => {
                const tone = nodeStatusTone(server)
                return (
                  <li key={server.id}>
                    <button
                      type="button"
                      className={cn(
                        'cg-dash-node-item',
                        index === activeServerIndex && 'is-selected',
                      )}
                      onClick={() => setActiveIndex(index)}
                      aria-pressed={index === activeServerIndex}
                    >
                      <span className={cn('cg-dash-node-dot', tone)} />
                      <span className="cg-dash-node-copy">
                        <strong>{server.alias}</strong>
                        <small>
                          {server.country_code || '--'} · {server.location || '位置待补充'}
                        </small>
                      </span>
                      <span
                        className={cn(
                          'cg-status',
                          tone === 'is-online'
                            ? 'is-lime'
                            : tone === 'is-warning'
                              ? 'is-muted'
                              : 'is-red',
                        )}
                      >
                        {serverConnectionLabel(server.connection_state)}
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          ) : (
            <div className="cg-dash-empty">
              <ServerIcon />
              <strong>等待服务器接入</strong>
              <span>添加服务器后，这里会显示节点状态。</span>
              <Link href="/servers" className="cg-button is-primary">
                添加服务器
              </Link>
            </div>
          )}
        </section>

        <section
          className="cg-terminal cg-dash-topology"
          aria-labelledby="cg-dash-topology-heading"
        >
          <header className="cg-dash-terminal-head">
            <div>
              <span className="cg-micro" style={{ color: 'var(--cg-lime-dark)' }}>
                NODE / TOPOLOGY
              </span>
              <h2 className="cg-dash-terminal-title" id="cg-dash-topology-heading">
                全球节点拓扑
              </h2>
            </div>
            <div className="cg-dash-terminal-controls">
              <span className="cg-dash-terminal-count">
                {isDemoData ? 'DEMO · ' : ''}
                {displayServers.length} 个节点
              </span>
              <button
                type="button"
                className="cg-dash-terminal-button"
                onClick={() => setMotionEnabled((current) => !current)}
                aria-pressed={motionEnabled}
              >
                {motionEnabled ? <PauseIcon size={13} /> : <PlayIcon size={13} />}
                {motionEnabled ? '暂停' : '巡航'}
              </button>
            </div>
          </header>

          {displayServers.length ? (
            <Suspense
              fallback={
                <div
                  className="dashboard-globe-loading"
                  role="status"
                  aria-label="正在加载全球节点"
                />
              }
            >
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
            <div className="cg-dash-empty">
              <WifiIcon />
              <strong>暂无节点数据</strong>
              <span>添加服务器后，这里会显示全球节点拓扑。</span>
            </div>
          )}
        </section>
      </div>

      {/* Focus Node：选中服务器运行时（浅色数据卡） */}
      {selectedServer ? (
        <section className="cg-card cg-dash-focus" aria-label="选中服务器运行时">
          <header className="cg-dash-terminal-head">
            <div>
              <span className="cg-micro" style={{ color: 'var(--cg-lime-dark)' }}>
                NODE / RUNTIME
              </span>
              <h2 className="cg-dash-terminal-title">
                <MapPinIcon size={15} style={{ color: 'var(--cg-lime-dark)' }} />
                {selectedServer.alias}
                <small>
                  {selectedServer.country_code || '--'} · {selectedServer.location || '位置待补充'}
                </small>
              </h2>
            </div>
            <span
              className={cn('cg-status', isServerOnline(selectedServer) ? 'is-lime' : 'is-red')}
            >
              {serverConnectionLabel(selectedServer.connection_state)}
            </span>
          </header>

          <dl className="cg-dash-focus-grid">
            <div>
              <dt>
                <CpuIcon size={12} />
                CPU
              </dt>
              <dd>{selectedMetrics ? `${Math.round(selectedMetrics.cpu_percent ?? 0)}%` : '--'}</dd>
            </div>
            <div>
              <dt>
                <ArrowUpIcon size={12} />
                上行 TX
              </dt>
              <dd>{formatByteRate(selectedMetrics?.network_tx_bps ?? null)}</dd>
            </div>
            <div>
              <dt>
                <ArrowDownIcon size={12} />
                下行 RX
              </dt>
              <dd>{formatByteRate(selectedMetrics?.network_rx_bps ?? null)}</dd>
            </div>
            <div>
              <dt>
                <HardDriveIcon size={12} />
                磁盘
              </dt>
              <dd>
                {selectedMetrics
                  ? `${humanizeBytes(selectedMetrics.disk_used)} / ${humanizeBytes(selectedMetrics.disk_total)}`
                  : '--'}
              </dd>
            </div>
            <div>
              <dt>
                <TimerIcon size={12} />
                运行时间
              </dt>
              <dd>{formatUptime(selectedMetrics?.uptime_seconds)}</dd>
            </div>
            <div>
              <dt>
                <GaugeIcon size={12} />
                AGENT
              </dt>
              <dd>{selectedServer.agent_version || '--'}</dd>
            </div>
          </dl>

          <div className="cg-dash-focus-addr">
            <span className="cg-micro">ENTRY ADDR</span>
            <code>{selectedServer.learned_addr || selectedServer.address || '--'}</code>
            <Link href="/servers" className="cg-dash-focus-link">
              查看服务器 <ChevronRightIcon size={13} />
            </Link>
          </div>
        </section>
      ) : null}

      {/* Bottom：链路健康 */}
      {displayStats.links_degraded > 0 ? (
        <section className="cg-semantic-card is-bad" aria-label="降级链路">
          <header>
            <span className="flex items-center gap-2">
              <AlertTriangleIcon size={15} />
              降级链路需要关注
            </span>
            <span className="cg-micro">LINKS / DEGRADED</span>
          </header>
          <div className="cg-semantic-body cg-dash-degraded-list">
            {degradedChains.length ? (
              degradedChains.map((chain) => (
                <Link key={chain.id} href="/chains" className="cg-dash-degraded-item">
                  <strong>{chain.name}</strong>
                  <span>{chain.error || chain.status}</span>
                  <ChevronRightIcon size={14} />
                </Link>
              ))
            ) : (
              <Link href="/chains" className="cg-dash-degraded-item">
                <strong>{displayStats.links_degraded} 条链路降级</strong>
                <span>前往链路页面查看详情</span>
                <ChevronRightIcon size={14} />
              </Link>
            )}
          </div>
        </section>
      ) : (
        <section className="cg-semantic-card is-good" aria-label="链路健康">
          <header>
            <span className="flex items-center gap-2">
              <CheckCircle2Icon size={15} />
              所有系统稳定
            </span>
            <span className="cg-micro">LINKS / HEALTHY</span>
          </header>
          <div className="cg-semantic-body" style={{ color: 'var(--cg-muted)', fontSize: 13 }}>
            当前没有降级或失败的链路，{displayStats.links} 条链路中 {displayStats.links_active}{' '}
            条保持活跃。
          </div>
        </section>
      )}
    </div>
  )
}
