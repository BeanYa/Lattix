import { useEffect, useRef, useState } from 'react'
import { CheckCircle2Icon, CircleIcon, Loader2Icon, XCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { PanelUpdateStage, PanelUpdateStatus } from '@/lib/types'

// 更新阶段展示顺序（与后端 updStage* 对应）；done/failed 为终态不在步骤条中。
const STAGES: { key: PanelUpdateStage; label: string }[] = [
  { key: 'check', label: '检查版本' },
  { key: 'download', label: '下载更新包' },
  { key: 'verify', label: '校验完整性' },
  { key: 'extract', label: '解压更新包' },
  { key: 'apply', label: '应用更新' },
  { key: 'restart', label: '重启面板' },
]

// UpdateOverlay 面板自更新全屏遮罩：轮询更新状态，更新进行中阻断一切页面操作，
// 并可视化各阶段进度（下载百分比等）；进入重启阶段后等待面板恢复并整页刷新。
// 挂载于 Layout（登录后所有页面生效），无更新时不渲染。
export default function UpdateOverlay() {
  const [status, setStatus] = useState<PanelUpdateStatus | null>(null)
  // 本会话内观察到的更新：running→终态（done/failed）时展示结果，直到用户关闭。
  const [result, setResult] = useState<PanelUpdateStatus | null>(null)
  const [reloadSeconds, setReloadSeconds] = useState(5)
  const wasRunning = useRef(false)
  const lastRunningStatus = useRef<PanelUpdateStatus | null>(null)

  useEffect(() => {
    let stopped = false
    let timer: ReturnType<typeof setTimeout>

    const finishSuccess = (completion: PanelUpdateStatus) => {
      wasRunning.current = false
      setStatus(null)
      setReloadSeconds(5)
      setResult({
        ...completion,
        running: false,
        stage: 'done',
        percent: 100,
        message: '更新完成，新版本已生效。',
      })
    }

    const finishInterrupted = (current: PanelUpdateStatus) => {
      const previous = lastRunningStatus.current
      wasRunning.current = false
      setStatus(null)
      setResult({
        ...(previous ?? current),
        running: false,
        stage: 'failed',
        message: '更新中断',
        error: `面板已恢复，但当前版本 ${current.current_version || '未知'} 与目标版本 ${previous?.target_version || '未知'} 不一致。`,
      })
    }

    const poll = async () => {
      try {
        const s = await api.panelUpdateStatus()
        if (stopped) return
        if (s.running) {
          wasRunning.current = true
          lastRunningStatus.current = s
          setStatus(s)
          if (s.stage === 'restart') {
            // 面板即将退出：停止状态轮询，改为等待面板恢复后整页刷新。
            waitComeback()
            return
          }
        } else if (wasRunning.current) {
          if (s.stage === 'done') {
            finishSuccess(s)
            return
          }
          if (s.stage === 'failed') {
            wasRunning.current = false
            setStatus(null)
            setResult(s)
            return
          }

          // 更新进程可能在两次轮询之间完成重启，新进程的内存状态为空。
          // 用新进程报告的当前版本收口，避免界面永久停在重启前的旧进度。
          const previous = lastRunningStatus.current
          if (previous?.target_version && s.current_version === previous.target_version) {
            finishSuccess(previous)
            return
          }

          finishInterrupted(s)
          return
        }
      } catch {
        // 面板重启窗口内请求失败属预期，继续轮询
      }
      if (!stopped) timer = setTimeout(poll, 300)
    }

    // 等待面板重启恢复：新进程版本就位后进入完成倒计时。
    const waitComeback = async () => {
      const deadline = Date.now() + 90_000
      while (!stopped && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 2000))
        try {
          await api.me()
          const recovered = await api.panelUpdateStatus()
          if (stopped) return
          const previous = lastRunningStatus.current
          if (previous?.target_version && recovered.current_version === previous.target_version) {
            finishSuccess(previous)
            return
          }
          // 重启请求已发出但旧进程尚未退出，继续等待。
          if (recovered.running && recovered.stage === 'restart') continue
          finishInterrupted(recovered)
          return
        } catch {
          // 尚未恢复，继续等
        }
      }
      if (!stopped) {
        setStatus((s) =>
          s ? { ...s, message: '等待面板恢复超时，请稍后手动刷新页面。' } : s,
        )
      }
    }

    poll()
    return () => {
      stopped = true
      clearTimeout(timer)
    }
  }, [])

  const updateDone = result?.stage === 'done'
  useEffect(() => {
    if (!updateDone) return
    let seconds = 5
    const timer = window.setInterval(() => {
      seconds -= 1
      if (seconds <= 0) {
        window.clearInterval(timer)
        window.location.reload()
        return
      }
      setReloadSeconds(seconds)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [updateDone])

  const active = status ?? result
  if (!active || (!active.running && !result)) return null

  const failed = active.stage === 'failed'
  const done = active.stage === 'done'
  const currentIdx = STAGES.findIndex((s) => s.key === active.stage)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="w-full max-w-md rounded-xl border bg-card p-6 shadow-lg">
        <div className="mb-1 flex items-center gap-2">
          {failed ? (
            <XCircleIcon className="size-5 text-destructive" />
          ) : done ? (
            <CheckCircle2Icon className="size-5 text-green-600" />
          ) : (
            <Loader2Icon className="size-5 animate-spin text-primary" />
          )}
          <h2 className="text-lg font-semibold">
            {failed ? '面板更新失败' : done ? '更新完成' : '面板更新中'}
          </h2>
        </div>
        {active.target_version && (
          <p className="mb-4 text-sm text-muted-foreground">
            {active.current_version} → {active.target_version}
          </p>
        )}

        {!failed && !done && (
          <>
            <div className="mb-2 h-2 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{ width: `${Math.min(100, Math.max(0, active.percent))}%` }}
              />
            </div>
            <div className="mb-4 flex items-center justify-between text-xs text-muted-foreground">
              <span>{active.message}</span>
              <span>{active.percent}%</span>
            </div>
            <ul className="space-y-1.5">
              {STAGES.map((s, i) => (
                <li key={s.key} className="flex items-center gap-2 text-sm">
                  {i < currentIdx ? (
                    <CheckCircle2Icon className="size-4 text-green-600" />
                  ) : i === currentIdx ? (
                    <Loader2Icon className="size-4 animate-spin text-primary" />
                  ) : (
                    <CircleIcon className="size-4 text-muted-foreground/40" />
                  )}
                  <span className={i > currentIdx ? 'text-muted-foreground/60' : ''}>
                    {s.label}
                  </span>
                </li>
              ))}
            </ul>
            <p className="mt-4 text-xs text-muted-foreground">
              更新期间面板操作已被锁定，请勿关闭本页面。
            </p>
          </>
        )}

        {failed && (
          <>
            <p className="mb-4 text-sm text-destructive">{active.error || '未知错误'}</p>
            <p className="mb-4 text-xs text-muted-foreground">
              面板仍运行旧版本，可重试更新或查看日志排查。
            </p>
            <Button variant="outline" size="sm" onClick={() => setResult(null)}>
              关闭
            </Button>
          </>
        )}

        {done && (
          <>
            <p className="mb-2 text-sm text-muted-foreground">{active.message}</p>
            <p className="mb-4 text-xs text-muted-foreground">
              {reloadSeconds} 秒后将自动刷新页面。
            </p>
            <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
              立即刷新
            </Button>
          </>
        )}
      </div>
    </div>
  )
}
