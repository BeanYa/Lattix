import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ArrowDownIcon,
  ArrowUpIcon,
  EllipsisIcon,
  PencilIcon,
  RefreshCwIcon,
  RotateCcwKeyIcon,
  ServerCogIcon,
  Trash2Icon,
  WrenchIcon,
} from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
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
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { formatPortRange } from '@/lib/ports'
import { cn } from '@/lib/utils'
import type { Server, ServerMetrics, ServerMetricSeries } from '@/lib/types'

type Health = 'normal' | 'warning' | 'critical'

interface ServerMonitorProps {
  servers: Server[]
  samples: ServerMetricSeries[]
  loading: boolean
  timezone?: string
  onEdit: (server: Server) => void
  onRepair: (server: Server) => void
  onRotateToken: (server: Server) => void
  onUpgrade: (server: Server, kind: 'xray' | 'agent') => void
  onDelete: (server: Server) => void
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
    '[&_[data-slot=progress-indicator]]:bg-success',
    state === 'warning' && '[&_[data-slot=progress-indicator]]:bg-warning',
    state === 'critical' && '[&_[data-slot=progress-indicator]]:bg-destructive',
  )
}

function formatRate(value: number | null): string {
  return value === null ? '--' : `${humanizeBytes(Math.round(value))}/s`
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
            ? '[&_[data-slot=progress-indicator]]:bg-muted'
            : healthIndicatorClass(progress, warning, critical),
        )}
        aria-label={`${label} ${value}`}
      />
      <span className="truncate text-xs text-muted-foreground tabular-nums">{detail}</span>
    </div>
  )
}

function LatencyStrip({ samples }: { samples: ServerMetrics[] }) {
  const values = samples.slice(-30)
  const padded = Array.from({ length: Math.max(0, 30 - values.length) })
  return (
    <div className="grid h-3 grid-cols-[repeat(30,minmax(0,1fr))] gap-px" aria-label="最近 30 次延迟趋势">
      {padded.map((_, index) => (
        <span key={`empty-${index}`} className="rounded-[1px] bg-muted" />
      ))}
      {values.map((sample, index) => {
        const latency = sample.latency_ms
        const state = latency === null ? null : health(latency, 100, 300)
        return (
          <span
            key={`${sample.updated_at}-${index}`}
            title={latency === null ? '无数据' : `${Math.round(latency)} ms`}
            className={cn(
              'rounded-[1px] bg-muted',
              state === 'normal' && 'bg-success',
              state === 'warning' && 'bg-warning',
              state === 'critical' && 'bg-destructive',
            )}
          />
        )
      })}
    </div>
  )
}

