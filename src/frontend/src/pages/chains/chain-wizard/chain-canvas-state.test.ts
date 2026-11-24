import { describe, expect, it } from 'vitest'
import {
  CARD_H,
  CARD_W,
  INGRESS_NODE_ID,
  INGRESS_POS,
  INTERNET_NODE_ID,
  INTERNET_POS,
  INTERNET_R,
  MAX_CANVAS_NODES,
} from './chain-canvas'
import { canvasReducer, createCanvasState, type CanvasState } from './chain-canvas-state'

function add(state: CanvasState, serverId: number) {
  return canvasReducer(state, { type: 'add', serverId, point: { x: serverId * 350, y: 200 } })
}
function connect(state: CanvasState, from: string, to: string) {
  return canvasReducer(canvasReducer(state, { type: 'connect-start', from }), {
    type: 'connect-complete',
    to,
  })
}
function pair() {
  return add(add(createCanvasState(), 1), 2)
}

describe('explicit canvas connection state', () => {
  it('初始状态与清空后明确保存入网流量到 Internet 的直连', () => {
    const initial = createCanvasState()
    expect(initial.edges).toEqual([{ from: INGRESS_NODE_ID, to: INTERNET_NODE_ID }])
    expect(canvasReducer(pair(), { type: 'reset' }).edges).toEqual(initial.edges)
    const removed = canvasReducer(add(initial, 1), { type: 'remove', id: 'n1' })
    expect(removed.edges).toEqual(initial.edges)
  })
  it('第一次添加写入来源连接，增加孤立节点不会改变入口', () => {
    const state = pair()
    expect(state.edges).toEqual([
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: INTERNET_NODE_ID },
    ])
    expect(state.nodes).toHaveLength(2)
  })
  it('断开指定出线只删除该连线，入网连接保持不变', () => {
    const state = canvasReducer(pair(), {
      type: 'disconnect',
      from: 'n1',
      to: INTERNET_NODE_ID,
    })
    expect(state.edges).toEqual([{ from: INGRESS_NODE_ID, to: 'n1' }])
  })
  it('重连时保留旧线，完成才替换末端占位线，其他入线不变', () => {
    const state = canvasReducer(pair(), { type: 'connect-start', from: 'n1' })
    expect(state.edges).toHaveLength(2)
    const result = canvasReducer(state, { type: 'connect-complete', to: 'n2' })
    expect(result.edges).toEqual([
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: 'n2' },
    ])
    expect(result.connecting).toBeNull()
  })
  it('允许分支与汇合：同一出口可拉多条线，同一入口可被多个上游连接', () => {
    const state = add(pair(), 3)
    const branched = connect(connect(state, 'n1', 'n2'), 'n1', 'n3')
    expect(branched.edges).toContainEqual({ from: 'n1', to: 'n2' })
    expect(branched.edges).toContainEqual({ from: 'n1', to: 'n3' })
    const merged = connect(branched, 'n2', 'n3')
    expect(merged.edges).toContainEqual({ from: 'n2', to: 'n3' })
    // 断开只影响被点的那条线（n1→n3 保留）。
    const unlinked = canvasReducer(merged, { type: 'disconnect', from: 'n1', to: 'n2' })
    expect(unlinked.edges).toContainEqual({ from: 'n1', to: 'n3' })
    expect(unlinked.edges.some((edge) => edge.from === 'n1' && edge.to === 'n2')).toBe(false)
  })
  it('可从 Internet 终端拉分支，且分支中的环仍被拒绝', () => {
    const state = connect(pair(), 'n1', 'n2')
    const both = connect(state, 'n1', INTERNET_NODE_ID)
    expect(both.edges).toContainEqual({ from: 'n1', to: 'n2' })
    expect(both.edges).toContainEqual({ from: 'n1', to: INTERNET_NODE_ID })
    const cycle = connect(connect(add(pair(), 3), 'n1', 'n2'), 'n2', 'n3')
    const rejected = connect(cycle, 'n3', 'n1')
    expect(rejected.edges).toEqual(cycle.edges)
    expect(rejected.connectionError).not.toBe('')
  })
  it('取消或再次点相同出口不丢失原连接', () => {
    const original = pair()
    const active = canvasReducer(original, { type: 'connect-start', from: 'n1' })
    expect(canvasReducer(active, { type: 'connect-cancel' }).edges).toEqual(original.edges)
    expect(canvasReducer(active, { type: 'connect-start', from: 'n1' }).connecting).toBeNull()
  })
  it('拒绝自连和环路，图保持不变', () => {
    const original = connect(pair(), 'n1', 'n2')
    for (const [from, to] of [
      ['n1', 'n1'],
      ['n2', 'n1'],
    ]) {
      const result = connect(original, from, to)
      expect(result.edges).toEqual(original.edges)
      expect(result.connectionError).not.toBe('')
    }
  })
  it('已占用入口可被其他上游汇合（多入边）', () => {
    const original = connect(add(pair(), 3), 'n1', 'n2')
    const result = connect(original, 'n3', 'n2')
    expect(result.edges).toContainEqual({ from: 'n1', to: 'n2' })
    expect(result.edges).toContainEqual({ from: 'n3', to: 'n2' })
    expect(result.connectionError).toBe('')
  })
  it('导入拓扑允许分支/汇合，忽略环与非法端点', () => {
    const state = add(pair(), 3)
    const result = canvasReducer(state, {
      type: 'prefill',
      nodes: state.nodes,
      edges: [
        { from: INGRESS_NODE_ID, to: 'n1' },
        { from: INGRESS_NODE_ID, to: 'n2' },
        { from: 'n1', to: 'n2' },
        { from: 'n1', to: 'n3' },
        { from: 'n3', to: 'n2' },
        { from: 'n2', to: INTERNET_NODE_ID },
        { from: 'n3', to: INTERNET_NODE_ID },
        { from: INTERNET_NODE_ID, to: 'n3' },
        { from: 'n3', to: INGRESS_NODE_ID },
        { from: 'n3', to: 'n3' },
        { from: 'missing', to: 'n3' },
        { from: 'n3', to: 'missing' },
      ],
    })
    expect(result.edges).toEqual([
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: INGRESS_NODE_ID, to: 'n2' },
      { from: 'n1', to: 'n2' },
      { from: 'n1', to: 'n3' },
      { from: 'n3', to: 'n2' },
      { from: 'n2', to: INTERNET_NODE_ID },
      { from: 'n3', to: INTERNET_NODE_ID },
    ])
  })
  it('来源连接可显式断开和重连，服务器出线不会让来源自动恢复', () => {
    const disconnected = canvasReducer(pair(), {
      type: 'disconnect',
      from: INGRESS_NODE_ID,
      to: 'n1',
    })
    const state = connect(disconnected, 'n1', 'n2')
    expect(state.edges.some((e) => e.from === INGRESS_NODE_ID)).toBe(false)
    expect(connect(state, INGRESS_NODE_ID, 'n1').edges).toContainEqual({
      from: INGRESS_NODE_ID,
      to: 'n1',
    })
  })
  it('拒绝不存在的端点和从来源直接跳过服务器', () => {
    const original = pair()
    expect(connect(original, 'n1', 'missing').edges).toEqual(original.edges)
    expect(connect(original, INGRESS_NODE_ID, INTERNET_NODE_ID).edges).toEqual(original.edges)
    expect(
      canvasReducer(original, { type: 'connect-start', from: INTERNET_NODE_ID }).connecting,
    ).toBeNull()
  })
  it('画布节点受上限约束，移除仅清理相邻边，重置恢复初始状态', () => {
    const full = Array.from({ length: MAX_CANVAS_NODES + 1 }, (_, index) => index + 1).reduce(
      add,
      createCanvasState(),
    )
    expect(full.nodes).toHaveLength(MAX_CANVAS_NODES)
    const linked = connect(connect(full, 'n1', 'n2'), 'n2', INTERNET_NODE_ID)
    const result = canvasReducer(linked, { type: 'remove', id: 'n2' })
    expect(result.edges).toEqual([{ from: INGRESS_NODE_ID, to: 'n1' }])
    expect(canvasReducer(result, { type: 'reset' })).toEqual(createCanvasState())
  })
  it('节点与终端可移到负坐标，非法坐标不会污染图', () => {
    const state = pair()
    expect(
      canvasReducer(state, { type: 'move', id: 'n1', point: { x: -400, y: -200 } }).nodes[0].x,
    ).toBe(-400)
    expect(
      canvasReducer(state, {
        type: 'terminal-move',
        id: INGRESS_NODE_ID,
        point: { x: -80, y: -60 },
      }).ingressPos,
    ).toEqual({ x: -80, y: -60 })
    expect(canvasReducer(state, { type: 'move', id: 'n1', point: { x: NaN, y: 3 } })).toBe(state)
  })
  it('原地替换节点服务器时保留坐标、端口与全部连线，并拒绝重复服务器', () => {
    let state = connect(connect(pair(), 'n1', 'n2'), 'n2', INTERNET_NODE_ID)
    state = canvasReducer(state, { type: 'address', id: 'n1', value: '203.0.113.10' })
    state = canvasReducer(state, { type: 'port', id: 'n1', value: '8443' })
    const replaced = canvasReducer(state, { type: 'replace', id: 'n1', serverId: 9 })
    expect(replaced.nodes[0]).toMatchObject({
      id: 'n1',
      serverId: 9,
      x: 350 - 132,
      y: 200 - 100,
      address: '',
      port: '8443',
    })
    expect(replaced.edges).toEqual(state.edges)
    // 不允许替换成画布中已存在的其他服务器
    const duplicate = canvasReducer(replaced, { type: 'replace', id: 'n1', serverId: 2 })
    expect(duplicate).toBe(replaced)
  })
  it('prefill 多跳拓扑时自动把入网流量和 Internet 放到首末节点两侧不遮挡卡片', () => {
    const prefilled = canvasReducer(createCanvasState(), {
      type: 'prefill',
      nodes: [
        { id: 'n1', serverId: 1, x: 240, y: 240, port: '8443', address: '' },
        { id: 'n2', serverId: 2, x: 600, y: 240, port: '', address: '' },
        { id: 'n3', serverId: 3, x: 960, y: 240, port: '443', address: '' },
      ],
      edges: [
        { from: INGRESS_NODE_ID, to: 'n1' },
        { from: 'n1', to: 'n2' },
        { from: 'n2', to: 'n3' },
        { from: 'n3', to: INTERNET_NODE_ID },
      ],
    })
    const midY = 240 + CARD_H / 2
    expect(prefilled.ingressPos.x + INTERNET_R).toBeLessThan(240)
    expect(prefilled.internetPos.x - INTERNET_R).toBeGreaterThan(960 + CARD_W)
    expect(prefilled.ingressPos.y).toBe(midY)
    expect(prefilled.internetPos.y).toBe(midY)

    const empty = canvasReducer(prefilled, { type: 'prefill', nodes: [], edges: [] })
    expect(empty.ingressPos).toEqual(INGRESS_POS)
    expect(empty.internetPos).toEqual(INTERNET_POS)
  })
})

