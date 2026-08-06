import { CheckCircle2Icon, CircleIcon, Loader2Icon, XCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import type { ProgressState } from '@/lib/operation-progress-state'

const LOST_MESSAGE = '进度已丢失，操作可能仍在后台继续'

export default function OperationProgress({
  state,
  onClose,
}: {
  state: ProgressState
  onClose: () => void
}) {
  if (state.phase === 'idle') return null

  const running = state.phase === 'running'
  const done = state.phase === 'done'
  const failed = state.phase === 'failed'
  const lost = state.phase === 'lost'
  const autoClose = done && state.autoClose
  const observation = running || done || failed ? state.observation : null
  const fallbackTitle = failed ? '操作失败' : done ? '操作完成' : lost ? '操作进度' : '操作进行中'
  const title = observation?.title ?? fallbackTitle
  const stages = observation?.stages ?? []
  const currentIdx = observation ? stages.findIndex((s) => s.key === observation.stage) : -1
  const warnings = observation?.warnings ?? []
  const percent = observation?.percent ?? 0
  const message = observation?.message ?? '正在获取进度…'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="w-full max-w-md rounded-xl border bg-card p-6 shadow-lg">
        <div className="mb-1 flex items-center gap-2">
          {failed || lost ? (
            <XCircleIcon className="size-5 text-destructive" />
          ) : done ? (
            <CheckCircle2Icon className="size-5 text-success" />
          ) : (
            <Loader2Icon className="size-5 animate-spin text-primary" />
          )}
          <h2 className="text-lg font-semibold">{title}</h2>
        </div>

        {running && (
          <>
            <div className="mb-2 h-2 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
              />
            </div>
            <div className="mb-4 flex items-center justify-between text-xs text-muted-foreground">
              <span>{message}</span>
              <span>{percent}%</span>
            </div>
          </>
        )}

        {stages.length > 0 && (
          <ul className="space-y-1.5">
            {stages.map((s, i) => {
              const icon = done ? (
                <CheckCircle2Icon className="size-4 text-success" />
              ) : failed && i === currentIdx ? (
                <XCircleIcon className="size-4 text-destructive" />
              ) : i < currentIdx ? (
                <CheckCircle2Icon className="size-4 text-success" />
              ) : i === currentIdx ? (
                <Loader2Icon className="size-4 animate-spin text-primary" />
              ) : (
                <CircleIcon className="size-4 text-muted-foreground/40" />
              )
              return (
                <li key={s.key} className="flex items-center gap-2 text-sm">
                  {icon}
                  <span className={i > currentIdx && !done ? 'text-muted-foreground/60' : ''}>
                    {s.label}
                  </span>
                </li>
              )
            })}
          </ul>
        )}

        {done && (
          <>
            <p className="mt-4 text-sm text-muted-foreground">{message}</p>
            {warnings.length > 0 && (
              <ul className="mt-2 space-y-1 rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning">
                {warnings.map((warning, i) => (
                  <li key={i} className="flex items-start gap-1.5">
                    <span aria-hidden="true">•</span>
                    <span>{warning}</span>
                  </li>
                ))}
              </ul>
            )}
            <div className="mt-4 flex items-center justify-between">
              {autoClose && <span className="text-xs text-muted-foreground">1 秒后自动关闭</span>}
              <Button variant="outline" size="sm" onClick={onClose} className={autoClose ? '' : 'ml-auto'}>
                关闭
              </Button>
            </div>
          </>
        )}

        {failed && (
          <>
            <p className="mb-4 mt-4 text-sm text-destructive">{observation?.error || '未知错误'}</p>
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={onClose}>
                关闭
              </Button>
            </div>
          </>
        )}

        {lost && (
          <>
            <p className="mb-4 mt-4 text-sm text-muted-foreground">{LOST_MESSAGE}</p>
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={onClose}>
                关闭
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
