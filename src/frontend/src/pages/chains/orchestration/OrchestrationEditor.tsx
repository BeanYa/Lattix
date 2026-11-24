import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BanIcon,
  CheckCircle2Icon,
  EraserIcon,
  PlusIcon,
  RotateCcwIcon,
  SearchIcon,
  ShieldAlertIcon,
  TriangleAlertIcon,
} from 'lucide-react'
import type { CatalogProbeRow, Chain, ChainHop, Server } from '@/lib/types'
import { api } from '@/lib/api'
import { usePolling } from '@/lib/use-polling'
import { AppDialogContext } from '@/lib/app-dialog'
import { isServerOnline, isServerUpdating, serverConnectionLabel } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import { CountryFlag } from '@/components/CountryFlag'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { CARD_H, CARD_W, isExternalServer } from '../chain-wizard/chain-canvas'
import { ServerProbeMini } from '../chain-wizard/ServerProbeMini'
import { externalExitSummary } from '../external-exit'
import {
  TRIGGERS,
  TRIGGER_H,
  TRIGGER_W,
  addServer,
  addTrigger,
  autoLayout,
  canConnect,
  clearGraph,
  connect,
  createEditorState,
  moveNode,
  removeEdge,
  removeNode,
  serverEligibility,
  setPriority,
  setServerAddress,
  setServerListenPort,
  toSavePayload,
  triggerSource,
  validateGraphIssues,
  type EditorState,
  type TriggerAction,
  type TriggerSubject,
  type ValidationContext,
} from './orchestration-graph'
import {
  ORCH_SERVER_DND_MIME,
  ORCH_TRIGGER_DND_MIME,
  OrchestrationCanvas,
} from './OrchestrationCanvas'
// 与链路编辑器同源：卡片、端口圆点、世界层与连线样式全部复用链路画布样式表。
import '../chain-wizard/chain-wizard.css'
import './orchestration.css'

const HOP_ROLE_TEXT = {
  entry: '入口跳',
  middle: '中转跳',
  exit: '出口跳',
} as const

function stateFingerprint(state: EditorState): string {
  return JSON.stringify({
    nodes: state.nodes.map((node) => ({
      kind: node.kind,
      serverId: node.serverId ?? 0,
      address: node.address,
      listenPort: node.listenPort,
      subject: node.subject ?? '',
      action: node.action ?? '',
      priority: Number(node.priority) || 0,
      isRoot: Boolean(node.isRoot),
    })),
    edges: state.edges,
  })
}

function triggerTone(
  subject: TriggerSubject,
  action: TriggerAction,
): 'ok' | 'warn' | 'danger' | 'signal' {
  if (subject === 'root') return 'ok'
  if (action === 'ban_suspected') return 'warn'
  if (action === 'ban_banned') return 'danger'
  return 'signal'
}

function PaletteTriggerIcon({
  subject,
  action,
}: {
  subject: TriggerSubject
  action: TriggerAction
}) {
  if (subject === 'root') return <RotateCcwIcon aria-hidden />
  if (subject === 'prev') return <ArrowLeftIcon aria-hidden />
  if (subject === 'next') return <ArrowRightIcon aria-hidden />
  if (action === 'ban_banned') return <BanIcon aria-hidden />
  return <ShieldAlertIcon aria-hidden />
}

function initialHopPort(chain: Chain, hop: ChainHop): string {
  if (hop.forward_port > 0) return String(hop.forward_port)
  if (hop.seq === 0 && chain.entry_port > 0) return String(chain.entry_port)
  return ''
}

