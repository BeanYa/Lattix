import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { LocateFixedIcon, MailIcon, MinusIcon, PlusIcon } from 'lucide-react'
import type { CatalogProbeRow } from '@/lib/types'
import { cn } from '@/lib/utils'
import {
  CARD_H,
  CARD_W,
  INGRESS_NODE_ID,
  INTERNET_NODE_ID,
  INTERNET_R,
  entryPortPoint,
  exitPortPoint,
  portEditableRole,
} from './chain-canvas'
import { fitViewport, panViewport, screenToWorld, zoomAt, type Point } from './canvas-viewport'
import { routeCanvasEdge } from './canvas-edge-routing'
import { CanvasTerminal } from './CanvasTerminal'
import { ChainNodeCard } from './ChainNodeCard'
import type { ChainCanvasController } from './use-chain-canvas'

const DND_MIME = 'text/x-lattix-server'

export function ChainCanvas({
  controller,
  probesByServer,
  probeTitle,
  linkColor,
}: {
  controller: ChainCanvasController
  probesByServer: Map<number, CatalogProbeRow[]>
  probeTitle: string | null
  linkColor: string
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const panRef = useRef<{ pointerId: number; last: Point; distance: number } | null>(null)
  const suppressClick = useRef(false)
  const gesturePointer = useRef<number | null>(null)
  const [panning, setPanning] = useState(false)
  const [motionVisible, setMotionVisible] = useState(true)
  const { nodes, edges, viewport, setViewport, ingressPos, internetPos, connecting } = controller
  const latest = useRef(controller)
  latest.current = controller
  const localPoint = (clientX: number, clientY: number) => {
    const bounds = hostRef.current?.getBoundingClientRect()
    return {
      x: clientX - (bounds?.left ?? 0) - (hostRef.current?.clientLeft ?? 0),
      y: clientY - (bounds?.top ?? 0) - (hostRef.current?.clientTop ?? 0),
    }
  }
  const toWorld = (x: number, y: number) => screenToWorld(localPoint(x, y), latest.current.viewport)
  const geometry = useMemo(() => {
    const obstacles = [
      ...nodes.map((node) => ({ x: node.x, y: node.y, width: CARD_W, height: CARD_H })),
      ...[ingressPos, internetPos].map((point) => ({
        x: point.x - INTERNET_R,
        y: point.y - INTERNET_R,
        width: INTERNET_R * 2,
        height: INTERNET_R * 2 + 24,
      })),
    ]
    const endpoint = (id: string, output: boolean) => {
      if (id === INGRESS_NODE_ID)
        return {
          point: { x: ingressPos.x + INTERNET_R, y: ingressPos.y },
          bounds: obstacles[nodes.length],
        }
      if (id === INTERNET_NODE_ID)
        return {
          point: { x: internetPos.x - INTERNET_R, y: internetPos.y },
          bounds: obstacles[nodes.length + 1],
        }
      const index = nodes.findIndex((node) => node.id === id)
      return index >= 0
        ? {
            point: output ? exitPortPoint(nodes[index]) : entryPortPoint(nodes[index]),
            bounds: obstacles[index],
          }
        : null
    }
    const routes = new Map(
      edges.flatMap((edge) => {
        const from = endpoint(edge.from, true),
          to = endpoint(edge.to, false)
        return from && to ? [[edge.from, routeCanvasEdge(from, to, obstacles)] as const] : []
      }),
    )
    return { obstacles, endpoint, routes }
  }, [nodes, edges, ingressPos, internetPos])
  const preview = useMemo(() => {
    if (!connecting) return null
    const from = geometry.endpoint(connecting.from, true),
      point = connecting.cursor
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
  const sceneBounds = () => {
    const boxes = [
      ...geometry.obstacles.map((box) => ({
        x: box.x - 12,
        y: box.y - 12,
        width: box.width + 24,
        height: box.height + 24,
      })),
      ...[...geometry.routes.values()].map((route) => route.bounds),
    ]
    const x = Math.min(...boxes.map((box) => box.x)),
      y = Math.min(...boxes.map((box) => box.y))
    return {
      x,
      y,
      width: Math.max(...boxes.map((box) => box.x + box.width)) - x,
      height: Math.max(...boxes.map((box) => box.y + box.height)) - y,
    }
  }
  const fit = () => {
    const host = hostRef.current
    if (host)
      setViewport(
        fitViewport(sceneBounds(), { width: host.clientWidth, height: host.clientHeight }, 36),
      )
  }
  const fitRef = useRef(fit)
  fitRef.current = fit
  useLayoutEffect(() => {
    if (controller.fitAppliedRevision.current === controller.fitRevision) return
    fitRef.current()
    controller.fitAppliedRevision.current = controller.fitRevision
  }, [controller.fitRevision, controller.fitAppliedRevision])
  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let intersecting = true
    const update = () => setMotionVisible(intersecting && document.visibilityState !== 'hidden')
    const observer = new IntersectionObserver(([entry]) => {
      intersecting = entry.isIntersecting
      update()
    })
    observer.observe(host)
    document.addEventListener('visibilitychange', update)
    update()
    return () => {
      observer.disconnect()
      document.removeEventListener('visibilitychange', update)
    }
  }, [])
  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const measure = () =>
      latest.current.setViewportSize({ width: host.clientWidth, height: host.clientHeight })
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(host)
    // Native non-passive listener is required to prevent the page from scrolling while navigating the scene.
    const wheel = (event: WheelEvent) => {
      if ((event.target as HTMLElement).closest('input,select')) return
      event.preventDefault()
      const point = localPoint(event.clientX, event.clientY)
      setViewport((current) =>
        event.ctrlKey || event.metaKey
          ? zoomAt(current, point, current.zoom * Math.exp(-event.deltaY * 0.002))
          : panViewport(current, { x: -event.deltaX, y: -event.deltaY }),
      )
    }
    host.addEventListener('wheel', wheel, { passive: false })
    return () => {
      observer.disconnect()
      host.removeEventListener('wheel', wheel)
    }
  }, [setViewport])
  const isConnecting = connecting !== null
  useEffect(() => {
    if (!isConnecting) return
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') latest.current.cancelConnect()
    }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [isConnecting])
  const endPan = (event: React.PointerEvent<HTMLDivElement>) => {
    if (panRef.current?.pointerId !== event.pointerId) return
    suppressClick.current = panRef.current.distance > 3
    panRef.current = null
    setPanning(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId)
  }
  const zoom = (factor: number) => {
    const host = hostRef.current
    if (host)
      setViewport((current) =>
        zoomAt(
          current,
          { x: host.clientWidth / 2, y: host.clientHeight / 2 },
          current.zoom * factor,
        ),
      )
  }
  const name = (id: string) =>
    id === INGRESS_NODE_ID
      ? '入网流量'
      : id === INTERNET_NODE_ID
        ? 'Internet'
        : (controller.serverById.get(nodes.find((n) => n.id === id)?.serverId ?? -1)?.alias ??
          '服务器')

  return (
    <div
      ref={hostRef}
      className={cn('cw-canvas', panning && 'is-panning', !motionVisible && 'is-motion-paused')}
      role="region"
      aria-label="链路拓扑画布"
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
            '.cw-node,.cw-internet,button,input,select,[role="button"]',
          )
        )
          return
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
        if (connecting) {
          const p = toWorld(event.clientX, event.clientY)
          controller.moveConnecting(p.x, p.y)
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
        controller.cancelConnect()
        controller.setPortEditingId(null)
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
          setViewport((v) => panViewport(v, directions[event.key]))
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
        if (!Array.from(event.dataTransfer.types).includes(DND_MIME)) return
        event.preventDefault()
        event.dataTransfer.dropEffect = controller.canAddMore ? 'copy' : 'none'
      }}
      onDrop={(event) => {
        const raw = event.dataTransfer.getData(DND_MIME)
        if (!raw) return
        event.preventDefault()
        const point = toWorld(event.clientX, event.clientY)
        controller.addNode(Number(raw), point.x, point.y)
      }}
    >
      <div
        className="cw-world"
        style={{ transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})` }}
      >
        <svg className="cw-wires" width="1" height="1" aria-label="拓扑连线">
          {edges.map((edge) => {
            const r = geometry.routes.get(edge.from)
            if (!r) return null
            const label = `断开连线：${name(edge.from)} → ${name(edge.to)}`
            return (
              <g key={edge.from}>
                <path
                  className={cn(
                    'cw-wire',
                    edge.from === INGRESS_NODE_ID ? 'cw-wire-entry' : 'cw-wire-link',
                    connecting?.from === edge.from && 'is-rewiring',
                  )}
                  style={edge.from === INGRESS_NODE_ID ? undefined : { stroke: linkColor }}
                  d={r.path}
                />
                <path
                  className="cw-wire-hit"
                  d={r.path}
                  role="button"
                  tabIndex={0}
                  aria-label={label}
                  onClick={(event) => {
                    event.stopPropagation()
                    controller.edgeClick(edge.from)
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      controller.edgeClick(edge.from)
                    }
                  }}
                >
                  <title>{label}</title>
                </path>
                <g
                  aria-hidden="true"
                  className={cn('cw-wire-packet', connecting?.from === edge.from && 'is-rewiring')}
                  style={{
                    offsetPath: `path("${r.path}")`,
                    color: edge.from === INGRESS_NODE_ID ? 'var(--panel-signal)' : linkColor,
                  }}
                >
                  <rect x={-10} y={-8} width={20} height={16} rx={3} />
                  <MailIcon x={-10} y={-10} width={20} height={20} />
                </g>
              </g>
            )
          })}
          {preview && <path className="cw-wire cw-wire-rubber" d={preview.path} />}
        </svg>
        <CanvasTerminal
          kind="ingress"
          position={ingressPos}
          connected={Boolean(controller.outgoingTargetOf(INGRESS_NODE_ID))}
          connecting={connecting?.from === INGRESS_NODE_ID}
          toWorld={toWorld}
          onMove={(x, y) => controller.moveTerminal(INGRESS_NODE_ID, x, y)}
          onPortClick={() => controller.exitPortClick(INGRESS_NODE_ID)}
        />
        {nodes.map((node) => {
          const role = controller.roleOf(node.id)
          return (
            <ChainNodeCard
              key={node.id}
              node={node}
              server={controller.serverById.get(node.serverId)}
              role={role}
              portEditable={portEditableRole(role)}
              hasIssue={controller.issueNodeIds.has(node.id)}
              isConnecting={Boolean(connecting && connecting.from !== node.id)}
              portEditing={controller.portEditingId === node.id}
              outgoingConnected={Boolean(controller.outgoingTargetOf(node.id))}
              incomingConnected={Boolean(controller.incomingFromOf(node.id))}
              probeRows={probesByServer.get(node.serverId) ?? []}
              probeTitle={probeTitle}
              toWorld={toWorld}
              onExitPortClick={() => controller.exitPortClick(node.id)}
              onEntryPortClick={() => controller.entryPortClick(node.id)}
              onRemove={() => controller.removeNode(node.id)}
              onPortChange={(port) => controller.setNodePort(node.id, port)}
              onPortEditToggle={() =>
                controller.setPortEditingId(controller.portEditingId === node.id ? null : node.id)
              }
              onAddressChange={(address) => controller.setNodeAddress(node.id, address)}
              onMove={(x, y) => controller.moveNode(node.id, x, y)}
            />
          )
        })}
        <CanvasTerminal
          kind="internet"
          position={internetPos}
          connected={Boolean(controller.incomingFromOf(INTERNET_NODE_ID))}
          connecting={Boolean(connecting)}
          toWorld={toWorld}
          onMove={(x, y) => controller.moveTerminal(INTERNET_NODE_ID, x, y)}
          onPortClick={() => controller.entryPortClick(INTERNET_NODE_ID)}
        />
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
      </div>
      {connecting && (
        <div className="cw-connect-hint" role="status">
          选择下游入口端口 · Esc 取消
          <button type="button" onClick={controller.cancelConnect}>
            取消连线
          </button>
        </div>
      )}
      {controller.connectionError && (
        <div className="cw-connect-error" role="alert">
          {controller.connectionError}
        </div>
      )}
    </div>
  )
}
