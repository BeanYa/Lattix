import type { Chain, PortRange, Server } from '@/lib/types'

// 链路拓扑画布（两步向导第一步）的纯函数层：节点/边模型、路径推导、有效性校验、
// 与现有 CreateChainRequest/EditChainRequest 拓扑字段的双向映射、几何常量。
// 后端契约不变：端口语义 = 首节点入口端口 entry_port、末节点入口端口出口节点端口、
// 中间跳端口由后端自动分配（画布上不可编辑）。

/** Internet 出口特殊节点的保留 id：仅有入口端口，连入它的节点即最终跳。 */
export const INTERNET_NODE_ID = '__internet__'
export const INGRESS_NODE_ID = '__ingress__'

/** 节点尺寸由视图直接读取；连线锚点与卡片共享几何值。 */
export const CARD_W = 264
export const CARD_H = 200
export const CARD_BODY_H = 120
export const ADDRESS_ROW_H = 24
export const PORT_R = 9
/** 地址下拉位于入口上方，入口圆点在左边缘，与右侧端口输入居中对齐。 */
export const ENTRY_PORT_OFFSET_X = 0
export const ENTRY_PORT_OFFSET_Y =
  CARD_BODY_H + ADDRESS_ROW_H + (CARD_H - CARD_BODY_H - ADDRESS_ROW_H) / 2
/** 终端初始世界坐标；之后由拖动修改，视角适配不改动节点坐标。 */
export const INTERNET_POS = { x: 900, y: 400 }
export const INTERNET_R = 52
/** 固定流量来源，与 Internet 终端共享圆球尺寸；不计入服务器跳数。 */
export const INGRESS_POS = { x: 80, y: 80 }

export interface CanvasNode {
  id: string
  serverId: number
  x: number
  y: number
  /** 入口端口输入值；'' 或 '0' = 自动分配。仅首节点（entry_port）与末节点（出口节点端口）会提交。 */
  port: string
  /** 该跳公网地址；'' = 跟随服务器默认地址（§9）。 */
  address: string
}

/** from 的出口端口 → to 的入口端口；to 为 INTERNET_NODE_ID 表示最终跳通向 Internet。 */
export interface CanvasEdge {
  from: string
  to: string
}

// ---------- 几何 ----------

export function entryPortPoint(node: Pick<CanvasNode, 'x' | 'y'>): { x: number; y: number } {
  return { x: node.x + ENTRY_PORT_OFFSET_X, y: node.y + ENTRY_PORT_OFFSET_Y }
}

export function exitPortPoint(node: Pick<CanvasNode, 'x' | 'y'>): { x: number; y: number } {
  return { x: node.x + CARD_W, y: node.y + CARD_BODY_H / 2 }
}

export function internetEntryPoint(pos: { x: number; y: number } = INTERNET_POS): {
  x: number
  y: number
} {
  return { x: pos.x - INTERNET_R, y: pos.y }
}

// ---------- 入站能力（§21，自 use-chain-form 迁入供校验复用） ----------

/** 入站能力：direct 或 NAT 受限（有端口段）。仅出口档 NAT 不能作入口/中间跳。 */
export function inboundCapable(s: Server): boolean {
  return s.machine_type === 'direct' || s.allowed_ports.length > 0
}

// ---------- 未托管外部直链节点 ----------

/** 未托管外部直链节点（无 Agent、无入站能力），只能作链路最后一跳（出口）。 */
export function isExternalServer(s: Server): boolean {
  return s.kind === 'external'
}

/** 外部节点端口来自直链规格（external_exit.port），不作为表单端口提交。 */
export function externalPortFor(s: Server): number {
  return s.external_exit?.port ?? 0
}

// ---------- 端口输入 ----------

/** 归一化端口输入：'0'/空白 → ''（自动分配）。 */
export function normalizePortInput(port: string): string {
  const trimmed = port.trim()
  return trimmed === '0' ? '' : trimmed
}

function portValue(port: string): number | null {
  const normalized = normalizePortInput(port)
  if (!normalized) return null
  if (!/^\d+$/.test(normalized)) return NaN
  return Number(normalized)
}

