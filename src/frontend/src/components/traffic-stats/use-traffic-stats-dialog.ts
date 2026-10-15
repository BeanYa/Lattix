import { useEffect, useRef, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import { toLocalDateInput } from '@/lib/user-subscription'
import type { TrafficStatBreakdownRow, TrafficStatGranularity, TrafficStatPoint } from '@/lib/types'

const HOUR = 3_600_000
const DAY = 86_400_000

// 下钻弹窗的统计对象：server 视图带 serverId/serverName，user 视图带 userUuid/userName。
export interface TrafficStatsScope {
  kind: 'server' | 'user'
  serverId?: number
  serverName?: string
  userUuid?: string
  userName?: string
}

export type TrafficStatsRangeKey = '24h' | '7d' | '30d' | 'custom'

// 'YYYY-MM-DD'，仅 rangeKey='custom' 时参与查询参数计算。
export interface TrafficStatsCustomRange {
  from: string
  to: string
}

// 区间→时序粒度（弹窗图表标签格式化复用同一映射，避免两处漂移）。
export const TRAFFIC_RANGE_GRANULARITY: Record<TrafficStatsRangeKey, TrafficStatGranularity> = {
  '24h': '15m',
  '7d': '1h',
  '30d': 'day',
  custom: 'day',
}

const RANGE_SPAN_MS: Record<Exclude<TrafficStatsRangeKey, 'custom'>, number> = {
  '24h': 24 * HOUR,
  '7d': 7 * DAY,
  '30d': 30 * DAY,
}

const DATE_INPUT_RE = /^\d{4}-\d{2}-\d{2}$/

function dayStart(value: string): number | null {
  if (!DATE_INPUT_RE.test(value)) return null
  const ts = new Date(`${value}T00:00:00`).getTime()
  return Number.isNaN(ts) ? null : ts
}

function dayEnd(value: string): number | null {
  if (!DATE_INPUT_RE.test(value)) return null
  const ts = new Date(`${value}T23:59:59.999`).getTime()
  return Number.isNaN(ts) ? null : ts
}

/**
 * trafficStatsRangeParams 把区间选择翻译为查询参数：
 * '24h'→15m/近24h、'7d'→1h/近7天、'30d'→day/近30天（to=now）；
 * 'custom'→day 粒度，from 当日零点、to 当日末；非法/倒置区间返回 null（调用方 onError 不发请求）。
 */
export function trafficStatsRangeParams(
  range: TrafficStatsRangeKey,
  custom: TrafficStatsCustomRange,
  now = Date.now(),
): { granularity: TrafficStatGranularity; from: number; to: number } | null {
  if (range !== 'custom') {
    return {
      granularity: TRAFFIC_RANGE_GRANULARITY[range],
      from: now - RANGE_SPAN_MS[range],
      to: now,
    }
  }
  const from = dayStart(custom.from)
  const to = dayEnd(custom.to)
  if (from === null || to === null || from > to) return null
  return { granularity: 'day', from, to }
}

// customRange 初始默认最近 30 天（切换到"自定义"即可用，无需先补日期）。
function defaultCustomRange(now = Date.now()): TrafficStatsCustomRange {
  return {
    from: toLocalDateInput(new Date(now - 29 * DAY).toISOString()),
    to: toLocalDateInput(new Date(now).toISOString()),
  }
}

/**
 * useTrafficStatsDialog 为流量统计下钻弹窗控制器：
 * scope 为空时静默；scope/区间变化即重拉 series+breakdown（同一窗口参数）；
 * server 视图的 user 行与 user 视图的 server 行经 drillRow 互换视角（区间保留），
 * 其余行忽略。onError 以 ref 中转（允许不稳定引用），请求以序号+active 双保险丢弃过期回包。
 */
export function useTrafficStatsDialog({ onError }: { onError: (msg: string) => void }) {
  const [scope, setScope] = useState<TrafficStatsScope | null>(null)
  const [rangeKey, setRangeKey] = useState<TrafficStatsRangeKey>('7d')
  const [customRange, setCustomRange] = useState<TrafficStatsCustomRange>(() =>
    defaultCustomRange(),
  )
  const [points, setPoints] = useState<TrafficStatPoint[]>([])
  const [rows, setRows] = useState<TrafficStatBreakdownRow[]>([])
  const [loading, setLoading] = useState(false)

  const onErrorRef = useRef(onError)
  useEffect(() => {
    onErrorRef.current = onError
  })
  const requestRef = useRef(0)

  useEffect(() => {
    if (scope === null) return
    const params = trafficStatsRangeParams(rangeKey, customRange)
    if (params === null) {
      onErrorRef.current('流量统计时间范围无效')
      return
    }
    const current = ++requestRef.current
    let active = true
    const isServer = scope.kind === 'server'
    setLoading(true)
    Promise.all([
      api.statsTrafficSeries({
        dim: isServer ? 'server' : 'user',
        ...(isServer ? { server_id: scope.serverId } : { user_uuid: scope.userUuid }),
        granularity: params.granularity,
        from: params.from,
        to: params.to,
      }),
      api.statsTrafficBreakdown({
        ...(isServer ? { server_id: scope.serverId } : { user_uuid: scope.userUuid }),
        from: params.from,
        to: params.to,
      }),
    ])
      .then(([nextPoints, nextRows]) => {
        if (!active || current !== requestRef.current) return
        setPoints(nextPoints)
        setRows(nextRows)
      })
      .catch((err: unknown) => {
        if (!active || current !== requestRef.current) return
        onErrorRef.current(errorMessage(err))
      })
      .finally(() => {
        if (active && current === requestRef.current) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [scope, rangeKey, customRange])

  // 每次全新打开回到默认区间；drillRow 互换视角时刻意保留当前区间。
  const open = (next: TrafficStatsScope) => {
    setRangeKey('7d')
    setScope(next)
  }

  const close = () => {
    setScope(null)
    setPoints([])
    setRows([])
    setLoading(false)
  }

  const setRange = (range: TrafficStatsRangeKey) => setRangeKey(range)

  const drillRow = (row: TrafficStatBreakdownRow) => {
    if (scope === null) return
    if (scope.kind === 'server' && row.dim_type === 'user') {
      setScope({ kind: 'user', userUuid: row.dim_id, userName: row.label || undefined })
    } else if (scope.kind === 'user' && row.dim_type === 'server') {
      const serverId = Number(row.dim_id)
      if (Number.isNaN(serverId)) return
      setScope({ kind: 'server', serverId, serverName: row.label || undefined })
    }
  }

  return {
    scope,
    rangeKey,
    customRange,
    setCustomRange,
    points,
    rows,
    loading,
    open,
    close,
    setRange,
    drillRow,
  }
}

export type TrafficStatsDialogController = ReturnType<typeof useTrafficStatsDialog>
