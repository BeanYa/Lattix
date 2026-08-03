import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'wouter'
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  BotIcon,
  ChevronLeftIcon,
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
  UsersIcon,
  WifiIcon,
} from 'lucide-react'

import FluidGlassCanvas from '@/components/FluidGlassCanvas'
import { Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import { api, errorMessage } from '@/lib/api'
import { DEMO_DASHBOARD_STATS, DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatByteRate, humanizeBytes } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import type { Chain, DashboardStats, Server } from '@/lib/types'
import { cn } from '@/lib/utils'

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

function serverInsight(server: Server, stats: DashboardStats) {
  if (!isServerOnline(server)) return '当前节点未在线。检查 Agent 进程、凭据和网络入口。'
  if (server.config_drift) return '检测到配置漂移。建议在服务器页核对并重新下发配置。'
  if ((server.metrics?.cpu_percent ?? 0) >= 80) return '节点 CPU 使用率较高。建议检查活跃连接和后台任务。'
  if (stats.links_degraded > 0) return `${stats.links_degraded} 条链路处于降级状态，建议优先查看相关跳点。`
  return '节点遥测与配置状态稳定，当前没有需要立即处理的异常。'
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
  const overviewPointerStart = useRef<number | null>(null)

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
      <Notice tone="danger" title="控制面板连接失败" className="max-w-xl">
        {error}
      </Notice>
    )
  }
  if (!stats) {
    return (
      <div className="dashboard-loading" aria-label="仪表盘加载中">
        <div className="dashboard-loading-bar" />
        <div className="dashboard-loading-stage" />
        <div className="dashboard-loading-panel" />
      </div>
    )
  }

  const isDemoData = import.meta.env.DEV && servers.length === 0
  const displayStats = isDemoData ? DEMO_DASHBOARD_STATS : stats
  const displayServers = isDemoData ? DEMO_SERVERS : servers
  const visibleServers = displayServers.slice(0, 8)
  const activeServerIndex = Math.min(activeIndex, Math.max(visibleServers.length - 1, 0))
  const selectedServer = visibleServers[activeServerIndex] ?? null
  const selectedMetrics = selectedServer?.metrics ?? null
  const serverHealth = percentage(displayStats.servers_online, displayStats.servers)
  const linkHealth = percentage(displayStats.links_active, displayStats.links)
  const activeChains = isDemoData
    ? displayStats.links_active
    : chains.filter((chain) => chain.status === 'active').length
  const today = new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date())
  const metricCards = [
    { key: 'nodes', eyebrow: 'NODES / 实时', title: '服务器', value: displayStats.servers, detail: `在线 ${displayStats.servers_online} 台`, change: `${serverHealth}% 可用`, icon: ServerIcon, fluid: { colorA: '#00E7D2', colorB: '#3CC8FF', colorC: '#075F68', seed: 1.7 } },
    { key: 'links', eyebrow: 'ROUTES / 链路', title: '链路', value: displayStats.links, detail: `活跃 ${displayStats.links_active} 条`, change: `${linkHealth}% 健康`, icon: RouteIcon, fluid: { colorA: '#3C8CFF', colorB: '#4EE5CE', colorC: '#153064', seed: 2.9, speed: 0.86 } },
    { key: 'users', eyebrow: 'ACCESS / 订阅', title: '用户', value: displayStats.users, detail: '已配置账户', change: '实时同步', icon: UsersIcon, fluid: { colorA: '#A7E8DB', colorB: '#52BFD2', colorC: '#294451', seed: 4.3, speed: 0.78 } },
    { key: 'alerts', eyebrow: 'SIGNAL / 告警', title: '降级链路', value: displayStats.links_degraded, detail: displayStats.links_degraded ? '需要检查' : '当前正常', change: displayStats.links_degraded ? '待处理' : '无告警', icon: ActivityIcon, fluid: { colorA: '#47F0A1', colorB: '#58A7E8', colorC: '#164D3D', seed: 5.8, speed: 0.96 } },
  ]

  return (
    <Page className="dashboard-page">
      <PageHeader
        title="仪表盘"
        description="服务器、链路与订阅访问的实时运行图景。"
        actions={(
          <div className="dashboard-toolbar">
            <span className="dashboard-date">{today}</span>
            <span className="sync-chip">
              <span className="status-dot is-online" />
              {lastSyncedAt ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} 已同步` : '同步中'}
            </span>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => void load()}
              aria-label="立即刷新"
              title="立即刷新"
            >
              <RefreshCwIcon />
            </Button>
          </div>
        )}
      />

      <div className="dashboard-workspace">
        <div className="dashboard-center">
          <section className="fluid-metric-grid" aria-label="核心指标">
            {metricCards.map((card, index) => (
              <article key={card.key} className="fluid-metric-card" data-fluid={index}>
                <FluidGlassCanvas config={card.fluid} index={index} />
                <span className="fluid-noise" aria-hidden="true" />
                <div className="fluid-metric-content">
                  <span className="fluid-eyebrow">{card.eyebrow}</span>
                  <span className="fluid-card-heading"><strong>{card.title}</strong><card.icon /></span>
                  <span className="fluid-card-value">{String(card.value).padStart(2, '0')}</span>
                  <span className="fluid-card-footer"><span>{card.detail}</span><b>{card.change}</b></span>
                </div>
              </article>
            ))}
          </section>

          <section className="globe-panel">
            <header className="workspace-section-header">
              <div>
                <span className="section-kicker">GLOBAL NODE NETWORK</span>
                <h2>全球节点</h2>
              </div>
              <div className="globe-controls">
                <span>{isDemoData ? '演示数据 · ' : ''}{visibleServers.length} 个节点</span>
                <Button
                  variant={motionEnabled ? 'secondary' : 'ghost'}
                  size="sm"
                  onClick={() => setMotionEnabled((current) => !current)}
                  aria-pressed={motionEnabled}
                >
                  {motionEnabled ? <PauseIcon /> : <PlayIcon />}
                  {motionEnabled ? '暂停自转' : '开始自转'}
                </Button>
              </div>
            </header>

            <Suspense fallback={<div className="dashboard-globe-loading" role="status" aria-label="正在加载全球节点" />}>
              <GlobeTopology
                servers={displayServers}
                chains={chains}
                activeServerId={selectedServer?.id}
                demoMode={isDemoData}
                motionEnabled={motionEnabled}
                onServerSelect={(serverId) => {
                  const index = visibleServers.findIndex((server) => server.id === serverId)
                  if (index >= 0) setActiveIndex(index)
                }}
              />
            </Suspense>
          </section>

          <section className="network-strip" aria-label="网络吞吐摘要">
            <div className="network-strip-title">
              <span className="network-live-mark"><WifiIcon /></span>
              <span><strong>实时网络</strong><small>{activeChains} 条链路已确认活跃</small></span>
            </div>
            <div className="network-stat"><ArrowUpIcon /><span><small>选中节点上行</small><strong>{formatByteRate(selectedMetrics?.network_tx_bps ?? null)}</strong></span></div>
            <div className="network-stat"><ArrowDownIcon /><span><small>选中节点下行</small><strong>{formatByteRate(selectedMetrics?.network_rx_bps ?? null)}</strong></span></div>
            <div className="network-health"><span style={{ width: `${linkHealth}%` }} /><small>链路健康度 {linkHealth}%</small></div>
          </section>
        </div>

        <aside className="smart-detail-panel" aria-label="服务器概况">
          <header>
            <div>
              <span className="section-kicker">SERVER OVERVIEW</span>
              <h2>服务器概况</h2>
            </div>
            {visibleServers.length > 0 && (
              <div className="server-carousel-controls">
                <span>{String(activeServerIndex + 1).padStart(2, '0')} / {String(visibleServers.length).padStart(2, '0')}</span>
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={visibleServers.length < 2}
                  onClick={() => setActiveIndex((activeServerIndex - 1 + visibleServers.length) % visibleServers.length)}
                  aria-label="上一台服务器"
                  title="上一台服务器"
                >
                  <ChevronLeftIcon />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={visibleServers.length < 2}
                  onClick={() => setActiveIndex((activeServerIndex + 1) % visibleServers.length)}
                  aria-label="下一台服务器"
                  title="下一台服务器"
                >
                  <ChevronRightIcon />
                </Button>
              </div>
            )}
          </header>

          {visibleServers.length > 0 ? (
            <>
              <div
                className="server-overview-viewport"
                aria-live="polite"
                onPointerDown={(event) => {
                  if (visibleServers.length < 2) return
                  overviewPointerStart.current = event.clientX
                  event.currentTarget.setPointerCapture(event.pointerId)
                }}
                onPointerUp={(event) => {
                  const start = overviewPointerStart.current
                  overviewPointerStart.current = null
                  if (start === null) return
                  const delta = event.clientX - start
                  if (Math.abs(delta) < 36) return
                  const direction = delta < 0 ? 1 : -1
                  setActiveIndex((activeServerIndex + direction + visibleServers.length) % visibleServers.length)
                }}
                onPointerCancel={() => { overviewPointerStart.current = null }}
              >
                <div
                  className="server-overview-track"
                  style={{ transform: `translate3d(-${activeServerIndex * 100}%, 0, 0)` }}
                >
                  {visibleServers.map((server, index) => {
                    const metrics = server.metrics
                    return (
                      <article
                        key={server.id}
                        className="server-overview-card"
                        aria-hidden={index !== activeServerIndex}
                      >
                        <section className="detail-identity">
                          <span className="detail-node-id">NODE-{String(server.id).padStart(4, '0')}</span>
                          <h3>{server.alias}</h3>
                          <p>{server.location || '位置待补全'}</p>
                          <span className={cn('detail-status', isServerOnline(server) && 'is-online')}>
                            <span className="status-dot" />
                            {serverConnectionLabel(server.connection_state)}
                          </span>
                        </section>

                        <dl className="detail-list">
                          <div><dt><MapPinIcon />位置</dt><dd>{server.country_code || '--'} · {server.location || '--'}</dd></div>
                          <div><dt>入口地址</dt><dd>{server.learned_addr || server.address || '--'}</dd></div>
                          <div><dt>Agent</dt><dd>{server.agent_version || '--'}</dd></div>
                          <div><dt>运行时间</dt><dd>{formatUptime(metrics?.uptime_seconds)}</dd></div>
                        </dl>

                        <section className="detail-metrics">
                          <h3>资源负载</h3>
                          {[
                            { label: 'CPU', value: metrics?.cpu_percent ?? 0, text: metrics?.cpu_percent === null || !metrics ? '--' : `${Math.round(metrics.cpu_percent)}%`, icon: CpuIcon },
                            { label: '内存', value: metrics ? percentage(metrics.mem_used, metrics.mem_total) : 0, text: metrics ? `${humanizeBytes(metrics.mem_used)} / ${humanizeBytes(metrics.mem_total)}` : '--', icon: GaugeIcon },
                            { label: '磁盘', value: metrics ? percentage(metrics.disk_used, metrics.disk_total) : 0, text: metrics ? `${humanizeBytes(metrics.disk_used)} / ${humanizeBytes(metrics.disk_total)}` : '--', icon: HardDriveIcon },
                          ].map((metric) => (
                            <div key={metric.label} className="detail-meter">
                              <span><metric.icon />{metric.label}<b>{metric.text}</b></span>
                              <i><span style={{ width: `${metric.value}%` }} /></i>
                            </div>
                          ))}
                        </section>

                        <section className="ai-insight">
                          <div><BotIcon /><strong>运行分析</strong></div>
                          <p>{serverInsight(server, displayStats)}</p>
                        </section>
                      </article>
                    )
                  })}
                </div>
              </div>

              <div className="server-carousel-pagination" aria-label="服务器轮播页码">
                {visibleServers.map((server, index) => (
                  <button
                    key={server.id}
                    type="button"
                    className={cn(index === activeServerIndex && 'is-active')}
                    onClick={() => setActiveIndex(index)}
                    aria-label={`查看 ${server.alias}`}
                    aria-current={index === activeServerIndex ? 'true' : undefined}
                  />
                ))}
              </div>
            </>
          ) : (
            <div className="detail-empty">
              <ServerIcon />
              <strong>暂无节点</strong>
              <p>服务器接入后会在这里显示运行分析。</p>
              <Link href="/servers" className="detail-link">添加服务器</Link>
            </div>
          )}
        </aside>
      </div>
    </Page>
  )
}