/** 后端 checkPortInRanges 镜像：有监听段约束时端口须落在段内（listen 缺省回退 pub）。 */
export function portInServerRanges(server: Server, port: number): boolean {
  return server.allowed_ports.some((range: PortRange) => {
    const start = range.listen_start ? range.listen_start : range.pub_start
    const end = range.listen_end ? range.listen_end : range.pub_end
    return port >= start && port <= end
  })
}

/** NAT 可用端口下拉的展开上限：超出时截断并提示（实际 NAT 段通常为几十个端口）。 */
export const NAT_PORT_LIST_LIMIT = 500

/** NAT 服务器的可用端口列表（allowed_ports 段展开，listen 缺省回退 pub）。 */
export function natPortOptions(server: Server): {
  options: number[]
  total: number
  truncated: boolean
} {
  let total = 0
  const options: number[] = []
  for (const range of server.allowed_ports) {
    const start = range.listen_start ? range.listen_start : range.pub_start
    const end = range.listen_end ? range.listen_end : range.pub_end
    total += Math.max(0, end - start + 1)
    for (let port = start; port <= end && options.length < NAT_PORT_LIST_LIMIT; port += 1) {
      options.push(port)
    }
  }
  return { options, total, truncated: total > options.length }
}

// ---------- 路径推导 ----------

/** 节点在链路中的角色：orphan = 未接入任何连线。 */
export type CanvasNodeRole = 'entry' | 'middle' | 'exit' | 'orphan'

/** 分支/汇合下节点可同时具备多个角色（入口节点也可以继续分叉）。 */
export interface CanvasNodeRoles {
  /** 有来自「流量入口」的入边。 */
  entry: boolean
  /** 有指向其他服务器的出边（非末位跳）。 */
  middle: boolean
  /** 有指向 Internet 的出边。 */
  exit: boolean
}

export function nodeRoles(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
  nodeId: string,
): CanvasNodeRoles {
  const ids = new Set(nodes.map((node) => node.id))
  const incoming = edges.filter((edge) => edge.to === nodeId)
  const outgoing = edges.filter((edge) => edge.from === nodeId)
  return {
    entry: incoming.some((edge) => edge.from === INGRESS_NODE_ID),
    middle: outgoing.some((edge) => edge.to !== INTERNET_NODE_ID && ids.has(edge.to)),
    exit: outgoing.some((edge) => edge.to === INTERNET_NODE_ID),
  }
}

/** 卡片样式用的主角色：入口 > 出口 > 中转 > 未接入。 */
export function nodeRole(nodes: CanvasNode[], edges: CanvasEdge[], nodeId: string): CanvasNodeRole {
  const roles = nodeRoles(nodes, edges, nodeId)
  if (roles.entry) return 'entry'
  if (roles.exit) return 'exit'
  if (roles.middle || edges.some((edge) => edge.to === nodeId)) return 'middle'
  return 'orphan'
}

/** 端口语义：有流量入口入边 → 入口端口；有 Internet 出边 → 出口端口（可两者兼有）。 */
export interface CanvasNodePortRole {
  entry: boolean
  exit: boolean
  editable: boolean
  label: string
}

export function nodePortRole(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
  nodeId: string,
): CanvasNodePortRole {
  const roles = nodeRoles(nodes, edges, nodeId)
  const label =
    roles.entry && roles.exit
      ? '入口/出口端口'
      : roles.entry
        ? '入口端口'
        : roles.exit
          ? '出口端口'
          : ''
  return { entry: roles.entry, exit: roles.exit, editable: roles.entry || roles.exit, label }
}

export interface CanvasWalk {
  start: CanvasNode | null
  order: CanvasNode[]
  endsAtInternet: boolean
  branchAt: string | null
  cycleAt: string | null
}

