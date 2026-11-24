// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { Chain, ChainHop, Server } from '@/lib/types'
import { OrchestrationTemplateEditor } from './OrchestrationTemplateEditor'
import { DraftOrchestrationEditor } from './DraftOrchestrationEditor'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const server = {
  id: 1,
  alias: 'Root',
  kind: 'managed',
  connection_state: 'online',
  machine_type: 'direct',
  addresses: [],
  address: '',
  allowed_ports: [],
  location: '',
  country_code: '',
  metrics: null,
} as unknown as Server
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
beforeEach(() => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  vi.spyOn(api, 'chains').mockResolvedValue([])
  vi.spyOn(api, 'saveHopOrchestration').mockRejectedValue(new Error('unexpected'))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
})
function button(text: string) {
  return [...document.body.querySelectorAll('button')].find((b) => b.textContent?.includes(text))!
}
it('starts a template with an empty canvas, then selects root with blank address and automatic port', async () => {
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor servers={[server]} onClose={() => {}} onSaved={() => {}} />,
    ),
  )
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(0)
  expect(document.body.textContent).toContain('选择根服务器')
  await act(async () => button('备选服务器').click())
  await act(async () => button('Root').click())
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(1)
  expect(document.body.textContent).toContain('自动')
  expect(api.chains).not.toHaveBeenCalled()
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
})
it('applies an unsaved draft locally without polling or saving a chain', async () => {
  const hop = {
    seq: 0,
    server_id: 1,
    server_alias: 'Root',
    role: 'entry',
    forward_port: 0,
    address: '',
  } as ChainHop
  const chain = {
    id: 0,
    name: 'Draft',
    hops: [hop],
    entry_port: 0,
    desired_revision_id: 0,
  } as Chain
  const apply = vi.fn()
  await act(async () =>
    root.render(
      <DraftOrchestrationEditor
        chain={chain}
        hop={hop}
        templates={[]}
        servers={[server]}
        onClose={() => {}}
        onApply={apply}
      />,
    ),
  )
  await act(async () => button('应用编排').click())
  expect(apply).toHaveBeenCalledOnce()
  expect(apply.mock.calls[0][0].nodes[0]).toMatchObject({
    server_id: 1,
    is_root: true,
    listen_port: 0,
  })
  expect(api.chains).not.toHaveBeenCalled()
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
})

it('keeps template addresses as an instance-only placeholder and saves automatic defaults', async () => {
  const save = vi.spyOn(api, 'saveOrchestrationTemplate').mockResolvedValue({ id: 7 } as never)
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor servers={[server]} onClose={() => {}} onSaved={() => {}} />,
    ),
  )
  await act(async () => button('备选服务器').click())
  await act(async () => button('Root').click())
  expect(document.body.querySelector('.cw-node-address-row')?.textContent).toContain('实例中填写')
  const name = document.body.querySelector<HTMLInputElement>('[aria-label="模板名称"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'New')
    name.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => button('保存模板').click())
  expect(save).toHaveBeenCalledWith(
    expect.objectContaining({
      name: 'New',
      nodes: [expect.objectContaining({ server_id: 1, address: '', listen_port: 0 })],
    }),
  )
})

it('validates a root-only template numeric default instead of clearing the graph', async () => {
  const template = {
    id: 7,
    name: 'Shared',
    revision: 1,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
    usage_count: 1,
    created_at: '',
    updated_at: '',
  } as const
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor
        template={structuredClone(template) as never}
        servers={[server]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    ),
  )
  await act(async () => button('8443').click())
  const port = document.body.querySelector<HTMLInputElement>('[aria-label="入口端口数值：Root"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(port, '70000')
    port.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(button('保存模板').disabled).toBe(true)
})

it('preserves explicit zero overrides, resets inheritance, and materializes an independent copy locally', async () => {
  const template = {
    id: 7,
    name: 'Shared',
    revision: 1,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
    usage_count: 1,
    created_at: '',
    updated_at: '',
  } as const
  const hop = {
    seq: 0,
    server_id: 1,
    server_alias: 'Root',
    role: 'entry',
    forward_port: 0,
    address: '',
  } as ChainHop
  const chain = {
    id: 0,
    name: 'Draft',
    hops: [hop],
    entry_port: 0,
    desired_revision_id: 0,
  } as Chain
  const value = {
    template_id: 7,
    template_revision: 1,
    nodes: structuredClone(template.nodes),
    edges: [],
    overrides: [{ server_id: 1, address: '', listen_port: 0 }],
  }
  const apply = vi.fn()
  await act(async () =>
    root.render(
      <DraftOrchestrationEditor
        chain={chain}
        hop={hop}
        value={value as never}
        templates={[template as never]}
        servers={[server]}
        onClose={() => {}}
        onApply={apply}
      />,
    ),
  )
  expect(document.body.textContent).toContain('已覆盖')
  await act(async () => button('应用编排').click())
  expect(apply.mock.calls[0][0].overrides).toEqual([{ server_id: 1, address: '', listen_port: 0 }])
  await act(async () => button('恢复端口继承').click())
  expect(document.body.querySelector('.cw-node-port-value')?.textContent).toBe('8443')
  await act(async () => button('另存为独立副本').click())
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall![0].template_id).toBeUndefined()
  expect(apply.mock.lastCall![0].nodes[0].listen_port).toBe(8443)
  expect(api.chains).not.toHaveBeenCalled()
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
})

