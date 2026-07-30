import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertTriangleIcon,
  CheckIcon,
  CircleDotIcon,
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
import { cn } from '@/lib/utils'
import type {
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
  { category: 'ip_quality', label: 'IP 质量', description: '风险库、DNSBL、流媒体与 ASN', group: 'quality' },
  { category: 'tcp_ipv4', label: 'IPv4 TCP', description: '三网 SYN 延迟与丢包', group: 'tcp' },
  { category: 'tcp_ipv6', label: 'IPv6 TCP', description: '三网 SYN 延迟与丢包', group: 'tcp' },
  { category: 'large_packet_ipv4', label: 'IPv4 大包', description: '大 SYN 包回程可达性', group: 'tcp' },
  { category: 'cernet_ipv4', label: '教育网 IPv4', description: 'CERNET 节点延迟', group: 'tcp' },
  { category: 'cernet2_ipv6', label: '教育网 IPv6', description: 'CERNET2 节点延迟', group: 'tcp' },
  { category: 'international', label: '国际连通', description: '国际站点与 CDN 延迟', group: 'tcp' },
  { category: 'return_route_ipv4', label: 'IPv4 回程', description: '三网回程路由', group: 'route' },
  { category: 'return_route_ipv6', label: 'IPv6 回程', description: '三网回程路由', group: 'route' },
  { category: 'speed', label: '单线程测速', description: 'Apple CDN 与可用运营商节点', group: 'speed' },
]

const categoryLabels = Object.fromEntries(categoryOptions.map((option) => [option.category, option.label])) as Record<ServerTestCategory, string>

const groupMeta = {
  quality: { label: 'IP 质量', icon: ShieldCheckIcon },
  tcp: { label: 'TCP 与连通性', icon: WifiIcon },
  route: { label: '回程路由', icon: RouteIcon },
  speed: { label: '带宽', icon: GaugeIcon },
} as const

const directDefaults: ServerTestCategory[] = [
  'ip_quality', 'tcp_ipv4', 'tcp_ipv6', 'return_route_ipv4', 'return_route_ipv6', 'international',
]

function isTerminal(status: ServerTestTaskStatus): boolean {
  return status === 'succeeded' || status === 'completed_with_errors' || status === 'failed'
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    queued: '等待 Agent', accepted: 'Agent 已接收', running: '正在测试', succeeded: '测试完成',
    completed_with_errors: '部分项目异常', failed: '测试失败', pending: '等待中',
    available: '可用', limited: '部分可用', unavailable: '不可用',
    provider_access_unavailable: '无公开访问方式', clean: '正常', listed: '已列入名单',
  }
  return labels[status] ?? status
}