export function walkPath(nodes: CanvasNode[], edges: CanvasEdge[]): CanvasWalk {
  const byId = new Map(nodes.map((node) => [node.id, node]))
  const sources = edges.filter((edge) => edge.from === INGRESS_NODE_ID)
  const start = sources.length === 1 ? (byId.get(sources[0].to) ?? null) : null
  const branchAt =
    sources.length > 1
      ? INGRESS_NODE_ID
      : (nodes.find(
          (node) =>
            edges.filter((edge) => edge.from === node.id).length > 1 ||
            edges.filter((edge) => edge.to === node.id).length > 1,
        )?.id ?? null)
  // 包括未接入片段的环路，避免无入口时漏掉非法图。
  let cycleAt: string | null = null
  for (const node of nodes) {
    const seen = new Set<string>()
    let cursor: string | undefined = node.id
    while (cursor && byId.has(cursor)) {
      if (seen.has(cursor)) {
        cycleAt = cursor
        break
      }
      seen.add(cursor)
      cursor = edges.find((edge) => edge.from === cursor)?.to
    }
    if (cycleAt) break
  }
  const order: CanvasNode[] = []
  const visited = new Set<string>()
  let current = start
  let endsAtInternet = false
  while (current && !visited.has(current.id)) {
    visited.add(current.id)
    order.push(current)
    const outgoing = edges.filter((edge) => edge.from === current!.id)
    if (outgoing.length !== 1) break
    if (outgoing[0].to === INTERNET_NODE_ID) {
      endsAtInternet = true
      break
    }
    current = byId.get(outgoing[0].to) ?? null
  }
  return { start, order, endsAtInternet, branchAt, cycleAt }
}

// ---------- 图分析与多链路枚举 ----------

export const MAX_HOPS = 4
/** 单张画布允许的节点上限（每条路径长度另由 MAX_HOPS 限制）。 */
export const MAX_CANVAS_NODES = 16
/** 单次最多枚举的链路数（防止极端分叉枚举/预览卡死，超出按硬错误处理）。 */
export const MAX_CHAIN_PATHS = 64

export type CanvasIssueCode =
  | 'empty'
  | 'hop_limit'
  | 'duplicate_server'
  | 'no_entry'
  | 'cycle'
  | 'no_chain'
  | 'too_many_paths'
  | 'external_not_last'
  | 'inbound'
  | 'port_range'
  | 'port_nat_span'
  | 'server_missing'
  | 'invalid_edge'

export interface CanvasIssue {
  code: CanvasIssueCode
  /** 定位到节点的 issue：画布上高亮该卡片。 */
  nodeId?: string
  message: string
}

export type CanvasWarningCode = 'entry_unreachable' | 'exit_unreachable' | 'isolated'

/** 空悬警告：只提示「不会生成」，不阻止其余合法链路。 */
export interface CanvasWarning {
  code: CanvasWarningCode
  nodeId?: string
  message: string
  /** 示例路径（预览里展示「不会生成：X → … → Internet」）。 */
  path?: CanvasNode[]
  /** 示例路径是否确实走到了 Internet（入口侧空悬的预览用）。 */
  reachesInternet?: boolean
}

export interface CanvasGraphAnalysis {
  valid: boolean
  errors: CanvasIssue[]
  warnings: CanvasWarning[]
  paths: CanvasNode[][]
  /** 按节点 id 索引的角色/端口语义（分支/汇合下可多重）。 */
  roles: Map<string, CanvasNodeRoles>
  portRoles: Map<string, CanvasNodePortRole>
}

export interface CanvasValidation {
  valid: boolean
  /** 硬错误列表（保留 issues 字段兼容既有画布 issue 渲染）。 */
  issues: CanvasIssue[]
  warnings: CanvasWarning[]
  /** 枚举出的合法链路：每条简单路径 = 一条将创建的链路。 */
  paths: CanvasNode[][]
}

// ---------- 分析辅助 ----------

function findDirectedCycle(nodeIds: string[], adjacency: Map<string, Set<string>>): string | null {
  const state = new Map<string, number>()
  let cycleAt: string | null = null
  const visit = (id: string): boolean => {
    state.set(id, 1)
    for (const next of adjacency.get(id) ?? []) {
      const nextState = state.get(next) ?? 0
      if (nextState === 1) {
        cycleAt = next
        return true
      }
      if (nextState === 0 && visit(next)) return true
    }
    state.set(id, 2)
    return false
  }
  for (const id of nodeIds) {
    if ((state.get(id) ?? 0) === 0 && visit(id)) break
  }
  return cycleAt
}

