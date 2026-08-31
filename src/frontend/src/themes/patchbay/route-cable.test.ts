import { describe, expect, it } from 'vitest'

import { getCableState, type RouteTone } from './route-cable'

describe('patchbay cable state', () => {
  it('shows signal flow only when the route and both endpoints are active', () => {
    expect(getCableState('active', 'active', 'active')).toEqual({
      tone: 'active',
      label: '链路正常',
    })
    const tones: RouteTone[] = ['active', 'degraded', 'inactive']
    for (const route of tones) {
      for (const source of tones) {
        for (const target of tones) {
          expect(getCableState(route, source, target).tone === 'active').toBe(
            route === 'active' && source === 'active' && target === 'active',
          )
        }
      }
    }
  })

  it('marks either failed endpoint, even if the route still reports active', () => {
    expect(getCableState('active', 'degraded', 'active')).toEqual({
      tone: 'degraded',
      label: '节点异常',
    })
    expect(getCableState('active', 'active', 'degraded').tone).toBe('degraded')
  })

  it('does not claim connectivity for pending endpoints', () => {
    expect(getCableState('active', 'active', 'inactive')).toEqual({
      tone: 'inactive',
      label: '等待部署',
    })
  })

  it('keeps healthy endpoints static while the overall route is degraded or unconfirmed', () => {
    expect(getCableState('degraded', 'active', 'active')).toEqual({
      tone: 'inactive',
      label: '待恢复',
    })
    expect(getCableState('inactive', 'active', 'active')).toEqual({
      tone: 'inactive',
      label: '待确认',
    })
  })
})
