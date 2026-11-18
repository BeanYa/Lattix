import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Server } from '@/lib/types'
import { cn } from '@/lib/utils'
import { ChainNodeCard } from '../chain-wizard/ChainNodeCard'
import {
  CARD_BODY_H,
  CARD_H,
  CARD_W,
  ENTRY_PORT_OFFSET_Y,
  PORT_R,
  type CanvasNode,
} from '../chain-wizard/chain-canvas'
import { routeCanvasEdge } from '../chain-wizard/canvas-edge-routing'
import {
  fitViewport,
  panViewport,
  screenToWorld,
  zoomAt,
  type Point,
} from '../chain-wizard/canvas-viewport'
import type { EditorNode, EditorState } from './orchestration-graph'
import { triggerLabel } from './orchestration-graph'

type HopRole = 'entry' | 'middle' | 'exit'

// 触发器与服务器卡片同尺寸：同列对齐、端口圆点落在同一条基线上。
const TRIGGER_W = CARD_W
const TRIGGER_H = CARD_H

function serverAnchor(node: EditorNode, output: boolean) {
  return output
    ? { x: node.x + CARD_W, y: node.y + CARD_BODY_H / 2 }
    : { x: node.x, y: node.y + ENTRY_PORT_OFFSET_Y }
}

function triggerAnchor(node: EditorNode, output: boolean) {
  return output
    ? { x: node.x + TRIGGER_W, y: node.y + CARD_BODY_H / 2 }
    : { x: node.x, y: node.y + CARD_BODY_H / 2 }
}

