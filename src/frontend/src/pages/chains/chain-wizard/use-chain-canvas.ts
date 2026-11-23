import { useCallback, useMemo, useReducer, useRef, useState } from 'react'
import type { Server } from '@/lib/types'
import {
  CARD_H,
  CARD_W,
  MAX_CANVAS_NODES,
  analyzeCanvasGraph,
  nodeRole,
  toFormTopology,
  type CanvasEdge,
  type CanvasNode,
  type CanvasValidation,
} from './chain-canvas'
import { canvasReducer, createCanvasState } from './chain-canvas-state'
import type { CanvasViewport } from './canvas-viewport'

/** 图状态机与相机互相独立；所有拓扑变更只经过 reducer。 */
export function useChainCanvas({
  servers,
  orchestratedServerIds,
  confirmRemoveOrchestration,
}: {
  servers: Server[]
  /** 已启用编排的跳服务器：删除对应画布节点前提醒会一并删除编排配置。 */
  orchestratedServerIds?: Set<number>
  confirmRemoveOrchestration?: (alias: string) => Promise<boolean>
}) {
  const [state, dispatch] = useReducer(canvasReducer, undefined, createCanvasState)
  const [viewport, setViewport] = useState<CanvasViewport>({ x: 0, y: 0, zoom: 1 })
  const [viewportSize, setViewportSize] = useState({ width: 960, height: 600 })
  const [fitRevision, setFitRevision] = useState(0)
  const fitAppliedRevision = useRef(-1)
  const serverById = useMemo(() => new Map(servers.map((server) => [server.id, server])), [servers])
  const analysis = useMemo(
    () => analyzeCanvasGraph(state.nodes, state.edges, serverById),
    [state.nodes, state.edges, serverById],
  )
  const validation = useMemo<CanvasValidation>(
    () => ({
      valid: analysis.valid,
      issues: analysis.errors,
      warnings: analysis.warnings,
      paths: analysis.paths,
    }),
    [analysis],
  )
  // 名称模板/端口提示等仍以第一条合法链路为基准（多链共享名称基准）。
  const formTopology = useMemo(
    () => toFormTopology(analysis.paths[0] ?? [], serverById),
    [analysis, serverById],
  )
  const issueNodeIds = useMemo(
    () => new Set(analysis.errors.flatMap((issue) => (issue.nodeId ? [issue.nodeId] : []))),
    [analysis],
  )
  const warningNodeIds = useMemo(
    () => new Set(analysis.warnings.flatMap((warning) => (warning.nodeId ? [warning.nodeId] : []))),
    [analysis],
  )
  const addNode = useCallback(
    (serverId: number, x: number, y: number) => {
      if (serverById.has(serverId)) dispatch({ type: 'add', serverId, point: { x, y } })
    },
    [serverById],
  )
  // 点选添加：按世界坐标 3 列网格铺开（避免斜向堆叠），添加后自动适应视图，
  // 保证多节点编辑时所有新节点可见；拖拽添加仍落在指针位置。
  const addVisibleNode = useCallback(
    (serverId: number) => {
      const index = state.nodes.length
      const col = index % 3
      const row = Math.floor(index / 3)
      addNode(serverId, 170 + col * (CARD_W + 66), 150 + row * (CARD_H + 70))
      setFitRevision((revision) => revision + 1)
    },
    [state.nodes.length, addNode],
  )
  // 删除节点：若该跳已启用自动编排，先提示「编排配置将一并删除」。
  const requestRemoveNode = useCallback(
    (id: string) => {
      const node = state.nodes.find((item) => item.id === id)
      if (!node) return
      const alias = serverById.get(node.serverId)?.alias ?? `#${node.serverId}`
      if (orchestratedServerIds?.has(node.serverId) && confirmRemoveOrchestration) {
        void confirmRemoveOrchestration(alias).then((ok) => {
          if (ok) dispatch({ type: 'remove', id })
        })
        return
      }
      dispatch({ type: 'remove', id })
    },
    [state.nodes, serverById, orchestratedServerIds, confirmRemoveOrchestration],
  )
  const removeNode = useCallback((id: string) => dispatch({ type: 'remove', id }), [])
  const replaceNode = useCallback(
    (id: string, serverId: number) => {
      if (serverById.has(serverId)) dispatch({ type: 'replace', id, serverId })
    },
    [serverById],
  )
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
  const addNodeCandidate = useCallback(
    (id: string, serverId: number) => dispatch({ type: 'candidate-add', id, serverId }),
    [],
  )
  const removeNodeCandidate = useCallback(
    (id: string, serverId: number) => dispatch({ type: 'candidate-remove', id, serverId }),
    [],
  )
  const moveNodeCandidate = useCallback(
    (id: string, index: number, direction: -1 | 1) =>
      dispatch({ type: 'candidate-move', id, index, direction }),
    [],
  )
  const exitPortClick = useCallback((from: string) => dispatch({ type: 'connect-start', from }), [])
  const entryPortClick = useCallback((to: string) => dispatch({ type: 'connect-complete', to }), [])
  const moveConnecting = useCallback(
    (x: number, y: number) => dispatch({ type: 'connect-move', point: { x, y } }),
    [],
  )
  const cancelConnect = useCallback(() => dispatch({ type: 'connect-cancel' }), [])
  const edgeClick = useCallback(
    (from: string, to: string) => dispatch({ type: 'disconnect', from, to }),
    [],
  )
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
    analysis,
    validation,
    formTopology,
    issueNodeIds,
    warningNodeIds,
    viewport,
    setViewport,
    viewportSize,
    setViewportSize,
    fitRevision,
    fitAppliedRevision,
    canAddMore: state.nodes.length < MAX_CANVAS_NODES,
    usedCount: (serverId: number) =>
      state.nodes.filter(
        (node) =>
          node.serverId === serverId ||
          (node.candidates ?? []).some((c) => c.serverId === serverId),
      ).length,
    outgoingTargetOf: (id: string) => state.edges.find((edge) => edge.from === id)?.to,
    incomingFromOf: (id: string) => state.edges.find((edge) => edge.to === id)?.from,
    roleOf: (id: string) => nodeRole(state.nodes, state.edges, id),
    rolesOf: (id: string) => analysis.roles.get(id) ?? { entry: false, middle: false, exit: false },
    portRoleOf: (id: string) =>
      analysis.portRoles.get(id) ?? { entry: false, exit: false, editable: false, label: '' },
    paths: analysis.paths,
    addNode,
    addVisibleNode,
    removeNode,
    replaceNode,
    requestRemoveNode,
    moveNode,
    moveTerminal,
    setNodePort,
    setNodeAddress,
    setPortEditingId,
    addNodeCandidate,
    removeNodeCandidate,
    moveNodeCandidate,
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
