import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import { AlertTriangleIcon, ArrowDownIcon, ArrowUpIcon, ServerIcon, UsersIcon } from 'lucide-react'

import { TrafficStatsDialog } from '@/components/traffic-stats/TrafficStatsDialog'
import { useTrafficStatsDialog } from '@/components/traffic-stats/use-traffic-stats-dialog'
import { Chart } from '@/components/echarts'
import { Notice } from '@/components/PagePrimitives'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { humanizeBytes } from '@/lib/format'
import { useTheme } from '@/lib/theme-context'
import type { TrafficStatTopItem, TrafficVolume } from '@/lib/types'
import { chartThemeColors } from '@/pages/costs-shared'
import { buildTrafficBarOption } from '@/pages/runtime/traffic-chart'
import { useTrafficStats, type TrafficStatGranularity } from '@/pages/runtime/use-traffic-stats'

import './agents-stats.css'

const GRANULARITY_ORDER: TrafficStatGranularity[] = ['5m', '15m', '1h', 'day', 'month']

const GRANULARITY_LABEL: Record<TrafficStatGranularity, string> = {
  '5m': '5分钟',
  '15m': '15分钟',
  '1h': '1小时',
  day: '天',
  month: '月',
}

// 环比：与上一周期同进度（相同时间进度）对比的总量变化；
// 上一周期无流量（up+down<=0）时返回 null，卡片展示「—」。
function periodDelta(current: TrafficVolume, prev: TrafficVolume): number | null {
  const prevTotal = prev.up + prev.down
  if (prevTotal <= 0) return null
  return ((current.up + current.down) / prevTotal - 1) * 100
}

function formatDelta(delta: number): string {
  const digits = Math.abs(delta) >= 100 ? 0 : 1
  return `${delta > 0 ? '+' : ''}${delta.toFixed(digits)}%`
}

function VolumeCard({ label, volume }: { label: string; volume: TrafficVolume }) {
  return (
    <article className="cg-card cg-rstats-card">
      <span className="cg-rstats-card-label">{label}</span>
      <div className="cg-rstats-volume">
        <span className="is-up">
          <ArrowUpIcon />
          {humanizeBytes(volume.up)}
        </span>
        <span className="is-down">
          <ArrowDownIcon />
          {humanizeBytes(volume.down)}
        </span>
      </div>
      <small className="cg-rstats-card-detail">合计 {humanizeBytes(volume.up + volume.down)}</small>
    </article>
  )
}

function DeltaCard({
  label,
  delta,
  detail,
}: {
  label: string
  delta: number | null
  detail: string
}) {
  return (
    <article className="cg-card cg-rstats-card" title="与上一周期相同时间进度对比">
      <span className="cg-rstats-card-label">{label}</span>
      <strong className="cg-rstats-card-value">{delta === null ? '—' : formatDelta(delta)}</strong>
      <small className="cg-rstats-card-detail">{detail}</small>
    </article>
  )
}

function TopList({
  title,
  icon,
  items,
  fallbackLabel,
  onOpen,
}: {
  title: string
  icon: ReactNode
  items: TrafficStatTopItem[]
  fallbackLabel: (item: TrafficStatTopItem) => string
  onOpen: (item: TrafficStatTopItem) => void
}) {
  return (
    <section className="cg-card cg-rstats-top" aria-label={title}>
      <header className="cg-rstats-top-head">
        <h2>
          {icon}
          {title}
        </h2>
        <span className="cg-micro">BY VOLUME</span>
      </header>
      <div className="cg-rstats-top-list">
        {items.length === 0 ? (
          <p className="cg-rstats-empty">暂无数据</p>
        ) : (
          items.map((item, index) => (
            <button
              key={`${item.server_id}-${item.user_uuid}`}
              type="button"
              className="cg-rstats-top-row"
              onClick={() => onOpen(item)}
            >
              <span className="cg-rstats-top-rank">{index + 1}</span>
              <span className="cg-rstats-top-name">{item.label || fallbackLabel(item)}</span>
              <span className="cg-rstats-top-volume">
                <span className="is-up">↑ {humanizeBytes(item.up)}</span>
                <span className="is-down">↓ {humanizeBytes(item.down)}</span>
              </span>
            </button>
          ))
        )}
      </div>
    </section>
  )
}

/**
 * AgentsStatsTab 为「被控统计」Tab 的页面组装（纯展示层，数据全来自 hook）：
 * 概览六卡（今日/本月 ↑↓、今日与本月环比、在线 Agent、今日错误→运营日志）→
 * 粒度 Tabs + 全网流量堆叠柱图 → Top 服务器 / Top 用户 双列（行点击开下钻弹窗）→ 口径页脚。
 * onError 以 useCallback 固定引用，避免 useTrafficStats/useTrafficStatsDialog 因回调变化重拉。
 */
