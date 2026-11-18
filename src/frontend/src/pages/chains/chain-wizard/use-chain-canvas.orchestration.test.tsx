// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { Server } from '@/lib/types'
import { useChainCanvas } from './use-chain-canvas'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const servers = [
  {
    id: 1,
    alias: 'A1',
    country_code: 'JP',
    location: 'A',
    machine_type: 'direct',
    allowed_ports: [],
  },
  {
    id: 2,
    alias: 'A2',
    country_code: 'JP',
    location: 'B',
    machine_type: 'direct',
    allowed_ports: [],
  },
] as unknown as Server[]

let api: ReturnType<typeof useChainCanvas> | null = null

function Harness({
  orchestrated,
  confirmFn,
}: {
  orchestrated: Set<number>
  confirmFn: (alias: string) => Promise<boolean>
}) {
  api = useChainCanvas({
    servers,
    orchestratedServerIds: orchestrated,
    confirmRemoveOrchestration: confirmFn,
  })
  return null
}

const container = document.createElement('div')
document.body.append(container)
const root = createRoot(container)
afterEach(() => {
  api = null
  act(() => root.render(null))
})

it('删除已启用编排的节点需要确认，取消则保留', async () => {
  const confirmFn = vi.fn(async () => false)
  await act(async () => root.render(<Harness orchestrated={new Set([1])} confirmFn={confirmFn} />))
  await act(async () => api?.addVisibleNode(1))
  const node = api?.nodes[0]
  expect(node).toBeTruthy()
  await act(async () => api?.requestRemoveNode(node!.id))
  await act(async () => {
    await Promise.resolve()
  })
  expect(confirmFn).toHaveBeenCalledWith('A1')
  expect(api?.nodes).toHaveLength(1)
})

it('确认后删除节点；未启用编排的节点直接删除', async () => {
  const confirmFn = vi.fn(async () => true)
  await act(async () => root.render(<Harness orchestrated={new Set([1])} confirmFn={confirmFn} />))
  await act(async () => api?.addVisibleNode(1))
  const node = api?.nodes[0]
  await act(async () => api?.requestRemoveNode(node!.id))
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(confirmFn).toHaveBeenCalledTimes(1)
  expect(api?.nodes).toHaveLength(0)

  await act(async () => api?.addVisibleNode(2))
  const plain = api?.nodes[0]
  await act(async () => api?.requestRemoveNode(plain!.id))
  expect(confirmFn).toHaveBeenCalledTimes(1)
  expect(api?.nodes).toHaveLength(0)
})
