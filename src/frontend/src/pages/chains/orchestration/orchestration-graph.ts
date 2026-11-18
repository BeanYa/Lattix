import type { ChainOrchestration, HopOrchestrationSaveRequest } from '@/lib/types'
import { CARD_H, CARD_W } from '../chain-wizard/chain-canvas'

export type TriggerSubject = 'self' | 'prev' | 'next' | 'root'
export type TriggerAction = 'ban_suspected' | 'ban_banned' | 'segment_timeout' | 'recovered'

export interface EditorNode {
  key: string
  kind: 'server' | 'trigger'
  serverId?: number
  address: string
  listenPort: string
  subject?: TriggerSubject
  action?: TriggerAction
  priority: string
  isRoot?: boolean
  x: number
  y: number
}

export interface EditorEdge {
  from: string
  to: string
}

export interface EditorState {
  chainId: number
  seq: number
  activeServerId: number
  rootServerId: number
  nodes: EditorNode[]
  edges: EditorEdge[]
  nextKey: number
}

export const MAX_SERVERS = 8
export const MAX_TRIGGERS = 16
// 与链路画布同源：列可复用链路卡片尺寸；触发器是紧凑「条件胶囊」，只占名称/说明/优先级三行。
export const TRIGGER_W = 228
export const TRIGGER_H = 86
export const CARD_X = CARD_W + 72
export const CARD_Y = CARD_H + 32
export const CARD_GAP = 32
export const TRIGGER_GAP = 22

/** 画布中节点的占用尺寸（布局与避障共用）。 */
export function nodeSize(kind: EditorNode['kind']): { width: number; height: number } {
  return kind === 'server'
    ? { width: CARD_W, height: CARD_H }
    : { width: TRIGGER_W, height: TRIGGER_H }
}

export function createEditorState(input: {
  chainId: number
  seq: number
  activeServerId: number
  orchestration?: ChainOrchestration
}): EditorState {
  const { chainId, seq, activeServerId, orchestration } = input
  if (!orchestration || orchestration.nodes.length === 0) {
    return {
      chainId,
      seq,
      activeServerId,
      rootServerId: activeServerId,
      nodes: [
        {
          key: 's0',
          kind: 'server',
          serverId: activeServerId,
          address: '',
          listenPort: '',
          priority: '',
          isRoot: true,
          x: 0,
          y: 0,
        },
      ],
      edges: [],
      nextKey: 1,
    }
  }
  const keyById = new Map<number, string>()
  const nodes: EditorNode[] = orchestration.nodes.map((node) => {
    const key = node.kind === 'server' ? `s${node.id}` : `t${node.id}`
    keyById.set(node.id, key)
    return {
      key,
      kind: node.kind,
      serverId: node.server_id,
      address: node.address ?? '',
      listenPort: node.listen_port ? String(node.listen_port) : '',
      subject: node.subject as TriggerSubject | undefined,
      action: node.action as TriggerAction | undefined,
      priority: String(node.priority ?? 0),
      isRoot: node.is_root,
      x: 0,
      y: 0,
    }
  })
  const edges = orchestration.edges.flatMap((edge) => {
    const from = keyById.get(edge.from)
    const to = keyById.get(edge.to)
    return from && to ? [{ from, to }] : []
  })
  const root = nodes.find((node) => node.kind === 'server' && node.isRoot)
  const state: EditorState = {
    chainId,
    seq,
    activeServerId,
    rootServerId: root?.serverId ?? orchestration.root_server_id ?? activeServerId,
    nodes,
    edges,
    nextKey: nodes.length + 1,
  }
  return autoLayout(state)
}

function newNodeKey(state: EditorState, prefix: string): string {
  return `${prefix}${state.nextKey}`
}

export function addServer(state: EditorState, serverId: number): EditorState {
  if (
    !serverId ||
    state.nodes.some((node) => node.kind === 'server' && node.serverId === serverId)
  ) {
    return state
  }
  if (state.nodes.filter((node) => node.kind === 'server').length >= MAX_SERVERS) return state
  const key = newNodeKey(state, 's')
  const node: EditorNode = {
    key,
    kind: 'server',
    serverId,
    address: '',
    listenPort: '',
    priority: '',
    x: 0,
    y: 0,
  }
  return autoLayout({ ...state, nodes: [...state.nodes, node], nextKey: state.nextKey + 1 })
}

export function removeNode(state: EditorState, key: string): EditorState {
  const node = state.nodes.find((item) => item.key === key)
  if (!node) return state
  if (node.kind === 'server' && (node.isRoot || node.serverId === state.activeServerId))
    return state
  return autoLayout({
    ...state,
    nodes: state.nodes.filter((item) => item.key !== key),
    edges: state.edges.filter((edge) => edge.from !== key && edge.to !== key),
  })
}

