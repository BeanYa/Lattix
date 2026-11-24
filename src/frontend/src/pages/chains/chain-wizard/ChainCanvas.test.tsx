// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Server } from '@/lib/types'
import { INGRESS_NODE_ID, INTERNET_NODE_ID } from './chain-canvas'
import { ChainCanvas } from './ChainCanvas'
import { useChainCanvas, type ChainCanvasController } from './use-chain-canvas'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const servers = [1, 2].map(
  (id) =>
    ({
      id,
      alias: id === 1 ? 'HK' : 'SG',
      country_code: id === 1 ? 'HK' : 'SG',
      machine_type: 'direct',
      addresses: ['203.0.113.10'],
      allowed_ports: [],
    }) as unknown as Server,
)
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let controller: ChainCanvasController
function Harness({ show = true }: { show?: boolean }) {
  controller = useChainCanvas({ servers })
  return show ? (
    <ChainCanvas
      controller={controller}
      probesByServer={new Map()}
      probeTitle={null}
      linkColor="var(--panel-line-2)"
    />
  ) : null
}
const click = async (label: string) => {
  const element = host.querySelector(`[aria-label="${label}"]`) as HTMLElement
  expect(element, label).toBeTruthy()
  await act(async () => element.dispatchEvent(new MouseEvent('click', { bubbles: true })))
}
beforeEach(async () => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root.render(<Harness />))
  const canvas = host.querySelector('.cw-canvas')!
  Object.defineProperties(canvas, {
    setPointerCapture: { value: () => {} },
    hasPointerCapture: { value: () => false },
  })
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('canvas orchestration entry', () => {
  it('编辑模式在卡片上方渲染自动编排入口，点击回调带服务器 ID', async () => {
    const calls: number[] = []
    function EntryHarness() {
      controller = useChainCanvas({ servers })
      return (
        <ChainCanvas
          controller={controller}
          probesByServer={new Map()}
          probeTitle={null}
          linkColor="var(--panel-line-2)"
          orchestrationFor={() => undefined}
          onEditOrchestration={(serverId) => calls.push(serverId)}
        />
      )
    }
    await act(async () => root.render(<EntryHarness />))
    await act(async () => controller.addNode(1, 200, 200))
    const entry = host.querySelector('.cw-node-orch') as HTMLElement
    expect(entry).toBeTruthy()
    expect(entry.getAttribute('aria-label')).toBe('自动编排：HK')
    expect(entry.hasAttribute('disabled')).toBe(false)
    await act(async () => entry.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(calls).toEqual([1])
  })

  it('新建链路：入口保留但禁用，并说明保存后可配置', async () => {
    const calls: number[] = []
    function LockedHarness() {
      controller = useChainCanvas({ servers })
      return (
        <ChainCanvas
          controller={controller}
          probesByServer={new Map()}
          probeTitle={null}
          linkColor="var(--panel-line-2)"
          onEditOrchestration={(serverId) => calls.push(serverId)}
          orchestrationLocked
        />
      )
    }
    await act(async () => root.render(<LockedHarness />))
    await act(async () => controller.addNode(1, 200, 200))
    const entry = host.querySelector('.cw-node-orch') as HTMLElement
    expect(entry).toBeTruthy()
    expect(entry.hasAttribute('disabled')).toBe(true)
    expect(entry.getAttribute('title')).toBe('保存链路后可配置自动编排')
    await act(async () => entry.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(calls).toEqual([])
  })

  it('没有编排回调时不渲染入口', async () => {
    await act(async () => root.render(<Harness />))
    await act(async () => controller.addNode(1, 200, 200))
    expect(host.querySelector('.cw-node-orch')).toBeNull()
  })
})

describe('rebuilt topology canvas interactions', () => {
  it('每条正式连线只有一个报文，跟随路径更新；预览不增加报文，断开即移除', async () => {
    await act(async () => {
      controller.addNode(1, 380, 260)
      controller.addNode(2, 740, 400)
    })
    const packets = () => host.querySelectorAll<SVGGElement>('.cw-wire-packet')
    expect(packets()).toHaveLength(2)
    await click('出口端口：HK')
    await click('入口端口：SG')
    const wire = host.querySelector('[aria-label="断开连线：HK → SG"]')!
    const packet = wire.parentElement!.querySelector<SVGGElement>('.cw-wire-packet')!
    const oldPath = wire.getAttribute('d')!
    expect(packet.style.offsetPath).toBe(`path("${oldPath}")`)
    await act(async () => controller.moveNode('n2', 800, -100))
    const updatedPath = wire.getAttribute('d')!
    expect(updatedPath).not.toBe(oldPath)
    expect(packet.style.offsetPath).toBe(`path("${updatedPath}")`)
    await click('出口端口：HK')
    await act(async () => controller.moveConnecting(1200, 300))
    expect(host.querySelector('.cw-wire-rubber')).toBeTruthy()
    expect(packets()).toHaveLength(2)
    await click('断开连线：HK → SG')
    expect(packets()).toHaveLength(1)
  })
  it('切换到后台时暂停报文动效，回到页面后继续', async () => {
    try {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
      await act(async () => document.dispatchEvent(new Event('visibilitychange')))
      expect(host.querySelector('.cw-canvas')!.classList.contains('is-motion-paused')).toBe(true)
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
      await act(async () => document.dispatchEvent(new Event('visibilitychange')))
      expect(host.querySelector('.cw-canvas')!.classList.contains('is-motion-paused')).toBe(false)
    } finally {
      Reflect.deleteProperty(document, 'visibilityState')
    }
  })
  it('返回拓扑重新挂载时保留上一次视角', async () => {
    await act(async () => controller.setViewport({ x: -230, y: 170, zoom: 0.75 }))
    await act(async () => root.render(<Harness show={false} />))
    await act(async () => root.render(<Harness />))
    expect(controller.viewport).toEqual({ x: -230, y: 170, zoom: 0.75 })
  })
  it('背景拖动期间第二指针不能抢走拖动或启动节点拖动', async () => {
    await act(async () => controller.addNode(1, 380, 260))
    const canvas = host.querySelector('.cw-canvas')!
    const card = host.querySelector('.cw-node') as HTMLElement
    card.setPointerCapture = () => {}
    const before = { ...controller.viewport },
      node = controller.nodes[0]
    await act(async () => {
      canvas.dispatchEvent(
        new PointerEvent('pointerdown', {
          bubbles: true,
          pointerId: 1,
          button: 0,
          clientX: 300,
          clientY: 300,
        }),
      )
      card.dispatchEvent(
        new PointerEvent('pointerdown', {
          bubbles: true,
          pointerId: 2,
          button: 0,
          clientX: 400,
          clientY: 400,
        }),
      )
      card.dispatchEvent(
        new PointerEvent('pointermove', {
          bubbles: true,
          pointerId: 2,
          clientX: 500,
          clientY: 500,
        }),
      )
      canvas.dispatchEvent(
        new PointerEvent('pointerdown', {
          bubbles: true,
          pointerId: 3,
          button: 0,
          clientX: 900,
          clientY: 900,
        }),
      )
      canvas.dispatchEvent(
        new PointerEvent('pointermove', {
          bubbles: true,
          pointerId: 1,
          clientX: 350,
          clientY: 340,
        }),
      )
      canvas.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 1 }))
    })
    expect(controller.nodes[0]).toEqual(node)
    expect(controller.viewport).toEqual({ ...before, x: before.x + 50, y: before.y + 40 })
  })
  it('HK出口开始重连、取消和完成都保留入网流量线和入口身份', async () => {
    await act(async () => {
      controller.addNode(1, 380, 260)
      controller.addNode(2, 740, 400)
    })
    await click('出口端口：HK')
    expect(controller.edges).toContainEqual({ from: INGRESS_NODE_ID, to: 'n1' })
    expect(host.querySelector('[aria-label="断开连线：入网流量 → HK"]')).toBeTruthy()
    expect(controller.roleOf('n1')).toBe('entry')
    await act(async () =>
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
    )
    expect(controller.connecting).toBeNull()
    expect(controller.edges).toHaveLength(2)
    await click('出口端口：HK')
    await click('入口端口：SG')
    expect(controller.edges).toContainEqual({ from: 'n1', to: 'n2' })
    expect(controller.edges).toContainEqual({ from: INGRESS_NODE_ID, to: 'n1' })
    await click('断开连线：HK → SG')
    expect(controller.edges).toEqual([{ from: INGRESS_NODE_ID, to: 'n1' }])
    expect(controller.roleOf('n1')).toBe('entry')
  })
  it('拖动空白只平移视角，拓扑与节点世界坐标不变', async () => {
    await act(async () => controller.addNode(1, 380, 260))
    const before = { ...controller.viewport },
      nodes = controller.nodes,
      edges = controller.edges
    const canvas = host.querySelector('.cw-canvas')!
    await act(async () => {
      canvas.dispatchEvent(
        new PointerEvent('pointerdown', {
          bubbles: true,
          button: 0,
          pointerId: 1,
          clientX: 500,
          clientY: 500,
        }),
      )
      canvas.dispatchEvent(
        new PointerEvent('pointermove', {
          bubbles: true,
          pointerId: 1,
          clientX: 620,
          clientY: 420,
        }),
      )
      canvas.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 1 }))
    })
    expect(controller.viewport).toEqual({ ...before, x: before.x + 120, y: before.y - 80 })
    expect(controller.nodes).toBe(nodes)
    expect(controller.edges).toBe(edges)
  })
  it('在平移和缩放后拖入节点，正确换算世界坐标', async () => {
    await act(async () => controller.setViewport({ x: 100, y: 50, zoom: 0.5 }))
    const event = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperties(event, {
      clientX: { value: 400 },
      clientY: { value: 300 },
      dataTransfer: { value: { getData: () => '1' } },
    })
    await act(async () => host.querySelector('.cw-canvas')!.dispatchEvent(event))
    expect(controller.nodes[0]).toMatchObject({ x: 468, y: 400 }) // ((400-100)/.5-132, (300-50)/.5-100)
  })
  it('滚轮平移和缩放控件不修改图，适应全部恢复可见视角', async () => {
    const before = { ...controller.viewport }
    await act(async () =>
      host
        .querySelector('.cw-canvas')!
        .dispatchEvent(
          new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaX: 20, deltaY: 60 }),
        ),
    )
    expect(controller.viewport).toMatchObject({ x: before.x - 20, y: before.y - 60 })
    await click('放大画布')
    expect(controller.viewport.zoom).toBeGreaterThan(before.zoom)
    await click('适应全部节点')
    expect(controller.edges).toEqual([{ from: INGRESS_NODE_ID, to: INTERNET_NODE_ID }])
    expect(host.textContent).toContain('入网流量')
    expect(host.textContent).toContain('Internet')
  })
  it('多入边汇合允许；形成环路时提示并保留原线，取消可恢复空闲', async () => {
    await act(async () => {
      controller.addNode(1, 380, 260)
      controller.addNode(2, 740, 400)
    })
    await click('出口端口：HK')
    await click('入口端口：SG')
    expect(controller.edges).toContainEqual({ from: 'n1', to: 'n2' })
    await click('出口端口：SG')
    await click('入口端口：HK')
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('环路')
    expect(controller.edges).toContainEqual({ from: 'n1', to: 'n2' })
    await act(async () => controller.cancelConnect())
    expect(host.querySelector('[role="alert"]')).toBeNull()
  })

  it('回填含已删除服务器的链路时渲染缺失卡，支持原地替换保留连线或直接移除节点', async () => {
    await act(async () => {
      controller.prefill(
        [
          { id: 'n1', serverId: 1, x: 100, y: 200, port: '443', address: '' },
          { id: 'n2', serverId: 999, x: 460, y: 200, port: '', address: '' },
        ],
        [
          { from: INGRESS_NODE_ID, to: 'n1' },
          { from: 'n1', to: 'n2' },
          { from: 'n2', to: INTERNET_NODE_ID },
        ],
      )
    })
    expect(host.querySelector('.cw-node.is-missing')?.textContent).toContain('已删除服务器 #999')
    expect(host.querySelector('[aria-label="断开连线：HK → 已删除服务器 #999"]')).toBeTruthy()
    expect(controller.validation.issues.some((i) => i.code === 'server_missing')).toBe(true)

    // 原地替换成 SG (id=2)，连线保持完整且校验转绿
    await act(async () =>
      host
        .querySelector('[aria-label="替换已删除服务器：#999"]')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true })),
    )
    const option = [...document.querySelectorAll('[role="option"]')].find((el) =>
      el.textContent?.includes('SG'),
    )!
    await act(async () => option.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(host.querySelector('.cw-node.is-missing')).toBeNull()
    expect(host.querySelector('[aria-label="断开连线：HK → SG"]')).toBeTruthy()
    expect(host.querySelector('[aria-label="断开连线：SG → Internet"]')).toBeTruthy()
    expect(controller.validation.valid).toBe(true)

    // 再次回填缺失节点并测试直接移除：节点和前后连线一并清除
    await act(async () => {
      controller.prefill(
        [
          { id: 'n1', serverId: 1, x: 100, y: 200, port: '443', address: '' },
          { id: 'n2', serverId: 999, x: 460, y: 200, port: '', address: '' },
        ],
        [
          { from: INGRESS_NODE_ID, to: 'n1' },
          { from: 'n1', to: 'n2' },
          { from: 'n2', to: INTERNET_NODE_ID },
        ],
      )
    })
    await click('移除节点 #999')
    expect(controller.nodes).toHaveLength(1)
    expect(controller.edges).toEqual([{ from: INGRESS_NODE_ID, to: 'n1' }])
  })
})

