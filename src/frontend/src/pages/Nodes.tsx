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
import { humanizeBytes } from '@/lib/format'
import type { CreateNodeRequest, NodeStatus, Server, XrayNode } from '@/lib/types'

const statusStyle: Record<NodeStatus, { label: string; className: string }> = {
  active: { label: '已生效', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '应用中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '失败', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '待处理', className: 'border-gray-200 bg-gray-50 text-gray-500' },
}

// 与后端 shared 包保持一致的协议/选项常量。
// xray 已将 vmess/trojan/shadowsocks 及 gRPC 传输标记为 deprecated（推荐迁移
// VLESS + XHTTP / VLESS Encryption），向导不再提供，存量节点不受影响（§15）。
const PROTOCOLS = ['vless', 'socks', 'http', 'dokodemo-door'] as const
const DEPRECATED_PROTOCOLS = ['vmess', 'trojan', 'shadowsocks']
const REALITY_PROTOCOLS = ['vless']
const NETWORKS = ['tcp', 'xhttp']
const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'edge', 'ios', 'android', '360', 'qq', 'random', 'randomized']
const FLOWS = ['xtls-rprx-vision', 'none']
const VLESS_ENCS = [
  { value: 'none', label: '无' },
  { value: 'mlkem768', label: 'mlkem768（后量子，推荐）' },
  { value: 'x25519', label: 'x25519' },
]
const XHTTP_MODES = ['auto', 'packet-up', 'stream-up']

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
  const [protocol, setProtocol] = useState('vless')
  const [port, setPort] = useState('')
  const [shortId, setShortId] = useState('')
  const [dest, setDest] = useState('dl.google.com:443')
  const [serverNames, setServerNames] = useState('dl.google.com')
  const [fingerprint, setFingerprint] = useState('chrome')
  const [network, setNetwork] = useState('tcp')
  const [path, setPath] = useState('/')
  const [mode, setMode] = useState('auto')
  const [host, setHost] = useState('')
  const [flow, setFlow] = useState('xtls-rprx-vision')
  const [encryption, setEncryption] = useState('none')
  const [targetAddress, setTargetAddress] = useState('')
  const [targetPort, setTargetPort] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')

  const isReality = REALITY_PROTOCOLS.includes(protocol)

  const load = useCallback(() => {
    Promise.all([api.nodes(), api.servers()])
      .then(([n, s]) => {
        setNodes(n)
        setServers(s)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setServerId('')
      setProtocol('vless')
      setPort('')
      setShortId('')
      setDest('dl.google.com:443')
      setServerNames('dl.google.com')
      setFingerprint('chrome')
      setNetwork('tcp')
      setPath('/')
      setMode('auto')
      setHost('')
      setFlow('xtls-rprx-vision')
      setEncryption('none')
      setTargetAddress('')
      setTargetPort('')
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
    const body: CreateNodeRequest = { server_id: server.id, protocol }
    if (port.trim()) {
      body.port = Number(port)
    }
    if (isReality) {
      body.fingerprint = fingerprint
      body.network = network
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
      if (network === 'xhttp') {
        body.path = path.trim() || '/'
        body.mode = mode
        if (host.trim()) {
          body.host = host.trim()
        }
      }
      if (protocol === 'vless') {
        // vision 仅 tcp；xhttp 必须无 flow；vision + Encryption 允许组合（§15）
        body.flow = network === 'tcp' ? flow : 'none'
        if (encryption !== 'none') {
          body.encryption = encryption
        }
      }
    }
    if (protocol === 'dokodemo-door') {
      if (!targetAddress.trim() || !targetPort.trim()) {
        setCreateError('dokodemo-door 需要目标地址与目标端口')
        return
      }
      body.target_address = targetAddress.trim()
      body.target_port = Number(targetPort)
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

  const onDelete = async (id: number) => {
    if (!window.confirm('确定删除该节点？将同时从服务器上移除该 inbound。')) {
      return
    }
    try {
      await api.deleteNode(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
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
              <TableHead>流量</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>错误信息</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : nodes.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  暂无节点
                </TableCell>
              </TableRow>
            ) : (
              nodes.map((n) => {
                const st = statusStyle[n.status] ?? statusStyle.pending
                return (
                  <TableRow key={n.id}>
                    <TableCell className="font-medium">{n.server_alias}</TableCell>
                    <TableCell>
                      {n.protocol}
                      {DEPRECATED_PROTOCOLS.includes(n.protocol) && (
                        <Badge variant="outline" className="ml-1 border-gray-200 bg-gray-50 text-gray-500">
                          已废弃
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>{nodePort(n)}</TableCell>
                    <TableCell className="text-xs whitespace-nowrap">
                      {n.traffic
                        ? `↑${humanizeBytes(n.traffic.up)} ↓${humanizeBytes(n.traffic.down)}`
                        : '-'}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={st.className}>
                        {st.label}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-64 truncate text-destructive" title={n.error ?? ''}>
                      {n.error || '-'}
                    </TableCell>
                    <TableCell className="space-x-2">
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
                      <Button variant="outline" size="sm" onClick={() => onDelete(n.id)}>
                        删除
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>创建节点</DialogTitle>
            <DialogDescription>
              在指定服务器上创建节点。vless 走 Reality（tcp / XHTTP），可选 VLESS Encryption
              后量子加密；socks / http 为明文账密代理；dokodemo-door 为端口转发（不进订阅）。
              vmess / trojan / shadowsocks 及 gRPC 已被 xray 标记为 deprecated，不再提供新建。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label>服务器</Label>
              <Select
                value={serverId}
                onValueChange={(v) => setServerId(String(v))}
                // Base UI 关闭状态下 Trigger 文本由 items 解析，否则只显示原始 value（服务器 id）。
                items={servers.map((s) => ({
                  value: String(s.id),
                  label: s.online ? s.alias : `${s.alias}（离线）`,
                }))}
              >
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
              <Label>协议</Label>
              <Select value={protocol} onValueChange={(v) => v && setProtocol(v)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROTOCOLS.map((p) => (
                    <SelectItem key={p} value={p}>
                      {p}
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

            {isReality && (
              <>
                <div className="space-y-2">
                  <Label>传输（network）</Label>
                  <Select value={network} onValueChange={(v) => v && setNetwork(v)}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {NETWORKS.map((n) => (
                        <SelectItem key={n} value={n}>
                          {n}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                {network === 'xhttp' && (
                  <>
                    <div className="space-y-2">
                      <Label htmlFor="xhttpPath">XHTTP path</Label>
                      <Input
                        id="xhttpPath"
                        value={path}
                        onChange={(e) => setPath(e.target.value)}
                        placeholder="/"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label>XHTTP mode</Label>
                      <Select value={mode} onValueChange={(v) => v && setMode(v)}>
                        <SelectTrigger className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {XHTTP_MODES.map((m) => (
                            <SelectItem key={m} value={m}>
                              {m}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="xhttpHost">XHTTP host（可空）</Label>
                      <Input
                        id="xhttpHost"
                        value={host}
                        onChange={(e) => setHost(e.target.value)}
                        placeholder="留空不设置"
                      />
                    </div>
                  </>
                )}
                {protocol === 'vless' && (
                  <div className="space-y-2">
                    <Label>VLESS Encryption（可与 flow 组合）</Label>
                    <Select value={encryption} onValueChange={(v) => v !== null && setEncryption(v)} items={VLESS_ENCS}>
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {VLESS_ENCS.map((e) => (
                          <SelectItem key={e.value} value={e.value}>
                            {e.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                )}
                {protocol === 'vless' && network === 'tcp' && (
                  <div className="space-y-2">
                    <Label>flow</Label>
                    <Select
                      value={flow}
                      onValueChange={(v) => v && setFlow(v)}
                      items={FLOWS.map((f) => ({ value: f, label: f === 'none' ? '无' : f }))}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {FLOWS.map((f) => (
                          <SelectItem key={f} value={f}>
                            {f === 'none' ? '无' : f}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                )}
                <div className="space-y-2">
                  <Label>uTLS 指纹（客户端）</Label>
                  <Select value={fingerprint} onValueChange={(v) => v && setFingerprint(v)}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {FINGERPRINTS.map((f) => (
                        <SelectItem key={f} value={f}>
                          {f}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
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
                    placeholder="dl.google.com:443"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="serverNames">serverNames（逗号分隔）</Label>
                  <Input
                    id="serverNames"
                    value={serverNames}
                    onChange={(e) => setServerNames(e.target.value)}
                    placeholder="dl.google.com"
                  />
                </div>
              </>
            )}

            {protocol === 'dokodemo-door' && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="targetAddress">目标地址</Label>
                  <Input
                    id="targetAddress"
                    value={targetAddress}
                    onChange={(e) => setTargetAddress(e.target.value)}
                    placeholder="例如：10.0.0.2 或 internal.example.com"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="targetPort">目标端口</Label>
                  <Input
                    id="targetPort"
                    type="number"
                    min={1}
                    max={65535}
                    value={targetPort}
                    onChange={(e) => setTargetPort(e.target.value)}
                    placeholder="转发目的地端口"
                  />
                </div>
              </>
            )}

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