export function addTrigger(
  state: EditorState,
  trigger: {
    subject: TriggerSubject
    action: TriggerAction
    priority?: string
  },
): EditorState {
  if (state.nodes.filter((node) => node.kind === 'trigger').length >= MAX_TRIGGERS) return state
  const key = newNodeKey(state, 't')
  const node: EditorNode = {
    key,
    kind: 'trigger',
    subject: trigger.subject,
    action: trigger.action,
    priority: trigger.priority ?? (trigger.subject === 'root' ? '1' : '0'),
    address: '',
    listenPort: '',
    x: 0,
    y: 0,
  }
  let edges = state.edges
  const root = state.nodes.find((item) => item.kind === 'server' && item.isRoot)
  if (trigger.subject === 'root' && root) {
    // 原节点恢复：添加即连到根入口。
    edges = [...edges, { from: key, to: root.key }]
  }
  return autoLayout({ ...state, nodes: [...state.nodes, node], edges, nextKey: state.nextKey + 1 })
}

export function connect(state: EditorState, from: string, to: string): EditorState {
  const source = state.nodes.find((node) => node.key === from)
  const target = state.nodes.find((node) => node.key === to)
  if (!source || !target || from === to) return state
  if (source.kind === target.kind) return state
  if (state.edges.some((edge) => edge.from === from && edge.to === to)) return state
  let edges = state.edges
  if (source.kind === 'server' && target.kind === 'trigger') {
    if (target.subject === 'root') return state
    // 触发器入边同样唯一：换上游服务器时替换原入边（重指来源）。
    edges = edges.filter((edge) => edge.to !== target.key)
  }
  if (source.kind === 'trigger' && target.kind === 'server') {
    if (source.subject === 'root') {
      const root = state.nodes.find((node) => node.kind === 'server' && node.isRoot)
      if (!root || target.key !== root.key) return state
    }
    // 触发器出边唯一：改连到新的服务器时替换原出边（重指目标），而不是拒绝操作。
    edges = edges.filter((edge) => edge.from !== source.key)
  }
  return autoLayout({ ...state, edges: [...edges, { from, to }] })
}

export function disconnect(state: EditorState, from: string, to: string): EditorState {
  return autoLayout({
    ...state,
    edges: state.edges.filter((edge) => !(edge.from === from && edge.to === to)),
  })
}

export function setPriority(state: EditorState, key: string, value: string): EditorState {
  return {
    ...state,
    nodes: state.nodes.map((node) => (node.key === key ? { ...node, priority: value } : node)),
  }
}

export function setServerField(
  state: EditorState,
  key: string,
  field: 'address' | 'listenPort',
  value: string,
): EditorState {
  return {
    ...state,
    nodes: state.nodes.map((node) => (node.key === key ? { ...node, [field]: value } : node)),
  }
}

function priorityRank(node: EditorNode) {
  const parsed = Number(node.priority)
  const explicit = Number.isFinite(parsed) && parsed > 0
  const typeRank =
    node.subject === 'root'
      ? 0
      : node.subject === 'self' && node.action === 'ban_suspected'
        ? 1
        : node.subject === 'self' && node.action === 'ban_banned'
          ? 2
          : node.subject === 'prev'
            ? 3
            : 4
  return { explicit, priority: explicit ? parsed : 0, typeRank }
}

