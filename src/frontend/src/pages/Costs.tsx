import { useCallback, useEffect, useMemo, useState } from 'react'
import { CoinsIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Chart, type ChartOption } from '@/components/echarts'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
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
  return rateMode === 'custom' && server.actual_costs_custom
    ? server.actual_costs_custom
    : server.actual_costs_public
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

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const result = await api.billingStats(
          {
            from,
            to,
            granularity,
            rate_mode: rateMode,
          },
          signal ? { signal, display: 'silent' } : { display: 'silent' },
        )
        if (signal?.aborted) return
        setStats(result)
        setError('')
      } catch (err) {
        if (signal?.aborted) return
        setError(errorMessage(err))
      } finally {
        if (!signal?.aborted) setLoading(false)
      }
    },
    [from, to, granularity, rateMode],
  )

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
    return stats.servers
      .map((server) => {
        const costs = costsOf(server, rateMode)
        const total = costs.reduce((sum, value) => sum + value, 0)
        return {
          name: server.alias,
          server,
          total,
          share: totalAll > 0 ? total / totalAll : 0,
          daily:
            rateMode === 'custom'
              ? (server.daily_custom_minor ?? server.daily_minor)
              : server.daily_minor,
        }
      })
      .sort((a, b) => {
        const left = a[sort.key]
        const right = b[sort.key]
        if (typeof left === 'string' && typeof right === 'string') {
          return left.localeCompare(right) * sort.dir
        }
        return ((left as number) - (right as number)) * sort.dir
      })
  }, [stats, rateMode, sort])

  const totalAll = useMemo(() => rows.reduce((sum, row) => sum + row.total, 0), [rows])
  const totals =
    rateMode === 'custom' && stats?.actual_totals_custom
      ? stats.actual_totals_custom
      : (stats?.actual_totals_public ?? [])
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
    <Notice tone="danger" title="成本统计加载失败">
      {error}
    </Notice>
  )

  return (
    <div className="cost-tab-panel">
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
        <LoadingState className="cost-loading">正在统计成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用统计计费的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示已生效成本。"
        />
      ) : stats ? (
        <>
          <section className="cost-mod" aria-label="成本汇总">
            <header className="cost-mod-head">
              <span className="cost-tab">COST</span>
              <h2 className="cost-mod-title">成本汇总</h2>
              <span className="cost-mod-meta">{reportingCurrency} · 已生效成本</span>
            </header>
            <div className="cost-summary">
              <div className="cost-summary-cell">
                <span className="cost-summary-label">范围内总成本</span>
                <strong className="cost-summary-value">
                  {money(totalAll, reportingCurrency)}
                  <small>{reportingCurrency}</small>
                </strong>
                <span className="cost-summary-detail">TOTAL / {reportingCurrency}</span>
              </div>
              <div className="cost-summary-cell">
                <span className="cost-summary-label">启用计费服务器</span>
                <strong className="cost-summary-value">
                  {String(stats.servers.length).padStart(2, '0')}
                </strong>
                <span className="cost-summary-detail">SERVERS / 计费中</span>
              </div>
              <div className="cost-summary-cell">
                <span className="cost-summary-label">平均月成本</span>
                <strong className="cost-summary-value">
                  {money(Math.round(totalAll / monthsInRange), reportingCurrency)}
                  <small>{reportingCurrency}</small>
                </strong>
                <span className="cost-summary-detail">AVG MONTH / {reportingCurrency}</span>
              </div>
              <div className="cost-summary-cell">
                <span className="cost-summary-label">成本最高服务器</span>
                <strong className="cost-summary-value">
                  {topServer ? money(topServer.total, reportingCurrency) : '—'}
                  {topServer ? <small>{reportingCurrency}</small> : null}
                </strong>
                <span className="cost-summary-detail" title={topServer?.server.alias}>
                  TOP / {topServer?.server.alias ?? '—'}
                </span>
              </div>
            </div>
          </section>

          <div className="cost-charts">
            <section className="cost-mod cost-chart-card" aria-labelledby="cg-costs-bar-heading">
              <header className="cost-mod-head">
                <span className="cost-tab">PERIOD</span>
                <h2 className="cost-mod-title" id="cg-costs-bar-heading">
                  周期成本分布
                </h2>
                <span className="cost-mod-meta">{GRANULARITY_LABEL[granularity]}粒度</span>
              </header>
              <p className="cost-mod-desc">
                每台服务器一个色段，悬停查看明细；图例可点击隐藏单台服务器。
              </p>
              <div className="cost-chart-body">
                <Chart option={barOption} className="cost-chart-canvas" />
              </div>
            </section>
            <section className="cost-mod cost-chart-card" aria-labelledby="cg-costs-donut-heading">
              <header className="cost-mod-head">
                <span className="cost-tab">SHARE</span>
                <h2 className="cost-mod-title" id="cg-costs-donut-heading">
                  成本占比
                </h2>
                <span className="cost-mod-meta">{reportingCurrency}</span>
              </header>
              <p className="cost-mod-desc">范围内各服务器成本占比。</p>
              <div className="cost-chart-body">
                {totalAll > 0 ? (
                  <Chart option={donutOption} className="cost-chart-canvas" />
                ) : (
                  <EmptyState
                    title="范围内暂无成本"
                    description="调整时间范围后查看占比。"
                    className="cost-chart-empty"
                  />
                )}
              </div>
            </section>
          </div>

          <section className="cost-mod" aria-labelledby="cg-costs-summary-heading">
            <header className="cost-mod-head">
              <span className="cost-tab">SERVERS</span>
              <h2 className="cost-mod-title" id="cg-costs-summary-heading">
                服务器汇总
              </h2>
              <span className="cost-mod-meta">{rows.length} 台 · 点击列头排序</span>
            </header>
            <p className="cost-mod-desc">
              原价与周期以服务器币种展示；其余数值按 {reportingCurrency} 折算，点击列头排序。
            </p>
            <div className="cost-table-wrap">
              <table className="cost-table">
                <thead>
                  <tr>
                    <th scope="col">{header('name', '服务器')}</th>
                    <th scope="col" className="cost-num cost-col-secondary">
                      原价 / 周期
                    </th>
                    <th scope="col" className="cost-num cost-col-secondary">
                      服务天数
                    </th>
                    <th scope="col" className="cost-num">
                      {header('daily', `日均成本 (${reportingCurrency})`)}
                    </th>
                    <th scope="col" className="cost-num">
                      {header('total', `总成本 (${reportingCurrency})`)}
                    </th>
                    <th scope="col" className="cost-num">
                      {header('share', '占比')}
                    </th>
                    <th scope="col" className="cost-num">
                      状态
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => {
                    const { server } = row
                    return (
                      <tr key={server.server_id}>
                        <td>
                          <span className="cost-server">
                            <CountryFlag
                              code={server.country_code}
                              label={server.country_code}
                              className="cost-flag"
                            />
                            <span className="cost-server-name" title={server.alias}>
                              {server.alias}
                            </span>
                            {server.location ? (
                              <span className="cost-server-loc" title={server.location}>
                                {server.location}
                              </span>
                            ) : null}
                          </span>
                        </td>
                        <td className="cost-num cost-col-secondary">
                          {money(server.amount_minor, server.currency)} {server.currency}
                          <span className="cost-muted">
                            {' '}
                            / {server.interval_count} {GRANULARITY_LABEL[server.interval_unit]}
                          </span>
                        </td>
                        <td className="cost-num cost-col-secondary">{server.days_active} 天</td>
                        <td className="cost-num">{money(row.daily, reportingCurrency)}</td>
                        <td className="cost-num cost-strong">
                          {money(row.total, reportingCurrency)}
                        </td>
                        <td className="cost-num">{(row.share * 100).toFixed(1)}%</td>
                        <td className="cost-num">
                          {billingStatusLabel[server.status] ? (
                            <span
                              className={cn(
                                'cg-status',
                                billingStatusTone[server.status] ?? 'is-muted',
                              )}
                            >
                              {billingStatusLabel[server.status]}
                            </span>
                          ) : null}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </section>

          <section className="cost-mod" aria-labelledby="cg-costs-matrix-heading">
            <header className="cost-mod-head">
              <span className="cost-tab">MATRIX</span>
              <h2 className="cost-mod-title" id="cg-costs-matrix-heading">
                周期明细矩阵
              </h2>
              <span className="cost-mod-meta">
                {stats.periods.length} 周期 × {stats.servers.length} 服务器
              </span>
            </header>
            <p className="cost-mod-desc">
              行 = 周期，列 = 服务器；单元格为对应周期成本（{reportingCurrency}），行尾为周期合计。
            </p>
            <div className="cost-table-wrap cost-table-scroll">
              <table className="cost-table cost-table-matrix">
                <thead>
                  <tr>
                    <th scope="col" className="cost-sticky">
                      周期
                    </th>
                    {stats.servers.map((server) => (
                      <th
                        key={server.server_id}
                        scope="col"
                        className="cost-num cost-matrix-server"
                        title={server.alias}
                      >
                        {server.alias}
                      </th>
                    ))}
                    <th scope="col" className="cost-num">
                      合计
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {stats.periods.map((period, index) => (
                    <tr key={period}>
                      <td className="cost-sticky cost-period">
                        {periodLabel(period, granularity)}
                      </td>
                      {stats.servers.map((server) => {
                        const costs = costsOf(server, rateMode)
                        return (
                          <td key={server.server_id} className="cost-num">
                            {costs[index] ? money(costs[index], reportingCurrency) : '—'}
                          </td>
                        )
                      })}
                      <td className="cost-num cost-strong">
                        {totals[index] ? money(totals[index], reportingCurrency) : '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
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
    <Page className="costs">
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
