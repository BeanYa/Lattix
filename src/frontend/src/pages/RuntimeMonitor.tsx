import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import {
  ActivityIcon,
  BracesIcon,
  Clock3Icon,
  CpuIcon,
  DatabaseIcon,
  HardDriveIcon,
  ListChecksIcon,
  MemoryStickIcon,
  PauseIcon,
  PlayIcon,
  RefreshCwIcon,
  ServerIcon,
  TimerResetIcon,
  WaypointsIcon,
} from 'lucide-react'

import { Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api, errorMessage } from '@/lib/api'
import { humanizeBytes } from '@/lib/format'
import type { PanelLifecycleState, PanelRuntimeSnapshot, ScheduledTaskRuntime } from '@/lib/types'
import { cn } from '@/lib/utils'

import AgentsStatsTab from './runtime/AgentsStatsTab'

import './runtime-monitor.css'

const MAX_SAMPLES = 36

const stateLabel: Record<PanelLifecycleState, string> = {
  startup: '启动中',
  active: '运行正常',
  updating: '正在更新',
  faulted: '运行故障',
}

const taskLabels: Record<string, string> = {
  'billing.lifecycle': '账单生命周期',
  'cdn.catalog.refresh': 'CDN 目录刷新',
  'exchange_rates.refresh': '汇率刷新',
  'external_subscriptions.sync': '外部订阅同步',
  'metrics.retention': '指标数据清理',
  'release.agent': 'Agent 版本巡检',
  'release.xray': 'Xray 版本巡检',
  'subscription.templates.refresh': '订阅模板刷新',
  'traffic.reset': '流量周期重置',
  'traffic_stats.retention': '流量统计清理',
  'user.expiry': '用户到期巡检',
}

function percent(value: number, total: number): number {
  if (total <= 0) return 0
  return Math.min(100, Math.max(0, (value / total) * 100))
}

function formatPercent(value: number | null): string {
  return value === null ? '采样中' : `${value.toFixed(value >= 10 ? 0 : 1)}%`
}

function formatDuration(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  if (minutes > 0) return `${minutes} 分钟`
  return `${Math.max(0, Math.floor(seconds))} 秒`
}

function formatTaskTime(value?: string): string {
  if (!value) return '--'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}

function metricTone(value: number, warning = 75, critical = 90): 'normal' | 'warning' | 'critical' {
  if (value >= critical) return 'critical'
  if (value >= warning) return 'warning'
  return 'normal'
}

function panelStateTone(state: PanelLifecycleState): 'is-ok' | 'is-warn' | 'is-red' {
  if (state === 'active') return 'is-ok'
  if (state === 'faulted') return 'is-red'
  return 'is-warn'
}

/** 模块头：红色索引标签 + 中文标题 + 右侧 meta / 操作。 */
function ModuleHead({
  tab,
  title,
  id,
  meta,
  children,
}: {
  tab: string
  title: string
  id?: string
  meta?: ReactNode
  children?: ReactNode
}) {
  return (
    <header className="rt-mod-head">
      <span className="rt-tab">{tab}</span>
      <h2 className="rt-mod-title" id={id}>
        {title}
      </h2>
      {meta ? <span className="rt-mod-meta">{meta}</span> : null}
      {children ? <div className="rt-head-actions">{children}</div> : null}
    </header>
  )
}

function MetricTile({
  icon,
  label,
  value,
  detail,
  progress,
}: {
  icon: ReactNode
  label: string
  value: string
  detail: string
  progress: number
}) {
  const tone = metricTone(progress)
  return (
    <article className="rt-metric" data-tone={tone}>
      <span className="rt-metric-label">
        {icon}
        {label}
      </span>
      <div className="rt-metric-row">
        <strong className="rt-metric-value">{value}</strong>
        <span className="rt-metric-detail">{detail}</span>
      </div>
      <div
        className="rt-meter"
        role="progressbar"
        aria-label={`${label} ${value}`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(progress)}
      >
        <i style={{ '--meter-value': `${progress}%` } as CSSProperties} />
      </div>
    </article>
  )
}

