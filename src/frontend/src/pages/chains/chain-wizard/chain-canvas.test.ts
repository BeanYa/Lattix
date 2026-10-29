import { describe, expect, it } from 'vitest'

import type { Chain, Server } from '@/lib/types'

import {
  INTERNET_NODE_ID,
  INGRESS_NODE_ID,
  NAT_PORT_LIST_LIMIT,
  entryPortPoint,
  exitPortPoint,
  internetEntryPoint,
  natPortOptions,
  nodeRole,
  normalizePortInput,
  portInServerRanges,
  prefillCanvas,
  toFormTopology,
  validateTopology,
  walkPath,
  type CanvasEdge,
  type CanvasNode,
} from './chain-canvas'

function node(id: string, serverId: number, overrides: Partial<CanvasNode> = {}): CanvasNode {
  return { id, serverId, x: 100, y: 100, port: '', address: '', ...overrides }
}

function directServer(overrides: Partial<Server> = {}): Server {
  return {
    id: 1,
    alias: 'srv',
    machine_type: 'direct',
    allowed_ports: [],
    addresses: [],
    ...overrides,
  } as unknown as Server
}

function natServer(id: number, ranges: Array<[number, number]>): Server {
  return {
    id,
    alias: `nat-${id}`,
    machine_type: 'nat',
    allowed_ports: ranges.map(([pub_start, pub_end]) => ({ pub_start, pub_end })),
  } as unknown as Server
}

function serverMap(...servers: Server[]): Map<number, Server> {
  return new Map(servers.map((server) => [server.id, server]))
}

function chainOf(...ids: string[]): CanvasEdge[] {
  const edges: CanvasEdge[] = [
    { from: INGRESS_NODE_ID, to: ids[0] },
    ...ids.slice(0, -1).map((id, i) => ({ from: id, to: ids[i + 1] })),
  ]
  edges.push({ from: ids[ids.length - 1], to: INTERNET_NODE_ID })
  return edges
}

describe('walkPath', () => {
  it('单节点连 Internet：order 单元素且 endsAtInternet', () => {
    const nodes = [node('a', 1)]
    const walk = walkPath(nodes, chainOf('a'))
    expect(walk.start?.id).toBe('a')
    expect(walk.order.map((n) => n.id)).toEqual(['a'])
    expect(walk.endsAtInternet).toBe(true)
    expect(walk.branchAt).toBeNull()
    expect(walk.cycleAt).toBeNull()
  })

  it('多节点串联到 Internet：按连线顺序输出', () => {
    const nodes = [node('a', 1), node('b', 2), node('c', 3)]
    const walk = walkPath(nodes, chainOf('c', 'a', 'b'))
    expect(walk.start?.id).toBe('c')
    expect(walk.order.map((n) => n.id)).toEqual(['c', 'a', 'b'])
    expect(walk.endsAtInternet).toBe(true)
  })

  it('存在孤立片段不改变显式入口', () => {
    const nodes = [node('a', 1), node('b', 2)]
    const walk = walkPath(nodes, chainOf('a'))
    expect(walk.start?.id).toBe('a')
    expect(walk.order.map((n) => n.id)).toEqual(['a'])
  })

  it('分支：branchAt 指向分叉节点，endsAtInternet 为 false', () => {
    const nodes = [node('a', 1), node('b', 2), node('c', 3)]
    const edges = [
      { from: INGRESS_NODE_ID, to: 'a' },
      { from: 'a', to: 'b' },
      { from: 'a', to: 'c' },
      { from: 'b', to: INTERNET_NODE_ID },
    ]
    const walk = walkPath(nodes, edges)
    expect(walk.branchAt).toBe('a')
    expect(walk.endsAtInternet).toBe(false)
  })

  it('环：cycleAt 指向重复访问的节点', () => {
    const nodes = [node('a', 1), node('b', 2)]
    const edges = [
      { from: INGRESS_NODE_ID, to: 'a' },
      { from: 'a', to: 'b' },
      { from: 'b', to: 'a' },
    ]
    const walk = walkPath(nodes, edges)
    expect(walk.cycleAt).toBe('a')
    expect(walk.endsAtInternet).toBe(false)
  })
})

