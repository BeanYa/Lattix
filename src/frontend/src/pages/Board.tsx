import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowUpRightIcon,
  ChevronDownIcon,
  CircleAlertIcon,
  Clock3Icon,
  RefreshCwIcon,
  ServerIcon,
  TriangleAlertIcon,
  WaypointsIcon,
} from 'lucide-react'
import { Link } from 'wouter'

import { useBoardQueue, type BoardItem, type BoardSeverity } from '@/lib/board-queue'
import { api, errorMessage } from '@/lib/api'
import type { LogSeverity, OperationCategory, OperationLogEntry } from '@/lib/types'
import { cn } from '@/lib/utils'

/** 与 AppShell 一致的焦点环，颜色跟随 tokens.css 的 --panel-focus。 */
const focusRing =
  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]'

/** 严重度的三重编码：颜色 + 图标 + 文字，任何一项都不能单独承担语义。 */
const SEVERITY: Record<
  BoardSeverity,
  { label: string; text: string; chip: string; icon: typeof TriangleAlertIcon }
> = {
  critical: {
    label: '故障',
    text: 'text-failed',
    chip: 'border-failed/40 bg-failed/10 text-failed',
    icon: TriangleAlertIcon,
  },
  warning: {
    label: '降级',
    text: 'text-degraded',
    chip: 'border-degraded/40 bg-degraded/10 text-degraded',
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

/** 队列行：整行可展开，展开区承载「为什么 / 影响 / 动作」。 */
function QueueRow({
  item,
  now,
  expanded,
  onToggle,
}: {
  item: BoardItem
  now: number
  expanded: boolean
  onToggle: () => void
}) {
  const severity = SEVERITY[item.severity]
  const SeverityIcon = severity.icon
  const panelId = `board-item-${item.id}`

  return (
    <li className="border-b border-rule last:border-b-0">
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={onToggle}
        className={cn(
          'grid min-h-14 w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2 text-left transition-colors hover:bg-raised md:grid-cols-[auto_minmax(0,1.5fr)_minmax(0,1.1fr)_auto_auto] lg:grid-cols-[auto_minmax(0,1.5fr)_minmax(0,1.1fr)_auto_auto_auto]',
          focusRing,
        )}
      >
        <span
          className={cn(
            'inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-[12px] font-semibold',
            severity.chip,
          )}
        >
          <SeverityIcon aria-hidden="true" className="size-3.5" />
          {severity.label}
        </span>

        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex min-w-0 items-center gap-2">
            {item.kind === 'server' ? (
              <ServerIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
            ) : (
              <WaypointsIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
            )}
            <span className="truncate text-[15px] font-medium text-ink">{item.title}</span>
            <span className="shrink-0 rounded border border-rule px-1.5 text-[11px] font-semibold text-ink-3">
              {item.kind === 'server' ? '服务器' : '链路'}
            </span>
          </span>
          <span className="truncate text-[13px] text-ink-3 md:hidden">{item.reason}</span>
        </span>

        <span className="hidden min-w-0 truncate text-[13px] text-ink-2 md:block">
          {item.reason}
        </span>
        <span className="hidden whitespace-nowrap text-[13px] text-ink-3 md:block">
          {item.impact ?? '影响面未知'}
        </span>
        <span className="hidden items-center gap-1 whitespace-nowrap text-[13px] text-ink-3 sm:flex">
          <Clock3Icon aria-hidden="true" className="size-3.5" />
          {formatDuration(item.since, now)}
        </span>

        <ChevronDownIcon
          aria-hidden="true"
          className={cn(
            'size-4 shrink-0 text-ink-3 transition-transform',
            expanded && 'rotate-180',
          )}
        />
      </button>

      {expanded ? (
        <div id={panelId} className="border-t border-rule bg-inset/50 px-4 py-3">
          <dl className="grid gap-3 text-[13px] sm:grid-cols-[120px_minmax(0,1fr)]">
            <dt className="font-semibold text-ink-2">为什么</dt>
            <dd className="text-ink">{item.reason}</dd>
            <dt className="font-semibold text-ink-2">影响</dt>
            <dd className="text-ink">{item.impact ?? '影响面未知（尚未计算受影响用户与订阅）'}</dd>
            <dt className="font-semibold text-ink-2">持续</dt>
            <dd className="text-ink">
              {formatDuration(item.since, now)}
              {item.since ? ` · 起于 ${formatClock(item.since)}` : ' · 缺少可靠的开始时间'}
            </dd>
          </dl>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {(item.actions ?? []).map((action) => (
              <Link
                key={action.href + action.label}
                href={action.href}
                className={cn(
                  'inline-flex min-h-8 items-center gap-1 rounded-md border px-3 text-[13px] font-medium',
                  action.tone === 'primary'
                    ? 'border-signal bg-signal text-signal-ink hover:bg-signal/90'
                    : 'border-rule text-ink-2 hover:bg-raised',
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
          <span className="h-5 w-12 animate-pulse rounded border border-rule bg-raised" />
          <span className="h-3.5 w-40 animate-pulse rounded bg-raised" />
          <span className="ml-auto h-3.5 w-24 animate-pulse rounded bg-raised" />
        </li>
      ))}
    </ul>
  )
}

export default function Board() {
  const { items, counts, summary, loading, error, refresh } = useBoardQueue()
  const [now, setNow] = useState(() => Date.now())
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [logs, setLogs] = useState<OperationLogEntry[]>([])
  const [logsLoading, setLogsLoading] = useState(true)
  const [logsError, setLogsError] = useState<string | null>(null)
  const logsRequest = useRef(0)

  // 持续时长与「更新于」每 30 秒推进一次，与轮询节奏一致。
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const loadLogs = useCallback(async () => {
    const current = ++logsRequest.current
    setLogsLoading(true)
    try {
      const page = await api.operationLogs({ limit: 20 })
      if (current !== logsRequest.current) return
      setLogs(page.items)
      setLogsError(null)
    } catch (cause) {
      if (current !== logsRequest.current) return
      setLogsError(errorMessage(cause))
    } finally {
      if (current === logsRequest.current) setLogsLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadLogs()
  }, [loadLogs])

  const refreshAll = useCallback(() => {
    refresh()
    void loadLogs()
    setNow(Date.now())
  }, [loadLogs, refresh])

  const pending = counts.critical + counts.warning
  const today = useMemo(() => formatToday(now), [now])
  const onlineRatio =
    summary.serversTotal > 0
      ? Math.round((summary.serversOnline / summary.serversTotal) * 100)
      : null

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p className="text-[12px] font-semibold tracking-[0.14em] text-ink-3">
            值班板 · LINE BOARD
          </p>
          <h1 className="mt-1 text-[30px] font-black leading-[34px] tracking-tight text-ink">
            {today}
          </h1>
          <p className="mt-1 text-[14px] leading-5 text-ink-2">
            更新于 {formatClock(summary.updatedAt)} · 每 30 秒自动刷新
          </p>
        </div>
        <div className="flex items-center gap-3">
          <p
            aria-live="polite"
            className={cn(
              'rounded-md border px-3 py-1.5 text-[13px] font-semibold',
              pending > 0
                ? 'border-failed/40 bg-failed/10 text-failed'
                : 'border-rule bg-surface text-ink-2',
            )}
          >
            {pending > 0 ? `${pending} 项待处理` : '当前无待处理事项'}
          </p>
          <button
            type="button"
            onClick={refreshAll}
            disabled={loading}
            className={cn(
              'inline-flex min-h-9 items-center gap-2 rounded-md border border-rule bg-surface px-3 text-[13px] font-medium text-ink-2 transition-colors hover:bg-raised disabled:opacity-60',
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
          className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-degraded/40 bg-degraded/10 px-4 py-3 text-[13px] text-ink"
        >
          <span className="flex items-center gap-2">
            <CircleAlertIcon aria-hidden="true" className="size-4 text-degraded" />
            队列刷新失败：{error}
          </span>
          <button
            type="button"
            onClick={refreshAll}
            className={cn(
              'rounded-md border border-rule bg-surface px-2.5 py-1 font-medium text-ink-2 hover:bg-raised',
              focusRing,
            )}
          >
            重试
          </button>
        </div>
      ) : null}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_360px]">
        <section
          aria-labelledby="board-queue-title"
          className="overflow-hidden rounded-xl border border-rule bg-surface"
        >
          <div className="flex items-center justify-between gap-3 border-b border-rule px-4 py-3">
            <div>
              <h2 id="board-queue-title" className="text-[16px] font-semibold text-ink">
                待处理
              </h2>
              <p className="mt-0.5 text-[13px] text-ink-3">
                {counts.critical} 项故障 · {counts.warning} 项降级 · 按影响与时间排序
              </p>
            </div>
            <Link
              href="/run/logs/operations"
              className={cn(
                'inline-flex items-center gap-1 text-[13px] font-medium text-signal hover:underline',
                focusRing,
              )}
            >
              查看操作日志
              <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
            </Link>
          </div>

          {loading && items.length === 0 ? (
            <QueueSkeleton />
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center gap-2 px-4 py-12 text-center">
              <CircleAlertIcon aria-hidden="true" className="size-6 text-ok" />
              <p className="text-[15px] font-medium text-ink">当前无待处理事项</p>
              <p className="max-w-[38ch] text-[13px] leading-5 text-ink-3">
                最近一次检查于 {formatClock(summary.updatedAt)}
                ；服务器与链路状态每次刷新都会重新派生。
              </p>
              <Link
                href="/run/logs/operations"
                className={cn(
                  'mt-1 text-[13px] font-medium text-signal hover:underline',
                  focusRing,
                )}
              >
                查看最近操作
              </Link>
            </div>
          ) : (
            <ul aria-label="待处理事项">
              {items.map((item) => (
                <QueueRow
                  key={item.id}
                  item={item}
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
          className="flex flex-col gap-4 rounded-xl border border-rule bg-surface p-4"
        >
          <div>
            <h2 id="board-pulse-title" className="text-[16px] font-semibold text-ink">
              机群脉搏
            </h2>
            <p className="mt-0.5 text-[13px] text-ink-3">同步于 {formatClock(summary.updatedAt)}</p>
          </div>

          <dl className="grid grid-cols-2 gap-4">
            <div>
              <dt className="text-[12px] font-semibold tracking-[0.08em] text-ink-3">服务器在线</dt>
              <dd className="mt-1 [font-family:var(--font-readout)] text-[26px] leading-7 text-ink">
                {summary.serversOnline}
                <span className="text-[15px] text-ink-3">/{summary.serversTotal}</span>
              </dd>
            </div>
            <div>
              <dt className="text-[12px] font-semibold tracking-[0.08em] text-ink-3">配置漂移</dt>
              <dd
                className={cn(
                  'mt-1 [font-family:var(--font-readout)] text-[26px] leading-7',
                  summary.serversDrift > 0 ? 'text-degraded' : 'text-ink',
                )}
              >
                {summary.serversDrift}
              </dd>
            </div>
            <div>
              <dt className="text-[12px] font-semibold tracking-[0.08em] text-ink-3">链路正常</dt>
              <dd className="mt-1 [font-family:var(--font-readout)] text-[26px] leading-7 text-ink">
                {summary.chainsHealthy}
                <span className="text-[15px] text-ink-3">/{summary.chainsTotal}</span>
              </dd>
            </div>
            <div>
              <dt className="text-[12px] font-semibold tracking-[0.08em] text-ink-3">待处理</dt>
              <dd className="mt-1 [font-family:var(--font-readout)] text-[26px] leading-7 text-ink">
                {pending}
              </dd>
            </div>
          </dl>

          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between text-[12px] text-ink-3">
              <span>在线比例</span>
              <span>{onlineRatio === null ? '暂无数据' : `${onlineRatio}%`}</span>
            </div>
            <div
              className="h-1.5 overflow-hidden rounded-full bg-inset"
              role="img"
              aria-label={
                onlineRatio === null
                  ? '暂无可统计的服务器'
                  : `${summary.serversTotal} 台服务器中 ${summary.serversOnline} 台在线`
              }
            >
              <div
                className="h-full rounded-full bg-ok transition-[width] duration-300"
                style={{ width: `${onlineRatio ?? 0}%` }}
              />
            </div>
            {summary.serversTotal === 0 ? (
              <p className="text-[13px] text-ink-3">
                还没有服务器；添加服务器后这里会显示在线比例。
              </p>
            ) : null}
          </div>
        </section>
      </div>

      <section
        aria-labelledby="board-changes-title"
        className="overflow-hidden rounded-xl border border-rule bg-surface"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
          <div>
            <h2 id="board-changes-title" className="text-[16px] font-semibold text-ink">
              最近变更
            </h2>
            <p className="mt-0.5 text-[13px] text-ink-3">过去 20 条操作日志，按时间倒序</p>
          </div>
          <Link
            href="/run/logs/operations"
            className={cn(
              'inline-flex items-center gap-1 text-[13px] font-medium text-signal hover:underline',
              focusRing,
            )}
          >
            全部日志
            <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
          </Link>
        </div>

        {logsLoading && logs.length === 0 ? (
          <ul aria-hidden="true" className="divide-y divide-rule">
            {[0, 1, 2].map((row) => (
              <li key={row} className="flex min-h-11 items-center gap-3 px-4">
                <span className="h-2 w-2 animate-pulse rounded-full bg-raised" />
                <span className="h-3.5 w-56 animate-pulse rounded bg-raised" />
              </li>
            ))}
          </ul>
        ) : logsError ? (
          <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-4 text-[13px] text-ink">
            <span className="flex items-center gap-2">
              <CircleAlertIcon aria-hidden="true" className="size-4 text-degraded" />
              操作日志加载失败：{logsError}
            </span>
            <button
              type="button"
              onClick={() => void loadLogs()}
              className={cn(
                'rounded-md border border-rule bg-surface px-2.5 py-1 font-medium text-ink-2 hover:bg-raised',
                focusRing,
              )}
            >
              重试
            </button>
          </div>
        ) : logs.length === 0 ? (
          <p className="px-4 py-8 text-center text-[13px] text-ink-3">最近没有操作记录。</p>
        ) : (
          <ul className="divide-y divide-rule">
            {logs.map((log) => (
              <li
                key={log.event_id}
                className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 text-[13px]"
              >
                <span
                  aria-hidden="true"
                  className={cn('size-1.5 shrink-0 rounded-full', LOG_TONE[log.severity])}
                />
                <time dateTime={log.timestamp} className="w-20 shrink-0 tabular-nums text-ink-3">
                  {formatClock(log.timestamp)}
                </time>
                <span className="shrink-0 rounded border border-rule px-1.5 text-[11px] font-semibold text-ink-3">
                  {CATEGORY_LABEL[log.category]}
                </span>
                <span className="min-w-0 flex-1 truncate text-ink">{log.action}</span>
                {log.server_id !== undefined ? (
                  <Link
                    href={`/network/servers?sel=${log.server_id}`}
                    className={cn('shrink-0 text-signal hover:underline', focusRing)}
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