function statusBadge(status: string) {
  if (status === 'failed' || status === 'unavailable' || status === 'listed') return 'destructive' as const
  if (status === 'succeeded' || status === 'available' || status === 'clean') return 'secondary' as const
  return 'outline' as const
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function asRecords(value: unknown): Array<Record<string, unknown>> {
  return Array.isArray(value) ? value.map(asRecord) : []
}

function text(value: unknown, fallback = '-'): string {
  if (typeof value === 'string' && value !== '') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return fallback
}

function number(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function carrierLabel(value: unknown): string {
  return ({ telecom: '电信', unicom: '联通', mobile: '移动' } as Record<string, string>)[text(value, '')] ?? text(value)
}

function ErrorNotice({ code, message }: { code?: string; message?: string }) {
  if (!code && !message) return null
  return (
    <div className="flex items-start gap-2 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-xs text-destructive">
      <XCircleIcon className="mt-0.5 size-3.5 shrink-0" />
      <span><span className="font-mono">{code || 'error'}</span>{message ? ` · ${message}` : ''}</span>
    </div>
  )
}

function TCPReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[620px] text-left text-xs">
        <thead className="border-b text-muted-foreground">
          <tr><th className="px-2 py-2 font-medium">目的地</th><th className="px-2 py-2 font-medium">运营商</th><th className="px-2 py-2 font-medium">平均延迟</th><th className="px-2 py-2 font-medium">丢包</th><th className="px-2 py-2 font-medium">探测</th><th className="px-2 py-2 font-medium">状态</th></tr>
        </thead>
        <tbody className="divide-y">
          {(category.items ?? []).map((item, index) => {
            const average = number(item.rtt_avg_ms)
            const loss = number(item.loss_percent)
            return (
              <tr key={text(item.id, String(index))}>
                <td className="max-w-56 px-2 py-2 font-medium">{text(item.label)}</td>
                <td className="px-2 py-2">{carrierLabel(item.carrier)}</td>
                <td className="px-2 py-2 tabular-nums">{average === null ? '-' : `${average.toFixed(1)} ms`}</td>
                <td className={cn('px-2 py-2 tabular-nums', loss !== null && loss > 20 && 'text-destructive')}>{loss === null ? '-' : `${loss.toFixed(1)}%`}</td>
                <td className="px-2 py-2 font-mono">{text(item.probe_method)}</td>
                <td className="max-w-72 px-2 py-2">{item.error_message ? <span className="text-destructive">{text(item.error_code)} · {text(item.error_message)}</span> : <span className="text-success">完成</span>}</td>
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
            <span className={item.reached === true ? 'text-success' : 'text-warning'}>{item.reached === true ? '已到达' : '未到达'}</span>
          </div>
          <div className="grid gap-1 font-mono text-[11px] sm:grid-cols-2">
            {asRecords(item.hops).map((hop, hopIndex) => {
              const rtts = Array.isArray(hop.rtt_ms) ? hop.rtt_ms.filter((value): value is number => typeof value === 'number') : []
              return <div key={hopIndex} className="flex gap-2"><span className="w-6 text-right text-muted-foreground">{text(hop.hop)}</span><span className="min-w-0 truncate">{text(hop.address, '*')}</span><span className="ml-auto tabular-nums text-muted-foreground">{rtts.length ? `${rtts.map((value) => value.toFixed(1)).join('/')} ms` : '*'}</span></div>
            })}
          </div>
          <ErrorNotice code={text(item.error_code, '')} message={text(item.error_message, '')} />
        </div>
      ))}
    </div>
  )
}

function ProviderTable({ providers }: { providers: Array<Record<string, unknown>> }) {
  return (
    <>
      <div className="divide-y border-y sm:hidden">
        {providers.map((provider) => (
          <div key={text(provider.name)} className="space-y-2 py-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium">{text(provider.name)}</span>
              <Badge variant={statusBadge(text(provider.status))}>{statusLabel(text(provider.status))}</Badge>
            </div>
            <dl className="grid grid-cols-3 gap-2 text-xs">
              <div><dt className="text-muted-foreground">分数</dt><dd className="mt-0.5 tabular-nums">{number(provider.score) === null ? '-' : number(provider.score)?.toFixed(1)}</dd></div>
              <div><dt className="text-muted-foreground">类型</dt><dd className="mt-0.5 break-words">{text(provider.usage_type)}</dd></div>
              <div><dt className="text-muted-foreground">风险因子</dt><dd className="mt-0.5">{provider.error_message ? '-' : `${text(provider.factor_hits, '0')} / ${text(provider.effective_factors, '0')}`}</dd></div>
            </dl>
            {provider.error_message ? <ErrorNotice code={text(provider.error_code, '')} message={text(provider.error_message, '')} /> : null}
          </div>
        ))}
      </div>
      <div className="hidden max-w-full overflow-x-auto sm:block">
        <table className="w-full min-w-[600px] text-left text-xs">
        <thead className="border-b text-muted-foreground"><tr><th className="px-2 py-2 font-medium">数据库</th><th className="px-2 py-2 font-medium">状态</th><th className="px-2 py-2 font-medium">分数</th><th className="px-2 py-2 font-medium">类型</th><th className="px-2 py-2 font-medium">风险因子</th></tr></thead>
        <tbody className="divide-y">
          {providers.map((provider) => (
            <tr key={text(provider.name)}>
              <td className="px-2 py-2 font-medium">{text(provider.name)}</td>
              <td className="px-2 py-2"><Badge variant={statusBadge(text(provider.status))}>{statusLabel(text(provider.status))}</Badge></td>
              <td className="px-2 py-2 tabular-nums">{number(provider.score) === null ? '-' : number(provider.score)?.toFixed(1)}</td>
              <td className="px-2 py-2">{text(provider.usage_type)}</td>
              <td className="max-w-72 px-2 py-2">{provider.error_message ? <span className="text-destructive">{text(provider.error_code)} · {text(provider.error_message)}</span> : `${text(provider.factor_hits, '0')} / ${text(provider.effective_factors, '0')} 命中`}</td>
            </tr>
          ))}
        </tbody>
        </table>
      </div>
    </>
  )
}

function IPQualityReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="space-y-4">
      {(category.items ?? []).map((family, index) => {
        const dnsbl = asRecord(family.dnsbl)
        const network = asRecord(family.network)
        const asn = asRecord(network.asn)
        const streaming = asRecords(family.streaming)
        const dnsErrorCounts = new Map<string, number>()
        for (const entry of asRecords(dnsbl.errors)) {
          const reason = text(entry.error, 'DNS lookup failed')
          dnsErrorCounts.set(reason, (dnsErrorCounts.get(reason) ?? 0) + 1)
        }
        return (
          <section key={text(family.address_family, String(index))} className="space-y-3 border-t pt-4 first:border-0 first:pt-0">
            <div className="flex flex-wrap items-center gap-2">
              <h4 className="text-sm font-medium">{text(family.address_family).toUpperCase()}</h4>
              <Badge variant={statusBadge(text(family.status))}>{statusLabel(text(family.status))}</Badge>
              <span className="text-xs text-muted-foreground">{text(network.country)} · {asn.asn ? `AS${text(asn.asn)} ${text(asn.name, '')}` : statusLabel(text(asn.status))}</span>
            </div>
            <ErrorNotice code={text(asn.error_code, '')} message={text(asn.error_message, '')} />
            <ProviderTable providers={asRecords(family.providers)} />
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="border p-3">
                <div className="mb-2 flex items-center justify-between"><span className="text-xs font-medium">DNSBL</span><Badge variant={statusBadge(text(dnsbl.status))}>{statusLabel(text(dnsbl.status))}</Badge></div>
                <div className="text-xs text-muted-foreground">有效 {text(dnsbl.checked, '0')} · 黑名单 {text(dnsbl.blacklisted, '0')} · 标记 {text(dnsbl.marked, '0')} · 未知 {text(dnsbl.unknown, '0')}</div>
                {dnsErrorCounts.size ? <div className="mt-2 space-y-1 text-xs text-destructive">{Array.from(dnsErrorCounts).map(([reason, count]) => <div key={reason} className="break-words">{reason} · {count} 项</div>)}</div> : null}
              </div>
              <div className="border p-3">
                <div className="mb-2 text-xs font-medium">流媒体与 AI</div>
                <div className="flex flex-wrap gap-1.5">{streaming.map((service) => <Badge key={text(service.name)} variant={statusBadge(text(service.status))}>{text(service.name)} · {statusLabel(text(service.status))}</Badge>)}</div>
                {streaming.some((service) => service.error) ? <div className="mt-2 space-y-1 text-xs text-destructive">{streaming.filter((service) => service.error).map((service) => <div key={text(service.name)} className="break-words">{text(service.name)} · {text(service.error)}</div>)}</div> : null}
              </div>
            </div>
            <ErrorNotice code={text(family.error_code, '')} message={text(family.error_message, '')} />
          </section>
        )
      })}
    </div>
  )
}

