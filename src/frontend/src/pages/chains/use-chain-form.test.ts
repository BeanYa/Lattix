import type { FormEvent } from 'react'
import { describe, expect, it, vi } from 'vitest'

import type { Chain, Server, VirtualConfig } from '@/lib/types'

import {
  EMPTY_FORM_TOPOLOGY,
  type CanvasNode,
  type ChainFormTopology,
} from './chain-wizard/chain-canvas'

// useChainForm 仅用 useState：以最小状态表替代 React 渲染器，在无 DOM 的 node 环境驱动 hook。
/* eslint-disable react-hooks/rules-of-hooks -- 测试以手动"渲染"循环调用 hook，无 React 渲染器可用 */
const h = vi.hoisted(() => {
  const states: unknown[] = []
  let cursor = 0
  return {
    states,
    beginRender: () => {
      cursor = 0
    },
    useState: (init: unknown) => {
      const i = cursor++
      if (i >= states.length) {
        states.push(typeof init === 'function' ? (init as () => unknown)() : init)
      }
      return [
        states[i],
        (next: unknown) => {
          states[i] =
            typeof next === 'function' ? (next as (prev: unknown) => unknown)(states[i]) : next
        },
      ] as const
    },
    createChain: vi.fn(async (_body: Record<string, unknown>) => ({ observeId: 'obs-create' })),
    editChain: vi.fn(async (_body: Record<string, unknown>) => ({ observeId: 'obs-edit' })),
  }
})

vi.mock('react', () => ({ useState: h.useState }))
vi.mock('@/lib/api', () => ({
  api: { createChain: h.createChain, editChain: h.editChain },
  errorMessage: (err: unknown) => String(err),
}))

import { parseChainEdit, useChainForm } from './use-chain-form'

type Controller = ReturnType<typeof useChainForm>

function directTopology(entryId = '1'): ChainFormTopology {
  return { ...EMPTY_FORM_TOPOLOGY, entryId }
}

function relayTopology(entryId = '1', exitId = '2'): ChainFormTopology {
  return { ...EMPTY_FORM_TOPOLOGY, chainType: 'relay', entryId, exitId }
}

function canvasNode(id: string, port = '', address = ''): CanvasNode {
  return { id: 'n' + id, serverId: Number(id), x: 0, y: 0, port, address }
}

/** 单链拓扑 → 一条路径（兼容既有单链用例）。 */
function topologyPaths(topology: ChainFormTopology): CanvasNode[][] {
  if (!topology.entryId) return []
  if (topology.chainType === 'direct') {
    return [[canvasNode(topology.entryId, topology.entryPort, topology.entryAddr)]]
  }
  return [
    [
      canvasNode(topology.entryId, topology.entryPort, topology.entryAddr),
      ...topology.middleIds.map((id, index) =>
        canvasNode(id, '', topology.middleAddrs[index] ?? ''),
      ),
      canvasNode(topology.exitId, topology.port, topology.exitAddr),
    ],
  ]
}

function setup(
  topology: ChainFormTopology = directTopology(),
  servers: Server[] = [],
  paths?: CanvasNode[][],
) {
  h.states.length = 0
  h.createChain.mockClear()
  h.editChain.mockClear()
  const resolvedPaths = paths ?? topologyPaths(topology)
  let controller!: Controller
  const render = () => {
    h.beginRender()
    controller = useChainForm({
      chains: [],
      servers,
      topology,
      paths: resolvedPaths,
      onError: () => {},
      onSaved: () => {},
      showOperation: () => {},
    })
  }
  render()
  return {
    get controller() {
      return controller
    },
    act(fn: (c: Controller) => void) {
      fn(controller)
      render()
    },
    async submit() {
      await controller.onSubmit({ preventDefault() {} } as unknown as FormEvent)
      render()
    },
  }
}

interface SubmittedBody {
  node: Record<string, unknown>
  entry_node?: Record<string, unknown>
  hops?: Array<Record<string, unknown>>
  auto_policy?: Record<string, unknown>
}

