import { describe, expect, it } from 'vitest'

import { breakdownRowLabel, canDrillBreakdownRow } from './breakdown-labels'

describe('breakdownRowLabel', () => {
  it('labels external attribution rows with the source chain name', () => {
    expect(
      breakdownRowLabel({ dim_type: 'external', dim_id: '12', label: '香港中转', up: 1, down: 2 }),
    ).toBe('外部出口 · 香港中转')
  })

  it('falls back to the chain id when the chain name is missing', () => {
    expect(
      breakdownRowLabel({ dim_type: 'external', dim_id: '12', label: '', up: 1, down: 2 }),
    ).toBe('外部出口 #12')
  })

  it('keeps the existing user/server/node labels', () => {
    expect(
      breakdownRowLabel({ dim_type: 'user', dim_id: 'uuid-aaaa', label: 'Alice', up: 1, down: 2 }),
    ).toBe('Alice')
    expect(
      breakdownRowLabel({ dim_type: 'server', dim_id: '3', label: '节点C', up: 1, down: 2 }),
    ).toBe('节点C')
    expect(breakdownRowLabel({ dim_type: 'node', dim_id: '7', label: '', up: 1, down: 2 })).toBe(
      '节点 #7',
    )
  })
})

describe('canDrillBreakdownRow', () => {
  it('does not drill external attribution rows', () => {
    expect(
      canDrillBreakdownRow({
        dim_type: 'external',
        dim_id: '12',
        label: '香港中转',
        up: 1,
        down: 2,
      }),
    ).toBe(false)
  })
})
