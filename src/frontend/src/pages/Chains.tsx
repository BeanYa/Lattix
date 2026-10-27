import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  BarChart3Icon,
  ChevronDownIcon,
  ChevronRightIcon,
  CircleCheckIcon,
  Clock3Icon,
  LinkIcon,
  LoaderCircleIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  RouteIcon,
  SearchIcon,
  SendIcon,
  TriangleAlertIcon,
} from 'lucide-react'

import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { isServerOnline } from '@/lib/server-state'
import { chainStatusStyle, hopStatusStyle, type CgStatusStyle } from '@/lib/status'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'
import { cn } from '@/lib/utils'
import type {
  Chain,
  ChainSegmentStat,
  ChainStatsResponse,
  ChainStatus,
  NodeStatus,
  Server,
  XrayNode,
} from '@/lib/types'

import {
  buildChainStatsIndex,
  chainTodayBytes,
  formatLatency,
  latencyTone,
  segmentForLink,
} from './chains/chain-stats'
import { ChainFormDialog } from './chains/ChainFormDialog'
import { ChainOverviewPanel } from './chains/ChainOverviewPanel'
import { ShareLinkDialog } from './chains/ShareLinkDialog'
import { TrafficHistoryDialog } from './chains/TrafficHistoryDialog'
import { useChainForm } from './chains/use-chain-form'
import { useShareLink } from './chains/use-share-link'
import { roleLabel, useTrafficHistory } from './chains/use-traffic-history'

import './chains.css'

type ChainFilter = 'all' | 'active' | 'issue' | 'relay' | 'direct'
type ChainTab = 'overview' | 'details'

function statusTone(tone: CgStatusStyle['cg']): string {
  return tone === 'is-lime' ? 'is-ok' : tone
}

function ChainStateMark({
  status,
  style,
}: {
  status: ChainStatus | NodeStatus
  style: CgStatusStyle
}) {
  const Icon =
    style.cg === 'is-lime' || status === 'active_unconfirmed'
      ? CircleCheckIcon
      : style.loading
        ? LoaderCircleIcon
        : TriangleAlertIcon
  return (
    <span className={cn('cg-chain-mark', style.cg)} title={style.label} aria-label={style.label}>
      <Icon className={cn(style.loading && 'animate-spin motion-reduce:animate-none')} />
    </span>
  )
}

// HopLatency 是跳间连线上的段延迟标注（§链路统计）：直连段由上游探测、反向段由
// 下游 bridge 探测；失败/超时样本保留"超时"字样作连通性参考。
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
      title={`跳间${segment.kind === 'forward' ? '直连' : '反向'}段探测 RTT`}
    >
      {formatLatency(segment.latency_ms)}
    </span>
  )
}

// HopLink 是相邻两跳之间的连线：长箭头 + 段延迟标注。有延迟样本时整条箭头
// （线体 + 箭头头）跟随延迟状态色，与标注一致；无样本保持中性/线路色。
function HopLink({ segment, flowing }: { segment: ChainSegmentStat | null; flowing: boolean }) {
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

function TrafficSummary({
  up,
  down,
  rawUp,
  rawDown,
  multiplier,
  today,
}: {
  up?: number
  down?: number
  rawUp?: number
  rawDown?: number
  multiplier?: string
  today?: number
}) {
  const hasTraffic = up !== undefined && down !== undefined
  const adjusted =
    hasTraffic && rawUp !== undefined && rawDown !== undefined && (rawUp !== up || rawDown !== down)
  return (
    <div className="cg-chain-traffic" aria-label="累计流量">
      <div className="cg-chain-traffic-item is-up">
        <span className="cg-chain-traffic-label">
          <ArrowUpIcon />
          累计上传
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(up ?? 0) : '--'}
        </strong>
        {adjusted ? (
          <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawUp ?? 0)}</span>
        ) : null}
      </div>
      <div className="cg-chain-traffic-item is-down">
        <span className="cg-chain-traffic-label">
          <ArrowDownIcon />
          累计下载
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(down ?? 0) : '--'}
        </strong>
        {adjusted ? (
          <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawDown ?? 0)}</span>
        ) : null}
      </div>
      {today !== undefined ? (
        <div className="cg-chain-traffic-item is-today" aria-label="今日负载">
          <span className="cg-chain-traffic-label">
            <ActivityIcon />
            今日负载
          </span>
          <strong className="cg-chain-traffic-value">
            {today > 0 ? humanizeBytes(today) : '--'}
          </strong>
        </div>
      ) : null}
      {multiplier ? (
        <span className="cg-chain-traffic-multiplier">流量倍率 x{multiplier}</span>
      ) : null}
    </div>
  )
}

