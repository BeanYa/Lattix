import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { PlusIcon, XIcon } from 'lucide-react'

import { RealityDestPicker } from '@/components/RealityDestPicker'
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
import { api, errorMessage } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { DEFAULT_CHAIN_NAME_TEMPLATE, renderNameTemplate } from '@/lib/naming'
import { DEFAULT_REALITY_DEST } from '@/lib/reality'
import { useTimezone } from '@/lib/timezone'
import type {
  Chain,
  ChainHopRole,
  ChainStatus,
  CreateChainRequest,
  NodeStatus,
  Server,
} from '@/lib/types'

const chainStatusStyle: Record<ChainStatus, { label: string; className: string }> = {
  active: { label: '已生效', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '应用中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '失败', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '待处理', className: 'border-gray-200 bg-gray-50 text-gray-500' },
  degraded: { label: '降级', className: 'border-amber-200 bg-amber-50 text-amber-700' },
}

const hopStatusStyle: Record<NodeStatus, { label: string; className: string }> = {
  active: { label: '已生效', className: 'border-green-200 bg-green-50 text-green-700' },
  applying: { label: '应用中', className: 'border-yellow-200 bg-yellow-50 text-yellow-700' },
  failed: { label: '失败', className: 'border-red-200 bg-red-50 text-red-700' },
  pending: { label: '待处理', className: 'border-gray-200 bg-gray-50 text-gray-500' },
}

const roleLabel: Record<ChainHopRole, string> = {
  entry: '入口',
  middle: '中间',
  exit: '出口',
}

// 与后端 shared 包保持一致的协议/选项常量（出口节点协议表单复用 Nodes 向导的 vless+reality 字段）。
const PROTOCOLS = ['vless', 'socks', 'http'] as const
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

export default function Chains() {
  const { timezone } = useTimezone()
  const [chains, setChains] = useState<Chain[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [name, setName] = useState(DEFAULT_CHAIN_NAME_TEMPLATE)
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
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')

  const isReality = REALITY_PROTOCOLS.includes(protocol)
  const selectedEntry = servers.find((s) => String(s.id) === entryId)
  const selectedExit = servers.find((s) => String(s.id) === exitId)
  const namePreview = renderNameTemplate(name, {
    location: selectedEntry?.alias ?? '?',
    serverId: selectedEntry?.id,
    protocol,
    port: entryPort,
    entry: selectedEntry?.alias,
    entryId: selectedEntry?.id,
    exit: selectedExit?.alias,
    exitId: selectedExit?.id,
    hops: 2 + middleIds.length,
    tags: selectedEntry?.tags ?? [],
  })

  const load = useCallback(() => {
    Promise.all([api.chains(), api.servers()])
      .then(([c, s]) => {
        setChains(c)
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
      setName(DEFAULT_CHAIN_NAME_TEMPLATE)
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
      setCreateError('')
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
    const trimmedName = name.trim()
    if (!trimmedName) {
      setCreateError('请输入链路名称')
      return
    }
    if (!entryId || !exitId || middleIds.some((m) => !m)) {
      setCreateError('请完整选择入口、中间跳与出口服务器')
      return
    }
    const hopIds = [entryId, ...middleIds, exitId]
    if (hopIds.length > 4) {
      setCreateError('链长上限 4 跳（入口 + 中间跳 ≤2 + 出口）')
      return
    }
    if (new Set(hopIds).size !== hopIds.length) {
      setCreateError('同一服务器在一条链中不重复')
      return
    }
    // 入口与中间跳必须有入站能力（direct 或 NAT 受限直连），出口任意。
    for (const id of hopIds.slice(0, -1)) {
      const srv = servers.find((s) => String(s.id) === id)
      if (srv && !inboundCapable(srv)) {
        setCreateError(`服务器 ${srv.alias} 无入站能力（仅出口档 NAT），不能作入口/中间跳`)
        return
      }
    }
    const body: CreateChainRequest = {
      name: trimmedName,
      entry: { server_id: Number(entryId) },
      middle: middleIds.map((id) => ({ server_id: Number(id) })),
      exit: { server_id: Number(exitId) },
      node: { protocol },
    }
    if (entryPort.trim()) {
      body.entry_port = Number(entryPort)
    }
    if (port.trim()) {
      body.node.port = Number(port)
    }
    if (isReality) {
      body.node.fingerprint = fingerprint
      body.node.network = network
      if (shortId.trim()) {
        body.node.short_id = shortId.trim()
      }
      if (dest.trim()) {
        body.node.dest = dest.trim()
      }
      const names = serverNames
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      if (names.length > 0) {
        body.node.server_names = names
      }
      if (network === 'xhttp') {
        body.node.path = path.trim() || '/'
        body.node.mode = mode
        if (host.trim()) {
          body.node.host = host.trim()
        }
      }
      if (protocol === 'vless') {
        // vision 仅 tcp；xhttp 必须无 flow；vision + Encryption 允许组合（§15）
        body.node.flow = network === 'tcp' ? flow : 'none'
        if (encryption !== 'none') {
          body.node.encryption = encryption
        }
      }
    }
    setCreating(true)
    try {
      await api.createChain(body)
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
      await api.retryChain(id)
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

  const serverOnline = (id: number): boolean =>
    servers.find((s) => s.id === id)?.online ?? false

  const serverSelectItems = servers.map((s) => ({ value: String(s.id), label: serverLabel(s) }))

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

      <div className="space-y-3">
        {loading ? (
          <div className="rounded-lg border p-6 text-center text-sm text-muted-foreground">
            加载中…
          </div>
        ) : chains.length === 0 ? (
          <div className="rounded-lg border p-6 text-center text-sm text-muted-foreground">
            暂无链路，点击右上角「创建链路」开始
          </div>
        ) : (
          chains.map((c) => {
            const st = chainStatusStyle[c.status] ?? chainStatusStyle.pending
            const hasFailedHop = c.hops.some((h) => h.status === 'failed')
            return (
              <div key={c.id} className="space-y-2 rounded-lg border p-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{c.name || `链 #${c.id}`}</span>
                    <span className="text-xs text-muted-foreground">#{c.id}</span>
                    <Badge variant="outline" className={st.className}>
                      {st.label}
                    </Badge>
                    {c.status === 'degraded' && (
                      <span className="text-xs text-amber-700">存在离线跳，恢复后自愈</span>
                    )}
                    <span className="text-xs text-muted-foreground">
                      {formatDateTime(c.created_at, timezone)}
                    </span>
                  </div>
                  <div className="space-x-2">
                    {(c.status === 'failed' || hasFailedHop) && (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={retrying === c.id}
                        onClick={() => onRetry(c.id)}
                      >
                        {retrying === c.id ? '重试中…' : '重试'}
                      </Button>
                    )}
                    <Button variant="outline" size="sm" onClick={() => onDelete(c.id)}>
                      删除
                    </Button>
                  </div>
                </div>
                {c.error && <p className="text-sm text-destructive">{c.error}</p>}
                <div className="flex flex-wrap items-center gap-1 text-sm">
                  {c.hops.map((h, i) => {
                    const hst = hopStatusStyle[h.status] ?? hopStatusStyle.pending
                    const offline = !serverOnline(h.server_id)
                    return (
                      <span key={h.id} className="flex items-center gap-1">
                        {i > 0 && <span className="text-muted-foreground">→</span>}
                        <span
                          className="inline-flex items-center gap-1 rounded-md border px-2 py-1"
                          title={h.error || undefined}
                        >
                          <span className="text-muted-foreground">{roleLabel[h.role]}</span>
                          <span className="font-medium">{h.server_alias}</span>
                          {h.role === 'entry' && h.forward_port !== 0 && (
                            <span className="text-muted-foreground">:{h.forward_port}</span>
                          )}
                          <Badge variant="outline" className={hst.className}>
                            {hst.label}
                          </Badge>
                          {(c.status === 'degraded' || c.status === 'failed') && offline && (
                            <Badge
                              variant="outline"
                              className="border-amber-200 bg-amber-50 text-amber-700"
                            >
                              离线
                            </Badge>
                          )}
                        </span>
                      </span>
                    )
                  })}
                </div>
                {c.hops.some((h) => h.error) && (
                  <div className="space-y-1 text-xs text-destructive">
                    {c.hops
                      .filter((h) => h.error)
                      .map((h) => (
                        <p key={h.id}>
                          {roleLabel[h.role]}跳（{h.server_alias}）：{h.error}
                        </p>
                      ))}
                  </div>
                )}
              </div>
            )
          })
        )}
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>创建链路</DialogTitle>
            <DialogDescription>
              依次选择入口 → 中间跳（0-2 个）→ 出口，客户端仅见入口；出口节点协议表单与普通节点一致
              （dokodemo-door 不能作链出口）。入口与中间跳必须有入站能力（独立 IP 或有端口段的 NAT）。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="chainName">链路名称模板</Label>
              <Input
                id="chainName"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={200}
                placeholder="{{ENTRY}}-{{EXIT}}-{{PROTOCOL}}-chain"
                autoFocus
                required
              />
              <p className="text-xs text-muted-foreground">
                可用参数：{'{{LOCATION}}'}、{'{{ENTRY}}'}、{'{{EXIT}}'}、{'{{HOPS}}'}、
                {'{{PROTOCOL}}'}、{'{{PORT}}'}、{'{{TAG_1}}'}…；Tag 来自入口服务器。
              </p>
              <p className="text-xs">
                预览：<span className="font-medium">{namePreview}</span>
              </p>
            </div>
            <div className="space-y-2">
              <Label>入口服务器</Label>
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
            <div className="space-y-2">
              <Label>中间跳服务器（0-2 个）</Label>
              {middleIds.map((id, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Select
                    value={id}
                    onValueChange={(v) => setMiddle(i, String(v))}
                    items={serverSelectItems}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder={`中间跳 ${i + 1}`} />
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
                  添加中间跳
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
            <div className="space-y-2">
              <Label htmlFor="entryPort">入口端口</Label>
              <Input
                id="entryPort"
                type="number"
                min={1}
                max={65535}
                value={entryPort}
                onChange={(e) => setEntryPort(e.target.value)}
                placeholder="留空自动分配（须在入口机可用段内）"
              />
            </div>

            <div className="space-y-2">
              <Label>出口节点协议</Label>
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

            {createError && <p className="text-sm text-destructive">{createError}</p>}
            <DialogFooter>
              <Button type="submit" disabled={creating || !name.trim() || !entryId || !exitId}>
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
