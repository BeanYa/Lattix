import { useCallback, useEffect, useRef, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import { usePolling } from '@/lib/use-polling'
import type { TrafficStatGranularity, TrafficStatPoint, TrafficStatsOverview } from '@/lib/types'

// 粒度类型复用全局契约（预检裁定：不在本模块本地重定义）。
export type { TrafficStatGranularity }
export type TrafficStatsGranularity = TrafficStatGranularity

// 各粒度展示窗口：5m/15m→6h、1h→24h、day→30d、month→365d。
const WINDOW_HOURS: Record<TrafficStatGranularity, number> = {
  '5m': 6,
  '15m': 6,
  '1h': 24,
  day: 24 * 30,
  month: 24 * 365,
}

export function trafficSeriesWindow(
  g: TrafficStatGranularity,
  now = Date.now(),
): { from: number; to: number } {
  return { from: now - WINDOW_HOURS[g] * 3_600_000, to: now }
}

export interface TrafficStatsController {
  overview: TrafficStatsOverview | null
  points: TrafficStatPoint[]
  granularity: TrafficStatGranularity
  loading: boolean
  setGranularity: (g: TrafficStatGranularity) => void
  errors: { today_error_count: number } | null
}

/**
 * useTrafficStats 为流量统计页控制器：
 * overview+errors 由 usePolling 60s 轮询（挂载全量一次，随后静默刷新）；
 * series 随 granularity 变化由独立 useEffect 立即重拉，与轮询互不重启。
 */
export function useTrafficStats({
  onError,
}: {
  onError: (msg: string) => void
}): TrafficStatsController {
  const [overview, setOverview] = useState<TrafficStatsOverview | null>(null)
  const [points, setPoints] = useState<TrafficStatPoint[]>([])
  const [granularity, setGranularityState] = useState<TrafficStatGranularity>('1h')
  const [loading, setLoading] = useState(true)
  const [errors, setErrors] = useState<{ today_error_count: number } | null>(null)
  const request = useRef(0)

  const load = useCallback(
    async (silent = false, signal?: AbortSignal) => {
      const current = ++request.current
      try {
        const [nextOverview, nextErrors] = await Promise.all([
          api.statsTrafficOverview(),
          api.statsErrorsSummary(),
        ])
        if (signal?.aborted || current !== request.current) return
        setOverview(nextOverview)
        setErrors(nextErrors)
      } catch (err) {
        if (signal?.aborted || current !== request.current) return
        if (!silent) onError(errorMessage(err))
      } finally {
        if (!signal?.aborted && current === request.current) setLoading(false)
      }
    },
    [onError],
  )

  usePolling(load, request, 60000)

  // series 随粒度切换重拉：独立于 60s overview 轮询的副作用。
  useEffect(() => {
    let active = true
    const { from, to } = trafficSeriesWindow(granularity)
    api
      .statsTrafficSeries({ dim: 'fleet', granularity, from, to })
      .then((next) => {
        if (active) setPoints(next)
      })
      .catch((err: unknown) => {
        if (active) onError(errorMessage(err))
      })
    return () => {
      active = false
    }
  }, [granularity, onError])

  const setGranularity = (g: TrafficStatGranularity) => setGranularityState(g)

  return { overview, points, granularity, loading, setGranularity, errors }
}
