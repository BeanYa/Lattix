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
          kind: 'tcp',
          target_address: 'a',
          target_port: 30000,
          latency_ms: 12.5,
          sampled_at: 't1',
        },
        {
          hop_id: 10,
          kind: 'icmp',
          target_address: 'a',
          target_port: 0,
          latency_ms: 13.8,
          sampled_at: 't1b',
        },
        {
          hop_id: 12,
          kind: 'icmp',
          target_address: 'b',
          target_port: 0,
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
          kind: 'tcp',
          target_address: 'c',
          target_port: 3,
          latency_ms: null,
          sampled_at: 't3',
        },
        {
          hop_id: 20,
          kind: 'icmp',
          target_address: 'c',
          target_port: 0,
          latency_ms: 20,
          sampled_at: 't3b',
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
  it('按链与按段上游跳索引', () => {
    const index = buildChainStatsIndex(response)
    expect(index.byChain.get(1)?.segments).toHaveLength(3)
    expect(index.byChain.get(2)?.latency_ms).toBeNull()
    expect(index.byHop.get(10)).toHaveLength(2)
    expect(index.byHop.get(12)?.[0].kind).toBe('icmp')
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

  it('tcp 样本优先（与业务路径同端口）', () => {
    const segment = segmentForLink(index, 10, 11)
    expect(segment?.kind).toBe('tcp')
    expect(segment?.latency_ms).toBe(12.5)
  })

  it('无 tcp 样本回退 icmp', () => {
    const segment = segmentForLink(index, 12, 13)
    expect(segment?.kind).toBe('icmp')
    expect(segment?.latency_ms).toBe(52)
  })

  it('tcp 失败样本优先于 icmp 成功样本（连通性参考，显示超时）', () => {
    const segment = segmentForLink(index, 20, 21)
    expect(segment?.kind).toBe('tcp')
    expect(segment?.latency_ms).toBeNull()
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
