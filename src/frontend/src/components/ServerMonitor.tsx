import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearch } from 'wouter'
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
  SearchIcon,
  ServerCogIcon,
  SparklesIcon,
  Trash2Icon,
  WifiOffIcon,
  WrenchIcon,
} from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { addressFamily } from '@/lib/address'
import { EmptyState, LoadingState } from '@/components/PagePrimitives'
import { ServerTestPanel } from '@/components/ServerTestPanel'
import { trendChartViewBoxPoint } from '@/components/trend-chart-pointer'
import { TrafficStatsDialog } from '@/components/traffic-stats/TrafficStatsDialog'
import { useTrafficStatsDialog } from '@/components/traffic-stats/use-traffic-stats-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
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
import { useAppDialog } from '@/lib/app-dialog'
import { formatByteRate, formatDateTime, humanizeBytes } from '@/lib/format'
import { formatPortRange } from '@/lib/ports'
import { providerWebsiteUrl } from '@/lib/provider-website'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type {
  ConvertedCost,
  Server,
  ServerConnectionState,
  ServerMetrics,
  ServerMetricSeries,
  TrafficStatPoint,
} from '@/lib/types'

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

function openExternalUrl(url: string) {
  window.open(url, '_blank', 'noopener,noreferrer')
}

function convertedRateLabel(cost: ConvertedCost): string {
  const label =
    cost.source === 'identity'
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

function TrafficSegmentBar({
  ratio,
  complete,
  unlimited,
}: {
  ratio: number
  complete: boolean
  unlimited: boolean
}) {
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
    <div className="sv-traffic" aria-label="本周期流量">
      <div className="sv-traffic-line">
        <span className={cn('sv-traffic-used', alert && (ratio >= 80 ? 'is-crit' : 'is-warn'))}>
          {formatTrafficBytes(traffic.used_bytes)}
        </span>
        <span className="sv-traffic-quota">
          <span aria-label={traffic.quota_bytes === null ? '无限额度' : undefined}>
            {traffic.quota_bytes === null ? '∞' : formatTrafficBytes(traffic.quota_bytes)}
          </span>
          <span className="sv-traffic-reset">· {traffic.next_reset_on} 重置</span>
        </span>
      </div>
      <TrafficSegmentBar
        ratio={ratio}
        complete={traffic.complete}
        unlimited={traffic.quota_bytes === null}
      />
      {!traffic.complete ? <span className="sv-traffic-note">本周期数据不完整</span> : null}
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

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分钟`
}

function UsageCell({
  label,
  value,
  detail,
  warning = 80,
  critical = 90,
}: {
  label: string
  value: number | null
  detail: string
  warning?: number
  critical?: number
}) {
  const state = value === null ? null : health(value, warning, critical)
  return (
    <div className="sv-usage">
      <span className="sv-usage-value">{value === null ? '--' : `${value.toFixed(1)}%`}</span>
      <span
        className="sv-usage-bar"
        role="progressbar"
        aria-label={`${label} ${value === null ? '无数据' : `${value.toFixed(1)}%`}`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value === null ? undefined : Math.round(value)}
      >
        <i
          aria-hidden="true"
          className={cn(
            'sv-usage-fill',
            state === 'warning' && 'is-warn',
            state === 'critical' && 'is-crit',
          )}
          style={{ width: `${value ?? 0}%` }}
        />
      </span>
      <span className="sv-usage-detail">{detail}</span>
    </div>
  )
}

function RuntimeMetric({
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
  const state = progress === null ? null : health(progress, warning, critical)
  return (
    <div className="sv-metric">
      <span className="sv-metric-label">{label}</span>
      <span className="sv-metric-value">{value}</span>
      <span
        className="sv-metric-bar"
        role="progressbar"
        aria-label={`${label} ${value}`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={progress === null ? undefined : Math.round(progress)}
      >
        <i
          aria-hidden="true"
          className={cn(
            'sv-metric-fill',
            state === 'warning' && 'is-warn',
            state === 'critical' && 'is-crit',
          )}
          style={{ width: `${progress ?? 0}%` }}
        />
      </span>
      <span className="sv-metric-detail">{detail}</span>
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
      className="sv-lat-strip"
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
        setActiveIndex((current) =>
          Math.min(cells.length - 1, Math.max(0, (current ?? cells.length - 1) + direction)),
        )
      }}
    >
      {cells.map((sample, index) => {
        const latency = sample?.latency_ms ?? null
        const latencyValue = latency ?? 0
        const missing = sample === null
        const timedOut = !missing && latency === null
        const state = missing || timedOut ? null : health(latencyValue, 100, 300)
        const distance =
          activeIndex === null ? Number.POSITIVE_INFINITY : Math.abs(activeIndex - index)
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
              'sv-lat-cell',
              distance === 0 && 'is-active',
              distance === 1 && 'is-near',
            )}
          >
            {activeIndex === index ? (
              <span
                role="tooltip"
                className={cn(
                  'sv-lat-tip',
                  index < 3 ? 'is-start' : index >= cells.length - 3 ? 'is-end' : 'is-center',
                )}
              >
                <span
                  className={cn(
                    'sv-lat-tip-value',
                    timedOut && 'is-crit',
                    state === 'normal' && 'is-ok',
                    state === 'warning' && 'is-warn',
                    state === 'critical' && 'is-crit',
                  )}
                >
                  {missing ? '无数据' : timedOut ? '探测超时' : `${Math.round(latencyValue)} ms`}
                </span>
                <span className="sv-lat-tip-time">
                  {sample ? formatDateTime(sample.updated_at, timezone) : '尚无采样记录'}
                </span>
              </span>
            ) : null}
            <span
              aria-hidden="true"
              className={cn(
                'sv-lat-fill',
                timedOut && 'is-crit',
                state === 'normal' && 'is-ok',
                state === 'warning' && 'is-warn',
                state === 'critical' && 'is-crit',
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
          {server.billing.enabled &&
          ['due_today', 'assumed_valid', 'expired'].includes(server.billing.status) ? (
            <DropdownMenuItem onClick={() => onRenew(server)}>
              <CalendarCheckIcon />
              续费确认
            </DropdownMenuItem>
          ) : null}
          {server.billing.provider?.website_url ? (
            <DropdownMenuItem
              onClick={() => {
                const url = providerWebsiteUrl(server.billing.provider?.website_url)
                if (url) openExternalUrl(url)
              }}
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

function QuotaCell({ server }: { server: Server }) {
  const traffic = server.traffic_plan
  const unlimited = traffic.quota_bytes === null
  const ratio = traffic.quota_bytes ? (traffic.used_bytes / traffic.quota_bytes) * 100 : 0
  const clamped = Math.min(100, Math.max(0, ratio))
  const tone = !traffic.complete
    ? null
    : clamped >= 80
      ? 'is-crit'
      : clamped >= 60
        ? 'is-warn'
        : null
  return (
    <div className="sv-quota">
      <span className="sv-quota-line">
        <span className={cn('sv-quota-used', tone)}>{formatTrafficBytes(traffic.used_bytes)}</span>
        <span className="sv-quota-cap">
          <span aria-label={unlimited ? '无限额度' : undefined}>
            {unlimited ? '∞' : formatTrafficBytes(traffic.quota_bytes ?? 0)}
          </span>
        </span>
      </span>
      <span
        className="sv-quota-bar"
        role="progressbar"
        aria-label="流量额度使用率"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={unlimited ? undefined : Math.round(clamped)}
        aria-valuetext={unlimited ? '无限额度' : undefined}
      >
        <i
          aria-hidden="true"
          className={cn('sv-quota-fill', tone)}
          style={{ width: `${unlimited ? 100 : clamped}%` }}
        />
      </span>
      {!traffic.complete ? <span className="sv-cell-note">本周期数据不完整</span> : null}
    </div>
  )
}

function ServerRow({
  server,
  selected,
  timezone,
  onSelect,
  ...actions
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'loading' | 'timezone'> & {
  server: Server
  selected: boolean
  timezone?: string
  onSelect: () => void
}) {
  const metrics = server.metrics
  const online = isServerOnline(server)
  const transitioning =
    server.connection_state === 'connecting' || server.connection_state === 'reconnecting'
  const publicAddress = server.address || server.learned_addr
  const memoryPercent = metrics ? percent(metrics.mem_used, metrics.mem_total) : 0
  const diskPercent = metrics ? percent(metrics.disk_used, metrics.disk_total) : 0
  const cpuPercent = metrics?.cpu_percent ?? null
  const latency = metrics?.latency_ms ?? null
  const billingLabel =
    server.billing.enabled && server.billing.status in billingStatusLabel
      ? billingStatusLabel[server.billing.status as keyof typeof billingStatusLabel]
      : null
  return (
    <tr
      className={cn('sv-row', selected && 'is-selected')}
      data-state={selected ? 'selected' : undefined}
      aria-selected={selected}
      onClick={onSelect}
    >
      <th scope="row" className="sv-cell-server">
        <button
          type="button"
          className="sv-row-select"
          aria-pressed={selected}
          aria-label={`查看 ${server.alias} 的运行时读数，${serverConnectionLabel(server.connection_state)}`}
          onClick={(event) => {
            event.stopPropagation()
            onSelect()
          }}
        >
          <CountryFlag code={server.country_code} />
          <span className="sv-row-alias">{server.alias}</span>
        </button>
        <span className="sv-row-flags">
          {server.config_drift ? <span className="sv-flag is-alert">DRIFT</span> : null}
          {billingLabel ? (
            <span
              className={cn(
                'sv-flag',
                server.billing.status === 'expired' && 'is-alert',
                server.billing.status === 'due_today' && 'is-warn',
              )}
            >
              {billingLabel}
            </span>
          ) : null}
        </span>
      </th>
      <td className="sv-cell-agent">
        <span
          className={cn(
            'sv-agent',
            online ? 'is-online' : transitioning ? 'is-loading' : 'is-offline',
          )}
        >
          {online ? (
            <CircleCheckIcon />
          ) : transitioning ? (
            <LoaderCircleIcon className="sv-spin" />
          ) : (
            <WifiOffIcon />
          )}
          <span>Agent {serverConnectionLabel(server.connection_state)}</span>
        </span>
        <span className="sv-cell-note">
          {online
            ? metrics
              ? `已运行 ${formatUptime(metrics.uptime_seconds)}`
              : '等待首次遥测'
            : metrics
              ? `上次遥测 ${formatDateTime(metrics.updated_at, timezone)}`
              : serverConnectionNotice(server.connection_state)}
        </span>
      </td>
      <td className="sv-cell-addr">
        <span className="sv-addr-loc">
          {server.location || server.country_code || '未设置地区'}
        </span>
        <span
          className="sv-addr"
          title={publicAddress || '公网地址待学习'}
          aria-label={`公网地址 ${publicAddress || '待学习'}`}
        >
          <Globe2Icon />
          <span className="sv-addr-text">{publicAddress || '公网地址待学习'}</span>
          {server.addresses.length > 1 ? (
            <span className="sv-addr-count" title={`共 ${server.addresses.length} 个公网地址`}>
              +{server.addresses.length - 1}
            </span>
          ) : null}
        </span>
      </td>
      <td className="sv-cell-metric">
        <UsageCell
          label="CPU"
          value={cpuPercent}
          detail={
            metrics
              ? `${metrics.load1.toFixed(2)} / ${metrics.load5.toFixed(2)} / ${metrics.load15.toFixed(2)}`
              : '--'
          }
        />
      </td>
      <td className="sv-cell-metric">
        <UsageCell
          label="内存"
          value={metrics ? memoryPercent : null}
          detail={
            metrics
              ? `${humanizeBytes(metrics.mem_used)} / ${humanizeBytes(metrics.mem_total)}`
              : '--'
          }
        />
      </td>
      <td className="sv-cell-metric">
        <UsageCell
          label="磁盘"
          value={metrics ? diskPercent : null}
          detail={
            metrics
              ? `${humanizeBytes(metrics.disk_used)} / ${humanizeBytes(metrics.disk_total)}`
              : '--'
          }
        />
      </td>
      <td className="sv-cell-latency">
        <span className="sv-latency-value">
          {latency === null ? '--' : `${Math.round(latency)} ms`}
        </span>
        <span className="sv-cell-note">最近一次探针</span>
      </td>
      <td className="sv-cell-rate">
        <span className="sv-rate">
          <span className="sv-rate-line">
            <ArrowUpIcon />
            {metrics ? formatByteRate(metrics.network_tx_bps) : '--'}
          </span>
          <span className="sv-rate-line">
            <ArrowDownIcon />
            {metrics ? formatByteRate(metrics.network_rx_bps) : '--'}
          </span>
        </span>
      </td>
      <td className="sv-cell-quota">
        <QuotaCell server={server} />
      </td>
      <td className="sv-cell-versions">
        <span className="sv-versions">
          <span>Agent {server.agent_version ?? '--'}</span>
          <span>Xray {server.xray_version ?? '--'}</span>
        </span>
      </td>
      <td className="sv-cell-actions" onClick={(event) => event.stopPropagation()}>
        <ServerActions server={server} {...actions} />
      </td>
    </tr>
  )
}

function ServerRuntime({
  server,
  samples,
  timezone,
  onOpenDetail,
  onOpenTrafficStats,
}: {
  server: Server
  samples: ServerMetrics[]
  timezone?: string
  onOpenDetail: () => void
  onOpenTrafficStats: () => void
}) {
  const metrics = server.metrics
  const online = isServerOnline(server)
  const cpuPercent = metrics?.cpu_percent ?? null
  const memoryPercent = metrics ? percent(metrics.mem_used, metrics.mem_total) : 0
  const diskPercent = metrics ? percent(metrics.disk_used, metrics.disk_total) : 0
  const latency = metrics?.latency_ms ?? null
  return (
    <section className="sv-runtime" aria-label={`${server.alias} 运行时读数`}>
      <header className="sv-runtime-head">
        <span className="sv-tab">RUNTIME</span>
        <span className="sv-runtime-title">{server.alias}</span>
        <span className={cn('sv-agent', online ? 'is-online' : 'is-offline')}>
          {online ? <CircleCheckIcon /> : <WifiOffIcon />}
          <span>{online ? 'Agent 连接正常' : serverConnectionNotice(server.connection_state)}</span>
        </span>
        <span className="sv-runtime-meta">
          {online && metrics
            ? `已运行 ${formatUptime(metrics.uptime_seconds)} · 遥测 ${formatDateTime(metrics.updated_at, timezone)}`
            : metrics
              ? `上次遥测 ${formatDateTime(metrics.updated_at, timezone)} · 历史数据`
              : '暂无可用遥测'}
        </span>
        <div className="sv-runtime-actions">
          <Button variant="outline" size="sm" onClick={onOpenDetail}>
            详情与历史
          </Button>
          <Button variant="outline" size="sm" onClick={onOpenTrafficStats}>
            流量统计
          </Button>
        </div>
      </header>
      {metrics ? (
        <div className="sv-runtime-body">
          <div className="sv-runtime-metrics">
            <RuntimeMetric
              label="CPU"
              value={cpuPercent === null ? '--' : `${cpuPercent.toFixed(1)}%`}
              detail={`${metrics.load1.toFixed(2)} / ${metrics.load5.toFixed(2)} / ${metrics.load15.toFixed(2)}`}
              progress={cpuPercent}
            />
            <RuntimeMetric
              label="内存"
              value={`${memoryPercent.toFixed(1)}%`}
              detail={`${humanizeBytes(metrics.mem_used)} / ${humanizeBytes(metrics.mem_total)}`}
              progress={memoryPercent}
            />
            <RuntimeMetric
              label="磁盘"
              value={`${diskPercent.toFixed(1)}%`}
              detail={`${humanizeBytes(metrics.disk_used)} / ${humanizeBytes(metrics.disk_total)}`}
              progress={diskPercent}
            />
            <div className="sv-metric">
              <span className="sv-metric-label">网络</span>
              <span className="sv-metric-rate">
                <span>
                  <ArrowUpIcon />
                  {formatByteRate(metrics.network_tx_bps)}
                </span>
                <span>
                  <ArrowDownIcon />
                  {formatByteRate(metrics.network_rx_bps)}
                </span>
              </span>
              <span className="sv-metric-detail">
                累计 ↑ {humanizeBytes(metrics.network_tx_bytes)} · ↓{' '}
                {humanizeBytes(metrics.network_rx_bytes)}
              </span>
            </div>
          </div>
          <div className="sv-runtime-lat">
            <span className="sv-runtime-label">延迟 / 最近 30 次</span>
            <span className="sv-runtime-latvalue">
              {latency === null ? '--' : `${Math.round(latency)} ms`}
            </span>
            <LatencyStrip samples={samples} timezone={timezone} />
          </div>
          <div className="sv-runtime-quota">
            <span className="sv-runtime-label">本周期流量额度</span>
            <TrafficUsage server={server} />
          </div>
          <dl className="sv-runtime-facts">
            <div>
              <dt>地区</dt>
              <dd>{server.location || server.country_code || '-'}</dd>
            </div>
            <div>
              <dt>地址</dt>
              <dd className="sv-mono">{server.address || server.learned_addr || '-'}</dd>
            </div>
            <div>
              <dt>Agent / Xray</dt>
              <dd>
                {server.agent_version ?? '-'} / {server.xray_version ?? '-'}
              </dd>
            </div>
            <div>
              <dt>配置状态</dt>
              <dd>{server.config_drift ? '配置漂移' : '正常'}</dd>
            </div>
            <div>
              <dt>机器类型</dt>
              <dd>
                {server.machine_type === 'direct'
                  ? '直连'
                  : `NAT · ${server.allowed_ports.map(formatPortRange).join(', ') || '仅出口'}`}
              </dd>
            </div>
            {server.billing.enabled ? (
              <div>
                <dt>服务商</dt>
                <dd>{server.billing.provider?.name ?? '-'}</dd>
              </div>
            ) : null}
            {server.billing.enabled ? (
              <div>
                <dt>下次续费</dt>
                <dd>{server.billing.next_renewal_on || '-'}</dd>
              </div>
            ) : null}
          </dl>
        </div>
      ) : (
        <div className="sv-runtime-empty">
          <ServerCogIcon />
          <span>
            {online ? '等待 Agent 首次遥测' : serverConnectionLabel(server.connection_state)}
          </span>
          <TrafficUsage server={server} />
        </div>
      )}
    </section>
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
  const values = series.flatMap((item) =>
    item.values.filter((value): value is number => value !== null),
  )
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
    const point = trendChartViewBoxPoint(bounds, clientX, clientY, width, height)
    setActivePoint({ index: Math.round((point.x / width) * (sampleCount - 1)), y: point.y })
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
                    'size-2 bg-current',
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
          <div className="flex h-24 items-center justify-center text-xs text-muted-foreground">
            暂无趋势数据
          </div>
        )}
        {activePoint ? (
          <div
            className={cn(
              'sv-trend-tip',
              activePoint.index < sampleCount / 2 ? 'is-right' : 'is-left',
            )}
          >
            <div className="mb-1 whitespace-nowrap text-muted-foreground">
              {formatDateTime(timestamps[activePoint.index], timezone)}
            </div>
            {series.map((item) => (
              <div
                key={item.label}
                className="flex items-center justify-between gap-4 tabular-nums"
              >
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
  onOpenTrafficStats,
}: {
  server: Server | null
  open: boolean
  onOpenChange: (open: boolean) => void
  timezone?: string
  onOpenTrafficStats: (server: Server) => void
}) {
  const [history, setHistory] = useState<ServerMetrics[]>([])
  const [loading, setLoading] = useState(false)
  const [trafficHistory, setTrafficHistory] = useState<TrafficStatPoint[]>([])
  const { notify } = useAppDialog()

  const openProviderPage = () => {
    const url = providerWebsiteUrl(server?.billing.provider?.website_url)
    if (url) {
      openExternalUrl(url)
      return
    }
    void notify({
      title: '未设置服务商官网',
      description: '该服务器未绑定服务商或服务商未填写官网，可在服务器编辑或「服务商管理」中设置。',
    })
  }

  const load = useCallback(() => {
    if (!server) return
    setLoading(true)
    api
      .serverMetricHistory(server.id)
      .then(setHistory)
      .catch(() => setHistory([]))
      .finally(() => setLoading(false))
  }, [server])

  // 流量统计历史与探针指标各自轮询（两类采样点不同步，流量图表使用自己的时间轴）。
  const loadTraffic = useCallback(() => {
    if (!server) return
    api
      .statsServerTrafficHistory(server.id)
      .then(setTrafficHistory)
      .catch(() => setTrafficHistory([]))
  }, [server])

  useEffect(() => {
    if (!open || !server) return
    load()
    const timer = setInterval(load, 60000)
    return () => clearInterval(timer)
  }, [load, open, server])

  useEffect(() => {
    if (!open || !server) return
    loadTraffic()
    const timer = setInterval(loadTraffic, 60000)
    return () => clearInterval(timer)
  }, [loadTraffic, open, server])

  const cpu = history.map((sample) => sample.cpu_percent)
  const memory = history.map((sample) => percent(sample.mem_used, sample.mem_total))
  const disk = history.map((sample) => percent(sample.disk_used, sample.disk_total))
  const tx = history.map((sample) =>
    sample.network_tx_bps === null ? null : sample.network_tx_bps / 1024,
  )
  const rx = history.map((sample) =>
    sample.network_rx_bps === null ? null : sample.network_rx_bps / 1024,
  )
  const latency = history.map((sample) => sample.latency_ms)
  const timestamps = history.map((sample) => sample.updated_at)
  // 流量点为每分钟累计字节数：除以 60 秒得 B/s，再除以 1024 得 KB/s。
  const trafficUpRate = trafficHistory.map((point) => point.up / 60 / 1024)
  const trafficDownRate = trafficHistory.map((point) => point.down / 60 / 1024)
  const trafficTimestamps = trafficHistory.map((point) => new Date(point.ts * 1000).toISOString())
  const metrics = server?.metrics

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sv-sheet w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b">
          <SheetTitle className="flex items-center gap-2">
            <span className={cn('sv-state-dot', isServerOnline(server) && 'is-online')} />
            {server?.alias ?? '服务器详情'}
          </SheetTitle>
          <SheetDescription>主机信息与最近 24 小时探针指标</SheetDescription>
          {server ? (
            <Button
              variant="outline"
              size="sm"
              className="mt-1 self-start"
              onClick={openProviderPage}
            >
              <ExternalLinkIcon />
              打开服务商官网
            </Button>
          ) : null}
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
                <dd className="mt-1 font-mono text-xs">
                  {server.address || server.learned_addr || '-'}
                </dd>
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
                <dd className="mt-1">
                  {server.agent_version ?? '-'} / {server.xray_version ?? '-'}
                </dd>
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
                <dd className="mt-1">
                  {server.traffic_plan.quota_bytes === null
                    ? '无限'
                    : formatTrafficBytes(server.traffic_plan.quota_bytes)}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">本周期流量</dt>
                <dd className="mt-1">
                  {formatTrafficBytes(server.traffic_plan.used_bytes)} ·{' '}
                  {server.traffic_plan.accounting_mode === 'bidirectional'
                    ? '双向合计'
                    : server.traffic_plan.accounting_mode === 'max'
                      ? '取较大方向'
                      : '仅出站'}
                </dd>
              </div>
              {server.billing.enabled ? (
                <>
                  <div>
                    <dt className="text-xs text-muted-foreground">服务商</dt>
                    <dd className="mt-1">{server.billing.provider?.name ?? '-'}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">费用</dt>
                    <dd className="mt-1 tabular-nums">
                      {(
                        server.billing.amount_minor /
                        (['JPY', 'KRW', 'ISK'].includes(server.billing.currency) ? 1 : 100)
                      ).toLocaleString()}{' '}
                      {server.billing.currency} / {server.billing.interval_count}{' '}
                      {server.billing.interval_unit === 'day'
                        ? '天'
                        : server.billing.interval_unit === 'year'
                          ? '年'
                          : '月'}
                    </dd>
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
                        {formatConvertedCost(server.billing.public_converted)} ·{' '}
                        {convertedRateLabel(server.billing.public_converted)}
                      </span>
                    </div>
                    {server.billing.custom_converted ? (
                      <div>
                        <span className="block text-xs text-muted-foreground">自定义汇率结果</span>
                        <span className="mt-0.5 block tabular-nums">
                          {formatConvertedCost(server.billing.custom_converted)} ·{' '}
                          {convertedRateLabel(server.billing.custom_converted)}
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
                {Array.from({ length: 6 }).map((_, index) => (
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
                <div className="flex flex-col gap-2">
                  <div className="flex justify-end">
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`查看 ${server.alias} 流量统计`}
                      onClick={() => onOpenTrafficStats(server)}
                    >
                      流量统计
                    </Button>
                  </div>
                  <TrendChart
                    title="流量速率"
                    unit="KB/s"
                    series={[
                      { label: '上传', color: 'success', values: trafficUpRate },
                      { label: '下载', color: 'info', values: trafficDownRate },
                    ]}
                    timestamps={trafficTimestamps}
                    timezone={timezone}
                  />
                </div>
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
  const searchString = useSearch()
  const urlSelId = useMemo(() => {
    const raw = new URLSearchParams(searchString).get('sel')
    return raw && /^\d+$/.test(raw) ? Number(raw) : null
  }, [searchString])
  const [selectedID, setSelectedID] = useState<number | null>(urlSelId)
  const [detailServerID, setDetailServerID] = useState<number | null>(null)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<'all' | 'online' | 'offline' | 'drift'>('all')

  useEffect(() => {
    if (urlSelId !== null) setSelectedID(urlSelId)
  }, [urlSelId])

  // onError 以 useCallback 固定引用，避免 useTrafficStatsDialog 因回调变化重拉（同 AgentsStatsTab）；
  // 错误仅在弹窗打开期间展示，下次打开时在点击事件里清除。
  const [trafficStatsError, setTrafficStatsError] = useState('')
  const onTrafficStatsError = useCallback((message: string) => setTrafficStatsError(message), [])
  const trafficStatsDialog = useTrafficStatsDialog({ onError: onTrafficStatsError })

  const samplesByServer = useMemo(
    () => new Map(props.samples.map((series) => [series.server_id, series.samples])),
    [props.samples],
  )

  const onlineCount = props.servers.filter((server) => isServerOnline(server)).length
  const offlineCount = props.servers.length - onlineCount
  const driftCount = props.servers.filter((server) => server.config_drift).length

  const filteredServers = useMemo(() => {
    const q = query.trim().toLowerCase()
    return props.servers.filter((server) => {
      const online = isServerOnline(server)
      if (filter === 'online' && !online) return false
      if (filter === 'offline' && online) return false
      if (filter === 'drift' && !server.config_drift) return false
      if (q) {
        const hay = [
          server.alias,
          server.location,
          server.country_code,
          server.address,
          server.learned_addr,
          ...(server.tags ?? []),
          server.billing.provider?.name ?? '',
        ]
          .join(' ')
          .toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [filter, props.servers, query])

  // 默认选中第一台，让读数表下方始终有可读的运行时模块；点击任意行就地切换。
  const selected =
    props.servers.find((server) => server.id === selectedID) ??
    filteredServers[0] ??
    props.servers[0] ??
    null
  const detailServer = props.servers.find((server) => server.id === detailServerID) ?? null

  const openTrafficStats = (server: Server) => {
    setTrafficStatsError('')
    trafficStatsDialog.open({ kind: 'server', serverId: server.id, serverName: server.alias })
  }

  if (props.loading) {
    return (
      <section className="sv-board">
        <header className="sv-board-head">
          <span className="sv-tab">FLEET</span>
          <span className="sv-board-title">服务器读数</span>
          <span className="sv-board-meta">同步中…</span>
        </header>
        <LoadingState className="sv-board-state" />
      </section>
    )
  }

  if (props.servers.length === 0) {
    return (
      <section className="sv-board">
        <header className="sv-board-head">
          <span className="sv-tab">FLEET</span>
          <span className="sv-board-title">服务器读数</span>
          <span className="sv-board-meta">0 台</span>
        </header>
        <EmptyState
          className="sv-board-state"
          icon={<ServerCogIcon />}
          description="暂无服务器，点击右上角“添加服务器”开始"
        />
      </section>
    )
  }

  return (
    <>
      {trafficStatsDialog.scope !== null && trafficStatsError ? (
        <p role="alert" className="sv-board-alert">
          流量统计加载失败：{trafficStatsError}
        </p>
      ) : null}
      <section className="sv-board">
        <header className="sv-board-head">
          <span className="sv-tab">FLEET</span>
          <span className="sv-board-title">服务器读数</span>
          <span className="sv-board-meta">
            {onlineCount} 在线 / {props.servers.length} 台
          </span>
        </header>
        <div className="sv-board-toolbar">
          <div className="cg-search-wrap">
            <SearchIcon />
            <input
              type="search"
              className="cg-search-input"
              placeholder="搜索服务器别名、地区、公网地址、服务商…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="搜索服务器"
            />
          </div>
          <div className="cg-filter-pills" role="group" aria-label="服务器状态筛选">
            {(
              [
                { id: 'all', label: '全部', count: props.servers.length },
                { id: 'online', label: '在线', count: onlineCount },
                { id: 'offline', label: '离线/异常', count: offlineCount },
                { id: 'drift', label: '配置漂移', count: driftCount },
              ] as const
            ).map((item) => (
              <button
                key={item.id}
                type="button"
                className="cg-filter-pill"
                data-active={filter === item.id ? 'true' : 'false'}
                onClick={() => setFilter(item.id)}
              >
                <span>{item.label}</span>
                <span className="cg-filter-pill-count">{item.count}</span>
              </button>
            ))}
          </div>
        </div>
        {filteredServers.length === 0 ? (
          <EmptyState
            className="sv-board-state"
            icon={<ServerCogIcon />}
            title="未找到匹配的服务器"
            description="尝试调整搜索词或切换上方状态筛选"
          />
        ) : (
          <div className="sv-table-scroll">
            <table className="sv-table">
              <thead>
                <tr>
                  <th scope="col">服务器</th>
                  <th scope="col">状态</th>
                  <th scope="col">地区 / 地址</th>
                  <th scope="col">CPU</th>
                  <th scope="col">内存</th>
                  <th scope="col">磁盘</th>
                  <th scope="col">延迟</th>
                  <th scope="col">速率 ↑ / ↓</th>
                  <th scope="col">流量额度</th>
                  <th scope="col">版本</th>
                  <th scope="col">
                    <span className="sr-only">操作</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {filteredServers.map((server) => (
                  <ServerRow
                    key={server.id}
                    server={server}
                    selected={selected?.id === server.id}
                    timezone={props.timezone}
                    onSelect={() => setSelectedID(server.id)}
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
              </tbody>
            </table>
          </div>
        )}
        {selected ? (
          <ServerRuntime
            server={selected}
            samples={samplesByServer.get(selected.id) ?? []}
            timezone={props.timezone}
            onOpenDetail={() => setDetailServerID(selected.id)}
            onOpenTrafficStats={() => openTrafficStats(selected)}
          />
        ) : null}
      </section>
      <TrafficStatsDialog controller={trafficStatsDialog} />
      <DetailSheet
        server={detailServer}
        open={detailServer !== null}
        onOpenChange={(open) => {
          if (!open) setDetailServerID(null)
        }}
        timezone={props.timezone}
        onOpenTrafficStats={openTrafficStats}
      />
    </>
  )
}
