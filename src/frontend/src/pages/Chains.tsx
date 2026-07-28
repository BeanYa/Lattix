import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { PlusIcon, RouteIcon, XIcon } from 'lucide-react'

import { NameTemplateInput } from '@/components/NameTemplateInput'
import { RealityDestPicker } from '@/components/RealityDestPicker'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { validateNameTemplate } from '@/lib/naming'
import { DEFAULT_REALITY_DEST } from '@/lib/reality'
import { useTimezone } from '@/lib/timezone'
import type {
  Chain,
  ChainHopRole,
  ChainStatus,
  CreateChainRequest,
  CreateNodeRequest,
  NodeStatus,
  Server,
  XrayNode,
} from '@/lib/types'

const chainStatusStyle: Record<ChainStatus, { label: string; className: string }> = {
  active: { label: '正常', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '部署中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '异常', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '部署中', className: 'border-gray-200 bg-gray-50 text-gray-500' },
  degraded: { label: '降级', className: 'border-amber-200 bg-amber-50 text-amber-700' },
}

const hopStatusStyle: Record<NodeStatus, { label: string; className: string }> = {
  active: { label: '正常', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '部署中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '异常', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '部署中', className: 'border-gray-200 bg-gray-50 text-gray-500' },
}

const roleLabel: Record<ChainHopRole, string> = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
}

// 与后端 shared 包保持一致的协议/选项常量（出口节点协议表单复用 Nodes 向导的 vless+reality 字段）。
const DIRECT_PROTOCOLS = ['vless', 'socks', 'http', 'dokodemo-door'] as const
const RELAY_PROTOCOLS = ['vless', 'socks', 'http'] as const
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

/** 入站能力（§21）：direct 或 NAT 受限直连（有端口段）。仅出口档 NAT 不能作入口/中间跳。 */
function inboundCapable(s: Server): boolean {
  return s.machine_type === 'direct' || s.allowed_ports.length > 0
}

function serverLabel(s: Server): string {
  const tags: string[] = []
  if (!s.online) {
    tags.push('离线')
  }
  if (!inboundCapable(s)) {
    tags.push('仅出口')
  }
  return tags.length > 0 ? `${s.alias}（${tags.join('，')}）` : s.alias
}

const chainNameAlphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'

function randomChainName(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(4))
  const suffix = [...bytes].map((byte) => chainNameAlphabet[byte % chainNameAlphabet.length]).join('')
  return `Chain #${suffix}`
}

