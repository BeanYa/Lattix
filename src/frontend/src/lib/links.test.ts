import { describe, expect, it } from 'vitest'

import type { Chain, ChainHop } from './types'
import { buildLinkOptions } from './links'

function hop(id: number, role: ChainHop['role'], nodeId = 0): ChainHop {
  return {
    id,
    seq: id,
    server_id: id,
    server_alias: `Server ${id}`,
    role,
    address: '',
    node_id: nodeId,
    status: 'active',
    error: '',
    forward_port: 0,
    portal_port: 0,
  }
}

function chain(id: number, serviceNodeId: number, hops: ChainHop[]): Chain {
  return {
    id,
    name: '',
    status: 'active',
    error: '',
    created_at: '',
		hops,
		service_node_id: serviceNodeId,
		endpoint_id: 0,
		entry_port: hops[0]?.forward_port ?? 0,
    traffic_multiplier: '1.000',
    published_revision_id: 1,
    desired_revision_id: 1,
    revision_forced: false,
    revision_tasks: [],
  }
}

describe('buildLinkOptions', () => {
  it('labels a single-hop chain as direct without duplicating its exit node', () => {
    const options = buildLinkOptions(
      [chain(1, 10, [hop(1, 'exit', 10)])],
    )

    expect(options).toEqual([
      expect.objectContaining({
			chainId: 1,
        name: '直连 #1',
        type: 'direct',
        detail: 'Server 1',
      }),
    ])
  })

  it('labels a multi-hop chain as relay', () => {
    const options = buildLinkOptions(
      [chain(2, 20, [hop(1, 'entry'), hop(2, 'exit', 20)])],
    )

    expect(options).toEqual([
      expect.objectContaining({
			chainId: 2,
        name: '中转 #2',
        type: 'relay',
        detail: 'Server 1 → Server 2',
      }),
    ])
  })
})