describe('validateTopology', () => {
  it('单服务器仍需承接入网流量，仅出口 NAT 不可作为直连入口', () => {
    const server = natServer(1, [])
    const result = validateTopology([node('a', 1)], chainOf('a'), serverMap(server))
    expect(result.valid).toBe(false)
    expect(result.issues.some((issue) => issue.code === 'inbound')).toBe(true)
  })
  it('空画布：empty issue', () => {
    const result = validateTopology([], [], serverMap())
    expect(result.valid).toBe(false)
    expect(result.issues[0]?.code).toBe('empty')
  })

  it('超过 4 跳：hop_limit', () => {
    const nodes = [node('a', 1), node('b', 2), node('c', 3), node('d', 4), node('e', 5)]
    const result = validateTopology(nodes, chainOf(...nodes.map((n) => n.id)), serverMap())
    expect(result.issues.some((issue) => issue.code === 'hop_limit')).toBe(true)
  })

  it('重复服务器：duplicate_server 定位到第二个出现', () => {
    const nodes = [node('a', 1), node('b', 1)]
    const result = validateTopology(nodes, chainOf('a', 'b'), serverMap(directServer()))
    expect(
      result.issues.some((issue) => issue.code === 'duplicate_server' && issue.nodeId === 'b'),
    ).toBe(true)
  })

  it('未连 Internet：not_linked', () => {
    const nodes = [node('a', 1)]
    const result = validateTopology(
      nodes,
      [{ from: INGRESS_NODE_ID, to: 'a' }],
      serverMap(directServer()),
    )
    expect(result.issues.some((issue) => issue.code === 'not_linked')).toBe(true)
  })

  it('孤岛节点：orphan 定位到未接入节点', () => {
    const nodes = [node('a', 1), node('b', 2)]
    const result = validateTopology(nodes, chainOf('a'), serverMap(directServer()))
    expect(result.issues.some((issue) => issue.code === 'orphan' && issue.nodeId === 'b')).toBe(
      true,
    )
  })

  it('中间跳无入站能力：inbound 定位到该节点；末节点不受限', () => {
    const exitOnly = natServer(2, [])
    // NAT 仅出口机作入口 → 违规；作末节点 → 豁免。
    const asEntry = validateTopology(
      [node('a', 2), node('b', 1)],
      chainOf('a', 'b'),
      serverMap(directServer(), exitOnly),
    )
    expect(asEntry.issues.some((issue) => issue.code === 'inbound' && issue.nodeId === 'a')).toBe(
      true,
    )
    const asExit = validateTopology(
      [node('a', 1), node('b', 2)],
      chainOf('a', 'b'),
      serverMap(directServer(), exitOnly),
    )
    expect(asExit.issues.every((issue) => issue.code !== 'inbound')).toBe(true)
  })

  it('首节点端口不在 NAT 段：port_nat_span；段内端口通过', () => {
    const nat = natServer(1, [[1000, 2000]])
    const bad = validateTopology([node('a', 1, { port: '8080' })], chainOf('a'), serverMap(nat))
    expect(bad.issues.some((issue) => issue.code === 'port_nat_span')).toBe(true)
    const good = validateTopology([node('a', 1, { port: '1443' })], chainOf('a'), serverMap(nat))
    expect(good.valid).toBe(true)
  })

  it('末节点（出口）端口同样检查 NAT 段', () => {
    const natExit = natServer(2, [[5000, 6000]])
    const nodes = [node('a', 1), node('b', 2, { port: '7000' })]
    const result = validateTopology(nodes, chainOf('a', 'b'), serverMap(directServer(), natExit))
    expect(
      result.issues.some((issue) => issue.code === 'port_nat_span' && issue.nodeId === 'b'),
    ).toBe(true)
  })

  it('端口范围非法：port_range', () => {
    const result = validateTopology(
      [node('a', 1, { port: '70000' })],
      chainOf('a'),
      serverMap(directServer()),
    )
    expect(result.issues.some((issue) => issue.code === 'port_range')).toBe(true)
  })

  it('最小合法链：单节点 + Internet，valid', () => {
    const result = validateTopology([node('a', 1)], chainOf('a'), serverMap(directServer()))
    expect(result.valid).toBe(true)
    expect(result.issues).toEqual([])
  })
})

describe('toFormTopology', () => {
  it('单节点：direct，port 为空', () => {
    const topology = toFormTopology([node('a', 7, { port: '0', address: '1.2.3.4' })])
    expect(topology).toEqual({
      chainType: 'direct',
      entryId: '7',
      middleIds: [],
      exitId: '',
      entryAddr: '1.2.3.4',
      middleAddrs: [],
      exitAddr: '',
      entryPort: '',
      port: '',
    })
  })

  it('多节点：relay，中间跳按序，末节点端口映射到出口端口', () => {
    const order = [
      node('a', 1, { port: '1443', address: '' }),
      node('b', 2, { address: '2.2.2.2' }),
      node('c', 3, { port: '8443' }),
    ]
    const topology = toFormTopology(order)
    expect(topology.chainType).toBe('relay')
    expect(topology.entryId).toBe('1')
    expect(topology.middleIds).toEqual(['2'])
    expect(topology.exitId).toBe('3')
    expect(topology.middleAddrs).toEqual(['2.2.2.2'])
    expect(topology.entryPort).toBe('1443')
    expect(topology.port).toBe('8443')
  })
})

