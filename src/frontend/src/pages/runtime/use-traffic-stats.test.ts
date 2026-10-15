import { afterEach, describe, expect, it, vi } from 'vitest'

import type { TrafficStatGranularity } from '@/lib/types'

// useTrafficStats 使用 useState/useRef/useCallback/useEffect：以最小状态表 + 副作用表
// 替代 React 渲染器，在无 DOM 的 node 环境驱动 hook（先例：use-chain-form.test.ts）。
/* eslint-disable react-hooks/rules-of-hooks -- 测试以手动"渲染"循环调用 hook，无 React 渲染器可用 */
const h = vi.hoisted(() => {
  const states: unknown[] = []
  const refs: Array<{ current: unknown }> = []
  const effects: Array<{ deps?: unknown[]; cleanup?: () => void }> = []
  let stateCursor = 0
  let refCursor = 0
  let effectCursor = 0
  let pollingArmed = true
  const overview = {
    today: { up: 11, down: 22 },
    today_prev: { up: 1, down: 2 },
    month: { up: 110, down: 220 },
    month_prev: { up: 10, down: 20 },
    agents_online: 2,
    agents_total: 3,
    top_servers: [],
    top_users: [],
  }
  const points = [
    { ts: 1, up: 10, down: 20 },
    { ts: 2, up: 30, down: 40 },
  ]
  return {
    states,
    overview,
    points,
    beginRender: () => {
      stateCursor = 0
      refCursor = 0
      effectCursor = 0
    },
    reset: () => {
      states.length = 0
      refs.length = 0
      effects.length = 0
      pollingArmed = true
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
    // 真实 usePolling 挂载即全量 load 一次（非 silent）后 60s 定时静默刷新；
    // node 环境无 window 定时器，mock 为仅挂载触发一次。
    usePolling: (load: (silent?: boolean, signal?: AbortSignal) => Promise<void>) => {
      if (!pollingArmed) return
      pollingArmed = false
      void load(false)
    },
    statsTrafficOverview: vi.fn(async () => overview),
    statsTrafficSeries: vi.fn(
      async (_p: {
        dim: 'fleet' | 'server' | 'user'
        granularity: TrafficStatGranularity
        from?: number
        to?: number
      }) => points,
    ),
    statsErrorsSummary: vi.fn(async () => ({ today_error_count: 3 })),
  }
})

vi.mock('react', () => ({
  useState: h.useState,
  useRef: h.useRef,
  useCallback: h.useCallback,
  useEffect: h.useEffect,
}))
vi.mock('@/lib/use-polling', () => ({ usePolling: h.usePolling }))
vi.mock('@/lib/api', () => ({
  api: {
    statsTrafficOverview: h.statsTrafficOverview,
    statsTrafficSeries: h.statsTrafficSeries,
    statsErrorsSummary: h.statsErrorsSummary,
  },
  errorMessage: (err: unknown) => String(err),
}))

import { trafficSeriesWindow, useTrafficStats } from './use-traffic-stats'

const NOW = new Date(2026, 9, 15, 14, 0).getTime()
const HOUR = 3_600_000
const DAY = 86_400_000

type Controller = ReturnType<typeof useTrafficStats>

function setup(onError: (msg: string) => void = () => {}) {
  h.reset()
  h.statsTrafficOverview.mockClear()
  h.statsTrafficSeries.mockClear()
  h.statsErrorsSummary.mockClear()
  let controller!: Controller
  const render = () => {
    h.beginRender()
    controller = useTrafficStats({ onError })
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
    act(fn: (c: Controller) => void) {
      fn(controller)
      render()
    },
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('trafficSeriesWindow', () => {
  it('各粒度窗口：5m/15m→6h、1h→24h、day→30d、month→365d', () => {
    expect(trafficSeriesWindow('5m', NOW)).toEqual({ from: NOW - 6 * HOUR, to: NOW })
    expect(trafficSeriesWindow('15m', NOW)).toEqual({ from: NOW - 6 * HOUR, to: NOW })
    expect(trafficSeriesWindow('1h', NOW)).toEqual({ from: NOW - 24 * HOUR, to: NOW })
    expect(trafficSeriesWindow('day', NOW)).toEqual({ from: NOW - 30 * DAY, to: NOW })
    expect(trafficSeriesWindow('month', NOW)).toEqual({ from: NOW - 365 * DAY, to: NOW })
  })

  it('缺省 now 取当前时间', () => {
    const before = Date.now()
    const window = trafficSeriesWindow('1h')
    const after = Date.now()
    expect(window.to).toBeGreaterThanOrEqual(before)
    expect(window.to).toBeLessThanOrEqual(after)
    expect(window.to - window.from).toBe(24 * HOUR)
  })
})

describe('useTrafficStats', () => {
  it('初始加载：overview+errors 各一次；series 取 fleet/1h 与 24h 窗口并落地状态', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    expect(t.controller.overview).toBeNull()
    expect(t.controller.errors).toBeNull()
    expect(t.controller.loading).toBe(true)
    await t.settle()
    expect(h.statsTrafficOverview).toHaveBeenCalledOnce()
    expect(h.statsErrorsSummary).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'fleet',
      granularity: '1h',
      from: NOW - 24 * HOUR,
      to: NOW,
    })
    expect(t.controller.overview).toEqual(h.overview)
    expect(t.controller.errors).toEqual({ today_error_count: 3 })
    expect(t.controller.points).toEqual(h.points)
    expect(t.controller.granularity).toBe('1h')
    expect(t.controller.loading).toBe(false)
  })

  it('setGranularity("day")：仅 series 以 day/30d 窗口重拉，overview 轮询不重启', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    const t = setup()
    await t.settle()
    h.statsTrafficSeries.mockClear()
    h.statsTrafficOverview.mockClear()
    t.act((c) => c.setGranularity('day'))
    await t.settle()
    expect(t.controller.granularity).toBe('day')
    expect(h.statsTrafficSeries).toHaveBeenCalledOnce()
    expect(h.statsTrafficSeries.mock.calls[0][0]).toEqual({
      dim: 'fleet',
      granularity: 'day',
      from: NOW - 30 * DAY,
      to: NOW,
    })
    expect(h.statsTrafficOverview).not.toHaveBeenCalled()
    expect(t.controller.points).toEqual(h.points)
  })
})
