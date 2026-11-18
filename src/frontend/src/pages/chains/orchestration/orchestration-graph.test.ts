import { describe, expect, it } from 'vitest'
import {
  CARD_X,
  addServer,
  addTrigger,
  connect,
  createEditorState,
  removeNode,
  toSavePayload,
  validateGraph,
} from './orchestration-graph'

describe('orchestration editor model', () => {
  it('空编排以当前服务器为根', () => {
    const state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    expect(state.nodes).toHaveLength(1)
    expect(state.nodes[0]).toMatchObject({ kind: 'server', serverId: 7, isRoot: true })
    expect(toSavePayload(state)).toEqual({ chain_id: 1, seq: 0, nodes: [], edges: [] })
  })

  it('添加备选与触发器后可通过校验并生成载荷', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    const backup = state.nodes.find((node) => node.kind === 'server' && node.serverId === 8)!
    state = connect(state, root.key, trigger.key)
    state = connect(state, trigger.key, backup.key)
    expect(validateGraph(state)).toEqual([])
    const payload = toSavePayload(state)
    expect(payload.nodes).toHaveLength(3)
    const triggerIndex = payload.nodes.findIndex((node) => node.kind === 'trigger')
    const backupIndex = payload.nodes.findIndex((node) => node.kind === 'server' && !node.is_root)
    expect(payload.edges).toEqual([
      { from: 0, to: triggerIndex },
      { from: triggerIndex, to: backupIndex },
    ])
    expect(payload.nodes[0].is_root).toBe(true)
    expect(payload.nodes[triggerIndex]).toMatchObject({
      kind: 'trigger',
      subject: 'self',
      action: 'ban_suspected',
    })
  })

  it('原节点恢复添加即连根且优先级默认 1', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'root', action: 'recovered' })
    const recovery = state.nodes.find((node) => node.subject === 'root')!
    expect(recovery.priority).toBe('1')
    expect(state.edges).toContainEqual({ from: recovery.key, to: state.nodes[0].key })
  })

  it('触发器保持单目标：连到新服务器时重指，恢复触发器只能连根', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addServer(state, 9)
    state = addTrigger(state, { subject: 'self', action: 'ban_banned' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    const [backupA, backupB] = state.nodes.filter((node) => node.kind === 'server' && !node.isRoot)
    state = connect(state, root.key, trigger.key)
    state = connect(state, trigger.key, backupA.key)
    state = connect(state, trigger.key, backupB.key)
    const outgoing = state.edges.filter((edge) => edge.from === trigger.key)
    expect(outgoing).toHaveLength(1)
    expect(outgoing[0].to).toBe(backupB.key)
    // 恢复触发器只能指向根。
    state = addTrigger(state, { subject: 'root', action: 'recovered' })
    const recovery = state.nodes.find((node) => node.subject === 'root')!
    const blocked = connect(state, recovery.key, backupA.key)
    expect(blocked.edges).toEqual(state.edges)
  })

  it('根与当前运行服务器不可删除', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    const root = state.nodes[0]
    const backup = state.nodes[1]
    expect(removeNode(state, root.key)).toBe(state)
    const asActive = { ...state, activeServerId: 8 }
    expect(removeNode(asActive, backup.key)).toBe(asActive)
  })

  it('非根环与重复条件会被拒绝', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addServer(state, 9)
    state = addTrigger(state, { subject: 'self', action: 'ban_banned' })
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    const root = state.nodes[0]
    const [t1, t2] = state.nodes.filter((node) => node.kind === 'trigger')
    const [a, b] = state.nodes.filter((node) => node.kind === 'server' && !node.isRoot)
    state = connect(state, root.key, t1.key)
    state = connect(state, t1.key, a.key)
    state = connect(state, a.key, t2.key)
    state = connect(state, t2.key, b.key)
    expect(
      validateGraph(state).some(
        (error) => error.includes('无法从根到达') || error.includes('不可达'),
      ),
    ).toBe(false)
    // 非根环需要服务器多来源，用第二个触发器构造。
    let loop = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    loop = addServer(loop, 8)
    loop = addServer(loop, 9)
    loop = addTrigger(loop, { subject: 'self', action: 'ban_banned' })
    loop = addTrigger(loop, { subject: 'self', action: 'ban_suspected' })
    loop = addTrigger(loop, { subject: 'self', action: 'segment_timeout' })
    const lroot = loop.nodes[0]
    const [lt1, lt2, lt3] = loop.nodes.filter((node) => node.kind === 'trigger')
    const [la, lb] = loop.nodes.filter((node) => node.kind === 'server' && !node.isRoot)
    loop = connect(loop, lroot.key, lt1.key)
    loop = connect(loop, lt1.key, la.key)
    loop = connect(loop, la.key, lt2.key)
    loop = connect(loop, lt2.key, lb.key)
    loop = connect(loop, lb.key, lt3.key)
    loop = connect(loop, lt3.key, la.key)
    expect(validateGraph(loop).some((error) => error.includes('非根环'))).toBe(true)
  })

  it('自动布局按 BFS 分层', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'self', action: 'ban_banned' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    const backup = state.nodes.find((node) => node.kind === 'server' && node.serverId === 8)!
    state = connect(state, root.key, trigger.key)
    state = connect(state, trigger.key, backup.key)
    const positioned = state.nodes
    expect(positioned.find((node) => node.key === root.key)?.x).toBe(CARD_X)
    expect(positioned.find((node) => node.key === trigger.key)?.x).toBe(CARD_X * 2)
    expect(positioned.find((node) => node.key === backup.key)?.x).toBe(CARD_X * 3)
  })

  it('同一层的不同父节点子节点不会重叠', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addServer(state, 9)
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    state = addTrigger(state, { subject: 'next', action: 'segment_timeout' })
    const root = state.nodes[0]
    const [t1, t2] = state.nodes.filter((node) => node.kind === 'trigger')
    const [a, b] = state.nodes.filter((node) => node.kind === 'server' && !node.isRoot)
    state = connect(state, root.key, t1.key)
    state = connect(state, t1.key, a.key)
    state = connect(state, root.key, t2.key)
    state = connect(state, t2.key, b.key)
    const posA = state.nodes.find((node) => node.key === a.key)!
    const posB = state.nodes.find((node) => node.key === b.key)!
    expect(posA.x).toBe(posB.x)
    expect(posA.y).not.toBe(posB.y)
  })

  it('未接入图的节点排到最后一列右侧且不与已布局节点重叠', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    state = connect(state, root.key, trigger.key)
    state = addServer(state, 9)
    const loose = state.nodes.find((node) => node.kind === 'server' && !node.isRoot)!
    expect(loose.x).toBeGreaterThan(trigger.x)
    const overlaps = state.nodes.some(
      (other) =>
        other.key !== loose.key &&
        Math.abs(other.x - loose.x) < 1 &&
        Math.abs(other.y - loose.y) < 1,
    )
    expect(overlaps).toBe(false)
  })

  it('触发器换目标服务器时替换出边而不是新增', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addServer(state, 9)
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    const [first, second] = state.nodes.filter((node) => node.kind === 'server' && !node.isRoot)
    state = connect(state, root.key, trigger.key)
    state = connect(state, trigger.key, first.key)
    state = connect(state, trigger.key, second.key)
    const outgoing = state.edges.filter((edge) => edge.from === trigger.key)
    expect(outgoing).toHaveLength(1)
    expect(outgoing[0].to).toBe(second.key)
  })
})
