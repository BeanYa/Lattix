import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BanIcon,
  LayoutGridIcon,
  LocateFixedIcon,
  MinusIcon,
  PlusIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  XIcon,
} from 'lucide-react'
import type { CatalogProbeRow, Server } from '@/lib/types'
import { cn } from '@/lib/utils'
import { ChainNodeCard } from '../chain-wizard/ChainNodeCard'
import { CARD_H, CARD_W, type CanvasNode } from '../chain-wizard/chain-canvas'
import { routeCanvasEdge } from '../chain-wizard/canvas-edge-routing'
import {
  fitViewport,
  panViewport,
  screenToWorld,
  zoomAt,
  type CanvasViewport,
  type Point,
} from '../chain-wizard/canvas-viewport'
import { useWorldDrag } from '../chain-wizard/use-world-drag'
import {
  TRIGGER_H,
  TRIGGER_W,
  canConnect,
  nodeSize,
  triggerDesc,
  triggerLabel,
  triggerSource,
  type EditorNode,
  type EditorState,
  type TriggerAction,
  type TriggerSubject,
  type ValidationContext,
} from './orchestration-graph'

type HopRole = 'entry' | 'middle' | 'exit'

export const ORCH_SERVER_DND_MIME = 'text/x-lattix-orch-server'
export const ORCH_TRIGGER_DND_MIME = 'text/x-lattix-orch-trigger'

function nodeAnchor(node: EditorNode, output: boolean): Point {
  if (node.kind === 'server') {
    return output
      ? { x: node.x + CARD_W, y: node.y + CARD_H / 2 }
      : { x: node.x, y: node.y + CARD_H / 2 }
  }
  return output
    ? { x: node.x + TRIGGER_W, y: node.y + TRIGGER_H / 2 }
    : { x: node.x, y: node.y + TRIGGER_H / 2 }
}

function triggerTone(
  node: Pick<EditorNode, 'subject' | 'action'>,
): 'ok' | 'warn' | 'danger' | 'signal' {
  if (node.subject === 'root') return 'ok'
  if (node.action === 'ban_suspected') return 'warn'
  if (node.action === 'ban_banned') return 'danger'
  return 'signal'
}

function TriggerIcon({ subject, action }: Pick<EditorNode, 'subject' | 'action'>) {
  if (subject === 'root') return <RotateCcwIcon aria-hidden />
  if (subject === 'prev') return <ArrowLeftIcon aria-hidden />
  if (subject === 'next') return <ArrowRightIcon aria-hidden />
  if (action === 'ban_banned') return <BanIcon aria-hidden />
  return <ShieldAlertIcon aria-hidden />
}