describe('local orchestration drafts', () => {
  const draft = {
    template_id: 7,
    template_revision: 1,
    nodes: [
      { kind: 'server' as const, server_id: 1, is_root: true, address: '', listen_port: 8443 },
    ],
    edges: [],
    overrides: [],
  }
  it('adds a template with metadata, retains it on move, and clears it on replacement/removal', () => {
    const state = canvasReducer(createCanvasState(), {
      type: 'template',
      draft,
      point: { x: 300, y: 200 },
    })
    expect(state.nodes[0]).toMatchObject({ serverId: 1, port: '8443', orchestration: draft })
    const moved = canvasReducer(state, { type: 'move', id: 'n1', point: { x: 50, y: 60 } })
    expect(moved.nodes[0].orchestration).toEqual(draft)
    expect(
      canvasReducer(moved, { type: 'replace', id: 'n1', serverId: 2 }).nodes[0].orchestration,
    ).toBeUndefined()
    expect(canvasReducer(moved, { type: 'remove', id: 'n1' }).nodes).toEqual([])
  })
  it('records explicit blank/zero main edits and refreshes only inherited fields', () => {
    let state = canvasReducer(createCanvasState(), {
      type: 'template',
      draft,
      point: { x: 300, y: 200 },
    })
    state = canvasReducer(state, { type: 'port', id: 'n1', value: '' })
    state = canvasReducer(state, { type: 'address', id: 'n1', value: '' })
    expect(state.nodes[0].orchestration?.overrides).toEqual([
      { server_id: 1, listen_port: 0, address: '' },
    ])
    const template = {
      ...draft,
      id: 7,
      revision: 2,
      name: 'Updated',
      usage_count: 0,
      created_at: '',
      updated_at: '',
      nodes: [{ ...draft.nodes[0], listen_port: 9443 }],
    }
    state = canvasReducer(state, { type: 'templates-refresh', templates: [template] })
    expect(state.nodes[0].port).toBe('')
    expect(state.nodes[0].orchestration?.template_revision).toBe(2)
    expect(state.nodes[0].orchestration?.overrides).toEqual([
      { server_id: 1, listen_port: 0, address: '' },
    ])
  })
  it('never silently replaces an existing root on template add', () => {
    const state = add(createCanvasState(), 1)
    const result = canvasReducer(state, { type: 'template', draft, point: { x: 300, y: 200 } })
    expect(result.nodes).toEqual(state.nodes)
    expect(result.connectionError).toContain('确认')
  })
})

