import { useCallback, useEffect, useMemo, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Chart, type ChartOption } from '@/components/echarts'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api, errorMessage } from '@/lib/api'
import { useTheme } from '@/lib/theme-context'
import type {
  BillingActualServerStats,
  BillingActualStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
import { cn } from '@/lib/utils'
import EstimatedCostsTab from './EstimatedCostsTab'
import {
  GRANULARITY_LABEL,
  StatsControls,
  billingStatusLabel,
  billingStatusTone,
  buildBarOption,
  buildDonutOption,
  chartThemeColors,
  clampRange,
  firstOfMonth,
  addMonths,
  localDate,
  money,
  periodLabel,
  useEarliestStart,
  useRowSort,
  type ChartThemeColors,
} from './costs-shared'

import './costs.css'

function costsOf(server: BillingActualServerStats, rateMode: BillingStatsRateMode): number[] {
  return rateMode === 'custom' && server.actual_costs_custom ? server.actual_costs_custom : server.actual_costs_public
}

interface ServerRow {
  name: string
  server: BillingActualServerStats
  total: number
  share: number
  daily: number
}

function ActualCostsTab() {
  const { theme } = useTheme()
  const [granularity, setGranularity] = useState<BillingStatsGranularity>('month')
  const [from, setFrom] = useState(() => firstOfMonth(addMonths(localDate(), -11)))
  const [to, setTo] = useState(() => localDate())
  const [rateMode, setRateMode] = useState<BillingStatsRateMode>('custom')
  const [stats, setStats] = useState<BillingActualStats | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const { sort, header } = useRowSort()
  const earliestStart = useEarliestStart()

  // 图表颜色在主题切换后从 CSS 变量重新解析（.dark 类在 ThemeProvider 的
  // layout effect 中应用，这里的被动 effect 一定在其之后运行）。
  const [chartColors, setChartColors] = useState<ChartThemeColors>(() => chartThemeColors())
  useEffect(() => {
    setChartColors(chartThemeColors())
  }, [theme])

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
  const totals = rateMode === 'custom' && stats?.actual_totals_custom ? stats.actual_totals_custom : stats?.actual_totals_public ?? []
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

  const barOption = useMemo<ChartOption>(() => {
    if (!stats) return {}
    return buildBarOption({
      periods: stats.periods,
      servers: stats.servers.map((server) => ({
        alias: server.alias,
        costs: costsOf(server, rateMode),
      })),
      granularity,
      currency: reportingCurrency,
      colors: chartColors,
    })
  }, [stats, rateMode, granularity, reportingCurrency, chartColors])

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return buildDonutOption(
      rows.map((row) => ({ name: row.server.alias, value: row.total })),
      reportingCurrency,
      chartColors,
    )
  }, [stats, rows, totalAll, reportingCurrency, chartColors])

  const errorPage = (
    <Notice tone="danger" title="成本统计加载失败">{error}</Notice>
  )

  return (
    <div className="cg-costs-tab">
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
      {error ? errorPage : null}
      {loading && !stats ? (
        <LoadingState className="py-16">正在统计成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用统计计费的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示已生效成本。"
        />
      ) : stats ? (
        <>
          <section className="cg-costs-metrics" aria-label="成本汇总">
            <article className="cg-metric">
              <span className="cg-metric-value">{money(totalAll, reportingCurrency)}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">范围内总成本</span>
                <span className="cg-metric-detail">TOTAL / {reportingCurrency}</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">{String(stats.servers.length).padStart(2, '0')}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">启用计费服务器</span>
                <span className="cg-metric-detail">SERVERS / 计费中</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">{money(Math.round(totalAll / monthsInRange), reportingCurrency)}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">平均月成本</span>
                <span className="cg-metric-detail">AVG MONTH / {reportingCurrency}</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">
                {topServer ? money(topServer.total, reportingCurrency) : '—'}
              </span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">成本最高服务器</span>
                <span className="cg-metric-detail" title={topServer?.server.alias}>
                  TOP / {topServer?.server.alias ?? '—'}
                </span>
              </span>
            </article>
          </section>

          <div className="cg-costs-charts">
            <section className="cg-card cg-costs-chart-card" aria-labelledby="cg-costs-bar-heading">
              <header className="cg-costs-card-head">
                <div>
                  <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>COST / PERIODS</span>
                  <h2 className="cg-title cg-costs-card-title" id="cg-costs-bar-heading">周期成本分布</h2>
                </div>
                <span className="cg-status is-blue">{GRANULARITY_LABEL[granularity]}粒度</span>
              </header>
              <p className="cg-costs-card-desc">每台服务器一个色段，悬停查看明细；图例可点击隐藏单台服务器。</p>
              <div className="cg-costs-chart-body">
                <Chart option={barOption} className="h-80 w-full" />
              </div>
            </section>
            <section className="cg-card cg-costs-chart-card" aria-labelledby="cg-costs-donut-heading">
              <header className="cg-costs-card-head">
                <div>
                  <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>COST / SHARE</span>
                  <h2 className="cg-title cg-costs-card-title" id="cg-costs-donut-heading">成本占比</h2>
                </div>
              </header>
              <p className="cg-costs-card-desc">范围内各服务器成本占比。</p>
              <div className="cg-costs-chart-body">
                {totalAll > 0
                  ? <Chart option={donutOption} className="h-80 w-full" />
                  : <EmptyState title="范围内暂无成本" description="调整时间范围后查看占比。" className="h-80" />}
              </div>
            </section>
          </div>

          <section className="cg-card cg-costs-table-card" aria-labelledby="cg-costs-summary-heading">
            <header className="cg-costs-card-head">
              <div>
                <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>SERVERS / SUMMARY</span>
                <h2 className="cg-title cg-costs-card-title" id="cg-costs-summary-heading">服务器汇总</h2>
              </div>
            </header>
            <p className="cg-costs-card-desc">
              原价与周期以服务器币种展示；其余数值按 {reportingCurrency} 折算，点击列头排序。
            </p>
            <div className="cg-costs-table-body">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{header('name', '服务器')}</TableHead>
                    <TableHead className="text-right">原价 / 周期</TableHead>
                    <TableHead className="text-right">服务天数</TableHead>
                    <TableHead className="text-right">{header('daily', `日均成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{header('total', `总成本 (${reportingCurrency})`)}</TableHead>
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
                        <TableCell className="text-right tabular-nums">{server.days_active} 天</TableCell>
                        <TableCell className="text-right tabular-nums">{money(row.daily, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums font-medium">{money(row.total, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums">{(row.share * 100).toFixed(1)}%</TableCell>
                        <TableCell className="text-right">
                          {billingStatusLabel[server.status] ? (
                            <span className={cn('cg-status', billingStatusTone[server.status] ?? 'is-muted')}>
                              {billingStatusLabel[server.status]}
                            </span>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          </section>

          <section className="cg-card cg-costs-table-card" aria-labelledby="cg-costs-matrix-heading">
            <header className="cg-costs-card-head">
              <div>
                <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>COST / MATRIX</span>
                <h2 className="cg-title cg-costs-card-title" id="cg-costs-matrix-heading">周期明细矩阵</h2>
              </div>
            </header>
            <p className="cg-costs-card-desc">
              行 = 周期，列 = 服务器；单元格为对应周期成本（{reportingCurrency}），行尾为周期合计。
            </p>
            <div className="cg-costs-table-body is-scroll">
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
            </div>
          </section>
        </>
      ) : null}
    </div>
  )
}

export default function Costs() {
  const [tab, setTab] = useState<'actual' | 'estimated'>('actual')
  return (
    <Page className="cg-costs">
      <div className="cg-costs-topline">
        <span className="cg-eyebrow">BILLING / COSTS</span>
      </div>
      <PageHeader
        title="成本统计"
        description="已生效成本按服务期摊算实际花费；计算成本按日成本估算各周期成本，统一以统计币种展示"
      />
      <Tabs value={tab} onValueChange={(value) => value && setTab(value as 'actual' | 'estimated')}>
        <TabsList>
          <TabsTrigger value="actual">已生效成本</TabsTrigger>
          <TabsTrigger value="estimated">计算成本</TabsTrigger>
        </TabsList>
      </Tabs>
      {tab === 'actual' ? <ActualCostsTab /> : <EstimatedCostsTab />}
    </Page>
  )
}