export default function Chains() {
  const { timezone } = useTimezone()
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState<string | null>(null)

  const [open, setOpen] = useState(false)
  const [chainType, setChainType] = useState<'direct' | 'relay'>('direct')
  const [name, setName] = useState('')
  const [entryId, setEntryId] = useState('')
  const [middleIds, setMiddleIds] = useState<string[]>([])
  const [exitId, setExitId] = useState('')
  const [entryPort, setEntryPort] = useState('')
  const [protocol, setProtocol] = useState('vless')
  const [port, setPort] = useState('')
  const [shortId, setShortId] = useState('')
  const [destPreset, setDestPreset] = useState(DEFAULT_REALITY_DEST)
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
  const selectedEntry = servers.find((s) => String(s.id) === entryId)
  const selectedExit = servers.find((s) => String(s.id) === exitId)
  const selectedMiddleServers = middleIds.flatMap((id) => {
    const server = servers.find((candidate) => String(candidate.id) === id)
    return server ? [server] : []
  })
  const topologyServers = [
    ...(selectedEntry ? [selectedEntry] : []),
    ...selectedMiddleServers,
    ...(chainType === 'relay' && selectedExit ? [selectedExit] : []),
  ]
  const hopIndexes =
    chainType === 'relay' ? selectedMiddleServers.map((_, index) => index + 1) : []
  const strictNameResult = validateNameTemplate(name, {
    servers: topologyServers,
    protocol,
    port: entryPort,
    hopIndexes,
  })

  const load = useCallback(() => {
    Promise.all([api.chains(), api.nodes(), api.servers()])
      .then(([c, n, s]) => {
        setChains(c)
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
      setChainType('direct')
      setName('')
      setEntryId('')
      setMiddleIds([])
      setExitId('')
      setEntryPort('')
      setProtocol('vless')
      setPort('')
      setShortId('')
      setDestPreset(DEFAULT_REALITY_DEST)
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

  const onTypeChange = (value: string | null) => {
    if (value !== 'direct' && value !== 'relay') return
    setChainType(value)
    setMiddleIds([])
    setExitId('')
    if (value === 'relay' && protocol === 'dokodemo-door') {
      setProtocol('vless')
    }
  }

  const setMiddle = (i: number, value: string) => {
    const next = middleIds.slice()
    next[i] = value
    setMiddleIds(next)
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const resolvedName = name.trim() || randomChainName()
    if (name.trim() && strictNameResult.error) {
      setCreateError(strictNameResult.error)
      return
    }
    if (!entryId) {
      setCreateError('请选择直连服务器')
      return
    }
    if (chainType === 'relay' && (!exitId || middleIds.some((m) => !m))) {
      setCreateError('请完整选择入口、中间跳与出口服务器')
      return
    }
    const hopIds = chainType === 'direct' ? [entryId] : [entryId, ...middleIds, exitId]
    if (hopIds.length > 4) {
      setCreateError('链长上限 4 跳（入口 + 中间跳 ≤2 + 出口）')
      return
    }
    if (new Set(hopIds).size !== hopIds.length) {
      setCreateError('同一服务器在一条链中不重复')
      return
    }
    // 直连唯一服务器、或中转的入口与中转跳必须有入站能力；中转出口任意。
    const inboundIds = chainType === 'direct' ? hopIds : hopIds.slice(0, -1)
    for (const id of inboundIds) {
      const srv = servers.find((s) => String(s.id) === id)
      if (srv && !inboundCapable(srv)) {
        setCreateError(`服务器 ${srv.alias} 无入站能力（仅出口档 NAT），不能作入口/中间跳`)
        return
      }
    }
    const nodeBody: CreateNodeRequest = {
      name: resolvedName,
      server_id: Number(chainType === 'direct' ? entryId : exitId),
      protocol,
    }
    if ((chainType === 'direct' ? entryPort : port).trim()) {
      nodeBody.port = Number(chainType === 'direct' ? entryPort : port)
    }
    if (isReality) {
      nodeBody.fingerprint = fingerprint
      nodeBody.network = network
      if (shortId.trim()) {
        nodeBody.short_id = shortId.trim()
      }
      if (dest.trim()) {
        nodeBody.dest = dest.trim()
      }
      const names = serverNames
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      if (names.length > 0) {
        nodeBody.server_names = names
      }
      if (network === 'xhttp') {
        nodeBody.path = path.trim() || '/'
        nodeBody.mode = mode
        if (host.trim()) {
          nodeBody.host = host.trim()
        }
      }
      if (protocol === 'vless') {
        // vision 仅 tcp；xhttp 必须无 flow；vision + Encryption 允许组合（§15）
        nodeBody.flow = network === 'tcp' ? flow : 'none'
        if (encryption !== 'none') {
          nodeBody.encryption = encryption
        }
      }
    }
    if (protocol === 'dokodemo-door') {
      if (!targetAddress.trim() || !targetPort.trim()) {
        setCreateError('dokodemo-door 需要目标地址与目标端口')
        return
      }
      nodeBody.target_address = targetAddress.trim()
      nodeBody.target_port = Number(targetPort)
    }
    setCreating(true)
    try {
      if (chainType === 'direct') {
        await api.createNode(nodeBody)
      } else {
        const body: CreateChainRequest = {
          name: resolvedName,
          entry: { server_id: Number(entryId) },
          middle: middleIds.map((id) => ({ server_id: Number(id) })),
          exit: { server_id: Number(exitId) },
          node: nodeBody,
        }
        if (entryPort.trim()) body.entry_port = Number(entryPort)
        await api.createChain(body)
      }
      onOpenChange(false)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onRetry = async (id: number) => {
    setRetrying(`relay-${id}`)
    try {
      await api.retryChain(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRetrying(null)
    }
  }

  const onRetryDirect = async (id: number) => {
    setRetrying(`direct-${id}`)
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
    const chain = chains.find((c) => c.id === id)
    if (!window.confirm(`确定删除链路「${chain?.name || `#${id}`}」？将逐跳拆除转发/隧道并删除出口节点。`)) {
      return
    }
    try {
      await api.deleteChain(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onDeleteDirect = async (id: number) => {
    const node = nodes.find((candidate) => candidate.id === id)
    if (!window.confirm(`确定删除直连链路「${node?.name || `#${id}`}」？将从服务器移除业务入站。`)) {
      return
    }
    try {
      await api.deleteNode(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const serverOnline = (id: number): boolean =>
    servers.find((s) => s.id === id)?.online ?? false

  const serverSelectItems = servers.map((s) => ({ value: String(s.id), label: serverLabel(s) }))
  const nodeById = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes])
  const relayExitNodeIds = useMemo(
    () => new Set(chains.flatMap((chain) => chain.hops.filter((hop) => hop.role === 'exit').map((hop) => hop.node_id))),
    [chains],
  )
  const directNodes = useMemo(
    () => nodes.filter((node) => !relayExitNodeIds.has(node.id)),
    [nodes, relayExitNodeIds],
  )
  const entries = useMemo(
    () =>
      [
        ...directNodes.map((node) => ({ type: 'direct' as const, createdAt: node.created_at, node })),
        ...chains.map((chain) => ({ type: 'relay' as const, createdAt: chain.created_at, chain })),
      ].toSorted((a, b) => b.createdAt.localeCompare(a.createdAt)),
    [chains, directNodes],
  )

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">链路</h1>
        <Button onClick={() => setOpen(true)}>
          <PlusIcon />
          创建链路
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="flex flex-col gap-3">
        {loading ? (
          <div className="rounded-lg border p-6 text-center text-sm text-muted-foreground">
            加载中…
          </div>
        ) : entries.length === 0 ? (
          <div className="flex min-h-64 flex-col items-center justify-center rounded-xl border border-dashed text-center">
            <RouteIcon className="size-8 text-muted-foreground" />
            <p className="mt-3 text-sm text-muted-foreground">暂无链路，点击右上角“创建链路”开始</p>
          </div>
        ) : (
          entries.map((entry) => {
            if (entry.type === 'direct') {
              const node = entry.node
              const st = chainStatusStyle[node.status] ?? chainStatusStyle.pending
              const server = servers.find((candidate) => candidate.id === node.server_id)
              const displayPort = node.realized_config?.port ?? node.port
              return (
                <Card key={`direct-${node.id}`} size="sm">
                  <CardHeader>
                    <CardTitle className="flex flex-wrap items-center gap-2">
                      <span>{node.name || `直连 #${node.id}`}</span>
                      <Badge variant="secondary">直连</Badge>
                      <Badge variant="outline" className={st.className}>
                        {st.label}
                      </Badge>
                    </CardTitle>
                    <CardDescription>
                      {formatDateTime(node.created_at, timezone)}
                      {node.traffic
                        ? ` · ↑ ${humanizeBytes(node.traffic.up)} / ↓ ${humanizeBytes(node.traffic.down)}`
                        : ''}
                    </CardDescription>
                    <CardAction>
                      <div className="flex gap-2">
                        {node.status === 'failed' ? (
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={retrying === `direct-${node.id}`}
                            onClick={() => onRetryDirect(node.id)}
                          >
                            {retrying === `direct-${node.id}` ? '重试中…' : '重试链路'}
                          </Button>
                        ) : null}
                        <Button variant="outline" size="sm" onClick={() => onDeleteDirect(node.id)}>
                          删除链路
                        </Button>
                      </div>
                    </CardAction>
                  </CardHeader>
                  <CardContent>
                    {node.error ? <p className="mb-2 text-sm text-destructive">{node.error}</p> : null}
                    <div className="flex flex-wrap items-center gap-1 text-sm">
                      <span className="inline-flex items-center gap-1 rounded-md border px-2 py-1">
                        <span className="text-muted-foreground">直连</span>
                        <span className="font-medium">{node.server_alias}</span>
                        {displayPort ? <span className="text-muted-foreground">:{displayPort}</span> : null}
                        <Badge variant="outline" className={st.className}>
                          {st.label}
                        </Badge>
                        {server && !server.online ? <Badge variant="outline">离线</Badge> : null}
                      </span>
                    </div>
                  </CardContent>
                </Card>
              )
            }
            const c = entry.chain
            const st = chainStatusStyle[c.status] ?? chainStatusStyle.pending
            const hasFailedHop = c.hops.some((h) => h.status === 'failed')
            const exitNodeId = c.hops.find((hop) => hop.role === 'exit')?.node_id
            const traffic = exitNodeId ? nodeById.get(exitNodeId)?.traffic : null
            return (
              <Card key={`relay-${c.id}`} size="sm">
                <CardHeader>
                  <CardTitle className="flex flex-wrap items-center gap-2">
                    <span>{c.name || `中转 #${c.id}`}</span>
                    <Badge variant="secondary">中转</Badge>
                    <Badge variant="outline" className={st.className}>
                      {st.label}
                    </Badge>
                    {c.status === 'degraded' ? (
                      <span className="text-xs text-amber-700">存在离线跳，恢复后自愈</span>
                    ) : null}
                  </CardTitle>
                  <CardDescription>
                    {formatDateTime(c.created_at, timezone)}
                    {traffic
                      ? ` · ↑ ${humanizeBytes(traffic.up)} / ↓ ${humanizeBytes(traffic.down)}`
                      : ''}
                  </CardDescription>
                  <CardAction>
                    <div className="flex gap-2">
                      {c.status === 'failed' || hasFailedHop ? (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={retrying === `relay-${c.id}`}
                          onClick={() => onRetry(c.id)}
                        >
                          {retrying === `relay-${c.id}` ? '重试中…' : '重试链路'}
                        </Button>
                      ) : null}
                      <Button variant="outline" size="sm" onClick={() => onDelete(c.id)}>
                        删除链路
                      </Button>
                    </div>
                  </CardAction>
                </CardHeader>
                <CardContent className="flex flex-col gap-2">
                  {c.error ? <p className="text-sm text-destructive">{c.error}</p> : null}
                  <div className="flex flex-wrap items-center gap-1 text-sm">
                  {c.hops.map((h, i) => {
                    const hst = hopStatusStyle[h.status] ?? hopStatusStyle.pending
                    const offline = !serverOnline(h.server_id)
                    return (
                      <span key={h.id} className="flex items-center gap-1">
                        {i > 0 ? <span className="text-muted-foreground">→</span> : null}
                        <span
                          className="inline-flex items-center gap-1 rounded-md border px-2 py-1"
                          title={h.error || undefined}
                        >
                          <span className="text-muted-foreground">{roleLabel[h.role]}</span>
                          <span className="font-medium">{h.server_alias}</span>
                          {h.role === 'entry' && h.forward_port !== 0 ? (
                            <span className="text-muted-foreground">:{h.forward_port}</span>
                          ) : null}
                          <Badge variant="outline" className={hst.className}>
                            {hst.label}
                          </Badge>
                          {(c.status === 'degraded' || c.status === 'failed') && offline ? (
                            <Badge
                              variant="outline"
                              className="border-amber-200 bg-amber-50 text-amber-700"
                            >
                              离线
                            </Badge>
                          ) : null}
                        </span>
                      </span>
                    )
                  })}
                  </div>
                {c.hops.some((h) => h.error) ? (
                  <div className="flex flex-col gap-1 text-xs text-destructive">
                    {c.hops
                      .filter((h) => h.error)
                      .map((h) => (
                        <p key={h.id}>
                          {roleLabel[h.role]}（{h.server_alias}）：{h.error}
                        </p>
                      ))}
                  </div>
                ) : null}
                </CardContent>
              </Card>
            )
          })
        )}
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>创建链路</DialogTitle>
            <DialogDescription>
              直连只包含一台服务器；中转依次选择入口 → 中转（0-2 个）→ 出口，客户端仅见入口。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label>链路类型</Label>
              <Select
                value={chainType}
                onValueChange={onTypeChange}
                items={[
                  { value: 'direct', label: '直连' },
                  { value: 'relay', label: '中转' },
                ]}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="direct">直连</SelectItem>
                    <SelectItem value="relay">中转</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="chain-name-template">链路名称模板</Label>
              <NameTemplateInput
                id="chain-name-template"
                value={name}
                onChange={setName}
                context={{ servers: topologyServers, protocol, port: entryPort, hopIndexes }}
                allowEmpty
                placeholder="留空自动生成 Chain #xxxx"
                emptyHint="留空将在创建时自动生成 Chain #xxxx（4 位随机大小写字母）"
              />
              <p className="text-xs text-muted-foreground">
                输入 {'{{'} 后可选择变量；中转节点显示为 HOP_1/HOP_2，对应模板中的
                HOP[1]/HOP[2]。
              </p>
            </div>
            <div className="space-y-2">
              <Label>{chainType === 'direct' ? '直连服务器' : '入口服务器'}</Label>
              <Select
                value={entryId}
                onValueChange={(v) => setEntryId(String(v))}
                items={serverSelectItems}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择入口服务器" />
                </SelectTrigger>
                <SelectContent>
                  {servers.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {serverLabel(s)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {chainType === 'relay' ? (
              <>
            <div className="space-y-2">
              <Label>中转服务器（0-2 个）</Label>
              {middleIds.map((id, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Select
                    value={id}
                    onValueChange={(v) => setMiddle(i, String(v))}
                    items={serverSelectItems}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder={`中转 ${i + 1}`} />
                    </SelectTrigger>
                    <SelectContent>
                      {servers.map((s) => (
                        <SelectItem key={s.id} value={String(s.id)}>
                          {serverLabel(s)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setMiddleIds(middleIds.filter((_, j) => j !== i))}
                  >
                    <XIcon />
                  </Button>
                </div>
              ))}
              {middleIds.length < 2 && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setMiddleIds([...middleIds, ''])}
                >
                  <PlusIcon />
                  添加中转
                </Button>
              )}
            </div>
            <div className="space-y-2">
              <Label>出口服务器</Label>
              <Select
                value={exitId}
                onValueChange={(v) => setExitId(String(v))}
                items={serverSelectItems}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择出口服务器" />
                </SelectTrigger>
                <SelectContent>
                  {servers.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {serverLabel(s)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
              </>
            ) : null}
            <div className="space-y-2">
              <Label htmlFor="entryPort">{chainType === 'direct' ? '业务端口' : '入口端口'}</Label>
              <Input
                id="entryPort"
                type="number"
                min={1}
                max={65535}
                value={entryPort}
                onChange={(e) => setEntryPort(e.target.value)}
                placeholder="留空自动分配（须在服务器可用段内）"
              />
            </div>

            <div className="space-y-2">
              <Label>{chainType === 'direct' ? '协议' : '出口协议'}</Label>
              <Select value={protocol} onValueChange={(v) => v && setProtocol(v)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(chainType === 'direct' ? DIRECT_PROTOCOLS : RELAY_PROTOCOLS).map((p) => (
                    <SelectItem key={p} value={p}>
                      {p}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {chainType === 'relay' ? (
              <div className="space-y-2">
              <Label htmlFor="exitNodePort">出口节点端口</Label>
              <Input
                id="exitNodePort"
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="留空自动分配"
              />
            </div>
            ) : null}

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
                <RealityDestPicker
                  idPrefix="chain"
                  preset={destPreset}
                  onPresetChange={setDestPreset}
                  dest={dest}
                  onDestChange={setDest}
                  serverNames={serverNames}
                  onServerNamesChange={setServerNames}
                />
              </>
            )}

            {chainType === 'direct' && protocol === 'dokodemo-door' ? (
              <>
                <div className="space-y-2">
                  <Label htmlFor="targetAddress">目标地址</Label>
                  <Input
                    id="targetAddress"
                    value={targetAddress}
                    onChange={(event) => setTargetAddress(event.target.value)}
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
                    onChange={(event) => setTargetPort(event.target.value)}
                    placeholder="转发目的地端口"
                  />
                </div>
              </>
            ) : null}

            {createError && <p className="text-sm text-destructive">{createError}</p>}
            <DialogFooter>
              <Button
                type="submit"
                disabled={
                  creating ||
                  Boolean(name.trim() && strictNameResult.error) ||
                  !entryId ||
                  (chainType === 'relay' && !exitId)
                }
              >
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