describe('candidate conflicts on actual chain paths', () => {
  const graph = (root: number, candidate: number) => ({
    nodes: [
      { kind: 'server' as const, server_id: root, is_root: true },
      { kind: 'trigger' as const, subject: 'self' as const, action: 'ban_banned' as const },
      { kind: 'server' as const, server_id: candidate },
    ],
    edges: [
      { from: 0, to: 1 },
      { from: 1, to: 2 },
    ],
  })
  it('allows candidate reuse on separate branches, rejects a new path containing both groups without losing drafts', () => {
    let state = add(pair(), 3)
    state = canvasReducer(state, { type: 'orchestration', id: 'n2', draft: graph(2, 4) })
    state = canvasReducer(state, { type: 'orchestration', id: 'n3', draft: graph(3, 4) })
    state = connect(
      connect(connect(connect(state, 'n1', 'n2'), 'n1', 'n3'), 'n2', INTERNET_NODE_ID),
      'n3',
      INTERNET_NODE_ID,
    )
    expect(state.connectionError).toBe('')
    const rejected = connect(state, 'n2', 'n3')
    expect(rejected.connectionError).toContain('多个跳位')
    expect(rejected.edges).toEqual(state.edges)
    expect(rejected.nodes).toEqual(state.nodes)
  })
  it('applying an overlapping template is atomic and preserves the existing local graph', () => {
    const state = connect(connect(pair(), 'n1', 'n2'), 'n2', INTERNET_NODE_ID)
    const rejected = canvasReducer(state, {
      type: 'template',
      replaceId: 'n1',
      point: { x: 1, y: 1 },
      draft: graph(1, 2),
    })
    expect(rejected.connectionError).toContain('重复备选')
    expect(rejected.nodes).toEqual(state.nodes)
    expect(rejected.edges).toEqual(state.edges)
  })
})

