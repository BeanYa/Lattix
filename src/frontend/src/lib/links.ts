import type { Chain, XrayNode } from '@/lib/types'

export interface LinkOption {
  nodeId: number
  name: string
  type: 'direct' | 'relay'
  detail: string
  status: string
}

export function buildLinkOptions(nodes: XrayNode[], chains: Chain[]): LinkOption[] {
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const exitNodeIds = new Set<number>()
  const relayOptions = chains.flatMap((chain) => {
    const exitHop = chain.hops.find((hop) => hop.role === 'exit')
    if (!exitHop?.node_id) return []
    exitNodeIds.add(exitHop.node_id)
    const exitNode = nodeById.get(exitHop.node_id)
    return [{
      nodeId: exitHop.node_id,
      name: chain.name || `中转 #${chain.id}`,
      type: 'relay' as const,
      detail: chain.hops.map((hop) => hop.server_alias).join(' → '),
      status: chain.status,
      ...(exitNode ? { protocol: exitNode.protocol } : {}),
    }]
  })
  const directOptions = nodes
    .filter((node) => !exitNodeIds.has(node.id))
    .map((node) => ({
      nodeId: node.id,
      name: node.name || `直连 #${node.id}`,
      type: 'direct' as const,
      detail: `${node.server_alias} · ${node.protocol} · ${node.realized_config?.port ?? node.port ?? '自动'}`,
      status: node.status,
    }))
  return [...directOptions, ...relayOptions].toSorted((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
}