function SpeedReport({ category }: { category: ServerTestCategoryResult }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[600px] text-left text-xs">
        <thead className="border-b text-muted-foreground"><tr><th className="px-2 py-2 font-medium">目的地</th><th className="px-2 py-2 font-medium">协议</th><th className="px-2 py-2 font-medium">上传</th><th className="px-2 py-2 font-medium">下载</th><th className="px-2 py-2 font-medium">状态</th></tr></thead>
        <tbody className="divide-y">{(category.items ?? []).map((item, index) => <tr key={text(item.id, String(index))}><td className="px-2 py-2 font-medium">{text(item.label)}</td><td className="px-2 py-2">{text(item.address_family).toUpperCase()}</td><td className="px-2 py-2 tabular-nums">{number(item.upload_mbps) === null ? '-' : `${number(item.upload_mbps)?.toFixed(1)} Mbps`}</td><td className="px-2 py-2 tabular-nums">{number(item.download_mbps) === null ? '-' : `${number(item.download_mbps)?.toFixed(1)} Mbps`}</td><td className="max-w-72 px-2 py-2">{item.error_message ? <span className="text-destructive">{text(item.error_code)} · {text(item.error_message)}</span> : statusLabel(text(item.status))}</td></tr>)}</tbody>
      </table>
    </div>
  )
}

