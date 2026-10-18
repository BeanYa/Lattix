import { useCallback, useEffect, useState } from 'react'
import { RefreshCwIcon, Trash2Icon } from 'lucide-react'
import { useLocation, useSearch } from 'wouter'

import { Notice } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime } from '@/lib/format'
import {
  OPERATION_PAGE_SIZE_OPTIONS,
  REFRESH_OPTIONS,
  useStoredNumber,
  type OperationPageSize,
  type RefreshSeconds,
} from '@/lib/log-preferences'
import { initialSeverityFromSearch, stripSeverityParam } from '@/lib/log-severity-param'
import { useTimezone } from '@/lib/timezone'
import type { LogSeverity, OperationCategory, OperationLogEntry, Server } from '@/lib/types'
import { cn } from '@/lib/utils'

import './logs.css'

const REFRESH_VALUES = REFRESH_OPTIONS.map((option) => option.value)
const CATEGORY_OPTIONS: { value: OperationCategory; label: string }[] = [
  { value: 'server', label: '服务器' },
  { value: 'chain', label: '链路' },
  { value: 'user', label: '用户' },
  { value: 'settings', label: '设置' },
  { value: 'panel', label: '面板' },
  { value: 'agent', label: 'Agent' },
  { value: 'command', label: '命令' },
  { value: 'auth', label: '认证' },
  { value: 'log', label: '日志' },
]
const SEVERITY_OPTIONS: { value: LogSeverity; label: string }[] = [
  { value: 'info', label: '信息' },
  { value: 'warning', label: '警告' },
  { value: 'error', label: '错误' },
]
const SEVERITY_TONE: Record<LogSeverity, 'is-red' | 'is-warn' | 'is-muted'> = {
  debug: 'is-muted',
  info: 'is-muted',
  warning: 'is-warn',
  error: 'is-red',
}

function severityBadge(severity: LogSeverity) {
  const label = SEVERITY_OPTIONS.find((option) => option.value === severity)?.label ?? severity
  return <span className={cn('cg-status', SEVERITY_TONE[severity] ?? 'is-muted')}>{label}</span>
}

function prettyDetail(detail: string): string {
  if (!detail) return ''
  try {
    return JSON.stringify(JSON.parse(detail), null, 2)
  } catch {
    return detail
  }
}

