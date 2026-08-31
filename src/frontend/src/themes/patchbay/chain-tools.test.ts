import { describe, expect, it } from 'vitest'

import { filterChains, visibleHopRange } from './chain-tools'

const chains = [
  {
    id: 'tokyo',
    name: '日本主链路',
    tone: 'active',
    hops: [{ label: 'Tokyo Edge 01', code: 'JP-01', location: '东京 · 品川' }],
  },
  {
    id: 'hk',
    name: '香港备用链路',
    tone: 'degraded',
    hops: [{ label: 'Hong Kong Gateway', code: 'HK-05', location: '香港 · 将军澳' }],
  },
  {
    id: 'pending',
    name: '东京待部署',
    tone: 'inactive',
    hops: [{ label: 'Tokyo [02]', code: 'JP-02', location: '东京' }],
  },
]

describe('chain search and attention filters', () => {
  it('preserves the original list when no filter is set', () => {
    expect(filterChains(chains, '  ', false)).toBe(chains)
  })

  it('matches chain names, node aliases, codes and locations', () => {
    for (const query of ['日本主链路', 'Tokyo Edge', 'jp-01', '品川']) {
      expect(filterChains(chains, query, false).map((c) => c.id)).toEqual(['tokyo'])
    }
  })

  it('normalizes full-width characters and case', () => {
    expect(filterChains(chains, 'ＴＯＫＹＯ', false)).toHaveLength(2)
  })

  it('requires all words while allowing them to match different fields', () => {
    expect(filterChains(chains, 'Tokyo 品川', false).map((c) => c.id)).toEqual(['tokyo'])
    expect(filterChains(chains, 'Tokyo 香港', false)).toEqual([])
  })

  it('matches query punctuation literally, without evaluating regular expressions', () => {
    expect(filterChains(chains, '[02]', false).map((c) => c.id)).toEqual(['pending'])
  })

  it('includes both degraded and pending chains in needs-attention', () => {
    expect(filterChains(chains, '', true).map((c) => c.id)).toEqual(['hk', 'pending'])
    expect(filterChains(chains, 'Tokyo', true).map((c) => c.id)).toEqual(['pending'])
  })

  it('does not mutate source data or renumber filtered chains', () => {
    const before = JSON.stringify(chains)
    const result = filterChains(chains, '备用', true)
    expect(result[0]).toBe(chains[1])
    expect(JSON.stringify(chains)).toBe(before)
  })
})

const hops = [0, 1, 2].map((index) => ({ left: 18 + index * 268, width: 212 }))

describe('topology viewport range', () => {
  it('reports a complete desktop route', () => {
    expect(visibleHopRange(hops, 0, 800)).toEqual({ first: 0, last: 2 })
  })

  it('does not count an unreadable edge sliver on mobile', () => {
    expect(visibleHopRange(hops, 0, 353)).toEqual({ first: 0, last: 0 })
    expect(visibleHopRange(hops, 268, 353)).toEqual({ first: 1, last: 1 })
    expect(visibleHopRange(hops, 431, 353)).toEqual({ first: 2, last: 2 })
  })

  it('uses the nearest node when stopped between cards', () => {
    expect(visibleHopRange(hops, 240, 20)).toEqual({ first: 0, last: 0 })
    expect(visibleHopRange(hops, 260, 20)).toEqual({ first: 1, last: 1 })
  })

  it('handles single-hop, empty, zero-width and overscrolled views', () => {
    expect(visibleHopRange(hops.slice(0, 1), 0, 353)).toEqual({ first: 0, last: 0 })
    expect(visibleHopRange([], 0, 353)).toEqual({ first: 0, last: 0 })
    expect(visibleHopRange(hops, 0, 0)).toEqual({ first: 0, last: 0 })
    expect(visibleHopRange(hops, -10, 353)).toEqual({ first: 0, last: 0 })
  })
})
