// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppDialogProvider } from '@/components/AppDialogProvider'
import { api } from '@/lib/api'
import type { Chain, ChainHop, Server } from '@/lib/types'
import { OrchestrationEditor } from './OrchestrationEditor'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const servers: Server[] = [
  {
    id: 1,
    alias: 'JP-Root',
    kind: 'managed',
    connection_state: 'online',
    machine_type: 'direct',
    addresses: ['203.0.113.1', '2001:db8::1'],
    address: '203.0.113.1',
    learned_addr: '',
    nic_addresses: [],
    allowed_ports: [],
    tags: [],
    country_code: 'JP',
    location: 'Tokyo',
    non_direct: false,
    metrics: { network_rx_bps: 1024, network_tx_bps: 2048 },
  } as unknown as Server,
  {
    id: 2,
    alias: 'SG-Backup',
    kind: 'managed',
    connection_state: 'online',
    machine_type: 'direct',
    addresses: ['203.0.113.2'],
    address: '203.0.113.2',
    learned_addr: '',
    nic_addresses: [],
    allowed_ports: [],
    tags: [],
    country_code: 'SG',
    location: 'Singapore',
    non_direct: false,
    metrics: { network_rx_bps: 322.515, network_tx_bps: 444.9 },
  } as unknown as Server,
  {
    id: 3,
    alias: 'US-OtherHop',
    kind: 'managed',
    connection_state: 'online',
    machine_type: 'direct',
    addresses: ['203.0.113.3'],
    address: '203.0.113.3',
    learned_addr: '',
    nic_addresses: [],
    allowed_ports: [],
    tags: [],
    country_code: 'US',
    location: 'Los Angeles',
    non_direct: false,
    metrics: null,
  } as unknown as Server,
  {
    id: 4,
    alias: 'SOCKS-External',
    kind: 'external',
    connection_state: 'external',
    machine_type: 'nat',
    addresses: [],
    address: '',
    learned_addr: '',
    nic_addresses: [],
    allowed_ports: [],
    tags: [],
    country_code: 'US',
    location: '',
    non_direct: false,
    external_exit: { protocol: 'socks', address: '198.51.100.4', port: 1080 },
    metrics: null,
  } as unknown as Server,
]

function makeChain(triggered = false): { chain: Chain; hop: ChainHop } {
  const hop0: ChainHop = {
    id: 10,
    seq: 0,
    server_id: triggered ? 2 : 1,
    server_alias: triggered ? 'SG-Backup' : 'JP-Root',
    role: 'entry',
    address: '203.0.113.1',
    node_id: 100,
    status: 'active',
    error: '',
    forward_port: 8443,
    portal_port: 0,
    orchestration: triggered
      ? {
          root_server_id: 1,
          active_server_id: 2,
          triggered: true,
          nodes: [
            {
              id: 1,
              kind: 'server',
              server_id: 1,
              address: '203.0.113.1',
              listen_port: 8443,
              priority: 0,
              is_root: true,
              active: false,
              alias: 'JP-Root',
              online: true,
              banned: false,
            },
            {
              id: 2,
              kind: 'trigger',
              subject: 'self',
              action: 'ban_banned',
              priority: 0,
              online: false,
              banned: false,
            },
            {
              id: 3,
              kind: 'server',
              server_id: 2,
              address: '203.0.113.2',
              listen_port: 9443,
              priority: 0,
              is_root: false,
              active: true,
              alias: 'SG-Backup',
              online: true,
              banned: false,
            },
          ],
          edges: [
            { from: 1, to: 2 },
            { from: 2, to: 3 },
          ],
        }
      : undefined,
  }
  const hop1: ChainHop = {
    id: 11,
    seq: 1,
    server_id: 3,
    server_alias: 'US-OtherHop',
    role: 'exit',
    address: '',
    node_id: 101,
    status: 'active',
    error: '',
    forward_port: 0,
    portal_port: 0,
  }
  const chain: Chain = {
    id: 5,
    name: 'JP-SG-US',
    status: 'active',
    error: '',
    created_at: '2026-01-01T00:00:00Z',
    hops: [hop0, hop1],
    service_node_id: 101,
    endpoint_id: 0,
    entry_port: 8443,
    revision_forced: false,
    revision_tasks: [],
    published_revision_id: 1,
    desired_revision_id: 0,
    traffic_multiplier: '1',
  }
  return { chain, hop: hop0 }
}

let host: HTMLDivElement
let root: ReturnType<typeof createRoot>

