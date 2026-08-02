import { useCallback, useEffect, useMemo, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Chart, type ChartOption } from '@/components/echarts'
import { EmptyState, LoadingState, Notice } from '@/components/PagePrimitives'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useTheme } from '@/lib/theme-context'
import type {
  BillingEstimatedServerStats,
  BillingEstimatedStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
import {
  GRANULARITY_LABEL,
  StatsControls,
  billingStatusLabel,
  billingStatusVariant,
  buildBarOption,
  buildDonutOption,
  clampRange,
  firstOfMonth,
  addMonths,
  localDate,
  money,
  periodLabel,
  useEarliestStart,
  useRowSort,
} from './costs-shared'

function costsOfEstimated(server: BillingEstimatedServerStats, rateMode: BillingStatsRateMode): number[] {
  return rateMode === 'custom' && server.estimated_costs_custom
    ? server.estimated_costs_custom
    : server.estimated_costs_public
}

export default function EstimatedCostsTab() {
  const { theme } = useTheme()
  const [granularity, setGranularity] = useState<BillingStatsGranularity>('month')
  const [from, setFrom] = useState(() => firstOfMonth(addMonths(localDate(), -11)))
  const [to, setTo] = useState(() => localDate())
  const [rateMode, setRateMode] = useState<BillingStatsRateMode>('custom')
  const [stats, setStats] = useState<BillingEstimatedStats | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const { sort, header } = useRowSort()
  const earliestStart = useEarliestStart()

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await api.billingStatsEstimated({
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

  const rows = useMemo(() => {
    if (!stats) return []
    const totalAll = stats.servers.reduce(
      (sum, server) => sum + costsOfEstimated(server, rateMode).reduce((acc, value) => acc + value, 0),
      0,
    )
    return stats.servers.map((server) => {
      const costs = costsOfEstimated(server, rateMode)
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
  const dailyTotal = useMemo(() => rows.reduce((sum, row) => sum + row.daily, 0), [rows])
  const totals = rateMode === 'custom' && stats?.estimated_totals_custom
    ? stats.estimated_totals_custom
    : stats?.estimated_totals_public ?? []

  const reportingCurrency = stats?.reporting_currency ?? 'CNY'
  const textColor = theme === 'dark' ? '#c9cbe2' : '#686a7c'
  const axisColor = theme === 'dark' ? '#42466f' : '#d4d6e0'

  const barOption = useMemo<ChartOption>(() => {
    if (!stats) return {}
    return buildBarOption({
      periods: stats.periods,
      servers: stats.servers.map((server) => ({
        alias: server.alias,
        costs: costsOfEstimated(server, rateMode),
      })),
      granularity,
      currency: reportingCurrency,
      textColor,
      axisColor,
    })
  }, [stats, rateMode, granularity, reportingCurrency, textColor, axisColor])

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return buildDonutOption(
      rows.map((row) => ({ name: row.server.alias, value: row.total })),
      reportingCurrency,
      textColor,
      theme,
    )
  }, [stats, rows, totalAll, reportingCurrency, textColor, theme])

  return (
    <>
      <StatsControls
        granularity={granularity}
        from={from}
        to={to}
        rateMode={rateMode}
        customAvailable={stats?.custom_available ?? false}
        rateDate={stats?.rate_date}
        presetsDisabled={!earliestStart}
        onGranularity={changeGranularity}
        onFrom={setFrom}
        onTo={setTo}
        onPreset={applyPreset}
        onRateMode={(value) => setRateMode(value)}
      />
      {error ? <Notice tone="danger" title="计算成本加载失败">{error}</Notice> : null}
      {loading && !stats ? (
        <LoadingState className="py-16">正在估算成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用计费且未过期的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示计算成本估算。"
        />
      ) : stats ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <Card>
              <CardHeader>
                <CardDescription>估算日成本</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 天 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>估算月成本（×30）</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal * 30, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 月 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>估算年成本（×365）</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal * 365, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 年 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>启用计费服务器</CardDescription>
                <CardTitle className="text-2xl tabular-nums">{stats.servers.length} 台</CardTitle>
              </CardHeader>
            </Card>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle>周期估算成本分布</CardTitle>
                <CardDescription>每台服务器一个色段，悬停查看明细；图例可点击隐藏单台服务器。</CardDescription>
              </CardHeader>
              <CardContent>
                <Chart option={barOption} className="h-80 w-full" />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>成本占比</CardTitle>
                <CardDescription>范围内各服务器估算成本占比。</CardDescription>
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
                原价与周期以服务器币种展示；其余数值按 {reportingCurrency} 估算，点击列头排序。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{header('name', '服务器')}</TableHead>
                    <TableHead className="text-right">原价 / 周期</TableHead>
                    <TableHead className="text-right">{header('daily', `估算日成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{header('total', `估算总成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{header('share', '占比')}</TableHead>
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
              <CardDescription>行 = 周期，列 = 服务器；单元格为对应周期估算成本（{reportingCurrency}），行尾为周期合计。</CardDescription>
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
                        {periodLabel(period, granularity)}
                      </TableCell>
                      {stats.servers.map((server) => {
                        const costs = costsOfEstimated(server, rateMode)
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
    </>
  )
}
