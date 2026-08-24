import { useCallback, useMemo, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import type { Chain, ChainHopRole, ChainTrafficBucket } from '@/lib/types'

export const roleLabel: Record<ChainHopRole, string> = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
}

/** 流量历史对话框的状态与数据加载；加载失败经 onError 上报给页面级 Notice。 */
export function useTrafficHistory({ onError }: { onError: (message: string) => void }) {
  const [trafficChain, setTrafficChain] = useState<Chain | null>(null)
  const [trafficHopId, setTrafficHopId] = useState(0)
  const [trafficRange, setTrafficRange] = useState<'day' | 'month'>('day')
  const [trafficHistory, setTrafficHistory] = useState<ChainTrafficBucket[]>([])
  const [trafficLoading, setTrafficLoading] = useState(false)

  const loadTrafficHistory = useCallback(
    async (chain: Chain, hopId: number, range: 'day' | 'month') => {
      setTrafficLoading(true)
      try {
        setTrafficHistory(
          (await api.chainTrafficHistory(chain.id, hopId, range === 'day' ? 30 : 365)) ?? [],
        )
      } catch (err) {
        onError(errorMessage(err))
      } finally {
        setTrafficLoading(false)
      }
    },
    [onError],
  )

  const openTraffic = (chain: Chain) => {
    setTrafficChain(chain)
    setTrafficHopId(0)
    setTrafficRange('day')
    void loadTrafficHistory(chain, 0, 'day')
  }

  const close = () => setTrafficChain(null)

  const selectRange = (range: 'day' | 'month') => {
    if (!trafficChain) return
    setTrafficRange(range)
    void loadTrafficHistory(trafficChain, trafficHopId, range)
  }

  const selectHop = (hopId: number) => {
    if (!trafficChain) return
    setTrafficHopId(hopId)
    void loadTrafficHistory(trafficChain, hopId, trafficRange)
  }

  const displayedTrafficHistory = useMemo(() => {
    const normalized = trafficHistory.map((bucket) => ({
      ...bucket,
      date: bucket.date ?? bucket.Date ?? '',
    }))
    if (trafficRange === 'day') return normalized
    const months = new Map<string, ChainTrafficBucket & { date: string }>()
    for (const bucket of normalized) {
      const month = bucket.date.slice(0, 7)
      const current = months.get(month) ?? {
        date: month,
        raw_up: 0,
        raw_down: 0,
        effective_up: 0,
        effective_down: 0,
      }
      current.raw_up += bucket.raw_up
      current.raw_down += bucket.raw_down
      current.effective_up += bucket.effective_up
      current.effective_down += bucket.effective_down
      months.set(month, current)
    }
    return [...months.values()]
  }, [trafficHistory, trafficRange])

  return {
    trafficChain,
    trafficHopId,
    trafficRange,
    trafficLoading,
    displayedTrafficHistory,
    openTraffic,
    close,
    selectRange,
    selectHop,
  }
}

export type TrafficHistoryController = ReturnType<typeof useTrafficHistory>
