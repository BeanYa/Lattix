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