function TrendStrip({
  label,
  values,
  formatter,
}: {
  label: string
  values: Array<number | null>
  formatter: (value: number | null) => string
}) {
  const padded = [
    ...Array.from<null>({ length: Math.max(0, MAX_SAMPLES - values.length) }).fill(null),
    ...values,
  ].slice(-MAX_SAMPLES)
  return (
    <figure className="rt-trend">
      <figcaption>
        <span>{label}</span>
        <strong>{formatter(values.at(-1) ?? null)}</strong>
      </figcaption>
      <div className="rt-trend-bars" aria-label={`${label}最近 ${values.length} 次采样`}>
        {padded.map((value, index) => (
          <i
            key={`${index}-${value ?? 'empty'}`}
            className={cn(value === null && 'is-empty')}
            title={value === null ? '暂无采样' : formatter(value)}
            style={
              {
                '--sample-value': `${value === null ? 4 : Math.max(4, Math.min(100, value))}%`,
              } as CSSProperties
            }
          />
        ))}
      </div>
    </figure>
  )
}

function ProcessMetric({
  label,
  value,
  detail,
  progress,
}: {
  label: string
  value: string
  detail: string
  progress: number
}) {
  return (
    <div className="rt-process-metric">
      <div className="rt-process-top">
        <span>{label}</span>
        <strong>{value}</strong>
      </div>
      <div className="rt-meter">
        <i style={{ '--meter-value': `${progress}%` } as CSSProperties} />
      </div>
      <small>{detail}</small>
    </div>
  )
}

function TaskRow({ task }: { task: ScheduledTaskRuntime }) {
  const failed = Boolean(task.last_error)
  const status = task.running ? '运行中' : failed ? '上次失败' : task.runs > 0 ? '正常' : '等待执行'
  const statusTone = task.running
    ? 'is-ok'
    : failed
      ? 'is-red'
      : task.runs > 0
        ? 'is-ok'
        : 'is-muted'
  return (
    <>
      <tr
        className="rt-task-row"
        data-status={task.running ? 'running' : failed ? 'failed' : 'idle'}
      >
        <td className="rt-task-name">
          <span className="rt-task-dot" aria-hidden="true" />
          <span className="rt-task-copy">
            <strong>{taskLabels[task.name] ?? task.name}</strong>
            <small>{task.name}</small>
          </span>
        </td>
        <td>
          <span className={cn('cg-status', statusTone)}>{status}</span>
        </td>
        <td className="rt-task-time">{formatTaskTime(task.last_finished_at)}</td>
        <td className="rt-task-time">
          {task.runs > 0 ? `${task.last_duration_ms.toLocaleString()} ms` : '--'}
        </td>
        <td className="rt-task-time">{formatTaskTime(task.next_run_at)}</td>
      </tr>
      {failed ? (
        <tr className="rt-task-error-row">
          <td colSpan={5} className="rt-task-error" title={task.last_error}>
            <span className="rt-task-error-text">{task.last_error}</span>
          </td>
        </tr>
      ) : null}
    </>
  )
}

