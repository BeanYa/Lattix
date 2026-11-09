import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import {
  AlertTriangleIcon,
  ArrowUpRightIcon,
  ChevronDownIcon,
  Clock3Icon,
  HistoryIcon,
  RefreshCwIcon,
  ServerIcon,
  ShieldCheckIcon,
} from 'lucide-react'

import { Notice } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { serverTestStatusLabel } from '@/lib/status'
import type {
  ServerInspectionItem,
  ServerInspectionRun,
  ServerInspectionSummary,
  ServerInspectionsResponse,
} from '@/lib/types'
import { cn } from '@/lib/utils'

import './inspection.css'

/** 运行监控页轮询间隔：周体检变化不频繁，60 秒足够。 */
const POLL_INTERVAL_MS = 60_000

function inspectionStatusTone(status: string | undefined): string {
  switch (status) {
    case 'succeeded':
    case 'available':
      return 'is-ok'
    case 'completed_with_errors':
    case 'limited':
    case 'running':
    case 'accepted':
      return 'is-warn'
    case 'failed':
    case 'unavailable':
      return 'is-red'
    default:
      return 'is-muted'
  }
}

function statusLabel(status: string | undefined): string {
  if (!status) return '未体检'
  return serverTestStatusLabel(status)
}

function metric(value: number | undefined | null, suffix: string): string {
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(1)}${suffix}` : '—'
}

function latestTime(run: ServerInspectionRun | undefined): string {
  return formatDateTime(run?.completed_at || run?.started_at)
}

function historyKey(run: ServerInspectionRun): string {
  return `${run.task_id}:${run.started_at}`
}

function SummaryMetric({
  icon,
  label,
  value,
  detail,
  tone,
}: {
  icon: ReactNode
  label: string
  value: ReactNode
  detail: string
  tone?: 'ok' | 'warn' | 'danger'
}) {
  return (
    <article className="rt-stat-cell rt-insp-stat">
      <span className="rt-stat-label">
        {icon}
        {label}
      </span>
      <strong className="rt-stat-value" data-tone={tone === 'warn' ? 'warn' : undefined}>
        {value}
      </strong>
      <small className="rt-stat-detail" title={detail}>
        {detail}
      </small>
    </article>
  )
}

function IPQualityCell({ summary }: { summary?: ServerInspectionSummary }) {
  const families = summary?.ip_quality?.families ?? []
  if (families.length === 0) {
    return <span className="rt-insp-muted">—</span>
  }
  return (
    <div className="rt-insp-stack">
      {families.map((family) => {
        const blacklisted = family.dnsbl_blacklisted ?? 0
        const marked = family.dnsbl_marked ?? 0
        const unlocked = family.media_unlocked ?? 0
        const mediaTotal = family.media_total ?? 0
        return (
          <span key={family.family} className="rt-insp-family">
            <span className="rt-insp-chip" data-tone={blacklisted > 0 ? 'danger' : 'ok'}>
              {family.family.toUpperCase()}
            </span>
            <span className="rt-insp-family-text">
              {family.type || '类型未知'}
              {family.ip ? ` · ${family.ip}` : ''}
            </span>
            <span
              className={cn('rt-insp-dot', blacklisted > 0 ? 'is-danger' : 'is-ok')}
              title={`DNSBL 黑名单 ${blacklisted} · 标记 ${marked}`}
            >
              DNSBL {blacklisted > 0 ? blacklisted : '正常'}
            </span>
            <span className="rt-insp-media" title={`流媒体解锁 ${unlocked}/${mediaTotal}`}>
              解锁 {unlocked}/{mediaTotal}
            </span>
          </span>
        )
      })}
    </div>
  )
}

function IPv4Cell({ summary }: { summary?: ServerInspectionSummary }) {
  const tcp = summary?.tcp_ipv4
  if (!tcp) return <span className="rt-insp-muted">—</span>
  const lossTone =
    (tcp.max_loss_percent ?? 0) >= 40 ? 'danger' : (tcp.max_loss_percent ?? 0) > 0 ? 'warn' : 'ok'
  return (
    <div className="rt-insp-stack">
      <span className="rt-insp-primary">
        {metric(tcp.avg_rtt_ms, ' ms')}
        <span className={cn('rt-insp-dot', `is-${lossTone}`)}>
          丢包 {metric(tcp.max_loss_percent, '%')}
        </span>
      </span>
      <span className="rt-insp-muted">
        {tcp.targets - tcp.failed}/{tcp.targets} 目标可达
        {tcp.worst_target ? ` · 最差 ${tcp.worst_target}` : ''}
      </span>
    </div>
  )
}

function ConnectivityCell({ summary }: { summary?: ServerInspectionSummary }) {
  const connectivity = summary?.connectivity
  if (!connectivity) return <span className="rt-insp-muted">—</span>
  const reachable = Math.max(0, connectivity.targets - connectivity.failed)
  return (
    <div className="rt-insp-stack">
      <span className="rt-insp-primary">
        {reachable}/{connectivity.targets}
        <span className="rt-insp-muted">可达</span>
      </span>
      <span className="rt-insp-muted">平均 {metric(connectivity.avg_rtt_ms, ' ms')}</span>
    </div>
  )
}

function CatalogProbesCell({ summary }: { summary?: ServerInspectionSummary }) {
  const probes = summary?.catalog_probes
  if (!probes || probes.rows.length === 0) {
    return <span className="rt-insp-muted">暂无样本</span>
  }
  return (
    <div
      className="rt-insp-carrier-list"
      title={`${probes.province}${probes.city ? ` · ${probes.city}` : ''}`}
    >
      {probes.rows.map((row) => (
        <span key={row.carrier} className="rt-insp-carrier">
          <span>{row.label}</span>
          <strong>{metric(row.latency_ms, ' ms')}</strong>
          <span className={cn('rt-insp-dot', (row.loss_percent ?? 0) > 0 ? 'is-warn' : 'is-ok')}>
            {metric(row.loss_percent, '%')}
          </span>
        </span>
      ))}
    </div>
  )
}

function Warnings({ summary }: { summary?: ServerInspectionSummary }) {
  const warnings = summary?.warnings ?? []
  if (warnings.length === 0) return null
  return (
    <span className="rt-insp-warn" title={warnings.join('\n')}>
      <AlertTriangleIcon aria-hidden="true" />
      {warnings.length}
    </span>
  )
}

function HistoryToggle({
  count,
  open,
  onToggle,
}: {
  count: number
  open: boolean
  onToggle: () => void
}) {
  if (count === 0) {
    return <span className="rt-insp-muted">—</span>
  }
  return (
    <button
      type="button"
      className="rt-insp-history-toggle"
      onClick={onToggle}
      aria-expanded={open}
    >
      <HistoryIcon aria-hidden="true" />
      {count} 次
      <ChevronDownIcon aria-hidden="true" className={open ? 'is-open' : undefined} />
    </button>
  )
}

function HistoryLine({ item }: { item: ServerInspectionItem }) {
  return (
    <tr className="rt-insp-history-line">
      <td colSpan={7}>
        <div className="rt-insp-history-panel">
          <span className="rt-insp-history-title">最近 {item.history.length} 次体检</span>
          {item.history.map((run) => (
            <div key={historyKey(run)} className="rt-insp-history-row">
              <span className="rt-insp-history-time">{latestTime(run)}</span>
              <span className={cn('cg-status', inspectionStatusTone(run.status))}>
                {statusLabel(run.status)}
              </span>
              <span className="rt-insp-history-metrics">
                IPv4 {metric(run.summary?.tcp_ipv4?.avg_rtt_ms, 'ms')} · 连通性{' '}
                {run.summary?.connectivity
                  ? `${run.summary.connectivity.targets - run.summary.connectivity.failed}/${run.summary.connectivity.targets}`
                  : '—'}
                {run.summary?.catalog_probes
                  ? ` · 三网 ${run.summary.catalog_probes.rows.filter((row) => row.latency_ms != null).length}/${run.summary.catalog_probes.rows.length}`
                  : ''}
              </span>
            </div>
          ))}
        </div>
      </td>
    </tr>
  )
}

function InspectionRow({
  item,
  openHistory,
  onToggleHistory,
}: {
  item: ServerInspectionItem
  openHistory: boolean
  onToggleHistory: () => void
}) {
  const latest = item.latest
  const summary = latest?.summary
  const status = latest?.status
  return (
    <>
      <tr>
        <td>
          <Link href={`/network/servers?sel=${item.server_id}`} className="rt-insp-server">
            <ServerIcon aria-hidden="true" />
            <span>
              <strong>{item.server_alias || `服务器 #${item.server_id}`}</strong>
              <small>#{item.server_id}</small>
            </span>
            <ArrowUpRightIcon aria-hidden="true" />
          </Link>
        </td>
        <td>
          <div className="rt-insp-stack">
            <span className={cn('cg-status', inspectionStatusTone(status))}>
              {statusLabel(status)}
            </span>
            <span className="rt-insp-muted">{latestTime(latest)}</span>
            {latest?.error_message ? (
              <span className="rt-insp-error" title={latest.error_message}>
                {latest.error_code || 'error'}
              </span>
            ) : null}
          </div>
        </td>
        <td>
          <IPQualityCell summary={summary} />
        </td>
        <td>
          <IPv4Cell summary={summary} />
        </td>
        <td>
          <ConnectivityCell summary={summary} />
        </td>
        <td>
          <CatalogProbesCell summary={summary} />
        </td>
        <td>
          <div className="rt-insp-stack">
            <Warnings summary={summary} />
            <HistoryToggle
              count={item.history.length}
              open={openHistory}
              onToggle={onToggleHistory}
            />
          </div>
        </td>
      </tr>
      {openHistory ? <HistoryLine item={item} /> : null}
    </>
  )
}

