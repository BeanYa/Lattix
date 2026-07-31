import type { Chain } from '@/lib/types'

export interface LinkOption {
	chainId: number
  name: string
  type: 'direct' | 'relay'
  detail: string
  status: string
}

export function buildLinkOptions(chains: Chain[]): LinkOption[] {
  const chainOptions = chains.flatMap((chain) => {
    if (!chain.service_node_id || chain.hops.length === 0) return []
    const type: LinkOption['type'] = chain.hops.length === 1 ? 'direct' : 'relay'
    return [{
			chainId: chain.id,
      name: chain.name || `${type === 'direct' ? '直连' : '中转'} #${chain.id}`,
      type,
      detail: chain.hops.map((hop) => hop.server_alias).join(' → '),
      status: chain.status,
    }]
  })
  return chainOptions.toSorted((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
}