function PanelRuntimeSection() {
  const [snapshot, setSnapshot] = useState<PanelRuntimeSnapshot | null>(null)
  const [samples, setSamples] = useState<PanelRuntimeSnapshot[]>([])
  const [live, setLive] = useState(true)
  const [lastSyncedAt, setLastSyncedAt] = useState<Date | null>(null)
  const [error, setError] = useState('')
  const requestNumber = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const currentRequest = ++requestNumber.current
    try {
      const next = await api.panelRuntime({
        signal,
        ...(silent ? { display: 'silent' as const } : {}),
      })
      if (signal?.aborted || currentRequest !== requestNumber.current) return
      setSnapshot(next)
      setSamples((current) => [...current, next].slice(-MAX_SAMPLES))
      setLastSyncedAt(new Date(next.sampled_at))
      setError('')
    } catch (loadError) {
      if (signal?.aborted || currentRequest !== requestNumber.current) return
      setError(errorMessage(loadError))
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(false, controller.signal)
    if (!live) return () => controller.abort()
    const timer = window.setInterval(() => void load(true, controller.signal), 5000)
    return () => {
      requestNumber.current += 1
      controller.abort()
      window.clearInterval(timer)
    }
  }, [live, load])

  const trends = useMemo(
    () =>
      samples.map((sample) => ({
        cpu: sample.host.cpu_percent,
        memory: percent(sample.host.memory_active, sample.host.memory_total),
        rss: percent(sample.process.rss_bytes, sample.host.memory_total),
      })),
    [samples],
  )

  if (!snapshot && error) {
    return (
      <Notice tone="danger" title="无法读取运行状态" className="max-w-xl">
        {error}
      </Notice>
    )
  }

  if (!snapshot) {
    return (
      <div className="rt-loading" role="status" aria-label="正在读取运行状态">
        <span className="rt-loading-label">LOADING / RUNTIME</span>
        <div className="rt-loading-bar" aria-hidden="true">
          <i />
          <i />
          <i />
        </div>
      </div>
    )
  }

  const memoryPercent = percent(snapshot.host.memory_active, snapshot.host.memory_total)
  const heapPercent = percent(snapshot.process.heap_alloc, snapshot.process.heap_sys)
  const rssPercent = percent(snapshot.process.rss_bytes, snapshot.host.memory_total)
  const logPercent = percent(
    snapshot.services.request_log_usage,
    snapshot.services.request_log_limit,
  )
  const onlinePercent = snapshot.services.agents_total
    ? percent(snapshot.services.agents_online, snapshot.services.agents_total)
    : 100
  const cpuPercent = snapshot.host.cpu_percent ?? 0
  const runningTasks = snapshot.tasks.filter((task) => task.running).length
  const failedTasks = snapshot.tasks.filter((task) => task.last_error).length
  const servicesOk =
    snapshot.services.database_healthy &&
    failedTasks === 0 &&
    snapshot.services.request_log_dropped === 0
  const syncedLabel = lastSyncedAt
    ? `${lastSyncedAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })} 已同步`
    : '正在同步'

  return (
    <>
      {error ? (
        <Notice tone="warning" title="本次刷新失败">
          正在显示上一次有效快照：{error}
        </Notice>
      ) : null}

      {/* 面板运行时：状态 + 版本/运行时间/PID/Go Runtime */}
      <section
        className="rt-mod"
        data-state={snapshot.panel.state}
        aria-labelledby="runtime-panel-heading"
      >
        <ModuleHead
          tab="PANEL"
          title="面板运行时"
          id="runtime-panel-heading"
          meta={`${snapshot.host.hostname || 'Lattix Panel'} · ${snapshot.host.os}/${snapshot.host.arch}`}
        >
          <span className="rt-sync">{syncedLabel}</span>
          <span className={cn('cg-status', live ? 'is-ok' : 'is-muted')}>
            {live ? '实时采样' : '已暂停'}
          </span>
          <button
            type="button"
            className="cg-icon-button"
            onClick={() => setLive((current) => !current)}
            aria-label={live ? '暂停自动刷新' : '继续自动刷新'}
            title={live ? '暂停自动刷新' : '继续自动刷新'}
          >
            {live ? <PauseIcon /> : <PlayIcon />}
          </button>
          <button
            type="button"
            className="cg-icon-button"
            onClick={() => void load()}
            aria-label="立即刷新"
            title="立即刷新"
          >
            <RefreshCwIcon />
          </button>
        </ModuleHead>
        <div className="rt-panel-body">
          <div className="rt-panel-state">
            <span className="rt-beacon" aria-hidden="true" />
            <strong>{stateLabel[snapshot.panel.state]}</strong>
            <span className={cn('cg-status', panelStateTone(snapshot.panel.state))}>
              {snapshot.panel.state}
            </span>
          </div>
          <dl className="rt-kv">
            <div>
              <dt>版本 Version</dt>
              <dd>{snapshot.panel.version || 'dev'}</dd>
            </div>
            <div>
              <dt>运行时间 Uptime</dt>
              <dd>{formatDuration(snapshot.panel.uptime_seconds)}</dd>
            </div>
            <div>
              <dt>进程 PID</dt>
              <dd>{snapshot.panel.pid}</dd>
            </div>
            <div>
              <dt>Go Runtime</dt>
              <dd>{snapshot.process.go_version}</dd>
            </div>
          </dl>
        </div>
      </section>

      {/* 资源指标 */}
      <section className="rt-mod" aria-label="资源概览">
        <ModuleHead
          tab="RESOURCE"
          title="资源概览"
          meta={`${snapshot.host.cpu_cores} 核 · Load ${snapshot.host.load1.toFixed(2)}`}
        />
        <div className="rt-metric-grid">
          <MetricTile
            icon={<CpuIcon />}
            label="CPU 使用率"
            value={formatPercent(snapshot.host.cpu_percent)}
            detail={`${snapshot.host.cpu_cores} 核 · Load ${snapshot.host.load1.toFixed(2)}`}
            progress={cpuPercent}
          />
          <MetricTile
            icon={<MemoryStickIcon />}
            label="主机内存"
            value={`${memoryPercent.toFixed(0)}%`}
            detail={`${humanizeBytes(snapshot.host.memory_active)} / ${humanizeBytes(snapshot.host.memory_total)}`}
            progress={memoryPercent}
          />
          <MetricTile
            icon={<HardDriveIcon />}
            label="进程驻留内存"
            value={humanizeBytes(snapshot.process.rss_bytes)}
            detail={`虚拟内存 ${humanizeBytes(snapshot.process.virtual_bytes)}`}
            progress={rssPercent}
          />
          <MetricTile
            icon={<DatabaseIcon />}
            label="数据库响应"
            value={
              snapshot.services.database_healthy
                ? `${snapshot.services.database_latency_ms.toFixed(2)} ms`
                : '不可用'
            }
            detail={snapshot.services.database_healthy ? 'SQLite 探测正常' : '探测未通过'}
            progress={
              snapshot.services.database_healthy
                ? Math.min(100, snapshot.services.database_latency_ms)
                : 100
            }
          />
        </div>
      </section>

      <div className="rt-main-grid">
        <section className="rt-mod rt-span-7" aria-labelledby="runtime-resource-heading">
          <ModuleHead
            tab="TREND"
            title="资源趋势"
            id="runtime-resource-heading"
            meta={`${samples.length} / ${MAX_SAMPLES} 采样`}
          />
          <div className="rt-trend-list">
            <TrendStrip
              label="CPU"
              values={trends.map((sample) => sample.cpu)}
              formatter={formatPercent}
            />
            <TrendStrip
              label="主机内存"
              values={trends.map((sample) => sample.memory)}
              formatter={(value) => formatPercent(value)}
            />
            <TrendStrip
              label="进程 RSS / 主机"
              values={trends.map((sample) => sample.rss)}
              formatter={(value) => formatPercent(value)}
            />
          </div>
        </section>

        <aside className="rt-mod rt-span-5" aria-labelledby="runtime-service-heading">
          <ModuleHead
            tab="SERVICE"
            title="服务状态"
            id="runtime-service-heading"
            meta={
              <span className="rt-meta-tone" data-tone={servicesOk ? 'ok' : 'bad'}>
                {servicesOk ? 'ALL OK' : 'ATTENTION'}
              </span>
            }
          />
          <div className="rt-service-list">
            <div className="rt-service-row">
              <span
                className={cn(
                  'rt-service-icon',
                  snapshot.services.database_healthy && 'is-ok',
                  !snapshot.services.database_healthy && 'is-warning',
                )}
              >
                <DatabaseIcon />
              </span>
              <p>
                <strong>SQLite</strong>
                <small>主数据存储</small>
              </p>
              <span
                className={cn('cg-status', snapshot.services.database_healthy ? 'is-ok' : 'is-red')}
              >
                {snapshot.services.database_healthy ? '正常' : '异常'}
              </span>
            </div>
            <div className="rt-service-row">
              <span
                className={cn(
                  'rt-service-icon',
                  failedTasks === 0 && 'is-ok',
                  failedTasks > 0 && 'is-warning',
                )}
              >
                <ListChecksIcon />
              </span>
              <p>
                <strong>后台调度</strong>
                <small>
                  {snapshot.tasks.length} 项任务 · {runningTasks} 项运行中
                </small>
              </p>
              <span
                className={cn(
                  'cg-status',
                  failedTasks > 0 ? 'is-red' : runningTasks > 0 ? 'is-ok' : 'is-muted',
                )}
              >
                {failedTasks > 0
                  ? `${failedTasks} 项异常`
                  : runningTasks > 0
                    ? `${runningTasks} 项运行中`
                    : '待命'}
              </span>
            </div>
            <div className="rt-service-row">
              <span
                className={cn(
                  'rt-service-icon',
                  onlinePercent === 100 && 'is-ok',
                  onlinePercent < 100 && 'is-warning',
                )}
              >
                <WaypointsIcon />
              </span>
              <p>
                <strong>Agent 会话</strong>
                <small>控制通道</small>
              </p>
              <span className={cn('cg-status', onlinePercent === 100 ? 'is-ok' : 'is-warn')}>
                {snapshot.services.agents_online} / {snapshot.services.agents_total}
              </span>
            </div>
            <div className="rt-service-row">
              <span
                className={cn(
                  'rt-service-icon',
                  snapshot.services.request_log_dropped === 0 && 'is-ok',
                  snapshot.services.request_log_dropped > 0 && 'is-warning',
                )}
              >
                <ServerIcon />
              </span>
              <p>
                <strong>请求日志</strong>
                <small>{logPercent.toFixed(0)}% 容量</small>
              </p>
              <span
                className={cn(
                  'cg-status',
                  snapshot.services.request_log_dropped ? 'is-red' : 'is-ok',
                )}
              >
                {snapshot.services.request_log_dropped
                  ? `丢弃 ${snapshot.services.request_log_dropped}`
                  : '正常'}
              </span>
            </div>
          </div>
        </aside>
      </div>

      {/* Go 进程详情 */}
      <section className="rt-mod" aria-labelledby="runtime-process-heading">
        <ModuleHead
          tab="GO"
          title="进程详情"
          id="runtime-process-heading"
          meta={`${snapshot.process.goroutines} GOROUTINES`}
        />
        <div className="rt-process-grid">
          <ProcessMetric
            label="Heap Alloc"
            value={humanizeBytes(snapshot.process.heap_alloc)}
            detail={`Heap Sys ${humanizeBytes(snapshot.process.heap_sys)}`}
            progress={heapPercent}
          />
          <ProcessMetric
            label="Heap In-use"
            value={humanizeBytes(snapshot.process.heap_inuse)}
            detail={`已保留堆内存的 ${percent(snapshot.process.heap_inuse, snapshot.process.heap_sys).toFixed(0)}%`}
            progress={percent(snapshot.process.heap_inuse, snapshot.process.heap_sys)}
          />
          <ProcessMetric
            label="Stack In-use"
            value={humanizeBytes(snapshot.process.stack_inuse)}
            detail={`${snapshot.process.goroutines} 个 Goroutine`}
            progress={percent(
              snapshot.process.stack_inuse,
              snapshot.process.heap_sys + snapshot.process.stack_inuse,
            )}
          />
          <ProcessMetric
            label="垃圾回收"
            value={`${snapshot.process.gc_cycles.toLocaleString()} 次`}
            detail={
              snapshot.process.last_gc_at
                ? `最近 ${formatTaskTime(snapshot.process.last_gc_at)}`
                : '尚无 GC 记录'
            }
            progress={Math.min(100, snapshot.process.gc_cycles % 100)}
          />
        </div>
      </section>

      {/* 后台任务 */}
      <section className="rt-mod" aria-labelledby="runtime-task-heading">
        <ModuleHead
          tab="TASKS"
          title="后台任务"
          id="runtime-task-heading"
          meta={`${snapshot.tasks.length} REGISTERED${failedTasks > 0 ? ` · ${failedTasks} 异常` : ''}`}
        />
        <div className="rt-task-wrap">
          <table className="rt-task-table">
            <thead>
              <tr>
                <th scope="col">任务</th>
                <th scope="col">状态</th>
                <th scope="col">上次完成</th>
                <th scope="col">耗时</th>
                <th scope="col">下次执行</th>
              </tr>
            </thead>
            <tbody>
              {snapshot.tasks.length ? (
                snapshot.tasks.map((task) => <TaskRow key={task.name} task={task} />)
              ) : (
                <tr>
                  <td colSpan={5}>
                    <span className="rt-task-empty">
                      <Clock3Icon aria-hidden="true" />
                      尚无已注册任务
                    </span>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <footer className="rt-foot" aria-label="运行时采样状态">
        <span>
          <ActivityIcon aria-hidden="true" /> {live ? '每 5 秒自动采样' : '自动采样已暂停'}
        </span>
        <span>
          <TimerResetIcon aria-hidden="true" /> 最多保留 {MAX_SAMPLES} 个样本
        </span>
        <span>
          <BracesIcon aria-hidden="true" /> {snapshot.process.goroutines} Goroutine ·{' '}
          {snapshot.process.gc_cycles.toLocaleString()} 次 GC
        </span>
        <span className="rt-foot-end">{syncedLabel}</span>
      </footer>
    </>
  )
}

export default function RuntimeMonitor() {
  const [tab, setTab] = useState<'panel' | 'agents'>('panel')

  return (
    <Page className="rt">
      <PageHeader title="运行监控" description="面板进程、宿主资源与后台任务的实时状态。" />
      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value === 'agents' ? 'agents' : 'panel')}
        className="rt-tabs"
      >
        <TabsList>
          <TabsTrigger value="panel">Panel 运行</TabsTrigger>
          <TabsTrigger value="agents">被控统计</TabsTrigger>
        </TabsList>
        <TabsContent value="panel" className="rt-tab-panel">
          <PanelRuntimeSection />
        </TabsContent>
        <TabsContent value="agents" className="rt-tab-panel">
          <AgentsStatsTab />
        </TabsContent>
      </Tabs>
    </Page>
  )
}