function ReportCategory({ category }: { category: ServerTestCategoryResult }) {
  const isRoute = category.category === 'return_route_ipv4' || category.category === 'return_route_ipv6'
  const isIP = category.category === 'ip_quality'
  const isSpeed = category.category === 'speed'
  return (
    <section className="min-w-0 border-b py-5 last:border-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold">{categoryLabels[category.category]}</h3>
        <Badge variant={statusBadge(category.status)}>{statusLabel(category.status)}</Badge>
      </div>
      <ErrorNotice code={category.error_code} message={category.error_message} />
      {isIP ? <IPQualityReport category={category} /> : isRoute ? <RouteReport category={category} /> : isSpeed ? <SpeedReport category={category} /> : <TCPReport category={category} />}
    </section>
  )
}

function TestReport({ report, timezone }: { report: ServerTestReport; timezone?: string }) {
  return (
    <div className="min-w-0 space-y-4">
      <div className="grid gap-3 border-y py-3 text-xs sm:grid-cols-4">
        <div><span className="block text-muted-foreground">状态</span><span className="mt-1 block font-medium">{statusLabel(report.status)}</span></div>
        <div><span className="block text-muted-foreground">完成时间</span><span className="mt-1 block">{formatDateTime(report.completed_at, timezone)}</span></div>
        <div><span className="block text-muted-foreground">Agent</span><span className="mt-1 block font-mono">{report.agent_version}</span></div>
        <div><span className="block text-muted-foreground">权限 / 沙箱</span><span className="mt-1 block">{report.environment.privileges} · {report.environment.sandbox}</span></div>
      </div>
      {report.environment.degraded || report.environment.sandbox_reason ? <div className="flex items-start gap-2 bg-warning/10 px-3 py-2 text-xs text-warning"><AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" /><span>{report.environment.degraded_reason || report.environment.sandbox_reason}</span></div> : null}
      <ErrorNotice code={report.error_code} message={report.error_message} />
      <div>{report.categories.map((category) => <ReportCategory key={category.category} category={category} />)}</div>
    </div>
  )
}

export function ServerTestPanel({ server, active, timezone }: { server: Server; active: boolean; timezone?: string }) {
  const [task, setTask] = useState<ServerTestTask | null>(null)
  const [loading, setLoading] = useState(false)
  const [selectionOpen, setSelectionOpen] = useState(false)
  const [progressOpen, setProgressOpen] = useState(false)
  const [reportOpen, setReportOpen] = useState(false)
  const [warningOpen, setWarningOpen] = useState(false)
  const [pendingCategory, setPendingCategory] = useState<ServerTestCategory | null>(null)
  const [selected, setSelected] = useState<ServerTestCategory[]>(server.machine_type === 'nat' ? ['ip_quality'] : directDefaults)
  const [catalog, setCatalog] = useState<ServerTestCatalogStatus | null>(null)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  const loadTask = useCallback(async () => {
    const latest = await api.serverTest(server.id, { display: 'silent' })
    setTask(latest)
    return latest
  }, [server.id])

  useEffect(() => {
    if (!active) return
    let cancelled = false
    setLoading(true)
    api.serverTest(server.id, { display: 'silent' })
      .then((latest) => { if (!cancelled) setTask(latest) })
      .catch(() => { if (!cancelled) setTask(null) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [active, server.id])

  useEffect(() => {
    if (!active || !task || isTerminal(task.status)) return
    const timer = setInterval(() => {
      loadTask().then((latest) => {
        if (latest && isTerminal(latest.status)) {
          setProgressOpen(false)
          setReportOpen(true)
        }
      }).catch(() => undefined)
    }, 1000)
    return () => clearInterval(timer)
  }, [active, loadTask, task])

  const progressPercent = useMemo(() => {
    if (!task?.progress || task.progress.total <= 0) return 0
    return Math.min(100, (task.progress.completed / task.progress.total) * 100)
  }, [task])

  const openSelection = () => {
    setSelected(server.machine_type === 'nat' ? ['ip_quality'] : directDefaults)
    setError('')
    setSelectionOpen(true)
    setCatalogLoading(true)
    api.serverTestCatalogStatus({ display: 'silent' })
      .then(setCatalog)
      .catch((reason) => setError(errorMessage(reason)))
      .finally(() => setCatalogLoading(false))
  }

  const toggleCategory = (category: ServerTestCategory, checked: boolean) => {
    if (!checked) {
      setSelected((current) => current.filter((item) => item !== category))
      return
    }
    if ((server.machine_type === 'nat' && category !== 'ip_quality') || category === 'speed') {
      setPendingCategory(category)
      setWarningOpen(true)
      return
    }
    setSelected((current) => [...current, category])
  }

  const confirmWarning = () => {
    if (pendingCategory) setSelected((current) => current.includes(pendingCategory) ? current : [...current, pendingCategory])
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
      setProgressOpen(true)
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
      try { setCatalog(await api.serverTestCatalogStatus({ display: 'silent' })) } catch { /* keep current status */ }
    } finally {
      setRefreshing(false)
    }
  }

  const primaryAction = () => {
    if (!task) return openSelection()
    if (!isTerminal(task.status)) return setProgressOpen(true)
    setReportOpen(true)
  }

  return (
    <section aria-labelledby="server-test-heading" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 id="server-test-heading" className="text-sm font-semibold">服务器测试</h3>
          <p className="mt-1 text-xs text-muted-foreground">{task ? `${statusLabel(task.status)} · ${formatDateTime(task.updated_at, timezone)}` : '尚无测试结果'}</p>
        </div>
        <Button variant={task?.result ? 'outline' : 'default'} onClick={primaryAction} disabled={loading}>
          {loading ? <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" /> : task && !isTerminal(task.status) ? <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" /> : task ? <FlaskConicalIcon /> : <CircleDotIcon />}
          {loading ? '读取状态' : task && !isTerminal(task.status) ? '查看测试进度' : task ? '查看测试结果' : '点击运行测试'}
        </Button>
      </div>
      {task?.error_message && !task.result ? <ErrorNotice code={task.error_code} message={task.error_message} /> : null}

      <Dialog open={selectionOpen} onOpenChange={setSelectionOpen}>
        <DialogContent className="max-h-[88vh] w-[calc(100%-2rem)] overflow-y-auto sm:max-w-3xl">
          <DialogHeader><DialogTitle>运行服务器测试</DialogTitle><DialogDescription>选择本次原子任务包含的测试项目</DialogDescription></DialogHeader>
          <div className="space-y-4">
            {(Object.keys(groupMeta) as Array<keyof typeof groupMeta>).map((group) => {
              const meta = groupMeta[group]
              const Icon = meta.icon
              return <fieldset key={group} className="border-t pt-3 first:border-0 first:pt-0"><legend className="mb-2 flex items-center gap-2 text-xs font-medium"><Icon className="size-3.5" />{meta.label}</legend><div className="grid gap-2 sm:grid-cols-2">{categoryOptions.filter((option) => option.group === group).map((option) => <label key={option.category} className={cn('flex cursor-pointer items-start gap-3 border px-3 py-2.5 transition-colors hover:bg-muted/40', selected.includes(option.category) && 'border-primary/40 bg-primary/[0.04]')}><input type="checkbox" className="mt-0.5 size-4 accent-primary" checked={selected.includes(option.category)} onChange={(event) => toggleCategory(option.category, event.target.checked)} /><span className="min-w-0"><span className="block text-sm font-medium">{option.label}</span><span className="mt-0.5 block text-xs text-muted-foreground">{option.description}</span></span></label>)}</div></fieldset>
            })}
            <div className={cn('flex items-start gap-2 border px-3 py-2 text-xs', catalog?.available ? 'border-success/30 bg-success/5' : 'border-warning/30 bg-warning/5')}>
              {catalogLoading || catalog?.refreshing ? <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin motion-reduce:animate-none" /> : catalog?.available ? <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" /> : <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0 text-warning" />}
              <div className="min-w-0 flex-1"><span className="font-medium">{catalogLoading ? '读取节点目录' : catalog?.available ? '节点目录可用' : '节点目录不可用'}</span>{catalog?.fetched_at ? <span className="ml-2 text-muted-foreground">{formatDateTime(catalog.fetched_at, timezone)}</span> : null}{catalog?.last_error ? <p className="mt-1 break-words text-warning">最近刷新失败：{catalog.last_error}</p> : null}</div>
              {!catalogLoading ? <Button size="sm" variant="outline" onClick={refreshCatalog} disabled={refreshing}>{refreshing ? <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" /> : <RefreshCwIcon />}刷新</Button> : null}
            </div>
            {error ? <ErrorNotice code="request_failed" message={error} /> : null}
          </div>
          <DialogFooter><Button variant="outline" onClick={() => setSelectionOpen(false)}>取消</Button><Button onClick={submit} disabled={selected.length === 0 || !catalog?.available || submitting || refreshing}>{submitting ? <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" /> : <FlaskConicalIcon />}下发 {selected.length} 项测试</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={warningOpen} onOpenChange={(open) => { setWarningOpen(open); if (!open) setPendingCategory(null) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader><DialogTitle>确认运行 {pendingCategory ? categoryLabels[pendingCategory] : '测试'}</DialogTitle><DialogDescription>{server.machine_type === 'nat' && pendingCategory !== 'ip_quality' ? 'NAT 机型的 TCP、回程或测速测试可能因系统、端口映射或运营商限制而不可用。' : '该项目会产生大量网络流量。'}{pendingCategory === 'speed' ? ' 单线程测速最多可能消耗约 5.5 GiB 流量。' : ''}</DialogDescription></DialogHeader>
          <DialogFooter><Button variant="outline" onClick={() => { setWarningOpen(false); setPendingCategory(null) }}>取消</Button><Button onClick={confirmWarning}>确认勾选</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={progressOpen} onOpenChange={setProgressOpen}>
        <DialogContent className="w-[calc(100%-2rem)] sm:max-w-xl">
          <DialogHeader><DialogTitle>服务器测试进行中</DialogTitle><DialogDescription>进度为尽力上报，最终报告为权威结果</DialogDescription></DialogHeader>
          <div className="space-y-4">
            <div><div className="mb-2 flex items-center justify-between text-xs"><span>{task ? statusLabel(task.status) : '读取状态'}</span><span className="tabular-nums">{Math.round(progressPercent)}%</span></div><Progress value={progressPercent} /></div>
            <div className="divide-y border-y">{(task?.progress?.categories ?? task?.categories.map((category) => ({ category, status: 'pending', completed: 0, total: 1, message: '' })) ?? []).map((progress) => <div key={progress.category} className="flex items-center gap-3 py-3"><span className={cn('flex size-7 shrink-0 items-center justify-center border', progress.status === 'running' && 'border-info text-info', ['available', 'limited', 'succeeded'].includes(progress.status) && 'border-success text-success', ['unavailable', 'failed'].includes(progress.status) && 'border-destructive text-destructive')}>{progress.status === 'running' ? <LoaderCircleIcon className="size-3.5 animate-spin motion-reduce:animate-none" /> : ['available', 'limited', 'succeeded'].includes(progress.status) ? <CheckIcon className="size-3.5" /> : ['unavailable', 'failed'].includes(progress.status) ? <XCircleIcon className="size-3.5" /> : <CircleDotIcon className="size-3.5" />}</span><div className="min-w-0 flex-1"><div className="flex items-center justify-between gap-3 text-xs"><span className="font-medium">{categoryLabels[progress.category]}</span><span className="tabular-nums text-muted-foreground">{progress.completed}/{progress.total}</span></div>{progress.message ? <p className="mt-1 truncate text-xs text-muted-foreground">{progress.message}</p> : null}</div></div>)}</div>
            {task?.error_message ? <ErrorNotice code={task.error_code} message={task.error_message} /> : null}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={reportOpen} onOpenChange={setReportOpen}>
        <DialogContent initialFocus={false} className="max-h-[90vh] w-[calc(100%-1rem)] min-w-0 overflow-x-hidden overflow-y-auto p-5 sm:max-w-5xl">
          <DialogHeader><DialogTitle>服务器测试报告</DialogTitle><DialogDescription>{server.alias} · 仅保留最近一次测试结果</DialogDescription></DialogHeader>
          {task?.result ? <TestReport report={task.result} timezone={timezone} /> : <ErrorNotice code={task?.error_code || 'result_unavailable'} message={task?.error_message || '测试已结束，但结果报告不可用'} />}
          <DialogFooter className="grid grid-cols-1 gap-2 sm:flex"><Button variant="outline" onClick={() => setReportOpen(false)}>关闭</Button><Button onClick={() => { setReportOpen(false); openSelection() }}><RefreshCwIcon />重新测试</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
