import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from 'react'
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CpuIcon,
  GaugeIcon,
  HardDriveIcon,
  PauseIcon,
  PlayIcon,
  RefreshCwIcon,
  RouteIcon,
  SearchIcon,
  ServerIcon,
  TimerIcon,
  UsersIcon,
  XIcon,
} from 'lucide-react'
import { Link } from 'wouter'

import { Notice } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { DEMO_DASHBOARD_STATS, DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatByteRate, humanizeBytes } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import type { Chain, ChainStatus, DashboardStats, NodeStatus, Server } from '@/lib/types'
import { cn } from '@/lib/utils'

import { getCableState, type RouteTone } from './route-cable'
import { animateRackFeedback, hopFeedbackDelay, nextChainIndex } from './motion'
import { RollingReadout } from './RollingReadout'
import { filterChains } from './chain-tools'
import { RouteNavigator } from './RouteNavigator'
import { HopServerDetails } from './HopServerDetails'
import './dashboard.css'

interface RouteHopView {
  id: string
  role: string
  label: string
  code: string
  location: string
  statusLabel: string
  tone: RouteTone
  serverId: number
  server?: Server
  address: string
}

interface ChainView {
  id: string
  name: string
  statusLabel: string
  tone: RouteTone
  hops: RouteHopView[]
  error: string
  metaLabel: string
}

interface IssueView {
  id: string
  title: string
  subject: string
  detail: string
  href: '/servers' | '/chains'
  tone: 'warning' | 'pending'
}

const CHAIN_STATUS_LABEL: Record<ChainStatus, string> = {
  active: '正常',
  applying: '部署中',
  failed: '异常',
  pending: '部署中',
  degraded: '降级',
  waiting_for_agent: '等待 Agent',
  active_unconfirmed: '已强制发布',
  active_failed: '发布后失败',
  cleanup_pending: '等待清理',
  invalid: '已失效',
  deleted: '已删除',
}

const NODE_STATUS_LABEL: Record<NodeStatus, string> = {
  active: '正常',
  applying: '部署中',
  failed: '异常',
  pending: '等待部署',
}

function percentage(value: number, total: number) {
  if (total <= 0) return 0
  return Math.min(100, Math.round((value / total) * 100))
}

function formatUptime(seconds: number | undefined) {
  if (seconds === undefined) return '--'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  return days > 0 ? `${days}d ${String(hours).padStart(2, '0')}h` : `${hours}h`
}

function channelCode(server: Server, index: number) {
  const country = server.country_code?.toUpperCase() || 'NODE'
  return `${country}-${String(index + 1).padStart(2, '0')}`
}

function routeTone(chain: Chain): RouteTone {
  if (chain.status === 'active') return 'active'
  if (['degraded', 'failed', 'active_failed', 'invalid'].includes(chain.status)) return 'degraded'
  return 'inactive'
}

function nodeTone(status: NodeStatus): RouteTone {
  if (status === 'active') return 'active'
  if (status === 'failed') return 'degraded'
  return 'inactive'
}

function routeToneLabel(tone: RouteTone) {
  if (tone === 'active') return 'ACTIVE SIGNAL'
  if (tone === 'degraded') return 'DEGRADED'
  return 'STANDBY'
}