/** 入口侧空悬示例：从该节点出发找到的第一条通往 Internet 的简单路径。 */
function firstPathToInternet(
  startId: string,
  adjacency: Map<string, Set<string>>,
  internetOut: Set<string>,
  nodeById: Map<string, CanvasNode>,
  externalIds: Set<string>,
): CanvasNode[] | null {
  const visit = (id: string, path: CanvasNode[]): CanvasNode[] | null => {
    if (path.some((node) => node.id === id)) return null
    const node = nodeById.get(id)
    if (!node) return null
    const nextPath = [...path, node]
    if (internetOut.has(id)) return nextPath
    if (externalIds.has(id) || nextPath.length >= MAX_HOPS) return null
    for (const target of adjacency.get(id) ?? []) {
      const found = visit(target, nextPath)
      if (found) return found
    }
    return null
  }
  return visit(startId, [])
}

/** 出口侧空悬示例：从任一入口到该节点的第一条简单路径。 */
function firstPathToNode(
  roots: string[],
  targetId: string,
  adjacency: Map<string, Set<string>>,
  nodeById: Map<string, CanvasNode>,
): CanvasNode[] | null {
  const queue = roots.map((id) => [id])
  const seen = new Set<string>()
  while (queue.length > 0) {
    const path = queue.shift()
    if (!path) break
    const id = path[path.length - 1]
    if (seen.has(id)) continue
    seen.add(id)
    if (id === targetId) {
      const pathNodes: CanvasNode[] = []
      for (const nodeId of path) {
        const node = nodeById.get(nodeId)
        if (node) pathNodes.push(node)
      }
      return pathNodes
    }
    if (path.length >= MAX_HOPS) continue
    for (const target of adjacency.get(id) ?? []) queue.push([...path, target])
  }
  return null
}

