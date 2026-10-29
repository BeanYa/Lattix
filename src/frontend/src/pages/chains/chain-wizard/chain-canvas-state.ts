import {
  CARD_H,
  CARD_W,
  INGRESS_NODE_ID,
  INGRESS_POS,
  INTERNET_NODE_ID,
  INTERNET_POS,
  INTERNET_R,
  MAX_HOPS,
  exitPortPoint,
  type CanvasNode,
  type CanvasEdge,
} from './chain-canvas'

type Point = { x: number; y: number }
export interface ConnectingState {
  from: string
  cursor: Point
}
export interface CanvasState {
  nodes: CanvasNode[]
  edges: CanvasEdge[]
  connecting: ConnectingState | null
  portEditingId: string | null
  ingressPos: Point
  internetPos: Point
  nextNodeId: number
  connectionError: string
}
export type CanvasAction =
  | { type: 'add'; serverId: number; point: Point }
  | { type: 'remove'; id: string }
  | { type: 'move'; id: string; point: Point }
  | { type: 'terminal-move'; id: string; point: Point }
  | { type: 'port'; id: string; value: string }
  | { type: 'address'; id: string; value: string }
  | { type: 'port-edit'; id: string | null }
  | { type: 'connect-start'; from: string }
  | { type: 'connect-move'; point: Point }
  | { type: 'connect-complete'; to: string }
  | { type: 'connect-cancel' }
  | { type: 'disconnect'; from: string }
  | { type: 'reset' }
  | { type: 'prefill'; nodes: CanvasNode[]; edges: CanvasEdge[] }

export function createCanvasState(): CanvasState {
  return {
    nodes: [],
    edges: [{ from: INGRESS_NODE_ID, to: INTERNET_NODE_ID }],
    connecting: null,
    portEditingId: null,
    ingressPos: { ...INGRESS_POS },
    internetPos: { ...INTERNET_POS },
    nextNodeId: 1,
    connectionError: '',
  }
}
const finite = (point: Point) => Number.isFinite(point.x) && Number.isFinite(point.y)

function connectionError(state: CanvasState, from: string, to: string): string {
  if (from !== INGRESS_NODE_ID && !state.nodes.some((n) => n.id === from))
    return '请选择有效的上游出口'
  if (from === to) return '不能连接到节点自身'
  if (to === INGRESS_NODE_ID || (to !== INTERNET_NODE_ID && !state.nodes.some((n) => n.id === to)))
    return '请选择服务器入口或 Internet 入口'
  if (from === INGRESS_NODE_ID && to === INTERNET_NODE_ID && state.nodes.length > 0)
    return '请先将入网流量连接到服务器'
  // Cycle check precedes occupancy so cycles receive a meaningful error as well.
  const seen = new Set<string>()
  let cursor: string | undefined = to
  while (cursor && !seen.has(cursor)) {
    if (cursor === from) return '该连接会形成环路，请选择下游节点'
    seen.add(cursor)
    cursor = state.edges.find((e) => e.from === cursor)?.to
  }
  if (state.edges.some((e) => e.to === to && e.from !== from))
    return '该入口已有连接，请先断开原连线'
  return ''
}

/** 图编辑的唯一写入口。预览与已提交边分离，取消/失败不修改任何边。 */
export function canvasReducer(state: CanvasState, action: CanvasAction): CanvasState {
  switch (action.type) {
    case 'add': {
      if (state.nodes.length >= MAX_HOPS || !finite(action.point)) return state
      const id = `n${state.nextNodeId}`
      const node: CanvasNode = {
        id,
        serverId: action.serverId,
        x: action.point.x - CARD_W / 2,
        y: action.point.y - CARD_H / 2,
        port: '',
        address: '',
      }
      return {
        ...state,
        nodes: [...state.nodes, node],
        nextNodeId: state.nextNodeId + 1,
        edges: state.nodes.length
          ? state.edges
          : [
              { from: INGRESS_NODE_ID, to: id },
              { from: id, to: INTERNET_NODE_ID },
            ],
        connectionError: '',
      }
    }
    case 'remove': {
      const nodes = state.nodes.filter((n) => n.id !== action.id)
      return {
        ...state,
        nodes,
        edges: nodes.length
          ? state.edges.filter((e) => e.from !== action.id && e.to !== action.id)
          : createCanvasState().edges,
        connecting: state.connecting?.from === action.id ? null : state.connecting,
        portEditingId: state.portEditingId === action.id ? null : state.portEditingId,
        connectionError: '',
      }
    }
    case 'move':
      return finite(action.point)
        ? {
            ...state,
            nodes: state.nodes.map((n) => (n.id === action.id ? { ...n, ...action.point } : n)),
          }
        : state
    case 'terminal-move': {
      if (!finite(action.point)) return state
      if (action.id === INGRESS_NODE_ID) return { ...state, ingressPos: action.point }
      if (action.id === INTERNET_NODE_ID) return { ...state, internetPos: action.point }
      return state
    }
    case 'port':
      return {
        ...state,
        nodes: state.nodes.map((n) => (n.id === action.id ? { ...n, port: action.value } : n)),
      }
    case 'address':
      return {
        ...state,
        nodes: state.nodes.map((n) => (n.id === action.id ? { ...n, address: action.value } : n)),
      }
    case 'port-edit':
      return { ...state, portEditingId: action.id }
    case 'connect-start': {
      const node = state.nodes.find((n) => n.id === action.from)
      if (action.from !== INGRESS_NODE_ID && !node) return state
      if (state.connecting?.from === action.from)
        return { ...state, connecting: null, connectionError: '' }
      const cursor = node
        ? exitPortPoint(node)
        : { x: state.ingressPos.x + INTERNET_R, y: state.ingressPos.y }
      return {
        ...state,
        connecting: { from: action.from, cursor },
        portEditingId: null,
        connectionError: '',
      }
    }
    case 'connect-move':
      return state.connecting && finite(action.point)
        ? { ...state, connecting: { ...state.connecting, cursor: action.point } }
        : state
    case 'connect-complete': {
      if (!state.connecting) return state
      const from = state.connecting.from
      const error = connectionError(state, from, action.to)
      if (error) return { ...state, connectionError: error }
      return {
        ...state,
        connecting: null,
        connectionError: '',
        edges: [...state.edges.filter((e) => e.from !== from), { from, to: action.to }],
      }
    }
    case 'connect-cancel':
      return { ...state, connecting: null, connectionError: '' }
    case 'disconnect':
      return {
        ...state,
        edges: state.edges.filter((e) => e.from !== action.from),
        connecting: null,
        connectionError: '',
      }
    case 'reset':
      return createCanvasState()
    case 'prefill': {
      const imported: CanvasState = {
        ...createCanvasState(),
        nodes: action.nodes.map((n) => ({ ...n })),
        edges: [],
        nextNodeId:
          Math.max(0, ...action.nodes.map((n) => Number(n.id.replace(/^n/, '')) || 0)) + 1,
      }
      // Imported connections obey the same one-input/one-output and cycle rules as editing.
      for (const edge of action.edges) {
        if (imported.edges.some((e) => e.from === edge.from)) continue
        if (!connectionError(imported, edge.from, edge.to)) imported.edges.push({ ...edge })
      }
      if (!imported.nodes.length) imported.edges = createCanvasState().edges
      return imported
    }
  }
}
