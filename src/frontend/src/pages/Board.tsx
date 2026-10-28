import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ActivityIcon,
  ArrowUpRightIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  CircleAlertIcon,
  Clock3Icon,
  RefreshCwIcon,
  ServerIcon,
  TriangleAlertIcon,
  UsersIcon,
  WaypointsIcon,
} from 'lucide-react'
import { Link } from 'wouter'

import { useBoardQueueContext } from '@/lib/board-queue-context'
import type { BoardItem, BoardSeverity } from '@/lib/board-queue'
import { api, errorMessage } from '@/lib/api'
import { humanizeBytes } from '@/lib/format'
import type {
  LogSeverity,
  OperationCategory,
  OperationLogEntry,
  TrafficStatPoint,
} from '@/lib/types'
import { cn } from '@/lib/utils'

/** 与 AppShell 一致的焦点环，颜色跟随 tokens.css 的 --panel-focus。 */
const focusRing =
  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]'

/** 严重度的三重编码：颜色 + 图标 + 文字，任何一项都不能单独承担语义。 */
const SEVERITY: Record<
  BoardSeverity,
  { label: string; text: string; chip: string; bar: string; icon: typeof TriangleAlertIcon }
> = {
  critical: {
    label: '故障',
    text: 'text-failed',
    chip: 'border-failed/40 bg-failed/10 text-failed',
    bar: 'bg-failed',
    icon: TriangleAlertIcon,
  },
  warning: {
    label: '降级',
    text: 'text-degraded',
    chip: 'border-degraded/40 bg-degraded/10 text-degraded',
    bar: 'bg-degraded',
    icon: CircleAlertIcon,
  },
}

const LOG_TONE: Record<LogSeverity, string> = {
  error: 'bg-failed',
  warning: 'bg-degraded',
  info: 'bg-signal',
  debug: 'bg-ink-3',
}

const CATEGORY_LABEL: Record<OperationCategory, string> = {
  server: '服务器',
  chain: '链路',
  user: '用户',
  settings: '设置',
  panel: '面板',
  agent: 'Agent',
  command: '命令',
  auth: '认证',
  log: '日志',
}

