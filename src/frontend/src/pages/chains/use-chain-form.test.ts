import type { FormEvent } from 'react'
import { describe, expect, it, vi } from 'vitest'

import type { Chain, VirtualConfig } from '@/lib/types'

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

import { useChainForm } from './use-chain-form'

type Controller = ReturnType<typeof useChainForm>

function setup() {
  h.states.length = 0
  h.createChain.mockClear()
  h.editChain.mockClear()
  let controller!: Controller
  const render = () => {
    h.beginRender()
    controller = useChainForm({
      chains: [],
      nodes: [],
      servers: [],
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

describe('useChainForm hy2', () => {
  it('hy2 提交载荷：证书模式/混淆/带宽/跳跃段透传，矩阵外字段不携带，入口区块挂载 entry_node', async () => {
    const t = setup()
    t.act((c) =>
      c.patch({
        chainType: 'relay',
        entryId: '1',
        exitId: '2',
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
    const t = setup()
    t.act((c) => c.patch({ chainType: 'relay', entryId: '1', exitId: '2', protocol: 'hysteria' }))
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
    const t = setup()
    t.act((c) =>
      c.patch({
        entryId: '1',
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
    const t = setup()
    t.act((c) => c.openEdit(relayChain(hy2ServiceConfig(), entryRealityConfig())))
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
    const t = setup()
    t.act((c) => c.openEdit(relayChain(hy2ServiceConfig())))
    const form = t.controller.form
    expect(form.entryProtocolEnabled).toBe(false)
    expect(form.entryShortId).toBe('')
    expect(form.entryDest).toBe('dl.google.com:443')
    expect(form.entryServerNames).toBe('dl.google.com')
    expect(form.entryFingerprint).toBe('chrome')
  })

  it('编辑无端点存量链：入口区块锁定，直接勾选后提交也不携带 entry_node（终审修复 I-2）', async () => {
    const t = setup()
    t.act((c) => c.openEdit(relayChain(hy2ServiceConfig())))
    expect(t.controller.entryBlockLocked).toBe(true)
    // 模拟绕过禁用态直接勾选：提交载荷仍不得携带 entry_node（后端 400 兜底的前端镜像）。
    t.act((c) => c.patch({ entryProtocolEnabled: true }))
    await t.submit()
    const body = editBody()
    expect(body.entry_node).toBeUndefined()
  })

  it('编辑带入口区块（有端点）的链：入口区块不锁定，取消勾选提交不携带 entry_node', async () => {
    const t = setup()
    t.act((c) => c.openEdit(relayChain(hy2ServiceConfig(), entryRealityConfig())))
    expect(t.controller.entryBlockLocked).toBe(false)
    expect(t.controller.form.entryProtocolEnabled).toBe(true)
    t.act((c) => c.patch({ entryProtocolEnabled: false }))
    await t.submit()
    const body = editBody()
    expect(body.entry_node).toBeUndefined()
  })

  it('编辑回填：vless 出口链即使存在 entry_config 也不勾选入口区块（endpoint 即主协议）', () => {
    const t = setup()
    const vlessConfig = {
      protocol: 'vless',
      network: 'tcp',
      security: 'reality',
      template: {
        streamSettings: { realitySettings: { shortIds: ['aa'], dest: 'dl.google.com:443' } },
      },
    } as unknown as VirtualConfig
    t.act((c) => c.openEdit(relayChain(vlessConfig, entryRealityConfig())))
    const form = t.controller.form
    expect(form.protocol).toBe('vless')
    expect(form.entryProtocolEnabled).toBe(false)
  })

  it('vless 出口勾选入口区块时同样携带 entry_node（UI 对 hysteria/vless 出口开放该区块）', async () => {
    const t = setup()
    t.act((c) =>
      c.patch({
        chainType: 'relay',
        entryId: '1',
        exitId: '2',
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