export function OrchestrationEditorCore({
  chain: chainProp,
  hop: hopProp,
  servers: serversProp,
  probesByServer: probesProp,
  probeTitle = null,
  onClose,
  onSaved,
  config,
  runtimeError = '',
}: {
  chain: Chain
  hop: ChainHop
  servers?: Server[]
  probesByServer?: Map<number, CatalogProbeRow[]>
  probeTitle?: string | null
  onClose: () => void
  onSaved: (updated: Chain) => void
  runtimeError?: string
  config?: {
    initialState: EditorState
    title: string
    saveLabel: string
    onApply: (state: EditorState) => void | Promise<void>
    actions?: ReactNode
    templateMode?: boolean
    structureLocked?: boolean
    extraDirty?: boolean
    onParameterChange?: (serverId: number, field: 'address' | 'listen_port', value: string) => void
  }
}) {
  const dialog = useContext(AppDialogContext)
  const chain = chainProp
  const hop = hopProp
  const initialState = useMemo(
    () =>
      config?.initialState ??
      createEditorState({
        chainId: chain.id,
        seq: hop.seq,
        activeServerId: hop.server_id,
        orchestration: hop.orchestration,
        initialAddress: hop.address ?? '',
        initialListenPort: initialHopPort(chain, hop),
      }),
    [chain, hop, config?.initialState],
  )
  const initialConfigFingerprint = useRef(stateFingerprint(initialState))
  const panelRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    return () => {
      if (previous?.isConnected) previous.focus()
    }
  }, [])
  const initialFingerprint = useMemo(() => stateFingerprint(initialState), [initialState])

  const [state, setState] = useState<EditorState>(() => initialState)
  // 运行节点由服务端推进；只同步当前标记，保留用户尚未保存的图和布局。
  useEffect(() => {
    setState((current) =>
      current.activeServerId === hop.server_id
        ? current
        : { ...current, activeServerId: hop.server_id },
    )
  }, [hop.server_id])
  const [fetchedServers, setFetchedServers] = useState<Server[]>([])
  const [tab, setTab] = useState<'trigger' | 'server'>('trigger')
  const [serverQuery, setServerQuery] = useState('')
  const [connecting, setConnecting] = useState<{
    from: string
    cursor: { x: number; y: number }
  } | null>(null)
  const [connectionError, setConnectionError] = useState('')
  const [portEditingKey, setPortEditingKey] = useState<string | null>(null)
  const [fitRevision, setFitRevision] = useState(0)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const confirmingCloseRef = useRef(false)

  useEffect(() => {
    if (serversProp) return
    let cancelled = false
    api
      .servers()
      .then((list) => {
        if (!cancelled) setFetchedServers(list)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [serversProp])

  const servers = serversProp ?? fetchedServers
  const probesByServer = useMemo(
    () => probesProp ?? new Map<number, CatalogProbeRow[]>(),
    [probesProp],
  )
  const serverMap = useMemo(() => new Map(servers.map((server) => [server.id, server])), [servers])

  const blockedServerIds = useMemo(() => {
    const blocked = new Set<number>()
    for (const other of chain.hops) {
      if (other.seq === hop.seq) continue
      blocked.add(other.server_id)
      for (const node of other.orchestration?.nodes ?? []) {
        if (node.kind === 'server' && node.server_id) blocked.add(node.server_id)
      }
    }
    return blocked
  }, [chain.hops, hop.seq])

  const primaryIsExternal = useMemo(() => {
    if (hop.role === 'exit' && chain.service_external != null) return true
    const rootSrv = serverMap.get(state.rootServerId)
    return rootSrv ? isExternalServer(rootSrv) : false
  }, [hop.role, chain.service_external, serverMap, state.rootServerId])

  const draftMode = Boolean(config)
  const validationContext = useMemo<ValidationContext>(
    () => ({
      validateRootParameters: draftMode,
      seq: config?.templateMode ? undefined : hop.seq,
      totalHops: config?.templateMode ? undefined : chain.hops.length,
      hopRole: config?.templateMode ? undefined : hop.role,
      primaryIsExternal,
      serverMap,
      blockedServerIds: config?.templateMode ? undefined : blockedServerIds,
    }),
    [
      hop.seq,
      hop.role,
      chain.hops.length,
      primaryIsExternal,
      serverMap,
      blockedServerIds,
      config?.templateMode,
      draftMode,
    ],
  )

  const issues = useMemo(
    () => validateGraphIssues(state, validationContext),
    [state, validationContext],
  )
  const issueNodeKeys = useMemo(() => {
    const keys = new Set<string>()
    for (const issue of issues) {
      for (const key of issue.nodeKeys ?? []) keys.add(key)
    }
    return keys
  }, [issues])

  const isDirty = useMemo(
    () =>
      Boolean(config?.extraDirty) ||
      stateFingerprint(state) !== (config ? initialConfigFingerprint.current : initialFingerprint),
    [state, initialFingerprint, config],
  )

  const requestClose = useCallback(async () => {
    if (confirmingCloseRef.current) return
    if (!isDirty || !dialog) {
      onClose()
      return
    }
    confirmingCloseRef.current = true
    try {
      const ok = await dialog.confirm({
        title: '放弃未保存的编排修改？',
        description: '当前编排图有尚未保存的更改，关闭后将丢失这些修改。',
        confirmLabel: '放弃修改',
        cancelLabel: '继续编辑',
        destructive: true,
      })
      if (ok) onClose()
    } finally {
      confirmingCloseRef.current = false
    }
  }, [isDirty, dialog, onClose])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (confirmingCloseRef.current) return
      if (
        document.querySelector(
          '[data-slot="select-content"][data-open], [data-slot="dialog-content"][data-open]',
        )
      ) {
        return
      }
      if (connecting) {
        event.preventDefault()
        setConnecting(null)
        setConnectionError('')
        return
      }
      if (portEditingKey) {
        event.preventDefault()
        setPortEditingKey(null)
        return
      }
      void requestClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [connecting, portEditingKey, requestClose])

  const lastSeq = chain.hops.length - 1
  const hasRecovery = state.nodes.some((node) => node.subject === 'root')
  const chainDeploying = !config && (chain.desired_revision_id !== 0 || chain.status === 'applying')

  const serverItems = useMemo(() => {
    const keyword = serverQuery.trim().toLowerCase()
    const filtered = keyword
      ? servers.filter((server) =>
          `${server.alias} ${server.location} ${server.country_code}`
            .toLowerCase()
            .includes(keyword),
        )
      : servers
    return filtered
      .map((server) => ({
        server,
        eligibility: serverEligibility(server, state, {
          hopRole: config?.templateMode ? 'exit' : hop.role,
          primaryIsExternal:
            config?.templateMode && !state.nodes.length
              ? isExternalServer(server)
              : primaryIsExternal,
          blockedServerIds: config?.templateMode ? undefined : blockedServerIds,
        }),
      }))
      .sort((a, b) => {
        if (a.eligibility.eligible !== b.eligibility.eligible) {
          return a.eligibility.eligible ? -1 : 1
        }
        return a.server.alias.localeCompare(b.server.alias)
      })
  }, [
    servers,
    serverQuery,
    state,
    hop.role,
    primaryIsExternal,
    blockedServerIds,
    config?.templateMode,
  ])

  const eligibleServerCount = useMemo(
    () => serverItems.filter((item) => item.eligibility.eligible).length,
    [serverItems],
  )

  const rootAlias =
    hop.orchestration?.nodes.find((node) => node.is_root)?.alias ??
    serverMap.get(state.rootServerId)?.alias ??
    hop.server_alias
  const activeAlias =
    hop.orchestration?.nodes.find((node) => node.active)?.alias ??
    serverMap.get(state.activeServerId)?.alias ??
    hop.server_alias

  const nodeAnchorPoint = useCallback(
    (key: string) => {
      const node = state.nodes.find((item) => item.key === key)
      if (!node) return { x: 0, y: 0 }
      return node.kind === 'server'
        ? { x: node.x + CARD_W, y: node.y + CARD_H / 2 }
        : { x: node.x + TRIGGER_W, y: node.y + TRIGGER_H / 2 }
    },
    [state.nodes],
  )

  const addServerNode = (serverId: number, point?: { x: number; y: number }) => {
    if (config?.structureLocked) return
    const target = serverMap.get(serverId)
    if (target) {
      const eligibility = serverEligibility(target, state, {
        hopRole: config?.templateMode ? 'exit' : hop.role,
        primaryIsExternal:
          config?.templateMode && !state.nodes.length && target
            ? isExternalServer(target)
            : primaryIsExternal,
        blockedServerIds: config?.templateMode ? undefined : blockedServerIds,
      })
      if (!eligibility.eligible) {
        setError(`无法添加 ${target.alias}：${eligibility.reason}`)
        return
      }
    }
    const next =
      config?.templateMode && state.nodes.length === 0
        ? createEditorState({
            chainId: 0,
            seq: 0,
            activeServerId: serverId,
            initialAddress: '',
            initialListenPort: '0',
          })
        : addServer(state, serverId, point)
    if (next === state) {
      setError('无法添加该服务器（重复或已达上限）')
      return
    }
    setError('')
    setState(next)
    if (state.nodes.length === 0) setFitRevision((n) => n + 1)
  }

  const triggerAvailability = (subject: TriggerSubject): { disabled: boolean; reason?: string } => {
    if (config?.structureLocked) return { disabled: true, reason: '共享结构只读' }
    if (config?.templateMode && state.nodes.length === 0)
      return { disabled: true, reason: '请先选择根服务器' }
    if (!config?.templateMode && subject === 'prev' && hop.seq === 0)
      return { disabled: true, reason: '入口跳无上一跳' }
    if (!config?.templateMode && subject === 'next' && hop.seq === lastSeq) {
      return { disabled: true, reason: '出口跳无下一跳' }
    }
    if (subject === 'root' && hasRecovery) return { disabled: true, reason: '已添加（限 1 个）' }
    if (subject === 'self' && hop.role === 'exit' && primaryIsExternal) {
      return { disabled: true, reason: '外部直链不可用' }
    }
    return { disabled: false }
  }

  const addTriggerNode = (
    subject: TriggerSubject,
    action: TriggerAction,
    point?: { x: number; y: number },
  ) => {
    const avail = triggerAvailability(subject)
    if (avail.disabled) {
      setError(avail.reason ?? '当前跳位不可添加该触发器')
      return
    }
    const next = addTrigger(state, { subject, action }, point)
    if (next === state) {
      setError('无法添加触发器（已达上限）')
      return
    }
    setError('')
    setState(next)
  }

  const startConnect = (key: string) => {
    if (config?.structureLocked) return
    setPortEditingKey(null)
    setConnectionError('')
    setError('')
    setConnecting((current) => {
      if (current?.from === key) return null
      return { from: key, cursor: nodeAnchorPoint(key) }
    })
  }

  const moveConnect = useCallback((x: number, y: number) => {
    setConnecting((current) => (current ? { ...current, cursor: { x, y } } : null))
  }, [])

  const cancelConnect = useCallback(() => {
    setConnecting(null)
    setConnectionError('')
  }, [])

  const completeConnect = (key: string) => {
    if (config?.structureLocked) return
    if (!connecting) return
    if (connecting.from === key) {
      setConnecting(null)
      setConnectionError('')
      return
    }
    const check = canConnect(state, connecting.from, key, validationContext)
    if (!check.ok) {
      if (check.reason === '连线已存在') {
        setConnecting(null)
        setConnectionError('')
        return
      }
      setConnectionError(check.reason ?? '无法连接这两个节点')
      return
    }
    const next = connect(state, connecting.from, key, validationContext)
    setConnectionError('')
    setError('')
    setState(next)
    setConnecting(null)
  }

  const handleRemoveNode = (key: string) => {
    if (config?.structureLocked) return
    const next = removeNode(state, key)
    if (next === state) {
      setError('根服务器与当前使用的服务器不能删除')
      return
    }
    setError('')
    setConnectionError('')
    if (connecting?.from === key) setConnecting(null)
    if (portEditingKey === key) setPortEditingKey(null)
    setState(next)
  }

  const handleRemoveEdge = (from: string, to: string) => {
    if (config?.structureLocked) return
    setError('')
    setConnectionError('')
    setState((current) => removeEdge(current, from, to))
  }

  const handleClearGraph = () => {
    if (config?.structureLocked) return
    setError('')
    setConnectionError('')
    setConnecting(null)
    setPortEditingKey(null)
    setState((current) => clearGraph(current))
    setFitRevision((n) => n + 1)
  }

  const handleAutoLayout = () => {
    setState((current) => autoLayout(current))
    setFitRevision((n) => n + 1)
  }

  const save = async () => {
    if (issues.length > 0 || chainDeploying) return
    setSaving(true)
    setError('')
    try {
      if (config) await config.onApply(state)
      else {
        const updated = await api.saveHopOrchestration(toSavePayload(state))
        onSaved(updated.data)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const serverCount = state.nodes.filter((node) => node.kind === 'server').length
  const triggerCount = state.nodes.filter((node) => node.kind === 'trigger').length
  const hasOrchestrationContent = serverCount > 1 || triggerCount > 0 || state.edges.length > 0

  return createPortal(
    <div
      className="orch-overlay"
      role="presentation"
      onClick={(event) => {
        if (event.target === event.currentTarget) void requestClose()
      }}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        className="orch-panel"
        role="dialog"
        aria-modal="true"
        aria-label={config?.title ?? '自动编排编辑器'}
        onKeyDown={(event) => {
          if (event.key !== 'Tab') return
          const scope = panelRef.current?.querySelector('[role="alertdialog"]') ?? panelRef.current
          const controls = Array.from(
            scope?.querySelectorAll<HTMLElement>(
              'button:not(:disabled),input:not(:disabled),select:not(:disabled),summary,[tabindex="0"]',
            ) ?? [],
          )
          const first = controls[0],
            last = controls.at(-1)
          if (!first) {
            event.preventDefault()
            return
          }
          if (
            !scope?.contains(document.activeElement) ||
            document.activeElement === scope ||
            (event.shiftKey && document.activeElement === first)
          ) {
            event.preventDefault()
            ;(event.shiftKey ? last : first)?.focus()
          } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault()
            first.focus()
          }
        }}
      >
        <header className="orch-head">
          <div className="orch-head-copy">
            <div className="orch-head-title">
              <h2>{config?.title ?? `自动编排 · 第 ${hop.seq + 1} 跳`}</h2>
              {!config?.templateMode ? (
                <span className="orch-head-pill">{HOP_ROLE_TEXT[hop.role]}</span>
              ) : null}
              {!config && hop.orchestration?.triggered ? (
                <span className="orch-head-pill is-triggered">编排已触发</span>
              ) : null}
              {chainDeploying ? (
                <span className="orch-head-pill is-warn">链路部署中，暂不可保存</span>
              ) : null}
            </div>
            {!config?.templateMode ? (
              <p className="orch-head-meta">
                <span>{chain.name}</span>
                <span>·</span>
                <span>原服务器：{rootAlias}</span>
                <span>·</span>
                <span>当前使用：{activeAlias}</span>
              </p>
            ) : (
              <p className="orch-head-meta">地址在链路实例中填写 · 端口 0 为自动分配</p>
            )}
          </div>
          <div className="orch-head-actions">
            {config?.actions}
            {hasOrchestrationContent && !config?.structureLocked ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={handleClearGraph}
                title="清空全部替补与触发器（保存后删除该跳编排）"
              >
                <EraserIcon />
                清空编排
              </Button>
            ) : null}
            <Button type="button" variant="outline" onClick={() => void requestClose()}>
              取消
            </Button>
            <Button
              type="button"
              disabled={
                saving ||
                issues.length > 0 ||
                chainDeploying ||
                Boolean(config?.templateMode && !state.nodes.length)
              }
              title={
                chainDeploying
                  ? '链路有在途修订，暂不能保存自动编排'
                  : issues.length > 0
                    ? '请先解决下方校验问题'
                    : undefined
              }
              onClick={() => void save()}
            >
              {saving ? '保存中…' : (config?.saveLabel ?? '保存编排')}
            </Button>
          </div>
        </header>

        <div className="orch-body">
          <aside className="orch-side" aria-label="节点面板">
            <div className="orch-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={tab === 'trigger'}
                className={cn(tab === 'trigger' && 'is-active')}
                onClick={() => setTab('trigger')}
              >
                触发动作
                <span className="orch-tab-count">{TRIGGERS.length}</span>
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={tab === 'server'}
                className={cn(tab === 'server' && 'is-active')}
                onClick={() => setTab('server')}
              >
                备选服务器
                <span className="orch-tab-count">{eligibleServerCount}</span>
              </button>
            </div>

            {tab === 'trigger' ? (
              <div className="orch-side-pane">
                <p className="orch-side-hint">点击或拖入画布；连线顺序为 服务器 → 触发器 → 替补</p>
                <div className="orch-list">
                  {TRIGGERS.map((trigger) => {
                    const avail = triggerAvailability(trigger.subject)
                    const tone = triggerTone(trigger.subject, trigger.action)
                    return (
                      <button
                        key={`${trigger.subject}/${trigger.action}`}
                        type="button"
                        className="orch-list-item orch-trigger-item"
                        data-tone={tone}
                        disabled={avail.disabled}
                        draggable={!avail.disabled}
                        onDragStart={(event) => {
                          event.dataTransfer.setData(
                            ORCH_TRIGGER_DND_MIME,
                            JSON.stringify({ subject: trigger.subject, action: trigger.action }),
                          )
                          event.dataTransfer.effectAllowed = 'copy'
                        }}
                        onClick={() => addTriggerNode(trigger.subject, trigger.action)}
                      >
                        <div className="orch-list-item-head">
                          <span className="orch-cond-icon" aria-hidden="true">
                            <PaletteTriggerIcon subject={trigger.subject} action={trigger.action} />
                          </span>
                          <strong>{trigger.label}</strong>
                          <span className="orch-cond-tag">
                            {avail.disabled
                              ? avail.reason
                              : triggerSource({ subject: trigger.subject })}
                          </span>
                        </div>
                        <span className="orch-list-item-desc">{trigger.desc}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
            ) : (
              <div className="orch-side-pane">
                <div className="orch-search">
                  <SearchIcon aria-hidden="true" />
                  <Input
                    value={serverQuery}
                    onChange={(event) => setServerQuery(event.target.value)}
                    placeholder="搜索服务器"
                    aria-label="搜索备选服务器"
                  />
                </div>
                <p className="orch-side-hint">拖入或点击添加到画布；同一服务器每条链仅可用一次</p>
                <div className="orch-list">
                  {serverItems.length === 0 ? <p className="orch-empty">没有匹配的服务器</p> : null}
                  {serverItems.map(({ server, eligibility }) => {
                    const online = isServerOnline(server)
                    const external = isExternalServer(server)
                    return (
                      <button
                        key={server.id}
                        type="button"
                        className="orch-list-item orch-server-item"
                        disabled={!eligibility.eligible || config?.structureLocked}
                        draggable={eligibility.eligible && !config?.structureLocked}
                        onDragStart={(event) => {
                          event.dataTransfer.setData(ORCH_SERVER_DND_MIME, String(server.id))
                          event.dataTransfer.effectAllowed = 'copy'
                        }}
                        onClick={() => addServerNode(server.id)}
                      >
                        <div className="orch-list-item-head">
                          <CountryFlag code={server.country_code} />
                          <strong title={server.alias}>{server.alias}</strong>
                          {eligibility.eligible ? (
                            <span className="orch-server-add" aria-hidden="true">
                              <PlusIcon />
                            </span>
                          ) : (
                            <span className="orch-server-reason">{eligibility.reason}</span>
                          )}
                        </div>
                        <div className="orch-server-meta">
                          <i
                            className={cn(
                              'orch-server-dot',
                              isServerUpdating(server)
                                ? 'is-updating'
                                : online
                                  ? 'is-online'
                                  : 'is-offline',
                            )}
                          />
                          <span>
                            {server.location || server.country_code || '未填写地区'} ·{' '}
                            {external
                              ? '外部直链'
                              : `托管 · ${serverConnectionLabel(server.connection_state)}`}
                          </span>
                        </div>
                        {external ? (
                          <span className="orch-list-item-desc">
                            {server.external_exit
                              ? externalExitSummary(server.external_exit)
                              : '外部直链配置缺失'}
                          </span>
                        ) : (
                          <ServerProbeMini
                            server={server}
                            probeRows={probesByServer.get(server.id) ?? []}
                            probeTitle={probeTitle}
                          />
                        )}
                      </button>
                    )
                  })}
                </div>
              </div>
            )}
          </aside>

          {config?.templateMode && !state.nodes.length ? (
            <p className="orch-root-prompt" role="status">
              选择根服务器：在「备选服务器」中点击或拖入服务器开始编排
            </p>
          ) : null}
          <OrchestrationCanvas
            state={state}
            templateMode={config?.templateMode}
            structureLocked={config?.structureLocked}
            parametersEditable={Boolean(config)}
            hopRole={hop.role}
            serverMap={serverMap}
            probesByServer={probesByServer}
            probeTitle={probeTitle}
            validationContext={validationContext}
            issueNodeKeys={issueNodeKeys}
            connecting={connecting}
            connectionError={connectionError}
            portEditingKey={portEditingKey}
            fitRevision={fitRevision}
            onStartConnect={startConnect}
            onMoveConnect={moveConnect}
            onCompleteConnect={completeConnect}
            onCancelConnect={cancelConnect}
            onRemoveNode={handleRemoveNode}
            onRemoveEdge={handleRemoveEdge}
            onMove={(key, x, y) => setState((current) => moveNode(current, key, x, y))}
            onDropServer={(serverId, point) => addServerNode(serverId, point)}
            onDropTrigger={(subject, action, point) => addTriggerNode(subject, action, point)}
            onAutoLayout={handleAutoLayout}
            onPriority={(key, value) => {
              if (!config?.structureLocked) setState((current) => setPriority(current, key, value))
            }}
            onPort={(key, value) => {
              const node = state.nodes.find((item) => item.key === key)
              if (node?.serverId) config?.onParameterChange?.(node.serverId, 'listen_port', value)
              setState((current) => setServerListenPort(current, key, value))
            }}
            onPortEditToggle={setPortEditingKey}
            onAddress={(key, value) => {
              if (config?.templateMode) return
              const node = state.nodes.find((item) => item.key === key)
              if (node?.serverId) config?.onParameterChange?.(node.serverId, 'address', value)
              setState((current) => setServerAddress(current, key, value))
            }}
          />
        </div>

        <footer className="orch-foot">
          <div className="orch-errors" role="status">
            {error || runtimeError ? (
              <p className="is-error">
                <TriangleAlertIcon aria-hidden="true" />
                <span>{error || runtimeError}</span>
              </p>
            ) : null}
            {issues.length > 0 ? (
              <ul aria-label="校验错误">
                {issues.slice(0, 3).map((item) => (
                  <li key={item.message}>
                    <TriangleAlertIcon aria-hidden="true" />
                    <span>{item.message}</span>
                  </li>
                ))}
                {issues.length > 3 ? (
                  <li className="is-more">另有 {issues.length - 3} 项待处理问题</li>
                ) : null}
              </ul>
            ) : !error && !runtimeError ? (
              <p className="is-ok">
                <CheckCircle2Icon aria-hidden="true" />
                <span>
                  {hasOrchestrationContent
                    ? `校验通过 · ${serverCount} 台服务器 · ${triggerCount} 个触发器`
                    : '未配置自动编排（保持默认单机运行）'}
                </span>
              </p>
            ) : null}
          </div>
          <span className="orch-hint">
            拖动空白平移 · Ctrl/⌘ + 滚轮缩放 · 点击出口圆点连线 · 点击连线断开
          </span>
        </footer>
      </div>
    </div>,
    document.body,
  )
}

export function OrchestrationEditor(props: {
  chain: Chain
  hop: ChainHop
  servers?: Server[]
  probesByServer?: Map<number, CatalogProbeRow[]>
  probeTitle?: string | null
  onClose: () => void
  onSaved: (updated: Chain) => void
}) {
  const chainProp = props.chain
  const hopProp = props.hop
  const [chain, setChain] = useState(chainProp)
  const [runtimeError, setRuntimeError] = useState('')
  const runtimeRequest = useRef(0)
  const hop = chain.hops.find((item) => item.seq === hopProp.seq) ?? hopProp

  useEffect(() => setChain(chainProp), [chainProp])
  const loadRuntime = useCallback(
    async (silent: boolean, signal?: AbortSignal) => {
      const request = ++runtimeRequest.current
      try {
        const chains = await api.chains({ display: silent ? 'silent' : 'foreground', signal })
        if (signal?.aborted || request !== runtimeRequest.current) return
        const latest = chains.find((item) => item.id === chainProp.id)
        if (!latest || !latest.hops.some((item) => item.seq === hopProp.seq)) {
          throw new Error('链路或跳位已不存在，请关闭编辑器并刷新链路')
        }
        setChain(latest)
        setRuntimeError('')
      } catch (err) {
        if (signal?.aborted || request !== runtimeRequest.current) return
        setRuntimeError(err instanceof Error ? err.message : '运行状态刷新失败')
      }
    },
    [chainProp.id, hopProp.seq],
  )
  usePolling(loadRuntime, runtimeRequest)

  return <OrchestrationEditorCore {...props} chain={chain} hop={hop} runtimeError={runtimeError} />
}