function buildDemoChains(servers: Server[]): ChainView[] {
  const definitions = [
    { name: '演示链路 1', serverIndexes: [0, 1, 3], tone: 'active' as const },
    { name: '亚太中继链路', serverIndexes: [1, 2, 3], tone: 'active' as const },
    { name: '欧洲核心链路', serverIndexes: [2, 1, 3], tone: 'active' as const },
    { name: '美西出口链路', serverIndexes: [0, 3], tone: 'active' as const },
    { name: '香港备用链路', serverIndexes: [4, 1, 5], tone: 'degraded' as const },
    { name: '悉尼边缘链路', serverIndexes: [5, 1, 3], tone: 'active' as const },
  ]

  return definitions.map((definition, chainIndex) => ({
    id: `demo-${chainIndex + 1}`,
    name: definition.name,
    statusLabel: definition.tone === 'degraded' ? '降级' : '正常',
    tone: definition.tone,
    error: definition.tone === 'degraded' ? '入口服务器正在重连' : '',
    metaLabel: `${definition.serverIndexes.length} 跳链路`,
    hops: definition.serverIndexes.map((serverIndex, hopIndex) => {
      const server = servers[serverIndex]
      const isFault = definition.tone === 'degraded' && hopIndex === 0
      const role =
        hopIndex === 0
          ? 'ENTRY'
          : hopIndex === definition.serverIndexes.length - 1
            ? 'EXIT'
            : 'RELAY'
      return {
        id: `${chainIndex}-${server.id}-${hopIndex}`,
        role,
        label: server.alias,
        code: channelCode(server, serverIndex),
        location: server.location || '位置待补充',
        statusLabel: isFault ? '重连中' : '正常',
        tone: isFault ? 'degraded' : 'active',
        serverId: server.id,
        server,
        address: server.address,
      }
    }),
  }))
}

function buildChainViews(chains: Chain[], servers: Server[]): ChainView[] {
  const serverById = new Map(servers.map((server, index) => [server.id, { server, index }]))
  return chains
    .filter((chain) => chain.status !== 'deleted')
    .map((chain) => {
      const orderedHops = [...chain.hops].sort((left, right) => left.seq - right.seq)
      return {
        id: String(chain.id),
        name: chain.name || `链路 #${chain.id}`,
        statusLabel: CHAIN_STATUS_LABEL[chain.status],
        tone: routeTone(chain),
        error: chain.error || chain.endpoint_error || '',
        metaLabel: chain.entry_port
          ? `${orderedHops.length} 跳 · 入口 ${chain.entry_port}`
          : `${orderedHops.length} 跳链路`,
        hops: orderedHops.map((hop, hopIndex) => {
          const match = serverById.get(hop.server_id)
          const role = hop.role === 'middle' ? 'RELAY' : hop.role.toUpperCase()
          return {
            id: `${chain.id}-${hop.id}-${hopIndex}`,
            role,
            label: match?.server.alias || hop.server_alias,
            code: match ? channelCode(match.server, match.index) : `NODE-${hop.server_id}`,
            location: match?.server.location || match?.server.country_code || '位置待补充',
            statusLabel: NODE_STATUS_LABEL[hop.status],
            tone: nodeTone(hop.status),
            serverId: hop.server_id,
            server: match?.server,
            address: hop.address,
          }
        }),
      }
    })
}

function buildIssues(servers: Server[], chainViews: ChainView[]): IssueView[] {
  const serverIssues = servers
    .filter((server) => !isServerOnline(server))
    .map<IssueView>((server) => ({
      id: `server-${server.id}`,
      title: '服务器异常',
      subject: server.alias,
      detail: `${serverConnectionLabel(server.connection_state)} · ${server.location || server.country_code || '位置待补充'}`,
      href: '/servers',
      tone: 'warning',
    }))
  const chainIssues = chainViews
    .filter((chain) => chain.tone !== 'active')
    .map<IssueView>((chain) => ({
      id: `chain-${chain.id}`,
      title: chain.tone === 'degraded' ? '链路异常' : '链路处理中',
      subject: chain.name,
      detail: chain.error || `${chain.metaLabel} · ${chain.statusLabel}`,
      href: '/chains',
      tone: chain.tone === 'degraded' ? 'warning' : 'pending',
    }))
  return [...serverIssues, ...chainIssues].slice(0, 4)
}

const PATCH_CABLE_PATH = 'M0 0 C0 52 18 58 40 58 H60 C82 58 100 52 100 0'

