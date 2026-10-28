import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useSearch } from 'wouter'
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowDownUpIcon,
  ArrowLeftRightIcon,
  ArrowUpIcon,
  Building2Icon,
  CalendarCheckIcon,
  CableIcon,
  ClockIcon,
  CpuIcon,
  CreditCardIcon,
  EllipsisIcon,
  ExternalLinkIcon,
  GaugeIcon,
  Globe2Icon,
  HardDriveIcon,
  LoaderCircleIcon,
  Maximize2Icon,
  MemoryStickIcon,
  NetworkIcon,
  PencilIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  RotateCcwKeyIcon,
  SearchIcon,
  ServerCogIcon,
  SettingsIcon,
  ShieldCheckIcon,
  SparklesIcon,
  Trash2Icon,
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
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatByteRate, formatDateTime, humanizeBytes } from '@/lib/format'
import { formatPortRange } from '@/lib/ports'
import { providerWebsiteUrl } from '@/lib/provider-website'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type {
  CatalogProbeRow,
  CatalogProbeStats,
  ConvertedCost,
  Server,
  ServerMetrics,
  ServerMetricSeries,
  TrafficStatPoint,
} from '@/lib/types'

interface ServerMonitorProps {
  servers: Server[]
  samples: ServerMetricSeries[]
  probes: CatalogProbeStats | null
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

function percent(used: number, total: number): number {
  return total > 0 ? Math.min(100, Math.max(0, (used / total) * 100)) : 0
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分钟`
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
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'probes' | 'loading' | 'timezone'> & {
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

// ---------- 服务器卡片（§三网测速） ----------

function formatLatency(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—'
  if (ms >= 100) return `${Math.round(ms)} ms`
  return `${ms.toFixed(1)} ms`
}

function lossTone(loss: number | null): 'ok' | 'warn' | 'crit' | null {
  if (loss === null) return null
  if (loss <= 5) return 'ok'
  if (loss <= 30) return 'warn'
  return 'crit'
}

function daysUntil(dateStr: string): number {
  const target = new Date(`${dateStr}T00:00:00`)
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return Math.round((target.getTime() - today.getTime()) / 86400000)
}

// serverBanFlag 渲染 Ban 告警徽标（§告警分类）：banned 用警示色，疑似/解除中用提醒色。
function serverBanFlag(server: Server): { label: string; tone: 'is-alert' | 'is-warn' } | null {
  switch (server.ban_watch?.state) {
    case 'banned':
      return { label: '已被 BAN', tone: 'is-alert' }
    case 'suspected':
      return { label: '疑似 BAN', tone: 'is-warn' }
    case 'recovering':
      return { label: 'BAN 解除中', tone: 'is-warn' }
    default:
      return null
  }
}

// DotBar 是资源占用的分段点阵条（20 段）：内存/磁盘/负载/流量共用。
function DotBar({
  ratio,
  tone,
  label,
}: {
  ratio: number
  tone: 'ok' | 'warn' | 'crit' | 'info' | 'signal'
  label: string
}) {
  const segments = 20
  const clamped = Math.min(100, Math.max(0, ratio))
  const filled = clamped === 0 ? 0 : Math.max(1, Math.round((clamped / 100) * segments))
  return (
    <span
      className="sv-dotbar"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(clamped)}
    >
      {Array.from({ length: segments }).map((_, index) => (
        <i key={index} className={cn('sv-dotbar-seg', index < filled && `is-${tone}`)} />
      ))}
    </span>
  )
}

// DotTrend 是速率的点阵趋势（每点一次采样，亮度随数值相对峰值变化）。
function DotTrend({ values, tone }: { values: Array<number | null>; tone: 'ok' | 'info' }) {
  const recent = values.slice(-24)
  const peak = Math.max(0, ...recent.filter((value): value is number => value !== null))
  const padded: Array<number | null> = [
    ...Array.from<null>({ length: Math.max(0, 24 - recent.length) }).fill(null),
    ...recent,
  ]
  return (
    <span className="sv-dot-trend" aria-hidden="true">
      {padded.map((value, index) => (
        <i
          key={index}
          className={cn('sv-dot-cell', `is-${tone}`, value === null && 'is-idle')}
          style={
            value === null || peak <= 0
              ? undefined
              : { opacity: 0.35 + 0.65 * Math.min(1, value / peak) }
          }
        />
      ))}
    </span>
  )
}

function ResCell({
  icon,
  label,
  value,
  detail,
  barRatio,
  barTone = 'ok',
}: {
  icon: ReactNode
  label: string
  value: string
  detail: string
  barRatio: number | null
  barTone?: 'ok' | 'warn' | 'crit' | 'info' | 'signal'
}) {
  return (
    <div className="sv-res">
      <span className="sv-res-top">
        <span className="sv-res-label">
          {icon}
          {label}
        </span>
        <span className="sv-res-value">{value}</span>
      </span>
      <span className="sv-res-detail">{detail}</span>
      {barRatio !== null ? <DotBar ratio={barRatio} tone={barTone} label={`${label}占用`} /> : null}
    </div>
  )
}

// ProbeRow 是三网测速的一行：目标标签 / 延迟 / 丢包条 / 丢包率。
function ProbeRow({ row }: { row: CatalogProbeRow }) {
  const tone = lossTone(row.loss_percent)
  const loss = row.loss_percent
  const width = loss === null ? 0 : loss <= 0 ? 0 : Math.max(4, Math.min(100, loss))
  return (
    <div
      className="sv-probe-row"
      title={`${row.label}${row.latency_ms !== null ? ` · 延迟 ${formatLatency(row.latency_ms)}` : ''}${
        loss === null ? '' : ` · 丢包 ${loss.toFixed(1)}%`
      }`}
    >
      <span className="sv-probe-label">{row.label}</span>
      <span className="sv-probe-latency">{formatLatency(row.latency_ms)}</span>
      <span
        className="sv-probe-lossbar"
        role="img"
        aria-label={`${row.label} 丢包率 ${loss === null ? '无数据' : `${loss.toFixed(1)}%`}`}
      >
        <i
          aria-hidden="true"
          className={cn(
            'sv-probe-lossfill',
            tone === 'ok' && 'is-ok',
            tone === 'warn' && 'is-warn',
            tone === 'crit' && 'is-crit',
          )}
          style={{ width: `${width}%` }}
        />
      </span>
      <span
        className={cn(
          'sv-probe-loss',
          tone === 'ok' && 'is-ok',
          tone === 'warn' && 'is-warn',
          tone === 'crit' && 'is-crit',
        )}
      >
        {loss === null ? '—' : `${loss.toFixed(1)}%`}
      </span>
    </div>
  )
}

// ---------- 卡片 / 详情弹窗共用的概览区（§服务器详情复用卡片样式） ----------

// CardOverviewSections 渲染资源四宫格、网络速率与剩余流量三段。
function CardOverviewSections({ server, samples }: { server: Server; samples: ServerMetrics[] }) {
  const metrics = server.metrics
  const cpuPercent = metrics?.cpu_percent ?? null
  const memoryPercent = metrics ? percent(metrics.mem_used, metrics.mem_total) : null
  const diskPercent = metrics ? percent(metrics.disk_used, metrics.disk_total) : null
  const load1 = metrics?.load1 ?? null
  const traffic = server.traffic_plan
  const quotaRatio = traffic.quota_bytes ? (traffic.used_bytes / traffic.quota_bytes) * 100 : 0
  const remaining =
    traffic.quota_bytes === null ? null : Math.max(0, traffic.quota_bytes - traffic.used_bytes)
  const quotaTone = !traffic.complete
    ? 'ok'
    : quotaRatio >= 80
      ? 'crit'
      : quotaRatio >= 60
        ? 'warn'
        : 'ok'
  return (
    <>
      <div className="sv-card-res">
        <ResCell
          icon={<CpuIcon />}
          label="CPU"
          value={cpuPercent === null ? '--' : `${cpuPercent.toFixed(1)}%`}
          detail={metrics ? `Agent ${server.agent_version ?? '--'}` : '--'}
          barRatio={cpuPercent}
          barTone="ok"
        />
        <ResCell
          icon={<MemoryStickIcon />}
          label="内存"
          value={memoryPercent === null ? '--' : `${memoryPercent.toFixed(1)}%`}
          detail={
            metrics
              ? `${humanizeBytes(metrics.mem_used)} / ${humanizeBytes(metrics.mem_total)}`
              : '--'
          }
          barRatio={memoryPercent}
          barTone="info"
        />
        <ResCell
          icon={<HardDriveIcon />}
          label="磁盘"
          value={diskPercent === null ? '--' : `${diskPercent.toFixed(1)}%`}
          detail={
            metrics
              ? `${humanizeBytes(metrics.disk_used)} / ${humanizeBytes(metrics.disk_total)}`
              : '--'
          }
          barRatio={diskPercent}
          barTone="signal"
        />
        <ResCell
          icon={<GaugeIcon />}
          label="负载"
          value={load1 === null ? '--' : load1.toFixed(2)}
          detail={
            metrics ? `5/15 分钟 ${metrics.load5.toFixed(2)} / ${metrics.load15.toFixed(2)}` : '--'
          }
          barRatio={load1 === null ? null : Math.min(100, load1 * 20)}
          barTone="ok"
        />
      </div>
      <section className="sv-card-net" aria-label="网络速率">
        <div className="sv-net-row">
          <span className="sv-net-label">
            <ArrowUpIcon />
            上行
          </span>
          <span className="sv-net-rate">
            {metrics?.network_tx_bps != null ? formatByteRate(metrics.network_tx_bps) : '--'}
          </span>
          <DotTrend values={samples.map((sample) => sample.network_tx_bps)} tone="ok" />
        </div>
        <div className="sv-net-row">
          <span className="sv-net-label">
            <ArrowDownIcon />
            下行
          </span>
          <span className="sv-net-rate">
            {metrics?.network_rx_bps != null ? formatByteRate(metrics.network_rx_bps) : '--'}
          </span>
          <DotTrend values={samples.map((sample) => sample.network_rx_bps)} tone="info" />
        </div>
        <div className="sv-net-totals">
          <span className="sv-net-total">
            <Globe2Icon />
            出站 {metrics ? humanizeBytes(metrics.network_tx_bytes) : '--'}
          </span>
          <span className="sv-net-total">
            <Globe2Icon />
            入站 {metrics ? humanizeBytes(metrics.network_rx_bytes) : '--'}
          </span>
        </div>
      </section>
      <section className="sv-card-quota" aria-label="流量额度">
        <span className="sv-quota2-line">
          <span className="sv-quota2-label">剩余流量</span>
          <span className="sv-quota2-remaining">
            {remaining === null ? '∞' : formatTrafficBytes(remaining)}
          </span>
        </span>
        <DotBar
          ratio={traffic.quota_bytes === null ? 100 : quotaRatio}
          tone={quotaTone}
          label="流量额度使用率"
        />
        <span className="sv-quota2-detail">
          {formatTrafficBytes(traffic.used_bytes)} /{' '}
          {traffic.quota_bytes === null ? '∞' : formatTrafficBytes(traffic.quota_bytes)}
          {traffic.next_reset_on ? ` · ${traffic.next_reset_on} 重置` : ''}
        </span>
      </section>
    </>
  )
}

// CardProbesSection 渲染三网测速区（卡片与详情弹窗共用）。
function CardProbesSection({ rows, title }: { rows: CatalogProbeRow[]; title: string | null }) {
  return (
    <section className="sv-card-probes" aria-label="三网测速">
      <span className="sv-probe-head">
        三网测速
        {title ? <span className="sv-probe-region"> · {title}</span> : null}
      </span>
      {rows.length > 0 ? (
        <div className="sv-probe-rows">
          {rows.map((row) => (
            <ProbeRow key={`${row.carrier}:${row.label}`} row={row} />
          ))}
        </div>
      ) : (
        <span className="sv-probe-empty">{title ? '暂无测速数据' : '测速目录未配置'}</span>
      )}
    </section>
  )
}

// FactItem 是详情弹窗信息区的单条事实（图标 + 标签 + 值）。
function FactItem({
  icon,
  label,
  children,
}: {
  icon: ReactNode
  label: string
  children: ReactNode
}) {
  return (
    <div className="sv-fact-item">
      <dt className="sv-fact-label">
        {icon}
        {label}
      </dt>
      <dd className="sv-fact-text">{children}</dd>
    </div>
  )
}

function ServerCard({
  server,
  probeRows,
  probeTitle,
  samples,
  selected,
  timezone,
  onSelect,
  onOpenDetail,
  ...actions
}: Omit<ServerMonitorProps, 'servers' | 'samples' | 'probes' | 'loading' | 'timezone'> & {
  server: Server
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
  samples: ServerMetrics[]
  selected: boolean
  timezone?: string
  onSelect: () => void
  onOpenDetail: () => void
}) {
  const metrics = server.metrics
  const online = isServerOnline(server)
  const transitioning =
    server.connection_state === 'connecting' || server.connection_state === 'reconnecting'
  const families = new Set(server.addresses.map((addr) => addressFamily(addr)))
  const renewalDays =
    server.billing.enabled && server.billing.next_renewal_on
      ? daysUntil(server.billing.next_renewal_on)
      : null
  const renewalTone =
    renewalDays === null ? null : renewalDays < 0 ? 'crit' : renewalDays <= 7 ? 'warn' : 'ok'
  const banFlag = serverBanFlag(server)
  return (
    <article
      className={cn('sv-card', selected && 'is-selected', !online && 'is-offline')}
      data-state={selected ? 'selected' : undefined}
      aria-selected={selected}
      onClick={onSelect}
    >
      <header className="sv-card-head">
        {transitioning ? (
          <LoaderCircleIcon
            className="sv-spin sv-card-conn"
            aria-label={serverConnectionLabel(server.connection_state)}
          />
        ) : (
          <span
            className={cn('sv-state-dot sv-card-conn', online && 'is-online')}
            title={serverConnectionLabel(server.connection_state)}
          />
        )}
        <CountryFlag code={server.country_code} />
        <button
          type="button"
          className="sv-card-title"
          aria-pressed={selected}
          aria-label={`选择 ${server.alias}，${serverConnectionLabel(server.connection_state)}`}
          onClick={(event) => {
            event.stopPropagation()
            onSelect()
          }}
        >
          {server.alias}
        </button>
        <span className="sv-card-head-actions" onClick={(event) => event.stopPropagation()}>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`查看 ${server.alias} 详情与历史`}
            onClick={() => onOpenDetail()}
          >
            <Maximize2Icon />
          </Button>
          <ServerActions server={server} {...actions} />
        </span>
      </header>
      <div className="sv-card-badges">
        <span className="sv-flag">{server.location || server.country_code || '未设置地区'}</span>
        {families.has('ipv4') ? <span className="sv-flag is-v4">V4</span> : null}
        {families.has('ipv6') ? <span className="sv-flag is-v6">V6</span> : null}
        {server.non_direct ? <span className="sv-flag">非直连</span> : null}
        {server.config_drift ? <span className="sv-flag is-alert">DRIFT</span> : null}
        {banFlag ? <span className={cn('sv-flag', banFlag.tone)}>{banFlag.label}</span> : null}
      </div>
      <CardOverviewSections server={server} samples={samples} />
      <CardProbesSection rows={probeRows} title={probeTitle} />
      <footer className="sv-card-foot">
        <span className="sv-fact">
          <ActivityIcon />
          在线
          <span className="sv-fact-value">
            {metrics ? formatUptime(metrics.uptime_seconds) : '--'}
          </span>
        </span>
        {renewalDays !== null ? (
          <span className="sv-fact">
            <CalendarCheckIcon />
            到期
            <span
              className={cn(
                'sv-fact-value',
                renewalTone === 'ok' && 'is-ok',
                renewalTone === 'warn' && 'is-warn',
                renewalTone === 'crit' && 'is-crit',
              )}
            >
              {renewalDays < 0 ? '已过期' : `${renewalDays} 天`}
            </span>
          </span>
        ) : (
          <span className="sv-fact">
            <CalendarCheckIcon />
            遥测
            <span className="sv-fact-value">
              {metrics ? formatDateTime(metrics.updated_at, timezone) : '--'}
            </span>
          </span>
        )}
      </footer>
      {server.tags.length > 0 ? (
        <div className="sv-card-tags">
          {server.tags.map((tag) => (
            <span key={tag} className="sv-flag is-tag">
              {tag}
            </span>
          ))}
        </div>
      ) : null}
    </article>
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

// DetailDialog 是服务器详情弹窗（卡片「扩大」按钮打开）：固定头部 + 内部滚动体，
// 概览区直接复用卡片样式（CardOverviewSections / CardProbesSection），
// 其下为主机信息、计费、24 小时趋势与服务器测试。
function DetailDialog({
  server,
  probeRows,
  probeTitle,
  open,
  onOpenChange,
  timezone,
  onOpenTrafficStats,
}: {
  server: Server | null
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
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
  const online = server ? isServerOnline(server) : false
  const families = new Set((server?.addresses ?? []).map((addr) => addressFamily(addr)))
  const traffic = server?.traffic_plan
  const billing = server?.billing
  const banFlag = server ? serverBanFlag(server) : null
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sv-detail flex max-h-[88vh] w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl">
        <header className="sv-detail-head">
          <div className="sv-detail-head-row">
            <span className={cn('sv-state-dot', online && 'is-online')} />
            {server ? <CountryFlag code={server.country_code} /> : null}
            <DialogTitle className="sv-detail-title">{server?.alias ?? '服务器详情'}</DialogTitle>
            {server ? (
              <span className="sv-detail-badges">
                <span className="sv-flag">
                  {server.location || server.country_code || '未设置地区'}
                </span>
                {families.has('ipv4') ? <span className="sv-flag is-v4">V4</span> : null}
                {families.has('ipv6') ? <span className="sv-flag is-v6">V6</span> : null}
                {server.non_direct ? <span className="sv-flag">非直连</span> : null}
                {server.config_drift ? <span className="sv-flag is-alert">DRIFT</span> : null}
                {banFlag ? (
                  <span className={cn('sv-flag', banFlag.tone)}>{banFlag.label}</span>
                ) : null}
              </span>
            ) : null}
            {server ? (
              <Button
                variant="outline"
                size="sm"
                className="mr-8 ml-auto shrink-0"
                onClick={openProviderPage}
              >
                <ExternalLinkIcon />
                服务商官网
              </Button>
            ) : null}
          </div>
          <DialogDescription className="sv-detail-desc">
            运行概览、主机信息、最近 24 小时趋势与服务器测试
          </DialogDescription>
        </header>
        <div className="sv-detail-body">
          {server ? (
            <>
              <div className="sv-card is-static">
                <CardOverviewSections server={server} samples={history} />
                <CardProbesSection rows={probeRows} title={probeTitle} />
              </div>
              <dl className="sv-detail-facts">
                <FactItem icon={<Globe2Icon />} label="地区">
                  {server.location || '-'}
                </FactItem>
                <FactItem icon={<NetworkIcon />} label="地址">
                  <span className="sv-mono">{server.address || server.learned_addr || '-'}</span>
                  {server.addresses.length > 0 ? (
                    <ul className="sv-fact-list">
                      {server.addresses.map((addr) => {
                        const family = addressFamily(addr)
                        return (
                          <li key={addr} className="sv-fact-addr">
                            <span className="sv-mono">{addr}</span>
                            <Badge variant="outline">
                              {family === 'ipv4' ? 'IPv4' : family === 'ipv6' ? 'IPv6' : '域名'}
                            </Badge>
                            {addr === server.address ? (
                              <span className="sv-fact-note">默认</span>
                            ) : null}
                          </li>
                        )
                      })}
                    </ul>
                  ) : null}
                </FactItem>
                <FactItem icon={<CableIcon />} label="默认网卡">
                  {metrics?.network_interface || '-'}
                </FactItem>
                <FactItem icon={<ActivityIcon />} label="运行时间">
                  {metrics ? formatUptime(metrics.uptime_seconds) : '-'}
                </FactItem>
                <FactItem icon={<ClockIcon />} label="最近采样">
                  {formatDateTime(metrics?.updated_at, timezone)}
                </FactItem>
                <FactItem icon={<ServerCogIcon />} label="Agent / Xray">
                  <span>
                    {server.agent_version ?? '-'} / {server.xray_version ?? '-'}
                  </span>
                  {server.effective_xray_version ? (
                    <span className="sv-fact-note">
                      期望 {server.effective_xray_version}
                      {server.custom_settings ? '（服务器覆盖）' : '（面板默认）'}
                    </span>
                  ) : null}
                </FactItem>
                <FactItem icon={<ArrowLeftRightIcon />} label="机器类型 / 端口">
                  {server.machine_type === 'direct'
                    ? '直连'
                    : `NAT · ${server.allowed_ports.map(formatPortRange).join(', ') || '仅出口'}`}
                </FactItem>
                <FactItem icon={<ShieldCheckIcon />} label="配置状态">
                  <span className={cn(server.config_drift && 'sv-fact-alert')}>
                    {server.config_drift ? '配置漂移' : '正常'}
                  </span>
                </FactItem>
                <FactItem icon={<SettingsIcon />} label="Agent 设置">
                  {server.agent_settings_status === 'synced'
                    ? '已同步'
                    : server.agent_settings_status === 'failed'
                      ? '同步失败'
                      : '待同步'}
                </FactItem>
                <FactItem icon={<CreditCardIcon />} label="流量额度">
                  {traffic
                    ? traffic.quota_bytes === null
                      ? '无限'
                      : formatTrafficBytes(traffic.quota_bytes)
                    : '-'}
                </FactItem>
                <FactItem icon={<ArrowDownUpIcon />} label="本周期流量">
                  {traffic
                    ? `${formatTrafficBytes(traffic.used_bytes)} · ${
                        traffic.accounting_mode === 'bidirectional'
                          ? '双向合计'
                          : traffic.accounting_mode === 'max'
                            ? '取较大方向'
                            : '仅出站'
                      }`
                    : '-'}
                </FactItem>
                {billing?.enabled ? (
                  <FactItem icon={<Building2Icon />} label="服务商">
                    {billing.provider?.name ?? '-'}
                  </FactItem>
                ) : null}
                {billing?.enabled ? (
                  <FactItem icon={<CreditCardIcon />} label="费用">
                    {(
                      billing.amount_minor /
                      (['JPY', 'KRW', 'ISK'].includes(billing.currency) ? 1 : 100)
                    ).toLocaleString()}{' '}
                    {billing.currency} / {billing.interval_count}{' '}
                    {billing.interval_unit === 'day'
                      ? '天'
                      : billing.interval_unit === 'year'
                        ? '年'
                        : '月'}
                  </FactItem>
                ) : null}
                {billing?.enabled ? (
                  <FactItem icon={<CalendarCheckIcon />} label="下次续费">
                    {billing.next_renewal_on || '-'}
                  </FactItem>
                ) : null}
              </dl>
              {billing?.enabled && billing.public_converted ? (
                <div className="sv-detail-converted">
                  <dt className="sv-fact-label">
                    <CreditCardIcon />
                    费用折算
                  </dt>
                  <dd className="sv-converted-grid">
                    <div>
                      <span className="block text-xs text-muted-foreground">公共汇率结果</span>
                      <span className="mt-0.5 block tabular-nums">
                        {formatConvertedCost(billing.public_converted)} ·{' '}
                        {convertedRateLabel(billing.public_converted)}
                      </span>
                    </div>
                    {billing.custom_converted ? (
                      <div>
                        <span className="block text-xs text-muted-foreground">自定义汇率结果</span>
                        <span className="mt-0.5 block tabular-nums">
                          {formatConvertedCost(billing.custom_converted)} ·{' '}
                          {convertedRateLabel(billing.custom_converted)}
                        </span>
                      </div>
                    ) : null}
                  </dd>
                </div>
              ) : null}
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
            </>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
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

  const probesByServer = useMemo(() => {
    const map = new Map<number, CatalogProbeRow[]>()
    if (props.probes) {
      for (const [id, entry] of Object.entries(props.probes.servers)) {
        map.set(Number(id), entry.rows)
      }
    }
    return map
  }, [props.probes])
  const probeTitle = props.probes
    ? `${props.probes.province}${props.probes.city ? ` · ${props.probes.city}` : ''}`
    : null

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
          <div className="sv-cards">
            {filteredServers.map((server) => (
              <ServerCard
                key={server.id}
                server={server}
                probeRows={probesByServer.get(server.id) ?? []}
                probeTitle={probeTitle}
                samples={samplesByServer.get(server.id) ?? []}
                selected={selected?.id === server.id}
                timezone={props.timezone}
                onSelect={() => setSelectedID(server.id)}
                onOpenDetail={() => setDetailServerID(server.id)}
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
        )}
      </section>
      <TrafficStatsDialog controller={trafficStatsDialog} />
      <DetailDialog
        server={detailServer}
        probeRows={detailServer ? (probesByServer.get(detailServer.id) ?? []) : []}
        probeTitle={probeTitle}
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
