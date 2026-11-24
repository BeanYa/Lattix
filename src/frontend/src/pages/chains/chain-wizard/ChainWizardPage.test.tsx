// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { Chain, OrchestrationTemplate, Server } from '@/lib/types'
import { ChainWizardPage } from './ChainWizardPage'

const mocks = vi.hoisted(() => ({ confirm: vi.fn(async () => true), navigate: vi.fn() }))
vi.mock('wouter', () => ({ useLocation: () => ['/network/chains/new', mocks.navigate] }))
vi.mock('@/lib/app-dialog', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/app-dialog')>()),
  useAppDialog: () => ({ confirm: mocks.confirm }),
}))
vi.mock('@/lib/operation-progress-context', () => ({
  useOperationProgress: () => ({ showOperation: vi.fn() }),
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const servers = [1, 2].map(
  (id) =>
    ({
      id,
      alias: `Server ${id}`,
      kind: 'managed',
      connection_state: 'online',
      machine_type: 'direct',
      tags: [],
      allowed_ports: [],
      addresses: ['203.0.113.1'],
      address: '203.0.113.1',
      country_code: 'JP',
      location: '',
      metrics: null,
    }) as unknown as Server,
)
const template: OrchestrationTemplate = {
  id: 8,
  revision: 1,
  name: 'Shared auto',
  usage_count: 0,
  created_at: '',
  updated_at: '',
  nodes: [{ kind: 'server', server_id: 1, is_root: true, address: '', listen_port: 0 }],
  edges: [],
}
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const button = (label: string) =>
  [...document.body.querySelectorAll('button')].find((b) => b.textContent?.includes(label))!
beforeEach(async () => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  mocks.confirm.mockClear()
  vi.spyOn(api, 'chains').mockResolvedValue([])
  vi.spyOn(api, 'nodes').mockResolvedValue([])
  vi.spyOn(api, 'servers').mockResolvedValue(servers)
  vi.spyOn(api, 'settings').mockResolvedValue({ panel_short: '' } as Awaited<
    ReturnType<typeof api.settings>
  >)
  vi.spyOn(api, 'catalogProbes').mockRejectedValue(new Error('no probes'))
  vi.spyOn(api, 'orchestrationTemplates').mockResolvedValue([template])
  vi.spyOn(api, 'saveHopOrchestration').mockRejectedValue(new Error('unexpected runtime save'))
  vi.spyOn(api, 'createChain').mockResolvedValue({ observeId: '', data: { id: 99 } as Chain })
  await act(async () => root.render(<ChainWizardPage />))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
})
it('opens orchestration before saving and applies a template draft in the creation transaction', async () => {
  await act(async () => button('编排模板').click())
  await act(async () =>
    (host.querySelector('[aria-label="添加模板 Shared auto 到画布"]') as HTMLElement).click(),
  )
  const entry = host.querySelector('.cw-node-orch') as HTMLButtonElement
  expect(entry.disabled).toBe(false)
  await act(async () => entry.click())
  expect(document.body.textContent).toContain('共享编排 · Shared auto')
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
  await act(async () => button('取消').click())
  await act(async () => button('下一步').click())
  const form = host.querySelector('form')!
  expect(form).toBeTruthy()
  await act(async () =>
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })),
  )
  expect(api.createChain).toHaveBeenCalledOnce()
  expect(vi.mocked(api.createChain).mock.calls[0][0].orchestrations).toEqual([
    {
      nodes: template.nodes,
      edges: [],
      template_id: 8,
      template_revision: 1,
      overrides: [],
      seq: 0,
    },
  ])
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
})
it('requires explicit replacement confirmation for repeated template roots and retains the node on cancel', async () => {
  await act(async () => button('编排模板').click())
  const add = () =>
    (host.querySelector('[aria-label="添加模板 Shared auto 到画布"]') as HTMLElement).click()
  await act(async () => add())
  mocks.confirm.mockResolvedValueOnce(false)
  await act(async () => add())
  expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '替换该跳编排' }))
  expect(host.querySelectorAll('.cw-node')).toHaveLength(1)
  expect(host.querySelector('.cw-node-orch')?.textContent).toContain('自动编排')
})