function ChainSelector({
  chains,
  selectedId,
  onSelect,
}: {
  chains: ChainView[]
  selectedId: string | undefined
  onSelect: (id: string) => void
}) {
  const list = useRef<HTMLDivElement>(null)
  const cursor = useRef<HTMLSpanElement>(null)
  const positioned = useRef(false)
  const stopMotion = useRef<() => void>(() => {})
  const search = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [attentionOnly, setAttentionOnly] = useState(false)
  const visibleChains = useMemo(
    () => filterChains(chains, query, attentionOnly),
    [chains, query, attentionOnly],
  )
  const visibleKey = JSON.stringify(visibleChains.map((chain) => chain.id))
  const previousVisible = useRef(visibleKey)
  const chainNumbers = new Map(chains.map((chain, index) => [chain.id, index + 1]))
  const attentionCount = chains.filter((chain) => chain.tone !== 'active').length
  const selectionHidden = selectedId && !visibleChains.some((chain) => chain.id === selectedId)
  const reset = () => {
    setQuery('')
    setAttentionOnly(false)
    search.current?.focus()
  }

  useLayoutEffect(() => {
    if (previousVisible.current === visibleKey) return
    previousVisible.current = visibleKey
    const host = list.current
    if (!host) return
    host.scrollTop = 0
    host.scrollLeft = 0
    return animateRackFeedback(host, [{ opacity: 0.65 }, { opacity: 1 }], { duration: 160 })
  }, [visibleKey])

  useLayoutEffect(() => {
    const host = list.current
    const marker = cursor.current
    const selected = host?.querySelector<HTMLButtonElement>('[aria-pressed="true"]')
    if (!host || !marker) return
    if (!selected) {
      stopMotion.current()
      marker.style.visibility = 'hidden'
      positioned.current = false
      return
    }
    const position = (animated: boolean) => {
      const from = getComputedStyle(marker).transform
      stopMotion.current()
      const to = `translate(${selected.offsetLeft}px, ${selected.offsetTop}px)`
      marker.style.width = `${selected.offsetWidth}px`
      marker.style.height = `${selected.offsetHeight}px`
      marker.style.transform = to
      marker.style.visibility = 'visible'
      if (animated && positioned.current) {
        stopMotion.current = animateRackFeedback(marker, [{ transform: from }, { transform: to }])
      }
      positioned.current = true
    }
    position(true)
    let size = `${host.clientWidth}:${host.clientHeight}:${selected.offsetWidth}:${selected.offsetHeight}`
    const observer = new ResizeObserver(() => {
      const nextSize = `${host.clientWidth}:${host.clientHeight}:${selected.offsetWidth}:${selected.offsetHeight}`
      if (size !== nextSize) {
        size = nextSize
        position(false)
      }
    })
    observer.observe(host)
    observer.observe(selected)
    // Do not cancel here: the next selection reads the in-flight position first.
    return () => observer.disconnect()
  }, [selectedId, visibleKey])

  useEffect(() => () => stopMotion.current(), [])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const buttons = Array.from(
      list.current?.querySelectorAll<HTMLButtonElement>('.pb-chain-row') ?? [],
    )
    const current = buttons.indexOf(event.target as HTMLButtonElement)
    if (current < 0) return
    const next = nextChainIndex(event.key, current, visibleChains.length)
    if (next === null) return
    event.preventDefault()
    buttons[next]?.focus({ preventScroll: true })
    buttons[next]?.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' })
    onSelect(visibleChains[next].id)
  }

  return (
    <div className="pb-chain-picker">
      <div className="pb-chain-tools">
        <div className="pb-chain-search">
          <SearchIcon aria-hidden="true" />
          <input
            ref={search}
            type="search"
            value={query}
            maxLength={80}
            aria-label="搜索链路或节点"
            placeholder="搜索链路或节点"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return
              if (event.key === 'Escape' && query) {
                event.preventDefault()
                setQuery('')
              }
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                list.current?.querySelector<HTMLButtonElement>('.pb-chain-row')?.focus()
              }
            }}
          />
          {query ? (
            <button
              type="button"
              aria-label="清空搜索"
              onClick={() => {
                setQuery('')
                search.current?.focus()
              }}
            >
              <XIcon aria-hidden="true" />
            </button>
          ) : null}
        </div>
        <div className="pb-chain-filter-line">
          <div
            className="pb-chain-filter"
            role="group"
            aria-label="链路状态筛选"
            data-attention={attentionOnly}
          >
            <span className="pb-filter-thumb" aria-hidden="true" />
            <button
              type="button"
              aria-pressed={!attentionOnly}
              onClick={() => setAttentionOnly(false)}
            >
              全部
            </button>
            <button
              type="button"
              aria-pressed={attentionOnly}
              onClick={() => setAttentionOnly(true)}
            >
              需关注 <span>{attentionCount}</span>
            </button>
          </div>
          <span
            className="pb-filter-count"
            role="status"
            aria-label={`显示 ${visibleChains.length} 条，共 ${chains.length} 条链路`}
          >
            {visibleChains.length} / {chains.length}
          </span>
        </div>
      </div>
      <div
        className="pb-chain-list"
        ref={list}
        onKeyDown={onKeyDown}
        role="group"
        aria-label="选择已有链路"
      >
        <span className="pb-chain-cursor" ref={cursor} aria-hidden="true" />
        {visibleChains.map((chain) => (
          <button
            key={chain.id}
            type="button"
            className={cn(
              'pb-chain-row',
              chain.id === selectedId && 'is-selected',
              `is-${chain.tone}`,
            )}
            onClick={() => onSelect(chain.id)}
            aria-pressed={chain.id === selectedId}
          >
            <span className="pb-chain-number">
              {String(chainNumbers.get(chain.id)).padStart(2, '0')}
            </span>
            <span className="pb-chain-copy">
              <strong>{chain.name}</strong>
              <small>{chain.metaLabel}</small>
            </span>
            <span className="pb-chain-state">
              <i aria-hidden="true" />
              {chain.statusLabel}
            </span>
          </button>
        ))}
        {!visibleChains.length ? (
          <div className="pb-chain-no-results">
            <SearchIcon aria-hidden="true" />
            <strong>没有匹配的链路</strong>
            <span>试试链路名、节点名或地区</span>
            <button type="button" onClick={reset}>
              重置筛选
            </button>
          </div>
        ) : null}
      </div>
      {selectionHidden && visibleChains.length > 0 ? (
        <div className="pb-filter-selection-note">
          <span>当前查看的链路已被筛除</span>
          <button type="button" onClick={reset}>
            显示全部
          </button>
        </div>
      ) : null}
    </div>
  )
}