it('portals outside transformed page ancestors and keeps keyboard focus inside the dialog', async () => {
  host.style.transform = 'translateY(0)'
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor servers={[server]} onClose={() => {}} onSaved={() => {}} />,
    ),
  )
  const overlay = document.body.querySelector('.orch-overlay')!
  const dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')!
  expect(overlay.parentElement).toBe(document.body)
  expect(host.contains(overlay)).toBe(false)
  expect(document.activeElement).toBe(dialog)
  await act(async () =>
    dialog.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }),
    ),
  )
  expect(dialog.contains(document.activeElement)).toBe(true)
  expect(document.activeElement).not.toBe(dialog)
})

it('previews concrete shared impacts before saving, keeps errors visible and never saves a hop', async () => {
  const template = {
    id: 7,
    name: 'Shared',
    revision: 3,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
    usage_count: 1,
    created_at: '',
    updated_at: '',
  } as const
  const preview = vi.spyOn(api, 'previewOrchestrationTemplate').mockResolvedValue({
    affected_chains: [{ chain_id: 9, chain_name: 'Live Chain', seq: 1, redeploy: true }],
  })
  const save = vi
    .spyOn(api, 'saveOrchestrationTemplate')
    .mockRejectedValue(new Error('版本已变化，请刷新模板'))
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor
        template={template as never}
        servers={[server]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    ),
  )
  await act(async () => button('保存模板').click())
  expect(preview).toHaveBeenCalledWith(
    expect.objectContaining({
      id: 7,
      revision: 3,
      nodes: [expect.objectContaining({ listen_port: 8443, address: '' })],
    }),
  )
  expect(save).not.toHaveBeenCalled()
  expect(document.body.querySelector('[role="alertdialog"]')?.textContent).toContain(
    'Live Chain · 第 2 跳 · 需要重新部署',
  )
  await act(async () => button('确认影响并保存').click())
  expect(save).toHaveBeenCalledOnce()
  expect(document.body.textContent).toContain('版本已变化')
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(1)
  expect(api.saveHopOrchestration).not.toHaveBeenCalled()
})

it('saves a separate template identity without previewing existing shared chains', async () => {
  const template = {
    id: 7,
    name: 'Shared',
    revision: 3,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 9443 }],
    edges: [],
    usage_count: 1,
    created_at: '',
    updated_at: '',
  } as const
  const save = vi.spyOn(api, 'saveOrchestrationTemplate').mockResolvedValue({ id: 8 } as never)
  const preview = vi.spyOn(api, 'previewOrchestrationTemplate')
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor
        template={template as never}
        servers={[server]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    ),
  )
  const checkbox = document.body.querySelector<HTMLInputElement>('input[type="checkbox"]')!
  await act(async () => checkbox.click())
  await act(async () => button('保存为新模板').click())
  expect(save).toHaveBeenCalledWith(
    expect.objectContaining({
      name: 'Shared',
      nodes: [expect.objectContaining({ listen_port: 9443 })],
    }),
  )
  expect(save.mock.calls[0][0].id).toBeUndefined()
  expect(save.mock.calls[0][0].revision).toBeUndefined()
  expect(preview).not.toHaveBeenCalled()
})

it('turns a user-entered automatic port into a real override and restores template inheritance', async () => {
  const template = {
    id: 7,
    name: 'Shared',
    revision: 1,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
    usage_count: 1,
    created_at: '',
    updated_at: '',
  }
  const hop = {
    seq: 0,
    server_id: 1,
    server_alias: 'Root',
    role: 'entry',
    forward_port: 0,
    address: '',
  } as ChainHop
  const chain = {
    id: 0,
    name: 'Draft',
    hops: [hop],
    entry_port: 0,
    desired_revision_id: 0,
  } as Chain
  const value = {
    template_id: 7,
    template_revision: 1,
    nodes: structuredClone(template.nodes),
    edges: [],
    overrides: [],
  }
  const apply = vi.fn()
  await act(async () =>
    root.render(
      <DraftOrchestrationEditor
        chain={chain}
        hop={hop}
        value={value as never}
        templates={[template as never]}
        servers={[server]}
        onClose={() => {}}
        onApply={apply}
      />,
    ),
  )
  await act(async () => button('8443').click())
  const port = document.body.querySelector<HTMLInputElement>('[aria-label="入口端口数值：Root"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(port, '0')
    port.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall![0].overrides).toEqual([{ server_id: 1, listen_port: 0 }])
  expect(value.overrides).toEqual([])
  await act(async () => button('恢复端口继承').click())
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall![0].overrides).toEqual([])
  expect(apply.mock.lastCall![0].nodes[0].listen_port).toBe(8443)
})
