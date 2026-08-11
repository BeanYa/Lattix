import { useCallback, useEffect, useState } from 'react'
import { RefreshCwIcon, Trash2Icon } from 'lucide-react'

import { Notice, Surface } from '@/components/PagePrimitives'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
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
import { useTimezone } from '@/lib/timezone'
import type {
  LogSeverity,
  OperationCategory,
  OperationLogEntry,
  Server,
} from '@/lib/types'
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

function severityBadge(severity: LogSeverity) {
  const label = SEVERITY_OPTIONS.find((option) => option.value === severity)?.label ?? severity
  const tone = severity === 'error' ? 'is-red' : 'is-muted'
  return <span className={cn('cg-status', tone)}>{label}</span>
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
  const [items, setItems] = useState<OperationLogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [detailEntry, setDetailEntry] = useState<OperationLogEntry | null>(null)
  const [offset, setOffset] = useState(0)
  const [severity, setSeverity] = useState<LogSeverity | ''>('')
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
    api.servers().then(setServers).catch(() => {})
  }, [])

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
    if (!(await confirm({
      title: '清空操作日志',
      description: '确定清空操作日志？清空操作本身会作为新的第一条记录保留。',
      confirmLabel: '清空日志',
      destructive: true,
    }))) return
    try {
      await api.clearOperationLogs()
      setOffset(0)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="cg-logs">
      <div className="cg-logs-toolbar">
        <span className="cg-pill">共 {total} 条</span>
        <div className="cg-logs-toolbar-group">
          <Label htmlFor="operation-page-size" className="cg-log-label">每页</Label>
          <Select
            value={String(pageSize)}
            onValueChange={(value) => {
              setPageSize(Number(value) as OperationPageSize)
              setOffset(0)
            }}
            items={OPERATION_PAGE_SIZE_OPTIONS.map((value) => ({ value: String(value), label: `${value} 条` }))}
          >
            <SelectTrigger id="operation-page-size" className="w-24">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {OPERATION_PAGE_SIZE_OPTIONS.map((value) => (
                  <SelectItem key={value} value={String(value)}>{value} 条</SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Label htmlFor="operation-refresh" className="cg-log-label">刷新</Label>
          <Select
            value={String(refreshSeconds)}
            onValueChange={(value) => setRefreshSeconds(Number(value) as RefreshSeconds)}
            items={REFRESH_OPTIONS.map((option) => ({ ...option, value: String(option.value) }))}
          >
            <SelectTrigger id="operation-refresh" className="w-28">
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
      </div>

      {error ? <Notice tone="danger">{error}</Notice> : null}

      <div className="cg-card cg-log-filters">
        <div className="cg-log-filter-item">
          <Label className="cg-log-label">程度</Label>
          <Select
            value={severity || null}
            onValueChange={(value) => {
              setSeverity((value as LogSeverity) ?? '')
              setOffset(0)
            }}
            items={SEVERITY_OPTIONS}
          >
            <SelectTrigger className="w-28"><SelectValue placeholder="全部" /></SelectTrigger>
            <SelectContent><SelectGroup>
              {SEVERITY_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
              ))}
            </SelectGroup></SelectContent>
          </Select>
        </div>
        <div className="cg-log-filter-item">
          <Label className="cg-log-label">类别</Label>
          <Select
            value={category || null}
            onValueChange={(value) => {
              setCategory((value as OperationCategory) ?? '')
              setOffset(0)
            }}
            items={CATEGORY_OPTIONS}
          >
            <SelectTrigger className="w-32"><SelectValue placeholder="全部" /></SelectTrigger>
            <SelectContent><SelectGroup>
              {CATEGORY_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
              ))}
            </SelectGroup></SelectContent>
          </Select>
        </div>
        <div className="cg-log-filter-item">
          <Label className="cg-log-label">服务器</Label>
          <Select
            value={serverId || null}
            onValueChange={(value) => {
              setServerId(value ? String(value) : '')
              setOffset(0)
            }}
            items={servers.map((server) => ({ value: String(server.id), label: server.alias }))}
          >
            <SelectTrigger className="w-40"><SelectValue placeholder="全部" /></SelectTrigger>
            <SelectContent><SelectGroup>
              {servers.map((server) => (
                <SelectItem key={server.id} value={String(server.id)}>{server.alias}</SelectItem>
              ))}
            </SelectGroup></SelectContent>
          </Select>
        </div>
        <div className="cg-log-filter-item">
          <Label htmlFor="operation-operator" className="cg-log-label">操作员</Label>
          <Input id="operation-operator" className="w-32" value={operator} onChange={(event) => setOperator(event.target.value)} />
        </div>
        <div className="cg-log-filter-item">
          <Label htmlFor="operation-query" className="cg-log-label">关键字</Label>
          <Input id="operation-query" className="w-48" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="动作 / 详情 / 请求 ID" />
        </div>
        <div className="cg-log-filter-item">
          <Label htmlFor="operation-from" className="cg-log-label">开始时间</Label>
          <Input id="operation-from" type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
        </div>
        <div className="cg-log-filter-item">
          <Label htmlFor="operation-to" className="cg-log-label">结束时间</Label>
          <Input id="operation-to" type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
        </div>
        <Button variant="outline" size="sm" onClick={applyFilters}>筛选</Button>
        <Button variant="ghost" size="sm" onClick={resetFilters}>重置</Button>
      </div>

      <Surface>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              <TableHead>程度</TableHead>
              <TableHead>类别</TableHead>
              <TableHead>动作</TableHead>
              <TableHead>服务器</TableHead>
              <TableHead>来源</TableHead>
              <TableHead>详情</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow><TableCell colSpan={7} className="text-center text-muted-foreground">加载中…</TableCell></TableRow>
            ) : items.length === 0 ? (
              <TableRow><TableCell colSpan={7} className="text-center text-muted-foreground">暂无记录</TableCell></TableRow>
            ) : items.map((entry) => {
              const detail = prettyDetail(entry.detail)
              const categoryLabel = CATEGORY_OPTIONS.find((option) => option.value === entry.category)?.label ?? entry.category
              return (
                <TableRow key={entry.event_id} className={detail ? 'cursor-pointer' : ''} onClick={() => detail && setDetailEntry(entry)}>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(entry.timestamp, timezone)}</TableCell>
                  <TableCell>{severityBadge(entry.severity)}</TableCell>
                  <TableCell><span className="cg-status is-blue">{categoryLabel}</span></TableCell>
                  <TableCell className="font-medium">{entry.action}</TableCell>
                  <TableCell>{entry.server || '-'}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{entry.operator || entry.ip || '-'}{entry.operator && entry.ip ? ` · ${entry.ip}` : ''}</TableCell>
                  <TableCell className="max-w-64 truncate text-xs text-muted-foreground" title={detail || undefined}>{detail || '-'}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </Surface>

      <div className="cg-logs-pagination">
        <span className="cg-log-label">{total > 0 ? `第 ${offset + 1}-${Math.min(offset + pageSize, total)} 条` : ''}</span>
        <div className="cg-logs-pagination-buttons">
          <Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - pageSize))}>上一页</Button>
          <Button variant="outline" size="sm" disabled={offset + pageSize >= total} onClick={() => setOffset(offset + pageSize)}>下一页</Button>
        </div>
      </div>

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
            <div className="cg-terminal cg-log-terminal">
              <div className="cg-log-terminal-head">
                <span className="cg-micro">DETAIL / JSON</span>
                {detailEntry.request_id ? <span className="cg-micro">REQ {detailEntry.request_id}</span> : null}
              </div>
              <pre className="cg-log-terminal-body">{prettyDetail(detailEntry.detail)}</pre>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