function PatchCable({
  chainTone,
  source,
  target,
  index,
}: {
  chainTone: RouteTone
  source: RouteHopView
  target: RouteHopView
  index: number
}) {
  const state = getCableState(chainTone, source.tone, target.tone)
  return (
    <span
      className={cn('pb-patch-cable', `is-${state.tone}`)}
      style={{ '--pb-signal-delay': `${index * 0.65}s` } as CSSProperties}
      role="img"
      aria-label={`${source.label} OUT → ${target.label} IN：${state.label}`}
    >
      <svg viewBox="0 0 100 80" preserveAspectRatio="none" aria-hidden="true">
        <path className="pb-cable-sheath" d={PATCH_CABLE_PATH} vectorEffect="non-scaling-stroke" />
        <path className="pb-cable-core" d={PATCH_CABLE_PATH} vectorEffect="non-scaling-stroke" />
        {state.tone === 'active' ? (
          <>
            <path
              className="pb-cable-signal pb-cable-tail"
              d={PATCH_CABLE_PATH}
              pathLength="100"
              vectorEffect="non-scaling-stroke"
            />
            <path
              className="pb-cable-signal pb-cable-head"
              d={PATCH_CABLE_PATH}
              pathLength="100"
              vectorEffect="non-scaling-stroke"
            />
          </>
        ) : null}
        <rect className="pb-cable-plug" x="-4" y="-2" width="8" height="18" rx="3" />
        <rect className="pb-cable-plug" x="96" y="-2" width="8" height="18" rx="3" />
        <path
          className="pb-cable-collar"
          d="M-3 5 H3 M97 5 H103"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <span className="pb-cable-label" aria-hidden="true">
        {state.label}
      </span>
    </span>
  )
}