/** 自动布局：根在左，BFS 按触发器优先级展开。 */
export function autoLayout(state: EditorState): EditorState {
  const root = state.nodes.find((node) => node.kind === 'server' && node.isRoot)
  if (!root) return state
  const byKey = new Map(state.nodes.map((node) => [node.key, node]))
  const outgoing = new Map<string, string[]>()
  for (const edge of state.edges) {
    const list = outgoing.get(edge.from) ?? []
    list.push(edge.to)
    outgoing.set(edge.from, list)
  }
  const positions = new Map<string, { x: number; y: number }>()
  // 各列纵向游标：服务器用卡片行距，触发器用紧凑行距，互不挤占。
  const cursor = new Map<number, number>()
  const step = (kind: EditorNode['kind']) =>
    nodeSize(kind).height + (kind === 'trigger' ? TRIGGER_GAP : CARD_GAP)
  const place = (node: EditorNode, column: number) => {
    const y = cursor.get(column) ?? 0
    cursor.set(column, y + step(node.kind))
    positions.set(node.key, { x: column * CARD_X, y })
  }
  // 第 0 列留给「原节点恢复」触发器，根固定在第 1 列。
  place(root, 1)
  const queue: { key: string; level: number }[] = [{ key: root.key, level: 1 }]
  const seen = new Set<string>([root.key])
  for (const node of state.nodes) {
    if (node.kind !== 'trigger' || node.subject !== 'root') continue
    if (state.edges.some((edge) => edge.to === node.key)) continue
    place(node, 0)
    seen.add(node.key)
    queue.push({ key: node.key, level: 0 })
  }
  while (queue.length > 0) {
    const current = queue.shift() as { key: string; level: number }
    const nextKeys = (outgoing.get(current.key) ?? []).slice().sort((a, b) => {
      const left = byKey.get(a)
      const right = byKey.get(b)
      if (!left || !right) return 0
      const lp = priorityRank(left)
      const rp = priorityRank(right)
      if (lp.explicit !== rp.explicit) return lp.explicit ? -1 : 1
      if (lp.priority !== rp.priority) return lp.priority - rp.priority
      if (lp.typeRank !== rp.typeRank) return lp.typeRank - rp.typeRank
      return state.nodes.indexOf(left) - state.nodes.indexOf(right)
    })
    for (const key of nextKeys) {
      if (seen.has(key)) continue
      seen.add(key)
      const node = byKey.get(key)
      if (!node) continue
      const level = current.level + 1
      place(node, level)
      queue.push({ key, level })
    }
  }
  // 尚未接入图的节点排到最后一列右侧，方便继续连线。
  const loose = state.nodes.filter((node) => !positions.has(node.key))
  if (loose.length > 0) {
    const column = Math.max(0, ...Array.from(cursor.keys())) + 1
    loose.forEach((node) => place(node, column))
  }
  return {
    ...state,
    nodes: state.nodes.map((node) => ({ ...node, ...(positions.get(node.key) ?? {}) })),
  }
}
export function validateGraph(state: EditorState): string[] {
  const errors: string[] = []
  const servers = state.nodes.filter((node) => node.kind === 'server')
  const triggers = state.nodes.filter((node) => node.kind === 'trigger')
  if (servers.length > MAX_SERVERS) errors.push(`服务器不能超过 ${MAX_SERVERS} 台`)
  if (triggers.length > MAX_TRIGGERS) errors.push(`触发器不能超过 ${MAX_TRIGGERS} 个`)
  if (state.nodes.length === 0) return errors
  const root = servers.find((node) => node.isRoot)
  if (!root) errors.push('缺少根服务器')
  if (servers.length > 0 && !servers.some((node) => node.serverId === state.activeServerId)) {
    errors.push('当前运行的服务器必须保留在编排图中')
  }
  const byKey = new Map(state.nodes.map((node) => [node.key, node]))
  const out = new Map<string, string[]>()
  const incoming = new Map<string, string[]>()
  for (const edge of state.edges) {
    if (!byKey.has(edge.from) || !byKey.has(edge.to)) continue
    out.set(edge.from, [...(out.get(edge.from) ?? []), edge.to])
    incoming.set(edge.to, [...(incoming.get(edge.to) ?? []), edge.from])
  }
  const triggerKeys = new Set<string>()
  for (const trigger of triggers) {
    const priority = Number(trigger.priority)
    if (!Number.isInteger(priority) || priority < 0 || priority > 9999) {
      errors.push('触发器优先级须在 0-9999')
    }
    const ins = incoming.get(trigger.key) ?? []
    const outs = out.get(trigger.key) ?? []
    if (trigger.subject === 'root') {
      if (ins.length !== 0) errors.push('原节点恢复触发器不能有来源服务器')
      if (!root || outs.length !== 1 || outs[0] !== root.key) {
        errors.push('原节点恢复触发器只能连接根服务器')
      }
    } else {
      if (ins.length !== 1) errors.push('触发器必须且只能连接一个所属服务器')
      if (outs.length !== 1) errors.push('触发器只能连接一个目标服务器')
      if (ins.length === 1 && outs.length === 1 && ins[0] === outs[0]) {
        errors.push('触发器不能指向自己的所属服务器')
      }
      if (ins.length === 1) {
        const owner = byKey.get(ins[0])
        const key = `${owner?.serverId}| ${trigger.subject}| ${trigger.action}`
        if (triggerKeys.has(key)) errors.push('同一服务器同一触发条件只能配置一个触发器')
        triggerKeys.add(key)
      }
    }
  }
  for (const server of servers) {
    if (!server.isRoot && (incoming.get(server.key) ?? []).length === 0) {
      errors.push('存在没有来源触发器的服务器')
    }
  }
  // 连通性：根 + 恢复触发器出发。
  const queue = root ? [root.key] : []
  const seen = new Set(queue)
  for (const trigger of triggers) {
    if (trigger.subject === 'root' && !seen.has(trigger.key)) {
      seen.add(trigger.key)
      queue.push(trigger.key)
    }
  }
  while (queue.length > 0) {
    const current = queue.shift() as string
    for (const next of out.get(current) ?? []) {
      if (!seen.has(next)) {
        seen.add(next)
        queue.push(next)
      }
    }
  }
  if (seen.size !== state.nodes.length) errors.push('存在从根不可达的节点')
  // 去掉指向根的边后必须无环。
  const color = new Map<string, number>()
  const walk = (key: string): boolean => {
    const value = color.get(key) ?? 0
    if (value === 1) return false
    if (value === 2) return true
    color.set(key, 1)
    for (const next of out.get(key) ?? []) {
      if (root && next === root.key) continue
      if (!walk(next)) return false
    }
    color.set(key, 2)
    return true
  }
  for (const node of state.nodes) {
    if (!walk(node.key)) {
      errors.push('不允许出现非根环（只允许经触发器回到根）')
      break
    }
  }
  return errors
}

