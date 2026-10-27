import { describe, expect, it } from 'vitest'

import type { ChainStatsResponse } from '@/lib/types'

import {
  buildChainStatsIndex,
  chainTodayBytes,
  formatLatency,
  latencyTone,
  segmentForLink,
} from './chain-stats'

const response: ChainStatsResponse = {
  chains: [
    {
      chain_id: 1,
      latency_ms: 64.5,
      segments: [
        {
          hop_id: 10,
          kind: 'forward',
          target_address: 'a',
          target_port: 1,
          latency_ms: 12.5,
          sampled_at: 't1',
        },
        {
          hop_id: 12,
          kind: 'bridge',
          target_address: 'b',
          target_port: 2,
          latency_ms: 52,
          sampled_at: 't2',
        },
      ],
      today: { raw_up: 100, raw_down: 200, effective_up: 150, effective_down: 300 },
    },
    {
      chain_id: 2,
      latency_ms: null,
      segments: [
        // 失败样本（latency 缺省）保留为连通性参考
        {
          hop_id: 20,
          kind: 'forward',
          target_address: 'c',
          target_port: 3,
          latency_ms: null,
          sampled_at: 't3',
        },
      ],
      today: { raw_up: 0, raw_down: 0, effective_up: 0, effective_down: 0 },
    },
  ],
  today_up: 150,
  today_down: 300,
}

describe('latencyTone', () => {
  it('按 <100 / 100-300 / >300 分级', () => {
    expect(latencyTone(1.5)).toBe('ok')
    expect(latencyTone(99.9)).toBe('ok')
    expect(latencyTone(100)).toBe('warn')
    expect(latencyTone(299)).toBe('warn')
    expect(latencyTone(300)).toBe('fail')
  })
})

describe('formatLatency', () => {
  it('小值保留一位小数，大值取整', () => {
    expect(formatLatency(1.5)).toBe('1.5 ms')
    expect(formatLatency(9.94)).toBe('9.9 ms')
    expect(formatLatency(12.25)).toBe('12 ms')
    expect(formatLatency(64.5)).toBe('65 ms')
    expect(formatLatency(300)).toBe('300 ms')
  })
})

describe('buildChainStatsIndex', () => {
  it('按链与按探测跳双向索引', () => {
    const index = buildChainStatsIndex(response)
    expect(index.byChain.get(1)?.segments).toHaveLength(2)
    expect(index.byChain.get(2)?.latency_ms).toBeNull()
    expect(index.byHop.get(10)?.[0].kind).toBe('forward')
    expect(index.byHop.get(12)?.[0].kind).toBe('bridge')
    expect(index.byHop.get(99)).toBeUndefined()
  })

  it('空响应得到空索引', () => {
    const index = buildChainStatsIndex(null)
    expect(index.byChain.size).toBe(0)
    expect(index.byHop.size).toBe(0)
  })
})

describe('segmentForLink', () => {
  const index = buildChainStatsIndex(response)

  it('直连段取上游 forward 探测', () => {
    expect(segmentForLink(index, 10, 11)?.latency_ms).toBe(12.5)
  })

  it('反向段取下游 bridge 探测', () => {
    expect(segmentForLink(index, 11, 12)?.latency_ms).toBe(52)
  })

  it('失败样本兜底返回（连通性参考）', () => {
    expect(segmentForLink(index, 20, 21)?.latency_ms).toBeNull()
  })

  it('无新鲜样本返回 null', () => {
    expect(segmentForLink(index, 30, 31)).toBeNull()
  })
})

describe('chainTodayBytes', () => {
  it('取折算口径上下行之和，无数据为 0', () => {
    expect(chainTodayBytes(response.chains[0])).toBe(450)
    expect(chainTodayBytes(buildChainStatsIndex(null).byChain.get(1))).toBe(0)
  })
})