function RouteFocus({ chain, motionPaused }: { chain: ChainView; motionPaused: boolean }) {
  const stageRef = useRef<HTMLDivElement>(null)
  const [signalRunning, setSignalRunning] = useState(false)
  const previousChain = useRef(chain.id)

  useLayoutEffect(() => {
    const changed = previousChain.current !== chain.id
    previousChain.current = chain.id
    const stage = stageRef.current
    if (!stage || !changed) return
    // A new selection starts at ENTRY; polling the same chain never resets scroll or motion.
    stage.scrollLeft = 0
    if (motionPaused) return
    const cleanups = Array.from(stage.querySelectorAll<HTMLElement>('.pb-hop-card')).map(
      (card, index) =>
        animateRackFeedback(card, [{ borderColor: '#d2e6ee' }, { borderColor: '#71898f' }], {
          duration: 360,
          delay: hopFeedbackDelay(index),
        }),
    )
    return () => cleanups.forEach((stop) => stop())
  }, [chain.id, motionPaused])

  useEffect(() => {
    const stage = stageRef.current
    if (!stage) return
    let inView = false
    const syncMotion = () => setSignalRunning(inView && !document.hidden)
    const observer = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting
      syncMotion()
    })
    observer.observe(stage)
    document.addEventListener('visibilitychange', syncMotion)
    return () => {
      observer.disconnect()
      document.removeEventListener('visibilitychange', syncMotion)
    }
  }, [])

  return (
    <article
      className={cn('pb-route-focus', `is-${chain.tone}`)}
      data-signal-running={signalRunning && !motionPaused}
      aria-label={`选中链路：${chain.name}`}
    >
      <RouteNavigator
        stageRef={stageRef}
        chainId={chain.id}
        count={chain.hops.length}
        motionPaused={motionPaused}
      />
      <div className="pb-route-stage" ref={stageRef}>
        <ol className="pb-route-steps">
          {chain.hops.map((hop, index) => (
            <li key={hop.id} className={`is-${hop.tone}`}>
              <div className="pb-hop-card">
                <span className="pb-hop-sequence">
                  <span>{String(index + 1).padStart(2, '0')}</span>
                  <b>{hop.role}</b>
                </span>
                <span className="pb-hop-port" aria-hidden="true">
                  <ServerIcon />
                  <i />
                </span>
                <span className="pb-hop-copy">
                  <strong title={hop.label}>{hop.label}</strong>
                  <small>
                    {hop.code} · <span className="pb-hop-status">节点{hop.statusLabel}</span>
                  </small>
                </span>
                <HopServerDetails server={hop.server} fallbackAddress={hop.address} />
                <span className="pb-hop-jacks" aria-hidden="true">
                  {index > 0 ? (
                    <span className="pb-hop-jack is-in">
                      <i />
                      <span>IN</span>
                    </span>
                  ) : (
                    <span />
                  )}
                  {chain.hops.length === 1 ? <small>单节点 · 无中继连线</small> : null}
                  {index < chain.hops.length - 1 ? (
                    <span className="pb-hop-jack is-out">
                      <i />
                      <span>OUT</span>
                    </span>
                  ) : (
                    <span />
                  )}
                </span>
              </div>
              {index < chain.hops.length - 1 ? (
                <PatchCable
                  chainTone={chain.tone}
                  source={hop}
                  target={chain.hops[index + 1]}
                  index={index}
                />
              ) : null}
            </li>
          ))}
        </ol>
      </div>

      <footer className="pb-route-summary">
        <p className="pb-route-hint">端口方向 OUT → IN · 动效表示链路状态，非实时流量</p>
        <dl>
          <div>
            <dt>链路名称</dt>
            <dd>{chain.name}</dd>
          </div>
          <div>
            <dt>链路状态</dt>
            <dd className={`is-${chain.tone}`}>{chain.statusLabel}</dd>
          </div>
          <div>
            <dt>拓扑</dt>
            <dd>{chain.metaLabel}</dd>
          </div>
          <div>
            <dt>入口</dt>
            <dd>{chain.hops[0]?.label || '--'}</dd>
          </div>
          <div>
            <dt>出口</dt>
            <dd>{chain.hops.at(-1)?.label || '--'}</dd>
          </div>
        </dl>
      </footer>
    </article>
  )
}

