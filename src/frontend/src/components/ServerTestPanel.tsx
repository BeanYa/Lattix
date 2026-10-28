import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertTriangleIcon,
  CheckIcon,
  ChevronRightIcon,
  CircleDotIcon,
  FileTextIcon,
  FlaskConicalIcon,
  GaugeIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
  RouteIcon,
  ShieldCheckIcon,
  WifiIcon,
  XCircleIcon,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { api, errorMessage } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { asRecords, number, text } from '@/lib/records'
import { cn } from '@/lib/utils'
import { isIpv6Category, ipv6Unavailable, withoutIpv6Categories } from '@/lib/server-test-ipv6'
import {
  mediaMethodText,
  mediaRegionText,
  mediaStatusText,
  mediaVendorDisplayName,
  mediaVendorLogo,
  orderedMediaVendors,
} from '@/lib/server-test-logos'
import { serverTestStatusBadge, serverTestStatusLabel } from '@/lib/status'
import { BrandMark, ConnectivityReport } from '@/components/ServerTestConnectivityReport'
import type {
  IPQualityFactor,
  IPQualityFamily,
  Server,
  ServerTestCatalogStatus,
  ServerTestCategory,
  ServerTestCategoryResult,
  ServerTestReport,
  ServerTestTask,
  ServerTestTaskStatus,
} from '@/lib/types'

const categoryOptions: Array<{
  category: ServerTestCategory
  label: string
  description: string
  group: 'quality' | 'tcp' | 'route' | 'speed'
}> = [
  {
    category: 'ip_quality',
    label: 'IP 质量',
    description: '风险库、DNSBL、流媒体与 ASN',
    group: 'quality',
  },
  { category: 'tcp_ipv4', label: 'IPv4 TCP', description: '三网 SYN 延迟与丢包', group: 'tcp' },
  { category: 'tcp_ipv6', label: 'IPv6 TCP', description: '三网 SYN 延迟与丢包', group: 'tcp' },
  {
    category: 'large_packet_ipv4',
    label: 'IPv4 大包',
    description: '大 SYN 包回程可达性',
    group: 'tcp',
  },
  { category: 'cernet_ipv4', label: '教育网 IPv4', description: 'CERNET 节点延迟', group: 'tcp' },
  { category: 'cernet2_ipv6', label: '教育网 IPv6', description: 'CERNET2 节点延迟', group: 'tcp' },
  {
    category: 'connectivity',
    label: '网络连通性',
    description: '知名站点 HTTP 连通与延迟',
    group: 'tcp',
  },
  {
    category: 'return_route_ipv4',
    label: 'IPv4 回程',
    description: '三网回程路由',
    group: 'route',
  },
  {
    category: 'return_route_ipv6',
    label: 'IPv6 回程',
    description: '三网回程路由',
    group: 'route',
  },
  {
    category: 'speed',
    label: '单线程测速',
    description: 'Apple CDN 与 speedtest.net 三网节点',
    group: 'speed',
  },
]

const categoryLabels = Object.fromEntries(
  categoryOptions.map((option) => [option.category, option.label]),
) as Record<string, string>

// 已退役类别的历史报告仍可能存储在库中，展示时保留原名称。
const legacyCategoryLabels: Record<string, string> = { international: '国际连通' }
Object.assign(categoryLabels, legacyCategoryLabels)

function categoryLabel(category: string): string {
  return categoryLabels[category] ?? category
}

const groupMeta = {
  quality: { label: 'IP 质量', icon: ShieldCheckIcon },
  tcp: { label: 'TCP 与连通性', icon: WifiIcon },
  route: { label: '回程路由', icon: RouteIcon },
  speed: { label: '带宽', icon: GaugeIcon },
} as const

const directDefaults: ServerTestCategory[] = [
  'ip_quality',
  'tcp_ipv4',
  'tcp_ipv6',
  'return_route_ipv4',
  'return_route_ipv6',
  'connectivity',
]

function isTerminal(status: ServerTestTaskStatus): boolean {
  return status === 'succeeded' || status === 'completed_with_errors' || status === 'failed'
}

function carrierLabel(value: unknown): string {
  return (
    ({ telecom: '电信', unicom: '联通', mobile: '移动' } as Record<string, string>)[
      text(value, '')
    ] ?? text(value)
  )
}

function ErrorNotice({ code, message }: { code?: string; message?: string }) {
  if (!code && !message) return null
  return (
    <div className="sv-inline-warning is-danger">
      <XCircleIcon className="mt-0.5 size-3.5 shrink-0" />
      <span>
        <span className="font-mono">{code || 'error'}</span>
        {message ? ` · ${message}` : ''}
      </span>
    </div>
  )
}

function TCPReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[620px] text-left text-xs">
        <thead className="border-b text-muted-foreground">
          <tr>
            <th className="px-2 py-2 font-medium">目的地</th>
            <th className="px-2 py-2 font-medium">运营商</th>
            <th className="px-2 py-2 font-medium">平均延迟</th>
            <th className="px-2 py-2 font-medium">丢包</th>
            <th className="px-2 py-2 font-medium">探测</th>
            <th className="px-2 py-2 font-medium">状态</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {(category.items ?? []).map((item, index) => {
            const average = number(item.rtt_avg_ms)
            const loss = number(item.loss_percent)
            return (
              <tr key={text(item.id, String(index))}>
                <td className="max-w-56 px-2 py-2 font-medium">{text(item.label)}</td>
                <td className="px-2 py-2">{carrierLabel(item.carrier)}</td>
                <td className="px-2 py-2 tabular-nums">
                  {average === null ? '-' : `${average.toFixed(1)} ms`}
                </td>
                <td
                  className={cn(
                    'px-2 py-2 tabular-nums',
                    loss !== null && loss > 20 && 'text-destructive',
                  )}
                >
                  {loss === null ? '-' : `${loss.toFixed(1)}%`}
                </td>
                <td className="px-2 py-2 font-mono">{text(item.probe_method)}</td>
                <td className="max-w-72 px-2 py-2">
                  {item.error_message ? (
                    <span className="text-destructive">
                      {text(item.error_code)} · {text(item.error_message)}
                    </span>
                  ) : (
                    <span className="text-success">完成</span>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function RouteReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="divide-y">
      {(category.items ?? []).map((item, index) => (
        <div key={text(item.id, String(index))} className="py-3 first:pt-0 last:pb-0">
          <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
            <span className="font-medium">{text(item.label)}</span>
            <Badge variant="outline">{carrierLabel(item.carrier)}</Badge>
            <span className="font-mono text-muted-foreground">{text(item.probe_method)}</span>
            <span className={item.reached === true ? 'text-success' : 'text-warning'}>
              {item.reached === true ? '已到达' : '未到达'}
            </span>
          </div>
          <div className="grid gap-1 font-mono text-[11px] sm:grid-cols-2">
            {asRecords(item.hops).map((hop, hopIndex) => {
              const rtts = Array.isArray(hop.rtt_ms)
                ? hop.rtt_ms.filter((value): value is number => typeof value === 'number')
                : []
              return (
                <div key={hopIndex} className="flex gap-2">
                  <span className="w-6 text-right text-muted-foreground">{text(hop.hop)}</span>
                  <span className="min-w-0 truncate">{text(hop.address, '*')}</span>
                  <span className="ml-auto tabular-nums text-muted-foreground">
                    {rtts.length ? `${rtts.map((value) => value.toFixed(1)).join('/')} ms` : '*'}
                  </span>
                </div>
              )
            })}
          </div>
          <ErrorNotice code={text(item.error_code, '')} message={text(item.error_message, '')} />
        </div>
      ))}
    </div>
  )
}

const factorLabels: Record<string, string> = {
  CountryCode: '国家代码',
  Proxy: '代理',
  Tor: 'Tor',
  VPN: 'VPN',
  Server: '机房',
  Abuser: '滥用',
  Robot: '爬虫',
}
const factorKeys: Array<keyof IPQualityFactor> = [
  'Proxy',
  'Tor',
  'VPN',
  'Server',
  'Abuser',
  'Robot',
]

function ipText(value: string | undefined): string {
  return value && value !== 'null' ? value : '-'
}

/** 流媒体解锁状态徽片的配色（解锁/仅自制/屏蔽/未知）。 */
const mediaStatusChipClass: Record<'success' | 'warning' | 'destructive' | 'muted', string> = {
  success: 'border border-success/40 bg-success/10 px-1.5 py-0.5 text-success',
  warning: 'border border-warning/30 bg-warning/10 px-1.5 py-0.5 text-warning',
  destructive: 'border border-destructive/40 bg-destructive/15 px-1.5 py-0.5 text-destructive',
  muted: 'border border-rule bg-muted/40 px-1.5 py-0.5 text-muted-foreground',
}

function IPQualityFamilySection({ family }: { family: IPQualityFamily }) {
  const info = family.Info ?? {}
  const scoreRows = Object.entries(family.Score ?? {})
  const factorRows = (() => {
    const names = new Set<string>()
    for (const key of factorKeys) {
      for (const name of Object.keys(family.Factor?.[key] ?? {})) names.add(name)
    }
    return Array.from(names)
  })()
  const mediaRows = orderedMediaVendors(family.Media ?? {})
  const mailRows = Object.entries(family.Mail?.Providers ?? {})
  const dnsbl = family.Mail?.DNSBlacklist
  return (
    <section className="space-y-3 border-t pt-4 first:border-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="text-sm font-medium">{family.family.toUpperCase()}</h4>
        <span className="font-mono text-xs text-muted-foreground">{ipText(family.Head?.IP)}</span>
        {family.Head?.Version ? <Badge variant="outline">{family.Head.Version}</Badge> : null}
        {family.Head?.Time ? (
          <span className="text-xs text-muted-foreground">{family.Head.Time}</span>
        ) : null}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">基础信息</div>
          <dl className="grid grid-cols-2 gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">ASN</dt>
            <dd>
              {ipText(info.ASN)}
              {info.Organization ? ` · ${info.Organization}` : ''}
            </dd>
            <dt className="text-muted-foreground">城市</dt>
            <dd>
              {ipText(info.City?.Name)}
              {info.City?.Subdivisions ? ` · ${info.City.Subdivisions}` : ''}
            </dd>
            <dt className="text-muted-foreground">地区</dt>
            <dd>
              {ipText(info.Region?.Name)}
              {info.Region?.Code ? ` (${info.Region.Code})` : ''}
            </dd>
            <dt className="text-muted-foreground">注册地</dt>
            <dd>
              {ipText(info.RegisteredRegion?.Name)}
              {info.RegisteredRegion?.Code ? ` (${info.RegisteredRegion.Code})` : ''}
            </dd>
            <dt className="text-muted-foreground">时区</dt>
            <dd>{ipText(info.TimeZone)}</dd>
            <dt className="text-muted-foreground">IP 类型</dt>
            <dd>{ipText(info.Type)}</dd>
          </dl>
        </div>
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">风险评分</div>
          <table className="w-full text-left text-xs">
            <tbody className="divide-y">
              {scoreRows.length === 0 ? (
                <tr>
                  <td className="py-1.5 text-muted-foreground">无数据</td>
                </tr>
              ) : (
                scoreRows.map(([name, score]) => (
                  <tr key={name}>
                    <td className="py-1.5 font-medium">{name}</td>
                    <td className="py-1.5 text-right tabular-nums">
                      {score === 'null' ? '-' : score}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
      <div className="overflow-x-auto border">
        <table className="w-full min-w-[560px] text-left text-xs">
          <thead className="border-b text-muted-foreground">
            <tr>
              <th className="px-2 py-2 font-medium">数据库</th>
              {factorKeys.map((key) => (
                <th key={key} className="px-2 py-2 text-center font-medium">
                  {factorLabels[key]}
                </th>
              ))}
              <th className="px-2 py-2 text-center font-medium">{factorLabels.CountryCode}</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {factorRows.length === 0 ? (
              <tr>
                <td colSpan={factorKeys.length + 2} className="px-2 py-2 text-muted-foreground">
                  无数据
                </td>
              </tr>
            ) : (
              factorRows.map((name) => (
                <tr key={name}>
                  <td className="px-2 py-2 font-medium">{name}</td>
                  {factorKeys.map((key) => {
                    const value = family.Factor?.[key]?.[name]
                    return (
                      <td key={key} className="px-2 py-2 text-center">
                        {value === undefined || value === null ? (
                          <span className="text-muted-foreground">-</span>
                        ) : value ? (
                          <span className="text-destructive">是</span>
                        ) : (
                          <span className="text-success">否</span>
                        )}
                      </td>
                    )
                  })}
                  <td className="px-2 py-2 text-center">
                    {ipText(family.Factor?.CountryCode?.[name])}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <div className="border p-3">
        <div className="mb-2 text-xs font-medium">流媒体与 AI 解锁</div>
        {mediaRows.length === 0 ? (
          <div className="text-xs text-muted-foreground">无数据</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[440px] text-left text-xs">
              <thead className="border-b text-muted-foreground">
                <tr>
                  <th className="px-2 py-1.5 font-medium">服务</th>
                  <th className="px-2 py-1.5 font-medium">状态</th>
                  <th className="px-2 py-1.5 font-medium">地区</th>
                  <th className="px-2 py-1.5 font-medium">方式</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {mediaRows.map(({ key, rawName, status }) => {
                  const statusInfo = mediaStatusText(status)
                  const displayName = mediaVendorDisplayName(key, rawName)
                  return (
                    <tr key={key}>
                      <td className="px-2 py-1.5">
                        <span className="flex items-center gap-2 font-medium">
                          <BrandMark src={mediaVendorLogo(key)} label={displayName} />
                          {displayName}
                        </span>
                      </td>
                      <td className="px-2 py-1.5">
                        <span className={mediaStatusChipClass[statusInfo.tone]}>
                          {statusInfo.label}
                        </span>
                      </td>
                      <td className="px-2 py-1.5 font-mono">{mediaRegionText(status)}</td>
                      <td className="px-2 py-1.5">{mediaMethodText(status)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <div className="border p-3">
        <div className="mb-2 text-xs font-medium">邮局检测</div>
        <div className="flex flex-wrap gap-1.5">
          <Badge variant={family.Mail?.Port25 ? 'secondary' : 'outline'}>
            {family.Mail?.Port25 == null
              ? '25 端口未知'
              : `25 端口${family.Mail.Port25 ? '开放' : '受限'}`}
          </Badge>
          {mailRows.map(([name, open]) => (
            <Badge key={name} variant={open ? 'secondary' : 'outline'}>
              {name}
            </Badge>
          ))}
        </div>
        {dnsbl ? (
          <div className="mt-2 text-xs text-muted-foreground">
            DNSBL {dnsbl.Total} 库 · 正常 {dnsbl.Clean} · 标记 {dnsbl.Marked} · 黑名单{' '}
            {dnsbl.Blacklisted}
          </div>
        ) : null}
      </div>
    </section>
  )
}

function IPQualityReport({ category }: { category: ServerTestCategoryResult }) {
  const result = category.ip_quality
  if (!result || result.families.length === 0) return null
  return (
    <div className="space-y-4">
      {result.script_stale ? (
        <div className="sv-inline-warning">
          <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
          <span>脚本版本可能过期（script_version={result.script_version}），本次复用缓存</span>
        </div>
      ) : null}
      {result.families.map((family) => (
        <IPQualityFamilySection key={family.family} family={family} />
      ))}
    </div>
  )
}

function SpeedReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[600px] text-left text-xs">
        <thead className="border-b text-muted-foreground">
          <tr>
            <th className="px-2 py-2 font-medium">目的地</th>
            <th className="px-2 py-2 font-medium">协议</th>
            <th className="px-2 py-2 font-medium">上传</th>
            <th className="px-2 py-2 font-medium">下载</th>
            <th className="px-2 py-2 font-medium">状态</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {(category.items ?? []).map((item, index) => (
            <tr key={text(item.id, String(index))}>
              <td className="px-2 py-2 font-medium">{text(item.label)}</td>
              <td className="px-2 py-2">{text(item.address_family).toUpperCase()}</td>
              <td className="px-2 py-2 tabular-nums">
                {number(item.upload_mbps) === null
                  ? '-'
                  : `${number(item.upload_mbps)?.toFixed(1)} Mbps`}
              </td>
              <td className="px-2 py-2 tabular-nums">
                {number(item.download_mbps) === null
                  ? '-'
                  : `${number(item.download_mbps)?.toFixed(1)} Mbps`}
              </td>
              <td className="max-w-72 px-2 py-2">
                {item.error_message ? (
                  <span className="text-destructive">
                    {text(item.error_code)} · {text(item.error_message)}
                  </span>
                ) : (
                  serverTestStatusLabel(text(item.status))
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ReportCategory({ category }: { category: ServerTestCategoryResult }) {
  const isRoute =
    category.category === 'return_route_ipv4' || category.category === 'return_route_ipv6'
  const isIP = category.category === 'ip_quality'
  const isSpeed = category.category === 'speed'
  const isConnectivity = category.category === 'connectivity'
  return (
    <section className="min-w-0 border-b py-5 last:border-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold">{categoryLabel(category.category)}</h3>
        <Badge variant={serverTestStatusBadge(category.status)}>
          {serverTestStatusLabel(category.status)}
        </Badge>
      </div>
      <ErrorNotice code={category.error_code} message={category.error_message} />
      {isIP ? (
        <IPQualityReport category={category} />
      ) : isRoute ? (
        <RouteReport category={category} />
      ) : isSpeed ? (
        <SpeedReport category={category} />
      ) : isConnectivity ? (
        <ConnectivityReport category={category} />
      ) : (
        <TCPReport category={category} />
      )}
    </section>
  )
}

function TestReport({ report, timezone }: { report: ServerTestReport; timezone?: string }) {
  const ipv6Off = ipv6Unavailable(report.environment)
  const visibleCategories = ipv6Off ? withoutIpv6Categories(report.categories) : report.categories
  return (
    <div className="min-w-0 space-y-4">
      <div className="grid gap-3 border-y py-3 text-xs sm:grid-cols-4">
        <div>
          <span className="block text-muted-foreground">状态</span>
          <span className="mt-1 block font-medium">{serverTestStatusLabel(report.status)}</span>
        </div>
        <div>
          <span className="block text-muted-foreground">完成时间</span>
          <span className="mt-1 block">{formatDateTime(report.completed_at, timezone)}</span>
        </div>
        <div>
          <span className="block text-muted-foreground">Agent</span>
          <span className="mt-1 block font-mono">{report.agent_version}</span>
        </div>
        <div>
          <span className="block text-muted-foreground">权限 / 沙箱</span>
          <span className="mt-1 block">
            {report.environment.privileges} · {report.environment.sandbox}
          </span>
        </div>
      </div>
      {report.environment.degraded || report.environment.sandbox_reason ? (
        <div className="sv-inline-warning">
          <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
          <span>{report.environment.degraded_reason || report.environment.sandbox_reason}</span>
        </div>
      ) : null}
      {ipv6Off ? (
        <div className="sv-inline-note">
          <WifiIcon className="mt-0.5 size-3.5 shrink-0" />
          <span>IPv6 不可用，IPv6 相关章节已隐藏</span>
        </div>
      ) : null}
      <ErrorNotice code={report.error_code} message={report.error_message} />
      <div>
        {visibleCategories.map((category) => (
          <ReportCategory key={category.category} category={category} />
        ))}
      </div>
    </div>
  )
}

export function ServerTestPanel({
  server,
  active,
  timezone,
}: {
  server: Server
  active: boolean
  timezone?: string
}) {
  const [task, setTask] = useState<ServerTestTask | null>(null)
  const [loading, setLoading] = useState(false)
  const [selectionOpen, setSelectionOpen] = useState(false)
  const [overviewOpen, setOverviewOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)
  // 'full' 表示完整报告；具体类别表示仅查看该模块。
  const [reportScope, setReportScope] = useState<ServerTestCategory | 'full' | null>(null)
  const [warningOpen, setWarningOpen] = useState(false)
  const [pendingCategory, setPendingCategory] = useState<ServerTestCategory | null>(null)
  const [selected, setSelected] = useState<ServerTestCategory[]>(
    server.machine_type === 'nat' ? ['ip_quality'] : directDefaults,
  )
  const [catalog, setCatalog] = useState<ServerTestCatalogStatus | null>(null)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const ipv6Off = ipv6Unavailable(task?.result?.environment)
  const progressRows =
    task?.progress?.categories ??
    task?.categories.map((category) => ({
      category,
      status: 'pending',
      completed: 0,
      total: 1,
      message: '',
    })) ??
    []
  const visibleProgressRows = ipv6Off ? withoutIpv6Categories(progressRows) : progressRows
  // 任务终态后 progress 不再存在（进度不持久化），模块列表从最终报告构建。
  const overviewTerminal = Boolean(task && isTerminal(task.status))
  const resultRows =
    task && overviewTerminal && task.result
      ? task.result.categories.map((category) => ({
          category: category.category,
          result: category,
        }))
      : []
  const visibleResultRows = ipv6Off ? withoutIpv6Categories(resultRows) : resultRows
  const scopedMode = reportScope !== null && reportScope !== 'full'
  const scopedCategoryResult = scopedMode
    ? task?.result?.categories.find((category) => category.category === reportScope)
    : undefined

  const loadTask = useCallback(async () => {
    const latest = await api.serverTest(server.id, { display: 'silent' })
    setTask(latest)
    return latest
  }, [server.id])

  useEffect(() => {
    if (!active) return
    let cancelled = false
    setLoading(true)
    api
      .serverTest(server.id, { display: 'silent' })
      .then((latest) => {
        if (!cancelled) setTask(latest)
      })
      .catch(() => {
        if (!cancelled) setTask(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [active, server.id])

  useEffect(() => {
    if (!active || !task || isTerminal(task.status)) return
    // 终态后总览窗口自动切换为模块列表（含查看完整报告入口），无需切换弹窗。
    const timer = setInterval(() => {
      loadTask().catch(() => undefined)
    }, 1000)
    return () => clearInterval(timer)
  }, [active, loadTask, task])

  const progressPercent = useMemo(() => {
    if (!task?.progress || task.progress.total <= 0) return 0
    return Math.min(100, (task.progress.completed / task.progress.total) * 100)
  }, [task])

  const openSelection = () => {
    const base =
      server.machine_type === 'nat' ? (['ip_quality'] as ServerTestCategory[]) : directDefaults
    setSelected(ipv6Off ? base.filter((category) => !isIpv6Category(category)) : base)
    setError('')
    setSelectionOpen(true)
    setCatalogLoading(true)
    api
      .serverTestCatalogStatus({ display: 'silent' })
      .then(setCatalog)
      .catch((reason) => setError(errorMessage(reason)))
      .finally(() => setCatalogLoading(false))
  }

  const toggleCategory = (category: ServerTestCategory, checked: boolean) => {
    if (!checked) {
      setSelected((current) => current.filter((item) => item !== category))
      return
    }
    if (
      (server.machine_type === 'nat' &&
        category !== 'ip_quality' &&
        // 网络连通性是纯 HTTPS 出站探测，NAT 机不受端口映射限制，可直接勾选。
        category !== 'connectivity') ||
      category === 'speed' ||
      (ipv6Off && isIpv6Category(category))
    ) {
      setPendingCategory(category)
      setWarningOpen(true)
      return
    }
    setSelected((current) => [...current, category])
  }

  const confirmWarning = () => {
    if (pendingCategory)
      setSelected((current) =>
        current.includes(pendingCategory) ? current : [...current, pendingCategory],
      )
    setPendingCategory(null)
    setWarningOpen(false)
  }

  const submit = async () => {
    setSubmitting(true)
    setError('')
    try {
      const created = await api.runServerTest(server.id, selected)
      setTask(created)
      setSelectionOpen(false)
      setOverviewOpen(true)
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSubmitting(false)
    }
  }

  const refreshCatalog = async () => {
    setRefreshing(true)
    setError('')
    try {
      setCatalog(await api.refreshServerTestCatalog())
    } catch (reason) {
      setError(errorMessage(reason))
      try {
        setCatalog(await api.serverTestCatalogStatus({ display: 'silent' }))
      } catch {
        /* keep current status */
      }
    } finally {
      setRefreshing(false)
    }
  }

  const openModuleReport = (category: ServerTestCategory) => {
    setReportScope(category)
    setReportOpen(true)
  }

  const openFullReport = () => {
    setReportScope('full')
    setReportOpen(true)
  }

  const primaryAction = () => {
    if (!task) return openSelection()
    setOverviewOpen(true)
  }

  return (
    <section aria-labelledby="server-test-heading" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 id="server-test-heading" className="text-sm font-semibold">
            服务器测试
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {task
              ? `${serverTestStatusLabel(task.status)} · ${formatDateTime(task.updated_at, timezone)}`
              : '尚无测试结果'}
          </p>
        </div>
        {task && isTerminal(task.status) ? (
          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={() => setOverviewOpen(true)} disabled={loading}>
              {loading ? (
                <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" />
              ) : (
                <FlaskConicalIcon />
              )}
              查看测试结果
            </Button>
            <Button onClick={openSelection}>
              <CircleDotIcon />
              运行测试
            </Button>
          </div>
        ) : (
          <Button onClick={primaryAction} disabled={loading}>
            {loading || (task && !isTerminal(task.status)) ? (
              <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" />
            ) : (
              <CircleDotIcon />
            )}
            {loading ? '读取状态' : task ? '查看测试进度' : '点击运行测试'}
          </Button>
        )}
      </div>
      {task?.error_message && !task.result ? (
        <ErrorNotice code={task.error_code} message={task.error_message} />
      ) : null}

      <Dialog open={selectionOpen} onOpenChange={setSelectionOpen}>
        <DialogContent className="flex max-h-[88vh] w-[calc(100%-2rem)] flex-col overflow-hidden sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>运行服务器测试</DialogTitle>
            <DialogDescription>选择本次原子任务包含的测试项目</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <div className="space-y-4">
              {(Object.keys(groupMeta) as Array<keyof typeof groupMeta>).map((group) => {
                const meta = groupMeta[group]
                const Icon = meta.icon
                return (
                  <fieldset key={group} className="border-t pt-3 first:border-0 first:pt-0">
                    <legend className="mb-2 flex items-center gap-2 text-xs font-medium">
                      <Icon className="size-3.5" />
                      {meta.label}
                    </legend>
                    <div className="grid gap-2 sm:grid-cols-2">
                      {categoryOptions
                        .filter((option) => option.group === group)
                        .map((option) => {
                          const greyed = ipv6Off && isIpv6Category(option.category)
                          return (
                            <label
                              key={option.category}
                              className={cn(
                                'flex cursor-pointer items-start gap-3 border px-3 py-2.5 transition-colors hover:bg-muted/40',
                                selected.includes(option.category) &&
                                  'border-primary/40 bg-primary/[0.04]',
                                greyed && 'opacity-60',
                              )}
                            >
                              <input
                                type="checkbox"
                                className="mt-0.5 size-4 accent-primary"
                                checked={selected.includes(option.category)}
                                onChange={(event) =>
                                  toggleCategory(option.category, event.target.checked)
                                }
                              />
                              <span className="min-w-0">
                                <span className="block text-sm font-medium">{option.label}</span>
                                <span className="mt-0.5 block text-xs text-muted-foreground">
                                  {option.description}
                                </span>
                              </span>
                            </label>
                          )
                        })}
                    </div>
                  </fieldset>
                )
              })}
              <div
                className={cn(
                  'flex items-start gap-2 border px-3 py-2 text-xs',
                  catalog?.available
                    ? 'border-success/30 bg-success/5'
                    : 'border-warning/30 bg-warning/5',
                )}
              >
                {catalogLoading || catalog?.refreshing ? (
                  <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin motion-reduce:animate-none" />
                ) : catalog?.available ? (
                  <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" />
                ) : (
                  <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0 text-warning" />
                )}
                <div className="min-w-0 flex-1">
                  <span className="font-medium">
                    {catalogLoading
                      ? '读取节点目录'
                      : catalog?.available
                        ? '节点目录可用'
                        : '节点目录不可用'}
                  </span>
                  {catalog?.fetched_at ? (
                    <span className="ml-2 text-muted-foreground">
                      {formatDateTime(catalog.fetched_at, timezone)}
                    </span>
                  ) : null}
                  {catalog?.last_error ? (
                    <p className="mt-1 break-words text-warning">
                      最近刷新失败：{catalog.last_error}
                    </p>
                  ) : null}
                </div>
                {!catalogLoading ? (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={refreshCatalog}
                    disabled={refreshing}
                  >
                    {refreshing ? (
                      <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" />
                    ) : (
                      <RefreshCwIcon />
                    )}
                    刷新
                  </Button>
                ) : null}
              </div>
              {error ? <ErrorNotice code="request_failed" message={error} /> : null}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setSelectionOpen(false)}>
              取消
            </Button>
            <Button
              onClick={submit}
              disabled={selected.length === 0 || !catalog?.available || submitting || refreshing}
            >
              {submitting ? (
                <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" />
              ) : (
                <FlaskConicalIcon />
              )}
              下发 {selected.length} 项测试
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={warningOpen}
        onOpenChange={(open) => {
          setWarningOpen(open)
          if (!open) setPendingCategory(null)
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              确认运行 {pendingCategory ? categoryLabel(pendingCategory) : '测试'}
            </DialogTitle>
            <DialogDescription>
              {pendingCategory && isIpv6Category(pendingCategory) && ipv6Off
                ? '该服务器未检测到 IPv6 地址，相关测试可能全部失败。'
                : server.machine_type === 'nat' && pendingCategory !== 'ip_quality'
                  ? 'NAT 机型的 TCP、回程或测速测试可能因系统、端口映射或运营商限制而不可用。'
                  : '该项目会产生大量网络流量。'}
              {pendingCategory === 'speed'
                ? ' Apple CDN 单线程测速最多约 5.5 GiB；speedtest.net 节点会跑满带宽，流量与带宽成正比（千兆线路约数十 GB）。'
                : ''}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setWarningOpen(false)
                setPendingCategory(null)
              }}
            >
              取消
            </Button>
            <Button onClick={confirmWarning}>确认勾选</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={overviewOpen} onOpenChange={setOverviewOpen}>
        <DialogContent className="w-[calc(100%-2rem)] sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{overviewTerminal ? '服务器测试报告' : '服务器测试进行中'}</DialogTitle>
            <DialogDescription>
              {overviewTerminal
                ? `${server.alias} · 点击模块查看对应报告`
                : '进度为尽力上报，最终报告为权威结果'}
            </DialogDescription>
          </DialogHeader>
          {overviewTerminal ? (
            task?.result ? (
              <div className="divide-y border-y">
                {visibleResultRows.map(({ category, result }) => (
                  <button
                    key={category}
                    type="button"
                    onClick={() => openModuleReport(category)}
                    className="flex w-full items-center gap-3 py-3 text-left transition-colors hover:bg-muted/40"
                  >
                    <span
                      className={cn(
                        'flex size-7 shrink-0 items-center justify-center border',
                        ['available', 'limited', 'succeeded'].includes(result.status) &&
                          'border-success text-success',
                        ['unavailable', 'failed'].includes(result.status) &&
                          'border-destructive text-destructive',
                      )}
                    >
                      {['available', 'limited', 'succeeded'].includes(result.status) ? (
                        <CheckIcon className="size-3.5" />
                      ) : ['unavailable', 'failed'].includes(result.status) ? (
                        <XCircleIcon className="size-3.5" />
                      ) : (
                        <CircleDotIcon className="size-3.5" />
                      )}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center justify-between gap-3 text-xs">
                        <span className="font-medium">{categoryLabel(category)}</span>
                        <span className="text-muted-foreground">
                          {serverTestStatusLabel(result.status)}
                        </span>
                      </span>
                    </span>
                    <ChevronRightIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  </button>
                ))}
              </div>
            ) : (
              <ErrorNotice
                code={task?.error_code || 'result_unavailable'}
                message={task?.error_message || '测试已结束，但结果报告不可用'}
              />
            )
          ) : (
            <div className="space-y-4">
              <div>
                <div className="mb-2 flex items-center justify-between text-xs">
                  <span>{task ? serverTestStatusLabel(task.status) : '读取状态'}</span>
                  <span className="tabular-nums">{Math.round(progressPercent)}%</span>
                </div>
                <Progress value={progressPercent} />
              </div>
              <div className="divide-y border-y">
                {visibleProgressRows.map((progress) => (
                  <div key={progress.category} className="flex items-center gap-3 py-3">
                    <span
                      className={cn(
                        'flex size-7 shrink-0 items-center justify-center border',
                        progress.status === 'running' && 'border-info text-info',
                        ['available', 'limited', 'succeeded'].includes(progress.status) &&
                          'border-success text-success',
                        ['unavailable', 'failed'].includes(progress.status) &&
                          'border-destructive text-destructive',
                      )}
                    >
                      {progress.status === 'running' ? (
                        <LoaderCircleIcon className="size-3.5 animate-spin motion-reduce:animate-none" />
                      ) : ['available', 'limited', 'succeeded'].includes(progress.status) ? (
                        <CheckIcon className="size-3.5" />
                      ) : ['unavailable', 'failed'].includes(progress.status) ? (
                        <XCircleIcon className="size-3.5" />
                      ) : (
                        <CircleDotIcon className="size-3.5" />
                      )}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center justify-between gap-3 text-xs">
                        <span className="font-medium">{categoryLabel(progress.category)}</span>
                        <span className="tabular-nums text-muted-foreground">
                          {progress.completed}/{progress.total}
                        </span>
                      </div>
                      {progress.message ? (
                        <p className="mt-1 truncate text-xs text-muted-foreground">
                          {progress.message}
                        </p>
                      ) : null}
                    </div>
                  </div>
                ))}
              </div>
              {task?.error_message ? (
                <ErrorNotice code={task.error_code} message={task.error_message} />
              ) : null}
            </div>
          )}
          {overviewTerminal ? (
            <DialogFooter className="grid grid-cols-1 gap-2 sm:flex">
              <Button variant="outline" onClick={() => setOverviewOpen(false)}>
                关闭
              </Button>
              {task?.result ? (
                <Button onClick={openFullReport}>
                  <FileTextIcon />
                  查看完整报告
                </Button>
              ) : null}
            </DialogFooter>
          ) : null}
        </DialogContent>
      </Dialog>

      <Dialog
        open={reportOpen}
        onOpenChange={(open) => {
          setReportOpen(open)
          if (!open) setReportScope(null)
        }}
      >
        <DialogContent
          initialFocus={false}
          className="flex max-h-[90vh] w-[calc(100%-1rem)] min-w-0 flex-col overflow-hidden p-5 sm:max-w-5xl"
        >
          <DialogHeader>
            <DialogTitle>
              {scopedMode && reportScope
                ? `服务器测试报告 · ${categoryLabel(reportScope)}`
                : '服务器测试报告'}
            </DialogTitle>
            <DialogDescription>{server.alias} · 仅保留最近一次测试结果</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {!task?.result ? (
              <ErrorNotice
                code={task?.error_code || 'result_unavailable'}
                message={task?.error_message || '测试已结束，但结果报告不可用'}
              />
            ) : scopedMode ? (
              scopedCategoryResult ? (
                <div className="min-w-0">
                  <ReportCategory category={scopedCategoryResult} />
                </div>
              ) : (
                <ErrorNotice code="result_unavailable" message="该模块的报告不可用" />
              )
            ) : (
              <TestReport report={task.result} timezone={timezone} />
            )}
          </div>
          <DialogFooter className="grid grid-cols-1 gap-2 sm:flex">
            <Button variant="outline" onClick={() => setReportOpen(false)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