export function OrchestrationCanvas({
  state,
  hopRole,
  serverMap,
  probesByServer,
  probeTitle,
  validationContext,
  issueNodeKeys,
  connecting,
  connectionError,
  portEditingKey,
  fitRevision,
  onStartConnect,
  onMoveConnect,
  onCompleteConnect,
  onCancelConnect,
  onRemoveNode,
  onRemoveEdge,
  onMove,
  onDropServer,
  onDropTrigger,
  onAutoLayout,
  onPriority,
  onPort,
  onPortEditToggle,
  onAddress,
}: {
  state: EditorState
  hopRole: HopRole
  serverMap: Map<number, Server>
  probesByServer: Map<number, CatalogProbeRow[]>
  probeTitle: string | null
  validationContext: ValidationContext
  issueNodeKeys: Set<string>
  connecting: { from: string; cursor: Point } | null
  connectionError: string
  portEditingKey: string | null
  fitRevision: number
  onStartConnect: (key: string) => void
  onMoveConnect: (x: number, y: number) => void
  onCompleteConnect: (key: string) => void
  onCancelConnect: () => void
  onRemoveNode: (key: string) => void
  onRemoveEdge: (from: string, to: string) => void
  onMove: (key: string, x: number, y: number) => void
  onDropServer: (serverId: number, point: Point) => void
  onDropTrigger: (subject: TriggerSubject, action: TriggerAction, point: Point) => void
  onAutoLayout: () => void
  onPriority: (key: string, value: string) => void
  onPort: (key: string, value: string) => void
  onPortEditToggle: (key: string | null) => void
  onAddress: (key: string, value: string) => void
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [viewport, setViewport] = useState<CanvasViewport>({ x: 40, y: 20, zoom: 1 })
  const [size, setSize] = useState({ width: 0, height: 0 })
  const [panning, setPanning] = useState(false)
  const panRef = useRef<{ pointerId: number; last: Point; distance: number } | null>(null)
  const gesturePointer = useRef<number | null>(null)
  const suppressClick = useRef(false)
  const didInitialFit = useRef(false)
  const appliedFitRevision = useRef(fitRevision)

  const latest = useRef({ viewport, connecting, onMoveConnect })
  latest.current = { viewport, connecting, onMoveConnect }

  const localPoint = useCallback((clientX: number, clientY: number): Point => {
    const host = hostRef.current
    const bounds = host?.getBoundingClientRect()
    return {
      x: clientX - (bounds?.left ?? 0) - (host?.clientLeft ?? 0),
      y: clientY - (bounds?.top ?? 0) - (host?.clientTop ?? 0),
    }
  }, [])

  const toWorld = useCallback(
    (clientX: number, clientY: number): Point =>
      screenToWorld(localPoint(clientX, clientY), latest.current.viewport),
    [localPoint],
  )

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const measure = () => setSize({ width: host.clientWidth, height: host.clientHeight })
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(host)
    // 原生非被动滚轮监听：普通滚轮平移，Ctrl/Meta + 滚轮缩放，阻止页面滚动。
    const onWheel = (event: WheelEvent) => {
      if ((event.target as HTMLElement).closest('input,select,[role="listbox"]')) return
      event.preventDefault()
      const point = localPoint(event.clientX, event.clientY)
      setViewport((current) =>
        event.ctrlKey || event.metaKey
          ? zoomAt(current, point, current.zoom * Math.exp(-event.deltaY * 0.002))
          : panViewport(current, { x: -event.deltaX, y: -event.deltaY }),
      )
    }
    host.addEventListener('wheel', onWheel, { passive: false })
    return () => {
      observer.disconnect()
      host.removeEventListener('wheel', onWheel)
    }
  }, [localPoint])

  const nodeByKey = useMemo(
    () => new Map(state.nodes.map((node) => [node.key, node])),
    [state.nodes],
  )

  const geometry = useMemo(() => {
    const obstacles = state.nodes.map((node) => ({
      x: node.x,
      y: node.y,
      ...nodeSize(node.kind),
    }))
    const endpoint = (key: string, output: boolean) => {
      const node = nodeByKey.get(key)
      if (!node) return null
      return {
        point: nodeAnchor(node, output),
        bounds: { x: node.x, y: node.y, ...nodeSize(node.kind) },
      }
    }
    const routes = new Map<string, ReturnType<typeof routeCanvasEdge>>()
    for (const edge of state.edges) {
      const from = endpoint(edge.from, true)
      const to = endpoint(edge.to, false)
      if (!from || !to) continue
      routes.set(`${edge.from}->${edge.to}`, routeCanvasEdge(from, to, obstacles))
    }
    return { obstacles, endpoint, routes }
  }, [state.nodes, state.edges, nodeByKey])

  const preview = useMemo(() => {
    if (!connecting) return null
    const from = geometry.endpoint(connecting.from, true)
    const point = connecting.cursor
    if (!from || Math.hypot(point.x - from.point.x, point.y - from.point.y) < 20) return null
    const obstacles = geometry.obstacles.filter(
      (box) =>
        !(
          point.x >= box.x &&
          point.x <= box.x + box.width &&
          point.y >= box.y &&
          point.y <= box.y + box.height
        ),
    )
    return routeCanvasEdge(from, { point, bounds: { ...point, width: 0, height: 0 } }, obstacles)
  }, [connecting, geometry])

  const fit = useCallback(() => {
    const width = hostRef.current?.clientWidth || size.width
    const height = hostRef.current?.clientHeight || size.height
    if (state.nodes.length === 0 || width <= 0 || height <= 0) return
    const boxes = [
      ...geometry.obstacles.map((box) => ({
        x: box.x - 24,
        y: box.y - 24,
        width: box.width + 48,
        height: box.height + 48,
      })),
      ...[...geometry.routes.values()].map((route) => route.bounds),
    ]
    const minX = Math.min(...boxes.map((box) => box.x))
    const minY = Math.min(...boxes.map((box) => box.y))
    const maxX = Math.max(...boxes.map((box) => box.x + box.width))
    const maxY = Math.max(...boxes.map((box) => box.y + box.height))
    const bounds = { x: minX, y: minY, width: maxX - minX, height: maxY - minY }
    const fitted = fitViewport(bounds, { width, height }, 40)
    const zoom = Math.max(fitted.zoom, 0.5)
    setViewport({
      zoom,
      x: width / 2 - (bounds.x + bounds.width / 2) * zoom,
      y: height / 2 - (bounds.y + bounds.height / 2) * zoom,
    })
  }, [state.nodes.length, size, geometry])

  const fitRef = useRef(fit)
  fitRef.current = fit

  // 仅在首次尺寸就绪或外部触发（整理布局/重置）时自动适配，不打断手动拖拽与缩放。
  useLayoutEffect(() => {
    if (size.width <= 0 || size.height <= 0) return
    if (!didInitialFit.current || appliedFitRevision.current !== fitRevision) {
      fitRef.current()
      didInitialFit.current = true
      appliedFitRevision.current = fitRevision
    }
  }, [size.width, size.height, fitRevision])

  const zoom = (factor: number) => {
    const host = hostRef.current
    if (!host) return
    setViewport((current) =>
      zoomAt(current, { x: host.clientWidth / 2, y: host.clientHeight / 2 }, current.zoom * factor),
    )
  }

  const endPan = (event: React.PointerEvent<HTMLDivElement>) => {
    if (panRef.current?.pointerId !== event.pointerId) return
    suppressClick.current = panRef.current.distance > 3
    panRef.current = null
    setPanning(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
  }

  const nodeLabel = (key: string) => {
    const node = nodeByKey.get(key)
    if (!node) return key
    if (node.kind === 'trigger') return triggerLabel(node)
    return serverMap.get(node.serverId ?? 0)?.alias ?? `#${node.serverId ?? '?'}`
  }

  return (
    <div
      ref={hostRef}
      className={cn('orch-canvas', panning && 'is-panning')}
      role="region"
      aria-label="自动编排画布"
      tabIndex={0}
      style={{
        backgroundSize: `${24 * viewport.zoom}px ${24 * viewport.zoom}px`,
        backgroundPosition: `${viewport.x}px ${viewport.y}px`,
      }}
      onPointerDownCapture={(event) => {
        if (!event.currentTarget.contains(event.target as Node)) return
        if (gesturePointer.current !== null && gesturePointer.current !== event.pointerId) {
          event.stopPropagation()
          event.preventDefault()
          return
        }
        gesturePointer.current = event.pointerId
      }}
      onPointerUpCapture={(event) => {
        if (gesturePointer.current === event.pointerId) gesturePointer.current = null
      }}
      onPointerCancelCapture={(event) => {
        if (gesturePointer.current === event.pointerId) gesturePointer.current = null
      }}
      onLostPointerCaptureCapture={(event) => {
        if (gesturePointer.current === event.pointerId) gesturePointer.current = null
      }}
      onPointerDown={(event) => {
        if (panRef.current) return
        if (!event.currentTarget.contains(event.target as Node)) return
        if (event.button !== 0 && event.button !== 1) return
        if (
          (event.target as Element).closest(
            '.cw-node,.orch-cond,.cw-view-controls,.cw-connect-hint,.cw-connect-error,button,input,select,[role="button"]',
          )
        ) {
          return
        }
        event.preventDefault()
        event.currentTarget.focus({ preventScroll: true })
        suppressClick.current = false
        panRef.current = {
          pointerId: event.pointerId,
          last: { x: event.clientX, y: event.clientY },
          distance: 0,
        }
        event.currentTarget.setPointerCapture(event.pointerId)
        setPanning(true)
      }}
      onPointerMove={(event) => {
        const active = panRef.current
        if (active?.pointerId === event.pointerId) {
          const delta = { x: event.clientX - active.last.x, y: event.clientY - active.last.y }
          active.last = { x: event.clientX, y: event.clientY }
          active.distance += Math.abs(delta.x) + Math.abs(delta.y)
          setViewport((current) => panViewport(current, delta))
        }
        if (latest.current.connecting) {
          const world = toWorld(event.clientX, event.clientY)
          latest.current.onMoveConnect(world.x, world.y)
        }
      }}
      onPointerUp={endPan}
      onPointerCancel={endPan}
      onLostPointerCapture={endPan}
      onClick={(event) => {
        if (suppressClick.current) {
          suppressClick.current = false
          return
        }
        if (event.target !== event.currentTarget) return
        onCancelConnect()
        onPortEditToggle(null)
      }}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return
        const directions: Record<string, Point> = {
          ArrowLeft: { x: 80, y: 0 },
          ArrowRight: { x: -80, y: 0 },
          ArrowUp: { x: 0, y: 80 },
          ArrowDown: { x: 0, y: -80 },
        }
        if (directions[event.key]) {
          event.preventDefault()
          setViewport((current) => panViewport(current, directions[event.key]))
        }
        if (event.key === '+' || event.key === '=') {
          event.preventDefault()
          zoom(1.2)
        }
        if (event.key === '-') {
          event.preventDefault()
          zoom(1 / 1.2)
        }
        if (event.key === 'Home') {
          event.preventDefault()
          fit()
        }
      }}
      onDragOver={(event) => {
        const types = Array.from(event.dataTransfer.types)
        if (!types.includes(ORCH_SERVER_DND_MIME) && !types.includes(ORCH_TRIGGER_DND_MIME)) return
        event.preventDefault()
        event.dataTransfer.dropEffect = 'copy'
      }}
      onDrop={(event) => {
        const serverRaw = event.dataTransfer.getData(ORCH_SERVER_DND_MIME)
        if (serverRaw) {
          event.preventDefault()
          onDropServer(Number(serverRaw), toWorld(event.clientX, event.clientY))
          return
        }
        const triggerRaw = event.dataTransfer.getData(ORCH_TRIGGER_DND_MIME)
        if (triggerRaw) {
          event.preventDefault()
          try {
            const parsed = JSON.parse(triggerRaw) as {
              subject: TriggerSubject
              action: TriggerAction
            }
            if (parsed.subject && parsed.action) {
              onDropTrigger(parsed.subject, parsed.action, toWorld(event.clientX, event.clientY))
            }
          } catch {
            // ignore invalid drag payload
          }
        }
      }}
    >
      <div
        className="cw-world"
        style={{ transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})` }}
      >
        <svg className="cw-wires" width="1" height="1" aria-label="编排连线">
          {state.edges.map((edge) => {
            const edgeKey = `${edge.from}->${edge.to}`
            const route = geometry.routes.get(edgeKey)
            if (!route) return null
            const toNode = nodeByKey.get(edge.to)
            const isReturn = Boolean(toNode?.kind === 'server' && toNode.isRoot)
            const label = `断开连线：${nodeLabel(edge.from)} → ${nodeLabel(edge.to)}`
            return (
              <g key={edgeKey} className="orch-wire-group">
                <path
                  className={cn(
                    'cw-wire',
                    isReturn ? 'orch-wire-return' : 'orch-wire-forward',
                    connecting?.from === edge.from && 'is-rewiring',
                  )}
                  d={route.path}
                />
                <path
                  className="cw-wire-hit"
                  d={route.path}
                  role="button"
                  tabIndex={0}
                  aria-label={label}
                  onClick={(event) => {
                    event.stopPropagation()
                    onRemoveEdge(edge.from, edge.to)
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      onRemoveEdge(edge.from, edge.to)
                    }
                  }}
                >
                  <title>{label}</title>
                </path>
              </g>
            )
          })}
          {preview ? <path className="cw-wire cw-wire-rubber" d={preview.path} /> : null}
        </svg>

        {state.nodes
          .filter((node) => node.kind === 'server')
          .map((node) => {
            const server = node.serverId ? serverMap.get(node.serverId) : undefined
            const canvasNode: CanvasNode = {
              id: node.key,
              serverId: node.serverId ?? 0,
              x: node.x,
              y: node.y,
              port: node.listenPort,
              address: node.address,
            }
            const active = node.serverId === state.activeServerId
            const targetOk = Boolean(
              connecting && canConnect(state, connecting.from, node.key, validationContext).ok,
            )
            return (
              <ChainNodeCard
                key={node.key}
                node={canvasNode}
                server={server}
                role={node.isRoot || active ? 'entry' : 'middle'}
                roleLabel={
                  node.isRoot
                    ? active
                      ? '根 · 当前'
                      : '根服务器'
                    : active
                      ? '备选 · 当前'
                      : '备选'
                }
                portEditable={hopRole === 'entry' || hopRole === 'exit'}
                portLabel={
                  hopRole === 'entry' ? '入口端口' : hopRole === 'exit' ? '出口端口' : undefined
                }
                portLayout="symmetric"
                removable={!node.isRoot && !active}
                hasIssue={issueNodeKeys.has(node.key)}
                isActive={active}
                isConnecting={targetOk}
                exitConnecting={connecting?.from === node.key}
                portEditing={portEditingKey === node.key}
                outgoingConnected={state.edges.some((edge) => edge.from === node.key)}
                incomingConnected={state.edges.some((edge) => edge.to === node.key)}
                probeRows={node.serverId ? (probesByServer.get(node.serverId) ?? []) : []}
                probeTitle={probeTitle}
                toWorld={toWorld}
                onExitPortClick={() => onStartConnect(node.key)}
                onEntryPortClick={() => onCompleteConnect(node.key)}
                onRemove={() => onRemoveNode(node.key)}
                onPortChange={(value) => onPort(node.key, value)}
                onPortEditToggle={() =>
                  onPortEditToggle(portEditingKey === node.key ? null : node.key)
                }
                onAddressChange={(value) => onAddress(node.key, value)}
                onMove={(x, y) => onMove(node.key, x, y)}
              />
            )
          })}

        {state.nodes
          .filter((node) => node.kind === 'trigger')
          .map((node) => {
            const incomingConnected = state.edges.some((edge) => edge.to === node.key)
            const outgoingConnected = state.edges.some((edge) => edge.from === node.key)
            const sourceNode = connecting ? nodeByKey.get(connecting.from) : undefined
            const targetOk = Boolean(
              connecting && canConnect(state, connecting.from, node.key, validationContext).ok,
            )
            // 从服务器拉线时，仅把「尚无来源服务器」的触发器高亮为主落点（已有所属服务器的仍可点击改连，但不喧宾夺主）。
            const entryTarget =
              targetOk && (sourceNode?.kind === 'server' ? !incomingConnected : true)
            return (
              <OrchestrationTriggerCard
                key={node.key}
                node={node}
                hasIssue={issueNodeKeys.has(node.key)}
                incomingConnected={incomingConnected}
                outgoingConnected={outgoingConnected}
                entryTarget={entryTarget}
                exitConnecting={connecting?.from === node.key}
                toWorld={toWorld}
                onMove={(x, y) => onMove(node.key, x, y)}
                onRemove={() => onRemoveNode(node.key)}
                onPriority={(value) => onPriority(node.key, value)}
                onEntryPortClick={() => onCompleteConnect(node.key)}
                onExitPortClick={() => onStartConnect(node.key)}
              />
            )
          })}
      </div>

      <div className="cw-view-controls" aria-label="画布视角">
        <button
          type="button"
          aria-label="缩小画布"
          onClick={() => zoom(1 / 1.2)}
          disabled={viewport.zoom <= 0.35}
        >
          <MinusIcon />
        </button>
        <span aria-label="当前缩放">{Math.round(viewport.zoom * 100)}%</span>
        <button
          type="button"
          aria-label="放大画布"
          onClick={() => zoom(1.2)}
          disabled={viewport.zoom >= 1.75}
        >
          <PlusIcon />
        </button>
        <button type="button" aria-label="适应全部节点" title="适应全部节点（Home）" onClick={fit}>
          <LocateFixedIcon />
        </button>
        <div className="orch-view-divider" aria-hidden="true" />
        <button type="button" aria-label="整理布局" title="自动整理布局" onClick={onAutoLayout}>
          <LayoutGridIcon />
        </button>
      </div>

      {connecting ? (
        <div className="cw-connect-hint" role="status">
          <span>
            正在从 <strong>{nodeLabel(connecting.from)}</strong> 连线：点击目标入口圆点 · Esc 取消
          </span>
          <button type="button" onClick={onCancelConnect}>
            取消连线
          </button>
        </div>
      ) : null}

      {connectionError ? (
        <div className="cw-connect-error" role="alert">
          {connectionError}
        </div>
      ) : null}
    </div>
  )
}

function OrchestrationTriggerCard({
  node,
  hasIssue,
  incomingConnected,
  outgoingConnected,
  entryTarget,
  exitConnecting,
  toWorld,
  onMove,
  onRemove,
  onPriority,
  onEntryPortClick,
  onExitPortClick,
}: {
  node: EditorNode
  hasIssue: boolean
  incomingConnected: boolean
  outgoingConnected: boolean
  entryTarget: boolean
  exitConnecting: boolean
  toWorld: (clientX: number, clientY: number) => Point
  onMove: (x: number, y: number) => void
  onRemove: () => void
  onPriority: (value: string) => void
  onEntryPortClick: () => void
  onExitPortClick: () => void
}) {
  const drag = useWorldDrag(node, toWorld, onMove)
  const label = triggerLabel(node)
  const isRecovery = node.subject === 'root'

  return (
    <div
      className={cn('orch-cond', hasIssue && 'is-issue', drag.dragging && 'is-dragging')}
      data-tone={triggerTone(node)}
      style={{ left: node.x, top: node.y, width: TRIGGER_W, height: TRIGGER_H }}
      {...drag.handlers}
    >
      <div className="orch-cond-head">
        <span className="orch-cond-icon" aria-hidden="true">
          <TriggerIcon subject={node.subject} action={node.action} />
        </span>
        <span className="orch-cond-name" title={label}>
          {label}
        </span>
        <span className="orch-cond-tag">{triggerSource(node)}</span>
        <button
          type="button"
          className="orch-cond-remove"
          aria-label={`移除触发器 ${label}`}
          onClick={(event) => {
            event.stopPropagation()
            onRemove()
          }}
        >
          <XIcon />
        </button>
      </div>
      <p className="orch-cond-desc" title={triggerDesc(node)}>
        {triggerDesc(node)}
      </p>
      <label className="orch-cond-priority">
        <span>优先级</span>
        <input
          type="text"
          value={node.priority}
          placeholder="自动"
          inputMode="numeric"
          aria-label="触发器优先级"
          title="留空或 0 为自动排序；1-9999 越小越优先"
          onChange={(event) => onPriority(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === 'Escape') {
              event.stopPropagation()
              event.currentTarget.blur()
            }
          }}
        />
      </label>
      {!isRecovery ? (
        <button
          type="button"
          className={cn(
            'orch-port',
            'is-entry',
            incomingConnected && 'is-connected',
            entryTarget && 'is-target',
          )}
          aria-label={`触发器入口：${label}`}
          title={entryTarget ? '点击完成连线' : '上游服务器出口连到此处'}
          onClick={(event) => {
            event.stopPropagation()
            onEntryPortClick()
          }}
        />
      ) : null}
      <button
        type="button"
        className={cn(
          'orch-port',
          'is-exit',
          outgoingConnected && 'is-connected',
          exitConnecting && 'is-connecting',
        )}
        aria-label={`触发器出口：${label}`}
        title={isRecovery ? '连到根服务器入口（恢复时自动切回）' : '点击连到下游替补或根服务器入口'}
        onClick={(event) => {
          event.stopPropagation()
          onExitPortClick()
        }}
      />
    </div>
  )
}
