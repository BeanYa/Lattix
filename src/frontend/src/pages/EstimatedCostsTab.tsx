import { useCallback, useEffect, useMemo, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Chart, type ChartOption } from '@/components/echarts'
import { EmptyState, LoadingState, Notice } from '@/components/PagePrimitives'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useTheme } from '@/lib/theme-context'
import type {
  BillingEstimatedServerStats,
  BillingEstimatedStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
import { cn } from '@/lib/utils'
import {
  GRANULARITY_LABEL,
  StatsControls,
  billingStatusLabel,
  billingStatusTone,
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

function costsOfEstimated(server: BillingEstimatedServerStats, rateMode: BillingStatsRateMode): number[] {
  return rateMode === 'custom' && server.estimated_costs_custom
    ? server.estimated_costs_custom
    : server.estimated_costs_public
}

function ratesOfEstimated(
  server: BillingEstimatedServerStats,
  rateMode: BillingStatsRateMode,
): { monthly: number; annual: number; daily: number } {
  const monthly = rateMode === 'custom' ? server.monthly_custom_minor ?? server.monthly_minor : server.monthly_minor
  const annual = rateMode === 'custom' ? server.annual_custom_minor ?? server.annual_minor : server.annual_minor
  const daily = rateMode === 'custom' ? server.daily_custom_minor ?? server.daily_minor : server.daily_minor
  return { monthly, annual, daily }
}

// estimateOf 返回当前粒度对应的估算值：粒度与计费周期匹配（如年付 + 年粒度）时
// 直接用后端返回的周期实价（monthly_minor/annual_minor 为精确值），否则为估算折算值。
function estimateOf(
  server: BillingEstimatedServerStats,
  rateMode: BillingStatsRateMode,
  granularity: BillingStatsGranularity,
): number {
  const { monthly, annual, daily } = ratesOfEstimated(server, rateMode)
  return granularity === 'year' ? annual : granularity === 'month' ? monthly : daily
}

function conversionBasis(server: BillingEstimatedServerStats, granularity: BillingStatsGranularity): string {
  const { interval_unit: unit, interval_count: count } = server
  if (granularity === 'day') {
    if (unit === 'day' && count === 1) return '日付'
    if (unit === 'day') return `实付 ÷ ${count} 天`
    return '月成本 ÷ 30'
  }
  if (granularity === 'month') {
    if (unit === 'month' && count === 1) return '月付'
    if (unit === 'month') return `实付 ÷ ${count} 月`
    if (unit === 'year') return '年成本 ÷ 12'
    return '日成本 × 30'
  }
  if (unit === 'year' && count === 1) return '年付'
  if (unit === 'year') return `实付 ÷ ${count} 年`
  if (unit === 'month') return '月成本 × 12'
  return '日成本 × 360'
}

function formulaParts(
  server: BillingEstimatedServerStats,
  rateMode: BillingStatsRateMode,
  currency: string,
  granularity: BillingStatsGranularity,
) {
  const unit = GRANULARITY_LABEL[granularity]
  const value = estimateOf(server, rateMode, granularity)
  const basis = conversionBasis(server, granularity)
  const exact = server.interval_unit === granularity && server.interval_count === 1
  return exact
    ? `${money(value, currency)} / ${unit}（${basis}实价）`
    : `≈ ${money(value, currency)} / ${unit}（${basis}）`
}

function formulaTitle(server: BillingEstimatedServerStats): string {
  switch (server.interval_unit) {
    case 'year':
      return '月成本 = 年成本 ÷ 12；日成本 = 月成本 ÷ 30'
    case 'month':
      return server.interval_count === 3 ? '季付：年成本 = 季付 × 4；月成本 = 季付 ÷ 3；日成本 = 月成本 ÷ 30' : '年成本 = 月成本 × 12；日成本 = 月成本 ÷ 30'
    default:
      return '日成本 = 日付金额 ÷ 周期天数；月成本 = 日成本 × 30；年成本 = 日成本 × 360'
  }
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

  // 图表颜色在主题切换后从 CSS 变量重新解析（.dark 类在 ThemeProvider 的
  // layout effect 中应用，这里的被动 effect 一定在其之后运行）。
  const [chartColors, setChartColors] = useState<ChartThemeColors>(() => chartThemeColors())
  useEffect(() => {
    setChartColors(chartThemeColors())
  }, [theme])

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
  const monthlyTotal = useMemo(
    () => (stats ? stats.servers.reduce((sum, server) => sum + ratesOfEstimated(server, rateMode).monthly, 0) : 0),
    [stats, rateMode],
  )
  const annualTotal = useMemo(
    () => (stats ? stats.servers.reduce((sum, server) => sum + ratesOfEstimated(server, rateMode).annual, 0) : 0),
    [stats, rateMode],
  )
  const totals = rateMode === 'custom' && stats?.estimated_totals_custom
    ? stats.estimated_totals_custom
    : stats?.estimated_totals_public ?? []

  const reportingCurrency = stats?.reporting_currency ?? 'CNY'

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return buildDonutOption(
      rows.map((row) => ({ name: row.server.alias, value: row.total })),
      reportingCurrency,
      chartColors,
    )
  }, [stats, rows, totalAll, reportingCurrency, chartColors])

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
          <section className="cg-costs-metrics" aria-label="估算成本汇总">
            <article className="cg-metric">
              <span className="cg-metric-value">{money(dailyTotal, reportingCurrency)}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">估算日成本</span>
                <span className="cg-metric-detail">PER DAY / {reportingCurrency} · 月成本 ÷ 30 天</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">{money(monthlyTotal, reportingCurrency)}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">估算月成本</span>
                <span className="cg-metric-detail">PER MONTH / {reportingCurrency} · 月付实价，其余估算折算</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">{money(annualTotal, reportingCurrency)}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">估算年成本</span>
                <span className="cg-metric-detail">PER YEAR / {reportingCurrency} · 年付实价，其余估算折算</span>
              </span>
            </article>
            <article className="cg-metric">
              <span className="cg-metric-value">{String(stats.servers.length).padStart(2, '0')}</span>
              <span className="cg-metric-copy">
                <span className="cg-metric-label">启用计费服务器</span>
                <span className="cg-metric-detail">SERVERS / 计费中</span>
              </span>
            </article>
          </section>

          <section className="cg-card cg-costs-chart-card" aria-labelledby="cg-costs-est-donut-heading">
            <header className="cg-costs-card-head">
              <div>
                <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>COST / SHARE</span>
                <h2 className="cg-title cg-costs-card-title" id="cg-costs-est-donut-heading">成本占比</h2>
              </div>
            </header>
            <p className="cg-costs-card-desc">范围内各服务器估算成本占比。</p>
            <div className="cg-costs-chart-body">
              {totalAll > 0
                ? <Chart option={donutOption} className="h-80 w-full" />
                : <EmptyState title="范围内暂无成本" description="调整时间范围后查看占比。" className="h-80" />}
            </div>
          </section>

          <section className="cg-card cg-costs-table-card" aria-labelledby="cg-costs-est-summary-heading">
            <header className="cg-costs-card-head">
              <div>
                <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>SERVERS / SUMMARY</span>
                <h2 className="cg-title cg-costs-card-title" id="cg-costs-est-summary-heading">服务器汇总</h2>
              </div>
            </header>
            <p className="cg-costs-card-desc">
              估算总成本 = 年成本 × 整年数 + 月成本 × 整月数 + 日成本 × 剩余天数；月成本按
              30 天/月、年成本按 360 天/年折算。原价与周期以服务器币种展示；其余数值按{' '}
              {reportingCurrency} 估算，点击列头排序。
            </p>
            <div className="cg-costs-table-body">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{header('name', '服务器')}</TableHead>
                    <TableHead className="text-right">原价 / 周期</TableHead>
                    <TableHead className="text-right">{header('daily', `估算${GRANULARITY_LABEL[granularity]}成本 (${reportingCurrency})`)}</TableHead>
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
                        <TableCell className="text-right whitespace-nowrap">
                          <div className="tabular-nums">
                            {money(server.amount_minor, server.currency)} {server.currency}
                            <span className="text-muted-foreground"> / {server.interval_count} {GRANULARITY_LABEL[server.interval_unit]}</span>
                          </div>
                          <div className="text-xs text-muted-foreground tabular-nums" title={formulaTitle(server)}>
                            {formulaParts(server, rateMode, reportingCurrency, granularity)}
                          </div>
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{money(estimateOf(server, rateMode, granularity), reportingCurrency)}</TableCell>
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

          <section className="cg-card cg-costs-table-card" aria-labelledby="cg-costs-est-matrix-heading">
            <header className="cg-costs-card-head">
              <div>
                <span className="cg-micro" style={{ color: 'var(--cg-blue)' }}>COST / MATRIX</span>
                <h2 className="cg-title cg-costs-card-title" id="cg-costs-est-matrix-heading">周期明细矩阵</h2>
              </div>
            </header>
            <p className="cg-costs-card-desc">
              行 = 周期，列 = 服务器；单元格为对应周期估算成本（{reportingCurrency}），行尾为周期合计。
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
            </div>
          </section>
        </>
      ) : null}
    </div>
  )
}
