import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDownIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  BarChart3Icon,
  CircleCheckIcon,
  Clock3Icon,
  LoaderCircleIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  RouteIcon,
  SendIcon,
  TriangleAlertIcon,
} from 'lucide-react'

import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { isServerOnline } from '@/lib/server-state'
import { chainStatusStyle, hopStatusStyle, type CgStatusStyle } from '@/lib/status'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'
import { cn } from '@/lib/utils'
import type { Chain, ChainStatus, NodeStatus, Server, XrayNode } from '@/lib/types'

import { ChainFormDialog } from './chains/ChainFormDialog'
import { TrafficHistoryDialog } from './chains/TrafficHistoryDialog'
import { useChainForm } from './chains/use-chain-form'
import { roleLabel, useTrafficHistory } from './chains/use-traffic-history'

import './chains.css'

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

function TrafficSummary({
  up,
  down,
  rawUp,
  rawDown,
  multiplier,
}: {
  up?: number
  down?: number
  rawUp?: number
  rawDown?: number
  multiplier?: string
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
      {multiplier ? (
        <span className="cg-chain-traffic-multiplier">流量倍率 x{multiplier}</span>
      ) : null}
    </div>
  )
}

export default function Chains() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [panelShort, setPanelShort] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState<string | null>(null)
  const loadRequest = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
    try {
      const [nextChains, nextNodes, nextServers] = await Promise.all([
        api.chains(options),
        api.nodes(options),
        api.servers(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setChains(nextChains)
      setNodes(nextNodes)
      setServers(nextServers)
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(err))
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
  return (
    <Page className="cg-chains">
      <div className="cg-chains-topline">
        <span className="cg-eyebrow">LINKS / ROUTING</span>
        <span className="cg-micro cg-chains-topline-note">入口 → 中转 → 出口 · 客户端仅见入口</span>
      </div>

      <PageHeader
        title="链路"
        description="直连与中转链路的部署状态、跳点拓扑与累计流量，统一在此编排与维护。"
        actions={
          <>
            <span className="cg-pill">{loading ? '同步中…' : `${entries.length} 条链路`}</span>
            <button type="button" className="cg-button is-primary" onClick={chainForm.openCreate}>
              <PlusIcon />
              创建链路
            </button>
          </>
        }
      />

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="cg-chain-list">
        {loading ? (
          <LoadingState />
        ) : entries.length === 0 ? (
          <EmptyState icon={<RouteIcon />} title="暂无链路" description="点击上方“创建链路”开始" />
        ) : (
          entries.map((entry) => {
            if (entry.type === 'direct') {
              const node = entry.node
              const st = chainStatusStyle[node.status] ?? chainStatusStyle.pending
              const server = servers.find((candidate) => candidate.id === node.server_id)
              const displayPort = node.realized_config?.port ?? node.port
              return (
                <article
                  key={`direct-${node.id}`}
                  className="cg-card cg-chain-card"
                  data-tone={st.cg}
                >
                  <header className="cg-chain-card-head">
                    <div className="cg-chain-card-title">
                      <ChainStateMark status={node.status} style={st} />
                      <strong className="cg-chain-name">{node.name || `直连 #${node.id}`}</strong>
                      <span className="cg-chain-id">#{node.id}</span>
                      <span className="cg-chain-tag">直连</span>
                      <span className={cn('cg-status', st.cg)}>{st.label}</span>
                    </div>
                    <div className="cg-chain-card-side">
                      <span className="cg-chain-meta">
                        <Clock3Icon />
                        创建于 {formatDateTime(node.created_at, timezone)}
                      </span>
                      <div className="cg-chain-actions">
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
                        <Button variant="outline" size="sm" onClick={() => onDeleteDirect(node.id)}>
                          删除链路
                        </Button>
                      </div>
                    </div>
                  </header>
                  <div className="cg-chain-card-body">
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
                            server ? (isServerOnline(server) ? 'is-lime' : 'is-red') : 'is-muted',
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
            const pendingTasks = c.revision_tasks.filter(
              (task) => task.status === 'pending' || task.status === 'queued',
            )
            return (
              <article key={`relay-${c.id}`} className="cg-card cg-chain-card" data-tone={st.cg}>
                <header className="cg-chain-card-head">
                  <div className="cg-chain-card-title">
                    <ChainStateMark status={c.status} style={st} />
                    <strong className="cg-chain-name">{c.name || `中转 #${c.id}`}</strong>
                    <span className="cg-chain-id">#{c.id}</span>
                    <span className="cg-chain-tag">{isDirect ? '直连' : '中转'}</span>
                    <span className={cn('cg-status', st.cg)}>{st.label}</span>
                    {c.status === 'degraded' ? (
                      c.endpoint_status === 'failed' ? (
                        <span className="cg-chain-note is-red">
                          共享入口部署失败，已发布链路仍保留
                        </span>
                      ) : c.endpoint_status === 'applying' || c.endpoint_status === 'pending' ? (
                        <span className="cg-chain-note is-blue">
                          共享入口部署中，已发布链路仍保留
                        </span>
                      ) : (
                        <span className="cg-chain-note is-red">Agent 离线，已发布链路仍保留</span>
                      )
                    ) : null}
                    {c.revision_forced ? (
                      <span className="cg-chain-note is-blue">订阅已发布，配置等待 Agent 确认</span>
                    ) : null}
                  </div>
                  <div className="cg-chain-card-side">
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
                        title="编辑链路"
                        disabled={
                          c.desired_revision_id !== 0 &&
                          c.status !== 'failed' &&
                          c.status !== 'active_failed'
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
                <div className="cg-chain-card-body">
                  {c.error ? <p className="cg-chain-error">{c.error}</p> : null}
                  <div className="cg-chain-split">
                    <div className="cg-hop-scroll">
                      <div className="cg-hop-flow">
                        {c.hops.map((h, i) => {
                          const hst = hopStatusStyle[h.status] ?? hopStatusStyle.pending
                          const offline = !serverOnline(h.server_id)
                          const exitNode =
                            h.role === 'exit' ? nodes.find((n) => n.id === h.node_id) : undefined
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
                                <span className="cg-hop-link" aria-hidden="true">
                                  <i />
                                  <ArrowDownIcon className="cg-hop-link-v" />
                                  <ArrowRightIcon className="cg-hop-link-h" />
                                </span>
                              ) : null}
                              <div className="cg-hop">
                                <span className="cg-hop-head">
                                  <span className="cg-hop-num">
                                    {String(i + 1).padStart(2, '0')}
                                  </span>
                                  <span className={cn('cg-hop-dot', offline ? 'is-red' : hst.cg)} />
                                  <span className="cg-hop-role">
                                    {roleLabel[h.role]} · {hst.label}
                                  </span>
                                </span>
                                <strong className="cg-hop-name">{h.server_alias}</strong>
                                <span className="cg-hop-meta">
                                  {hopPort !== 0 ? <span>端口 {hopPort}</span> : null}
                                  {(h.role === 'entry' || c.hops.length === 1) && c.entry_shared ? (
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
                    />
                  </div>
                  {pendingTasks.length > 0 ? (
                    <p className="cg-chain-queue">
                      {pendingTasks.filter((task) => task.phase === 'apply').length} 个部署任务、
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
          })
        )}
      </div>

      <ChainFormDialog controller={chainForm} servers={servers} panelShort={panelShort} />
      <TrafficHistoryDialog controller={traffic} />
    </Page>
  )
}