function createBody(): SubmittedBody {
  expect(h.createChain).toHaveBeenCalledOnce()
  return h.createChain.mock.calls[0][0] as unknown as SubmittedBody
}

function editBody(): SubmittedBody {
  expect(h.editChain).toHaveBeenCalledOnce()
  return h.editChain.mock.calls[0][0] as unknown as SubmittedBody
}

function hy2ServiceConfig(): VirtualConfig {
  return {
    protocol: 'hysteria',
    cert_mode: 'acme',
    obfs_password: 'stored-obfs',
    up_mbps: 30,
    down_mbps: 80,
    port_hop: '20000-20031',
    template: {},
  } as unknown as VirtualConfig
}

function entryRealityConfig(): VirtualConfig {
  return {
    protocol: 'vless',
    fingerprint: 'safari',
    template: {
      streamSettings: {
        realitySettings: {
          shortIds: ['ff00'],
          dest: 'www.amazon.com:443',
          serverNames: ['www.amazon.com'],
        },
      },
    },
  } as unknown as VirtualConfig
}

function relayChain(serviceConfig: VirtualConfig, entryConfig?: VirtualConfig | null): Chain {
  return {
    id: 7,
    name: 'hy2-chain',
    hops: [
      { server_id: 1, address: '' },
      { server_id: 2, address: '' },
    ],
    service_node_id: 99,
    entry_port: 1443,
    // 入口区块存在 ⟺ 链挂共享入口端点（后端 toChainDTO：entry_config 由 endpoint 回填）。
    endpoint_id: entryConfig ? 5 : 0,
    traffic_multiplier: '1.000',
    service_config: serviceConfig,
    entry_config: entryConfig,
  } as unknown as Chain
}

function managedServer(id: number): Server {
  return {
    id,
    alias: `managed-${id}`,
    kind: 'managed',
    machine_type: 'direct',
    allowed_ports: [],
    addresses: [],
  } as unknown as Server
}

function externalServer(id = 2): Server {
  return {
    id,
    alias: `ext-${id}`,
    kind: 'external',
    machine_type: 'nat',
    allowed_ports: [],
    addresses: [],
    external_exit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
  } as unknown as Server
}

function externalChain(overrides: Partial<Chain> = {}): Chain {
  return {
    id: 8,
    name: 'ext-chain',
    hops: [
      { server_id: 1, address: '' },
      { server_id: 2, address: '' },
    ],
    service_node_id: 0,
    entry_port: 1443,
    endpoint_id: 5,
    traffic_multiplier: '1.000',
    service_external: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
    entry_config: entryRealityConfig(),
    ...overrides,
  } as unknown as Chain
}

function beginEditAct(t: ReturnType<typeof setup>, chain: Chain) {
  t.act((c) => {
    const parsed = parseChainEdit(chain, [])
    if (!parsed.ok) throw new Error(parsed.error)
    c.beginEdit(chain, parsed.value)
  })
}

