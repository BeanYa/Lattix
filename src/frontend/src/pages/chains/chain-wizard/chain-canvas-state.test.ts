import { describe, expect, it } from 'vitest'
import { INGRESS_NODE_ID, INTERNET_NODE_ID } from './chain-canvas'
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
  it('断开香港出口只删除香港的出线，入网连接保持不变', () => {
    const state = canvasReducer(pair(), { type: 'disconnect', from: 'n1' })
    expect(state.edges).toEqual([{ from: INGRESS_NODE_ID, to: 'n1' }])
  })
  it('重连时保留旧线，完成才替换指定出口，其他入线不变', () => {
    const state = canvasReducer(pair(), { type: 'connect-start', from: 'n1' })
    expect(state.edges).toHaveLength(2)
    const result = canvasReducer(state, { type: 'connect-complete', to: 'n2' })
    expect(result.edges).toEqual([
      { from: INGRESS_NODE_ID, to: 'n1' },
      { from: 'n1', to: 'n2' },
    ])
    expect(result.connecting).toBeNull()
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
  it('已占用入口不可被其他出口静默抢走', () => {
    const original = connect(add(pair(), 3), 'n1', 'n2')
    const result = connect(original, 'n3', 'n2')
    expect(result.edges).toEqual(original.edges)
    expect(result.connectionError).not.toBe('')
  })
  it('导入拓扑也限制每个入口与出口只有一条线，忽略非法端点', () => {
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
      { from: 'n1', to: 'n2' },
      { from: 'n2', to: INTERNET_NODE_ID },
    ])
  })
  it('来源连接可显式断开和重连，服务器出线不会让来源自动恢复', () => {
    const disconnected = canvasReducer(pair(), { type: 'disconnect', from: INGRESS_NODE_ID })
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
  it('快速添加仍受4跳限制，移除仅清理相邻边，重置恢复初始状态', () => {
    const full = [1, 2, 3, 4, 5].reduce(add, createCanvasState())
    expect(full.nodes).toHaveLength(4)
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
})
