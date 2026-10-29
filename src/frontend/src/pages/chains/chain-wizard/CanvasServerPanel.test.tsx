// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it } from 'vitest'

import type { CatalogProbeRow, Server } from '@/lib/types'
import { CanvasServerPanel } from './CanvasServerPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const server = {
  id: 1,
  alias: 'hk-entry-01',
  country_code: 'HK',
  location: '香港',
  machine_type: 'direct',
  allowed_ports: [],
  addresses: ['203.0.113.1'],
} as unknown as Server
const container = document.createElement('div')
document.body.append(container)
const root = createRoot(container)
afterEach(() => {
  act(() => root.render(null))
})

it('服务器卡的三网测速行显示完整标签（省份+运营商）', async () => {
  const row = (label: string, latency: number): CatalogProbeRow =>
    ({
      label,
      carrier: 'telecom',
      latency_ms: latency,
      loss_percent: 0,
      samples: 1,
      rounds: [{ latency_ms: latency, measured_at: '2026-10-30T02:00:00Z' }],
      measured_at: '2026-10-30T02:00:00Z',
    }) as CatalogProbeRow
  const probesByServer = new Map<number, CatalogProbeRow[]>([
    [server.id, [row('广东电信', 88), row('广东联通', 163.33), row('广东移动', 175.616)]],
  ])
  try {
    await act(async () =>
      root.render(
        <CanvasServerPanel
          servers={[server]}
          probesByServer={probesByServer}
          probeTitle="广东"
          usedCount={() => 0}
          canAddMore
          onAdd={() => {}}
          emptyCanvas
        />,
      ),
    )
    const labels = [...container.querySelectorAll('.cw-server-item .cw-probe-label')].map(
      (element) => element.textContent,
    )
    expect(labels).toEqual(['广东电信', '广东联通', '广东移动'])
  } finally {
    await act(async () => root.render(null))
  }
})

it('拖动服务器使用完整画布节点作为预览，光标在卡片中心', async () => {
  await act(async () =>
    root.render(
      <CanvasServerPanel
        servers={[server]}
        probesByServer={new Map()}
        probeTitle={null}
        usedCount={() => 0}
        canAddMore
        onAdd={() => {}}
        emptyCanvas
      />,
    ),
  )
  let dragImage: Element | undefined
  let anchor: number[] = []
  const transfer = {
    setData() {},
    effectAllowed: '',
    setDragImage(element: Element, x: number, y: number) {
      dragImage = element
      anchor = [x, y]
    },
  }
  const event = new Event('dragstart', { bubbles: true })
  Object.defineProperty(event, 'dataTransfer', { value: transfer })
  await act(async () => container.querySelector('[draggable="true"]')!.dispatchEvent(event))
  expect(dragImage).toBeInstanceOf(HTMLElement)
  expect(dragImage!.querySelector('.cw-node-alias')?.textContent).toBe('hk-entry-01')
  expect(dragImage!.textContent).toContain('香港')
  expect(dragImage!.textContent).toContain('入口端口')
  expect(dragImage!.querySelector('.cw-port-entry')).toBeTruthy()
  expect(dragImage!.querySelector('.cw-port-exit')).toBeTruthy()
  expect(anchor).toEqual([144, 112]) // 264×200 卡片 + 12px 端口/阴影留白
})
