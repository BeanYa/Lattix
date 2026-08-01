import { useCallback, useEffect, useMemo, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Chart, type ChartOption } from '@/components/echarts'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api, errorMessage } from '@/lib/api'
import { useTheme } from '@/lib/theme-context'
import type {
  BillingServerStats,
  BillingStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
import { cn } from '@/lib/utils'

const SERVER_PALETTE = [
  '#5f5cdb', '#48aa88', '#e6bc45', '#e17872', '#6994d1', '#66cbb7',
  '#f2a291', '#b9b4ff', '#42b88c', '#e97872', '#3e72c7', '#278f69',
]

const GRANULARITY_LABEL: Record<BillingStatsGranularity, string> = {
  day: '日',
  month: '月',
  year: '年',
}

const billingStatusVariant: Record<string, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  active: 'default',
  due_today: 'secondary',
  assumed_valid: 'outline',
  expired: 'destructive',
}

const billingStatusLabel: Record<string, string> = {
  active: '有效',
  due_today: '今日到期',
  assumed_valid: '推定有效',
  expired: '已过期',
}

function localDate(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

function addMonths(date: string, months: number): string {
  const value = new Date(`${date}T00:00:00`)
  value.setMonth(value.getMonth() + months)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

function addDays(date: string, days: number): string {
  const value = new Date(`${date}T00:00:00`)
  value.setDate(value.getDate() + days)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

function firstOfMonth(date: string): string {
  return `${date.slice(0, 8)}01`
}

function daySpan(from: string, to: string): number {
  return Math.round((Date.parse(`${to}T00:00:00`) - Date.parse(`${from}T00:00:00`)) / 86400000) + 1
}

function clampRange(granularity: BillingStatsGranularity, from: string, to: string): [string, string] {
  const limit = granularity === 'day' ? 372 : 3660
  if (daySpan(from, to) > limit) return [addDays(to, -(limit - 1)), to]
  return [from, to]
}

function money(minor: number, currency: string): string {
  const divisor = ['JPY', 'KRW', 'ISK'].includes(currency) ? 1 : 100
  return (minor / divisor).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

function costsOf(server: BillingServerStats, rateMode: BillingStatsRateMode): number[] {
  return rateMode === 'custom' && server.costs_custom ? server.costs_custom : server.costs_public
}

interface ServerRow {
  name: string
  server: BillingServerStats
  total: number
  share: number
  daily: number
}

export default function Costs() {
  const { theme } = useTheme()
  const [granularity, setGranularity] = useState<BillingStatsGranularity>('month')
  const [from, setFrom] = useState(() => firstOfMonth(addMonths(localDate(), -11)))
  const [to, setTo] = useState(() => localDate())
  const [rateMode, setRateMode] = useState<BillingStatsRateMode>('custom')
  const [stats, setStats] = useState<BillingStats | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [earliestStart, setEarliestStart] = useState('')
  const [sort, setSort] = useState<{ key: 'name' | 'total' | 'daily' | 'share'; dir: 1 | -1 }>({
    key: 'total',
    dir: -1,
  })

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

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await api.billingStats({
        from,
        to,
        granularity,
        rate_mode: rateMode,
      }, signal ? { signal, display: 'silent' } : { display: 'silent' })
      if (signal?.aborted) return
      setStats(result)
      setError('')
    } catch (err) {
      if (signal?.aborted) return
      setError(errorMessage(err))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [from, to, granularity, rateMode])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const changeGranularity = (value: string) => {
    const gran = value as BillingStatsGranularity
    const [clampedFrom, clampedTo] = clampRange(gran, from, to)
    setFrom(clampedFrom)
    setTo(clampedTo)
    setGranularity(gran)
  }

  const applyPreset = (preset: 'month' | '12months' | '3years' | 'all') => {
    const today = localDate()
    switch (preset) {
      case 'month':
        setFrom(firstOfMonth(today))
        setTo(today)
        break
      case '12months':
        setFrom(firstOfMonth(addMonths(today, -11)))
        setTo(today)
        break
      case '3years':
        setFrom(`${String(Number(today.slice(0, 4)) - 2)}-01-01`)
        setTo(today)
        break
      case 'all':
        if (earliestStart) setFrom(earliestStart)
        setTo(today)
        break
    }
  }

  const rows = useMemo<ServerRow[]>(() => {
    if (!stats) return []
    const totalAll = stats.servers.reduce(
      (sum, server) => sum + costsOf(server, rateMode).reduce((acc, value) => acc + value, 0),
      0,
    )
    return stats.servers.map((server) => {
      const costs = costsOf(server, rateMode)
      const total = costs.reduce((sum, value) => sum + value, 0)
      return {
        name: server.alias,
        server,
        total,
        share: totalAll > 0 ? total / totalAll : 0,
        daily: rateMode === 'custom' ? server.daily_custom_minor ?? server.daily_minor : server.daily_minor,
      }
    }).sort((a, b) => {
      const left = a[sort.key]
      const right = b[sort.key]
      if (typeof left === 'string' && typeof right === 'string') {
        return left.localeCompare(right) * sort.dir
      }
      return ((left as number) - (right as number)) * sort.dir
    })
  }, [stats, rateMode, sort])

  const totalAll = useMemo(() => rows.reduce((sum, row) => sum + row.total, 0), [rows])
  const totals = rateMode === 'custom' && stats?.totals_custom ? stats.totals_custom : stats?.totals_public ?? []
  const monthsInRange = useMemo(() => {
    if (!stats) return 1
    if (stats.granularity === 'month') return Math.max(1, stats.periods.length)
    if (stats.granularity === 'year') return Math.max(1, stats.periods.length * 12)
    return Math.max(1, stats.periods.length / 30)
  }, [stats])
  const topServer = useMemo(() => {
    if (!stats || stats.servers.length === 0) return null
    return stats.servers
      .map((server) => ({
        server,
        total: costsOf(server, rateMode).reduce((sum, value) => sum + value, 0),
      }))
      .sort((a, b) => b.total - a.total)[0]
  }, [stats, rateMode])

  const reportingCurrency = stats?.reporting_currency ?? 'CNY'
  const textColor = theme === 'dark' ? '#c9cbe2' : '#686a7c'
  const axisColor = theme === 'dark' ? '#42466f' : '#d4d6e0'

  const barOption = useMemo<ChartOption>(() => {
    if (!stats) return {}
    return {
      color: SERVER_PALETTE,
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        valueFormatter: (value: unknown) => `${money(Number(value), reportingCurrency)} ${reportingCurrency}`,
      },
      legend: {
        type: 'scroll',
        bottom: 0,
        textStyle: { color: textColor },
        data: stats.servers.map((server) => server.alias),
      },
      grid: {
        left: 8,
        right: 16,
        top: 24,
        bottom: granularity === 'day' ? 56 : 28,
        containLabel: true,
      },
      xAxis: {
        type: 'category',
        data: stats.periods,
        axisLabel: { color: textColor },
        axisLine: { lineStyle: { color: axisColor } },
        axisTick: { show: false },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: textColor },
        splitLine: { lineStyle: { color: axisColor } },
      },
      dataZoom: granularity === 'day'
        ? [{ type: 'inside' }, { type: 'slider', bottom: 24, height: 18, borderColor: axisColor }]
        : [],
      series: stats.servers.map((server) => ({
        name: server.alias,
        type: 'bar',
        stack: 'cost',
        emphasis: { focus: 'series' },
        barMaxWidth: 36,
        data: costsOf(server, rateMode),
      })),
    }
  }, [stats, rateMode, granularity, reportingCurrency, textColor, axisColor])

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return {
      color: SERVER_PALETTE,
      tooltip: {
        trigger: 'item',
        valueFormatter: (value: unknown) => `${money(Number(value), reportingCurrency)} ${reportingCurrency}`,
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
        data: rows.map((row) => ({
          name: row.server.alias,
          value: row.total,
        })),
      }],
    }
  }, [stats, rows, totalAll, reportingCurrency, textColor, theme])

  const toggleSort = (key: 'name' | 'total' | 'daily' | 'share') => {
    setSort((current) => current.key === key
      ? { key, dir: current.dir === 1 ? -1 : 1 }
      : { key, dir: key === 'name' ? 1 : -1 })
  }

  const sortHeader = (key: 'name' | 'total' | 'daily' | 'share', label: string, className?: string) => (
    <button
      type="button"
      onClick={() => toggleSort(key)}
      className={cn('inline-flex items-center gap-1 hover:text-foreground', className)}
    >
      {label}
      <span className={cn('text-[10px] opacity-60', sort.key === key ? 'opacity-100' : 'invisible')}>
        {sort.dir === 1 ? '↑' : '↓'}
      </span>
    </button>
  )

  const periodLabel = (period: string) => {
    if (granularity === 'day') return period
    if (granularity === 'year') return period
    return period.replace('-', ' 年 ').concat(' 月')
  }

  if (error) {
    return (
      <Page>
        <PageHeader title="成本统计" description="启用统计计费的服务器按周期摊算的成本" />
        <Notice tone="danger" title="成本统计加载失败">{error}</Notice>
      </Page>
    )
  }

  return (
    <Page>
      <PageHeader
        title="成本统计"
        description={`启用统计计费的服务器按日/月/年周期摊算的成本，统一以 ${reportingCurrency} 展示`}
        actions={stats?.rate_date
          ? (
            <span className="inline-flex items-center gap-2 rounded-full border bg-card px-3 py-1.5 text-sm font-medium">
              <CoinsIcon className="size-4" />
              汇率日期 {stats.rate_date}
            </span>
          )
          : undefined}
      />

      <Card>
        <CardContent className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">统计粒度</span>
            <Tabs value={granularity} onValueChange={changeGranularity}>
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
              <Input type="date" value={from} max={to} onChange={(event) => setFrom(event.target.value)} className="w-40" />
            </div>
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">结束日期</span>
              <Input type="date" value={to} min={from} onChange={(event) => setTo(event.target.value)} className="w-40" />
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => applyPreset('month')}>本月</Button>
            <Button type="button" variant="outline" size="sm" onClick={() => applyPreset('12months')}>近 12 个月</Button>
            <Button type="button" variant="outline" size="sm" onClick={() => applyPreset('3years')}>近 3 年</Button>
            <Button type="button" variant="outline" size="sm" disabled={!earliestStart} onClick={() => applyPreset('all')}>全部</Button>
          </div>
          {stats?.custom_available ? (
            <div className="space-y-2">
              <span className="text-xs font-medium text-muted-foreground">换算方式</span>
              <Select
                value={rateMode}
                onValueChange={(value) => value && setRateMode(value as BillingStatsRateMode)}
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
        </CardContent>
      </Card>

      {loading && !stats ? (
        <LoadingState className="py-16">正在统计成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用统计计费的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示成本统计。"
        />
      ) : stats ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <Card>
              <CardHeader>
                <CardDescription>范围内总成本</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(totalAll, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">{reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>启用计费服务器</CardDescription>
                <CardTitle className="text-2xl tabular-nums">{stats.servers.length} 台</CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>平均月成本</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(Math.round(totalAll / monthsInRange), reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">{reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>成本最高服务器</CardDescription>
                <CardTitle className="truncate text-xl tabular-nums" title={topServer?.server.alias}>
                  {topServer
                    ? `${topServer.server.alias} · ${money(topServer.total, reportingCurrency)}`
                    : '—'}
                </CardTitle>
              </CardHeader>
            </Card>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle>周期成本分布</CardTitle>
                <CardDescription>每台服务器一个色段，悬停查看明细；图例可点击隐藏单台服务器。</CardDescription>
              </CardHeader>
              <CardContent>
                <Chart option={barOption} className="h-80 w-full" />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>成本占比</CardTitle>
                <CardDescription>范围内各服务器成本占比。</CardDescription>
              </CardHeader>
              <CardContent>
                {totalAll > 0
                  ? <Chart option={donutOption} className="h-80 w-full" />
                  : <EmptyState title="范围内暂无成本" description="调整时间范围后查看占比。" className="h-80" />}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader>
              <CardTitle>服务器汇总</CardTitle>
              <CardDescription>
                原价与周期以服务器币种展示；其余数值按 {reportingCurrency} 折算，点击列头排序。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{sortHeader('name', '服务器')}</TableHead>
                    <TableHead className="text-right">原价 / 周期</TableHead>
                    <TableHead className="text-right">服务天数</TableHead>
                    <TableHead className="text-right">{sortHeader('daily', `日均成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{sortHeader('total', `总成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{sortHeader('share', '占比')}</TableHead>
                    <TableHead className="text-right">状态</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => {
                    const { server } = row
                    return (
                      <TableRow key={server.server_id}>
                        <TableCell>
                          <span className="flex min-w-0 items-center gap-2">
                            <CountryFlag code={server.country_code} label={server.country_code} className="rounded-[2px] text-base" />
                            <span className="truncate font-medium" title={server.alias}>{server.alias}</span>
                            {server.location ? (
                              <span className="hidden truncate text-muted-foreground sm:inline">{server.location}</span>
                            ) : null}
                          </span>
                        </TableCell>
                        <TableCell className="text-right tabular-nums whitespace-nowrap">
                          {money(server.amount_minor, server.currency)} {server.currency}
                          <span className="text-muted-foreground"> / {server.interval_count} {GRANULARITY_LABEL[server.interval_unit]}</span>
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{server.days_active} 天</TableCell>
                        <TableCell className="text-right tabular-nums">{money(row.daily, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums font-medium">{money(row.total, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums">{(row.share * 100).toFixed(1)}%</TableCell>
                        <TableCell className="text-right">
                          {billingStatusLabel[server.status] ? (
                            <Badge variant={billingStatusVariant[server.status] ?? 'outline'}>
                              {billingStatusLabel[server.status]}
                            </Badge>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>周期明细矩阵</CardTitle>
              <CardDescription>行 = 周期，列 = 服务器；单元格为对应周期成本（{reportingCurrency}），行尾为周期合计。</CardDescription>
            </CardHeader>
            <CardContent className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="sticky left-0 bg-card">周期</TableHead>
                    {stats.servers.map((server) => (
                      <TableHead key={server.server_id} className="max-w-36 truncate text-right" title={server.alias}>
                        {server.alias}
                      </TableHead>
                    ))}
                    <TableHead className="text-right">合计</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {stats.periods.map((period, index) => (
                    <TableRow key={period}>
                      <TableCell className="sticky left-0 bg-card font-medium whitespace-nowrap">
                        {periodLabel(period)}
                      </TableCell>
                      {stats.servers.map((server) => {
                        const costs = costsOf(server, rateMode)
                        return (
                          <TableCell key={server.server_id} className="text-right tabular-nums">
                            {costs[index] ? money(costs[index], reportingCurrency) : '—'}
                          </TableCell>
                        )
                      })}
                      <TableCell className="text-right font-medium tabular-nums">
                        {totals[index] ? money(totals[index], reportingCurrency) : '—'}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      ) : null}
    </Page>
  )
}
