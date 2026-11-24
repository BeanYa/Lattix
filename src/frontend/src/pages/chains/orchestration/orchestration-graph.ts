import type { ChainOrchestration, HopOrchestrationSaveRequest, Server } from '@/lib/types'
import {
  CARD_H,
  CARD_W,
  inboundCapable,
  isExternalServer,
  isUsableExternalServer,
  portInServerRanges,
} from '../chain-wizard/chain-canvas'

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

export interface ValidationContext {
  validateRootParameters?: boolean
  seq?: number
  totalHops?: number
  hopRole?: 'entry' | 'middle' | 'exit'
  primaryIsExternal?: boolean
  serverMap?: Map<number, Server>
  blockedServerIds?: Set<number>
}

export interface GraphIssue {
  message: string
  nodeKeys?: string[]
}

export const MAX_SERVERS = 8
export const MAX_TRIGGERS = 16
export const TRIGGER_W = 228
export const TRIGGER_H = 104
export const COL_GAP = 80
export const CARD_GAP = 32
export const TRIGGER_GAP = 20
export const CARD_X = CARD_W + COL_GAP
export const CARD_Y = CARD_H + CARD_GAP

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
  initialAddress?: string
  initialListenPort?: string
}): EditorState {
  const { chainId, seq, activeServerId, orchestration, initialAddress, initialListenPort } = input
  if (!orchestration || orchestration.nodes.length === 0) {
    return autoLayout({
      chainId,
      seq,
      activeServerId,
      rootServerId: activeServerId,
      nodes: [
        {
          key: 's0',
          kind: 'server',
          serverId: activeServerId,
          address: initialAddress ?? '',
          listenPort: initialListenPort ?? '',
          priority: '',
          isRoot: true,
          x: 0,
          y: 0,
        },
      ],
      edges: [],
      nextKey: 1,
    })
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
      priority: node.priority > 0 ? String(node.priority) : '',
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
  const used = new Set(state.nodes.map((node) => node.key))
  let index = state.nextKey
  while (used.has(`${prefix}${index}`)) index += 1
  return `${prefix}${index}`
}

function overlapsExisting(
  nodes: EditorNode[],
  x: number,
  y: number,
  width: number,
  height: number,
  margin = 16,
): boolean {
  return nodes.some((node) => {
    const size = nodeSize(node.kind)
    return !(
      x + width + margin <= node.x ||
      node.x + size.width + margin <= x ||
      y + height + margin <= node.y ||
      node.y + size.height + margin <= y
    )
  })
}

function findNonOverlappingPoint(
  nodes: EditorNode[],
  width: number,
  height: number,
  startX: number,
  startY: number,
  stepY: number,
): { x: number; y: number } {
  let y = startY
  for (let i = 0; i < 32; i += 1) {
    if (!overlapsExisting(nodes, startX, y, width, height)) return { x: startX, y }
    y += stepY
  }
  return { x: startX, y: startY }
}

export function addServer(
  state: EditorState,
  serverId: number,
  point?: { x: number; y: number },
): EditorState {
  if (
    !serverId ||
    state.nodes.some((node) => node.kind === 'server' && node.serverId === serverId)
  ) {
    return state
  }
  if (state.nodes.filter((node) => node.kind === 'server').length >= MAX_SERVERS) return state
  const key = newNodeKey(state, 's')
  let pos: { x: number; y: number }
  if (point && Number.isFinite(point.x) && Number.isFinite(point.y)) {
    pos = { x: Math.round(point.x - CARD_W / 2), y: Math.round(point.y - CARD_H / 2) }
  } else {
    const root = state.nodes.find((node) => node.kind === 'server' && node.isRoot)
    const triggers = state.nodes.filter(
      (node) => node.kind === 'trigger' && node.subject !== 'root',
    )
    const baseRight =
      triggers.length > 0
        ? Math.max(...triggers.map((node) => node.x + TRIGGER_W))
        : root
          ? root.x + CARD_W
          : 0
    const startX = Math.round(baseRight + COL_GAP)
    const startY = root ? root.y : -CARD_H / 2
    pos = findNonOverlappingPoint(state.nodes, CARD_W, CARD_H, startX, startY, CARD_H + CARD_GAP)
  }
  const node: EditorNode = {
    key,
    kind: 'server',
    serverId,
    address: '',
    listenPort: '',
    priority: '',
    x: pos.x,
    y: pos.y,
  }
  return { ...state, nodes: [...state.nodes, node], nextKey: state.nextKey + 1 }
}

export function removeNode(state: EditorState, key: string): EditorState {
  const node = state.nodes.find((item) => item.key === key)
  if (!node) return state
  if (node.kind === 'server' && (node.isRoot || node.serverId === state.activeServerId))
    return state
  return {
    ...state,
    nodes: state.nodes.filter((item) => item.key !== key),
    edges: state.edges.filter((edge) => edge.from !== key && edge.to !== key),
  }
}

export function addTrigger(
  state: EditorState,
  trigger: {
    subject: TriggerSubject
    action: TriggerAction
    priority?: string
  },
  point?: { x: number; y: number },
): EditorState {
  if (state.nodes.filter((node) => node.kind === 'trigger').length >= MAX_TRIGGERS) return state
  const key = newNodeKey(state, 't')
  const root = state.nodes.find((item) => item.kind === 'server' && item.isRoot)
  let pos: { x: number; y: number }
  if (point && Number.isFinite(point.x) && Number.isFinite(point.y)) {
    pos = { x: Math.round(point.x - TRIGGER_W / 2), y: Math.round(point.y - TRIGGER_H / 2) }
  } else if (trigger.subject === 'root' && root) {
    const startX = Math.round(root.x - TRIGGER_W - COL_GAP)
    const startY = Math.round(root.y + (CARD_H - TRIGGER_H) / 2)
    pos = findNonOverlappingPoint(
      state.nodes,
      TRIGGER_W,
      TRIGGER_H,
      startX,
      startY,
      TRIGGER_H + TRIGGER_GAP,
    )
  } else {
    const startX = root ? Math.round(root.x + CARD_W + COL_GAP) : 0
    const startY = root ? Math.round(root.y + (CARD_H - TRIGGER_H) / 2) : -TRIGGER_H / 2
    pos = findNonOverlappingPoint(
      state.nodes,
      TRIGGER_W,
      TRIGGER_H,
      startX,
      startY,
      TRIGGER_H + TRIGGER_GAP,
    )
  }
  const node: EditorNode = {
    key,
    kind: 'trigger',
    subject: trigger.subject,
    action: trigger.action,
    priority: trigger.priority ?? (trigger.subject === 'root' ? '1' : ''),
    address: '',
    listenPort: '',
    x: pos.x,
    y: pos.y,
  }
  let edges = state.edges
  if (trigger.subject === 'root' && root) {
    // 原节点恢复：添加即连到根入口。
    edges = [...edges, { from: key, to: root.key }]
  }
  return { ...state, nodes: [...state.nodes, node], edges, nextKey: state.nextKey + 1 }
}

/** 校验两节点是否允许连线（与画布高亮和提示共用单一判定）。 */
export function canConnect(
  state: EditorState,
  from: string,
  to: string,
  context?: Pick<ValidationContext, 'serverMap'>,
): { ok: boolean; reason?: string } {
  const source = state.nodes.find((node) => node.key === from)
  const target = state.nodes.find((node) => node.key === to)
  if (!source || !target) return { ok: false, reason: '节点不存在' }
  if (from === to) return { ok: false, reason: '不能连接到节点自身' }
  if (source.kind === target.kind) {
    return {
      ok: false,
      reason: source.kind === 'server' ? '服务器只能连到触发器入口' : '触发器只能连到服务器入口',
    }
  }
  if (state.edges.some((edge) => edge.from === from && edge.to === to)) {
    return { ok: false, reason: '连线已存在' }
  }
  if (source.kind === 'server' && target.kind === 'trigger') {
    if (target.subject === 'root') {
      return { ok: false, reason: '原节点恢复触发器由面板全局检测，不能连接来源服务器' }
    }
    if (state.edges.some((edge) => edge.from === target.key && edge.to === source.key)) {
      return { ok: false, reason: '触发器不能指向自己的所属服务器' }
    }
    const server = source.serverId ? context?.serverMap?.get(source.serverId) : undefined
    if (target.subject === 'self' && server && (isExternalServer(server) || server.non_direct)) {
      return { ok: false, reason: '外部直链/非直连服务器没有三网封禁观察' }
    }
    const duplicate = state.edges.some((edge) => {
      if (edge.from !== source.key || edge.to === target.key) return false
      const peer = state.nodes.find((node) => node.key === edge.to)
      return (
        peer?.kind === 'trigger' && peer.subject === target.subject && peer.action === target.action
      )
    })
    if (duplicate) {
      return { ok: false, reason: '同一服务器同一触发条件只能配置一个触发器' }
    }
    return { ok: true }
  }
  // trigger -> server
  if (source.subject === 'root' && !target.isRoot) {
    return { ok: false, reason: '原节点恢复触发器只能连接根服务器' }
  }
  if (state.edges.some((edge) => edge.from === target.key && edge.to === source.key)) {
    return { ok: false, reason: '触发器不能指向自己的所属服务器' }
  }
  return { ok: true }
}

export function connect(
  state: EditorState,
  from: string,
  to: string,
  context?: Pick<ValidationContext, 'serverMap'>,
): EditorState {
  const check = canConnect(state, from, to, context)
  if (!check.ok) return state
  const source = state.nodes.find((node) => node.key === from)!
  const target = state.nodes.find((node) => node.key === to)!
  let edges = state.edges
  if (source.kind === 'server' && target.kind === 'trigger') {
    // 触发器入边唯一：换上游服务器时替换原入边（重指来源）。
    edges = edges.filter((edge) => edge.to !== target.key)
  }
  if (source.kind === 'trigger' && target.kind === 'server') {
    // 触发器出边唯一：改连到新的服务器时替换原出边（重指目标）。
    edges = edges.filter((edge) => edge.from !== source.key)
  }
  return { ...state, edges: [...edges, { from, to }] }
}

export function disconnect(state: EditorState, from: string, to: string): EditorState {
  return removeEdge(state, from, to)
}

export function clearGraph(state: EditorState): EditorState {
  const root = state.nodes.find((node) => node.kind === 'server' && node.isRoot)
  if (!root) return state
  return autoLayout({
    ...state,
    nodes: [root],
    edges: [],
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

/** 自动布局：根在左，BFS 按触发器优先级展开，各列紧贴对齐且纵向以 y=0 居中。 */
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

  const columns = new Map<number, EditorNode[]>()
  const assignColumn = (node: EditorNode, col: number) => {
    const list = columns.get(col) ?? []
    list.push(node)
    columns.set(col, list)
  }

  assignColumn(root, 1)
  const queue: { key: string; level: number }[] = [{ key: root.key, level: 1 }]
  const seen = new Set<string>([root.key])

  for (const node of state.nodes) {
    if (node.kind !== 'trigger' || node.subject !== 'root') continue
    if (state.edges.some((edge) => edge.to === node.key)) continue
    assignColumn(node, 0)
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
      assignColumn(node, level)
      queue.push({ key, level })
    }
  }

  // 尚未接入图的节点排到最后一列右侧，方便继续连线。
  const loose = state.nodes.filter((node) => !seen.has(node.key))
  if (loose.length > 0) {
    const nextCol = Math.max(0, ...Array.from(columns.keys())) + 1
    loose.forEach((node) => assignColumn(node, nextCol))
  }

  const positions = new Map<string, { x: number; y: number }>()
  const sortedCols = Array.from(columns.keys()).sort((a, b) => a - b)
  let cursorX = 0
  for (const col of sortedCols) {
    const colNodes = columns.get(col) ?? []
    if (colNodes.length === 0) continue
    const colWidth = Math.max(...colNodes.map((node) => nodeSize(node.kind).width))
    const gaps = colNodes
      .slice(0, -1)
      .map((node) => (node.kind === 'trigger' ? TRIGGER_GAP : CARD_GAP))
    const totalHeight =
      colNodes.reduce((sum, node) => sum + nodeSize(node.kind).height, 0) +
      gaps.reduce((sum, g) => sum + g, 0)
    let cursorY = -Math.round(totalHeight / 2)
    colNodes.forEach((node, index) => {
      const size = nodeSize(node.kind)
      positions.set(node.key, {
        x: Math.round(cursorX + (colWidth - size.width) / 2),
        y: Math.round(cursorY),
      })
      cursorY += size.height + (gaps[index] ?? 0)
    })
    cursorX += colWidth + COL_GAP
  }

  return {
    ...state,
    nodes: state.nodes.map((node) => ({ ...node, ...(positions.get(node.key) ?? {}) })),
  }
}

function serverAddressSet(server: Server): Set<string> {
  const list =
    server.addresses.length > 0
      ? server.addresses
      : [server.address, server.learned_addr].filter(Boolean)
  return new Set(list)
}

export function serverEligibility(
  server: Server,
  state: EditorState,
  context: Pick<ValidationContext, 'hopRole' | 'primaryIsExternal' | 'blockedServerIds'>,
): { eligible: boolean; reason?: string } {
  if (state.nodes.some((node) => node.kind === 'server' && node.serverId === server.id)) {
    return { eligible: false, reason: '已在图中' }
  }
  if (context.blockedServerIds?.has(server.id)) {
    return { eligible: false, reason: '其他跳已用' }
  }
  if (context.hopRole === 'exit') {
    const primaryExt = Boolean(context.primaryIsExternal)
    if (primaryExt !== isExternalServer(server)) {
      return { eligible: false, reason: primaryExt ? '需外部直链' : '需托管节点' }
    }
    if (isExternalServer(server) && !isUsableExternalServer(server)) {
      return { eligible: false, reason: '直链未配置' }
    }
    return { eligible: true }
  }
  if (isExternalServer(server)) {
    return { eligible: false, reason: '仅可作出口' }
  }
  if (!inboundCapable(server)) {
    return { eligible: false, reason: '无入站能力' }
  }
  return { eligible: true }
}

/** 校验编排图并返回带定位节点键的问题列表（供画布高亮与页脚展示）。 */
export function validateGraphIssues(state: EditorState, context?: ValidationContext): GraphIssue[] {
  const issues: GraphIssue[] = []
  const servers = state.nodes.filter((node) => node.kind === 'server')
  const triggers = state.nodes.filter((node) => node.kind === 'trigger')

  if (servers.length > MAX_SERVERS) {
    issues.push({ message: `服务器不能超过 ${MAX_SERVERS} 台` })
  }
  if (triggers.length > MAX_TRIGGERS) {
    issues.push({ message: `触发器不能超过 ${MAX_TRIGGERS} 个` })
  }
  if (state.nodes.length === 0) return issues
  // 只有根节点且无触发器无连线 = 清空该跳编排（即使当前已切换到替补也允许清空）。
  if (
    servers.length === 1 &&
    servers[0].isRoot &&
    triggers.length === 0 &&
    state.edges.length === 0 &&
    !context?.validateRootParameters
  ) {
    return issues
  }

  const root = servers.find((node) => node.isRoot)
  if (!root) {
    issues.push({ message: '缺少根服务器' })
  }
  if (servers.length > 0 && !servers.some((node) => node.serverId === state.activeServerId)) {
    issues.push({ message: '当前运行的服务器必须保留在编排图中' })
  }

  // 重复服务器检查
  const seenServerIds = new Map<number, string[]>()
  for (const server of servers) {
    if (!server.serverId) continue
    seenServerIds.set(server.serverId, [...(seenServerIds.get(server.serverId) ?? []), server.key])
  }
  for (const [, keys] of seenServerIds) {
    if (keys.length > 1) {
      issues.push({ message: '同一服务器在编排图中重复', nodeKeys: keys })
    }
  }

  if (
    state.edges.length === 0 &&
    !(servers.length === 1 && servers[0].isRoot && triggers.length === 0)
  ) {
    issues.push({
      message: '编排图至少需要一条连线',
      nodeKeys: state.nodes.filter((node) => !node.isRoot).map((node) => node.key),
    })
  }

  const byKey = new Map(state.nodes.map((node) => [node.key, node]))
  const out = new Map<string, string[]>()
  const incoming = new Map<string, string[]>()
  for (const edge of state.edges) {
    if (!byKey.has(edge.from) || !byKey.has(edge.to)) continue
    out.set(edge.from, [...(out.get(edge.from) ?? []), edge.to])
    incoming.set(edge.to, [...(incoming.get(edge.to) ?? []), edge.from])
  }

  const hasServerMap = Boolean(context?.serverMap && context.serverMap.size > 0)
  const triggerKeys = new Set<string>()
  let recoveryCount = 0

  for (const trigger of triggers) {
    const rawPriority = trigger.priority.trim()
    if (rawPriority !== '') {
      const parsed = Number(rawPriority)
      if (!/^\d+$/.test(rawPriority) || !Number.isInteger(parsed) || parsed < 0 || parsed > 9999) {
        issues.push({ message: '触发器优先级须在 0-9999', nodeKeys: [trigger.key] })
      }
    }

    const ins = incoming.get(trigger.key) ?? []
    const outs = out.get(trigger.key) ?? []

    if (trigger.subject === 'root') {
      recoveryCount += 1
      if (recoveryCount > 1) {
        issues.push({ message: '原节点恢复触发器最多一个', nodeKeys: [trigger.key] })
      }
      if (ins.length !== 0) {
        issues.push({ message: '原节点恢复触发器不能有来源服务器', nodeKeys: [trigger.key] })
      }
      if (!root || outs.length !== 1 || outs[0] !== root.key) {
        issues.push({ message: '原节点恢复触发器只能连接根服务器', nodeKeys: [trigger.key] })
      }
      continue
    }

    if (ins.length !== 1) {
      issues.push({ message: '触发器必须且只能连接一个所属服务器', nodeKeys: [trigger.key] })
    }
    if (outs.length !== 1) {
      issues.push({ message: '触发器只能连接一个目标服务器', nodeKeys: [trigger.key] })
    }
    if (ins.length === 1 && outs.length === 1 && ins[0] === outs[0]) {
      issues.push({ message: '触发器不能指向自己的所属服务器', nodeKeys: [trigger.key] })
    }
    if (trigger.subject === 'prev' && context?.seq === 0) {
      issues.push({ message: '入口跳没有上一跳超时触发器', nodeKeys: [trigger.key] })
    }
    if (
      trigger.subject === 'next' &&
      context?.seq !== undefined &&
      context?.totalHops !== undefined &&
      context.seq === context.totalHops - 1
    ) {
      issues.push({ message: '出口跳没有下一跳超时触发器', nodeKeys: [trigger.key] })
    }
    if (ins.length === 1) {
      const owner = byKey.get(ins[0])
      const condKey = `${owner?.serverId}|${trigger.subject}|${trigger.action}`
      if (triggerKeys.has(condKey)) {
        issues.push({
          message: '同一服务器同一触发条件只能配置一个触发器',
          nodeKeys: [trigger.key],
        })
      }
      triggerKeys.add(condKey)
      if (trigger.subject === 'self' && hasServerMap && owner?.serverId) {
        const ownerSrv = context?.serverMap?.get(owner.serverId)
        if (ownerSrv && (isExternalServer(ownerSrv) || ownerSrv.non_direct)) {
          issues.push({
            message: '外部直链/非直连服务器没有三网封禁观察',
            nodeKeys: [trigger.key, owner.key],
          })
        }
      }
    }
  }

  for (const server of servers) {
    if (!server.isRoot && (incoming.get(server.key) ?? []).length === 0) {
      issues.push({ message: '存在没有来源触发器的服务器', nodeKeys: [server.key] })
    }
    if (server.serverId && context?.blockedServerIds?.has(server.serverId)) {
      const alias = context.serverMap?.get(server.serverId)?.alias ?? `#${server.serverId}`
      issues.push({
        message: `服务器 ${alias} 已被同链其他跳使用`,
        nodeKeys: [server.key],
      })
    }

    const rawPort = server.listenPort.trim()
    let parsedPort = 0
    let validPortNumber = true
    if (rawPort !== '') {
      if (!/^\d+$/.test(rawPort)) {
        validPortNumber = false
      } else {
        parsedPort = Number(rawPort)
        if (!Number.isInteger(parsedPort) || parsedPort < 0 || parsedPort > 65535) {
          validPortNumber = false
        }
      }
      if (!validPortNumber) {
        issues.push({ message: '端口须为 1-65535（0 或留空为自动）', nodeKeys: [server.key] })
      }
    }

    if (hasServerMap && server.serverId) {
      const srv = context?.serverMap?.get(server.serverId)
      if (!srv) {
        issues.push({ message: `服务器 #${server.serverId} 不存在`, nodeKeys: [server.key] })
        continue
      }
      if (context?.hopRole === 'exit') {
        if (
          context.primaryIsExternal !== undefined &&
          isExternalServer(srv) !== context.primaryIsExternal
        ) {
          issues.push({
            message: `出口备选 ${srv.alias} 须与主出口同为托管或同为外部直链`,
            nodeKeys: [server.key],
          })
        } else if (isExternalServer(srv) && !isUsableExternalServer(srv)) {
          issues.push({
            message: `外部直链服务器 ${srv.alias} 缺少地址或端口配置`,
            nodeKeys: [server.key],
          })
        }
      } else if (context?.hopRole) {
        if (isExternalServer(srv)) {
          issues.push({
            message: `外部直链服务器 ${srv.alias} 不能作为入口/中转备选`,
            nodeKeys: [server.key],
          })
        } else if (!inboundCapable(srv)) {
          issues.push({
            message: `服务器 ${srv.alias} 无入站能力，不能作为入口/中转备选`,
            nodeKeys: [server.key],
          })
        }
      }
      if (server.address && !serverAddressSet(srv).has(server.address)) {
        issues.push({
          message: `服务器 ${srv.alias} 不存在公网地址 ${server.address}`,
          nodeKeys: [server.key],
        })
      }
      if (
        validPortNumber &&
        parsedPort > 0 &&
        srv.allowed_ports.length > 0 &&
        !portInServerRanges(srv, parsedPort)
      ) {
        issues.push({
          message: `端口 ${parsedPort} 不在服务器 ${srv.alias} 可用段内`,
          nodeKeys: [server.key],
        })
      }
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
  if (seen.size !== state.nodes.length) {
    const unreachable = state.nodes.filter((node) => !seen.has(node.key)).map((node) => node.key)
    issues.push({ message: '存在从根不可达的节点', nodeKeys: unreachable })
  }

  // 去掉指向根的边后必须无环。
  const color = new Map<string, number>()
  const cycleKeys: string[] = []
  const walk = (key: string): boolean => {
    const value = color.get(key) ?? 0
    if (value === 1) {
      cycleKeys.push(key)
      return false
    }
    if (value === 2) return true
    color.set(key, 1)
    for (const next of out.get(key) ?? []) {
      if (root && next === root.key) continue
      if (!walk(next)) {
        cycleKeys.push(key)
        return false
      }
    }
    color.set(key, 2)
    return true
  }
  for (const node of state.nodes) {
    if (!walk(node.key)) {
      issues.push({
        message: '不允许出现非根环（只允许经触发器回到根）',
        nodeKeys: [...new Set(cycleKeys)],
      })
      break
    }
  }
  return issues
}

export function validateGraph(state: EditorState, context?: ValidationContext): string[] {
  return validateGraphIssues(state, context).map((issue) => issue.message)
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
  if (!Number.isFinite(x) || !Number.isFinite(y)) return state
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
