import { describe, expect, it } from 'vitest'
import type { Server } from '@/lib/types'
import { CARD_H, CARD_W } from '../chain-wizard/chain-canvas'
import {
  COL_GAP,
  TRIGGER_H,
  TRIGGER_W,
  addServer,
  addTrigger,
  autoLayout,
  canConnect,
  clearGraph,
  connect,
  createEditorState,
  moveNode,
  removeEdge,
  removeNode,
  serverEligibility,
  setServerListenPort,
  toSavePayload,
  validateGraph,
  validateGraphIssues,
} from './orchestration-graph'

const makeServer = (overrides: Partial<Server> = {}): Server =>
  ({
    id: 7,
    alias: 'Root-JP',
    kind: 'managed',
    connection_state: 'online',
    machine_type: 'direct',
    addresses: ['198.51.100.7'],
    address: '198.51.100.7',
    learned_addr: '',
    allowed_ports: [],
    country_code: 'JP',
    location: 'Tokyo',
    non_direct: false,
    ...overrides,
  }) as unknown as Server

describe('orchestration editor model', () => {
  it('空编排以当前服务器为根并保留初始地址与端口', () => {
    const state = createEditorState({
      chainId: 1,
      seq: 0,
      activeServerId: 7,
      initialAddress: '198.51.100.7',
      initialListenPort: '443',
    })
    expect(state.nodes).toHaveLength(1)
    expect(state.nodes[0]).toMatchObject({
      kind: 'server',
      serverId: 7,
      isRoot: true,
      address: '198.51.100.7',
      listenPort: '443',
      x: 0,
      y: -CARD_H / 2,
    })
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
    expect(recovery.x).toBeLessThan(state.nodes[0].x)
  })

  it('触发器保持单目标：连到新服务器时重指，恢复触发器只能连根，禁止指向自身所属服务器', () => {
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
    // 禁止指向所属服务器（root -> trigger -> root）
    expect(canConnect(state, trigger.key, root.key).ok).toBe(false)
    expect(connect(state, trigger.key, root.key).edges).toEqual(state.edges)
    // 恢复触发器只能指向根。
    state = addTrigger(state, { subject: 'root', action: 'recovered' })
    const recovery = state.nodes.find((node) => node.subject === 'root')!
    expect(canConnect(state, recovery.key, backupA.key).ok).toBe(false)
    expect(connect(state, recovery.key, backupA.key).edges).toEqual(state.edges)
    expect(canConnect(state, root.key, recovery.key).ok).toBe(false)
  })

  it('根与当前运行服务器不可删除；已切换状态清空图后校验仍可通过', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    const root = state.nodes[0]
    const backup = state.nodes[1]
    expect(removeNode(state, root.key)).toBe(state)
    const asActive = { ...state, activeServerId: 8 }
    expect(removeNode(asActive, backup.key)).toBe(asActive)
    const cleared = clearGraph(asActive)
    expect(cleared.nodes).toHaveLength(1)
    expect(cleared.edges).toEqual([])
    expect(validateGraph(cleared)).toEqual([])
    expect(toSavePayload(cleared)).toEqual({ chain_id: 1, seq: 0, nodes: [], edges: [] })
  })

  it('非根环与重复条件会被拒绝并标记问题节点', () => {
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
    const issues = validateGraphIssues(loop)
    const cycleIssue = issues.find((issue) => issue.message.includes('非根环'))
    expect(cycleIssue).toBeTruthy()
    expect(cycleIssue?.nodeKeys?.length).toBeGreaterThan(0)
  })

  it('自动布局按列实际宽度留对称间距并纵向居中', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'self', action: 'ban_banned' })
    const root = state.nodes[0]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    const backup = state.nodes.find((node) => node.kind === 'server' && node.serverId === 8)!
    state = connect(state, root.key, trigger.key)
    state = connect(state, trigger.key, backup.key)
    state = autoLayout(state)
    const pRoot = state.nodes.find((node) => node.key === root.key)!
    const pTrig = state.nodes.find((node) => node.key === trigger.key)!
    const pBack = state.nodes.find((node) => node.key === backup.key)!
    expect(pRoot.x).toBe(0)
    expect(pTrig.x).toBe(CARD_W + COL_GAP)
    expect(pBack.x).toBe(CARD_W + COL_GAP + TRIGGER_W + COL_GAP)
    // 每列单节点围绕 y=0 居中，端口中线对齐 y=0
    expect(pRoot.y + CARD_H / 2).toBe(0)
    expect(pTrig.y + TRIGGER_H / 2).toBe(0)
    expect(pBack.y + CARD_H / 2).toBe(0)
  })

  it('手动拖拽与连线/删边不触发自动重排，拖入定位落在指定坐标中心', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' }, { x: 500, y: 200 })
    const trig = state.nodes.find((node) => node.kind === 'trigger')!
    expect(trig.x).toBe(Math.round(500 - TRIGGER_W / 2))
    expect(trig.y).toBe(Math.round(200 - TRIGGER_H / 2))
    state = addServer(state, 8, { x: 850, y: 240 })
    const backup = state.nodes.find((node) => node.serverId === 8)!
    state = moveNode(state, backup.key, 920, 310)
    state = connect(state, state.nodes[0].key, trig.key)
    state = connect(state, trig.key, backup.key)
    expect(state.nodes.find((node) => node.key === backup.key)).toMatchObject({ x: 920, y: 310 })
    state = removeEdge(state, trig.key, backup.key)
    expect(state.nodes.find((node) => node.key === backup.key)).toMatchObject({ x: 920, y: 310 })
  })

  it('上下文校验覆盖 NAT 端口段、跨跳复用、非直连封禁触发器、节点定位', () => {
    const serverMap = new Map<number, Server>([
      [
        7,
        makeServer({ id: 7, alias: 'Root', allowed_ports: [{ pub_start: 10000, pub_end: 10010 }] }),
      ],
      [8, makeServer({ id: 8, alias: 'Backup-NonDirect', non_direct: true })],
      [9, makeServer({ id: 9, alias: 'Backup-OtherHop' })],
      [10, makeServer({ id: 10, alias: 'External-Exit', kind: 'external' })],
      [11, makeServer({ id: 11, alias: 'Exit-NAT', machine_type: 'nat', allowed_ports: [] })],
    ])
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = setServerListenPort(state, 's0', '20000')
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    state = addTrigger(state, { subject: 'prev', action: 'segment_timeout' })
    const [tSelf, tPrev] = state.nodes.filter((n) => n.kind === 'trigger')
    const backup = state.nodes.find((n) => n.serverId === 8)!
    state = connect(state, 's0', tSelf.key)
    state = connect(state, tSelf.key, backup.key)
    state = connect(state, backup.key, tPrev.key)
    state = connect(state, tPrev.key, 's0')

    // 禁止外部/非直连节点连 self 触发器
    expect(canConnect(state, backup.key, tSelf.key, { serverMap }).ok).toBe(false)

    const issues = validateGraphIssues(state, {
      seq: 0,
      totalHops: 2,
      hopRole: 'entry',
      primaryIsExternal: false,
      serverMap,
      blockedServerIds: new Set([8]),
    })
    expect(issues.some((i) => i.message.includes('可用段内') && i.nodeKeys?.includes('s0'))).toBe(
      true,
    )
    expect(
      issues.some((i) => i.message.includes('上一跳') && i.nodeKeys?.includes(tPrev.key)),
    ).toBe(true)
    expect(
      issues.some((i) => i.message.includes('其他跳') && i.nodeKeys?.includes(backup.key)),
    ).toBe(true)

    // 列表资格检查
    expect(serverEligibility(serverMap.get(7)!, state, { hopRole: 'entry' })).toEqual({
      eligible: false,
      reason: '已在图中',
    })
    expect(
      serverEligibility(serverMap.get(9)!, state, {
        hopRole: 'entry',
        blockedServerIds: new Set([9]),
      }),
    ).toEqual({ eligible: false, reason: '其他跳已用' })
    expect(serverEligibility(serverMap.get(10)!, state, { hopRole: 'entry' })).toEqual({
      eligible: false,
      reason: '仅可作出口',
    })
    expect(serverEligibility(serverMap.get(11)!, state, { hopRole: 'entry' })).toEqual({
      eligible: false,
      reason: '无入站能力',
    })
  })

  it('触发器入边唯一：上游改连时替换原来源', () => {
    let state = createEditorState({ chainId: 1, seq: 0, activeServerId: 7 })
    state = addServer(state, 8)
    state = addTrigger(state, { subject: 'self', action: 'ban_suspected' })
    const root = state.nodes[0]
    const spare = state.nodes[1]
    const trigger = state.nodes.find((node) => node.kind === 'trigger')!
    state = connect(state, root.key, trigger.key)
    expect(state.edges.filter((edge) => edge.to === trigger.key)).toHaveLength(1)
    const next = connect(state, spare.key, trigger.key)
    const after = next.edges.filter((edge) => edge.to === trigger.key)
    expect(after).toHaveLength(1)
    expect(after[0].from).toBe(spare.key)
  })
})
