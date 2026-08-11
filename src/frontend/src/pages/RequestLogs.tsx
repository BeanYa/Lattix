import { useCallback, useEffect, useMemo, useState } from 'react'
import { RefreshCwIcon, Trash2Icon } from 'lucide-react'

import { Notice } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
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
  REFRESH_OPTIONS,
  REQUEST_WINDOW_OPTIONS,
  useStoredNumber,
  type RefreshSeconds,
  type RequestWindow,
} from '@/lib/log-preferences'
import { useTimezone } from '@/lib/timezone'
import type { LogSeverity, RequestLogEntry, RequestLogStatus } from '@/lib/types'
import { cn } from '@/lib/utils'

import './logs.css'

const REFRESH_VALUES = REFRESH_OPTIONS.map((option) => option.value)
const METHODS = ['GET', 'POST', 'WS']

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function severityTone(severity: LogSeverity) {
  if (severity === 'error') return 'is-red'
  if (severity === 'info') return 'is-blue'
  return 'is-muted'
}

export default function RequestLogs() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const [items, setItems] = useState<RequestLogEntry[]>([])
  const [status, setStatus] = useState<RequestLogStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [severity, setSeverity] = useState<LogSeverity | ''>('')
  const [method, setMethod] = useState('')
  const [query, setQuery] = useState('')
  const [refreshSeconds, setRefreshSeconds] = useStoredNumber<RefreshSeconds>(
    'lattix.logs.requests.refresh.v1',
    0,
    REFRESH_VALUES,
  )
  const [windowSize, setWindowSize] = useStoredNumber<RequestWindow>(
    'lattix.logs.requests.window.v1',
    30,
    REQUEST_WINDOW_OPTIONS,
  )

  const load = useCallback(() => {
    setError('')
    return api
      .requestLogs(windowSize)
      .then((page) => {
        setItems(page.items)
        setStatus(page.status)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [windowSize])

  useEffect(() => {
    setLoading(true)
    void load()
    if (refreshSeconds === 0) return
    const timer = window.setInterval(load, refreshSeconds * 1000)
    return () => window.clearInterval(timer)
  }, [load, refreshSeconds])

  const visibleItems = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((entry) => {
      if (severity && entry.severity !== severity) return false
      const displayMethod = entry.transport === 'websocket' ? 'WS' : entry.method
      if (method && displayMethod !== method) return false
      if (!needle) return true
      return [
        entry.path,
        entry.route,
        entry.rpc_type,
        entry.rpc_code,
        entry.operator,
        entry.ip,
        entry.request_id,
        entry.trace_id,
        entry.error_summary,
        JSON.stringify(entry.attributes ?? {}),
      ].some((value) => value?.toLowerCase().includes(needle))
    })
  }, [items, method, query, severity])

  const clearLogs = async () => {
    if (!(await confirm({
      title: '清空请求日志',
      description: '确定清空请求日志？本次清空请求会成为新的第一条请求记录。',
      confirmLabel: '清空日志',
      destructive: true,
    }))) return
    try {
      await api.clearRequestLogs()
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="cg-logs">
      <div className="cg-logs-toolbar">
        <div className="cg-logs-toolbar-group">
          <span className="cg-pill">显示最新 {items.length} / {windowSize} 行</span>
          {status ? <span className="cg-pill">{formatBytes(status.usage_bytes)} / {formatBytes(status.max_bytes)}</span> : null}
          {status?.dropped ? <span className="cg-status is-red">丢弃 {status.dropped}</span> : null}
        </div>
        <div className="cg-logs-toolbar-group">
          <Label className="cg-log-label">窗口</Label>
          <Select
            value={String(windowSize)}
            onValueChange={(value) => setWindowSize(Number(value) as RequestWindow)}
            items={REQUEST_WINDOW_OPTIONS.map((value) => ({ value: String(value), label: `${value} 行` }))}
          >
            <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup>
              {REQUEST_WINDOW_OPTIONS.map((value) => <SelectItem key={value} value={String(value)}>{value} 行</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
          <Label className="cg-log-label">刷新</Label>
          <Select
            value={String(refreshSeconds)}
            onValueChange={(value) => setRefreshSeconds(Number(value) as RefreshSeconds)}
            items={REFRESH_OPTIONS.map((option) => ({ ...option, value: String(option.value) }))}
          >
            <SelectTrigger className="w-28"><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup>
              {REFRESH_OPTIONS.map((option) => <SelectItem key={option.value} value={String(option.value)}>{option.label}</SelectItem>)}
            </SelectGroup></SelectContent>
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
          <Select value={severity || null} onValueChange={(value) => setSeverity((value as LogSeverity) ?? '')}>
            <SelectTrigger className="w-28"><SelectValue placeholder="全部" /></SelectTrigger>
            <SelectContent><SelectGroup>
              <SelectItem value="debug">调试</SelectItem>
              <SelectItem value="info">信息</SelectItem>
              <SelectItem value="warning">警告</SelectItem>
              <SelectItem value="error">错误</SelectItem>
            </SelectGroup></SelectContent>
          </Select>
        </div>
        <div className="cg-log-filter-item">
          <Label className="cg-log-label">方法</Label>
          <Select value={method || null} onValueChange={(value) => setMethod(value ? String(value) : '')}>
            <SelectTrigger className="w-28"><SelectValue placeholder="全部" /></SelectTrigger>
            <SelectContent><SelectGroup>
              {METHODS.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
        </div>
        <div className="cg-log-filter-item">
          <Label htmlFor="request-query" className="cg-log-label">当前窗口过滤</Label>
          <Input id="request-query" className="w-64" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="路径 / 参数 / IP / 请求 ID" />
        </div>
      </div>

      <Table>
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              <TableHead>程度</TableHead>
              <TableHead>方法</TableHead>
              <TableHead>结果</TableHead>
              <TableHead>路径 / 动作</TableHead>
              <TableHead>耗时</TableHead>
              <TableHead>来源</TableHead>
              <TableHead>参数 / 错误</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow><TableCell colSpan={8} className="text-center text-muted-foreground">加载中…</TableCell></TableRow>
            ) : visibleItems.length === 0 ? (
              <TableRow><TableCell colSpan={8} className="text-center text-muted-foreground">当前窗口暂无匹配记录</TableCell></TableRow>
            ) : visibleItems.map((entry) => (
              <TableRow key={entry.request_id}>
                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(entry.timestamp, timezone)}</TableCell>
                <TableCell><span className={cn('cg-status', severityTone(entry.severity))}>{entry.severity}</span></TableCell>
                <TableCell className="font-mono text-xs">{entry.transport === 'websocket' ? 'WS' : entry.method}</TableCell>
                <TableCell>
                  <span className={cn('cg-status', entry.severity === 'error' ? 'is-red' : entry.severity === 'warning' ? 'is-muted' : 'is-lime')}>
                    {entry.rpc_code || entry.http_status || '-'}
                  </span>
                </TableCell>
                <TableCell>
                  <div className="flex max-w-80 flex-col gap-1">
                    <span className="truncate text-sm" title={entry.path || entry.rpc_type}>
                      {entry.path || entry.rpc_type}
                    </span>
                    <span className="truncate font-mono text-xs text-muted-foreground" title={entry.route || entry.trace_id}>
                      {entry.route || `trace ${entry.trace_id}`}
                    </span>
                  </div>
                </TableCell>
                <TableCell className="whitespace-nowrap text-xs">{entry.duration_ms} ms</TableCell>
                <TableCell className="text-xs text-muted-foreground">{entry.operator || entry.ip || '-'}{entry.operator && entry.ip ? ` · ${entry.ip}` : ''}</TableCell>
                <TableCell className="max-w-72 truncate font-mono text-xs text-muted-foreground" title={entry.error_summary || JSON.stringify(entry.attributes ?? {})}>
                  {entry.error_summary || (entry.attributes ? JSON.stringify(entry.attributes) : '-')}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      <p className="cg-log-note">筛选仅作用于当前显示窗口，不扫描全部请求日志文件。</p>
    </div>
  )
}