describe('useChainForm hy2', () => {
  it('hy2 提交载荷：证书模式/混淆/带宽/跳跃段透传，矩阵外字段不携带，入口区块挂载 entry_node', async () => {
    const t = setup(relayTopology())
    t.act((c) =>
      c.patch({
        protocol: 'hysteria',
        certMode: 'selfsign',
        tlsDomain: 'cdn.example.com',
        obfsPassword: 'obfs-1',
        upMbps: '50',
        downMbps: '100',
        portHop: 'off',
        entryProtocolEnabled: true,
        entryShortId: 'abcd1234',
        entryDest: 'dl.google.com:443',
        entryServerNames: 'dl.google.com, cdn.example.com',
        entryFingerprint: 'firefox',
      }),
    )
    expect(t.controller.isHy2).toBe(true)
    await t.submit()
    const body = createBody()
    expect(body.node).toMatchObject({
      protocol: 'hysteria',
      cert_mode: 'selfsign',
      tls_domain: 'cdn.example.com',
      obfs_password: 'obfs-1',
      up_mbps: 50,
      down_mbps: 100,
      port_hop: 'off',
    })
    expect(body.node.network).toBeUndefined()
    expect(body.node.security).toBeUndefined()
    expect(body.node.flow).toBeUndefined()
    expect(body.node.encryption).toBeUndefined()
    expect(body.node.fingerprint).toBeUndefined()
    expect(body.entry_node).toEqual({
      protocol: 'vless',
      security: 'reality',
      short_id: 'abcd1234',
      dest: 'dl.google.com:443',
      server_names: ['dl.google.com', 'cdn.example.com'],
      fingerprint: 'firefox',
    })
  })

  it('hy2 默认值：带宽默认 50/100 透传；obfs/port_hop 留空不携带；未勾选入口区块不带 entry_node', async () => {
    const t = setup(relayTopology())
    t.act((c) => c.patch({ protocol: 'hysteria' }))
    await t.submit()
    const body = createBody()
    expect(body.node).toMatchObject({
      protocol: 'hysteria',
      cert_mode: 'selfsign',
      up_mbps: 50,
      down_mbps: 100,
    })
    expect(body.node.obfs_password).toBeUndefined()
    expect(body.node.port_hop).toBeUndefined()
    expect(body.node.tls_domain).toBeUndefined()
    expect(body.entry_node).toBeUndefined()
  })

  it('hy2 显式跳跃段透传；单跳不携带 entry_node（入口区块仅多跳）', async () => {
    const t = setup(directTopology())
    t.act((c) =>
      c.patch({
        protocol: 'hysteria',
        portHop: '30000-30015',
        entryProtocolEnabled: true,
      }),
    )
    await t.submit()
    const body = createBody()
    expect(body.node.port_hop).toBe('30000-30015')
    expect(body.entry_node).toBeUndefined()
  })

  it('编辑回填：service_config(hysteria) → hy2 字段；entry_config 存在 → 入口区块勾选与子参数回填', async () => {
    const t = setup(relayTopology())
    const chain = relayChain(hy2ServiceConfig(), entryRealityConfig())
    beginEditAct(t, chain)
    const form = t.controller.form
    expect(form.protocol).toBe('hysteria')
    expect(form.certMode).toBe('acme')
    expect(form.obfsPassword).toBe('stored-obfs')
    expect(form.upMbps).toBe('30')
    expect(form.downMbps).toBe('80')
    expect(form.portHop).toBe('20000-20031')
    expect(form.entryProtocolEnabled).toBe(true)
    expect(form.entryShortId).toBe('ff00')
    expect(form.entryDest).toBe('www.amazon.com:443')
    expect(form.entryServerNames).toBe('www.amazon.com')
    expect(form.entryFingerprint).toBe('safari')

    await t.submit()
    expect(h.editChain).toHaveBeenCalledOnce()
    const body = editBody()
    expect(body.node).toMatchObject({
      protocol: 'hysteria',
      cert_mode: 'acme',
      obfs_password: 'stored-obfs',
      up_mbps: 30,
      down_mbps: 80,
      port_hop: '20000-20031',
    })
    expect(body.entry_node).toMatchObject({
      protocol: 'vless',
      security: 'reality',
      short_id: 'ff00',
      dest: 'www.amazon.com:443',
      server_names: ['www.amazon.com'],
      fingerprint: 'safari',
    })
  })

  it('编辑回填：hy2 链无 entry_config → 入口区块不勾选，字段取默认', () => {
    const t = setup(relayTopology())
    beginEditAct(t, relayChain(hy2ServiceConfig()))
    const form = t.controller.form
    expect(form.entryProtocolEnabled).toBe(false)
    expect(form.entryShortId).toBe('')
    expect(form.entryDest).toBe('dl.google.com:443')
    expect(form.entryServerNames).toBe('dl.google.com')
    expect(form.entryFingerprint).toBe('chrome')
  })

  it('编辑无端点存量链：入口区块锁定，直接勾选后提交也不携带 entry_node（终审修复 I-2）', async () => {
    const t = setup(relayTopology())
    beginEditAct(t, relayChain(hy2ServiceConfig()))
    expect(t.controller.entryBlockLocked).toBe(true)
    // 模拟绕过禁用态直接勾选：提交载荷仍不得携带 entry_node（后端 400 兜底的前端镜像）。
    t.act((c) => c.patch({ entryProtocolEnabled: true }))
    await t.submit()
    const body = editBody()
    expect(body.entry_node).toBeUndefined()
  })

  it('编辑带入口区块（有端点）的链：入口区块不锁定，取消勾选提交不携带 entry_node', async () => {
    const t = setup(relayTopology())
    beginEditAct(t, relayChain(hy2ServiceConfig(), entryRealityConfig()))
    expect(t.controller.entryBlockLocked).toBe(false)
    expect(t.controller.form.entryProtocolEnabled).toBe(true)
    t.act((c) => c.patch({ entryProtocolEnabled: false }))
    await t.submit()
    const body = editBody()
    expect(body.entry_node).toBeUndefined()
  })

  it('编辑回填：vless 出口链即使存在 entry_config 也不勾选入口区块（endpoint 即主协议）', () => {
    const t = setup(relayTopology())
    const vlessConfig = {
      protocol: 'vless',
      network: 'tcp',
      security: 'reality',
      template: {
        streamSettings: { realitySettings: { shortIds: ['aa'], dest: 'dl.google.com:443' } },
      },
    } as unknown as VirtualConfig
    beginEditAct(t, relayChain(vlessConfig, entryRealityConfig()))
    const form = t.controller.form
    expect(form.protocol).toBe('vless')
    expect(form.entryProtocolEnabled).toBe(false)
  })

  it('vless 出口勾选入口区块时同样携带 entry_node（UI 对 hysteria/vless 出口开放该区块）', async () => {
    const t = setup(relayTopology())
    t.act((c) =>
      c.patch({
        protocol: 'vless',
        entryProtocolEnabled: true,
        entryShortId: 'ee11',
      }),
    )
    await t.submit()
    const body = createBody()
    expect(body.entry_node).toMatchObject({
      protocol: 'vless',
      security: 'reality',
      short_id: 'ee11',
      dest: 'dl.google.com:443',
      server_names: ['dl.google.com'],
      fingerprint: 'chrome',
    })
  })
})