export default function AgentsStatsTab() {
  const [error, setError] = useState('')
  const onError = useCallback((message: string) => setError(message), [])

  const { overview, points, granularity, setGranularity, errors } = useTrafficStats({ onError })
  const dialogController = useTrafficStatsDialog({ onError })

  // overview 引用在每次轮询成功后替换，用它清除上一轮的错误提示。
  useEffect(() => {
    if (overview) setError('')
  }, [overview])

  // 图表颜色在主题切换后从 CSS 变量重新解析（同 Costs / TrafficStatsDialog）。
  const { theme } = useTheme()
  const [chartColors, setChartColors] = useState(() => chartThemeColors())
  useEffect(() => {
    setChartColors(chartThemeColors())
  }, [theme])

  const chartOption = useMemo(
    () => buildTrafficBarOption(points, granularity, chartColors),
    [points, granularity, chartColors],
  )

  if (overview === null) {
    if (error) {
      return (
        <Notice tone="danger" title="无法读取流量统计" className="max-w-xl">
          {error}
        </Notice>
      )
    }
    return (
      <div className="cg-rstats-loading" role="status" aria-label="正在读取流量统计">
        <span className="cg-micro">LOADING / AGENTS STATS</span>
        <div className="cg-rstats-loading-bar">
          <i />
          <i />
          <i />
        </div>
      </div>
    )
  }

  const todayDelta = periodDelta(overview.today, overview.today_prev)
  const monthDelta = periodDelta(overview.month, overview.month_prev)
  const todayErrorCount = errors?.today_error_count ?? null

  return (
    <>
      <TrafficStatsDialog controller={dialogController} />

      {error ? (
        <Notice tone="warning" title="本次刷新失败">
          正在显示上一次有效数据：{error}
        </Notice>
      ) : null}

      {/* 概览六卡 */}
      <section className="cg-rstats-grid" aria-label="流量概览">
        <VolumeCard label="今日流量" volume={overview.today} />
        <VolumeCard label="本月流量" volume={overview.month} />
        <DeltaCard label="今日环比" delta={todayDelta} detail="较昨日同进度" />
        <DeltaCard label="本月环比" delta={monthDelta} detail="较上月同进度" />
        <article className="cg-card cg-rstats-card">
          <span className="cg-rstats-card-label">在线 Agent</span>
          <strong className="cg-rstats-card-value">
            {overview.agents_online}
            <em> / {overview.agents_total}</em>
          </strong>
          <small className="cg-rstats-card-detail">在线 / 总数</small>
        </article>
        <Link
          href="/logs/operations?severity=error"
          className="cg-card cg-rstats-card cg-rstats-card-link"
          data-tone={todayErrorCount !== null && todayErrorCount > 0 ? 'danger' : undefined}
        >
          <span className="cg-rstats-card-label">
            <AlertTriangleIcon />
            今日错误
          </span>
          <strong className="cg-rstats-card-value">{todayErrorCount ?? '--'}</strong>
          <small className="cg-rstats-card-detail">查看运营日志 ›</small>
        </Link>
      </section>

      {/* 粒度 + 全网趋势 */}
      <section className="cg-card cg-rstats-chart" aria-labelledby="agents-stats-chart-heading">
        <header className="cg-rstats-chart-head">
          <div>
            <span className="cg-micro">FLEET TRAFFIC</span>
            <h2 className="cg-rstats-chart-title" id="agents-stats-chart-heading">
              全网流量趋势
            </h2>
          </div>
          <Tabs
            value={granularity}
            onValueChange={(value) => setGranularity(value as TrafficStatGranularity)}
          >
            <TabsList>
              {GRANULARITY_ORDER.map((option) => (
                <TabsTrigger key={option} value={option}>
                  {GRANULARITY_LABEL[option]}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </header>
        <div className="cg-rstats-chart-body">
          {points.length === 0 ? (
            <p className="cg-rstats-empty">暂无流量记录</p>
          ) : (
            <Chart option={chartOption} className="cg-rstats-chart-canvas" />
          )}
        </div>
      </section>

      {/* Top 双列（行点击开下钻弹窗） */}
      <div className="cg-rstats-top-grid">
        <TopList
          title="Top 服务器"
          icon={<ServerIcon />}
          items={overview.top_servers}
          fallbackLabel={(item) => `服务器 #${item.server_id}`}
          onOpen={(item) =>
            dialogController.open({
              kind: 'server',
              serverId: item.server_id,
              serverName: item.label || undefined,
            })
          }
        />
        <TopList
          title="Top 用户"
          icon={<UsersIcon />}
          items={overview.top_users}
          fallbackLabel={(item) => `用户 ${item.user_uuid.slice(0, 8)}`}
          onOpen={(item) =>
            dialogController.open({
              kind: 'user',
              userUuid: item.user_uuid,
              userName: item.label || undefined,
            })
          }
        />
      </div>

      <p className="cg-rstats-note">全网为用户端到端口径；单服务器为实体口径，两者含链路时不相等</p>
    </>
  )
}