export default function Chains() {
  const [location] = useLocation()
  const searchString = useSearch()
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [chainStats, setChainStats] = useState<ChainStatsResponse | null>(null)
  const [panelShort, setPanelShort] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  // 轮询错误单独存放：刷新成功即可自动清除，不覆盖操作类错误横幅。
  const [loadError, setLoadError] = useState('')
  const [retrying, setRetrying] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<ChainFilter>('all')
  // 链路页双 tab：概览（链路一览，默认）/ 详情（链路读数板）。
  const [tab, setTab] = useState<ChainTab>('overview')
  // 链路一览点行定位（URL 深链优先，见 highlightedChainId）。
  const [selectedChainId, setSelectedChainId] = useState<number | null>(null)
  const loadRequest = useRef(0)
  const autoOpenedRoute = useRef('')

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
    try {
      // 链路统计（§链路统计）失败不阻断链路列表：统计位保持旧值展示。
      const [nextChains, nextNodes, nextServers, nextStats] = await Promise.all([
        api.chains(options),
        api.nodes(options),
        api.servers(options),
        api.chainStats(options).catch(() => undefined),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setChains(nextChains)
      setNodes(nextNodes)
      setServers(nextServers)
      if (nextStats !== undefined) setChainStats(nextStats)
      setLoadError('')
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setLoadError(errorMessage(err))
    } finally {
      if (!signal?.aborted && request === loadRequest.current) setLoading(false)
    }
  }, [])

  usePolling(load, loadRequest)

  // 名称模板 {{PANEL_SHORT}} 预览值；读取失败由 naming 层回退默认缩写。
  useEffect(() => {
    api
      .settings()
      .then((s) => setPanelShort(s.panel_short))
      .catch(() => {})
  }, [])

  const chainForm = useChainForm({
    chains,
    nodes,
    servers,
    onError: setError,
    onSaved: load,
    showOperation,
  })
  const traffic = useTrafficHistory({ onError: setError })
  const share = useShareLink()

  const statsIndex = useMemo(() => buildChainStatsIndex(chainStats), [chainStats])
  // 概览点行 → 切到详情 tab 并定位高亮（滚动等详情渲染后由下方 effect 执行）。
  const onSelectChain = (chainId: number) => {
    setSelectedChainId(chainId)
    setTab('details')
  }

  // 支持从命令面板或控制台深链到 /network/chains/new、/network/chains/:id、/network/chains/:id/edit
  const highlightedChainId = useMemo(() => {
    const selParam = new URLSearchParams(searchString).get('sel')
    if (selParam && /^\d+$/.test(selParam)) return Number(selParam)
    const match = location.match(/^\/network\/chains\/(\d+)(?:\/edit)?$/)
    return match ? Number(match[1]) : null
  }, [location, searchString])

  // 深链指向具体链路或新建：详情在概览之后，强制切到详情 tab。
  useEffect(() => {
    if (highlightedChainId !== null || location === '/network/chains/new') setTab('details')
  }, [highlightedChainId, location])

  // 详情渲染完成后滚动定位选中的链路卡。
  useEffect(() => {
    if (tab !== 'details' || selectedChainId === null || loading) return
    document
      .getElementById(`chain-entry-${selectedChainId}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [tab, selectedChainId, loading])

  useEffect(() => {
    if (loading) return
    if (location === '/network/chains/new' && autoOpenedRoute.current !== location) {
      autoOpenedRoute.current = location
      chainForm.openCreate()
      return
    }
    const editMatch = location.match(/^\/network\/chains\/(\d+)\/edit$/)
    if (editMatch && autoOpenedRoute.current !== location) {
      const target = chains.find((c) => c.id === Number(editMatch[1]))
      if (target) {
        autoOpenedRoute.current = location
        chainForm.openEdit(target)
      }
    }
  }, [chains, chainForm, loading, location])

  const onForcePublish = async (chain: Chain) => {
    if (
      !(await confirm({
        title: '强制发布未确认配置',
        description: `链路「${chain.name}」将立即更新订阅，离线 Agent 的任务继续排队。此操作不会自动回滚。`,
        confirmLabel: '强制发布',
        destructive: true,
      }))
    )
      return
    try {
      const { observeId } = await api.forcePublishChain(chain.id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onResetTraffic = async (chain: Chain) => {
    if (
      !(await confirm({
        title: '重置流量统计',
        description: `重置链路「${chain.name}」及各跳当前显示的累计流量？历史原始计数不会回写 Agent。`,
        confirmLabel: '重置',
        destructive: true,
      }))
    )
      return
    try {
      await api.resetChainTraffic(chain.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onRetry = async (id: number) => {
    setRetrying(`relay-${id}`)
    try {
      const { observeId } = await api.retryChain(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRetrying(null)
    }
  }

  const onRetryDirect = async (id: number) => {
    setRetrying(`direct-${id}`)
    try {
      const { observeId } = await api.retryNode(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRetrying(null)
    }
  }

  const onDelete = async (id: number) => {
    const chain = chains.find((c) => c.id === id)
    if (
      !(await confirm({
        title: '删除链路',
        description: `确定删除链路「${chain?.name || `#${id}`}」？将逐跳拆除转发/隧道并删除出口节点。`,
        confirmLabel: '删除链路',
        destructive: true,
      }))
    ) {
      return
    }
    try {
      const { observeId } = await api.deleteChain(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onDeleteDirect = async (id: number) => {
    const node = nodes.find((candidate) => candidate.id === id)
    if (
      !(await confirm({
        title: '删除直连链路',
        description: `确定删除直连链路「${node?.name || `#${id}`}」？将从服务器移除业务入站。`,
        confirmLabel: '删除链路',
        destructive: true,
      }))
    ) {
      return
    }
    try {
      const { observeId } = await api.deleteNode(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const serverOnline = (id: number): boolean => isServerOnline(servers.find((s) => s.id === id))

  const relayExitNodeIds = useMemo(
    () =>
      new Set(
        chains.flatMap((chain) =>
          chain.hops.filter((hop) => hop.role === 'exit').map((hop) => hop.node_id),
        ),
      ),
    [chains],
  )
  const directNodes = useMemo(
    () => nodes.filter((node) => !relayExitNodeIds.has(node.id)),
    [nodes, relayExitNodeIds],
  )
  const entries = useMemo(
    () =>
      [
        ...directNodes.map((node) => ({
          type: 'direct' as const,
          createdAt: node.created_at,
          node,
        })),
        ...chains.map((chain) => ({ type: 'relay' as const, createdAt: chain.created_at, chain })),
      ].toSorted((a, b) => b.createdAt.localeCompare(a.createdAt)),
    [chains, directNodes],
  )

  const filterCounts = useMemo(() => {
    let active = 0
    let issue = 0
    let relay = 0
    let direct = 0
    for (const entry of entries) {
      if (entry.type === 'direct') {
        direct += 1
        if (entry.node.status === 'active') active += 1
        if (entry.node.status === 'failed') issue += 1
      } else {
        const isSingleHop = entry.chain.hops.length === 1
        if (isSingleHop) direct += 1
        else relay += 1
        if (entry.chain.status === 'active') active += 1
        if (
          entry.chain.status === 'failed' ||
          entry.chain.status === 'active_failed' ||
          entry.chain.status === 'degraded' ||
          entry.chain.hops.some((h) => h.status === 'failed')
        ) {
          issue += 1
        }
      }
    }
    return { all: entries.length, active, issue, relay, direct }
  }, [entries])

  const filteredEntries = useMemo(() => {
    const q = query.trim().toLowerCase()
    return entries.filter((entry) => {
      if (entry.type === 'direct') {
        const n = entry.node
        if (filter === 'active' && n.status !== 'active') return false
        if (filter === 'issue' && n.status !== 'failed') return false
        if (filter === 'relay') return false
        if (q) {
          const hay = `${n.name} ${n.server_alias} #${n.id} 直连`.toLowerCase()
          if (!hay.includes(q)) return false
        }
        return true
      }
      const c = entry.chain
      const isDirect = c.hops.length === 1
      const hasIssue =
        c.status === 'failed' ||
        c.status === 'active_failed' ||
        c.status === 'degraded' ||
        c.hops.some((h) => h.status === 'failed')
      if (filter === 'active' && c.status !== 'active') return false
      if (filter === 'issue' && !hasIssue) return false
      if (filter === 'relay' && isDirect) return false
      if (filter === 'direct' && !isDirect) return false
      if (q) {
        const hopsText = c.hops.map((h) => h.server_alias).join(' ')
        const hay = `${c.name} ${hopsText} #${c.id} L${c.id % 9}`.toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [entries, filter, query])

  return (
    <Page className="cg-chains">
      <PageHeader
        title="链路"
        description="直连与中转链路的部署状态、跳点拓扑与累计流量，统一在此编排与维护。"
        actions={
          <button type="button" className="cg-button is-primary" onClick={chainForm.openCreate}>
            <PlusIcon />
            创建链路
          </button>
        }
      />

      {(error || loadError) && <Notice tone="danger">{error || loadError}</Notice>}

      <Tabs value={tab} onValueChange={(value) => value && setTab(value as ChainTab)}>
        <TabsList>
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="details">详情</TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === 'overview' ? (
        <section className="cg-chain-board">
          <header className="cg-chain-board-head">
            <span className="cg-chain-tab">OVERVIEW</span>
            <span className="cg-chain-board-title">链路一览</span>
            <span className="cg-chain-board-meta">
              {loading
                ? '同步中…'
                : `${chains.length} 条链路 · 今日共 ${humanizeBytes(
                    (chainStats?.today_up ?? 0) + (chainStats?.today_down ?? 0),
                  )}`}
            </span>
          </header>
          {loading ? (
            <LoadingState className="cg-board-state" />
          ) : chains.length === 0 ? (
            <EmptyState
              className="cg-board-state"
              icon={<RouteIcon />}
              title="暂无链路"
              description="点击右上角“创建链路”开始"
            />
          ) : (
            <ChainOverviewPanel
              chains={chains}
              index={statsIndex}
              statsLoaded={chainStats !== null}
              onSelect={onSelectChain}
            />
          )}
        </section>
      ) : (
        <section className="cg-chain-board">
          <header className="cg-chain-board-head">
            <span className="cg-chain-tab">ROUTES</span>
            <span className="cg-chain-board-title">链路读数</span>
            <span className="cg-chain-board-meta">
              {loading
                ? '同步中…'
                : filteredEntries.length !== entries.length
                  ? `${filteredEntries.length} / ${entries.length} 条链路`
                  : `${entries.length} 条链路`}
            </span>
          </header>
          {!loading && entries.length > 0 ? (
            <div className="cg-chain-toolbar">
              <div className="cg-search-wrap">
                <SearchIcon />
                <input
                  type="search"
                  className="cg-search-input"
                  placeholder="搜索链路名称、跳点机器、线路编号 L0–L8…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  aria-label="搜索链路"
                />
              </div>
              <div className="cg-filter-pills" role="group" aria-label="链路筛选">
                {(
                  [
                    { id: 'all', label: '全部', count: filterCounts.all },
                    { id: 'active', label: '正常', count: filterCounts.active },
                    { id: 'issue', label: '异常/降级', count: filterCounts.issue },
                    { id: 'relay', label: '中转', count: filterCounts.relay },
                    { id: 'direct', label: '直连', count: filterCounts.direct },
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
          ) : null}
          {loading ? (
            <LoadingState className="cg-board-state" />
          ) : entries.length === 0 ? (
            <EmptyState
              className="cg-board-state"
              icon={<RouteIcon />}
              title="暂无链路"
              description="点击上方“创建链路”开始"
            />
          ) : filteredEntries.length === 0 ? (
            <EmptyState
              className="cg-board-state"
              icon={<RouteIcon />}
              title="未找到匹配的链路"
              description="尝试调整搜索词或切换上方筛选条件"
            />
          ) : (
            <div className="cg-chain-rows">
              {filteredEntries.map((entry) => {
                if (entry.type === 'direct') {
                  const node = entry.node
                  const st = chainStatusStyle[node.status] ?? chainStatusStyle.pending
                  const server = servers.find((candidate) => candidate.id === node.server_id)
                  const displayPort = node.realized_config?.port ?? node.port
                  const lineIdx = node.id % 9
                  return (
                    <article
                      key={`direct-${node.id}`}
                      className="cg-chain-entry"
                      data-tone={st.cg}
                      style={
                        {
                          '--entry-line-color': `var(--panel-line-${lineIdx})`,
                        } as React.CSSProperties
                      }
                    >
                      <header className="cg-chain-entry-head">
                        <div className="cg-chain-entry-title">
                          <span
                            className="cg-line-badge"
                            style={{ background: `var(--panel-line-${lineIdx})` }}
                          >
                            L{lineIdx}
                          </span>
                          <ChainStateMark status={node.status} style={st} />
                          <strong className="cg-chain-name">
                            {node.name || `直连 #${node.id}`}
                          </strong>
                          <span className="cg-chain-id">#{node.id}</span>
                          <span className="cg-chain-tag">直连</span>
                          <span className={cn('cg-status', statusTone(st.cg))}>{st.label}</span>
                        </div>
                        <div className="cg-chain-entry-side">
                          <span className="cg-chain-meta">
                            <Clock3Icon />
                            创建于 {formatDateTime(node.created_at, timezone)}
                          </span>
                          <div className="cg-chain-actions">
                            <Button
                              variant="outline"
                              size="icon-sm"
                              title="导出分享链接"
                              onClick={() =>
                                void share.open({
                                  kind: 'node',
                                  id: node.id,
                                  name: node.name || `直连 #${node.id}`,
                                })
                              }
                            >
                              <LinkIcon />
                            </Button>
                            {node.status === 'failed' ? (
                              <Button
                                variant="outline"
                                size="sm"
                                disabled={retrying === `direct-${node.id}`}
                                onClick={() => onRetryDirect(node.id)}
                              >
                                {retrying === `direct-${node.id}` ? '重试中…' : '重试链路'}
                              </Button>
                            ) : null}
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => onDeleteDirect(node.id)}
                            >
                              删除链路
                            </Button>
                          </div>
                        </div>
                      </header>
                      <div className="cg-chain-entry-body">
                        {node.error ? <p className="cg-chain-error">{node.error}</p> : null}
                        <div className="cg-chain-split">
                          <div className="cg-chain-direct">
                            <span
                              className={cn(
                                'cg-hop-dot',
                                server && isServerOnline(server) ? 'is-lime' : 'is-red',
                              )}
                            />
                            <div className="cg-chain-direct-copy">
                              <span className="cg-chain-direct-label">直连服务器</span>
                              <strong className="cg-chain-direct-name">{node.server_alias}</strong>
                              {displayPort ? (
                                <span className="cg-chain-direct-port">:{displayPort}</span>
                              ) : null}
                            </div>
                            <span
                              className={cn(
                                'cg-status',
                                server ? (isServerOnline(server) ? 'is-ok' : 'is-red') : 'is-muted',
                              )}
                            >
                              {server
                                ? isServerOnline(server)
                                  ? 'Agent 在线'
                                  : 'Agent 离线'
                                : 'Agent 未知'}
                            </span>
                          </div>
                          <TrafficSummary up={node.traffic?.up} down={node.traffic?.down} />
                        </div>
                      </div>
                    </article>
                  )
                }
                const c = entry.chain
                const st = chainStatusStyle[c.status] ?? chainStatusStyle.pending
                const hasFailedHop = c.hops.some((h) => h.status === 'failed')
                const isDirect = c.hops.length === 1
                const lineIdx = c.id % 9
                const activeChainId = highlightedChainId ?? selectedChainId
                const isHighlighted = activeChainId === c.id
                const pendingTasks = c.revision_tasks.filter(
                  (task) => task.status === 'pending' || task.status === 'queued',
                )
                return (
                  <article
                    key={`relay-${c.id}`}
                    id={`chain-entry-${c.id}`}
                    className={cn('cg-chain-entry', isHighlighted && 'is-highlighted')}
                    data-tone={st.cg}
                    style={
                      {
                        '--entry-line-color': `var(--panel-line-${lineIdx})`,
                      } as React.CSSProperties
                    }
                  >
                    <header className="cg-chain-entry-head">
                      <div className="cg-chain-entry-title">
                        <span
                          className="cg-line-badge"
                          style={{ background: `var(--panel-line-${lineIdx})` }}
                        >
                          L{lineIdx}
                        </span>
                        <ChainStateMark status={c.status} style={st} />
                        <strong className="cg-chain-name">{c.name || `中转 #${c.id}`}</strong>
                        <span className="cg-chain-id">#{c.id}</span>
                        <span className="cg-chain-tag">{isDirect ? '直连' : '中转'}</span>
                        <span className={cn('cg-status', statusTone(st.cg))}>{st.label}</span>
                        {c.status === 'degraded' ? (
                          c.endpoint_status === 'failed' ? (
                            <span className="cg-chain-note is-red">
                              共享入口部署失败，已发布链路仍保留
                            </span>
                          ) : c.endpoint_status === 'applying' ||
                            c.endpoint_status === 'pending' ? (
                            <span className="cg-chain-note is-blue">
                              共享入口部署中，已发布链路仍保留
                            </span>
                          ) : (
                            <span className="cg-chain-note is-red">
                              Agent 离线，已发布链路仍保留
                            </span>
                          )
                        ) : null}
                        {c.revision_forced ? (
                          <span className="cg-chain-note is-blue">
                            订阅已发布，配置等待 Agent 确认
                          </span>
                        ) : null}
                      </div>
                      <div className="cg-chain-entry-side">
                        <span className="cg-chain-meta">
                          <Clock3Icon />
                          创建于 {formatDateTime(c.created_at, timezone)}
                        </span>
                        <div className="cg-chain-actions">
                          {c.status === 'failed' || c.status === 'active_failed' || hasFailedHop ? (
                            <Button
                              variant="outline"
                              size="sm"
                              disabled={retrying === `relay-${c.id}`}
                              onClick={() => onRetry(c.id)}
                            >
                              {retrying === `relay-${c.id}` ? '重试中…' : '重试链路'}
                            </Button>
                          ) : null}
                          {c.desired_revision_id !== 0 ? (
                            <Button variant="outline" size="sm" onClick={() => onForcePublish(c)}>
                              <SendIcon />
                              强制发布
                            </Button>
                          ) : null}
                          <Button
                            variant="outline"
                            size="icon-sm"
                            title="流量历史"
                            onClick={() => traffic.openTraffic(c)}
                          >
                            <BarChart3Icon />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon-sm"
                            title="重置流量"
                            onClick={() => onResetTraffic(c)}
                          >
                            <RotateCcwIcon />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon-sm"
                            title="导出分享链接"
                            onClick={() =>
                              void share.open({
                                kind: 'chain',
                                id: c.id,
                                name: c.name || `中转 #${c.id}`,
                                exitNodeId: c.hops.find((h) => h.role === 'exit')?.node_id,
                              })
                            }
                          >
                            <LinkIcon />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon-sm"
                            title="编辑链路"
                            disabled={
                              c.desired_revision_id !== 0 &&
                              c.status !== 'failed' &&
                              c.status !== 'active_failed' &&
                              // 等待 Agent 的编辑可再次编辑取代（无在途命令，废弃安全）
                              c.status !== 'waiting_for_agent'
                            }
                            onClick={() => chainForm.openEdit(c)}
                          >
                            <PencilIcon />
                          </Button>
                          <Button variant="outline" size="sm" onClick={() => onDelete(c.id)}>
                            删除链路
                          </Button>
                        </div>
                      </div>
                    </header>
                    <div className="cg-chain-entry-body">
                      {c.error ? <p className="cg-chain-error">{c.error}</p> : null}
                      <div className="cg-chain-split">
                        <div className="cg-hop-scroll">
                          <div className="cg-hop-flow">
                            {c.hops.map((h, i) => {
                              const hst = hopStatusStyle[h.status] ?? hopStatusStyle.pending
                              const offline = !serverOnline(h.server_id)
                              const exitNode =
                                h.role === 'exit'
                                  ? nodes.find((n) => n.id === h.node_id)
                                  : undefined
                              const hopPort =
                                h.role === 'entry'
                                  ? c.entry_port !== 0
                                    ? c.entry_port
                                    : h.forward_port
                                  : h.role === 'middle'
                                    ? h.forward_port
                                    : (exitNode?.realized_config?.port ??
                                      exitNode?.port ??
                                      h.forward_port)
                              return (
                                <div key={h.id} className="cg-hop-seg" title={h.error || undefined}>
                                  {i > 0 ? (
                                    <HopLink
                                      segment={segmentForLink(statsIndex, c.hops[i - 1].id, h.id)}
                                      flowing={c.status === 'active' && !offline}
                                    />
                                  ) : null}
                                  <div className="cg-hop">
                                    <span className="cg-hop-head">
                                      <span className="cg-hop-num">
                                        {String(i + 1).padStart(2, '0')}
                                      </span>
                                      <span
                                        className={cn('cg-hop-dot', offline ? 'is-red' : hst.cg)}
                                      />
                                      <span className="cg-hop-role">
                                        {roleLabel[h.role]} · {hst.label}
                                      </span>
                                    </span>
                                    <strong className="cg-hop-name">{h.server_alias}</strong>
                                    <span className="cg-hop-meta">
                                      {hopPort !== 0 ? <span>端口 {hopPort}</span> : null}
                                      {(h.role === 'entry' || c.hops.length === 1) &&
                                      c.entry_shared ? (
                                        <span className="cg-hop-shared">共享入口</span>
                                      ) : null}
                                      <span className={offline ? 'is-red' : 'is-lime'}>
                                        {offline ? 'Agent 离线' : 'Agent 在线'}
                                      </span>
                                      {h.traffic ? (
                                        <span>
                                          ↑ {humanizeBytes(h.traffic.effective_up)} · ↓{' '}
                                          {humanizeBytes(h.traffic.effective_down)}
                                        </span>
                                      ) : null}
                                    </span>
                                  </div>
                                </div>
                              )
                            })}
                          </div>
                        </div>
                        <TrafficSummary
                          up={c.traffic?.effective_up}
                          down={c.traffic?.effective_down}
                          rawUp={c.traffic?.raw_up}
                          rawDown={c.traffic?.raw_down}
                          multiplier={c.traffic_multiplier}
                          today={chainTodayBytes(statsIndex.byChain.get(c.id))}
                        />
                      </div>
                      {pendingTasks.length > 0 ? (
                        <p className="cg-chain-queue">
                          {pendingTasks.filter((task) => task.phase === 'apply').length}{' '}
                          个部署任务、
                          {pendingTasks.filter((task) => task.phase === 'cleanup').length}{' '}
                          个清理任务在队列中
                        </p>
                      ) : null}
                      {c.hops.some((h) => h.error) ? (
                        <div className="cg-chain-hop-errors">
                          {c.hops
                            .filter((h) => h.error)
                            .map((h) => (
                              <p key={h.id}>
                                {roleLabel[h.role]}（{h.server_alias}）：{h.error}
                              </p>
                            ))}
                        </div>
                      ) : null}
                    </div>
                  </article>
                )
              })}
            </div>
          )}
        </section>
      )}

      <ChainFormDialog controller={chainForm} servers={servers} panelShort={panelShort} />
      <TrafficHistoryDialog controller={traffic} />
      <ShareLinkDialog controller={share} />
    </Page>
  )
}