describe('useChainForm 外部直链出口', () => {
  it('创建：提交最小占位 node 且恒提交 entry_node（出口表单不参与校验/提交）', async () => {
    const t = setup(relayTopology('1', '2'), [managedServer(1), externalServer(2)])
    t.act((c) =>
      c.patch({
        protocol: 'hysteria',
        certMode: 'acme',
        obfsPassword: 'should-not-submit',
        portHop: '20000-20031',
        shortId: 'aa11',
      }),
    )
    expect(t.controller.exitIsExternal).toBe(true)
    expect(t.controller.externalExit?.protocol).toBe('socks')
    await t.submit()
    expect(h.createChain).toHaveBeenCalledOnce()
    const body = createBody()
    expect(body.node).toEqual({ name: expect.any(String), server_id: 2, protocol: 'vless' })
    expect(body.node.port).toBeUndefined()
    expect(body.node.cert_mode).toBeUndefined()
    expect(body.node.obfs_password).toBeUndefined()
    expect(body.node.port_hop).toBeUndefined()
    expect(body.node.network).toBeUndefined()
    // 入口服务器 A 与普通链路一样携带完整 VLESS 协议细节（传输/安全层/flow）。
    expect(body.entry_node).toMatchObject({
      protocol: 'vless',
      network: 'tcp',
      security: 'reality',
      flow: 'xtls-rprx-vision',
      short_id: 'aa11',
      dest: 'dl.google.com:443',
      server_names: ['dl.google.com'],
      fingerprint: 'chrome',
    })
  })

  it('创建：外部节点不是末位 → 与后端一致文案，拒绝提交', async () => {
    const topology: ChainFormTopology = {
      ...EMPTY_FORM_TOPOLOGY,
      chainType: 'relay',
      entryId: '1',
      middleIds: ['2'],
      exitId: '3',
    }
    const t = setup(topology, [managedServer(1), externalServer(2), managedServer(3)])
    await t.submit()
    expect(h.createChain).not.toHaveBeenCalled()
    expect(t.controller.createError).toBe('未托管服务器 ext-2 只能作为链路的最后一跳（出口）')
  })

  it('创建：单个外部节点（外部节点作入口）→ 拒绝提交', async () => {
    const t = setup(directTopology('2'), [externalServer(2)])
    await t.submit()
    expect(h.createChain).not.toHaveBeenCalled()
    expect(t.controller.createError).toBe('未托管服务器 ext-2 只能作为链路的最后一跳（出口）')
  })

  it('编辑：不因 service_config 缺失报错，回填入口参数并按占位 node 提交', async () => {
    const t = setup(relayTopology('1', '2'), [managedServer(1), externalServer(2)])
    const chain = externalChain()
    const parsed = parseChainEdit(chain, [])
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return
    expect(parsed.value.exitPort).toBe('')
    expect(parsed.value.details.entryProtocolEnabled).toBe(true)
    expect(parsed.value.details.entryShortId).toBe('ff00')
    t.act((c) => c.beginEdit(chain, parsed.value))
    await t.submit()
    expect(h.editChain).toHaveBeenCalledOnce()
    const body = editBody()
    expect(body.node).toEqual({ name: expect.any(String), server_id: 2, protocol: 'vless' })
    expect(body.node.port).toBeUndefined()
    expect(body.entry_node).toMatchObject({
      protocol: 'vless',
      network: 'tcp',
      security: 'reality',
      flow: 'xtls-rprx-vision',
      short_id: 'ff00',
      dest: 'www.amazon.com:443',
      server_names: ['www.amazon.com'],
      fingerprint: 'safari',
    })
  })

  it('编辑：忽略 entryBlockLocked，仍提交 entry_node', async () => {
    const t = setup(relayTopology('1', '2'), [managedServer(1), externalServer(2)])
    const chain = externalChain({ endpoint_id: 0 })
    const parsed = parseChainEdit(chain, [])
    if (!parsed.ok) throw new Error(parsed.error)
    t.act((c) => c.beginEdit(chain, parsed.value))
    expect(t.controller.entryBlockLocked).toBe(true)
    await t.submit()
    expect(editBody().entry_node).toMatchObject({ protocol: 'vless', security: 'reality' })
  })
})