/** 持续时长：只在有可靠起点时显示，缺失时明确写「未知」而不是 0。 */
function formatDuration(since: string | undefined, now: number): string {
  if (!since) return '未知'
  const timestamp = Date.parse(since)
  if (Number.isNaN(timestamp)) return '未知'
  const minutes = Math.max(0, Math.floor((now - timestamp) / 60_000))
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时 ${minutes % 60} 分`
  const days = Math.floor(hours / 24)
  return `${days} 天 ${hours % 24} 小时`
}

/** 日志与同步时间统一用 24 小时时钟，避免依赖语言环境。 */
function formatClock(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}

/** 页头日期：固定中文格式，服务端与浏览器时区差由 TimezoneProvider 处理。 */
function formatToday(now: number): string {
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'long',
  }).format(new Date(now))
}

/** 24 小时流量脉搏 SVG 微图（方案 8.2.2 / 10.5：无 ECharts 首屏依赖，支持悬停读数）。 */
function FleetPulseSparkline({ points }: { points: TrafficStatPoint[] }) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)
  const totalBytes = useMemo(() => points.reduce((sum, pt) => sum + pt.up + pt.down, 0), [points])
  const peakBytes = useMemo(
    () => points.reduce((max, pt) => Math.max(max, pt.up + pt.down), 0),
    [points],
  )

  if (points.length === 0 || totalBytes === 0) {
    return (
      <div className="rounded-lg border border-rule bg-inset/60 px-3 py-3">
        <div className="flex items-center justify-between text-xs text-ink-3">
          <span className="inline-flex items-center gap-1.5 font-semibold">
            <ActivityIcon className="size-3.5 text-signal" aria-hidden="true" />
            24h 流量脉搏
          </span>
          <span className="font-mono">暂无采样</span>
        </div>
        <p className="mt-1.5 text-xs text-ink-3">产生代理流量后将在此绘制过去 24 小时吞吐曲线。</p>
      </div>
    )
  }

  const width = 320
  const height = 64
  const padY = 6
  const maxVal = Math.max(1, peakBytes)
  const coords = points.map((pt, idx) => {
    const x = points.length <= 1 ? width / 2 : (idx / (points.length - 1)) * width
    const val = pt.up + pt.down
    const y = height - padY - (val / maxVal) * (height - padY * 2)
    return { x, y, pt, val }
  })

  const linePath = coords
    .map((c, idx) => `${idx === 0 ? 'M' : 'L'}${c.x.toFixed(1)},${c.y.toFixed(1)}`)
    .join(' ')
  const areaPath = `${linePath} L${width},${height} L0,${height} Z`
  const active = hoverIdx !== null ? coords[hoverIdx] : coords[coords.length - 1]
  const activeTime = active
    ? new Date(active.pt.ts * 1000).toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })
    : ''

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-rule bg-inset/60 p-3">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="inline-flex items-center gap-1.5 font-semibold text-ink-2">
          <ActivityIcon className="size-3.5 text-signal" aria-hidden="true" />
          24h 吞吐脉搏
        </span>
        <span className="font-mono text-[12px] font-semibold text-ink tabular-nums">
          总计 {humanizeBytes(totalBytes)}
        </span>
      </div>

      <div className="relative h-16 w-full select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="h-full w-full overflow-visible"
          aria-hidden="true"
        >
          <defs>
            <linearGradient id="board-pulse-grad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--panel-signal)" stopOpacity="0.28" />
              <stop offset="100%" stopColor="var(--panel-signal)" stopOpacity="0.0" />
            </linearGradient>
          </defs>
          <path d={areaPath} fill="url(#board-pulse-grad)" />
          <path
            d={linePath}
            fill="none"
            stroke="var(--panel-signal)"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          {active ? (
            <>
              <line
                x1={active.x}
                y1={0}
                x2={active.x}
                y2={height}
                stroke="var(--panel-rule-strong)"
                strokeDasharray="2 2"
                strokeWidth="1"
              />
              <circle
                cx={active.x}
                cy={active.y}
                r="3.5"
                fill="var(--panel-signal)"
                stroke="var(--panel-surface)"
                strokeWidth="1.5"
              />
            </>
          ) : null}
          {coords.map((c, idx) => (
            <rect
              key={c.pt.ts}
              x={idx === 0 ? 0 : (coords[idx - 1].x + c.x) / 2}
              y={0}
              width={
                points.length <= 1
                  ? width
                  : idx === 0
                    ? (coords[1].x - c.x) / 2
                    : idx === coords.length - 1
                      ? (c.x - coords[idx - 1].x) / 2
                      : (coords[idx + 1].x - coords[idx - 1].x) / 2
              }
              height={height}
              fill="transparent"
              className="cursor-crosshair"
              onMouseEnter={() => setHoverIdx(idx)}
            />
          ))}
        </svg>
      </div>

      {active ? (
        <div className="flex items-center justify-between border-t border-rule pt-1.5 font-mono text-[11px] text-ink-3 tabular-nums">
          <span>{hoverIdx !== null ? `${activeTime} 采样` : `最新 (${activeTime})`}</span>
          <span>
            ↑ {humanizeBytes(active.pt.up)} · ↓ {humanizeBytes(active.pt.down)}
          </span>
        </div>
      ) : null}
    </div>
  )
}

/** 队列行：整行可展开，展开区承载「为什么 / 影响 / 动作」。 */
function QueueRow({
  item,
  index,
  now,
  expanded,
  onToggle,
}: {
  item: BoardItem
  index: number
  now: number
  expanded: boolean
  onToggle: () => void
}) {
  const severity = SEVERITY[item.severity]
  const SeverityIcon = severity.icon
  const panelId = `board-item-${item.id}`

  return (
    <li
      className="cg-stagger-item relative border-b border-rule last:border-b-0"
      style={{ ['--stagger-index' as string]: index }}
    >
      {/* 左侧身份/状态色带 */}
      <span
        aria-hidden="true"
        className={cn(
          ' absolute inset-y-2 left-0 w-[3px] rounded-r-xs transition-all',
          item.kind === 'chain' && item.lineIndex !== undefined ? '' : severity.bar,
        )}
        style={
          item.kind === 'chain' && item.lineIndex !== undefined
            ? { background: `var(--panel-line-${item.lineIndex})` }
            : undefined
        }
      />

      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={onToggle}
        className={cn(
          'grid min-h-14 w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5 text-left transition-colors duration-[var(--duration-1)] hover:bg-raised/70 md:grid-cols-[auto_minmax(0,1.5fr)_minmax(0,1.1fr)_auto_auto] lg:grid-cols-[auto_minmax(0,1.5fr)_minmax(0,1.1fr)_auto_auto_auto]',
          expanded && 'bg-raised/50',
          focusRing,
        )}
      >
        <span
          className={cn(
            'inline-flex items-center gap-1.5 rounded-sm border px-2 py-0.5 text-[13px] font-semibold',
            severity.chip,
          )}
        >
          <SeverityIcon aria-hidden="true" className="size-3.5" />
          {severity.label}
        </span>

        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex min-w-0 items-center gap-2">
            {item.kind === 'chain' && item.lineIndex !== undefined ? (
              <span
                className="cg-line-badge shrink-0"
                style={{ ['--line-color' as string]: `var(--panel-line-${item.lineIndex})` }}
                lang="en"
              >
                L{item.lineIndex}
              </span>
            ) : item.kind === 'server' ? (
              <ServerIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
            ) : (
              <WaypointsIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
            )}
            <span className="truncate text-[16px] font-semibold text-ink">{item.title}</span>
            <span className="shrink-0 rounded-xs border border-rule bg-raised px-1.5 py-0.5 text-[11px] font-semibold text-ink-3">
              {item.kind === 'server' ? '服务器' : '链路'}
            </span>
          </span>
          <span className="truncate text-[13.5px] text-ink-3 md:hidden">{item.reason}</span>
        </span>

        <span className="hidden min-w-0 truncate text-[14px] text-ink-2 md:block">
          {item.reason}
        </span>
        <span className="hidden whitespace-nowrap font-mono text-[13px] text-ink-2 tabular-nums md:block">
          {item.impact ?? '影响面未知'}
        </span>
        <span className="hidden items-center gap-1 whitespace-nowrap font-mono text-[13px] text-ink-3 tabular-nums sm:flex">
          <Clock3Icon aria-hidden="true" className="size-3.5" />
          {formatDuration(item.since, now)}
        </span>

        <ChevronDownIcon
          aria-hidden="true"
          className={cn(
            'size-4 shrink-0 text-ink-3 transition-transform duration-[var(--duration-2)]',
            expanded && 'rotate-180 text-signal',
          )}
        />
      </button>

      {expanded ? (
        <div
          id={panelId}
          className="animate-in fade-in-0 slide-in-from-top-1 border-t border-rule bg-inset/55 px-5 py-3.5 duration-150"
        >
          <dl className="grid gap-2.5 text-[14px] sm:grid-cols-[96px_minmax(0,1fr)]">
            <dt className="font-semibold text-ink-3">异常原因</dt>
            <dd className="text-ink">{item.reason}</dd>
            <dt className="font-semibold text-ink-3">影响范围</dt>
            <dd className="font-mono text-ink tabular-nums">
              {item.impact ?? '影响面未知（尚未计算受影响用户与订阅）'}
            </dd>
            <dt className="font-semibold text-ink-3">持续时长</dt>
            <dd className="font-mono text-ink tabular-nums">
              {formatDuration(item.since, now)}
              {item.since ? ` · 起于 ${formatClock(item.since)}` : ' · 缺少可靠的开始时间'}
            </dd>
          </dl>
          <div className="mt-3.5 flex flex-wrap items-center gap-2">
            {(item.actions ?? []).map((action) => (
              <Link
                key={action.href + action.label}
                href={action.href}
                className={cn(
                  'inline-flex min-h-8 items-center gap-1.5 rounded-sm border px-3 text-[13.5px] font-semibold transition-all',
                  action.tone === 'primary'
                    ? 'border-signal bg-signal text-signal-ink hover:bg-[var(--panel-signal-hover)]'
                    : 'border-rule-strong bg-surface text-ink-2 hover:bg-raised hover:text-ink',
                  focusRing,
                )}
              >
                {action.label}
                <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
              </Link>
            ))}
          </div>
        </div>
      ) : null}
    </li>
  )
}

/** 队列骨架：几何与真实行一致，避免加载完成后跳动。 */
function QueueSkeleton() {
  return (
    <ul aria-hidden="true" className="divide-y divide-rule">
      {[0, 1, 2, 3].map((row) => (
        <li key={row} className="flex min-h-14 items-center gap-3 px-4">
          <span className="h-5 w-14 animate-pulse rounded-sm border border-rule bg-raised" />
          <span className="h-4 w-44 animate-pulse rounded-sm bg-raised" />
          <span className="ml-auto h-3.5 w-24 animate-pulse rounded-sm bg-raised" />
        </li>
      ))}
    </ul>
  )
}

export default function Board() {
  const { items, counts, summary, loading, error, refresh } = useBoardQueueContext()
  const [now, setNow] = useState(() => Date.now())
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [severityFilter, setSeverityFilter] = useState<'all' | BoardSeverity>('all')
  const [logs, setLogs] = useState<OperationLogEntry[]>([])
  const [logsLoading, setLogsLoading] = useState(true)
  const [logsError, setLogsError] = useState<string | null>(null)
  const [pulseSeries, setPulseSeries] = useState<TrafficStatPoint[]>([])
  const logsRequest = useRef(0)

  // 进入控制台时立即前台刷新共享队列：页面与侧栏角标同步到最新状态。
  useEffect(() => {
    refresh()
  }, [refresh])

  // 持续时长与「更新于」每 30 秒推进一次，与轮询节奏一致。
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const loadLogsAndPulse = useCallback(async () => {
    const current = ++logsRequest.current
    setLogsLoading(true)
    const toSec = Math.floor(Date.now() / 1000)
    const fromSec = toSec - 24 * 3600

    const [logsRes, pulseRes] = await Promise.allSettled([
      api.operationLogs({ limit: 20 }),
      api.statsTrafficSeries({ dim: 'fleet', granularity: '1h', from: fromSec, to: toSec }),
    ])

    if (current !== logsRequest.current) return
    if (logsRes.status === 'fulfilled') {
      setLogs(logsRes.value.items)
      setLogsError(null)
    } else {
      setLogsError(errorMessage(logsRes.reason))
    }
    if (pulseRes.status === 'fulfilled') {
      setPulseSeries(pulseRes.value)
    }
    setLogsLoading(false)
  }, [])

  useEffect(() => {
    void loadLogsAndPulse()
  }, [loadLogsAndPulse])

  const refreshAll = useCallback(() => {
    refresh()
    void loadLogsAndPulse()
    setNow(Date.now())
  }, [loadLogsAndPulse, refresh])

  const pending = counts.critical + counts.warning
  const today = useMemo(() => formatToday(now), [now])
  const onlineRatio =
    summary.serversTotal > 0
      ? Math.round((summary.serversOnline / summary.serversTotal) * 100)
      : null

  const filteredItems = useMemo(
    () =>
      severityFilter === 'all' ? items : items.filter((item) => item.severity === severityFilter),
    [items, severityFilter],
  )

  return (
    <div className="page-shell mx-auto flex w-full max-w-[1440px] flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-4 border-b border-rule pb-4">
        <div>
          <p className="flex items-center gap-2 text-[12px] font-semibold tracking-[0.14em] text-ink-3">
            <span className="size-1.5 rounded-full bg-signal" aria-hidden="true" />
            控制台 · LINE BOARD
          </p>
          <h1 className="mt-1 text-[30px] font-black leading-[36px] tracking-tight text-ink">
            {today}
          </h1>
          <p className="mt-1 text-[14px] leading-5 text-ink-2">
            更新于 {formatClock(summary.updatedAt)} · 每 30 秒自动巡检机群与线路状态
          </p>
        </div>
        <div className="flex items-center gap-2.5">
          <p
            aria-live="polite"
            className={cn(
              'inline-flex h-9 items-center gap-2 rounded-sm border px-3 text-[13.5px] font-semibold',
              counts.critical > 0
                ? 'border-failed/40 bg-failed/10 text-failed'
                : pending > 0
                  ? 'border-degraded/40 bg-degraded/10 text-degraded'
                  : 'border-ok/35 bg-ok/10 text-ok',
            )}
          >
            <span
              className={cn('size-2 rounded-full bg-current', pending > 0 && 'cg-beacon-live')}
              aria-hidden="true"
            />
            {pending > 0 ? `${pending} 项待处理` : '全网通行正常'}
          </p>
          <button
            type="button"
            onClick={refreshAll}
            disabled={loading}
            className={cn(
              'inline-flex h-9 items-center gap-2 rounded-sm border border-control bg-surface px-3 text-[13.5px] font-semibold text-ink transition-colors hover:bg-raised active:translate-y-px disabled:opacity-60',
              focusRing,
            )}
          >
            <RefreshCwIcon
              aria-hidden="true"
              className={cn('size-3.5', loading && 'animate-spin')}
            />
            刷新
          </button>
        </div>
      </header>

      {error ? (
        <div
          role="alert"
          className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-degraded/40 bg-degraded/10 px-4 py-3 text-[14px] text-ink"
        >
          <span className="flex items-center gap-2">
            <CircleAlertIcon aria-hidden="true" className="size-4 text-degraded" />
            队列刷新失败：{error}
          </span>
          <button
            type="button"
            onClick={refreshAll}
            className={cn(
              'rounded-sm border border-rule bg-surface px-2.5 py-1 font-medium text-ink-2 hover:bg-raised',
              focusRing,
            )}
          >
            重试
          </button>
        </div>
      ) : null}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_380px]">
        <section
          aria-labelledby="board-queue-title"
          className="flex flex-col overflow-hidden rounded-xl border border-rule bg-surface shadow-[var(--panel-shadow-card)]"
        >
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
            <div>
              <h2 id="board-queue-title" className="text-[17px] font-bold text-ink">
                待处理队列
              </h2>
              <p className="mt-0.5 text-[13px] text-ink-3">
                {counts.critical} 项故障 · {counts.warning} 项降级 · 按严重度与影响人数排序
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="cg-filter-pills" role="group" aria-label="按严重度筛选">
                <button
                  type="button"
                  aria-pressed={severityFilter === 'all'}
                  onClick={() => setSeverityFilter('all')}
                  className="cg-filter-pill"
                >
                  全部
                  <span className="cg-filter-count">{items.length}</span>
                </button>
                <button
                  type="button"
                  aria-pressed={severityFilter === 'critical'}
                  onClick={() => setSeverityFilter('critical')}
                  className="cg-filter-pill"
                >
                  故障
                  <span className="cg-filter-count">{counts.critical}</span>
                </button>
                <button
                  type="button"
                  aria-pressed={severityFilter === 'warning'}
                  onClick={() => setSeverityFilter('warning')}
                  className="cg-filter-pill"
                >
                  降级
                  <span className="cg-filter-count">{counts.warning}</span>
                </button>
              </div>
            </div>
          </div>

          {loading && items.length === 0 ? (
            <QueueSkeleton />
          ) : filteredItems.length === 0 ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-2.5 px-4 py-14 text-center">
              <span className="grid size-11 place-items-center rounded-full border border-ok/30 bg-ok/10 text-ok">
                <CheckCircle2Icon aria-hidden="true" className="size-5" />
              </span>
              <p className="text-[16px] font-bold text-ink">
                {severityFilter === 'all' ? '当前无待处理事项' : '该分类下无待处理事项'}
              </p>
              <p className="max-w-[40ch] text-[13.5px] leading-5 text-ink-3">
                最近一次检查于 {formatClock(summary.updatedAt)}
                ；所有受控服务器与链路状态均实时派生。
              </p>
              {severityFilter !== 'all' ? (
                <button
                  type="button"
                  onClick={() => setSeverityFilter('all')}
                  className="mt-1 rounded-sm border border-rule bg-raised px-3 py-1 text-xs font-semibold text-ink hover:border-rule-strong"
                >
                  显示全部待处理 ({items.length})
                </button>
              ) : (
                <Link
                  href="/run/logs/operations"
                  className={cn(
                    'mt-1 inline-flex items-center gap-1 text-[13.5px] font-semibold text-signal hover:underline',
                    focusRing,
                  )}
                >
                  查看最近操作日志
                  <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
                </Link>
              )}
            </div>
          ) : (
            <ul aria-label="待处理事项">
              {filteredItems.map((item, index) => (
                <QueueRow
                  key={item.id}
                  item={item}
                  index={index}
                  now={now}
                  expanded={expandedId === item.id}
                  onToggle={() =>
                    setExpandedId((current) => (current === item.id ? null : item.id))
                  }
                />
              ))}
            </ul>
          )}
        </section>

        <section
          aria-labelledby="board-pulse-title"
          className="flex flex-col gap-4 rounded-xl border border-rule bg-surface p-4 shadow-[var(--panel-shadow-card)]"
        >
          <div className="flex items-center justify-between">
            <div>
              <h2 id="board-pulse-title" className="text-[17px] font-bold text-ink">
                机群脉搏
              </h2>
              <p className="mt-0.5 text-[13px] text-ink-3">
                同步于 {formatClock(summary.updatedAt)}
              </p>
            </div>
            <Link
              href="/run/console"
              className={cn(
                'inline-flex items-center gap-1 text-xs font-semibold text-signal hover:underline',
                focusRing,
              )}
            >
              运行监控
              <ArrowUpRightIcon className="size-3.5" aria-hidden="true" />
            </Link>
          </div>

          <div className="grid grid-cols-2 gap-2.5">
            <Link
              href="/network/servers"
              className={cn(
                'group flex flex-col justify-between rounded-lg border border-rule bg-raised/50 p-3 transition-all hover:border-rule-strong hover:bg-raised',
                focusRing,
              )}
            >
              <span className="flex items-center justify-between text-[12px] font-semibold tracking-[0.06em] text-ink-3">
                <span>服务器在线</span>
                <ServerIcon className="size-3.5 text-ink-3 transition-colors group-hover:text-signal" />
              </span>
              <span className="mt-1.5 [font-family:var(--font-readout)] text-[28px] font-bold leading-7 text-ink tabular-nums">
                {summary.serversOnline}
                <span className="text-[16px] text-ink-3">/{summary.serversTotal}</span>
              </span>
            </Link>

            <Link
              href="/network/servers"
              className={cn(
                'group flex flex-col justify-between rounded-lg border border-rule bg-raised/50 p-3 transition-all hover:border-rule-strong hover:bg-raised',
                focusRing,
              )}
            >
              <span className="flex items-center justify-between text-[12px] font-semibold tracking-[0.06em] text-ink-3">
                <span>配置漂移</span>
                <TriangleAlertIcon
                  className={cn(
                    'size-3.5 transition-colors',
                    summary.serversDrift > 0
                      ? 'text-degraded'
                      : 'text-ink-3 group-hover:text-signal',
                  )}
                />
              </span>
              <span
                className={cn(
                  'mt-1.5 [font-family:var(--font-readout)] text-[28px] font-bold leading-7 tabular-nums',
                  summary.serversDrift > 0 ? 'text-degraded' : 'text-ink',
                )}
              >
                {summary.serversDrift}
              </span>
            </Link>

            <Link
              href="/network/chains"
              className={cn(
                'group flex flex-col justify-between rounded-lg border border-rule bg-raised/50 p-3 transition-all hover:border-rule-strong hover:bg-raised',
                focusRing,
              )}
            >
              <span className="flex items-center justify-between text-[12px] font-semibold tracking-[0.06em] text-ink-3">
                <span>链路通行</span>
                <WaypointsIcon className="size-3.5 text-ink-3 transition-colors group-hover:text-signal" />
              </span>
              <span className="mt-1.5 [font-family:var(--font-readout)] text-[28px] font-bold leading-7 text-ink tabular-nums">
                {summary.chainsHealthy}
                <span className="text-[16px] text-ink-3">/{summary.chainsTotal}</span>
              </span>
            </Link>

            <Link
              href="/access/users"
              className={cn(
                'group flex flex-col justify-between rounded-lg border border-rule bg-raised/50 p-3 transition-all hover:border-rule-strong hover:bg-raised',
                focusRing,
              )}
            >
              <span className="flex items-center justify-between text-[12px] font-semibold tracking-[0.06em] text-ink-3">
                <span>活跃用户</span>
                <UsersIcon className="size-3.5 text-ink-3 transition-colors group-hover:text-signal" />
              </span>
              <span className="mt-1.5 [font-family:var(--font-readout)] text-[28px] font-bold leading-7 text-ink tabular-nums">
                {summary.usersOnline}
                <span className="text-[16px] text-ink-3">/{summary.usersTotal}</span>
              </span>
            </Link>
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between text-[12.5px] font-medium text-ink-2">
              <span>机群在线率</span>
              <span className="font-mono font-semibold tabular-nums">
                {onlineRatio === null ? '暂无数据' : `${onlineRatio}%`}
              </span>
            </div>
            <div
              className="h-2 overflow-hidden rounded-full bg-inset"
              role="img"
              aria-label={
                onlineRatio === null
                  ? '暂无可统计的服务器'
                  : `${summary.serversTotal} 台服务器中 ${summary.serversOnline} 台在线`
              }
            >
              <div
                className={cn(
                  'h-full rounded-full transition-[width] duration-500',
                  onlineRatio !== null && onlineRatio < 60
                    ? 'bg-failed'
                    : onlineRatio !== null && onlineRatio < 100
                      ? 'bg-degraded'
                      : 'bg-ok',
                )}
                style={{ width: `${onlineRatio ?? 0}%` }}
              />
            </div>
          </div>

          <FleetPulseSparkline points={pulseSeries} />
        </section>
      </div>

      <section
        aria-labelledby="board-changes-title"
        className="overflow-hidden rounded-xl border border-rule bg-surface shadow-[var(--panel-shadow-card)]"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
          <div>
            <h2 id="board-changes-title" className="text-[17px] font-bold text-ink">
              最近变更
            </h2>
            <p className="mt-0.5 text-[13px] text-ink-3">过去 20 条操作审计记录，按时间倒序</p>
          </div>
          <Link
            href="/run/logs/operations"
            className={cn(
              'inline-flex items-center gap-1 text-[13.5px] font-semibold text-signal hover:underline',
              focusRing,
            )}
          >
            全部操作日志
            <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
          </Link>
        </div>

        {logsLoading && logs.length === 0 ? (
          <ul aria-hidden="true" className="divide-y divide-rule">
            {[0, 1, 2].map((row) => (
              <li key={row} className="flex min-h-11 items-center gap-3 px-4">
                <span className="h-2 w-2 animate-pulse rounded-full bg-raised" />
                <span className="h-3.5 w-56 animate-pulse rounded-sm bg-raised" />
              </li>
            ))}
          </ul>
        ) : logsError ? (
          <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-4 text-[14px] text-ink">
            <span className="flex items-center gap-2">
              <CircleAlertIcon aria-hidden="true" className="size-4 text-degraded" />
              操作日志加载失败：{logsError}
            </span>
            <button
              type="button"
              onClick={() => void loadLogsAndPulse()}
              className={cn(
                'rounded-sm border border-rule bg-surface px-2.5 py-1 font-medium text-ink-2 hover:bg-raised',
                focusRing,
              )}
            >
              重试
            </button>
          </div>
        ) : logs.length === 0 ? (
          <p className="px-4 py-8 text-center text-[14px] text-ink-3">最近没有操作记录。</p>
        ) : (
          <ul className="divide-y divide-rule">
            {logs.map((log) => (
              <li
                key={log.event_id}
                className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 text-[14px] transition-colors hover:bg-raised/60"
              >
                <span
                  aria-hidden="true"
                  className={cn('size-2 shrink-0 rounded-full', LOG_TONE[log.severity])}
                />
                <time
                  dateTime={log.timestamp}
                  className="w-20 shrink-0 font-mono text-[12.5px] tabular-nums text-ink-3"
                >
                  {formatClock(log.timestamp)}
                </time>
                <span className="shrink-0 rounded-xs border border-rule bg-raised px-1.5 py-0.5 text-[11px] font-semibold text-ink-2">
                  {CATEGORY_LABEL[log.category]}
                </span>
                <span className="min-w-0 flex-1 truncate font-medium text-ink">{log.action}</span>
                {log.server_id !== undefined ? (
                  <Link
                    href={`/network/servers?sel=${log.server_id}`}
                    className={cn('shrink-0 font-medium text-signal hover:underline', focusRing)}
                  >
                    {log.server || `服务器 #${log.server_id}`}
                  </Link>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