export function analyzeCanvasGraph(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
  serverById: Map<number, Server>,
): CanvasGraphAnalysis {
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const nodeIndex = new Map(nodes.map((node, index) => [node.id, index]))
  const roles = new Map<string, CanvasNodeRoles>()
  const portRoles = new Map<string, CanvasNodePortRole>()
  for (const node of nodes) {
    roles.set(node.id, nodeRoles(nodes, edges, node.id))
    portRoles.set(node.id, nodePortRole(nodes, edges, node.id))
  }
  if (nodes.length === 0) {
    return {
      valid: false,
      errors: [{ code: 'empty', message: '画布为空：从左侧拖入至少一台服务器作为入口节点' }],
      warnings: [],
      paths: [],
      roles,
      portRoles,
    }
  }
  const errors: CanvasIssue[] = []
  const warnings: CanvasWarning[] = []
  const paths: CanvasNode[][] = []
  const nodeIds = new Set(nodeById.keys())
  const externalIds = new Set<string>()
  for (const node of nodes) {
    const server = serverById.get(node.serverId)
    if (server && isExternalServer(server)) externalIds.add(node.id)
  }
  const isExternal = (id: string) => externalIds.has(id)

  const seenServers = new Set<number>()
  for (const node of nodes) {
    if (seenServers.has(node.serverId)) {
      errors.push({
        code: 'duplicate_server',
        nodeId: node.id,
        message: '同一服务器在一张画布中不重复',
      })
    }
    seenServers.add(node.serverId)
    if (!serverById.has(node.serverId)) {
      errors.push({
        code: 'server_missing',
        nodeId: node.id,
        message: '服务器已不存在，请移除该节点',
      })
    }
  }

  // 邻接：只保留存在的节点；同一 from→to 重复连线去重。
  const serverOut = new Map<string, Set<string>>()
  const internetOut = new Set<string>()
  const ingressTargets: string[] = []
  const seenEdges = new Set<string>()
  let invalidEdge = false
  for (const edge of edges) {
    const key = edge.from + '->' + edge.to
    if (seenEdges.has(key)) continue
    seenEdges.add(key)
    if (edge.from === INGRESS_NODE_ID) {
      if (edge.to === INTERNET_NODE_ID) continue
      if (!nodeIds.has(edge.to)) {
        invalidEdge = true
        continue
      }
      ingressTargets.push(edge.to)
      continue
    }
    if (!nodeIds.has(edge.from)) {
      invalidEdge = true
      continue
    }
    if (edge.to === INTERNET_NODE_ID) {
      internetOut.add(edge.from)
      continue
    }
    if (!nodeIds.has(edge.to)) {
      invalidEdge = true
      continue
    }
    const targets = serverOut.get(edge.from) ?? new Set<string>()
    targets.add(edge.to)
    serverOut.set(edge.from, targets)
  }
  if (invalidEdge) {
    errors.push({ code: 'invalid_edge', message: '连线引用了不存在的节点，请重新连接' })
  }

  const roots = [...new Set(ingressTargets)].sort(
    (a, b) => (nodeIndex.get(a) ?? 0) - (nodeIndex.get(b) ?? 0),
  )
  if (roots.length === 0) {
    errors.push({
      code: 'no_entry',
      message: '入网流量尚未连接服务器：点击入网流量出口，再点击服务器入口',
    })
  }

  // 环是硬错误；提前检测，后续枚举用简单路径不会死循环。
  const cycleAt = findDirectedCycle([...nodeIds], serverOut)
  if (cycleAt) {
    errors.push({ code: 'cycle', nodeId: cycleAt, message: '链路存在环路，请断开环路' })
  }

  // 未托管外部直链节点只能作最后一跳：不能作入口，也不能继续向下游连服务器。
  const invalidExternal = new Set<string>()
  for (const node of nodes) {
    if (!isExternal(node.id)) continue
    const hasServerOut = (serverOut.get(node.id)?.size ?? 0) > 0
    if (hasServerOut || roots.includes(node.id)) {
      invalidExternal.add(node.id)
      const server = serverById.get(node.serverId)
      errors.push({
        code: 'external_not_last',
        nodeId: node.id,
        message:
          '未托管服务器 ' +
          (server?.alias ?? '#' + node.serverId) +
          ' 只能作为链路的最后一跳（出口）',
      })
    }
  }

  // 可达性（外部节点只作为终点，不继续向下游遍历）。
  const reachable = new Set<string>()
  const queue = roots.filter((id) => !isExternal(id))
  while (queue.length > 0) {
    const id = queue.shift()
    if (!id || reachable.has(id)) continue
    reachable.add(id)
    if (isExternal(id)) continue
    for (const target of serverOut.get(id) ?? []) {
      if (!reachable.has(target)) queue.push(target)
    }
  }
  const canReachInternet = new Set<string>(internetOut)
  let changed = true
  while (changed) {
    changed = false
    for (const [from, targets] of serverOut) {
      if (canReachInternet.has(from)) continue
      for (const target of targets) {
        if (canReachInternet.has(target)) {
          canReachInternet.add(from)
          changed = true
          break
        }
      }
    }
  }

  // 空悬警告（不阻止其余合法链路生成）。
  for (const node of nodes) {
    if (invalidExternal.has(node.id)) continue
    const server = serverById.get(node.serverId)
    const alias = server?.alias ?? '#' + node.serverId
    const hasEdge = edges.some((edge) => edge.from === node.id || edge.to === node.id)
    if (!hasEdge) {
      warnings.push({
        code: 'isolated',
        nodeId: node.id,
        message: '孤立节点 ' + alias + '（没有任何连线，不会生成链路）',
      })
      continue
    }
    const isRoot = roots.includes(node.id)
    if (!isRoot && !reachable.has(node.id)) {
      const example = firstPathToInternet(node.id, serverOut, internetOut, nodeById, externalIds)
      warnings.push({
        code: 'entry_unreachable',
        nodeId: node.id,
        message: alias + ' 未接入流量入口，其链路不会生成',
        path: example ?? [node],
        reachesInternet: example !== null,
      })
      continue
    }
    if (!canReachInternet.has(node.id)) {
      warnings.push({
        code: 'exit_unreachable',
        nodeId: node.id,
        message: alias + ' 未连接到 Internet，其链路不会生成',
        path: firstPathToNode(roots, node.id, serverOut, nodeById) ?? [node],
      })
    }
  }

  // 简单路径枚举：每条「入口 → … → Internet」即一条将创建的链路；
  // 顺序 = 入口节点出现顺序 + 每个节点出边的节点出现顺序（稳定可对照）。
  let hopLimitExceeded = false
  let tooManyPaths = false
  const byNodeOrder = (a: string, b: string) => (nodeIndex.get(a) ?? 0) - (nodeIndex.get(b) ?? 0)
  const visit = (nodeId: string, path: CanvasNode[]) => {
    if (tooManyPaths) return
    if (path.some((node) => node.id === nodeId)) return
    const node = nodeById.get(nodeId)
    if (!node) return
    const nextPath = [...path, node]
    if (internetOut.has(nodeId)) {
      if (nextPath.length > MAX_HOPS) hopLimitExceeded = true
      else if (paths.length >= MAX_CHAIN_PATHS) tooManyPaths = true
      else paths.push(nextPath)
    }
    if (tooManyPaths) return
    if (isExternal(nodeId)) return
    if (nextPath.length >= MAX_HOPS) {
      if ((serverOut.get(nodeId)?.size ?? 0) > 0) hopLimitExceeded = true
      return
    }
    for (const target of [...(serverOut.get(nodeId) ?? [])].sort(byNodeOrder)) {
      visit(target, nextPath)
    }
  }
  for (const root of roots) {
    if (isExternal(root)) continue
    visit(root, [])
  }
  if (hopLimitExceeded) {
    errors.push({
      code: 'hop_limit',
      message: '链长上限 ' + MAX_HOPS + ' 跳（入口 + 中间跳 ≤2 + 出口）',
    })
  }
  if (tooManyPaths) {
    errors.push({
      code: 'too_many_paths',
      message: '可生成的链路过多（超过 ' + MAX_CHAIN_PATHS + ' 条），请减少分支后重试',
    })
  }
  if (paths.length === 0) {
    errors.push({
      code: 'no_chain',
      message: '没有可生成的链路：请将入网流量连接到服务器，并让每条路径最终连到 Internet 出口',
    })
  }

  // 端口只校验实际会提交的首跳（entry_port）与末跳（出口节点端口）；外部节点端口来自链接。
  const checkedPorts = new Set<string>()
  const checkPort = (node: CanvasNode) => {
    const server = serverById.get(node.serverId)
    if (!server || isExternalServer(server)) return
    const key = node.id + ':' + node.port
    if (checkedPorts.has(key)) return
    checkedPorts.add(key)
    const port = portValue(node.port)
    if (Number.isNaN(port)) {
      errors.push({ code: 'port_range', nodeId: node.id, message: '端口须为 1-65535 的数字' })
      return
    }
    if (port === null) return
    if (port < 1 || port > 65535) {
      errors.push({ code: 'port_range', nodeId: node.id, message: '端口须为 1-65535' })
      return
    }
    if (server.allowed_ports.length > 0 && !portInServerRanges(server, port)) {
      errors.push({
        code: 'port_nat_span',
        nodeId: node.id,
        message: '端口 ' + port + ' 不在服务器 ' + server.alias + ' 可用段内',
      })
    }
  }
  for (const path of paths) {
    checkPort(path[0])
    checkPort(path[path.length - 1])
  }

  // 入站能力：有流量入口入边或有指向其他服务器出边的节点必须有入站能力；纯末位跳豁免。
  for (const node of nodes) {
    const server = serverById.get(node.serverId)
    if (!server || isExternalServer(server)) continue
    const role = roles.get(node.id)
    if (role && (role.entry || role.middle) && !inboundCapable(server)) {
      errors.push({
        code: 'inbound',
        nodeId: node.id,
        message: '服务器 ' + server.alias + ' 无入站能力（仅出口档 NAT），不能作入口/中间跳',
      })
    }
  }

  return { valid: errors.length === 0, errors, warnings, paths, roles, portRoles }
}

