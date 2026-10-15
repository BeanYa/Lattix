import { afterEach, describe, expect, it, vi } from 'vitest'

import type { TrafficStatBreakdownRow, TrafficStatGranularity, TrafficStatPoint } from '@/lib/types'

// useTrafficStatsDialog 使用 useState/useRef/useEffect：以最小状态表 + 副作用表
// 替代 React 渲染器，在无 DOM 的 node 环境驱动 hook（先例：use-traffic-stats.test.ts）。
/* eslint-disable react-hooks/rules-of-hooks -- 测试以手动"渲染"循环调用 hook，无 React 渲染器可用 */
const h = vi.hoisted(() => {
  const states: unknown[] = []
  const refs: Array<{ current: unknown }> = []
  const effects: Array<{ deps?: unknown[]; cleanup?: () => void }> = []
  let stateCursor = 0
  let refCursor = 0
  let effectCursor = 0
  const points: TrafficStatPoint[] = [
    { ts: 1, up: 10, down: 20 },
    { ts: 2, up: 30, down: 40 },
  ]
  const rows: TrafficStatBreakdownRow[] = [
    { dim_type: 'user', dim_id: 'uuid-aaaa-bbbb', label: 'Alice', up: 5, down: 6 },
    { dim_type: 'node', dim_id: 'node-1', label: '', up: 7, down: 8 },
    { dim_type: 'server', dim_id: '3', label: '节点C', up: 9, down: 10 },
  ]
  return {
    states,
    points,
    rows,
    beginRender: () => {
      stateCursor = 0
      refCursor = 0
      effectCursor = 0
    },
    reset: () => {
      states.length = 0
      refs.length = 0
      effects.length = 0
    },
    useState: (init: unknown) => {
      const i = stateCursor++
      if (i >= states.length) {
        states.push(typeof init === 'function' ? (init as () => unknown)() : init)
      }
      return [
        states[i],
        (next: unknown) => {
          states[i] =
            typeof next === 'function' ? (next as (prev: unknown) => unknown)(states[i]) : next
        },
      ] as const
    },
    useRef: (init: unknown) => {
      const i = refCursor++
      if (i >= refs.length) refs.push({ current: init })
      return refs[i]
    },
    useCallback: <T>(fn: T): T => fn,
    useEffect: (effect: () => void | (() => void), deps?: readonly unknown[]) => {
      const i = effectCursor++
      const prev = effects[i]
      const prevDeps = prev?.deps
      const changed =
        prev === undefined ||
        prevDeps === undefined ||
        deps === undefined ||
        deps.length !== prevDeps.length ||
        deps.some((d, j) => !Object.is(d, prevDeps[j]))
      if (changed) {
        if (prev?.cleanup) prev.cleanup()
        const cleanup = effect()
        effects[i] = { deps: deps ? [...deps] : undefined, cleanup: cleanup ?? undefined }
      }
    },
    // 带参数签名声明 mock，使 mock.calls[i][0] 携带查询参数类型（先例：use-traffic-stats.test.ts）。
    statsTrafficSeries: vi.fn(
      async (_p: {
        dim: 'fleet' | 'server' | 'user'
        server_id?: number
        user_uuid?: string
        granularity: TrafficStatGranularity
        from?: number
        to?: number
      }) => points,
    ),
    statsTrafficBreakdown: vi.fn(
      async (_p: { server_id?: number; user_uuid?: string; from?: number; to?: number }) => rows,
    ),
  }
})

vi.mock('react', () => ({
  useState: h.useState,
  useRef: h.useRef,
  useCallback: h.useCallback,
  useEffect: h.useEffect,
}))
vi.mock('@/lib/api', () => ({
  api: {
    statsTrafficSeries: h.statsTrafficSeries,
    statsTrafficBreakdown: h.statsTrafficBreakdown,
  },
  errorMessage: (err: unknown) => String(err),
}))

import {
  trafficStatsRangeParams,
  useTrafficStatsDialog,
  type TrafficStatsDialogController,
} from './use-traffic-stats-dialog'

const NOW = new Date(2026, 9, 15, 14, 0).getTime()
const HOUR = 3_600_000
const DAY = 86_400_000
// 区间计算内部用 ms，出口契约为 Unix 秒：期望值统一以 sec() 换算。
const sec = (ms: number) => Math.floor(ms / 1000)