describe('prefillCanvas', () => {
  it('按 hops 顺序生成节点与连线，端口回填首尾', () => {
    const chain = {
      entry_port: 1443,
      hops: [
        { server_id: 1, address: '' },
        { server_id: 2, address: '2.2.2.2' },
        { server_id: 3, address: '' },
      ],
    } as unknown as Chain
    const { nodes, edges } = prefillCanvas(chain, '8443')
    expect(nodes.map((n) => n.serverId)).toEqual([1, 2, 3])
    expect(nodes[0].port).toBe('1443')
    expect(nodes[1].port).toBe('')
    expect(nodes[2].port).toBe('8443')
    expect(nodes[1].address).toBe('2.2.2.2')
    expect(edges).toEqual([
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: 'n2' },
      { from: 'n2', to: 'n3' },
      { from: 'n3', to: INTERNET_NODE_ID },
    ])
  })
})

describe('nodeRole（按图连通性）', () => {
  it('完整链：首=入口、尾=出口、中间=中转；孤立节点=未接入', () => {
    const nodes = [node('a', 1), node('b', 2), node('c', 3), node('d', 4)]
    const edges = [...chainOf('a', 'b', 'c')]
    expect(nodeRole(nodes, edges, 'a')).toBe('entry')
    expect(nodeRole(nodes, edges, 'b')).toBe('middle')
    expect(nodeRole(nodes, edges, 'c')).toBe('exit')
    expect(nodeRole(nodes, edges, 'd')).toBe('orphan')
  })

  it('多入口断链态：已连段的首尾仍标入口/出口，未接线节点=未接入', () => {
    const nodes = [node('a', 1), node('b', 2), node('x', 3)]
    const edges = [...chainOf('a', 'b')]
    expect(nodeRole(nodes, edges, 'a')).toBe('entry')
    expect(nodeRole(nodes, edges, 'b')).toBe('exit')
    expect(nodeRole(nodes, edges, 'x')).toBe('orphan')
  })

  it('链尾未通 Internet：末节点为 middle（等待接线）', () => {
    const nodes = [node('a', 1), node('b', 2)]
    const edges = [
      { from: INGRESS_NODE_ID, to: 'a' },
      { from: 'a', to: 'b' },
    ]
    expect(nodeRole(nodes, edges, 'b')).toBe('middle')
  })
})

describe('几何与端口工具', () => {
  it('端口点位：入口对齐左侧端口行，出口对齐右侧服务器区', () => {
    const n = { x: 100, y: 200 }
    expect(entryPortPoint(n)).toEqual({ x: 100, y: 372 }) // 入口对齐 56px 端口区中心
    expect(exitPortPoint(n)).toEqual({ x: 364, y: 260 }) // 出口对齐服务器信息区中心
    expect(internetEntryPoint().x).toBeLessThan(internetEntryPoint().x + 1)
  })

  it('natPortOptions：allowed_ports 段展开（listen 缺省回退 pub），超限截断', () => {
    const server = {
      allowed_ports: [
        { pub_start: 1000, pub_end: 1002, listen_start: 0, listen_end: 0 },
        { pub_start: 5000, pub_end: 5003, listen_start: 6000, listen_end: 6002 },
      ],
    } as unknown as Server
    const result = natPortOptions(server)
    expect(result.options).toEqual([1000, 1001, 1002, 6000, 6001, 6002])
    expect(result.truncated).toBe(false)
    const huge = natPortOptions({
      allowed_ports: [{ pub_start: 1, pub_end: 60000 }],
    } as unknown as Server)
    expect(huge.options.length).toBe(NAT_PORT_LIST_LIMIT)
    expect(huge.truncated).toBe(true)
  })

  it('normalizePortInput：0 与空白 → 自动', () => {
    expect(normalizePortInput('0')).toBe('')
    expect(normalizePortInput(' 0 ')).toBe('')
    expect(normalizePortInput(' 443 ')).toBe('443')
  })

  it('portInServerRanges：listen 段优先，缺省回退 pub 段', () => {
    const server = {
      allowed_ports: [
        { pub_start: 1000, pub_end: 2000, listen_start: 1500, listen_end: 1600 },
        { pub_start: 5000, pub_end: 5100 },
      ],
    } as unknown as Server
    expect(portInServerRanges(server, 1550)).toBe(true)
    expect(portInServerRanges(server, 1200)).toBe(false)
    expect(portInServerRanges(server, 5050)).toBe(true)
    expect(portInServerRanges(server, 80)).toBe(false)
  })
})
