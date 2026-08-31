interface SearchableChain {
  name: string
  tone: string
  hops: { label: string; code: string; location: string }[]
}

const normalize = (value: string) => value.normalize('NFKC').toLocaleLowerCase()

export function filterChains<T extends SearchableChain>(
  chains: T[],
  query: string,
  attentionOnly: boolean,
): T[] {
  const terms = normalize(query).trim().split(/\s+/).filter(Boolean)
  if (!terms.length && !attentionOnly) return chains
  return chains.filter((chain) => {
    if (attentionOnly && chain.tone === 'active') return false
    const text = normalize(
      [chain.name, ...chain.hops.flatMap((hop) => [hop.label, hop.code, hop.location])].join(' '),
    )
    return terms.every((term) => text.includes(term))
  })
}

export interface HopBounds {
  left: number
  width: number
}

/** Count a hop as visible only when at least half of its card can be read. */
export function visibleHopRange(hops: HopBounds[], scrollLeft: number, viewportWidth: number) {
  if (!hops.length || viewportWidth <= 0) return { first: 0, last: 0 }
  const visible = hops.flatMap((hop, index) => {
    const overlap =
      Math.min(hop.left + hop.width, scrollLeft + viewportWidth) - Math.max(hop.left, scrollLeft)
    return overlap >= Math.min(hop.width, viewportWidth) / 2 ? [index] : []
  })
  if (visible.length) return { first: visible[0], last: visible[visible.length - 1] }
  const center = scrollLeft + viewportWidth / 2
  let nearest = 0
  for (let i = 1; i < hops.length; i++) {
    if (
      Math.abs(hops[i].left + hops[i].width / 2 - center) <
      Math.abs(hops[nearest].left + hops[nearest].width / 2 - center)
    )
      nearest = i
  }
  return { first: nearest, last: nearest }
}