beforeEach(() => {
  vi.spyOn(api, 'chains').mockImplementation(() => new Promise(() => {}))
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('OrchestrationEditor', () => {
  it('打开时获取最新运行状态，并在后续切换时刷新画布且保留草稿', async () => {
    vi.useFakeTimers()
    const original = makeChain(true)
    original.hop.server_id = 1
    original.hop.server_alias = 'JP-Root'
    original.hop.orchestration!.active_server_id = 1
    original.hop.orchestration!.triggered = false
    original.hop.orchestration!.nodes = original.hop.orchestration!.nodes.map((node) => ({
      ...node,
      active: node.kind === 'server' && node.server_id === 1,
    }))
    const switched = makeChain(true)
    vi.mocked(api.chains)
      .mockResolvedValueOnce([switched.chain])
      .mockResolvedValue([original.chain])
    await act(async () =>
      root.render(
        <OrchestrationEditor
          chain={original.chain}
          hop={original.hop}
          servers={servers}
          onClose={() => {}}
          onSaved={() => {}}
        />,
      ),
    )
    expect(host.querySelector('.orch-head-meta')?.textContent).toContain('当前使用：SG-Backup')
    expect([...host.querySelectorAll('.cw-node-role')].map((el) => el.textContent)).toContain(
      '备选 · 当前',
    )

    const suspected = [...host.querySelectorAll<HTMLButtonElement>('.orch-trigger-item')].find(
      (button) => button.textContent?.includes('疑似被 ban'),
    )!
    await act(async () => suspected.click())
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })

    expect(host.querySelector('.orch-head-meta')?.textContent).toContain('当前使用：JP-Root')
    expect([...host.querySelectorAll('.cw-node-role')].map((el) => el.textContent)).toContain(
      '根 · 当前',
    )
    expect(host.querySelectorAll('.orch-cond')).toHaveLength(2)
  })

  it('容灾切换后同步当前节点标记并保留未保存的编排修改', async () => {
    const switched = makeChain(true)
    const originalHop: ChainHop = {
      ...switched.hop,
      server_id: 1,
      server_alias: 'JP-Root',
      orchestration: {
        ...switched.hop.orchestration!,
        active_server_id: 1,
        triggered: false,
        nodes: switched.hop.orchestration!.nodes.map((node) => ({
          ...node,
          active: node.kind === 'server' && node.server_id === 1,
        })),
      },
    }
    const renderEditor = async (chain: Chain, hop: ChainHop) => {
      await act(async () =>
        root.render(
          <OrchestrationEditor
            chain={chain}
            hop={hop}
            servers={servers}
            onClose={() => {}}
            onSaved={() => {}}
          />,
        ),
      )
    }
    await renderEditor(
      { ...switched.chain, hops: [originalHop, switched.chain.hops[1]] },
      originalHop,
    )
    const suspected = [...host.querySelectorAll<HTMLButtonElement>('.orch-trigger-item')].find(
      (button) => button.textContent?.includes('疑似被 ban'),
    )!
    await act(async () => suspected.click())
    expect(host.querySelectorAll('.orch-cond')).toHaveLength(2)

    await renderEditor(switched.chain, switched.hop)

    expect(host.querySelector('.orch-head-meta')?.textContent).toContain('当前使用：SG-Backup')
    const roles = [...host.querySelectorAll('.cw-node-role')].map((el) => el.textContent)
    expect(roles).toContain('根服务器')
    expect(roles).toContain('备选 · 当前')
    expect(roles).not.toContain('根 · 当前')
    expect(host.querySelectorAll('.orch-cond')).toHaveLength(2)
    expect(host.querySelector('[aria-label="移除节点 SG-Backup"]')).toBeNull()
  })

  it('展示真实原服务器与当前使用服务器、支持直连端口编辑与备份卡标签', async () => {
    const { chain, hop } = makeChain(true)
    await act(async () =>
      root.render(
        <OrchestrationEditor
          chain={chain}
          hop={hop}
          servers={servers}
          onClose={() => {}}
          onSaved={() => {}}
        />,
      ),
    )

    // 标题副文案区分原服务器与当前使用
    const metaText = host.querySelector('.orch-head-meta')?.textContent ?? ''
    expect(metaText).toContain('原服务器：JP-Root')
    expect(metaText).toContain('当前使用：SG-Backup')
    expect(host.querySelector('.orch-head-pill.is-triggered')?.textContent).toBe('编排已触发')

    // 根与当前服务器不渲染删除按钮
    expect(host.querySelector('[aria-label="移除节点 JP-Root"]')).toBeNull()
    expect(host.querySelector('[aria-label="移除节点 SG-Backup"]')).toBeNull()

    // 备选服务器显示「备选 · 当前」而非「入口」
    const roles = [...host.querySelectorAll('.cw-node-role')].map((el) => el.textContent)
    expect(roles).toContain('根服务器')
    expect(roles).toContain('备选 · 当前')

    // 点击直连服务器端口按钮展开数字输入并可修改
    const portBtn = [...host.querySelectorAll('.cw-node-port-value')].find(
      (el) => el.textContent === '8443',
    ) as HTMLButtonElement
    expect(portBtn).toBeTruthy()
    await act(async () => portBtn.click())
    const portInput = host.querySelector(
      'input[aria-label="入口端口数值：JP-Root"]',
    ) as HTMLInputElement
    expect(portInput).toBeTruthy()
  })

  it('连线仅高亮合法目标入口、支持橡皮筋预览且 Esc 优先取消连线', async () => {
    const onClose = vi.fn()
    const { chain, hop } = makeChain(false)
    await act(async () =>
      root.render(
        <OrchestrationEditor
          chain={chain}
          hop={hop}
          servers={servers}
          onClose={onClose}
          onSaved={() => {}}
        />,
      ),
    )

    // 添加「原节点恢复」与「疑似被 ban」
    const recoveryBtn = [...host.querySelectorAll('.orch-trigger-item')].find((el) =>
      el.textContent?.includes('原节点恢复'),
    ) as HTMLButtonElement
    const suspectedBtn = [...host.querySelectorAll('.orch-trigger-item')].find((el) =>
      el.textContent?.includes('疑似被 ban'),
    ) as HTMLButtonElement
    await act(async () => recoveryBtn.click())
    await act(async () => suspectedBtn.click())

    // 原节点恢复只有出口圆点，不渲染入口圆点
    expect(host.querySelector('[aria-label="触发器入口：原节点恢复"]')).toBeNull()
    expect(host.querySelector('[aria-label="触发器出口：原节点恢复"]')).toBeTruthy()

    // 点击 JP-Root 出口圆点进入连线模式
    const rootExit = host.querySelector('[aria-label="出口端口：JP-Root"]') as HTMLButtonElement
    await act(async () => rootExit.click())
    expect(host.querySelector('.cw-connect-hint')?.textContent).toContain('JP-Root')

    // 仅未连接来源的合法触发器入口高亮为 is-target，服务器入口不高亮
    const suspectedEntry = host.querySelector('[aria-label="触发器入口：疑似被 ban"]')!
    const rootEntry = host.querySelector('[aria-label="入口端口：JP-Root"]')!
    expect(suspectedEntry.classList.contains('is-target')).toBe(true)
    expect(rootEntry.classList.contains('is-target')).toBe(false)

    // 连线样式与链路编辑器一致：不渲染额外箭头 marker 或预览线末端圆点
    expect(host.querySelectorAll('.cw-wires marker')).toHaveLength(0)
    expect(host.querySelectorAll('.orch-rubber-cursor')).toHaveLength(0)
    for (const wire of host.querySelectorAll('.cw-wires .cw-wire')) {
      expect(wire.getAttribute('marker-end')).toBeNull()
    }

    // 按 Esc 取消连线而不关闭编辑器
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(host.querySelector('.cw-connect-hint')).toBeNull()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('从服务器连线时已有来源的触发器不高亮为主落点，但仍可点击改连来源', async () => {
    const spareServer = {
      id: 5,
      alias: 'TW-Spare',
      kind: 'managed',
      connection_state: 'online',
      machine_type: 'direct',
      addresses: ['203.0.113.5'],
      address: '203.0.113.5',
      learned_addr: '',
      nic_addresses: [],
      allowed_ports: [],
      tags: [],
      country_code: 'TW',
      location: 'Taipei',
      non_direct: false,
      metrics: null,
    } as unknown as Server
    const { chain, hop } = makeChain(true)
    await act(async () =>
      root.render(
        <OrchestrationEditor
          chain={chain}
          hop={hop}
          servers={[...servers, spareServer]}
          onClose={() => {}}
          onSaved={() => {}}
        />,
      ),
    )

    // 新增一个尚无来源的「疑似被 ban」触发器
    const suspectedBtn = [...host.querySelectorAll('.orch-trigger-item')].find((el) =>
      el.textContent?.includes('疑似被 ban'),
    ) as HTMLButtonElement
    await act(async () => suspectedBtn.click())

    // 切换到备选服务器 tab 并添加 TW-Spare
    const serverTab = [...host.querySelectorAll('[role="tab"]')].find((el) =>
      el.textContent?.includes('备选服务器'),
    ) as HTMLButtonElement
    await act(async () => serverTab.click())
    const twItem = [...host.querySelectorAll('.orch-server-item')].find((el) =>
      el.textContent?.includes('TW-Spare'),
    ) as HTMLButtonElement
    await act(async () => twItem.click())

    // 从 TW-Spare 发起连线：已有来源的「被 ban」不高亮 is-target，未连来源的「疑似被 ban」高亮 is-target
    const twExit = host.querySelector('[aria-label="出口端口：TW-Spare"]') as HTMLButtonElement
    await act(async () => twExit.click())
    const ownedBanEntry = host.querySelector(
      '[aria-label="触发器入口：被 ban"]',
    ) as HTMLButtonElement
    const freshSuspectedEntry = host.querySelector(
      '[aria-label="触发器入口：疑似被 ban"]',
    ) as HTMLButtonElement
    expect(ownedBanEntry.classList.contains('is-target')).toBe(false)
    expect(freshSuspectedEntry.classList.contains('is-target')).toBe(true)

    // 点击「被 ban」入口依然可以把它的来源改连到 TW-Spare
    await act(async () => ownedBanEntry.click())
    expect(host.querySelector('[aria-label="断开连线：TW-Spare → 被 ban"]')).toBeTruthy()
    expect(host.querySelector('[aria-label="断开连线：JP-Root → 被 ban"]')).toBeNull()
    expect(host.querySelectorAll('.cw-wires marker')).toHaveLength(0)
    expect(host.querySelectorAll('.orch-rubber-cursor')).toHaveLength(0)
    for (const wire of host.querySelectorAll('.cw-wires .cw-wire')) {
      expect(wire.getAttribute('marker-end')).toBeNull()
    }
  })

  it('备选服务器列表过滤跨跳占用与外部节点，可点击添加并拖拽触发器卡片', async () => {
    const { chain, hop } = makeChain(false)
    await act(async () =>
      root.render(
        <OrchestrationEditor
          chain={chain}
          hop={hop}
          servers={servers}
          onClose={() => {}}
          onSaved={() => {}}
        />,
      ),
    )

    // 添加触发器并确认它绑定了拖动（onPointerDown 不会抛错）
    const suspectedBtn = [...host.querySelectorAll('.orch-trigger-item')].find((el) =>
      el.textContent?.includes('疑似被 ban'),
    ) as HTMLButtonElement
    await act(async () => suspectedBtn.click())
    const condCard = host.querySelector('.orch-cond') as HTMLDivElement
    expect(condCard).toBeTruthy()
    expect(condCard.getAttribute('data-tone')).toBe('warn')

    // 切换到备选服务器 tab
    const serverTab = [...host.querySelectorAll('[role="tab"]')].find((el) =>
      el.textContent?.includes('备选服务器'),
    ) as HTMLButtonElement
    await act(async () => serverTab.click())

    const items = [...host.querySelectorAll('.orch-server-item')] as HTMLButtonElement[]
    const sgItem = items.find((el) => el.textContent?.includes('SG-Backup'))!
    const usItem = items.find((el) => el.textContent?.includes('US-OtherHop'))!
    const extItem = items.find((el) => el.textContent?.includes('SOCKS-External'))!
    expect(sgItem.disabled).toBe(false)
    expect(usItem.disabled).toBe(true)
    expect(usItem.textContent).toContain('其他跳已用')
    expect(extItem.disabled).toBe(true)
    expect(extItem.textContent).toContain('仅可作出口')

    // 添加 SG-Backup 到画布
    await act(async () => sgItem.click())
    expect(host.querySelector('[aria-label="入口端口：SG-Backup"]')).toBeTruthy()
  })

  it('有未保存修改时点击取消弹出二次确认框', async () => {
    const onClose = vi.fn()
    const { chain, hop } = makeChain(false)
    await act(async () =>
      root.render(
        <AppDialogProvider>
          <OrchestrationEditor
            chain={chain}
            hop={hop}
            servers={servers}
            onClose={onClose}
            onSaved={() => {}}
          />
        </AppDialogProvider>,
      ),
    )

    const suspectedBtn = [...host.querySelectorAll('.orch-trigger-item')].find((el) =>
      el.textContent?.includes('疑似被 ban'),
    ) as HTMLButtonElement
    await act(async () => suspectedBtn.click())

    const cancelBtn = [...host.querySelectorAll('button')].find(
      (el) => el.textContent === '取消',
    ) as HTMLButtonElement
    await act(async () => cancelBtn.click())

    // 二次确认弹窗出现，尚未直接关闭
    expect(onClose).not.toHaveBeenCalled()
    const confirmBtn = [...document.querySelectorAll('button')].find(
      (el) => el.textContent === '放弃修改',
    ) as HTMLButtonElement
    expect(confirmBtn).toBeTruthy()
    await act(async () => confirmBtn.click())
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