it('refreshes inherited defaults without turning a runtime allocated port into an explicit override', () => {
  const draft = {
    template_id: 7,
    template_revision: 1,
    nodes: [
      { kind: 'server' as const, server_id: 1, is_root: true, address: '', listen_port: 42123 },
    ],
    edges: [],
    overrides: [],
  }
  let state = canvasReducer(createCanvasState(), {
    type: 'prefill',
    nodes: [
      {
        id: 'n1',
        serverId: 1,
        x: 1,
        y: 2,
        port: '42123',
        address: '',
        orchestration: draft,
        originalSeq: 0,
      },
    ],
    edges: [
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: INTERNET_NODE_ID },
    ],
  })
  const template = {
    id: 7,
    revision: 2,
    name: 'Updated',
    usage_count: 1,
    created_at: '',
    updated_at: '',
    nodes: [{ ...draft.nodes[0], listen_port: 0 }],
    edges: [],
  }
  state = canvasReducer(state, { type: 'templates-refresh', templates: [template] })
  expect(state.nodes[0].port).toBe('')
  expect(state.nodes[0].orchestration?.overrides).toEqual([])
  expect(state.nodes[0].parameterOverrides).toBeUndefined()
  state = canvasReducer(state, {
    type: 'templates-refresh',
    templates: [{ ...template, revision: 3, nodes: [{ ...template.nodes[0], listen_port: 9443 }] }],
  })
  expect(state.nodes[0].port).toBe('9443')
  expect(state.nodes[0].originalSeq).toBe(0)
})