export function OrchestrationCanvas({
  state,
  hopRole,
  serverMap,
  selected,
  connecting,
  onStartConnect,
  onCompleteConnect,
  onRemove,
  onMove,
  onPriority,
  onPort,
  onAddress,
}: {
  state: EditorState
  hopRole: HopRole
  serverMap: Map<number, Server>
  selected: string | null
  connecting: string | null
  onStartConnect: (key: string) => void
  onCompleteConnect: (key: string) => void
  onRemove: (key: string) => void
  onMove: (key: string, x: number, y: number) => void
  onPriority: (key: string, value: string) => void
  onPort: (key: string, value: string) => void
  onAddress: (key: string, value: string) => void
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [viewport, setViewport] = useState({ x: 40, y: 20, zoom: 1 })
  const [size, setSize] = useState({ width: 900, height: 560 })
  const panRef = useRef<{ pointerId: number; last: Point } | null>(null)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const measure = () => setSize({ width: host.clientWidth, height: host.clientHeight })
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(host)
    return () => observer.disconnect()
  }, [])

  const toWorld = useCallback(
    (clientX: number, clientY: number): Point => {
      const bounds = hostRef.current?.getBoundingClientRect()
      const local = { x: clientX - (bounds?.left ?? 0), y: clientY - (bounds?.top ?? 0) }
      return screenToWorld(local, viewport)
    },
    [viewport],
  )

  const fit = useCallback(() => {
    if (state.nodes.length === 0) return
    const boxes = state.nodes.map((node) => ({
      x: node.x,
      y: node.y,
      width: node.kind === 'server' ? CARD_W : TRIGGER_W,
      height: node.kind === 'server' ? CARD_H : TRIGGER_H,
    }))
    const minX = Math.min(...boxes.map((box) => box.x))
    const minY = Math.min(...boxes.map((box) => box.y))
    const maxX = Math.max(...boxes.map((box) => box.x + box.width))
    const maxY = Math.max(...boxes.map((box) => box.y + box.height))
    const bounds = { x: minX, y: minY, width: maxX - minX, height: maxY - minY }
    const fitted = fitViewport(bounds, size, 32)
    // 保底缩放：整图适配后若过小则维持可读比例，溢出部分靠滚轮/拖拽浏览。
    const zoom = Math.max(fitted.zoom, 0.55)
    setViewport({
      zoom,
      x: (size.width - bounds.width * zoom) / 2 - bounds.x * zoom,
      y: (size.height - bounds.height * zoom) / 2 - bounds.y * zoom,
    })
  }, [state.nodes, size])

  // 宿主尺寸测量完成或节点数量变化时自动适配一次（拖动/连线/平移不打断视角）。
  useEffect(() => {
    if (size.width <= 0 || size.height <= 0) return
    fit()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [size.width, size.height, state.nodes.length])

  const geometry = useMemo(() => {
    const byKey = new Map(state.nodes.map((node) => [node.key, node]))
    const obstacles = state.nodes.map((node) => ({
      x: node.x,
      y: node.y,
      width: node.kind === 'server' ? CARD_W : TRIGGER_W,
      height: node.kind === 'server' ? CARD_H : TRIGGER_H,
    }))
    const routes = new Map<string, { path: string }>()
    for (const edge of state.edges) {
      const from = byKey.get(edge.from)
      const to = byKey.get(edge.to)
      if (!from || !to) continue
      const anchor = (node: EditorNode, output: boolean) => ({
        point: node.kind === 'server' ? serverAnchor(node, output) : triggerAnchor(node, output),
        bounds: {
          x: node.x,
          y: node.y,
          width: node.kind === 'server' ? CARD_W : TRIGGER_W,
          height: node.kind === 'server' ? CARD_H : TRIGGER_H,
        },
      })
      const route = routeCanvasEdge(anchor(from, true), anchor(to, false), obstacles)
      routes.set(edge.from + '->' + edge.to, { path: route.path })
    }
    return routes
  }, [state.nodes, state.edges])

  const localPoint = (event: React.PointerEvent<HTMLDivElement>) => {
    const bounds = hostRef.current?.getBoundingClientRect()
    return { x: event.clientX - (bounds?.left ?? 0), y: event.clientY - (bounds?.top ?? 0) }
  }

  return (
    <div
      ref={hostRef}
      className="orch-canvas"
      onPointerDown={(event) => {
        if (
          event.target !== event.currentTarget &&
          !(event.target as HTMLElement).closest('.orch-canvas-bg')
        )
          return
        panRef.current = { pointerId: event.pointerId, last: localPoint(event) }
        event.currentTarget.setPointerCapture(event.pointerId)
      }}
      onPointerMove={(event) => {
        const pan = panRef.current
        if (!pan || pan.pointerId !== event.pointerId) return
        const point = localPoint(event)
        setViewport((current) =>
          panViewport(current, { x: point.x - pan.last.x, y: point.y - pan.last.y }),
        )
        pan.last = point
      }}
      onPointerUp={() => {
        panRef.current = null
      }}
      onWheel={(event) => {
        event.preventDefault()
        const bounds = hostRef.current?.getBoundingClientRect()
        const local = {
          x: event.clientX - (bounds?.left ?? 0),
          y: event.clientY - (bounds?.top ?? 0),
        }
        setViewport((current) =>
          zoomAt(current, local, current.zoom * (event.deltaY < 0 ? 1.1 : 0.9)),
        )
      }}
    >
      <div className="orch-canvas-bg" aria-hidden="true" />
      <div
        className="cw-world"
        style={{ transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})` }}
      >
        <svg className="cw-wires" width="1" height="1" aria-label="编排连线">
          {state.edges.map((edge) => {
            const route = geometry.get(edge.from + '->' + edge.to)
            if (!route) return null
            return (
              <g key={edge.from + '->' + edge.to}>
                <path className="cw-wire cw-wire-link" d={route.path} />
                <path
                  className="cw-wire-hit"
                  d={route.path}
                  role="button"
                  tabIndex={0}
                  aria-label="断开连线"
                  onClick={(event) => {
                    event.stopPropagation()
                    onRemove(edge.from + '->' + edge.to)
                  }}
                />
              </g>
            )
          })}
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
            return (
              <ChainNodeCard
                key={node.key}
                node={canvasNode}
                server={server}
                role={hopRole}
                roleLabel={
                  node.isRoot
                    ? active
                      ? '根 · 当前使用'
                      : '根 · 原服务器'
                    : active
                      ? '当前使用'
                      : undefined
                }
                portEditable={hopRole === 'entry' || hopRole === 'exit'}
                portLabel={
                  hopRole === 'entry' ? '入口端口' : hopRole === 'exit' ? '出口端口' : undefined
                }
                hasIssue={false}
                isConnecting={Boolean(connecting) && connecting !== node.key}
                portEditing={false}
                outgoingConnected={state.edges.some((edge) => edge.from === node.key)}
                incomingConnected={state.edges.some((edge) => edge.to === node.key)}
                probeRows={[]}
                probeTitle={null}
                toWorld={toWorld}
                onExitPortClick={() => onStartConnect(node.key)}
                onEntryPortClick={() => onCompleteConnect(node.key)}
                onRemove={() => onRemove(node.key)}
                onPortChange={(value) => onPort(node.key, value)}
                onPortEditToggle={() => undefined}
                onAddressChange={(value) => onAddress(node.key, value)}
                onMove={(x, y) => onMove(node.key, x, y)}
              />
            )
          })}

        {state.nodes
          .filter((node) => node.kind === 'trigger')
          .map((node) => (
            <div
              key={node.key}
              className={cn(
                'cw-node',
                'is-middle',
                'orch-trigger',
                selected === node.key && 'is-selected',
              )}
              style={
                {
                  left: node.x,
                  top: node.y,
                  '--cw-card-w': `${TRIGGER_W}px`,
                  '--cw-card-h': `${TRIGGER_H}px`,
                  '--cw-body-h': `${CARD_BODY_H}px`,
                  '--cw-address-h': '24px',
                  '--cw-entry-y': `${CARD_BODY_H / 2}px`,
                  '--cw-exit-y': `${CARD_BODY_H / 2}px`,
                  '--cw-port-r': `${PORT_R}px`,
                } as React.CSSProperties
              }
              onPointerDown={(event) => event.stopPropagation()}
            >
              <div className="cw-node-card orch-trigger-card">
                <div className="cw-node-body">
                  <div className="cw-node-head">
                    <span className="orch-trigger-dot" aria-hidden="true" />
                    <span className="cw-node-alias" title={triggerLabel(node)}>
                      {triggerLabel(node)}
                    </span>
                    <span className="cw-node-role is-middle">
                      {node.subject === 'root'
                        ? '恢复'
                        : node.subject === 'self'
                          ? '本机'
                          : node.subject === 'prev'
                            ? '上一跳'
                            : '下一跳'}
                    </span>
                    <button
                      type="button"
                      className="cw-node-remove"
                      aria-label={`移除触发器 ${triggerLabel(node)}`}
                      onClick={(event) => {
                        event.stopPropagation()
                        onRemove(node.key)
                      }}
                    >
                      ×
                    </button>
                  </div>
                  <div className="cw-node-meta">
                    <span>
                      {node.subject === 'root' ? '当前不在根且根已恢复' : '触发后切换到下游服务器'}
                    </span>
                  </div>
                </div>
                <div className="cw-node-config">
                  <div className="orch-trigger-priority-row">
                    <span className="cw-node-port-label">优先级</span>
                    <input
                      className="orch-trigger-priority-input"
                      value={node.priority}
                      inputMode="numeric"
                      aria-label="触发器优先级"
                      onChange={(event) => onPriority(node.key, event.target.value)}
                    />
                  </div>
                </div>
                <button
                  type="button"
                  className={cn('cw-port', 'cw-port-entry', connecting && 'is-target')}
                  aria-label="触发器入口"
                  title={connecting ? '点击完成连线' : '上游服务器出口连到此处'}
                  onClick={(event) => {
                    event.stopPropagation()
                    onCompleteConnect(node.key)
                  }}
                />
                <button
                  type="button"
                  className="cw-port cw-port-exit"
                  aria-label="触发器出口"
                  title="从此外口连到下游服务器入口"
                  onClick={(event) => {
                    event.stopPropagation()
                    onStartConnect(node.key)
                  }}
                />
              </div>
            </div>
          ))}
      </div>
    </div>
  )
}
