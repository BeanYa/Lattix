import {
  CARD_H,
  CARD_W,
  INGRESS_NODE_ID,
  INGRESS_POS,
  INTERNET_NODE_ID,
  INTERNET_POS,
  INTERNET_R,
  MAX_CANVAS_NODES,
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
  | { type: 'candidate-add'; id: string; serverId: number }
  | { type: 'candidate-remove'; id: string; serverId: number }
  | { type: 'candidate-move'; id: string; index: number; direction: -1 | 1 }
  | { type: 'connect-start'; from: string }
  | { type: 'connect-move'; point: Point }
  | { type: 'connect-complete'; to: string }
  | { type: 'connect-cancel' }
  | { type: 'disconnect'; from: string; to: string }
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
  // 分支/汇合允许一个节点多条出边、多条入边；只拦环（from 可由 to 沿现有出边到达）。
  const stack = [to]
  const seen = new Set<string>()
  while (stack.length > 0) {
    const cursor = stack.pop()
    if (!cursor) break
    if (cursor === from) return '该连接会形成环路，请选择下游节点'
    if (seen.has(cursor)) continue
    seen.add(cursor)
    for (const edge of state.edges) {
      if (edge.from === cursor) stack.push(edge.to)
    }
  }
  return ''
}

/** 图编辑的唯一写入口。预览与已提交边分离，取消/失败不修改任何边。 */
export function canvasReducer(state: CanvasState, action: CanvasAction): CanvasState {
  switch (action.type) {
    case 'add': {
      if (state.nodes.length >= MAX_CANVAS_NODES || !finite(action.point)) return state
      const id = `n${state.nextNodeId}`
      const node: CanvasNode = {
        id,
        serverId: action.serverId,
        x: action.point.x - CARD_W / 2,
        y: action.point.y - CARD_H / 2,
        port: '',
        address: '',
        candidates: [],
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
    case 'candidate-add':
      return {
        ...state,
        nodes: state.nodes.map((n) => {
          if (n.id !== action.id) return n
          const current = n.candidates ?? []
          if (n.serverId === action.serverId || current.some((c) => c.serverId === action.serverId))
            return n
          return {
            ...n,
            candidates: [...current, { serverId: action.serverId, address: '', port: '' }],
          }
        }),
      }
    case 'candidate-remove':
      return {
        ...state,
        nodes: state.nodes.map((n) => {
          if (n.id !== action.id) return n
          return {
            ...n,
            candidates: (n.candidates ?? []).filter((c) => c.serverId !== action.serverId),
          }
        }),
      }
    case 'candidate-move':
      return {
        ...state,
        nodes: state.nodes.map((n) => {
          if (n.id !== action.id) return n
          const current = [...(n.candidates ?? [])]
          const nextIdx = action.index + action.direction
          if (
            action.index < 0 ||
            action.index >= current.length ||
            nextIdx < 0 ||
            nextIdx >= current.length
          )
            return n
          const [moved] = current.splice(action.index, 1)
          current.splice(nextIdx, 0, moved)
          return { ...n, candidates: current }
        }),
      }
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
      if (state.edges.some((edge) => edge.from === from && edge.to === action.to)) {
        return { ...state, connecting: null, connectionError: '' }
      }
      // 连到服务器即不再直通 Internet：移除自动生成的末端占位线；反向（连 Internet）
      // 保留既有服务器出边，从而支持「A→B 且 A→Internet」的分支。
      const kept =
        action.to === INTERNET_NODE_ID
          ? state.edges
          : state.edges.filter((edge) => !(edge.from === from && edge.to === INTERNET_NODE_ID))
      return {
        ...state,
        connecting: null,
        connectionError: '',
        edges: [...kept, { from, to: action.to }],
      }
    }
    case 'connect-cancel':
      return { ...state, connecting: null, connectionError: '' }
    case 'disconnect':
      return {
        ...state,
        edges: state.edges.filter((e) => !(e.from === action.from && e.to === action.to)),
        connecting: null,
        connectionError: '',
      }
    case 'reset':
      return createCanvasState()
    case 'prefill': {
      const imported: CanvasState = {
        ...createCanvasState(),
        nodes: action.nodes.map((n) => ({
          ...n,
          candidates: n.candidates ? [...n.candidates] : [],
        })),
        edges: [],
        nextNodeId:
          Math.max(0, ...action.nodes.map((n) => Number(n.id.replace(/^n/, '')) || 0)) + 1,
      }
      // Imported connections obey the same cycle rule as editing；分支/汇合均允许。
      for (const edge of action.edges) {
        if (imported.edges.some((e) => e.from === edge.from && e.to === edge.to)) continue
        if (!connectionError(imported, edge.from, edge.to)) imported.edges.push({ ...edge })
      }
      if (!imported.nodes.length) imported.edges = createCanvasState().edges
      return imported
    }
  }
}