describe('useChainForm 多链提交', () => {
  interface MultiBody {
    name: string
    hops: { server_id: number; address?: string }[]
    entry_port?: number
    node: Record<string, unknown>
  }

  function multiPaths(): CanvasNode[][] {
    return [
      [canvasNode('1', '1443'), canvasNode('2', '8443')],
      [canvasNode('1', '1444'), canvasNode('3')],
      [canvasNode('4', '1445')],
    ]
  }

  it('按顺序逐条创建：名称基准-1..N，每条链的 hops 与首尾端口独立', async () => {
    const topology: ChainFormTopology = {
      ...EMPTY_FORM_TOPOLOGY,
      chainType: 'relay',
      entryId: '1',
      exitId: '2',
    }
    const t = setup(topology, [], multiPaths())
    t.act((c) => c.patch({ name: '批量链' }))
    expect(t.controller.chainCount).toBe(3)
    await t.submit()
    expect(h.createChain).toHaveBeenCalledTimes(3)
    const bodies = h.createChain.mock.calls.map((call) => call[0] as unknown as MultiBody)
    expect(bodies.map((body) => body.name)).toEqual(['批量链-1', '批量链-2', '批量链-3'])
    // 链 1：入口 1443 / 出口节点端口 8443。
    expect(bodies[0].hops).toEqual([{ server_id: 1 }, { server_id: 2 }])
    expect(bodies[0].entry_port).toBe(1443)
    expect(bodies[0].node).toMatchObject({ server_id: 2, port: 8443 })
    // 链 2：入口 1444 / 出口端口留空（自动分配）。
    expect(bodies[1].hops).toEqual([{ server_id: 1 }, { server_id: 3 }])
    expect(bodies[1].entry_port).toBe(1444)
    expect(bodies[1].node.port).toBeUndefined()
    // 链 3：单节点直连，入口端口复用为节点端口。
    expect(bodies[2].hops).toEqual([{ server_id: 4 }])
    expect(bodies[2].entry_port).toBe(1445)
    expect(bodies[2].node).toMatchObject({ server_id: 4, port: 1445 })
  })

  it('名称为空时沿用随机基准名再拼序号', async () => {
    const topology: ChainFormTopology = {
      ...EMPTY_FORM_TOPOLOGY,
      chainType: 'relay',
      entryId: '1',
      exitId: '2',
    }
    const t = setup(topology, [], multiPaths())
    await t.submit()
    const bodies = h.createChain.mock.calls.map((call) => call[0] as unknown as MultiBody)
    expect(bodies[0].name).toMatch(/^Chain #[A-Za-z]{4}-1$/)
    expect(bodies[2].name).toMatch(/-3$/)
  })

  it('编辑模式遇到多路径：拒绝提交并提示新建链路', async () => {
    const topology: ChainFormTopology = {
      ...EMPTY_FORM_TOPOLOGY,
      chainType: 'relay',
      entryId: '1',
      exitId: '2',
    }
    const t = setup(topology, [], multiPaths())
    const chain = relayChain(hy2ServiceConfig(), entryRealityConfig())
    const parsed = parseChainEdit(chain, [])
    if (!parsed.ok) throw new Error(parsed.error)
    t.act((c) => c.beginEdit(chain, parsed.value))
    await t.submit()
    expect(h.editChain).not.toHaveBeenCalled()
    expect(t.controller.createError).toBe('编辑模式仅支持单条链路，多路径请新建链路')
  })
})

describe('useChainForm 跳位备选池与自动编排策略', () => {
  it('节点携带 candidates 且启用自动编排时，提交载荷包含 candidates 与 auto_policy', async () => {
    const path: CanvasNode[] = [
      {
        id: 'n1',
        serverId: 1,
        x: 0,
        y: 0,
        port: '1443',
        address: '',
        candidates: [
          { serverId: 3, address: '', port: '' },
          { serverId: 4, address: '203.0.113.4', port: '' },
        ],
      },
      { id: 'n2', serverId: 2, x: 0, y: 0, port: '8443', address: '' },
    ]
    const t = setup(
      relayTopology('1', '2'),
      [managedServer(1), managedServer(2), managedServer(3), managedServer(4)],
      [path],
    )
    t.act((c) =>
      c.patch({
        autoEnabled: true,
        onBan: true,
        banTriggerLevel: 'suspected',
        unreachableStreak: '4',
        failbackMode: 'preemptive',
        cooldownSec: '600',
      }),
    )
    await t.submit()
    const body = createBody()
    expect(body.hops).toEqual([
      {
        server_id: 1,
        candidates: [
          { server_id: 3, priority: 1, enabled: true },
          { server_id: 4, priority: 2, address: '203.0.113.4', enabled: true },
        ],
      },
      { server_id: 2 },
    ])
    expect(body.auto_policy).toEqual({
      auto_enabled: true,
      on_ban: true,
      ban_trigger_level: 'suspected',
      on_unreachable: true,
      unreachable_streak: 4,
      on_quota_exhausted: true,
      on_agent_offline: true,
      offline_timeout_sec: 180,
      failback_mode: 'preemptive',
      cooldown_sec: 600,
    })
  })
})
