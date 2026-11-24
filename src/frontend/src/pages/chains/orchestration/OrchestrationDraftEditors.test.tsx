// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RequestError } from '@/lib/requester'
import type { Chain, ChainHop, OrchestrationTemplate, Server } from '@/lib/types'
import { OrchestrationTemplateEditor } from './OrchestrationTemplateEditor'
import { DraftOrchestrationEditor } from './DraftOrchestrationEditor'
import { draftFromHop, draftOrchestrationDTO } from './orchestration-draft'
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
    preview_proof: 'proof',
    revision: 3,
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

it('defers reset for a newer materialized binding until current defaults arrive, then retains those defaults across an older response', async () => {
  const template = {
    id: 7,
    revision: 1,
    name: 'Shared',
    usage_count: 1,
    created_at: '',
    updated_at: '',
    nodes: [
      { kind: 'server' as const, server_id: 1, is_root: true, address: '', listen_port: 8443 },
    ],
    edges: [],
  }
  const value = {
    template_id: 7,
    template_revision: 2,
    nodes: [{ ...template.nodes[0], listen_port: 0 }],
    edges: [],
    overrides: [{ server_id: 1, address: '', listen_port: 0 }],
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
  const apply = vi.fn()
  const refresh = vi.fn()
  const render = (catalog: (typeof template)[]) =>
    root.render(
      <DraftOrchestrationEditor
        chain={chain}
        hop={hop}
        value={value}
        templates={catalog}
        servers={[server]}
        onClose={() => {}}
        onApply={apply}
        onTemplatesChanged={refresh}
      />,
    )
  await act(async () => render([template]))
  expect(button('恢复端口继承').disabled).toBe(true)
  await act(async () => button('刷新模板列表').click())
  expect(refresh).toHaveBeenCalledOnce()
  expect(button('恢复地址继承').disabled).toBe(true)
  expect(button('恢复端口继承').title).toContain('最新模板默认值尚未就绪')
  const current = { ...template, revision: 2, nodes: [{ ...template.nodes[0], listen_port: 9443 }] }
  await act(async () => render([current]))
  expect(button('恢复端口继承').disabled).toBe(false)
  await act(async () => button('恢复端口继承').click())
  expect(document.body.querySelector('.cw-node-port-value')?.textContent).toBe('9443')
  await act(async () => render([template]))
  expect(document.body.querySelector('.cw-node-port-value')?.textContent).toBe('9443')
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall?.[0]).toMatchObject({
    template_revision: 2,
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 9443, address: '' }],
    overrides: [{ server_id: 1, address: '' }],
  })
})

