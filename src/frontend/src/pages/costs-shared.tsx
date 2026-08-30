/* oxlint-disable react/only-export-components -- shared module: components + helpers must coexist */

import { useEffect, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { type ChartOption } from '@/components/echarts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import type { BillingStatsGranularity, BillingStatsRateMode } from '@/lib/types'
import { cn } from '@/lib/utils'

// ECharts 走 canvas 渲染，无法直接消费 var() 字符串，
// 因此在运行时从 CSS 变量解析颜色，深浅色随 .dark 令牌自动切换。
// fallback 仅在变量缺失时兜底（与 :root 令牌一致）。
export interface ChartThemeColors {
  palette: string[]
  textColor: string
  axisColor: string
  gridColor: string
  surfaceColor: string
}

export function chartThemeColors(): ChartThemeColors {
  const styles = getComputedStyle(document.documentElement)
  const read = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback
  return {
    palette: ['--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5'].map((name) =>
      read(name, '#3568D7'),
    ),
    textColor: read('--cg-muted', '#7F7A69'),
    axisColor: read('--cg-subtle', '#A09A89'),
    gridColor: read('--cg-grid-line', 'rgba(60, 55, 42, .1)'),
    surfaceColor: read('--cg-paper-light', '#FFFDF7'),
  }
}

export const GRANULARITY_LABEL: Record<BillingStatsGranularity, string> = {
  day: '日',
  month: '月',
  year: '年',
}

export const billingStatusTone: Record<string, 'is-lime' | 'is-blue' | 'is-muted' | 'is-red'> = {
  active: 'is-lime',
  due_today: 'is-blue',
  assumed_valid: 'is-muted',
  expired: 'is-red',
}

export const billingStatusLabel: Record<string, string> = {
  active: '有效',
  due_today: '今日到期',
  assumed_valid: '推定有效',
  expired: '已过期',
}

export function localDate(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

export function addMonths(date: string, months: number): string {
  const value = new Date(`${date}T00:00:00`)
  value.setMonth(value.getMonth() + months)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

export function addDays(date: string, days: number): string {
  const value = new Date(`${date}T00:00:00`)
  value.setDate(value.getDate() + days)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

export function firstOfMonth(date: string): string {
  return `${date.slice(0, 8)}01`
}

export function daySpan(from: string, to: string): number {
  return Math.round((Date.parse(`${to}T00:00:00`) - Date.parse(`${from}T00:00:00`)) / 86400000) + 1
}

export function clampRange(
  granularity: BillingStatsGranularity,
  from: string,
  to: string,
): [string, string] {
  const limit = granularity === 'day' ? 372 : 3660
  if (daySpan(from, to) > limit) return [addDays(to, -(limit - 1)), to]
  return [from, to]
}

export function money(minor: number, currency: string): string {
  const divisor = ['JPY', 'KRW', 'ISK'].includes(currency) ? 1 : 100
  return (minor / divisor).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

export function periodLabel(period: string, granularity: BillingStatsGranularity): string {
  if (granularity === 'day') return period
  if (granularity === 'year') return period
  return period.replace('-', ' 年 ').concat(' 月')
}

export function useEarliestStart(): string {
  const [earliestStart, setEarliestStart] = useState('')
  useEffect(() => {
    let active = true
    void api
      .servers({ display: 'silent' })
      .then((servers) => {
        if (!active) return
        const starts = servers
          .filter((server) => server.billing?.enabled && server.billing.service_started_on)
          .map((server) => server.billing.service_started_on)
        setEarliestStart(starts.length > 0 ? starts.sort()[0] : '')
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])
  return earliestStart
}

export interface CostsSeriesServer {
  alias: string
  costs: number[]
}

export function buildBarOption(options: {
  periods: string[]
  servers: CostsSeriesServer[]
  granularity: BillingStatsGranularity
  currency: string
  colors: ChartThemeColors
}): ChartOption {
  const { colors } = options
  return {
    color: colors.palette,
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      valueFormatter: (value: unknown) =>
        `${money(Number(value), options.currency)} ${options.currency}`,
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      textStyle: { color: colors.textColor },
      data: options.servers.map((server) => server.alias),
    },
    grid: {
      left: 8,
      right: 16,
      top: 24,
      bottom: options.granularity === 'day' ? 56 : 28,
      containLabel: true,
    },
    xAxis: {
      type: 'category',
      data: options.periods,
      axisLabel: { color: colors.textColor },
      axisLine: { lineStyle: { color: colors.axisColor } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: colors.textColor },
      splitLine: { lineStyle: { color: colors.gridColor } },
    },
    dataZoom:
      options.granularity === 'day'
        ? [
            { type: 'inside' },
            { type: 'slider', bottom: 24, height: 18, borderColor: colors.axisColor },
          ]
        : [],
    series: options.servers.map((server) => ({
      name: server.alias,
      type: 'bar',
      stack: 'cost',
      emphasis: { focus: 'series' },
      barMaxWidth: 36,
      data: server.costs,
    })),
  }
}

export function buildDonutOption(
  data: Array<{ name: string; value: number }>,
  currency: string,
  colors: ChartThemeColors,
): ChartOption {
  return {
    color: colors.palette,
    tooltip: {
      trigger: 'item',
      valueFormatter: (value: unknown) => `${money(Number(value), currency)} ${currency}`,
    },
    legend: {
      type: 'scroll',
      orient: 'vertical',
      right: 8,
      top: 'middle',
      textStyle: { color: colors.textColor },
    },
    series: [
      {
        name: '成本占比',
        type: 'pie',
        radius: ['42%', '68%'],
        center: ['38%', '50%'],
        avoidLabelOverlap: true,
        itemStyle: {
          borderColor: colors.surfaceColor,
          borderWidth: 2,
        },
        label: { color: colors.textColor, formatter: '{b}\n{d}%' },
        data,
      },
    ],
  }
}

export interface StatsControlsProps {
  granularity: BillingStatsGranularity
  from: string
  to: string
  rateMode: BillingStatsRateMode
  customAvailable: boolean
  rateDate?: string
  presetsDisabled: boolean
  onGranularity: (value: string) => void
  onFrom: (value: string) => void
  onTo: (value: string) => void
  onPreset: (preset: 'month' | '12months' | '3years' | 'all') => void
  onRateMode: (value: BillingStatsRateMode) => void
}

export function StatsControls({
  granularity,
  from,
  to,
  rateMode,
  customAvailable,
  rateDate,
  presetsDisabled,
  onGranularity,
  onFrom,
  onTo,
  onPreset,
  onRateMode,
}: StatsControlsProps) {
  return (
    <section className="cg-card cg-costs-controls" aria-label="统计条件">
      <div className="cg-costs-control">
        <span className="cg-costs-control-label">统计粒度</span>
        <Tabs value={granularity} onValueChange={onGranularity}>
          <TabsList>
            {(Object.keys(GRANULARITY_LABEL) as BillingStatsGranularity[]).map((gran) => (
              <TabsTrigger key={gran} value={gran}>
                {GRANULARITY_LABEL[gran]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <div className="cg-costs-control">
        <span className="cg-costs-control-label">起始日期</span>
        <Input
          type="date"
          value={from}
          max={to}
          onChange={(event) => onFrom(event.target.value)}
          className="w-40"
        />
      </div>
      <div className="cg-costs-control">
        <span className="cg-costs-control-label">结束日期</span>
        <Input
          type="date"
          value={to}
          min={from}
          onChange={(event) => onTo(event.target.value)}
          className="w-40"
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => onPreset('month')}>
          本月
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={() => onPreset('12months')}>
          近 12 个月
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={() => onPreset('3years')}>
          近 3 年
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={presetsDisabled}
          onClick={() => onPreset('all')}
        >
          全部
        </Button>
      </div>
      {customAvailable ? (
        <div className="cg-costs-control">
          <span className="cg-costs-control-label">换算方式</span>
          <Select
            value={rateMode}
            onValueChange={(value) => value && onRateMode(value as BillingStatsRateMode)}
          >
            <SelectTrigger className="w-40" size="sm">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="custom">自定义锚点</SelectItem>
              <SelectItem value="public">公共汇率</SelectItem>
            </SelectContent>
          </Select>
        </div>
      ) : null}
      {rateDate ? (
        <span className="cg-pill">
          <CoinsIcon size={14} />
          汇率日期 {rateDate}
        </span>
      ) : null}
    </section>
  )
}

export type CostSortKey = 'name' | 'total' | 'daily' | 'share'

export interface CostSortState {
  key: CostSortKey
  dir: 1 | -1
}

export function useRowSort() {
  const [sort, setSort] = useState<CostSortState>({ key: 'total', dir: -1 })
  const toggle = (key: CostSortKey) => {
    setSort((current) =>
      current.key === key
        ? { key, dir: current.dir === 1 ? -1 : 1 }
        : { key, dir: key === 'name' ? 1 : -1 },
    )
  }
  const header = (key: CostSortKey, label: string, className?: string) => (
    <button
      type="button"
      onClick={() => toggle(key)}
      className={cn('inline-flex items-center gap-1 hover:text-foreground', className)}
    >
      {label}
      <span
        className={cn('text-[10px] opacity-60', sort.key === key ? 'opacity-100' : 'invisible')}
      >
        {sort.dir === 1 ? '↑' : '↓'}
      </span>
    </button>
  )
  return { sort, toggle, header }
}
