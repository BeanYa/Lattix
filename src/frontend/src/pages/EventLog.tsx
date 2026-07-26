import { useCallback, useEffect, useState } from 'react'

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
import { useTimezone } from '@/lib/timezone'
import type { EventCategory, EventLogEntry, Server } from '@/lib/types'

const PAGE_SIZE = 50

// 类别 → Badge 配色（与 Nodes 页状态色风格一致）。
const categoryStyle: Record<EventCategory, { label: string; className: string }> = {
  command: { label: '命令', className: 'border-blue-200 bg-blue-50 text-blue-700' },
  node: { label: '节点', className: 'border-purple-200 bg-purple-50 text-purple-700' },
  agent: { label: 'Agent', className: 'border-cyan-200 bg-cyan-50 text-cyan-700' },
  admin: { label: '操作', className: 'border-amber-200 bg-amber-50 text-amber-700' },
}

const CATEGORY_OPTIONS: { value: EventCategory; label: string }[] = [
  { value: 'command', label: '命令' },
  { value: 'node', label: '节点' },
  { value: 'agent', label: 'Agent' },
  { value: 'admin', label: '操作' },
]

// prettyDetail 尝试美化 detail JSON；失败回退原文。
function prettyDetail(detail: string): string {
  if (!detail) return ''
  try {
    return JSON.stringify(JSON.parse(detail), null, 2)
  } catch {
    return detail
  }
}

export default function EventLogPage() {
  const { timezone } = useTimezone()
  const [items, setItems] = useState<EventLogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // 过滤条件
  const [category, setCategory] = useState<EventCategory | ''>('')
  const [serverId, setServerId] = useState('')
  const [q, setQ] = useState('')
  const [offset, setOffset] = useState(0)

  // 详情弹窗
  const [detailEntry, setDetailEntry] = useState<EventLogEntry | null>(null)

  // 提交态（输入框即时变，但查询按"筛选"按钮触发，避免每键一查）。
  const [qInput, setQInput] = useState('')

  const load = useCallback(() => {
    const sid = serverId ? Number(serverId) : undefined
    api
      .eventLog({
        category: category || undefined,
        server_id: sid,
        q: q || undefined,
        limit: PAGE_SIZE,
        offset,
      })
      .then((page) => {
        setItems(page.items)
        setTotal(page.total)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [category, serverId, q, offset])

  useEffect(() => {
    api.servers().then(setServers).catch(() => {})
  }, [])

  useEffect(() => {
    setLoading(true)
    load()
    // 日志页 30s 轮询刷新（同 Dashboard 范式）；过滤变更时立即重查。
    const timer = setInterval(load, 30000)
    return () => clearInterval(timer)
  }, [load])

  const onApplyFilters = () => {
    setQ(qInput.trim())
    setOffset(0)
  }

  const onResetFilters = () => {
    setCategory('')
    setServerId('')
    setQ('')
    setQInput('')
    setOffset(0)
  }

  const onPrev = () => setOffset(Math.max(0, offset - PAGE_SIZE))
  const onNext = () => setOffset(offset + PAGE_SIZE)

  const pageEnd = Math.min(offset + PAGE_SIZE, total)

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">日志</h1>
        <span className="text-sm text-muted-foreground">共 {total} 条</span>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {/* 过滤条 */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <Label className="text-xs">类别</Label>
          <Select
            value={category}
            onValueChange={(v) => {
              setCategory((v as EventCategory) ?? '')
              setOffset(0)
            }}
            items={
              category
                ? [{ value: category, label: categoryStyle[category].label }]
                : []
            }
          >
            <SelectTrigger className="w-32">
              <SelectValue placeholder="全部" />
            </SelectTrigger>
            <SelectContent>
              {CATEGORY_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1">
          <Label className="text-xs">服务器</Label>
          <Select
            value={serverId}
            onValueChange={(v) => {
              setServerId(v ? String(v) : '')
              setOffset(0)
            }}
            items={
              serverId
                ? [
                    {
                      value: serverId,
                      label: servers.find((s) => String(s.id) === serverId)?.alias ?? `#${serverId}`,
                    },
                  ]
                : []
            }
          >
            <SelectTrigger className="w-40">
              <SelectValue placeholder="全部" />
            </SelectTrigger>
            <SelectContent>
              {servers.map((s) => (
                <SelectItem key={s.id} value={String(s.id)}>
                  {s.alias}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1">
          <Label className="text-xs">关键字</Label>
          <Input
            className="w-56"
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onApplyFilters()
            }}
            placeholder="动作/详情"
          />
        </div>

        <Button variant="outline" size="sm" onClick={onApplyFilters}>
          筛选
        </Button>
        <Button variant="ghost" size="sm" onClick={onResetFilters}>
          重置
        </Button>
      </div>

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-44">时间</TableHead>
              <TableHead className="w-20">类别</TableHead>
              <TableHead>动作</TableHead>
              <TableHead className="w-40">服务器</TableHead>
              <TableHead className="w-40">来源</TableHead>
              <TableHead className="w-64">详情</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  暂无记录
                </TableCell>
              </TableRow>
            ) : (
              items.map((e) => {
                const cs = categoryStyle[e.category] ?? {
                  label: e.category,
                  className: 'border-gray-200 bg-gray-50 text-gray-600',
                }
                const detail = prettyDetail(e.detail)
                return (
                  <TableRow
                    key={e.id}
                    className={detail ? 'cursor-pointer' : ''}
                    onClick={() => detail && setDetailEntry(e)}
                  >
                    <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                      {formatDateTime(e.ts, timezone)}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={cs.className}>
                        {cs.label}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-medium">{e.action}</TableCell>
                    <TableCell className="text-sm">{e.server || '-'}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {e.operator || e.ip || '-'}
                      {e.operator && e.ip ? ` · ${e.ip}` : ''}
                    </TableCell>
                    <TableCell
                      className="max-w-64 truncate text-xs text-muted-foreground"
                      title={detail || undefined}
                    >
                      {detail || '-'}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      {/* 分页 */}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>
          {total > 0 ? `第 ${offset + 1}–${pageEnd} 条` : ''}
        </span>
        <div className="space-x-2">
          <Button variant="outline" size="sm" disabled={offset === 0} onClick={onPrev}>
            上一页
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={offset + PAGE_SIZE >= total}
            onClick={onNext}
          >
            下一页
          </Button>
        </div>
      </div>

      {/* 详情弹窗 */}
      <Dialog open={!!detailEntry} onOpenChange={(o) => !o && setDetailEntry(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{detailEntry?.action}</DialogTitle>
            <DialogDescription>
              {detailEntry && formatDateTime(detailEntry.ts, timezone)}
              {detailEntry?.server ? ` · ${detailEntry.server}` : ''}
              {detailEntry?.operator ? ` · ${detailEntry.operator}` : ''}
              {detailEntry?.ip ? ` · ${detailEntry.ip}` : ''}
            </DialogDescription>
          </DialogHeader>
          {detailEntry?.detail && (
            <pre className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs">
              {prettyDetail(detailEntry.detail)}
            </pre>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
