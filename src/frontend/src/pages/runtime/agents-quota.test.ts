import { describe, expect, it } from 'vitest'

import type { TrafficQuotaSummary } from '@/lib/types'

import { humanizeSignedBytes, quotaCardViews } from './agents-quota'

const KIB = 1024
const GIB = 1024 ** 3
const TIB = 1024 ** 4

function quota(overrides: Partial<TrafficQuotaSummary> = {}): TrafficQuotaSummary {
  return {
    total_available_bytes: 0,
    current_available_bytes: 0,
    used_bytes: 0,
    limited_servers: 0,
    ...overrides,
  }
}

describe('humanizeSignedBytes', () => {
  it('正数与零沿用 humanizeBytes', () => {
    expect(humanizeSignedBytes(500 * GIB)).toBe('500 GB')
    expect(humanizeSignedBytes(0)).toBe('0 B')
  })

  it('负数保留负号（超支场景）', () => {
    expect(humanizeSignedBytes(-20 * GIB)).toBe('-20.0 GB')
    expect(humanizeSignedBytes(-1536)).toBe('-1.5 KB')
    expect(humanizeSignedBytes(-KIB)).toBe('-1.0 KB')
  })
})

describe('quotaCardViews', () => {
  it('常规值：总可用/当前可用/已用三卡的值与文案', () => {
    const views = quotaCardViews(
      quota({
        total_available_bytes: 3 * TIB,
        current_available_bytes: 3 * TIB - 500 * GIB,
        used_bytes: 500 * GIB,
        limited_servers: 2,
      }),
      5,
    )

    expect(views.total).toEqual({ value: '3.0 TB', detail: '2 / 5 台已设上限' })
    expect(views.current).toEqual({ value: '2.5 TB', detail: '已用 16%' })
    expect(views.used).toEqual({ value: '500 GB', detail: '全部 5 台 · 当前计费周期' })
    // 低于 75% 时不应有告警色调（tone 缺省不渲染 data-tone）。
    expect(views.total.tone).toBeUndefined()
    expect(views.current.tone).toBeUndefined()
    expect(views.used.tone).toBeUndefined()
  })

  it('无限额服务器：total=0 时当前可用显示「—」，且不按已用占比告警', () => {
    const views = quotaCardViews(quota({ used_bytes: 500 * GIB }), 4)

    expect(views.total).toEqual({ value: '0 B', detail: '暂无服务器设置流量上限' })
    expect(views.current).toEqual({ value: '—', detail: '无限流量服务器不计入上限' })
    expect(views.current.tone).toBeUndefined()
    expect(views.used).toEqual({ value: '500 GB', detail: '全部 4 台 · 当前计费周期' })
  })

  it('没有被控服务器时已用卡提示暂无服务器', () => {
    const views = quotaCardViews(quota(), 0)

    expect(views.used.detail).toBe('暂无被控服务器')
  })

  it('超支：当前可用为负值，带负号并标记 danger', () => {
    const views = quotaCardViews(
      quota({
        total_available_bytes: 100 * GIB,
        current_available_bytes: -20 * GIB,
        used_bytes: 120 * GIB,
        limited_servers: 1,
      }),
      3,
    )

    expect(views.total).toEqual({ value: '100 GB', detail: '1 / 3 台已设上限' })
    expect(views.current.value).toBe('-20.0 GB')
    expect(views.current.detail).toBe('已用超出上限 20.0 GB')
    expect(views.current.tone).toBe('danger')
  })

  it('告警阈值：<75% 无色调、75–89% warn、≥90% danger', () => {
    const build = (usedBytes: number) =>
      quotaCardViews(
        quota({
          total_available_bytes: 1000 * GIB,
          current_available_bytes: 1000 * GIB - usedBytes,
          used_bytes: usedBytes,
          limited_servers: 1,
        }),
        2,
      ).current

    expect(build(740 * GIB)).toEqual({ value: '260 GB', detail: '已用 74%', tone: undefined })
    expect(build(750 * GIB)).toEqual({ value: '250 GB', detail: '已用 75%', tone: 'warn' })
    expect(build(890 * GIB)).toEqual({ value: '110 GB', detail: '已用 89%', tone: 'warn' })
    expect(build(900 * GIB)).toEqual({ value: '100 GB', detail: '已用 90%', tone: 'danger' })
  })
})