export function toSavePayload(state: EditorState): HopOrchestrationSaveRequest {
  const serverCount = state.nodes.filter((node) => node.kind === 'server').length
  if (serverCount <= 1 && state.edges.length === 0) {
    // 只有根节点且没有连线 = 清空该跳编排。
    return { chain_id: state.chainId, seq: state.seq, nodes: [], edges: [] }
  }
  const index = new Map(state.nodes.map((node, i) => [node.key, i]))
  return {
    chain_id: state.chainId,
    seq: state.seq,
    nodes: state.nodes.map((node) => ({
      kind: node.kind,
      ...(node.kind === 'server' ? { server_id: node.serverId, is_root: node.isRoot } : {}),
      ...(node.address ? { address: node.address } : {}),
      ...(node.listenPort ? { listen_port: Number(node.listenPort) } : {}),
      ...(node.kind === 'trigger' ? { subject: node.subject, action: node.action } : {}),
      priority: Number(node.priority) || 0,
    })),
    edges: state.edges.flatMap((edge) => {
      const from = index.get(edge.from)
      const to = index.get(edge.to)
      return from === undefined || to === undefined ? [] : [{ from, to }]
    }),
  }
}

export const TRIGGERS: {
  label: string
  desc: string
  subject: TriggerSubject
  action: TriggerAction
}[] = [
  { label: '原节点恢复', desc: '当前不在根且根恢复健康', subject: 'root', action: 'recovered' },
  { label: '疑似被 ban', desc: '本机三网连续 5 轮封禁', subject: 'self', action: 'ban_suspected' },
  { label: '被 ban', desc: '本机三网连续 10 轮封禁', subject: 'self', action: 'ban_banned' },
  { label: '上一跳超时', desc: '相邻跳连续探测超时', subject: 'prev', action: 'segment_timeout' },
  { label: '下一跳超时', desc: '相邻跳连续探测超时', subject: 'next', action: 'segment_timeout' },
]

export function triggerLabel(node: Pick<EditorNode, 'subject' | 'action'>): string {
  const found = TRIGGERS.find(
    (item) => item.subject === node.subject && item.action === node.action,
  )
  return found?.label ?? '触发器'
}

export function triggerDesc(node: Pick<EditorNode, 'subject' | 'action'>): string {
  const found = TRIGGERS.find(
    (item) => item.subject === node.subject && item.action === node.action,
  )
  return found?.desc ?? ''
}

/** 触发器来源徽章：本机 / 上一跳 / 下一跳 / 恢复。 */
export function triggerSource(node: Pick<EditorNode, 'subject'>): string {
  if (node.subject === 'root') return '恢复'
  if (node.subject === 'prev') return '上一跳'
  if (node.subject === 'next') return '下一跳'
  return '本机'
}

export function moveNode(state: EditorState, key: string, x: number, y: number): EditorState {
  return {
    ...state,
    nodes: state.nodes.map((node) => (node.key === key ? { ...node, x, y } : node)),
  }
}

export function setServerAddress(state: EditorState, key: string, value: string): EditorState {
  return {
    ...state,
    nodes: state.nodes.map((node) => (node.key === key ? { ...node, address: value } : node)),
  }
}

export function setServerListenPort(state: EditorState, key: string, value: string): EditorState {
  return {
    ...state,
    nodes: state.nodes.map((node) => (node.key === key ? { ...node, listenPort: value } : node)),
  }
}

export function removeEdge(state: EditorState, from: string, to: string): EditorState {
  return {
    ...state,
    edges: state.edges.filter((edge) => !(edge.from === from && edge.to === to)),
  }
}