export function validateTopology(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
  serverById: Map<number, Server>,
): CanvasValidation {
  const analysis = analyzeCanvasGraph(nodes, edges, serverById)
  return {
    valid: analysis.valid,
    issues: analysis.errors,
    warnings: analysis.warnings,
    paths: analysis.paths,
  }
}

// ---------- 与表单拓扑字段互转 ----------

/** use-chain-form 的拓扑注入（原 ChainFormState 内的拓扑字段）。 */
export interface ChainFormTopology {
  chainType: 'direct' | 'relay'
  entryId: string
  middleIds: string[]
  exitId: string
  entryAddr: string
  middleAddrs: string[]
  exitAddr: string
  entryPort: string
  /** 出口节点监听端口（中转链）；直连时与 entryPort 同一输入。 */
  port: string
}

export const EMPTY_FORM_TOPOLOGY: ChainFormTopology = {
  chainType: 'direct',
  entryId: '',
  middleIds: [],
  exitId: '',
  entryAddr: '',
  middleAddrs: [],
  exitAddr: '',
  entryPort: '',
  port: '',
}

/**
 * 路径 → 表单拓扑。直连 = 单节点（后端单跳 entry_port 别名节点端口）。
 * 末位为未托管外部直链节点时端口来自链接，port 返回 ''（不作为出口节点端口提交）。
 */
