import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { PlusIcon } from 'lucide-react'

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
import type { CreateNodeRequest, NodeStatus, Server, XrayNode } from '@/lib/types'

const statusStyle: Record<NodeStatus, { label: string; className: string }> = {
  active: { label: '已生效', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '应用中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '失败', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '待处理', className: 'border-gray-200 bg-gray-50 text-gray-500' },
}

function nodePort(node: XrayNode): string | number {
  return node.realized_config?.port ?? node.port ?? '自动'
}

export default function Nodes() {
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [serverId, setServerId] = useState('')
  const [port, setPort] = useState('')
  const [shortId, setShortId] = useState('')
  const [dest, setDest] = useState('www.microsoft.com:443')
  const [serverNames, setServerNames] = useState('www.microsoft.com')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')

  const load = useCallback(() => {
    Promise.all([api.nodes(), api.servers()])
      .then(([n, s]) => {
        setNodes(n)
        setServers(s)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(load, [load])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setServerId('')
      setPort('')
      setShortId('')
      setDest('www.microsoft.com:443')
      setServerNames('www.microsoft.com')
      setCreateError('')
    }
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    const server = servers.find((s) => String(s.id) === serverId)
    if (!server) {
      setCreateError('请选择服务器')
      return
    }
    const body: CreateNodeRequest = { server_id: server.id }
    if (port.trim()) {
      body.port = Number(port)
    }
    if (shortId.trim()) {
      body.short_id = shortId.trim()
    }
    if (dest.trim()) {
      body.dest = dest.trim()
    }
    const names = serverNames
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    if (names.length > 0) {
      body.server_names = names
    }
    setCreateError('')
    setCreating(true)
    try {
      await api.createNode(body)
      onOpenChange(false)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onRetry = async (id: number) => {
    setRetrying(id)
    try {
      await api.retryNode(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRetrying(null)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">节点</h1>
        <Button onClick={() => setOpen(true)}>
          <PlusIcon />
          创建节点
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>服务器</TableHead>
              <TableHead>协议</TableHead>
              <TableHead>端口</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>错误信息</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : nodes.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  暂无节点
                </TableCell>
              </TableRow>
            ) : (
              nodes.map((n) => {
                const st = statusStyle[n.status] ?? statusStyle.pending
                return (
                  <TableRow key={n.id}>
                    <TableCell className="font-medium">{n.server_alias}</TableCell>
                    <TableCell>{n.protocol}</TableCell>
                    <TableCell>{nodePort(n)}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={st.className}>
                        {st.label}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-64 truncate text-destructive" title={n.error ?? ''}>
                      {n.error || '-'}
                    </TableCell>
                    <TableCell>
                      {n.status === 'failed' && (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={retrying === n.id}
                          onClick={() => onRetry(n.id)}
                        >
                          {retrying === n.id ? '重试中…' : '重试'}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>创建节点</DialogTitle>
            <DialogDescription>在指定服务器上创建一个 VLESS Reality 节点。</DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label>服务器</Label>
              <Select value={serverId} onValueChange={(v) => setServerId(String(v))}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择服务器" />
                </SelectTrigger>
                <SelectContent>
                  {servers.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {s.alias}
                      {s.online ? '' : '（离线）'}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="port">端口</Label>
              <Input
                id="port"
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="留空自动分配"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="shortId">short_id</Label>
              <Input
                id="shortId"
                value={shortId}
                onChange={(e) => setShortId(e.target.value)}
                placeholder="留空随机生成"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="dest">dest</Label>
              <Input
                id="dest"
                value={dest}
                onChange={(e) => setDest(e.target.value)}
                placeholder="www.microsoft.com:443"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="serverNames">serverNames（逗号分隔）</Label>
              <Input
                id="serverNames"
                value={serverNames}
                onChange={(e) => setServerNames(e.target.value)}
                placeholder="www.microsoft.com"
              />
            </div>
            {createError && <p className="text-sm text-destructive">{createError}</p>}
            <DialogFooter>
              <Button type="submit" disabled={creating || !serverId}>
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
