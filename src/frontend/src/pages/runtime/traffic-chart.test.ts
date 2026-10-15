import { describe, expect, it } from 'vitest'

import type { ChartThemeColors } from '@/pages/costs-shared'

import { buildTrafficBarOption } from './traffic-chart'

const colors: ChartThemeColors = {
  palette: ['#3568D7', '#3F8F6B', '#C9A85C', '#B45B4A', '#7E6BC4'],
  textColor: '#7F7A69',
  axisColor: '#A09A89',
  gridColor: 'rgba(60, 55, 42, .1)',
  surfaceColor: '#FFFDF7',
}

// 标签按浏览器本地时区格式化：以本地时间构造时间戳，跨时区机器断言一致。
const ts = (month: number, day: number, hour: number, minute: number) =>
  new Date(2026, month - 1, day, hour, minute).getTime()

const points = [
  { ts: ts(10, 13, 14, 5), up: 1024, down: 2048 },
  { ts: ts(10, 14, 14, 10), up: 1536, down: 4096 },
  { ts: ts(10, 15, 14, 15), up: 512, down: 256 },
]

interface BarSeries {
  name: string
  type: string
  stack: string
  data: number[]
}

function seriesOf(option: ReturnType<typeof buildTrafficBarOption>): BarSeries[] {
  return option.series as BarSeries[]
}

describe('buildTrafficBarOption', () => {
  it('双系列堆叠柱：上传/下载、stack 一致、数据点与输入一一对应', () => {
    const option = buildTrafficBarOption(points, '15m', colors)
    const series = seriesOf(option)
    expect(series).toHaveLength(2)
    expect(series.map((s) => s.name)).toEqual(['上传', '下载'])
    expect(series.every((s) => s.type === 'bar')).toBe(true)
    expect(new Set(series.map((s) => s.stack)).size).toBe(1)
    expect(series[0].data).toEqual([1024, 1536, 512])
    expect(series[1].data).toEqual([2048, 4096, 256])
  })

  it('x 轴标签按粒度格式化（Intl zh-CN）', () => {
    const labels = (g: Parameters<typeof buildTrafficBarOption>[1]) =>
      (buildTrafficBarOption(points, g, colors).xAxis as { data: string[] }).data
    expect(labels('5m')).toEqual(['14:05', '14:10', '14:15'])
    expect(labels('15m')).toEqual(['14:05', '14:10', '14:15'])
    expect(labels('1h')).toEqual(['10/13 14:05', '10/14 14:10', '10/15 14:15'])
    expect(labels('day')).toEqual(['10/13', '10/14', '10/15'])
    expect(labels('month')).toEqual(['2026/10', '2026/10', '2026/10'])
  })

  it('dataZoom 仅在点数 > 60 时开启（inside + slider）', () => {
    expect(buildTrafficBarOption(points, '15m', colors).dataZoom).toEqual([])
    const many = Array.from({ length: 61 }, (_, i) => ({ ts: ts(10, 15, 0, i), up: i, down: i }))
    const zoomed = buildTrafficBarOption(many, '5m', colors).dataZoom as Array<{
      type: string
      borderColor?: string
    }>
    expect(zoomed.map((z) => z.type)).toEqual(['inside', 'slider'])
    expect(zoomed[1].borderColor).toBe(colors.axisColor)
  })

  it('axis/legend/tooltip 颜色与取色取自 ChartThemeColors', () => {
    const option = buildTrafficBarOption(points, '1h', colors)
    const xAxis = option.xAxis as {
      axisLabel: { color: string }
      axisLine: { lineStyle: { color: string } }
    }
    const yAxis = option.yAxis as { splitLine: { lineStyle: { color: string } } }
    const legend = option.legend as { textStyle: { color: string }; data: string[] }
    expect(xAxis.axisLabel.color).toBe(colors.textColor)
    expect(xAxis.axisLine.lineStyle.color).toBe(colors.axisColor)
    expect(yAxis.splitLine.lineStyle.color).toBe(colors.gridColor)
    expect(legend.textStyle.color).toBe(colors.textColor)
    expect(legend.data).toEqual(['上传', '下载'])
    expect(option.color).toEqual([colors.palette[0], colors.palette[1]])
  })

  it('数值以字节单位（B/KB/MB/GB）格式化', () => {
    const option = buildTrafficBarOption(points, '1h', colors)
    const valueFormatter = (option.tooltip as { valueFormatter: (v: unknown) => string })
      .valueFormatter
    expect(valueFormatter(0)).toBe('0 B')
    expect(valueFormatter(1536)).toBe('1.5 KB')
    expect(valueFormatter(2 * 1024 * 1024)).toBe('2.0 MB')
    expect(valueFormatter(3 * 1024 ** 3)).toBe('3.0 GB')
    const axisFormatter = (option.yAxis as { axisLabel: { formatter: (v: number) => string } })
      .axisLabel.formatter
    expect(axisFormatter(1024)).toBe('1.0 KB')
    expect(axisFormatter(1024 ** 3)).toBe('1.0 GB')
  })

  it('同一 (points, granularity, colors) 构建结果确定（含函数引用）', () => {
    expect(buildTrafficBarOption(points, 'day', colors)).toEqual(
      buildTrafficBarOption(points, 'day', colors),
    )
  })
})