function setup(onError: (msg: string) => void = () => {}) {
  h.reset()
  // 每个用例重置默认回包，避免上个用例的 mockImplementation 泄漏。
  h.statsTrafficSeries.mockReset().mockImplementation(async () => h.points)
  h.statsTrafficBreakdown.mockReset().mockImplementation(async () => h.rows)
  let controller!: TrafficStatsDialogController
  const render = () => {
    h.beginRender()
    controller = useTrafficStatsDialog({ onError })
  }
  render()
  // 异步回包写入状态后需再渲染一次，控制器快照才能取到新状态（模拟 React 重渲染）。
  const settle = async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 0))
    render()
  }
  return {
    get controller() {
      return controller
    },
    settle,
    act(fn: (c: TrafficStatsDialogController) => void) {
      fn(controller)
      render()
    },
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('trafficStatsRangeParams', () => {
  it('预设区间：24h→15m、7d→1h、30d→day，窗口从 now 向前推且 to=now（忽略 customRange，Unix 秒）', () => {
    const custom = { from: '', to: '' }
    expect(trafficStatsRangeParams('24h', custom, NOW)).toEqual({
      granularity: '15m',
      from: sec(NOW - 24 * HOUR),
      to: sec(NOW),
    })
    expect(trafficStatsRangeParams('7d', custom, NOW)).toEqual({
      granularity: '1h',
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })
    expect(trafficStatsRangeParams('30d', custom, NOW)).toEqual({
      granularity: 'day',
      from: sec(NOW - 30 * DAY),
      to: sec(NOW),
    })
  })

  it('custom 合法区间：day 粒度，from 当日零点、to 当日末（同日区间合法，Unix 秒）', () => {
    expect(
      trafficStatsRangeParams('custom', { from: '2026-09-20', to: '2026-10-10' }, NOW),
    ).toEqual({
      granularity: 'day',
      from: sec(new Date('2026-09-20T00:00:00').getTime()),
      to: sec(new Date('2026-10-10T23:59:59.999').getTime()),
    })
    expect(
      trafficStatsRangeParams('custom', { from: '2026-10-10', to: '2026-10-10' }, NOW),
    ).toBeTypeOf('object')
  })

  it('custom 非法区间（倒置/空串/非 YYYY-MM-DD）返回 null', () => {
    expect(
      trafficStatsRangeParams('custom', { from: '2026-10-10', to: '2026-09-20' }, NOW),
    ).toBeNull()
    expect(trafficStatsRangeParams('custom', { from: '', to: '2026-10-10' }, NOW)).toBeNull()
    expect(trafficStatsRangeParams('custom', { from: '2026-10-10', to: '' }, NOW)).toBeNull()
    expect(
      trafficStatsRangeParams('custom', { from: '2026/10/01', to: '2026-10-10' }, NOW),
    ).toBeNull()
    expect(
      trafficStatsRangeParams('custom', { from: '2026-10-1', to: '2026-10-10' }, NOW),
    ).toBeNull()
  })
})

describe('useTrafficStatsDialog', () => {
  it('初始：scope 为空、rangeKey 默认 7d、未发任何请求', () => {
    const t = setup()
    expect(t.controller.scope).toBeNull()
    expect(t.controller.rangeKey).toBe('7d')
    expect(t.controller.points).toEqual([])
    expect(t.controller.rows).toEqual([])
    expect(h.statsTrafficSeries).not.toHaveBeenCalled()
    expect(h.statsTrafficBreakdown).not.toHaveBeenCalled()
  })

  it('open(server)：series 取 dim server/1h/近7天窗口，breakdown 取 server_id 同窗口，状态落地', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    expect(h.statsTrafficSeries).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'server',
      server_id: 7,
      granularity: '1h',
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })
    expect(h.statsTrafficBreakdown).toHaveBeenCalledOnce()
    expect(h.statsTrafficBreakdown.mock.calls[0][0]).toEqual({
      server_id: 7,
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })
    expect(t.controller.scope).toEqual({ kind: 'server', serverId: 7, serverName: '节点A' })
    expect(t.controller.points).toEqual(h.points)
    expect(t.controller.rows).toEqual(h.rows)
    expect(t.controller.loading).toBe(false)
    // 再渲染（如父组件以新 onError 闭包重渲染）不应触发重拉。
    await t.settle()
    expect(h.statsTrafficSeries).toHaveBeenCalledOnce()
  })

  it('setRange 切换粒度：30d→day/近30天、24h→15m/近24小时', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    h.statsTrafficSeries.mockClear()
    h.statsTrafficBreakdown.mockClear()

    t.act((c) => c.setRange('30d'))
    await t.settle()
    expect(t.controller.rangeKey).toBe('30d')
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'server',
      server_id: 7,
      granularity: 'day',
      from: sec(NOW - 30 * DAY),
      to: sec(NOW),
    })
    expect(h.statsTrafficBreakdown.mock.calls[0][0]).toEqual({
      server_id: 7,
      from: sec(NOW - 30 * DAY),
      to: sec(NOW),
    })

    h.statsTrafficSeries.mockClear()
    t.act((c) => c.setRange('24h'))
    await t.settle()
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'server',
      server_id: 7,
      granularity: '15m',
      from: sec(NOW - 24 * HOUR),
      to: sec(NOW),
    })
  })

  it('custom 合法区间：day 粒度带 from/to 当日零点与当日末', async () => {
    const t = setup()
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    t.act((c) => c.setCustomRange({ from: '2026-09-20', to: '2026-10-10' }))
    await t.settle()
    h.statsTrafficSeries.mockClear()
    h.statsTrafficBreakdown.mockClear()

    t.act((c) => c.setRange('custom'))
    await t.settle()
    expect(t.controller.rangeKey).toBe('custom')
    expect(h.statsTrafficSeries).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'server',
      server_id: 7,
      granularity: 'day',
      from: sec(new Date('2026-09-20T00:00:00').getTime()),
      to: sec(new Date('2026-10-10T23:59:59.999').getTime()),
    })
    expect(h.statsTrafficBreakdown.mock.calls[0][0]).toEqual({
      server_id: 7,
      from: sec(new Date('2026-09-20T00:00:00').getTime()),
      to: sec(new Date('2026-10-10T23:59:59.999').getTime()),
    })
  })

  it('custom 倒置区间：onError 且不发请求', async () => {
    const onError = vi.fn()
    const t = setup(onError)
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    t.act((c) => c.setCustomRange({ from: '2026-10-10', to: '2026-09-20' }))
    await t.settle()
    h.statsTrafficSeries.mockClear()
    h.statsTrafficBreakdown.mockClear()
    expect(onError).not.toHaveBeenCalled()

    t.act((c) => c.setRange('custom'))
    await t.settle()
    expect(onError).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries).not.toHaveBeenCalled()
    expect(h.statsTrafficBreakdown).not.toHaveBeenCalled()
  })

  it('drillRow(user 行)：切换 user 视图并重拉 breakdown user_uuid / series dim user；user 视图下 server 行可反向下钻；其余行忽略', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    const userRow = t.controller.rows.find((row) => row.dim_type === 'user')
    expect(userRow).toBeDefined()

    t.act((c) => c.drillRow(userRow!))
    await t.settle()
    expect(t.controller.scope).toEqual({
      kind: 'user',
      userUuid: 'uuid-aaaa-bbbb',
      userName: 'Alice',
    })
    expect(h.statsTrafficSeries.mock.calls.at(-1)![0]).toEqual({
      dim: 'user',
      user_uuid: 'uuid-aaaa-bbbb',
      granularity: '1h',
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })
    expect(h.statsTrafficBreakdown.mock.calls.at(-1)![0]).toEqual({
      user_uuid: 'uuid-aaaa-bbbb',
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })

    // user 视图下 server 行 → 反向下钻回 server 视图（保留当前区间）。
    const serverRow = t.controller.rows.find((row) => row.dim_type === 'server')
    t.act((c) => c.drillRow(serverRow!))
    await t.settle()
    expect(t.controller.scope).toEqual({ kind: 'server', serverId: 3, serverName: '节点C' })
    expect(h.statsTrafficBreakdown.mock.calls.at(-1)![0]).toEqual({
      server_id: 3,
      from: sec(NOW - 7 * DAY),
      to: sec(NOW),
    })

    // node 行与同视角行不可下钻：scope 引用不变、不重拉。
    const nodeRow = t.controller.rows.find((row) => row.dim_type === 'node')
    const scopeBefore = t.controller.scope
    const callsBefore = h.statsTrafficBreakdown.mock.calls.length
    t.act((c) => c.drillRow(nodeRow!))
    await t.settle()
    expect(t.controller.scope).toBe(scopeBefore)
    expect(h.statsTrafficBreakdown.mock.calls.length).toBe(callsBefore)
  })

  it('close：scope 置空并清空数据', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    t.act((c) => c.open({ kind: 'server', serverId: 7, serverName: '节点A' }))
    await t.settle()
    expect(t.controller.points).toEqual(h.points)

    t.act((c) => c.close())
    expect(t.controller.scope).toBeNull()
    expect(t.controller.points).toEqual([])
    expect(t.controller.rows).toEqual([])
    expect(t.controller.loading).toBe(false)
  })

  it('scope 切换中途丢弃过期响应', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const responders: Array<(p: TrafficStatPoint[]) => void> = []
    const t = setup()
    h.statsTrafficSeries.mockImplementation(
      () => new Promise<TrafficStatPoint[]>((resolve) => responders.push(resolve)),
    )
    t.act((c) => c.open({ kind: 'server', serverId: 1, serverName: 'A' }))
    t.act((c) => c.open({ kind: 'server', serverId: 2, serverName: 'B' }))
    expect(responders.length).toBe(2)

    responders[0]([{ ts: 1, up: 1, down: 1 }])
    responders[1]([{ ts: 2, up: 2, down: 2 }])
    await t.settle()
    expect(t.controller.points).toEqual([{ ts: 2, up: 2, down: 2 }])
  })
})
