import { useCallback, useEffect, useState } from 'react'
import { RefreshCwIcon, Trash2Icon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
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
import { formatDateTime } from '@/lib/format'
import {
  REFRESH_OPTIONS,
  useStoredNumber,
  type RefreshSeconds,
} from '@/lib/log-preferences'
import { useTimezone } from '@/lib/timezone'
import type {
  LogSeverity,
  OperationCategory,
  OperationLogEntry,
  Server,
} from '@/lib/types'

const PAGE_SIZE = 10
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
  const variant = severity === 'error' ? 'destructive' : severity === 'warning' ? 'secondary' : 'outline'
  return <Badge variant={variant}>{label}</Badge>
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
        limit: PAGE_SIZE,
        offset,
      })
      .then((page) => {
        setItems(page.items)
        setTotal(page.total)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [category, filters, offset, serverId, severity])

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
    if (!window.confirm('确定清空操作日志？清空操作本身会作为新的第一条记录保留。')) return
    try {
      await api.clearOperationLogs()
      setOffset(0)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm text-muted-foreground">共 {total} 条</span>
        <div className="flex items-center gap-2">
          <Label htmlFor="operation-refresh" className="text-xs">刷新</Label>
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

      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <Label className="text-xs">程度</Label>
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
        <div className="flex flex-col gap-1">
          <Label className="text-xs">类别</Label>
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
        <div className="flex flex-col gap-1">
          <Label className="text-xs">服务器</Label>
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
        <div className="flex flex-col gap-1">
          <Label htmlFor="operation-operator" className="text-xs">操作员</Label>
          <Input id="operation-operator" className="w-32" value={operator} onChange={(event) => setOperator(event.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="operation-query" className="text-xs">关键字</Label>
          <Input id="operation-query" className="w-48" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="动作 / 详情 / 请求 ID" />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="operation-from" className="text-xs">开始时间</Label>
          <Input id="operation-from" type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="operation-to" className="text-xs">结束时间</Label>
          <Input id="operation-to" type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
        </div>
        <Button variant="outline" size="sm" onClick={applyFilters}>筛选</Button>
        <Button variant="ghost" size="sm" onClick={resetFilters}>重置</Button>
      </div>

      <div className="rounded-lg border">
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
                  <TableCell><Badge variant="outline">{categoryLabel}</Badge></TableCell>
                  <TableCell className="font-medium">{entry.action}</TableCell>
                  <TableCell>{entry.server || '-'}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{entry.operator || entry.ip || '-'}{entry.operator && entry.ip ? ` · ${entry.ip}` : ''}</TableCell>
                  <TableCell className="max-w-64 truncate text-xs text-muted-foreground" title={detail || undefined}>{detail || '-'}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{total > 0 ? `第 ${offset + 1}–${Math.min(offset + PAGE_SIZE, total)} 条` : ''}</span>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>上一页</Button>
          <Button variant="outline" size="sm" disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)}>下一页</Button>
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
            <pre className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs">{prettyDetail(detailEntry.detail)}</pre>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
