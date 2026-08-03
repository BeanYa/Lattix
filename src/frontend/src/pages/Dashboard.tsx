import { lazy, Suspense, useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { Link } from 'wouter'
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  ChevronRightIcon,
  CpuIcon,
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

import { Notice, Page, PageHeader } from '@/components/PagePrimitives'
import ElectricBorder from '@/components/react-bits/ElectricBorder'
import { Button } from '@/components/ui/button'
import { api, errorMessage } from '@/lib/api'
import { DEMO_DASHBOARD_STATS, DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatByteRate, humanizeBytes } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import type { Chain, DashboardStats, Server } from '@/lib/types'
import { cn } from '@/lib/utils'

import './subscription.css'

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

function serverStatusTone(server: Server) {
  if (!isServerOnline(server)) return 'is-offline'
  if (server.config_drift) return 'is-warning'
  return 'is-ready'
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
    return <Notice tone="danger" title="控制面板连接失败" className="max-w-xl">{error}</Notice>
  }

  if (!stats) {
    return (
      <div className="dashboard-quiet-loading" aria-label="仪表盘加载中">
        <span />
        <span />
        <span />
      </div>
    )
  }

  const isDemoData = import.meta.env.DEV && servers.length === 0
  const displayStats = isDemoData ? DEMO_DASHBOARD_STATS : stats
  const displayServers = isDemoData ? DEMO_SERVERS : servers
  const visibleServers = displayServers.slice(0, 7)
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
  const signalItems = [
    { label: '服务器', value: displayStats.servers, detail: `${displayStats.servers_online} 台在线`, icon: ServerIcon },
    { label: '链路', value: displayStats.links, detail: `${activeChains} 条活跃`, icon: RouteIcon },
    { label: '订阅用户', value: displayStats.users, detail: '访问状态正常', icon: UsersIcon },
    { label: '待处理', value: displayStats.links_degraded, detail: displayStats.links_degraded ? '存在降级链路' : '暂无异常', icon: ActivityIcon },
  ]
  const availability = Math.max(serverHealth, linkHealth)
  const healthStyle = { '--usage-angle': `${availability * 3.6}deg` } as CSSProperties

  return (
    <Page className="dashboard-page dashboard-subscription-language">
      <PageHeader
        title="仪表盘"
        description="服务器、链路与订阅访问的实时运行图景。"
        actions={(
          <div className="dashboard-toolbar">
            <span className="dashboard-date">{today}</span>
            <span className="dashboard-sync-status">
              <i />
              {lastSyncedAt ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} 已同步` : '正在同步'}
            </span>
            <Button variant="ghost" size="icon" onClick={() => void load()} aria-label="立即刷新" title="立即刷新">
              <RefreshCwIcon />
            </Button>
          </div>
        )}
      />

      <section className="dashboard-command-deck" aria-label="网络运行概览">
        <div className="dashboard-command-copy">
          <span className="dashboard-kicker">PANEL OVERVIEW</span>
          <h2>网络运行概览</h2>
          <p>核心节点和传输链路保持同步，集中查看当前可用性与资源负载。</p>
          <Link href="/servers" className="dashboard-primary-link">
            管理服务器
            <ChevronRightIcon />
          </Link>
        </div>
        <ElectricBorder className="traffic-electric dashboard-usage-electric" color="#bdf33b" speed={0.8} chaos={0.08} borderRadius={8}>
          <div className="dashboard-usage-orbit traffic-orbit" style={healthStyle} aria-label={`整体可用性 ${availability}%`}>
            <div className="orbit-track">
              <div className="orbit-core">
                <span>整体可用性</span>
                <strong>{availability}%</strong>
                <small>{displayStats.links_degraded ? `${displayStats.links_degraded} 项需要关注` : '所有系统稳定'}</small>
              </div>
            </div>
            <span className="orbit-node node-a" />
            <span className="orbit-node node-b" />
            <span className="orbit-node node-c" />
          </div>
        </ElectricBorder>
      </section>

      <section className="dashboard-signal-grid" aria-label="核心指标">
        {signalItems.map((item) => (
          <article key={item.label} className="dashboard-signal-item">
            <item.icon />
            <span>{item.label}</span>
            <strong>{String(item.value).padStart(2, '0')}</strong>
            <small>{item.detail}</small>
          </article>
        ))}
      </section>

      <div className="dashboard-content-grid">
        <section className="dashboard-globe-section" aria-labelledby="dashboard-node-heading">
          <header className="dashboard-section-heading">
            <div>
              <span className="dashboard-kicker">NODE STATUS</span>
              <h2 id="dashboard-node-heading">全球节点</h2>
            </div>
            <div className="dashboard-globe-controls">
              <span>{isDemoData ? '演示数据 · ' : ''}{visibleServers.length} 个节点</span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setMotionEnabled((current) => !current)}
                aria-pressed={motionEnabled}
              >
                {motionEnabled ? <PauseIcon /> : <PlayIcon />}
                {motionEnabled ? '暂停' : '巡航'}
              </Button>
            </div>
          </header>

          {visibleServers.length ? (
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
          ) : (
            <div className="dashboard-node-empty">
              <ServerIcon />
              <strong>等待服务器接入</strong>
              <span>添加服务器后，这里会显示全球节点状态。</span>
              <Link href="/servers">添加服务器</Link>
            </div>
          )}
        </section>

        <aside className="dashboard-focus-panel" aria-label="选中服务器概况">
          {selectedServer ? (
            <>
              <header className="dashboard-focus-header">
                <div>
                  <span className="dashboard-kicker">FOCUS NODE</span>
                  <h2>{selectedServer.alias}</h2>
                  <p><MapPinIcon />{selectedServer.country_code || '--'} · {selectedServer.location || '位置待补充'}</p>
                </div>
                <span className={cn('dashboard-status-label', serverStatusTone(selectedServer))}>
                  <i />{serverConnectionLabel(selectedServer.connection_state)}
                </span>
              </header>

              <dl className="dashboard-focus-meta">
                <div><dt>入口地址</dt><dd>{selectedServer.learned_addr || selectedServer.address || '--'}</dd></div>
                <div><dt>Agent</dt><dd>{selectedServer.agent_version || '--'}</dd></div>
                <div><dt>运行时间</dt><dd>{formatUptime(selectedMetrics?.uptime_seconds)}</dd></div>
              </dl>

              <section className="dashboard-resource-list" aria-label="资源负载">
                <div>
                  <span><CpuIcon />CPU</span>
                  <strong>{selectedMetrics ? `${Math.round(selectedMetrics.cpu_percent ?? 0)}%` : '--'}</strong>
                </div>
                <div>
                  <span><ArrowUpIcon />上行</span>
                  <strong>{formatByteRate(selectedMetrics?.network_tx_bps ?? null)}</strong>
                </div>
                <div>
                  <span><ArrowDownIcon />下行</span>
                  <strong>{formatByteRate(selectedMetrics?.network_rx_bps ?? null)}</strong>
                </div>
                <div>
                  <span><HardDriveIcon />磁盘</span>
                  <strong>{selectedMetrics ? `${humanizeBytes(selectedMetrics.disk_used)} / ${humanizeBytes(selectedMetrics.disk_total)}` : '--'}</strong>
                </div>
              </section>

              <Link href="/servers" className="dashboard-focus-link">查看服务器 <ChevronRightIcon /></Link>
            </>
          ) : (
            <div className="dashboard-focus-empty">
              <WifiIcon />
              <strong>尚未选择节点</strong>
              <span>选择服务器后显示运行概况。</span>
            </div>
          )}
        </aside>
      </div>
    </Page>
  )
}
