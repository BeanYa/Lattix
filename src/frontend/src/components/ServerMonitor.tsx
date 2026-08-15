import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CalendarCheckIcon,
  CircleCheckIcon,
  EllipsisIcon,
  ExternalLinkIcon,
  Globe2Icon,
  LoaderCircleIcon,
  Maximize2Icon,
  PencilIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  RotateCcwKeyIcon,
  ServerCogIcon,
  SparklesIcon,
  Trash2Icon,
  WifiOffIcon,
  WrenchIcon,
} from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { addressFamily } from '@/lib/address'
import { EmptyState } from '@/components/PagePrimitives'
import { ServerTestPanel } from '@/components/ServerTestPanel'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { formatByteRate, formatDateTime, humanizeBytes } from '@/lib/format'
import { formatPortRange } from '@/lib/ports'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type { ConvertedCost, Server, ServerConnectionState, ServerMetrics, ServerMetricSeries } from '@/lib/types'

type Health = 'normal' | 'warning' | 'critical'

interface ServerMonitorProps {
  servers: Server[]
  samples: ServerMetricSeries[]
  loading: boolean
  timezone?: string
  onEdit: (server: Server) => void
  onRepair: (server: Server) => void
  onCleanupXray: (server: Server) => void
  onRebuildXray: (server: Server) => void
  onRotateToken: (server: Server) => void
  onUpgrade: (server: Server, kind: 'xray' | 'agent') => void
  onRenew: (server: Server) => void
  onDelete: (server: Server) => void
}

function formatTrafficBytes(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes
  let unit = 0
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000
    unit += 1
  }
  return `${value.toLocaleString(undefined, { maximumFractionDigits: value >= 100 ? 0 : 2 })} ${units[unit]}`
}

const billingStatusLabel = {
  active: '有效',
  due_today: '今日到期',
  assumed_valid: '推定有效',
  expired: '已过期',
} as const

function serverConnectionNotice(state: ServerConnectionState): string {
  switch (state) {
    case 'never_connected':
      return 'Agent 尚未连接'
    case 'connecting':
      return '正在建立 Agent 连接'
    case 'reconnecting':
      return '正在重新连接 Agent'
    case 'offline':
      return 'Agent 连接已中断'
    case 'auth_rejected':
      return 'Agent 凭据验证失败'
    case 'online':
      return ''
  }
}

function convertedRateLabel(cost: ConvertedCost): string {
  const label = cost.source === 'identity'
    ? '无需换算'
    : cost.source === 'custom_anchor'
      ? `自定义锚点 ${cost.anchor_currency ?? ''}`.trim()
      : 'Frankfurter'
  return cost.rate_date ? `${label} · ${cost.rate_date}` : label
}

function formatConvertedCost(cost: ConvertedCost): string {
  const divisor = ['JPY', 'KRW', 'ISK'].includes(cost.currency) ? 1 : 100
  return `${(cost.amount_minor / divisor).toLocaleString()} ${cost.currency}`
}

function TrafficSegmentBar({ ratio, complete, unlimited }: { ratio: number; complete: boolean; unlimited: boolean }) {
  const segments = 24
  const clamped = Math.min(100, Math.max(0, ratio))
  const filled = unlimited ? segments : clamped === 0 ? 0 : Math.ceil((clamped / 100) * segments)
  const fillClass = unlimited
    ? 'sv-seg-ok'
    : !complete
      ? 'sv-seg-idle'
      : clamped >= 80
        ? 'sv-seg-crit'
        : clamped >= 60
          ? 'sv-seg-warn'
          : 'sv-seg-ok'
  return (
    <div
      className="grid h-2 grid-cols-[repeat(24,minmax(0,1fr))] gap-[2px]"
      role="progressbar"
      aria-label="流量额度使用率"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={unlimited ? undefined : Math.round(clamped)}
      aria-valuetext={unlimited ? '无限额度' : undefined}
    >
      {Array.from({ length: segments }).map((_, index) => (
        <span key={index} className={cn('sv-seg', index < filled && fillClass)} />
      ))}
    </div>
  )
}

function TrafficUsage({ server }: { server: Server }) {
  const traffic = server.traffic_plan
  const ratio = traffic.quota_bytes ? (traffic.used_bytes / traffic.quota_bytes) * 100 : 0
  const alert = traffic.complete && ratio >= 60
  return (
    <div className="space-y-1.5" aria-label="本周期流量">
      <div className="flex items-center justify-between gap-3 text-xs tabular-nums">
        <span className={cn('font-medium', alert && (ratio >= 80 ? 'text-destructive' : 'text-warning'))}>{formatTrafficBytes(traffic.used_bytes)}</span>
        <span className="text-muted-foreground">
          <span aria-label={traffic.quota_bytes === null ? '无限额度' : undefined}>
            {traffic.quota_bytes === null ? '∞' : formatTrafficBytes(traffic.quota_bytes)}
          </span>
          <span className="ml-1">· {traffic.next_reset_on} 重置</span>
        </span>
      </div>
      <TrafficSegmentBar ratio={ratio} complete={traffic.complete} unlimited={traffic.quota_bytes === null} />
      {!traffic.complete ? <span className="text-[11px] text-muted-foreground">本周期数据不完整</span> : null}
    </div>
  )
}

