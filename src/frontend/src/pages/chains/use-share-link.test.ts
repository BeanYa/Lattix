import { describe, expect, it } from 'vitest'

import type { SubUser } from '@/lib/types'

import { pickShareUser, userShareEligible, type ShareLinkTarget } from './use-share-link'

function user(overrides: Partial<SubUser>): SubUser {
  return {
    id: 1,
    name: 'u',
    uuid: 'uuid',
    online_connections: 0,
    sub_token: 't',
    sub_url: '',
    sub_links_url: '',
    node_ids: [],
    chain_ids: [],
    effective_chain_ids: [],
    user_group_ids: [],
    chain_assignments: [],
    traffic: null,
    expires_at: null,
    expired: false,
    disabled: false,
    effective_at: null,
    is_trial: false,
    quota_exhausted: false,
    traffic_limit: 0,
    traffic_reset_day: 0,
    sub_title: '',
    sub_announcement: '',
    plan_name: '',
    app_url: '',
    routing: {} as SubUser['routing'],
    subscription_snapshot: { status: 'ready', revision: 0 },
    external_subscriptions: [],
    created_at: '',
    ...overrides,
  }
}

const chainTarget: ShareLinkTarget = { kind: 'chain', id: 7, name: '链路', exitNodeId: 42 }
const nodeTarget: ShareLinkTarget = { kind: 'node', id: 42, name: '直连' }

describe('userShareEligible', () => {
  it('排除过期、禁用、配额耗尽与未生效用户', () => {
    expect(userShareEligible(user({}))).toBe(true)
    expect(userShareEligible(user({ expired: true }))).toBe(false)
    expect(userShareEligible(user({ disabled: true }))).toBe(false)
    expect(userShareEligible(user({ quota_exhausted: true }))).toBe(false)
    expect(
      userShareEligible(user({ effective_at: new Date(Date.now() + 60_000).toISOString() })),
    ).toBe(false)
  })
})

describe('pickShareUser', () => {
  const assigned = user({ id: 1, name: '已分配', chain_ids: [7] })
  const viaExit = user({ id: 2, name: '出口分配', node_ids: [42] })
  const other = user({ id: 3, name: '无关用户' })
  const inactive = user({ id: 4, name: '已过期', expired: true, chain_ids: [7] })

  it('链路目标优先已分配（直接或经出口节点），无效用户不参与', () => {
    expect(pickShareUser([other, inactive, assigned, viaExit], chainTarget)?.id).toBe(1)
    expect(pickShareUser([other, inactive, viaExit], chainTarget)?.id).toBe(2)
  })

  it('节点目标按 node_ids 预选', () => {
    expect(pickShareUser([other, viaExit], nodeTarget)?.id).toBe(2)
  })

  it('无人持有时回退首个有效用户，全无效返回 null', () => {
    expect(pickShareUser([other], chainTarget)?.id).toBe(3)
    expect(pickShareUser([inactive], chainTarget)).toBeNull()
  })
})
