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
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatBytes, formatDateTime } from '@/lib/format'
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

const SEVERITY_TONE: Record<LogSeverity, 'is-red' | 'is-warn' | 'is-muted'> = {
  debug: 'is-muted',
  info: 'is-muted',
  warning: 'is-warn',
  error: 'is-red',
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
    if (
      !(await confirm({
        title: '清空请求日志',
        description: '确定清空请求日志？本次清空请求会成为新的第一条请求记录。',
        confirmLabel: '清空日志',
        destructive: true,
      }))
    )
      return
    try {
      await api.clearRequestLogs()
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
          <span className="log-mod-meta">筛选仅作用于当前窗口</span>
        </header>
        <div className="log-fields">
          <div className="log-field">
            <Label className="log-field-label">程度</Label>
            <Select
              value={severity || null}
              onValueChange={(value) => setSeverity((value as LogSeverity) ?? '')}
            >
              <SelectTrigger className="w-28">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="debug">调试</SelectItem>
                  <SelectItem value="info">信息</SelectItem>
                  <SelectItem value="warning">警告</SelectItem>
                  <SelectItem value="error">错误</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className="log-field">
            <Label className="log-field-label">方法</Label>
            <Select
              value={method || null}
              onValueChange={(value) => setMethod(value ? String(value) : '')}
            >
              <SelectTrigger className="w-28">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {METHODS.map((value) => (
                    <SelectItem key={value} value={value}>
                      {value}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className="log-field log-field-wide">
            <Label htmlFor="request-query" className="log-field-label">
              当前窗口过滤
            </Label>
            <Input
              id="request-query"
              className="w-64"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="路径 / 参数 / IP / 请求 ID"
            />
          </div>
        </div>
      </section>

      {error ? <Notice tone="danger">{error}</Notice> : null}

      {/* 请求日志列表 */}
      <section className="log-mod" aria-label="请求日志列表">
        <header className="log-mod-head">
          <span className="log-tab">LOG</span>
          <h2 className="log-mod-title">请求日志</h2>
          <span className="log-mod-meta">
            显示最新 {items.length} / {windowSize} 行
            {status
              ? ` · ${formatBytes(status.usage_bytes)} / ${formatBytes(status.max_bytes)}`
              : ''}
          </span>
          {status?.dropped ? <span className="cg-status is-red">丢弃 {status.dropped}</span> : null}
          <div className="log-head-actions">
            <Label htmlFor="request-window" className="log-head-label">
              窗口
            </Label>
            <Select
              value={String(windowSize)}
              onValueChange={(value) => setWindowSize(Number(value) as RequestWindow)}
              items={REQUEST_WINDOW_OPTIONS.map((value) => ({
                value: String(value),
                label: `${value} 行`,
              }))}
            >
              <SelectTrigger id="request-window" className="w-24" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {REQUEST_WINDOW_OPTIONS.map((value) => (
                    <SelectItem key={value} value={String(value)}>
                      {value} 行
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Label htmlFor="request-refresh" className="log-head-label">
              刷新
            </Label>
            <Select
              value={String(refreshSeconds)}
              onValueChange={(value) => setRefreshSeconds(Number(value) as RefreshSeconds)}
              items={REFRESH_OPTIONS.map((option) => ({ ...option, value: String(option.value) }))}
            >
              <SelectTrigger id="request-refresh" className="w-28" size="sm">
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
          <table className="log-table log-table-requests">
            <thead>
              <tr>
                <th scope="col">时间</th>
                <th scope="col">程度</th>
                <th scope="col">方法</th>
                <th scope="col">结果</th>
                <th scope="col">路径 / 动作</th>
                <th scope="col" className="log-col-secondary">
                  耗时
                </th>
                <th scope="col" className="log-col-secondary">
                  来源
                </th>
                <th scope="col" className="log-col-secondary">
                  参数 / 错误
                </th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={8} className="log-table-empty">
                    加载中…
                  </td>
                </tr>
              ) : visibleItems.length === 0 ? (
                <tr>
                  <td colSpan={8} className="log-table-empty">
                    当前窗口暂无匹配记录
                  </td>
                </tr>
              ) : (
                visibleItems.map((entry) => {
                  const resultTone =
                    entry.severity === 'error'
                      ? 'is-red'
                      : entry.severity === 'warning'
                        ? 'is-warn'
                        : ''
                  return (
                    <tr key={entry.request_id}>
                      <td className="log-time">{formatDateTime(entry.timestamp, timezone)}</td>
                      <td>
                        <span
                          className={cn('cg-status', SEVERITY_TONE[entry.severity] ?? 'is-muted')}
                        >
                          {entry.severity}
                        </span>
                      </td>
                      <td className="log-method">
                        {entry.transport === 'websocket' ? 'WS' : entry.method}
                      </td>
                      <td>
                        <span className={cn('cg-status', resultTone)}>
                          {entry.rpc_code || entry.http_status || '-'}
                        </span>
                      </td>
                      <td className="log-path">
                        <span className="log-path-main" title={entry.path || entry.rpc_type}>
                          {entry.path || entry.rpc_type}
                        </span>
                        <span className="log-path-sub" title={entry.route || entry.trace_id}>
                          {entry.route || `trace ${entry.trace_id}`}
                        </span>
                      </td>
                      <td className="log-duration log-col-secondary">{entry.duration_ms} ms</td>
                      <td className="log-source log-col-secondary">
                        {entry.operator || entry.ip || '-'}
                        {entry.operator && entry.ip ? ` · ${entry.ip}` : ''}
                      </td>
                      <td
                        className="log-detail log-col-secondary"
                        title={entry.error_summary || JSON.stringify(entry.attributes ?? {})}
                      >
                        {entry.error_summary ||
                          (entry.attributes ? JSON.stringify(entry.attributes) : '-')}
                      </td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </div>
        <p className="log-note">筛选仅作用于当前显示窗口，不扫描全部请求日志文件。</p>
      </section>
    </div>
  )
}
