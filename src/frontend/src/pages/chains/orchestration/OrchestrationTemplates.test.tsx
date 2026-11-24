// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { OrchestrationTemplate } from '@/lib/types'
import { OrchestrationTemplates } from './OrchestrationTemplates'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const template: OrchestrationTemplate = {
  id: 7,
  name: 'Shared',
  revision: 3,
  nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 0 }],
  edges: [],
  usage_count: 1,
  created_at: '',
  updated_at: '',
}
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
beforeEach(() => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  vi.spyOn(api, 'orchestrationTemplates').mockResolvedValue([template])
  vi.spyOn(api, 'servers').mockResolvedValue([])
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
})
function button(text: string) {
  return [...host.querySelectorAll('button')].find((b) => b.textContent?.includes(text))!
}
it('fetches standalone data, displays counts, and requires affected-chain confirmation before detach-on-delete', async () => {
  const preview = vi.spyOn(api, 'previewOrchestrationTemplate')
  const chains = vi
    .spyOn(api, 'chains')
    .mockResolvedValue([
      { id: 9, name: 'Linked', hops: [{ seq: 0, orchestration: { template_id: 7 } }] },
    ] as never)
  const remove = vi.spyOn(api, 'deleteOrchestrationTemplate').mockResolvedValue(undefined)
  await act(async () => root.render(<OrchestrationTemplates />))
  expect(api.servers).toHaveBeenCalledOnce()
  expect(host.textContent).toContain('1 台服务器 · 0 个触发器 · 1 个关联跳位')
  await act(async () => button('删除').click())
  expect(chains).toHaveBeenCalledOnce()
  expect(preview).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
  expect(host.querySelector('[role="alertdialog"]')?.textContent).toContain('Linked · 第 1 跳')
  await act(async () => button('确认分离并删除').click())
  expect(remove).toHaveBeenCalledWith(7)
  expect(api.orchestrationTemplates).toHaveBeenCalledTimes(2)
})
it('shows request errors and permits retry without deleting the template', async () => {
  vi.mocked(api.orchestrationTemplates).mockRejectedValueOnce(new Error('list unavailable'))
  const remove = vi.spyOn(api, 'deleteOrchestrationTemplate')
  await act(async () => root.render(<OrchestrationTemplates servers={[]} />))
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('list unavailable')
  expect(api.servers).not.toHaveBeenCalled()
  await act(async () => button('刷新模板').click())
  expect(host.textContent).toContain('Shared')
  expect(remove).not.toHaveBeenCalled()
})
it('confirms detach-delete for an invalid template linked to an applying chain without save-preview validation', async () => {
  vi.mocked(api.orchestrationTemplates).mockResolvedValue([{ ...template, nodes: [] }])
  const preview = vi
    .spyOn(api, 'previewOrchestrationTemplate')
    .mockRejectedValue(new Error('链路正在部署，不可更新共享模板'))
  const chains = vi.spyOn(api, 'chains').mockResolvedValue([
    {
      id: 12,
      name: 'Applying chain',
      status: 'applying',
      desired_revision_id: 9,
      hops: [
        { seq: 1, orchestration: { template_id: 7 } },
        { seq: 2, orchestration: { template_id: 8 } },
      ],
    },
  ] as never)
  const remove = vi.spyOn(api, 'deleteOrchestrationTemplate').mockResolvedValue(undefined)
  await act(async () => root.render(<OrchestrationTemplates servers={[]} />))
  await act(async () => button('删除').click())
  expect(host.querySelector('[role="alertdialog"]')?.textContent).toContain(
    'Applying chain · 第 2 跳',
  )
  expect(host.querySelector('[role="alertdialog"]')?.textContent).not.toContain('第 3 跳')
  expect(chains).toHaveBeenCalledOnce()
  expect(preview).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
  await act(async () => button('确认分离并删除').click())
  expect(remove).toHaveBeenCalledWith(7)
})