function percent(used: number, total: number): number {
  return total > 0 ? Math.min(100, Math.max(0, (used / total) * 100)) : 0
}

function health(value: number, warning = 80, critical = 90): Health {
  if (value > critical) return 'critical'
  if (value >= warning) return 'warning'
  return 'normal'
}

function healthIndicatorClass(value: number, warning = 80, critical = 90): string {
  const state = health(value, warning, critical)
  return cn(
    'sv-progress-ok',
    state === 'warning' && 'sv-progress-warn',
    state === 'critical' && 'sv-progress-crit',
  )
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分钟`
}

function MetricBlock({
  label,
  value,
  detail,
  progress,
  warning,
  critical,
}: {
  label: string
  value: string
  detail: string
  progress: number | null
  warning?: number
  critical?: number
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex items-center justify-between gap-3 text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium tabular-nums">{value}</span>
      </div>
      <Progress
        value={progress ?? 0}
        className={cn(
          'gap-0',
          progress === null
            ? 'sv-progress-idle'
            : healthIndicatorClass(progress, warning, critical),
        )}
        aria-label={`${label} ${value}`}
      />
      <span className="truncate text-xs text-muted-foreground tabular-nums">{detail}</span>
    </div>
  )
}

function LatencyStrip({ samples, timezone }: { samples: ServerMetrics[]; timezone?: string }) {
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const values = samples.slice(-30)
  const cells: Array<ServerMetrics | null> = [
    ...values,
    ...Array.from<null>({ length: Math.max(0, 30 - values.length) }).fill(null),
  ]
  return (
    <div
      className="relative grid h-3 grid-cols-[repeat(30,minmax(0,1fr))] gap-px overflow-visible rounded-[2px] outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      role="group"
      tabIndex={0}
      aria-label="最近 30 次延迟趋势，使用左右方向键查看采样数据"
      onMouseLeave={(event) => {
        if (document.activeElement !== event.currentTarget) setActiveIndex(null)
      }}
      onFocus={() => setActiveIndex((current) => current ?? cells.length - 1)}
      onBlur={() => setActiveIndex(null)}
      onKeyDown={(event) => {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
        event.preventDefault()
        event.stopPropagation()
        const direction = event.key === 'ArrowLeft' ? -1 : 1
        setActiveIndex((current) => Math.min(cells.length - 1, Math.max(0, (current ?? cells.length - 1) + direction)))
      }}
    >
      {cells.map((sample, index) => {
        const latency = sample?.latency_ms ?? null
        const latencyValue = latency ?? 0
        const missing = sample === null
        const timedOut = !missing && latency === null
        const state = missing || timedOut ? null : health(latencyValue, 100, 300)
        const distance = activeIndex === null ? Number.POSITIVE_INFINITY : Math.abs(activeIndex - index)
        const label = missing
          ? '无数据'
          : timedOut
            ? '探测超时'
            : `${Math.round(latencyValue)} 毫秒`
        return (
          <span
            key={sample ? `${sample.updated_at}-${index}` : `empty-${index}`}
            role="img"
            aria-label={label}
            onMouseEnter={() => setActiveIndex(index)}
            className={cn(
              'group relative h-3 min-w-0 rounded-[1px] outline-none',
              distance === 0 && 'z-30',
              distance === 1 && 'z-20',
              distance === 2 && 'z-10',
            )}
          >
            {activeIndex === index ? (
              <span
                role="tooltip"
                className={cn(
                  'pointer-events-none absolute bottom-[calc(100%+10px)] z-50 min-w-28 whitespace-nowrap rounded-md border border-border bg-popover px-2 py-1.5 text-popover-foreground shadow-lg',
                  index < 3
                    ? 'left-0'
                    : index >= cells.length - 3
                      ? 'right-0'
                      : 'left-1/2 -translate-x-1/2',
                )}
              >
                <span className="flex items-center gap-1.5 text-xs font-semibold tabular-nums">
                  <span
                    aria-hidden="true"
                    className={cn(
                      'size-1.5 shrink-0 rounded-full bg-muted-foreground',
                      timedOut && 'bg-destructive',
                      state === 'normal' && 'bg-success',
                      state === 'warning' && 'bg-warning',
                      state === 'critical' && 'bg-destructive',
                    )}
                  />
                  {missing ? '无数据' : timedOut ? '探测超时' : `${Math.round(latencyValue)} ms`}
                </span>
                <span className="mt-0.5 block text-[10px] text-muted-foreground tabular-nums">
                  {sample ? formatDateTime(sample.updated_at, timezone) : '尚无采样记录'}
                </span>
              </span>
            ) : null}
            <span
              aria-hidden="true"
              className={cn(
                'sv-lat absolute inset-0 origin-bottom rounded-[1px] transition-[transform,box-shadow] duration-150 ease-out motion-reduce:transform-none motion-reduce:transition-none',
                timedOut && 'is-crit',
                state === 'normal' && 'is-ok',
                state === 'warning' && 'is-warn',
                state === 'critical' && 'is-crit',
                distance === 0 && '-translate-y-1 scale-x-[1.45] scale-y-[1.8] ring-1 ring-background shadow-md',
                distance === 1 && '-translate-y-0.5 scale-x-[1.2] scale-y-[1.4]',
                distance === 2 && 'scale-x-[1.08] scale-y-[1.15]',
              )}
            />
          </span>
        )
      })}
    </div>
  )
}

function ServerActions({
  server,
  onEdit,
  onRepair,
  onCleanupXray,
  onRebuildXray,
  onRotateToken,
  onUpgrade,
  onRenew,
  onDelete,
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'loading' | 'timezone'> & {
  server: Server
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`管理 ${server.alias}`}
            onClick={(event) => event.stopPropagation()}
          />
        }
      >
        <EllipsisIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
        <DropdownMenuGroup>
          <DropdownMenuLabel>服务器管理</DropdownMenuLabel>
          <DropdownMenuItem onClick={() => onEdit(server)}>
            <PencilIcon />
            编辑服务器
          </DropdownMenuItem>
          {server.config_drift ? (
            <DropdownMenuItem onClick={() => onRepair(server)}>
              <WrenchIcon />
              修复配置漂移
            </DropdownMenuItem>
          ) : null}
          {isServerOnline(server) ? (
            <DropdownMenuItem onClick={() => onCleanupXray(server)}>
              <SparklesIcon />
              清理 Xray 缓存
            </DropdownMenuItem>
          ) : null}
          {isServerOnline(server) ? (
            <DropdownMenuItem onClick={() => onRebuildXray(server)}>
              <RotateCcwIcon />
              重建 Xray 配置
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onClick={() => onUpgrade(server, 'xray')}>
            <RefreshCwIcon />
            升级 Xray
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => onUpgrade(server, 'agent')}>
            <ServerCogIcon />
            升级 Agent
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => onRotateToken(server)}>
            <RotateCcwKeyIcon />
            {server.last_seen_at ? '刷新凭证' : '查看安装命令'}
          </DropdownMenuItem>
          {server.billing.enabled && ['due_today', 'assumed_valid', 'expired'].includes(server.billing.status) ? (
            <DropdownMenuItem onClick={() => onRenew(server)}>
              <CalendarCheckIcon />
              续费确认
            </DropdownMenuItem>
          ) : null}
          {server.billing.provider?.website_url ? (
            <DropdownMenuItem
              onClick={() => window.open(server.billing.provider?.website_url, '_blank', 'noopener,noreferrer')}
            >
              <ExternalLinkIcon />
              打开服务商官网
            </DropdownMenuItem>
          ) : null}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem variant="destructive" onClick={() => onDelete(server)}>
            <Trash2Icon />
            删除服务器
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function ServerCard({
  server,
  samples,
  timezone,
  onOpen,
  ...actions
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'loading' | 'timezone'> & {
  server: Server
  samples: ServerMetrics[]
  timezone?: string
  onOpen: () => void
}) {
  const metrics = server.metrics
  const online = isServerOnline(server)
  const transitioning = server.connection_state === 'connecting' || server.connection_state === 'reconnecting'
  const publicAddress = server.address || server.learned_addr
  const memoryPercent = metrics ? percent(metrics.mem_used, metrics.mem_total) : 0
  const diskPercent = metrics ? percent(metrics.disk_used, metrics.disk_total) : 0
  const cpuPercent = metrics?.cpu_percent ?? null
  const latency = metrics?.latency_ms ?? null

  return (
    <Card
      size="sm"
      role="button"
      tabIndex={0}
      aria-label={`查看 ${server.alias} 监控详情，${serverConnectionLabel(server.connection_state)}`}
      className={cn(
        'sv-server-card relative cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        online && 'is-online',
        transitioning && 'is-transitioning',
        !online && !transitioning && 'is-offline',
      )}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') onOpen()
      }}
    >
      <CardHeader className="sv-card-head">
        <CardTitle className="flex min-w-0 items-center gap-2">
          <span
            className={cn(
              'sv-icon-chip',
              online && 'is-online',
              transitioning && 'is-transitioning',
              !online && !transitioning && 'is-offline',
            )}
            aria-hidden="true"
          >
            {online ? (
              <CircleCheckIcon className="size-4" />
            ) : transitioning ? (
              <LoaderCircleIcon className="size-3.5 animate-spin motion-reduce:animate-none" />
            ) : (
              <WifiOffIcon className="size-3.5" />
            )}
          </span>
          <span className="truncate">{server.alias}</span>
          <span
            className={cn(
              'cg-status ml-auto shrink-0',
              online ? 'is-lime' : transitioning ? 'is-blue' : 'is-red',
            )}
          >
            Agent {serverConnectionLabel(server.connection_state)}
          </span>
        </CardTitle>
        <CardDescription className="flex min-w-0 flex-col gap-1 text-xs">
          <span className="flex min-w-0 items-center gap-2">
            <CountryFlag code={server.country_code} />
            <span className="truncate">{server.location || server.country_code || '未设置地区'}</span>
          </span>
          {server.config_drift ? (
            <span className="cg-status is-muted mt-1 w-fit">DRIFT / 配置漂移</span>
          ) : null}
          {server.billing.enabled && server.billing.status in billingStatusLabel ? (
            <Badge
              variant={server.billing.status === 'expired' ? 'destructive' : 'outline'}
              className="mt-1 w-fit"
            >
              {billingStatusLabel[server.billing.status as keyof typeof billingStatusLabel]}
            </Badge>
          ) : null}
          <span
            className="flex min-w-0 items-center gap-1.5 font-mono text-[11px]"
            title={publicAddress || '公网地址待学习'}
            aria-label={`公网地址 ${publicAddress || '待学习'}`}
          >
            <Globe2Icon className="size-3 shrink-0" />
            <span className="truncate">{publicAddress || '公网地址待学习'}</span>
            {server.addresses.length > 1 ? (
              <span
                className="shrink-0 text-muted-foreground"
                title={`共 ${server.addresses.length} 个公网地址`}
              >
                +{server.addresses.length - 1}
              </span>
            ) : null}
          </span>
        </CardDescription>
        <CardAction>
          <ServerActions server={server} {...actions} />
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div
          className={cn(
            'sv-banner -mx-3 -mt-3 flex min-h-11 items-center gap-2.5 border-b px-3 py-2 text-xs',
            online && 'is-online',
            transitioning && 'is-transitioning',
            !online && !transitioning && 'is-offline',
          )}
        >
          <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-background/50">
            {online ? (
              <CircleCheckIcon className="size-3.5" />
            ) : transitioning ? (
              <LoaderCircleIcon className="size-3.5 animate-spin motion-reduce:animate-none" />
            ) : (
              <WifiOffIcon className="size-3.5" />
            )}
          </span>
          <span className="min-w-0">
            <strong className="block font-semibold">
              {online ? 'Agent 连接正常' : serverConnectionNotice(server.connection_state)}
            </strong>
            <span className="block truncate text-[10px] text-muted-foreground tabular-nums">
              {online
                ? metrics
                  ? `已运行 ${formatUptime(metrics.uptime_seconds)} · 遥测 ${formatDateTime(metrics.updated_at, timezone)}`
                  : '连接已建立，等待首次遥测'
                : metrics
                  ? `上次遥测 ${formatDateTime(metrics.updated_at, timezone)}`
                  : '暂无可用遥测'}
            </span>
          </span>
          {!online && metrics ? <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">历史数据</span> : null}
        </div>
        {metrics ? (
          <>
            <div className="grid grid-cols-2 gap-x-4 gap-y-3">
              <MetricBlock
                label="CPU"
                value={cpuPercent === null ? '--' : `${cpuPercent.toFixed(1)}%`}
                detail={`${metrics.load1.toFixed(2)} / ${metrics.load5.toFixed(2)} / ${metrics.load15.toFixed(2)}`}
                progress={cpuPercent}
              />
              <MetricBlock
                label="内存"
                value={`${memoryPercent.toFixed(1)}%`}
                detail={`${humanizeBytes(metrics.mem_used)} / ${humanizeBytes(metrics.mem_total)}`}
                progress={memoryPercent}
              />
              <MetricBlock
                label="磁盘"
                value={`${diskPercent.toFixed(1)}%`}
                detail={`${humanizeBytes(metrics.disk_used)} / ${humanizeBytes(metrics.disk_total)}`}
                progress={diskPercent}
              />
              <div className="flex min-w-0 flex-col gap-1.5">
                <span className="text-xs text-muted-foreground">网络</span>
                <div className="flex min-w-0 items-center gap-3 text-xs tabular-nums">
                  <span className="sv-rate-up flex min-w-0 items-center gap-1 truncate">
                    <ArrowUpIcon className="size-3" />
                    {formatByteRate(metrics.network_tx_bps)}
                  </span>
                  <span className="sv-rate-down flex min-w-0 items-center gap-1 truncate">
                    <ArrowDownIcon className="size-3" />
                    {formatByteRate(metrics.network_rx_bps)}
                  </span>
                </div>
              </div>
            </div>
            <Separator />
            <div className="grid grid-cols-[88px_1fr] items-end gap-3">
              <div className="flex flex-col gap-1">
                <span className="text-xs text-muted-foreground">延迟</span>
                <span className="text-base font-semibold tabular-nums">
                  {latency === null ? '--' : `${Math.round(latency)} ms`}
                </span>
              </div>
              <div className="flex flex-col gap-1">
                <span className="text-xs text-muted-foreground">最近 30 次</span>
                <LatencyStrip samples={samples} timezone={timezone} />
              </div>
            </div>
            <Separator />
            <TrafficUsage server={server} />
          </>
        ) : (
          <>
            <div className="flex min-h-28 flex-col items-center justify-center gap-2 text-center">
              <ServerCogIcon className="size-6 text-muted-foreground" />
              <span className="text-sm text-muted-foreground">
				{isServerOnline(server) ? '等待 Agent 首次遥测' : serverConnectionLabel(server.connection_state)}
              </span>
            </div>
            <Separator />
            <TrafficUsage server={server} />
          </>
        )}
      </CardContent>
    </Card>
  )
}

interface ChartSeries {
  label: string
  color: 'success' | 'info' | 'warning'
  values: Array<number | null>
}

function TrendChart({
  title,
  unit,
  series,
  timestamps,
  timezone,
  expanded = false,
}: {
  title: string
  unit: string
  series: ChartSeries[]
  timestamps: string[]
  timezone?: string
  expanded?: boolean
}) {
  const chartRef = useRef<SVGSVGElement>(null)
  const [activePoint, setActivePoint] = useState<{ index: number; y: number } | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const values = series.flatMap((item) => item.values.filter((value): value is number => value !== null))
  const max = Math.max(1, ...values)
  const width = 480
  const height = 96
  const sampleCount = Math.max(timestamps.length, ...series.map((item) => item.values.length))
  const pointX = (index: number) => (sampleCount <= 1 ? 0 : (index / (sampleCount - 1)) * width)
  const pointY = (value: number) => height - (value / max) * (height - 8) - 4
  const points = (items: Array<number | null>) =>
    items
      .map((value, index) => {
        if (value === null) return null
        const x = pointX(index)
        const y = pointY(value)
        return `${x.toFixed(1)},${y.toFixed(1)}`
      })
      .filter(Boolean)
      .join(' ')
  const updateActivePoint = (clientX: number, clientY: number) => {
    const bounds = chartRef.current?.getBoundingClientRect()
    if (!bounds || sampleCount === 0) return
    const xRatio = Math.min(1, Math.max(0, (clientX - bounds.left) / bounds.width))
    const yRatio = Math.min(1, Math.max(0, (clientY - bounds.top) / bounds.height))
    setActivePoint({ index: Math.round(xRatio * (sampleCount - 1)), y: yRatio * height })
  }
  const moveActivePoint = (offset: number) => {
    if (sampleCount === 0) return
    setActivePoint((current) => ({
      index: Math.min(sampleCount - 1, Math.max(0, (current?.index ?? sampleCount - 1) + offset)),
      y: current?.y ?? height / 2,
    }))
  }
  const formatValue = (value: number | null | undefined) => {
    if (value === null || value === undefined) return '--'
    return `${value.toLocaleString(undefined, { maximumFractionDigits: 2 })} ${unit}`
  }
  return (
    <section className="flex flex-col gap-2">
      <div className={cn('flex items-center justify-between gap-3', expanded && 'pr-10')}>
        <h3 className="text-sm font-medium">{title}</h3>
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            {series.map((item) => (
              <span key={item.label} className="flex items-center gap-1">
                <span
                  className={cn(
                    'size-2 rounded-full bg-current',
                    item.color === 'success' && 'sv-line-ok',
                    item.color === 'info' && 'sv-line-info',
                    item.color === 'warning' && 'sv-line-warn',
                  )}
                />
                {item.label}
              </span>
            ))}
          </div>
          {!expanded ? (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`放大${title}图表`}
              title={`放大${title}图表`}
              onClick={() => setDialogOpen(true)}
            >
              <Maximize2Icon />
            </Button>
          ) : null}
        </div>
      </div>
      <div className="sv-chart-box relative p-2">
        {values.length > 1 ? (
          <svg
            ref={chartRef}
            viewBox={`0 0 ${width} ${height}`}
            className={cn(
              'w-full touch-none outline-none focus-visible:ring-2 focus-visible:ring-ring',
              expanded ? 'h-[min(65vh,32rem)]' : 'h-24',
            )}
            role="img"
            aria-label={`${title}趋势，使用左右方向键查看采样数据`}
            tabIndex={0}
            onPointerMove={(event) => updateActivePoint(event.clientX, event.clientY)}
            onPointerLeave={() => setActivePoint(null)}
            onFocus={() => setActivePoint({ index: sampleCount - 1, y: height / 2 })}
            onBlur={() => setActivePoint(null)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                event.preventDefault()
                moveActivePoint(event.key === 'ArrowLeft' ? -1 : 1)
              }
            }}
          >
            <path d={`M 0 ${height - 1} H ${width}`} stroke="var(--border)" fill="none" />
            {series.map((item) => (
              <polyline
                key={item.label}
                points={points(item.values)}
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                className={cn(
                  item.color === 'success' && 'sv-line-ok',
                  item.color === 'info' && 'sv-line-info',
                  item.color === 'warning' && 'sv-line-warn',
                )}
              />
            ))}
            {activePoint ? (
              <>
                <line
                  x1={pointX(activePoint.index)}
                  x2={pointX(activePoint.index)}
                  y1={0}
                  y2={height}
                  stroke="var(--muted-foreground)"
                  strokeWidth="1"
                  strokeDasharray="3 3"
                  vectorEffect="non-scaling-stroke"
                />
                <line
                  x1={0}
                  x2={width}
                  y1={activePoint.y}
                  y2={activePoint.y}
                  stroke="var(--muted-foreground)"
                  strokeWidth="1"
                  strokeDasharray="3 3"
                  vectorEffect="non-scaling-stroke"
                />
                {series.map((item) => {
                  const value = item.values[activePoint.index]
                  if (value === null || value === undefined) return null
                  return (
                    <circle
                      key={item.label}
                      cx={pointX(activePoint.index)}
                      cy={pointY(value)}
                      r="3"
                      fill="var(--background)"
                      stroke="currentColor"
                      strokeWidth="2"
                      vectorEffect="non-scaling-stroke"
                      className={cn(
                        item.color === 'success' && 'sv-line-ok',
                        item.color === 'info' && 'sv-line-info',
                        item.color === 'warning' && 'sv-line-warn',
                      )}
                    />
                  )
                })}
              </>
            ) : null}
          </svg>
        ) : (
          <div className="flex h-24 items-center justify-center text-xs text-muted-foreground">暂无趋势数据</div>
        )}
        {activePoint ? (
          <div
            className={cn(
              'pointer-events-none absolute top-2 z-10 min-w-32 rounded-md border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-md',
              activePoint.index < sampleCount / 2 ? 'right-2' : 'left-2',
            )}
          >
            <div className="mb-1 whitespace-nowrap text-muted-foreground">
              {formatDateTime(timestamps[activePoint.index], timezone)}
            </div>
            {series.map((item) => (
              <div key={item.label} className="flex items-center justify-between gap-4 tabular-nums">
                <span>{item.label}</span>
                <span className="font-medium">{formatValue(item.values[activePoint.index])}</span>
              </div>
            ))}
          </div>
        ) : null}
      </div>
      <span className="text-right text-xs text-muted-foreground">{unit}</span>
      {!expanded ? (
        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogContent className="w-[calc(100%-2rem)] max-w-5xl p-5 sm:max-w-5xl">
            <DialogTitle className="sr-only">{title}</DialogTitle>
            <DialogDescription className="sr-only">{title}最近 24 小时趋势</DialogDescription>
            <TrendChart
              title={title}
              unit={unit}
              series={series}
              timestamps={timestamps}
              timezone={timezone}
              expanded
            />
          </DialogContent>
        </Dialog>
      ) : null}
    </section>
  )
}

function DetailSheet({
  server,
  open,
  onOpenChange,
  timezone,
}: {
  server: Server | null
  open: boolean
  onOpenChange: (open: boolean) => void
  timezone?: string
}) {
  const [history, setHistory] = useState<ServerMetrics[]>([])
  const [loading, setLoading] = useState(false)

  const load = useCallback(() => {
    if (!server) return
    setLoading(true)
    api.serverMetricHistory(server.id)
      .then(setHistory)
      .catch(() => setHistory([]))
      .finally(() => setLoading(false))
  }, [server])

  useEffect(() => {
    if (!open || !server) return
    load()
    const timer = setInterval(load, 60000)
    return () => clearInterval(timer)
  }, [load, open, server])

  const cpu = history.map((sample) => sample.cpu_percent)
  const memory = history.map((sample) => percent(sample.mem_used, sample.mem_total))
  const disk = history.map((sample) => percent(sample.disk_used, sample.disk_total))
  const tx = history.map((sample) => sample.network_tx_bps === null ? null : sample.network_tx_bps / 1024)
  const rx = history.map((sample) => sample.network_rx_bps === null ? null : sample.network_rx_bps / 1024)
  const latency = history.map((sample) => sample.latency_ms)
  const timestamps = history.map((sample) => sample.updated_at)
  const metrics = server?.metrics

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b">
          <SheetTitle className="flex items-center gap-2">
            <span className={cn('size-2 rounded-full bg-muted-foreground', isServerOnline(server) && 'bg-success')} />
            {server?.alias ?? '服务器详情'}
          </SheetTitle>
          <SheetDescription>主机信息与最近 24 小时探针指标</SheetDescription>
        </SheetHeader>
        {server ? (
          <div className="flex flex-col gap-5 px-4 pb-6">
            <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
              <div>
                <dt className="text-xs text-muted-foreground">地区</dt>
                <dd className="mt-1 flex items-center gap-2">
                  <CountryFlag code={server.country_code} />
                  {server.location || '-'}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">地址</dt>
                <dd className="mt-1 font-mono text-xs">{server.address || server.learned_addr || '-'}</dd>
                {server.addresses.length > 0 ? (
                  <ul className="mt-1.5 space-y-1">
                    {server.addresses.map((addr) => {
                      const family = addressFamily(addr)
                      return (
                        <li key={addr} className="flex items-center gap-2 font-mono text-xs">
                          <span className="truncate">{addr}</span>
                          <Badge variant="outline" className="shrink-0">
                            {family === 'ipv4' ? 'IPv4' : family === 'ipv6' ? 'IPv6' : '域名'}
                          </Badge>
                          {addr === server.address ? (
                            <span className="shrink-0 text-[10px] text-muted-foreground">默认</span>
                          ) : null}
                        </li>
                      )
                    })}
                  </ul>
                ) : null}
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">默认网卡</dt>
                <dd className="mt-1">{metrics?.network_interface || '-'}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">运行时间</dt>
                <dd className="mt-1">{metrics ? formatUptime(metrics.uptime_seconds) : '-'}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">累计发送 / 接收</dt>
                <dd className="mt-1 tabular-nums">
                  {metrics
                    ? `${humanizeBytes(metrics.network_tx_bytes)} / ${humanizeBytes(metrics.network_rx_bytes)}`
                    : '-'}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">最近采样</dt>
                <dd className="mt-1">{formatDateTime(metrics?.updated_at, timezone)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Agent / Xray</dt>
                <dd className="mt-1">{server.agent_version ?? '-'} / {server.xray_version ?? '-'}</dd>
                {server.effective_xray_version && (
                  <span className="text-xs text-muted-foreground">
                    期望 {server.effective_xray_version}
                    {server.custom_settings ? '（服务器覆盖）' : '（面板默认）'}
                  </span>
                )}
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">机器类型 / 端口</dt>
                <dd className="mt-1">
                  {server.machine_type === 'direct'
                    ? '直连'
                    : `NAT · ${server.allowed_ports.map(formatPortRange).join(', ') || '仅出口'}`}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">配置状态</dt>
                <dd className="mt-1">{server.config_drift ? '配置漂移' : '正常'}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">流量额度</dt>
                <dd className="mt-1">{server.traffic_plan.quota_bytes === null ? '无限' : formatTrafficBytes(server.traffic_plan.quota_bytes)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">本周期流量</dt>
                <dd className="mt-1">{formatTrafficBytes(server.traffic_plan.used_bytes)} · {server.traffic_plan.accounting_mode === 'bidirectional' ? '双向合计' : server.traffic_plan.accounting_mode === 'max' ? '取较大方向' : '仅出站'}</dd>
              </div>
              {server.billing.enabled ? (
                <>
                  <div>
                    <dt className="text-xs text-muted-foreground">服务商</dt>
                    <dd className="mt-1">{server.billing.provider?.name ?? '-'}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">费用</dt>
                    <dd className="mt-1 tabular-nums">{(server.billing.amount_minor / (['JPY', 'KRW', 'ISK'].includes(server.billing.currency) ? 1 : 100)).toLocaleString()} {server.billing.currency} / {server.billing.interval_count} {server.billing.interval_unit === 'day' ? '天' : server.billing.interval_unit === 'year' ? '年' : '月'}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">下次续费</dt>
                    <dd className="mt-1">{server.billing.next_renewal_on}</dd>
                  </div>
                </>
              ) : null}
              <div>
                <dt className="text-xs text-muted-foreground">Agent 设置</dt>
                <dd className="mt-1">
                  {server.agent_settings_status === 'synced'
                    ? '已同步'
                    : server.agent_settings_status === 'failed'
                      ? '同步失败'
                  : '待同步'}
                </dd>
              </div>
              {server.billing.enabled && server.billing.public_converted ? (
                <div className="col-span-2 mt-1 border-t pt-3">
                  <dt className="text-xs font-medium text-muted-foreground">费用折算</dt>
                  <dd className="mt-2 grid gap-2 sm:grid-cols-2">
                    <div>
                      <span className="block text-xs text-muted-foreground">公共汇率结果</span>
                      <span className="mt-0.5 block tabular-nums">
                        {formatConvertedCost(server.billing.public_converted)} · {convertedRateLabel(server.billing.public_converted)}
                      </span>
                    </div>
                    {server.billing.custom_converted ? (
                      <div>
                        <span className="block text-xs text-muted-foreground">自定义汇率结果</span>
                        <span className="mt-0.5 block tabular-nums">
                          {formatConvertedCost(server.billing.custom_converted)} · {convertedRateLabel(server.billing.custom_converted)}
                        </span>
                      </div>
                    ) : null}
                  </dd>
                </div>
              ) : null}
            </dl>
            <Separator />
            {loading && history.length === 0 ? (
              <div className="flex flex-col gap-4">
                {Array.from({ length: 5 }).map((_, index) => (
                  <Skeleton key={index} className="h-32 w-full" />
                ))}
              </div>
            ) : (
              <div className="flex flex-col gap-5">
                <TrendChart
                  title="CPU 使用率"
                  unit="%"
                  series={[{ label: 'CPU', color: 'success', values: cpu }]}
                  timestamps={timestamps}
                  timezone={timezone}
                />
                <TrendChart
                  title="内存使用率"
                  unit="%"
                  series={[{ label: '内存', color: 'success', values: memory }]}
                  timestamps={timestamps}
                  timezone={timezone}
                />
                <TrendChart
                  title="磁盘使用率"
                  unit="%"
                  series={[{ label: '磁盘', color: 'warning', values: disk }]}
                  timestamps={timestamps}
                  timezone={timezone}
                />
                <TrendChart
                  title="网络速率"
                  unit="KB/s"
                  series={[
                    { label: '上传', color: 'success', values: tx },
                    { label: '下载', color: 'info', values: rx },
                  ]}
                  timestamps={timestamps}
                  timezone={timezone}
                />
                <TrendChart
                  title="Agent 延迟"
                  unit="ms"
                  series={[{ label: '延迟', color: 'info', values: latency }]}
                  timestamps={timestamps}
                  timezone={timezone}
                />
              </div>
            )}
            <Separator />
            <ServerTestPanel server={server} active={open} timezone={timezone} />
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

export function ServerMonitorGrid(props: ServerMonitorProps) {
  const [selectedID, setSelectedID] = useState<number | null>(null)
  const selected = props.servers.find((server) => server.id === selectedID) ?? null
  const samplesByServer = useMemo(
    () => new Map(props.samples.map((series) => [series.server_id, series.samples])),
    [props.samples],
  )

  if (props.loading) {
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        {Array.from({ length: 8 }).map((_, index) => (
          <Skeleton key={index} className="h-80 w-full" />
        ))}
      </div>
    )
  }

  if (props.servers.length === 0) {
    return (
      <EmptyState
        icon={<ServerCogIcon />}
        description="暂无服务器，点击右上角“添加服务器”开始"
      />
    )
  }

  return (
    <>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        {props.servers.map((server) => (
          <ServerCard
            key={server.id}
            server={server}
            samples={samplesByServer.get(server.id) ?? []}
            timezone={props.timezone}
            onOpen={() => setSelectedID(server.id)}
            onEdit={props.onEdit}
            onRepair={props.onRepair}
            onCleanupXray={props.onCleanupXray}
            onRebuildXray={props.onRebuildXray}
            onRotateToken={props.onRotateToken}
            onUpgrade={props.onUpgrade}
            onRenew={props.onRenew}
            onDelete={props.onDelete}
          />
        ))}
      </div>
      <DetailSheet
        server={selected}
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelectedID(null)
        }}
        timezone={props.timezone}
      />
    </>
  )
}