function toRFC3339(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export default function OperationLogs() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const search = useSearch()
  const [, navigate] = useLocation()
  const [items, setItems] = useState<OperationLogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [detailEntry, setDetailEntry] = useState<OperationLogEntry | null>(null)
  const [offset, setOffset] = useState(0)
  const [severity, setSeverity] = useState<LogSeverity | ''>(() =>
    initialSeverityFromSearch(search),
  )
  const [category, setCategory] = useState<OperationCategory | ''>('')
  const [serverId, setServerId] = useState('')
  const [operator, setOperator] = useState('')
  const [query, setQuery] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [filters, setFilters] = useState({ operator: '', query: '', from: '', to: '' })
  const [pageSize, setPageSize] = useStoredNumber<OperationPageSize>(
    'lattix.logs.operations.page-size.v1',
    10,
    OPERATION_PAGE_SIZE_OPTIONS,
  )
  const [refreshSeconds, setRefreshSeconds] = useStoredNumber<RefreshSeconds>(
    'lattix.logs.operations.refresh.v1',
    0,
    REFRESH_VALUES,
  )

  const load = useCallback(() => {
    setError('')
    return api
      .operationLogs({
        severity: severity || undefined,
        category: category || undefined,
        server_id: serverId ? Number(serverId) : undefined,
        operator: filters.operator || undefined,
        q: filters.query || undefined,
        from: toRFC3339(filters.from),
        to: toRFC3339(filters.to),
        limit: pageSize,
        offset,
      })
      .then((page) => {
        setItems(page.items)
        setTotal(page.total)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [category, filters, offset, pageSize, serverId, severity])

  useEffect(() => {
    api
      .servers()
      .then(setServers)
      .catch(() => {})
  }, [])

  // Strip the severity param after applying it, so refresh and reset-filters
  // behave like a plain visit while keeping unrelated params intact.
  useEffect(() => {
    const next = stripSeverityParam(search)
    if (next === search) return
    navigate(`/logs/operations${next}`, { replace: true })
  }, [navigate, search])

  useEffect(() => {
    setLoading(true)
    void load()
    if (refreshSeconds === 0) return
    const timer = window.setInterval(load, refreshSeconds * 1000)
    return () => window.clearInterval(timer)
  }, [load, refreshSeconds])

  const applyFilters = () => {
    setFilters({
      operator: operator.trim(),
      query: query.trim(),
      from,
      to,
    })
    setOffset(0)
  }

  const resetFilters = () => {
    setSeverity('')
    setCategory('')
    setServerId('')
    setOperator('')
    setQuery('')
    setFrom('')
    setTo('')
    setFilters({ operator: '', query: '', from: '', to: '' })
    setOffset(0)
  }

  const clearLogs = async () => {
    if (
      !(await confirm({
        title: '清空操作日志',
        description: '确定清空操作日志？清空操作本身会作为新的第一条记录保留。',
        confirmLabel: '清空日志',
        destructive: true,
      }))
    )
      return
    try {
      await api.clearOperationLogs()
      setOffset(0)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="logs">
      {/* 筛选条件 */}
      <section className="log-mod" aria-label="筛选条件">
        <header className="log-mod-head">
          <span className="log-tab">FILTER</span>
          <h2 className="log-mod-title">筛选条件</h2>
          <span className="log-mod-meta">
            {items.length} / {total} 条已载入
          </span>
        </header>
        <div className="log-fields">
          <div className="log-field">
            <Label className="log-field-label">程度</Label>
            <Select
              value={severity || null}
              onValueChange={(value) => {
                setSeverity((value as LogSeverity) ?? '')
                setOffset(0)
              }}
              items={SEVERITY_OPTIONS}
            >
              <SelectTrigger className="w-28">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {SEVERITY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className="log-field">
            <Label className="log-field-label">类别</Label>
            <Select
              value={category || null}
              onValueChange={(value) => {
                setCategory((value as OperationCategory) ?? '')
                setOffset(0)
              }}
              items={CATEGORY_OPTIONS}
            >
              <SelectTrigger className="w-32">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {CATEGORY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className="log-field">
            <Label className="log-field-label">服务器</Label>
            <Select
              value={serverId || null}
              onValueChange={(value) => {
                setServerId(value ? String(value) : '')
                setOffset(0)
              }}
              items={servers.map((server) => ({ value: String(server.id), label: server.alias }))}
            >
              <SelectTrigger className="w-40">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {servers.map((server) => (
                    <SelectItem key={server.id} value={String(server.id)}>
                      {server.alias}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className="log-field">
            <Label htmlFor="operation-operator" className="log-field-label">
              操作员
            </Label>
            <Input
              id="operation-operator"
              className="w-32"
              value={operator}
              onChange={(event) => setOperator(event.target.value)}
            />
          </div>
          <div className="log-field log-field-wide">
            <Label htmlFor="operation-query" className="log-field-label">
              关键字
            </Label>
            <Input
              id="operation-query"
              className="w-48"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="动作 / 详情 / 请求 ID"
            />
          </div>
          <div className="log-field">
            <Label htmlFor="operation-from" className="log-field-label">
              开始时间
            </Label>
            <Input
              id="operation-from"
              type="datetime-local"
              value={from}
              onChange={(event) => setFrom(event.target.value)}
            />
          </div>
          <div className="log-field">
            <Label htmlFor="operation-to" className="log-field-label">
              结束时间
            </Label>
            <Input
              id="operation-to"
              type="datetime-local"
              value={to}
              onChange={(event) => setTo(event.target.value)}
            />
          </div>
          <div className="log-field-actions">
            <Button variant="outline" size="sm" onClick={applyFilters}>
              筛选
            </Button>
            <Button variant="ghost" size="sm" onClick={resetFilters}>
              重置
            </Button>
          </div>
        </div>
      </section>

      {error ? <Notice tone="danger">{error}</Notice> : null}

      {/* 操作日志列表 */}
      <section className="log-mod" aria-label="操作日志列表">
        <header className="log-mod-head">
          <span className="log-tab">LOG</span>
          <h2 className="log-mod-title">操作日志</h2>
          <span className="log-mod-meta">共 {total} 条</span>
          <div className="log-head-actions">
            <Label htmlFor="operation-page-size" className="log-head-label">
              每页
            </Label>
            <Select
              value={String(pageSize)}
              onValueChange={(value) => {
                setPageSize(Number(value) as OperationPageSize)
                setOffset(0)
              }}
              items={OPERATION_PAGE_SIZE_OPTIONS.map((value) => ({
                value: String(value),
                label: `${value} 条`,
              }))}
            >
              <SelectTrigger id="operation-page-size" className="w-24" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {OPERATION_PAGE_SIZE_OPTIONS.map((value) => (
                    <SelectItem key={value} value={String(value)}>
                      {value} 条
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Label htmlFor="operation-refresh" className="log-head-label">
              刷新
            </Label>
            <Select
              value={String(refreshSeconds)}
              onValueChange={(value) => setRefreshSeconds(Number(value) as RefreshSeconds)}
              items={REFRESH_OPTIONS.map((option) => ({ ...option, value: String(option.value) }))}
            >
              <SelectTrigger id="operation-refresh" className="w-28" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {REFRESH_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={String(option.value)}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              <RefreshCwIcon data-icon="inline-start" />
              刷新
            </Button>
            <Button variant="destructive" size="sm" onClick={clearLogs}>
              <Trash2Icon data-icon="inline-start" />
              清空
            </Button>
          </div>
        </header>
        <div className="log-table-wrap">
          <table className="log-table">
            <thead>
              <tr>
                <th scope="col">时间</th>
                <th scope="col">程度</th>
                <th scope="col" className="log-col-secondary">
                  类别
                </th>
                <th scope="col">动作</th>
                <th scope="col" className="log-col-secondary">
                  服务器
                </th>
                <th scope="col" className="log-col-secondary">
                  来源
                </th>
                <th scope="col">详情</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={7} className="log-table-empty">
                    加载中…
                  </td>
                </tr>
              ) : items.length === 0 ? (
                <tr>
                  <td colSpan={7} className="log-table-empty">
                    暂无记录
                  </td>
                </tr>
              ) : (
                items.map((entry) => {
                  const detail = prettyDetail(entry.detail)
                  const categoryLabel =
                    CATEGORY_OPTIONS.find((option) => option.value === entry.category)?.label ??
                    entry.category
                  return (
                    <tr
                      key={entry.event_id}
                      className={detail ? 'is-clickable' : undefined}
                      onClick={() => detail && setDetailEntry(entry)}
                    >
                      <td className="log-time">{formatDateTime(entry.timestamp, timezone)}</td>
                      <td>{severityBadge(entry.severity)}</td>
                      <td className="log-col-secondary">
                        <span className="cg-status">{categoryLabel}</span>
                      </td>
                      <td className="log-action">{entry.action}</td>
                      <td className="log-muted log-col-secondary">{entry.server || '-'}</td>
                      <td className="log-source log-col-secondary">
                        {entry.operator || entry.ip || '-'}
                        {entry.operator && entry.ip ? ` · ${entry.ip}` : ''}
                      </td>
                      <td className="log-detail" title={detail || undefined}>
                        {detail || '-'}
                      </td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </div>
        <footer className="log-pagination">
          <span className="log-pagination-meta">
            {total > 0 ? `第 ${offset + 1}-${Math.min(offset + pageSize, total)} 条` : ''}
          </span>
          <div className="log-pagination-buttons">
            <Button
              variant="outline"
              size="sm"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - pageSize))}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={offset + pageSize >= total}
              onClick={() => setOffset(offset + pageSize)}
            >
              下一页
            </Button>
          </div>
        </footer>
      </section>

      <Dialog open={detailEntry !== null} onOpenChange={(open) => !open && setDetailEntry(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{detailEntry?.action ?? ''}</DialogTitle>
            <DialogDescription>
              {detailEntry ? formatDateTime(detailEntry.timestamp, timezone) : ''}
              {detailEntry?.request_id ? ` · 请求 ${detailEntry.request_id}` : ''}
            </DialogDescription>
          </DialogHeader>
          {detailEntry?.detail ? (
            <div className="cg-terminal log-terminal">
              <div className="log-terminal-head">
                <span className="log-terminal-label">DETAIL / JSON</span>
                {detailEntry.request_id ? (
                  <span className="log-terminal-label is-muted">REQ {detailEntry.request_id}</span>
                ) : null}
              </div>
              <pre className="log-terminal-body">{prettyDetail(detailEntry.detail)}</pre>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
