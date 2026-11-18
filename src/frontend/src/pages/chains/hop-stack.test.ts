import { describe, expect, it } from 'vitest'
import type { ChainOrchestration, OrchestrationNode } from '@/lib/types'
import { buildHopStack, hopStackTotal, MAX_STACK_CARDS } from './hop-stack'

function server(id: number, serverId: number, root = false): OrchestrationNode {
  return {
    id,
    kind: 'server',
    server_id: serverId,
    priority: 0,
    is_root: root,
    online: true,
    banned: false,
  }
}
function trigger(id: number, subject: string, action: string): OrchestrationNode {
  return {
    id,
    kind: 'trigger',
    priority: 0,
    subject: subject as OrchestrationNode['subject'],
    action: action as OrchestrationNode['action'],
    online: false,
    banned: false,
  }
}

function graph(): ChainOrchestration {
  return {
    root_server_id: 1,
    active_server_id: 2,
    triggered: true,
    nodes: [
      server(10, 1, true),
      trigger(11, 'self', 'ban_suspected'),
      trigger(12, 'self', 'ban_banned'),
      server(13, 2),
      trigger(14, 'root', 'recovered'),
      server(15, 3),
      trigger(16, 'self', 'ban_banned'),
    ],
    edges: [
      { from: 10, to: 11 },
      { from: 10, to: 12 },
      { from: 11, to: 13 },
      { from: 12, to: 13 },
      { from: 14, to: 10 },
      { from: 10, to: 14 },
      { from: 13, to: 16 },
      { from: 16, to: 15 },
    ],
  }
}

describe('buildHopStack', () => {
  it('根在前，其余按 BFS 去重', () => {
    const stack = buildHopStack(graph())
    expect(stack.map((item) => item.node.server_id)).toEqual([1, 2, 3])
    expect(stack[0].active).toBe(false)
    expect(stack[1].active).toBe(true)
  })

  it('最多返回 4 张并统计总数', () => {
    const data = graph()
    for (let i = 0; i < 6; i++) {
      data.nodes.push(server(100 + i, 10 + i))
      data.edges.push({ from: 10, to: 150 + i })
      data.nodes.push(trigger(150 + i, 'self', 'ban_banned'))
      data.edges.push({ from: 150 + i, to: 100 + i })
    }
    const stack = buildHopStack(data)
    expect(stack.length).toBe(MAX_STACK_CARDS)
    expect(hopStackTotal(data)).toBe(9)
  })

  it('没有编排时返回空', () => {
    expect(buildHopStack(null)).toEqual([])
    expect(hopStackTotal(undefined)).toBe(0)
  })
})