/**
 * ServerInspectionTab 是「服务器体检」Tab 的展示层：直连机型每周自动复用
 * server-test（IP 质量 / IPv4 TCP / 连通性），并附完成时刻的三网测速快照。
 * NAT 机型不参与，因此列表只含直连服务器；历史来自面板侧 server_inspection_runs。
 */
export default function ServerInspectionTab() {
  const [data, setData] = useState<ServerInspectionsResponse | null>(null)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const [openHistory, setOpenHistory] = useState<number | null>(null)

  const load = useCallback(async () => {
    try {
      const response = await api.serverInspections({ display: 'silent' })
      setData(response)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [load])

  const summary = useMemo(() => {
    const items = data?.items ?? []
    const inspected = items.filter((item) => Boolean(item.latest?.summary)).length
    const issues = items.filter((item) => {
      const status = item.latest?.status
      return (
        status === 'failed' ||
        status === 'completed_with_errors' ||
        (item.latest?.summary?.warnings?.length ?? 0) > 0
      )
    }).length
    const last = items
      .map((item) => item.latest?.completed_at || item.latest?.started_at)
      .filter((value): value is string => Boolean(value))
      .sort()
      .at(-1)
    return { items, inspected, issues, last }
  }, [data])

  const onRefresh = async () => {
    setRefreshing(true)
    await load()
    setRefreshing(false)
  }

  if (data === null) {
    if (error) {
      return (
        <Notice tone="danger" title="无法读取服务器体检" className="max-w-xl">
          {error}
        </Notice>
      )
    }
    return (
      <div className="rt-loading" role="status" aria-label="正在读取服务器体检">
        <span className="rt-loading-label">LOADING / SERVER INSPECTION</span>
        <div className="rt-loading-bar" aria-hidden="true">
          <i />
          <i />
          <i />
        </div>
      </div>
    )
  }

  return (
    <>
      {error ? (
        <Notice tone="warning" title="本次刷新失败">
          正在显示上一次有效数据：{error}
        </Notice>
      ) : null}

      <section className="rt-mod" aria-label="体检概览">
        <header className="rt-mod-head">
          <span className="rt-tab">INSPECTION</span>
          <h2 className="rt-mod-title">服务器体检</h2>
          <span className="rt-mod-meta">直连机型 · 每周</span>
          <div className="rt-head-actions">
            <button
              type="button"
              className="cg-icon-button"
              onClick={() => void onRefresh()}
              aria-label="立即刷新体检汇总"
              title="立即刷新体检汇总"
              disabled={refreshing}
            >
              <RefreshCwIcon className={refreshing ? 'animate-spin' : undefined} />
            </button>
          </div>
        </header>
        <div className="rt-insp-stat-grid">
          <SummaryMetric
            icon={<ServerIcon />}
            label="直连机"
            value={summary.items.length}
            detail="参与周体检的服务器"
          />
          <SummaryMetric
            icon={<ShieldCheckIcon />}
            label="已出报告"
            value={summary.inspected}
            detail="最近一次有完整摘要"
          />
          <SummaryMetric
            icon={<AlertTriangleIcon />}
            label="需关注"
            value={summary.issues}
            detail="失败 / 部分异常 / 有告警"
            tone={summary.issues > 0 ? 'warn' : undefined}
          />
          <SummaryMetric
            icon={<Clock3Icon />}
            label="最近完成"
            value={summary.last ? formatDateTime(summary.last) : '—'}
            detail="最近一次体检完成时间"
          />
        </div>
        <p className="rt-note rt-insp-note">
          每周自动复用服务器测试，仅启用 IP 质量、IPv4 TCP 与网络连通性；首次接入或超过 7
          天重连时立即补跑。 三网测速取完成时刻的新鲜样本，不重复探测；NAT 机型不参与。
        </p>
      </section>

      <section className="rt-mod" aria-labelledby="server-inspection-heading">
        <header className="rt-mod-head">
          <span className="rt-tab">FLEET</span>
          <h2 className="rt-mod-title" id="server-inspection-heading">
            机群体检结果
          </h2>
          <span className="rt-mod-meta">{summary.items.length} 台直连</span>
        </header>
        <div className="rt-insp-wrap">
          {summary.items.length === 0 ? (
            <p className="rt-insp-empty">
              <ServerIcon aria-hidden="true" />
              暂无直连服务器；NAT 机型不参与周体检。
            </p>
          ) : (
            <table className="rt-insp-table">
              <thead>
                <tr>
                  <th scope="col">服务器</th>
                  <th scope="col">最近状态</th>
                  <th scope="col">IP 质量</th>
                  <th scope="col">IPv4 三网</th>
                  <th scope="col">连通性</th>
                  <th scope="col">三网测速</th>
                  <th scope="col">告警 / 历史</th>
                </tr>
              </thead>
              <tbody>
                {summary.items.map((item) => (
                  <InspectionRow
                    key={item.server_id}
                    item={item}
                    openHistory={openHistory === item.server_id}
                    onToggleHistory={() =>
                      setOpenHistory((current) =>
                        current === item.server_id ? null : item.server_id,
                      )
                    }
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <p className="rt-note">
        报告保存于面板侧（最近 26 次），Agent 只保留最后一次完整报告；手动测试与周体检共用同一份
        server-test 任务，详情可在服务器页查看。
      </p>
    </>
  )
}
