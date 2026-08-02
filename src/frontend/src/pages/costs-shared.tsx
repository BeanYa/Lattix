/* oxlint-disable react/only-export-components -- shared module: components + helpers must coexist */

import { useEffect, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { type ChartOption } from '@/components/echarts'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import type { BillingStatsGranularity, BillingStatsRateMode } from '@/lib/types'
import { cn } from '@/lib/utils'

export const SERVER_PALETTE = [
  '#5f5cdb', '#48aa88', '#e6bc45', '#e17872', '#6994d1', '#66cbb7',
  '#f2a291', '#b9b4ff', '#42b88c', '#e97872', '#3e72c7', '#278f69',
]

export const GRANULARITY_LABEL: Record<BillingStatsGranularity, string> = {
  day: '日',
  month: '月',
  year: '年',
}

export const billingStatusVariant: Record<string, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  active: 'default',
  due_today: 'secondary',
  assumed_valid: 'outline',
  expired: 'destructive',
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

export function clampRange(granularity: BillingStatsGranularity, from: string, to: string): [string, string] {
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
    void api.servers({ display: 'silent' }).then((servers) => {
      if (!active) return
      const starts = servers
        .filter((server) => server.billing?.enabled && server.billing.service_started_on)
        .map((server) => server.billing.service_started_on)
      setEarliestStart(starts.length > 0 ? starts.sort()[0] : '')
    }).catch(() => {})
    return () => { active = false }
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
  textColor: string
  axisColor: string
}): ChartOption {
  return {
    color: SERVER_PALETTE,
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      valueFormatter: (value: unknown) => `${money(Number(value), options.currency)} ${options.currency}`,
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      textStyle: { color: options.textColor },
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
      axisLabel: { color: options.textColor },
      axisLine: { lineStyle: { color: options.axisColor } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: options.textColor },
      splitLine: { lineStyle: { color: options.axisColor } },
    },
    dataZoom: options.granularity === 'day'
      ? [{ type: 'inside' }, { type: 'slider', bottom: 24, height: 18, borderColor: options.axisColor }]
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
  textColor: string,
  theme: string,
): ChartOption {
  return {
    color: SERVER_PALETTE,
    tooltip: {
      trigger: 'item',
      valueFormatter: (value: unknown) => `${money(Number(value), currency)} ${currency}`,
    },
    legend: {
      type: 'scroll',
      orient: 'vertical',
      right: 8,
      top: 'middle',
      textStyle: { color: textColor },
    },
    series: [{
      name: '成本占比',
      type: 'pie',
      radius: ['42%', '68%'],
      center: ['38%', '50%'],
      avoidLabelOverlap: true,
      itemStyle: {
        borderColor: theme === 'dark' ? '#1c1e2e' : '#ffffff',
        borderWidth: 2,
      },
      label: { color: textColor, formatter: '{b}\n{d}%' },
      data,
    }],
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
  granularity, from, to, rateMode, customAvailable, rateDate, presetsDisabled,
  onGranularity, onFrom, onTo, onPreset, onRateMode,
}: StatsControlsProps) {
  return (
    <Card>
      <CardContent className="flex flex-wrap items-end gap-x-4 gap-y-3">
        <div className="space-y-2">
          <span className="text-xs font-medium text-muted-foreground">统计粒度</span>
          <Tabs value={granularity} onValueChange={onGranularity}>
            <TabsList>
              {(Object.keys(GRANULARITY_LABEL) as BillingStatsGranularity[]).map((gran) => (
                <TabsTrigger key={gran} value={gran}>{GRANULARITY_LABEL[gran]}</TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </div>
        <div className="grid grid-cols-2 items-end gap-2">
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">起始日期</span>
            <Input type="date" value={from} max={to} onChange={(event) => onFrom(event.target.value)} className="w-40" />
          </div>
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">结束日期</span>
            <Input type="date" value={to} min={from} onChange={(event) => onTo(event.target.value)} className="w-40" />
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('month')}>本月</Button>
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('12months')}>近 12 个月</Button>
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('3years')}>近 3 年</Button>
          <Button type="button" variant="outline" size="sm" disabled={presetsDisabled} onClick={() => onPreset('all')}>全部</Button>
        </div>
        {customAvailable ? (
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">换算方式</span>
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
          <span className="inline-flex items-center gap-2 rounded-full border bg-card px-3 py-1.5 text-sm font-medium">
            <CoinsIcon className="size-4" />
            汇率日期 {rateDate}
          </span>
        ) : null}
      </CardContent>
    </Card>
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
    setSort((current) => current.key === key
      ? { key, dir: current.dir === 1 ? -1 : 1 }
      : { key, dir: key === 'name' ? 1 : -1 })
  }
  const header = (key: CostSortKey, label: string, className?: string) => (
    <button
      type="button"
      onClick={() => toggle(key)}
      className={cn('inline-flex items-center gap-1 hover:text-foreground', className)}
    >
      {label}
      <span className={cn('text-[10px] opacity-60', sort.key === key ? 'opacity-100' : 'invisible')}>
        {sort.dir === 1 ? '↑' : '↓'}
      </span>
    </button>
  )
  return { sort, toggle, header }
}