it('retains explicit candidate overrides when intentionally replacing a shared template on the same root', () => {
  const nodes = [
    { kind: 'server' as const, server_id: 1, is_root: true, listen_port: 8443 },
    { kind: 'trigger' as const, subject: 'self' as const, action: 'ban_banned' as const },
    { kind: 'server' as const, server_id: 2, listen_port: 7000 },
  ]
  const edges = [
    { from: 0, to: 1 },
    { from: 1, to: 2 },
  ]
  const original = {
    template_id: 7,
    template_revision: 1,
    nodes,
    edges,
    overrides: [{ server_id: 2, listen_port: 0, address: '' }],
  }
  let state = canvasReducer(createCanvasState(), {
    type: 'template',
    draft: original,
    point: { x: 300, y: 200 },
  })
  state = canvasReducer(state, {
    type: 'template',
    replaceId: 'n1',
    draft: { template_id: 8, template_revision: 1, nodes, edges, overrides: [] },
    point: { x: 300, y: 200 },
  })
  expect(state.nodes[0].orchestration?.overrides).toEqual([
    { server_id: 2, listen_port: 0, address: '' },
  ])
  expect(state.nodes[0].orchestration?.nodes[2]).toMatchObject({ listen_port: 0, address: '' })
})

it('rejects a replacement template that omits the active candidate and keeps the original draft', () => {
  const original = {
    template_id: 7,
    template_revision: 1,
    nodes: [
      { kind: 'server' as const, server_id: 1, is_root: true },
      { kind: 'trigger' as const, subject: 'self' as const, action: 'ban_banned' as const },
      { kind: 'server' as const, server_id: 2 },
    ],
    edges: [
      { from: 0, to: 1 },
      { from: 1, to: 2 },
    ],
    overrides: [],
  }
  const state = canvasReducer(createCanvasState(), {
    type: 'prefill',
    nodes: [
      { id: 'n1', serverId: 2, x: 100, y: 100, port: '', address: '', orchestration: original },
    ],
    edges: [
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: INTERNET_NODE_ID },
    ],
  })
  const result = canvasReducer(state, {
    type: 'template',
    replaceId: 'n1',
    point: { x: 100, y: 100 },
    draft: {
      template_id: 8,
      template_revision: 1,
      nodes: [original.nodes[0]],
      edges: [],
      overrides: [],
    },
  })
  expect(result.connectionError).toContain('当前运行')
  expect(result.nodes).toEqual(state.nodes)
})
