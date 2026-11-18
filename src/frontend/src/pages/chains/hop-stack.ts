import type { ChainOrchestration, OrchestrationNode } from '@/lib/types'

export const MAX_STACK_CARDS = 4

export interface HopStackServer {
  node: OrchestrationNode
  active: boolean
}

/** 详情页堆叠顺序：根服务器固定在前，其余服务器按编排图 BFS（触发器数组顺序）去重。 */
export function buildHopStack(orchestration?: ChainOrchestration | null): HopStackServer[] {
  if (!orchestration) return []
  const byId = new Map(orchestration.nodes.map((node) => [node.id, node]))
  const outgoing = new Map<number, number[]>()
  for (const edge of orchestration.edges) {
    const list = outgoing.get(edge.from) ?? []
    list.push(edge.to)
    outgoing.set(edge.from, list)
  }
  const root = orchestration.nodes.find((node) => node.kind === 'server' && node.is_root)
  if (!root) return []
  const ordered: OrchestrationNode[] = [root]
  const seen = new Set<number>([root.id])
  const queue: OrchestrationNode[] = [root]
  while (queue.length > 0) {
    const server = queue.shift() as OrchestrationNode
    for (const triggerId of outgoing.get(server.id) ?? []) {
      const trigger = byId.get(triggerId)
      if (!trigger || trigger.kind !== 'trigger') continue
      for (const targetId of outgoing.get(trigger.id) ?? []) {
        const target = byId.get(targetId)
        if (!target || target.kind !== 'server' || seen.has(target.id)) continue
        seen.add(target.id)
        ordered.push(target)
        queue.push(target)
      }
    }
  }
  const activeId = orchestration.active_server_id
  return ordered.slice(0, MAX_STACK_CARDS).map((node) => ({
    node,
    active: node.server_id === activeId,
  }))
}

/** 图中不同服务器总数（用于 +N）。 */
export function hopStackTotal(orchestration?: ChainOrchestration | null): number {
  if (!orchestration) return 0
  const ids = new Set<number>()
  for (const node of orchestration.nodes) {
    if (node.kind === 'server' && node.server_id) ids.add(node.server_id)
  }
  return ids.size
}