function ServerActions({
  server,
  onEdit,
  onRepair,
  onRotateToken,
  onUpgrade,
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
  onOpen,
  ...actions
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'loading' | 'timezone'> & {
  server: Server
  samples: ServerMetrics[]
  onOpen: () => void
}) {
  const metrics = server.metrics
  const memoryPercent = metrics ? percent(metrics.mem_used, metrics.mem_total) : 0
  const diskPercent = metrics ? percent(metrics.disk_used, metrics.disk_total) : 0
  const cpuPercent = metrics?.cpu_percent ?? null
  const latency = metrics?.latency_ms ?? null

  return (
    <Card
      size="sm"
      role="button"
      tabIndex={0}
      aria-label={`查看 ${server.alias} 监控详情`}
      className={cn(
        'cursor-pointer transition-[box-shadow,opacity] hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        !server.online && 'opacity-60',
      )}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') onOpen()
      }}
    >
      <CardHeader className="border-b">
        <CardTitle className="flex min-w-0 items-center gap-2">
          <span className={cn('size-2 shrink-0 rounded-full bg-muted-foreground', server.online && 'bg-success')} />
          <span className="truncate">{server.alias}</span>
        </CardTitle>
        <CardDescription className="flex items-center gap-2 text-xs">
          <CountryFlag code={server.country_code} />
          <span className="truncate">{server.location || server.country_code || '未设置地区'}</span>
        </CardDescription>
        <CardAction>
          <ServerActions server={server} {...actions} />
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
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
                  <span className="flex min-w-0 items-center gap-1 truncate text-success">
                    <ArrowUpIcon className="size-3" />
                    {formatRate(metrics.network_tx_bps)}
                  </span>
                  <span className="flex min-w-0 items-center gap-1 truncate text-info">
                    <ArrowDownIcon className="size-3" />
                    {formatRate(metrics.network_rx_bps)}
                  </span>
                </div>
              </div>
            </div>
            <Separator />
            <div className="grid grid-cols-[88px_1fr] items-end gap-3">
              <div className="flex flex-col gap-1">
                <span className="text-xs text-muted-foreground">Agent 延迟</span>
                <span className="text-base font-semibold tabular-nums">
                  {latency === null ? '--' : `${Math.round(latency)} ms`}
                </span>
              </div>
              <div className="flex flex-col gap-1">
                <span className="text-xs text-muted-foreground">最近 30 次</span>
                <LatencyStrip samples={samples} />
              </div>
            </div>
          </>
        ) : (
          <div className="flex min-h-48 flex-col items-center justify-center gap-2 text-center">
            <ServerCogIcon className="size-6 text-muted-foreground" />
            <span className="text-sm text-muted-foreground">
              {server.online ? '等待 Agent 首次遥测' : '服务器尚未连接'}
            </span>
          </div>
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

function TrendChart({ title, unit, series }: { title: string; unit: string; series: ChartSeries[] }) {
  const values = series.flatMap((item) => item.values.filter((value): value is number => value !== null))
  const max = Math.max(1, ...values)
  const width = 480
  const height = 96
  const points = (items: Array<number | null>) =>
    items
      .map((value, index) => {
        if (value === null) return null
        const x = items.length <= 1 ? 0 : (index / (items.length - 1)) * width
        const y = height - (value / max) * (height - 8) - 4
        return `${x.toFixed(1)},${y.toFixed(1)}`
      })
      .filter(Boolean)
      .join(' ')
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium">{title}</h3>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {series.map((item) => (
            <span key={item.label} className="flex items-center gap-1">
              <span
                className={cn(
                  'size-2 rounded-full',
                  item.color === 'success' && 'bg-success',
                  item.color === 'info' && 'bg-info',
                  item.color === 'warning' && 'bg-warning',
                )}
              />
              {item.label}
            </span>
          ))}
        </div>
      </div>
      <div className="rounded-lg border bg-muted/20 p-2">
        {values.length > 1 ? (
          <svg viewBox={`0 0 ${width} ${height}`} className="h-24 w-full" role="img" aria-label={`${title}趋势`}>
            <path d={`M 0 ${height - 1} H ${width}`} stroke="var(--color-border)" fill="none" />
            {series.map((item) => (
              <polyline
                key={item.label}
                points={points(item.values)}
                fill="none"
                stroke={`var(--color-${item.color})`}
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
              />
            ))}
          </svg>
        ) : (
          <div className="flex h-24 items-center justify-center text-xs text-muted-foreground">暂无趋势数据</div>
        )}
      </div>
      <span className="text-right text-xs text-muted-foreground">{unit}</span>
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
  const tx = history.map((sample) => sample.network_tx_bps)
  const rx = history.map((sample) => sample.network_rx_bps)
  const latency = history.map((sample) => sample.latency_ms)
  const metrics = server?.metrics

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b">
          <SheetTitle className="flex items-center gap-2">
            <span className={cn('size-2 rounded-full bg-muted-foreground', server?.online && 'bg-success')} />
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
                <dt className="text-xs text-muted-foreground">Agent 设置</dt>
                <dd className="mt-1">
                  {server.agent_settings_status === 'synced'
                    ? '已同步'
                    : server.agent_settings_status === 'failed'
                      ? '同步失败'
                      : '待同步'}
                </dd>
              </div>
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
                <TrendChart title="CPU 使用率" unit="%" series={[{ label: 'CPU', color: 'success', values: cpu }]} />
                <TrendChart title="内存使用率" unit="%" series={[{ label: '内存', color: 'success', values: memory }]} />
                <TrendChart title="磁盘使用率" unit="%" series={[{ label: '磁盘', color: 'warning', values: disk }]} />
                <TrendChart
                  title="网络速率"
                  unit="B/s"
                  series={[
                    { label: '上传', color: 'success', values: tx },
                    { label: '下载', color: 'info', values: rx },
                  ]}
                />
                <TrendChart
                  title="Agent 延迟"
                  unit="ms"
                  series={[{ label: '延迟', color: 'info', values: latency }]}
                />
              </div>
            )}
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
      <div className="flex min-h-64 flex-col items-center justify-center rounded-xl border border-dashed text-center">
        <ServerCogIcon className="size-8 text-muted-foreground" />
        <p className="mt-3 text-sm text-muted-foreground">暂无服务器，点击右上角“添加服务器”开始</p>
      </div>
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
            onOpen={() => setSelectedID(server.id)}
            onEdit={props.onEdit}
            onRepair={props.onRepair}
            onRotateToken={props.onRotateToken}
            onUpgrade={props.onUpgrade}
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
