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

/** 节点在链路中的角色：orphan = 未接入路径。 */
export type CanvasNodeRole = 'entry' | 'middle' | 'exit' | 'orphan'

/** 节点身份来自显式入网路径，不会因其他片段断开而改选入口。 */
export function nodeRole(nodes: CanvasNode[], edges: CanvasEdge[], nodeId: string): CanvasNodeRole {
  const walk = walkPath(nodes, edges)
  if (walk.start?.id === nodeId) return 'entry'
  if (!walk.order.some((node) => node.id === nodeId)) return 'orphan'
  return edges.some((edge) => edge.from === nodeId && edge.to === INTERNET_NODE_ID)
    ? 'exit'
    : 'middle'
}

export function portEditableRole(role: CanvasNodeRole): boolean {
  return role === 'entry' || role === 'exit'
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

// ---------- 校验 ----------

export type CanvasIssueCode =
  | 'empty'
  | 'hop_limit'
  | 'duplicate_server'
  | 'no_entry'
  | 'multi_entry'
  | 'branch'
  | 'cycle'
  | 'not_linked'
  | 'orphan'
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

export interface CanvasValidation {
  valid: boolean
  issues: CanvasIssue[]
}

export const MAX_HOPS = 4

export function validateTopology(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
  serverById: Map<number, Server>,
): CanvasValidation {
  if (nodes.length === 0) {
    return {
      valid: false,
      issues: [{ code: 'empty', message: '画布为空：从左侧拖入至少一台服务器作为入口节点' }],
    }
  }
  const issues: CanvasIssue[] = []
  if (nodes.length > MAX_HOPS) {
    issues.push({
      code: 'hop_limit',
      message: `链长上限 ${MAX_HOPS} 跳（入口 + 中间跳 ≤2 + 出口）`,
    })
  }
  const nodeIds = new Set(nodes.map((node) => node.id))
  if (
    edges.some(
      (edge) =>
        (edge.from !== INGRESS_NODE_ID && !nodeIds.has(edge.from)) ||
        (edge.to !== INTERNET_NODE_ID && !nodeIds.has(edge.to)),
    )
  ) {
    issues.push({ code: 'invalid_edge', message: '连线引用了不存在的节点，请重新连接' })
  }
  const seen = new Set<number>()
  for (const node of nodes) {
    if (seen.has(node.serverId)) {
      issues.push({
        code: 'duplicate_server',
        nodeId: node.id,
        message: '同一服务器在一条链中不重复',
      })
    }
    seen.add(node.serverId)
    if (!serverById.has(node.serverId))
      issues.push({
        code: 'server_missing',
        nodeId: node.id,
        message: '服务器已不存在，请移除该节点',
      })
  }

  const walk = walkPath(nodes, edges)
  if (!walk.start) {
    issues.push({
      code: 'no_entry',
      message: '入网流量尚未连接服务器：点击入网流量出口，再点击服务器入口',
    })
  }
  if (walk.branchAt) {
    issues.push({
      code: 'branch',
      nodeId: walk.branchAt,
      message: '一个节点的出口端口只能连一条线',
    })
  }
  if (walk.cycleAt) {
    issues.push({ code: 'cycle', nodeId: walk.cycleAt, message: '链路存在环路，请断开成单链' })
  }
  if (walk.start && !walk.endsAtInternet) {
    issues.push({
      code: 'not_linked',
      message: '链路尚未连接到 Internet 出口：把最后一个节点的出口端口连到右侧圆球',
    })
  }
  const onPath = new Set(walk.order.map((node) => node.id))
  for (const node of nodes) {
    if (!onPath.has(node.id)) {
      issues.push({ code: 'orphan', nodeId: node.id, message: '该节点未接入链路' })
    }
  }

  // 端口与入站能力只对已成形的路径逐跳检查，避免半连线状态刷屏。
  if (walk.start && !walk.branchAt && !walk.cycleAt) {
    walk.order.forEach((node, index) => {
      const server = serverById.get(node.serverId)
      if (!server) return
      const port = portValue(node.port)
      if (Number.isNaN(port)) {
        issues.push({ code: 'port_range', nodeId: node.id, message: '端口须为 1-65535 的数字' })
        return
      }
      if (port === null) return
      if (port < 1 || port > 65535) {
        issues.push({ code: 'port_range', nodeId: node.id, message: '端口须为 1-65535' })
        return
      }
      const isEntry = index === 0
      const isExit = index === walk.order.length - 1 && walk.endsAtInternet
      // 只有首节点（entry_port）与末节点（出口节点端口）的端口会提交，后端也只校验这两处。
      if (
        (isEntry || isExit) &&
        server.allowed_ports.length > 0 &&
        !portInServerRanges(server, port)
      ) {
        issues.push({
          code: 'port_nat_span',
          nodeId: node.id,
          message: `端口 ${port} 不在服务器 ${server.alias} 可用段内`,
        })
      }
    })
    walk.order.forEach((node, index) => {
      if (walk.order.length > 1 && index === walk.order.length - 1) return
      const server = serverById.get(node.serverId)
      if (server && !inboundCapable(server)) {
        issues.push({
          code: 'inbound',
          nodeId: node.id,
          message: `服务器 ${server.alias} 无入站能力（仅出口档 NAT），不能作入口/中间跳`,
        })
      }
    })
  }

  return { valid: issues.length === 0, issues }
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

/** 路径 → 表单拓扑。直连 = 单节点（后端单跳 entry_port 别名节点端口）。 */
export function toFormTopology(order: CanvasNode[]): ChainFormTopology {
  if (order.length === 0) return { ...EMPTY_FORM_TOPOLOGY }
  const first = order[0]
  const last = order[order.length - 1]
  const relay = order.length > 1
  return {
    chainType: relay ? 'relay' : 'direct',
    entryId: String(first.serverId),
    middleIds: order.slice(1, -1).map((node) => String(node.serverId)),
    exitId: relay ? String(last.serverId) : '',
    entryAddr: first.address,
    middleAddrs: order.slice(1, -1).map((node) => node.address),
    exitAddr: relay ? last.address : '',
    entryPort: normalizePortInput(first.port),
    port: relay ? normalizePortInput(last.port) : '',
  }
}

// ---------- 编辑回填 ----------

/**
 * 编辑模式画布预填：hops 已按 seq 升序（首位入口、末位出口），水平均布排位。
 * exitPort 来自出口节点 config 解析（见 use-chain-form.parseChainEdit）。
 */
export function prefillCanvas(
  chain: Chain,
  exitPort: string,
): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  // 预填行放在画布上部：常见可视高度内完整可见（含端口行），不依赖实际窗口尺寸
  const rowY = 240
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
          ? exitPort
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