it('refreshes an open shared canvas and its validation immediately while retaining an entered override', async () => {
  const catalogServers = [
    server,
    { ...server, id: 2, alias: 'Backup' },
    { ...server, id: 3, alias: 'Later' },
  ]
  const template: OrchestrationTemplate = {
    id: 7,
    revision: 2,
    name: 'Shared',
    usage_count: 1,
    created_at: '',
    updated_at: '',
    nodes: [
      { kind: 'server', server_id: 1, is_root: true, listen_port: 8443 },
      { kind: 'trigger', subject: 'self', action: 'ban_banned' },
      { kind: 'server', server_id: 2, listen_port: 7000 },
    ],
    edges: [
      { from: 0, to: 1 },
      { from: 1, to: 2 },
    ],
  }
  const value = {
    template_id: 7,
    template_revision: 2,
    nodes: template.nodes,
    edges: template.edges,
    overrides: [{ server_id: 1, address: '' }],
  }
  const hop = {
    seq: 0,
    server_id: 1,
    server_alias: 'Root',
    role: 'entry',
    forward_port: 8443,
    address: '',
  } as ChainHop
  const chain = {
    id: 0,
    name: 'Draft',
    hops: [hop],
    entry_port: 8443,
    desired_revision_id: 0,
  } as Chain
  const apply = vi.fn()
  const render = (catalog: OrchestrationTemplate) =>
    root.render(
      <DraftOrchestrationEditor
        chain={chain}
        hop={hop}
        value={value}
        templates={[catalog]}
        servers={catalogServers}
        onClose={() => {}}
        onApply={apply}
      />,
    )
  await act(async () => render(template))
  await act(async () => button('7000').click())
  const port = document.body.querySelector<HTMLInputElement>('[aria-label="入口端口数值：Backup"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(port, '0')
    port.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(document.body.querySelector('[aria-label="入口端口数值：Backup"]')).toBe(port)
  expect(port.value).toBe('0')
  const newer: OrchestrationTemplate = {
    ...template,
    revision: 3,
    nodes: [
      { ...template.nodes[0], listen_port: 9443 },
      ...template.nodes.slice(1),
      { kind: 'trigger', subject: 'self', action: 'ban_banned' },
      { kind: 'server', server_id: 3, listen_port: 70000 },
    ],
    edges: [...template.edges, { from: 2, to: 3 }, { from: 3, to: 4 }],
  }
  await act(async () => render(newer))
  // No Reset or other local action: the authoritative defaults and new graph
  // must appear and be validated as soon as this catalog revision is delivered.
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(3)
  expect(
    [...document.body.querySelectorAll('.cw-node-port-value')].map((node) => node.textContent),
  ).toEqual(['9443', '0', '70000'])
  expect(document.body.textContent).toContain('端口须为 1-65535')
  expect(button('应用编排').disabled).toBe(true)
  expect(apply).not.toHaveBeenCalled()
  const valid = {
    ...newer,
    revision: 4,
    nodes: newer.nodes.map((node) =>
      node.server_id === 3 ? { ...node, listen_port: 7443 } : node,
    ),
  }
  await act(async () => render(valid))
  expect(
    [...document.body.querySelectorAll('.cw-node-port-value')].map((node) => node.textContent),
  ).toEqual(['9443', '0', '7443'])
  expect(button('应用编排').disabled).toBe(false)
  await act(async () => button('应用编排').click())
  const applied = apply.mock.lastCall?.[0]
  expect(applied).toMatchObject({
    template_revision: 4,
    edges: valid.edges,
    overrides: [
      { server_id: 1, address: '' },
      { server_id: 2, listen_port: 0 },
    ],
  })
  const dto = draftOrchestrationDTO(applied, catalogServers, 1)
  expect(
    dto.nodes.filter((node) => node.kind === 'server').map((node) => node.listen_port),
  ).toEqual([9443, 0, 7443])
  expect(dto.edges).toEqual(valid.edges)
})

it('freezes the structural edit revision when a v2 catalog arrives during v1 editing', async () => {
  const v1: OrchestrationTemplate = {
    id: 7,
    name: 'Shared',
    revision: 1,
    usage_count: 1,
    created_at: '',
    updated_at: '',
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
  }
  const preview = vi
    .spyOn(api, 'previewOrchestrationTemplate')
    .mockRejectedValue(new Error('模板或关联链路已变化，请刷新后重试'))
  const render = (template: OrchestrationTemplate) =>
    root.render(
      <OrchestrationTemplateEditor
        template={template}
        servers={[server]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    )
  await act(async () => render(v1))
  const name = document.body.querySelector<HTMLInputElement>('[aria-label="模板名称"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'My edit')
    name.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () =>
    render({
      ...v1,
      revision: 2,
      name: 'Other editor',
      nodes: [...v1.nodes, { kind: 'server', server_id: 2, listen_port: 7000 }],
    }),
  )
  await act(async () => button('保存模板').click())
  expect(preview).toHaveBeenCalledWith(
    expect.objectContaining({
      revision: 1,
      name: 'My edit',
      nodes: [expect.objectContaining({ server_id: 1 })],
    }),
  )
  expect(document.body.textContent).toContain('模板或关联链路已变化')
  expect(name.value).toBe('My edit')
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(1)
})

it('uses pending v2 materialization with ack v1 and stale catalog through apply, reset gating and detach', async () => {
  const v1: OrchestrationTemplate = {
    id: 7,
    name: 'Shared',
    revision: 1,
    usage_count: 1,
    created_at: '',
    updated_at: '',
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
  }
  const v2 = {
    ...v1,
    revision: 2,
    nodes: [
      { ...v1.nodes[0], listen_port: 9443 },
      { kind: 'trigger' as const, subject: 'self' as const, action: 'ban_banned' as const },
      { kind: 'server' as const, server_id: 2, listen_port: 7000 },
    ],
    edges: [
      { from: 0, to: 1 },
      { from: 1, to: 2 },
    ],
  }
  const materialized = draftOrchestrationDTO(
    {
      ...v2,
      template_id: 7,
      template_revision: 2,
      overrides: [{ server_id: 1, address: '', listen_port: 0 }],
    },
    [server],
    1,
  )
  const hop = {
    seq: 0,
    server_id: 1,
    role: 'entry',
    forward_port: 0,
    address: '',
    orchestration: {
      ...materialized,
      template_revision: 1,
      template_source_revision: 2,
      template_sync_pending: true,
    },
  } as ChainHop
  const apply = vi.fn()
  await act(async () =>
    root.render(
      <DraftOrchestrationEditor
        chain={{ id: 9, name: 'Pending', hops: [hop] } as Chain}
        hop={hop}
        templates={[v1]}
        servers={[server, { ...server, id: 2, alias: 'Backup' }]}
        onClose={() => {}}
        onApply={apply}
      />,
    ),
  )
  expect(draftFromHop(hop).template_revision).toBe(2)
  expect(button('恢复端口继承').disabled).toBe(true)
  expect(button('恢复地址继承').disabled).toBe(true)
  expect(document.body.querySelectorAll('.cw-node')).toHaveLength(2)
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall?.[0]).toMatchObject({
    template_revision: 2,
    overrides: [{ server_id: 1, address: '', listen_port: 0 }],
  })
  await act(async () => button('另存为独立副本').click())
  await act(async () => button('应用编排').click())
  expect(apply.mock.lastCall?.[0].template_id).toBeUndefined()
  expect(apply.mock.lastCall?.[0].nodes).toHaveLength(3)
  expect(apply.mock.lastCall?.[0].nodes[0]).toMatchObject({ address: '', listen_port: 0 })
  expect(apply.mock.lastCall?.[0].nodes[2].listen_port).toBe(7000)
})

it('refreshes changed save impacts while retaining the edited graph and requires confirmation again', async () => {
  const template: OrchestrationTemplate = {
    id: 7,
    name: 'Shared',
    revision: 1,
    usage_count: 0,
    created_at: '',
    updated_at: '',
    nodes: [{ kind: 'server', server_id: 1, is_root: true, listen_port: 8443 }],
    edges: [],
  }
  const preview = vi
    .spyOn(api, 'previewOrchestrationTemplate')
    .mockResolvedValueOnce({ revision: 1, preview_proof: 'old', affected_chains: [] })
    .mockResolvedValue({
      revision: 1,
      preview_proof: 'new',
      affected_chains: [{ chain_id: 22, chain_name: 'New binding', seq: 0, redeploy: true }],
    })
  const save = vi
    .spyOn(api, 'saveOrchestrationTemplate')
    .mockRejectedValueOnce(
      new RequestError({ kind: 'business', code: 'CONFLICT', message: '范围已变化' }),
    )
    .mockResolvedValue(template)
  await act(async () =>
    root.render(
      <OrchestrationTemplateEditor
        template={template}
        servers={[server]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    ),
  )
  await act(async () => button('保存模板').click())
  await act(async () => button('确认影响并保存').click())
  expect(preview).toHaveBeenCalledTimes(2)
  expect(save).toHaveBeenCalledTimes(1)
  expect(document.body.querySelector('[role="alertdialog"]')?.textContent).toContain('New binding')
  expect(document.body.textContent).toContain('再次确认')
  await act(async () => button('确认影响并保存').click())
  expect(save).toHaveBeenLastCalledWith(
    expect.objectContaining({
      revision: 1,
      preview_proof: 'new',
      nodes: [expect.objectContaining({ listen_port: 8443 })],
    }),
  )
})