export function toFormTopology(
  order: CanvasNode[],
  serverById?: Map<number, Server>,
): ChainFormTopology {
  if (order.length === 0) return { ...EMPTY_FORM_TOPOLOGY }
  const first = order[0]
  const last = order[order.length - 1]
  const relay = order.length > 1
  const lastServer = serverById?.get(last.serverId)
  const lastExternal = lastServer ? isExternalServer(lastServer) : false
  return {
    chainType: relay ? 'relay' : 'direct',
    entryId: String(first.serverId),
    middleIds: order.slice(1, -1).map((node) => String(node.serverId)),
    exitId: relay ? String(last.serverId) : '',
    entryAddr: first.address,
    middleAddrs: order.slice(1, -1).map((node) => node.address),
    exitAddr: relay ? last.address : '',
    entryPort: normalizePortInput(first.port),
    port: relay && !lastExternal ? normalizePortInput(last.port) : '',
  }
}

// ---------- 编辑回填 ----------

/**
 * 编辑模式画布预填：hops 已按 seq 升序（首位入口、末位出口），水平均布排位。
 * exitPort 来自出口节点 config 解析（见 use-chain-form.parseChainEdit）；
 * 外部直链出口没有出口节点端口（端口来自链接），末位不回填 exitPort。
 */
export function prefillCanvas(
  chain: Chain,
  exitPort: string,
): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  // 预填行放在画布上部：常见可视高度内完整可见（含端口行），不依赖实际窗口尺寸
  const rowY = 240
  const externalExit = chain.service_external != null
  const nodes = chain.hops.map((hop, index) => ({
    id: `n${index + 1}`,
    serverId: hop.server_id,
    x: 240 + index * (CARD_W + 96),
    y: rowY,
    port:
      index === 0
        ? chain.entry_port
          ? String(chain.entry_port)
          : ''
        : index === chain.hops.length - 1
          ? externalExit
            ? ''
            : exitPort
          : '',
    address: hop.address ?? '',
  }))
  const edges: CanvasEdge[] = nodes.length ? [{ from: INGRESS_NODE_ID, to: nodes[0].id }] : []
  for (let i = 0; i < nodes.length - 1; i += 1) {
    edges.push({ from: nodes[i].id, to: nodes[i + 1].id })
  }
  if (nodes.length > 0) {
    edges.push({ from: nodes[nodes.length - 1].id, to: INTERNET_NODE_ID })
  }
  return { nodes, edges }
}
