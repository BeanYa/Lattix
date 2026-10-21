import { useState } from 'react'
import {
  CheckCircle2Icon,
  ChevronDownIcon,
  ChevronUpIcon,
  CircleIcon,
  ExternalLinkIcon,
  Loader2Icon,
  XCircleIcon,
  XIcon,
} from 'lucide-react'
import { Link } from 'wouter'

import type { ProgressState } from '@/lib/operation-progress-state'
import { cn } from '@/lib/utils'

const LOST_MESSAGE = '进度已丢失，操作可能仍在后台继续'
const focusRing =
  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]'

const PHASE_LABEL: Record<Exclude<ProgressState['phase'], 'idle'>, string> = {
  running: '进行中',
  done: '已完成',
  failed: '失败',
  lost: '进度丢失',
}

/**
 * 非阻塞操作托盘（方案 4.4 / 9.4 的 P1 版本）。
 *
 * 与旧 OperationProgress 的区别：不再铺满全屏、不抢焦点、不阻止页面滚动；
 * 终态保留到用户手动关闭，切页不丢上下文。P1 只跟踪单个 observeId，与现有
 * 单任务状态机一致；多卡堆叠属于 P5。
 */
export default function OperationTray({
  state,
  onClose,
}: {
  state: ProgressState
  onClose: () => void
}) {
  const [collapsed, setCollapsed] = useState(false)
  if (state.phase === 'idle') return null

  const phase = state.phase
  const observation = phase === 'lost' ? null : state.observation
  const running = phase === 'running'
  const done = phase === 'done'
  const failed = phase === 'failed'
  const lost = phase === 'lost'
  const stages = observation?.stages ?? []
  const currentIdx = observation ? stages.findIndex((stage) => stage.key === observation.stage) : -1
  const warnings = observation?.warnings ?? []
  const percent = Math.min(100, Math.max(0, observation?.percent ?? 0))
  const title =
    observation?.title ??
    (failed ? '操作失败' : done ? '操作完成' : lost ? '操作进度' : '操作进行中')
  const message = observation?.message ?? (running ? '正在获取进度…' : '')

  const statusTone = failed || lost ? 'text-failed' : done ? 'text-ok' : 'text-signal'
  const StatusIcon = failed || lost ? XCircleIcon : done ? CheckCircle2Icon : Loader2Icon

  return (
    <aside
      aria-label="操作进度"
      className="fixed right-4 bottom-4 z-40 w-[360px] max-w-[calc(100vw-32px)] overflow-hidden rounded-xl border border-rule bg-surface text-ink shadow-[var(--panel-shadow-overlay)] sm:right-6 sm:bottom-6"
    >
      <div className="flex items-center gap-2 border-b border-rule px-3 py-2">
        <StatusIcon
          aria-hidden="true"
          className={cn('size-4 shrink-0', statusTone, running && 'animate-spin')}
        />
        <p className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{title}</p>
        <span className={cn('shrink-0 text-[11px] font-semibold', statusTone)}>
          {PHASE_LABEL[phase]}
        </span>
        <button
          type="button"
          onClick={() => setCollapsed((value) => !value)}
          aria-expanded={!collapsed}
          aria-label={collapsed ? '展开操作详情' : '收起操作详情'}
          className={cn(
            'grid size-7 place-items-center rounded-md text-ink-3 transition-colors hover:bg-raised hover:text-ink',
            focusRing,
          )}
        >
          {collapsed ? (
            <ChevronUpIcon className="size-4" aria-hidden="true" />
          ) : (
            <ChevronDownIcon className="size-4" aria-hidden="true" />
          )}
        </button>
        <button
          type="button"
          onClick={onClose}
          aria-label="关闭操作进度"
          className={cn(
            'grid size-7 place-items-center rounded-md text-ink-3 transition-colors hover:bg-raised hover:text-ink',
            focusRing,
          )}
        >
          <XIcon className="size-4" aria-hidden="true" />
        </button>
      </div>

      <div aria-live="polite" className={cn('px-3 py-2.5', collapsed && 'hidden')}>
        {running ? (
          <div className="flex flex-col gap-1.5">
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-inset">
              <div
                className="h-full rounded-full bg-signal transition-[width] duration-300"
                style={{ width: `${percent}%` }}
              />
            </div>
            <div className="flex items-center justify-between text-[11px] text-ink-3">
              <span className="min-w-0 truncate">{message}</span>
              <span className="shrink-0 tabular-nums">{percent}%</span>
            </div>
          </div>
        ) : null}

        {stages.length > 0 ? (
          <ul className="mt-2 max-h-36 space-y-1 overflow-y-auto">
            {stages.map((stage, index) => {
              const icon = done ? (
                <CheckCircle2Icon className="size-3.5 text-ok" aria-hidden="true" />
              ) : failed && index === currentIdx ? (
                <XCircleIcon className="size-3.5 text-failed" aria-hidden="true" />
              ) : index < currentIdx ? (
                <CheckCircle2Icon className="size-3.5 text-ok" aria-hidden="true" />
              ) : index === currentIdx ? (
                <Loader2Icon className="size-3.5 animate-spin text-signal" aria-hidden="true" />
              ) : (
                <CircleIcon className="size-3.5 text-ink-3/50" aria-hidden="true" />
              )
              return (
                <li key={stage.key} className="flex items-center gap-2 text-[12px]">
                  {icon}
                  <span
                    className={cn(
                      'truncate',
                      index > currentIdx && !done ? 'text-ink-3' : 'text-ink-2',
                    )}
                  >
                    {stage.label}
                  </span>
                </li>
              )
            })}
          </ul>
        ) : null}

        {done ? (
          <div className="mt-2 flex flex-col gap-2">
            {message ? <p className="text-[12px] text-ink-2">{message}</p> : null}
            {warnings.length > 0 ? (
              <ul className="max-h-28 space-y-1 overflow-y-auto rounded-md border border-degraded/40 bg-degraded/10 p-2 text-[11px] text-ink">
                {warnings.map((warning) => (
                  <li key={warning} className="flex items-start gap-1.5">
                    <span aria-hidden="true">•</span>
                    <span>{warning}</span>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}

        {failed ? (
          <p className="mt-2 text-[12px] text-failed">{observation?.error || '未知错误'}</p>
        ) : null}

        {lost ? <p className="mt-2 text-[12px] text-ink-2">{LOST_MESSAGE}</p> : null}

        {done || failed || lost ? (
          <div className="mt-3 flex items-center justify-between gap-2 border-t border-rule pt-2">
            <Link
              href="/run/logs/operations"
              onClick={onClose}
              className={cn(
                'inline-flex items-center gap-1 text-[11px] font-medium text-signal hover:underline',
                focusRing,
              )}
            >
              查看操作日志
              <ExternalLinkIcon className="size-3" aria-hidden="true" />
            </Link>
            <button
              type="button"
              onClick={onClose}
              className={cn(
                'h-7 rounded-md border border-rule bg-raised px-2.5 text-[11px] font-semibold text-ink-2 transition-colors hover:bg-inset hover:text-ink',
                focusRing,
              )}
            >
              关闭
            </button>
          </div>
        ) : null}
      </div>
    </aside>
  )
}