export default function DashboardPatchbay() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [activeChainId, setActiveChainId] = useState<string | null>(null)
  const [lastSyncedAt, setLastSyncedAt] = useState<Date | null>(null)
  const [error, setError] = useState('')
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [motionPaused, setMotionPaused] = useState(false)
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
    } catch (loadError) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(loadError))
    }
  }, [])

  const refresh = useCallback(async () => {
    setIsRefreshing(true)
    try {
      await load()
    } finally {
      setIsRefreshing(false)
    }
  }, [load])

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

  const isDemoData = import.meta.env.DEV && servers.length === 0
  const displayServers = isDemoData ? DEMO_SERVERS : servers
  const chainViews = useMemo(
    () => (isDemoData ? buildDemoChains(displayServers) : buildChainViews(chains, displayServers)),
    [chains, displayServers, isDemoData],
  )
  const issues = useMemo(
    () => buildIssues(displayServers, chainViews),
    [chainViews, displayServers],
  )

  if (error)
    return (
      <Notice tone="danger" title="控制面板连接失败">
        {error}
      </Notice>
    )

  if (!stats) {
    return (
      <div className="pb-loading" role="status" aria-label="仪表盘加载中">
        <span className="pb-loading-lamp" aria-hidden="true" />
        <span>正在接通信号台</span>
      </div>
    )
  }

  const displayStats = isDemoData ? DEMO_DASHBOARD_STATS : stats
  const selectedChain =
    chainViews.find((chain) => chain.id === activeChainId) ?? chainViews[0] ?? null
  const entryServerId = selectedChain?.hops[0]?.serverId
  const selectedServer = displayServers.find((server) => server.id === entryServerId) ?? null
  const selectedServerIndex = selectedServer
    ? displayServers.findIndex((server) => server.id === selectedServer.id)
    : -1
  const selectedMetrics = selectedServer?.metrics
  const activeChains = isDemoData
    ? displayStats.links_active
    : chains.reduce((count, chain) => count + (chain.status === 'active' ? 1 : 0), 0)
  const unavailableServers = Math.max(0, displayStats.servers - displayStats.servers_online)
  const chainIssues =
    isDemoData || chains.length === 0
      ? displayStats.links_degraded
      : chains.filter((chain) => !['active', 'deleted'].includes(chain.status)).length
  const issueCount = unavailableServers + chainIssues
  const systemHealthy = issueCount === 0
  const healthSummary = systemHealthy
    ? '所有系统稳定'
    : [
        unavailableServers ? `${unavailableServers} 台服务器离线` : '',
        chainIssues ? `${chainIssues} 条链路异常` : '',
      ]
        .filter(Boolean)
        .join(' · ')
  const metricItems = [
    {
      label: '服务器',
      value: displayStats.servers,
      detail: `${displayStats.servers_online} 台在线`,
      icon: ServerIcon,
    },
    { label: '链路', value: displayStats.links, detail: `${activeChains} 条活跃`, icon: RouteIcon },
    { label: '订阅用户', value: displayStats.users, detail: '订阅用户总数', icon: UsersIcon },
    {
      label: '待处理',
      value: issueCount,
      detail: systemHealthy ? '没有异常' : '资源需关注',
      icon: ActivityIcon,
    },
  ]

  const runtimeItems = selectedServer
    ? [
        {
          label: 'CPU 使用率',
          value: selectedMetrics ? `${Math.round(selectedMetrics.cpu_percent ?? 0)}%` : '--',
          detail: selectedMetrics ? `LOAD ${selectedMetrics.load1.toFixed(2)}` : 'NO SIGNAL',
          level: selectedMetrics?.cpu_percent ?? 0,
          icon: CpuIcon,
        },
        {
          label: '上行速率',
          value: formatByteRate(selectedMetrics?.network_tx_bps ?? null),
          detail: selectedMetrics?.network_interface || 'NO SIGNAL',
          level: null,
          icon: ArrowUpIcon,
        },
        {
          label: '下行速率',
          value: formatByteRate(selectedMetrics?.network_rx_bps ?? null),
          detail: selectedMetrics?.network_interface || 'NO SIGNAL',
          level: null,
          icon: ArrowDownIcon,
        },
        {
          label: '磁盘使用量',
          value: selectedMetrics ? humanizeBytes(selectedMetrics.disk_used) : '--',
          detail: selectedMetrics ? `/ ${humanizeBytes(selectedMetrics.disk_total)}` : 'NO SIGNAL',
          level: selectedMetrics
            ? percentage(selectedMetrics.disk_used, selectedMetrics.disk_total)
            : 0,
          icon: HardDriveIcon,
        },
        {
          label: '运行时间',
          value: formatUptime(selectedMetrics?.uptime_seconds),
          detail: selectedServer.location || '位置待补充',
          level: null,
          icon: TimerIcon,
        },
        {
          label: 'Agent 版本',
          value: selectedServer.agent_version || '--',
          detail: selectedServer.effective_xray_version
            ? `XRAY ${selectedServer.effective_xray_version}`
            : 'VERSION UNKNOWN',
          level: null,
          icon: GaugeIcon,
        },
      ]
    : []

  return (
    <div className="pb-dashboard">
      <header className="pb-master-strip">
        <div className="pb-master-title">
          <span className="pb-master-index" aria-hidden="true">
            CH 01
          </span>
          <div>
            <h1>仪表盘</h1>
            <p>服务器信号与多跳链路总览</p>
          </div>
        </div>
        <div className="pb-master-status">
          {isDemoData ? <span className="pb-demo-plate">演示数据</span> : null}
          <span className={cn('pb-system-state', systemHealthy ? 'is-good' : 'is-warning')}>
            {systemHealthy ? <CheckCircle2Icon /> : <ActivityIcon />}
            {healthSummary}
          </span>
          <span className="pb-sync-time">
            {lastSyncedAt
              ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} 已同步`
              : '正在同步'}
          </span>
          <button
            type="button"
            className="pb-refresh"
            onClick={() => void refresh()}
            disabled={isRefreshing}
            aria-busy={isRefreshing}
          >
            <RefreshCwIcon aria-hidden="true" />
            {isRefreshing ? '刷新中' : '立即刷新'}
          </button>
        </div>
      </header>

      <section className="pb-overview-strip" aria-label="核心指标">
        {metricItems.map((item, index) => (
          <article
            key={item.label}
            className={cn('pb-overview-cell', index === 3 && issueCount > 0 && 'is-warning')}
          >
            <item.icon aria-hidden="true" />
            <span>
              <small>{item.label}</small>
              <strong>
                <RollingReadout value={String(item.value).padStart(2, '0')} />
              </strong>
            </span>
            <p>{item.detail}</p>
          </article>
        ))}
      </section>

      <section className="pb-command-deck" aria-label="链路工作台">
        <div className="pb-route-bay">
          <header className="pb-route-bay-header">
            <h2>{selectedChain?.name || '暂无可用链路'}</h2>
            {selectedChain ? (
              <div className="pb-route-controls">
                <span className={cn('pb-route-tone', `is-${selectedChain.tone}`)}>
                  <i aria-hidden="true" />
                  {routeToneLabel(selectedChain.tone)}
                </span>
                <button
                  type="button"
                  className="pb-motion-toggle"
                  onClick={() => setMotionPaused((paused) => !paused)}
                  aria-label={motionPaused ? '播放信号动效' : '暂停信号动效'}
                  title={motionPaused ? '播放信号动效' : '暂停信号动效'}
                  aria-pressed={motionPaused}
                >
                  {motionPaused ? (
                    <PlayIcon aria-hidden="true" />
                  ) : (
                    <PauseIcon aria-hidden="true" />
                  )}
                </button>
              </div>
            ) : null}
          </header>

          <div className="pb-route-workspace">
            <aside className="pb-chain-rail" aria-label="已有链路">
              <div className="pb-chain-rail-title">
                <span>已有链路</span>
                <strong>{String(chainViews.length).padStart(2, '0')}</strong>
              </div>
              {chainViews.length ? (
                <ChainSelector
                  chains={chainViews}
                  selectedId={selectedChain?.id}
                  onSelect={setActiveChainId}
                />
              ) : (
                <div className="pb-chain-empty">尚未创建链路</div>
              )}
              <Link href="/chains" className="pb-chain-all">
                管理全部链路 <ChevronRightIcon aria-hidden="true" />
              </Link>
            </aside>

            <div className="pb-route-canvas">
              {selectedChain?.hops.length ? (
                <RouteFocus chain={selectedChain} motionPaused={motionPaused} />
              ) : (
                <div className="pb-route-empty">
                  <RouteIcon aria-hidden="true" />
                  <strong>等待链路拓扑</strong>
                  <span>创建链路后，可在左侧选择并检查每一跳的实时状态。</span>
                  <Link href="/chains">前往链路管理</Link>
                </div>
              )}
            </div>
          </div>
        </div>

        <aside className="pb-incident-dock" aria-label="待处理事项">
          <header>
            <div>
              <ActivityIcon aria-hidden="true" />
              <h2>待处理</h2>
            </div>
            <strong>{String(issueCount).padStart(2, '0')}</strong>
          </header>
          {issues.length ? (
            <div className="pb-issue-list">
              {issues.map((issue) => (
                <Link
                  key={issue.id}
                  href={issue.href}
                  className={cn('pb-issue', `is-${issue.tone}`)}
                >
                  <span className="pb-issue-mark" aria-hidden="true" />
                  <span>
                    <small>{issue.title}</small>
                    <strong>{issue.subject}</strong>
                    <p>{issue.detail}</p>
                  </span>
                  <ChevronRightIcon aria-hidden="true" />
                </Link>
              ))}
            </div>
          ) : (
            <div className="pb-issue-clear">
              <CheckCircle2Icon aria-hidden="true" />
              <strong>当前没有异常</strong>
              <span>服务器和链路都处于可用状态。</span>
            </div>
          )}
          <footer>
            <Link href="/logs/operations">
              查看运行日志 <ChevronRightIcon aria-hidden="true" />
            </Link>
          </footer>
        </aside>
      </section>

      {selectedServer ? (
        <section className="pb-runtime-rack" aria-label="链路入口服务器运行指标">
          <header className="pb-runtime-identity">
            <span className="pb-runtime-channel">
              {String(selectedServerIndex + 1).padStart(2, '0')}
            </span>
            <div>
              <h2>{selectedServer.alias}</h2>
              <p>
                链路入口 · {channelCode(selectedServer, selectedServerIndex)} ·{' '}
                {selectedServer.location || '位置待补充'}
              </p>
            </div>
            <span
              className={cn(
                'pb-runtime-state',
                isServerOnline(selectedServer) ? 'is-online' : 'is-offline',
              )}
            >
              {serverConnectionLabel(selectedServer.connection_state)}
            </span>
          </header>
          <dl className="pb-runtime-grid">
            {runtimeItems.map((item) => (
              <div key={item.label} className="pb-instrument">
                <dt>
                  <item.icon aria-hidden="true" />
                  {item.label}
                </dt>
                <dd>
                  {item.label === 'Agent 版本' ? item.value : <RollingReadout value={item.value} />}
                </dd>
                {item.level === null ? (
                  <span className="pb-instrument-rule" aria-hidden="true" />
                ) : (
                  <div className="pb-meter" aria-hidden="true">
                    <span style={{ '--pb-meter-level': `${item.level / 100}` } as CSSProperties} />
                  </div>
                )}
                <small>{item.detail}</small>
              </div>
            ))}
          </dl>
        </section>
      ) : null}
    </div>
  )
}