it('drops a template graph at current world coordinates after panning and zooming', async () => {
  function TemplateHarness() {
    controller = useChainCanvas({ servers, templates: templateCatalog })
    return (
      <ChainCanvas
        controller={controller}
        probesByServer={new Map()}
        probeTitle={null}
        linkColor="var(--panel-line-2)"
      />
    )
  }
  const template = {
    id: 8,
    revision: 1,
    name: 'HK auto',
    usage_count: 0,
    created_at: '',
    updated_at: '',
    nodes: [{ kind: 'server' as const, server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
  }
  const templateCatalog = [template]
  // A stable catalog reference models the fetched page catalog.
  await act(async () => root.render(<TemplateHarness />))
  await act(async () => controller.setViewport({ x: 100, y: 50, zoom: 0.5 }))
  const event = new Event('drop', { bubbles: true, cancelable: true })
  Object.defineProperties(event, {
    clientX: { value: 400 },
    clientY: { value: 300 },
    dataTransfer: {
      value: {
        types: ['text/x-lattix-orchestration-template'],
        getData: (mime: string) => (mime === 'text/x-lattix-orchestration-template' ? '8' : ''),
      },
    },
  })
  await act(async () => host.querySelector('.cw-canvas')!.dispatchEvent(event))
  expect(controller.nodes[0]).toMatchObject({
    x: 468,
    y: 400,
    serverId: 1,
    port: '8443',
    orchestration: { template_id: 8, template_revision: 1 },
  })
})
