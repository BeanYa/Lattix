import { describe, expect, it } from 'vitest'
import type { ChainHop, OrchestrationTemplate } from '@/lib/types'
import {
  draftFromHop,
  draftFromTemplate,
  resolveOrchestrationDraft,
  detachOrchestrationDraft,
  setDraftOverride,
  resetDraftOverride,
  draftOrchestrationDTO,
} from './orchestration-draft'

const template: OrchestrationTemplate = {
  id: 7,
  name: 'shared',
  revision: 2,
  usage_count: 1,
  created_at: '',
  updated_at: '',
  nodes: [
    { kind: 'server', server_id: 1, is_root: true, listen_port: 8443 },
    { kind: 'trigger', subject: 'self', action: 'ban_banned' },
    { kind: 'server', server_id: 2, listen_port: 0 },
  ],
  edges: [
    { from: 0, to: 1 },
    { from: 1, to: 2 },
  ],
}
describe('orchestration drafts', () => {
  it('clones template defaults without address or runtime state and isolates copies', () => {
    const draft = draftFromTemplate(template)
    expect(draft.template_id).toBe(7)
    expect(resolveOrchestrationDraft(draft, template).nodes.map((n) => n.listen_port)).toEqual([
      8443,
      undefined,
      0,
    ])
    draft.nodes[0].listen_port = 10
    draft.edges[0].to = 2
    expect(template.nodes[0].listen_port).toBe(8443)
    expect(template.edges[0].to).toBe(1)
  })
  it('persists explicit empty address and zero port, resets inheritance and detaches a resolved copy', () => {
    let draft = setDraftOverride(draftFromTemplate(template), 1, { address: '', listen_port: 0 })
    expect(resolveOrchestrationDraft(draft, template).nodes[0]).toMatchObject({
      address: '',
      listen_port: 0,
    })
    const detached = detachOrchestrationDraft(draft, template)
    expect(detached.template_id).toBeUndefined()
    expect(detached.nodes[0].listen_port).toBe(0)
    draft = resetDraftOverride(draft, 1, 'listen_port')
    expect(resolveOrchestrationDraft(draft, template).nodes[0].listen_port).toBe(8443)
    expect(draft.overrides).toEqual([{ server_id: 1, address: '' }])
    detached.nodes[0].listen_port = 9
    expect(template.nodes[0].listen_port).toBe(8443)
  })
  it('converts runtime IDs into input indexes while retaining binding and overrides', () => {
    const hop = {
      server_id: 2,
      orchestration: {
        template_id: 7,
        template_revision: 2,
        overrides: [{ server_id: 1, listen_port: 0 }],
        root_server_id: 1,
        active_server_id: 2,
        triggered: true,
        nodes: template.nodes.map((n, i) => ({
          ...n,
          id: 100 + i,
          priority: 0,
          online: true,
          banned: false,
        })),
        edges: [
          { from: 100, to: 101 },
          { from: 101, to: 102 },
        ],
      },
    } as ChainHop
    const draft = draftFromHop(hop)
    expect(draft.edges).toEqual(template.edges)
    expect(draft.overrides).toEqual([{ server_id: 1, listen_port: 0 }])
    const dto = draftOrchestrationDTO(draft, [], 2, template)
    expect(dto.nodes[0].listen_port).toBe(0)
    expect(dto.nodes[2].active).toBe(true)
  })
})
