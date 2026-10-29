import { useCallback, useMemo, useReducer, useRef, useState } from 'react'
import type { Server } from '@/lib/types'
import {
  MAX_HOPS,
  nodeRole,
  toFormTopology,
  validateTopology,
  walkPath,
  type CanvasEdge,
  type CanvasNode,
} from './chain-canvas'
import { canvasReducer, createCanvasState } from './chain-canvas-state'
import { screenToWorld, type CanvasViewport } from './canvas-viewport'

/** 图状态机与相机互相独立；所有拓扑变更只经过 reducer。 */
export function useChainCanvas({ servers }: { servers: Server[] }) {
  const [state, dispatch] = useReducer(canvasReducer, undefined, createCanvasState)
  const [viewport, setViewport] = useState<CanvasViewport>({ x: 0, y: 0, zoom: 1 })
  const [viewportSize, setViewportSize] = useState({ width: 960, height: 600 })
  const [fitRevision, setFitRevision] = useState(0)
  const fitAppliedRevision = useRef(-1)
  const serverById = useMemo(() => new Map(servers.map((server) => [server.id, server])), [servers])
  const walk = useMemo(() => walkPath(state.nodes, state.edges), [state.nodes, state.edges])
  const validation = useMemo(
    () => validateTopology(state.nodes, state.edges, serverById),
    [state.nodes, state.edges, serverById],
  )
  const formTopology = useMemo(() => toFormTopology(walk.order), [walk])
  const issueNodeIds = useMemo(
    () => new Set(validation.issues.flatMap((issue) => (issue.nodeId ? [issue.nodeId] : []))),
    [validation],
  )
  const addNode = useCallback(
    (serverId: number, x: number, y: number) => {
      if (serverById.has(serverId)) dispatch({ type: 'add', serverId, point: { x, y } })
    },
    [serverById],
  )
  const addVisibleNode = useCallback(
    (serverId: number) => {
      const point = screenToWorld(
        { x: viewportSize.width / 2, y: viewportSize.height / 2 },
        viewport,
      )
      const offset = state.nodes.length * 24
      addNode(serverId, point.x + offset, point.y + offset)
    },
    [viewport, viewportSize, state.nodes.length, addNode],
  )
  const removeNode = useCallback((id: string) => dispatch({ type: 'remove', id }), [])
  const moveNode = useCallback(
    (id: string, x: number, y: number) => dispatch({ type: 'move', id, point: { x, y } }),
    [],
  )
  const moveTerminal = useCallback(
    (id: string, x: number, y: number) => dispatch({ type: 'terminal-move', id, point: { x, y } }),
    [],
  )
  const setNodePort = useCallback(
    (id: string, value: string) => dispatch({ type: 'port', id, value }),
    [],
  )
  const setNodeAddress = useCallback(
    (id: string, value: string) => dispatch({ type: 'address', id, value }),
    [],
  )
  const setPortEditingId = useCallback(
    (id: string | null) => dispatch({ type: 'port-edit', id }),
    [],
  )
  const exitPortClick = useCallback((from: string) => dispatch({ type: 'connect-start', from }), [])
  const entryPortClick = useCallback((to: string) => dispatch({ type: 'connect-complete', to }), [])
  const moveConnecting = useCallback(
    (x: number, y: number) => dispatch({ type: 'connect-move', point: { x, y } }),
    [],
  )
  const cancelConnect = useCallback(() => dispatch({ type: 'connect-cancel' }), [])
  const edgeClick = useCallback((from: string) => dispatch({ type: 'disconnect', from }), [])
  const reset = useCallback(() => {
    dispatch({ type: 'reset' })
    setFitRevision((n) => n + 1)
  }, [])
  const prefill = useCallback((nodes: CanvasNode[], edges: CanvasEdge[]) => {
    dispatch({ type: 'prefill', nodes, edges })
    setFitRevision((n) => n + 1)
  }, [])
  return {
    ...state,
    serverById,
    walk,
    validation,
    formTopology,
    issueNodeIds,
    viewport,
    setViewport,
    viewportSize,
    setViewportSize,
    fitRevision,
    fitAppliedRevision,
    canAddMore: state.nodes.length < MAX_HOPS,
    usedCount: (serverId: number) =>
      state.nodes.filter((node) => node.serverId === serverId).length,
    outgoingTargetOf: (id: string) => state.edges.find((edge) => edge.from === id)?.to,
    incomingFromOf: (id: string) => state.edges.find((edge) => edge.to === id)?.from,
    roleOf: (id: string) => nodeRole(state.nodes, state.edges, id),
    addNode,
    addVisibleNode,
    removeNode,
    moveNode,
    moveTerminal,
    setNodePort,
    setNodeAddress,
    setPortEditingId,
    exitPortClick,
    entryPortClick,
    moveConnecting,
    cancelConnect,
    edgeClick,
    reset,
    prefill,
  }
}
export type ChainCanvasController = ReturnType<typeof useChainCanvas>
