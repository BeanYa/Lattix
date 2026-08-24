import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import {
  ArrowDownIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  BarChart3Icon,
  CircleCheckIcon,
  Clock3Icon,
  LoaderCircleIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  RouteIcon,
  SendIcon,
  TriangleAlertIcon,
  XIcon,
} from 'lucide-react'

import { NameTemplateInput } from '@/components/NameTemplateInput'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { RealityDestPicker } from '@/components/RealityDestPicker'
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
import { addressFamily } from '@/lib/address'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { validateNameTemplate } from '@/lib/naming'
import { DEFAULT_REALITY_DEST, inferRealityDestPreset } from '@/lib/reality'
import { isServerOnline } from '@/lib/server-state'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'
import { cn } from '@/lib/utils'
import type {
  Chain,
  ChainHopInput,
  ChainHopRole,
  ChainStatus,
  ChainTrafficBucket,
  CreateChainRequest,
  CreateNodeRequest,
  EditChainRequest,
  NodeStatus,
  Server,
  XrayNode,
} from '@/lib/types'

import './chains.css'

/** cg-status 贴纸语义：lime=正常/在线，blue=部署流程中，red=异常/失败，muted=其他。 */
type CgStatusTone = 'is-lime' | 'is-blue' | 'is-red' | 'is-muted'

const chainStatusStyle: Record<ChainStatus, { label: string; cg: CgStatusTone }> = {
  active: { label: '正常', cg: 'is-lime' },
  applying: { label: '部署中', cg: 'is-blue' },
  failed: { label: '异常', cg: 'is-red' },
  pending: { label: '部署中', cg: 'is-blue' },
  degraded: { label: '降级', cg: 'is-red' },
  waiting_for_agent: { label: '等待 Agent', cg: 'is-blue' },
  active_unconfirmed: { label: '已强制发布', cg: 'is-muted' },
  active_failed: { label: '发布后失败', cg: 'is-red' },
  cleanup_pending: { label: '等待清理', cg: 'is-blue' },
  invalid: { label: '已失效', cg: 'is-red' },
  deleted: { label: '已删除', cg: 'is-muted' },
}

const hopStatusStyle: Record<NodeStatus, { label: string; cg: CgStatusTone }> = {
  active: { label: '正常', cg: 'is-lime' },
  applying: { label: '部署中', cg: 'is-blue' },
  failed: { label: '异常', cg: 'is-red' },
  pending: { label: '部署中', cg: 'is-blue' },
}

function ChainStateMark({ tone, label }: { tone: CgStatusTone; label: string }) {
  const loading = ['部署中', '等待 Agent', '等待清理'].includes(label)
  const Icon = tone === 'is-lime' || label === '已强制发布'
    ? CircleCheckIcon
    : loading
      ? LoaderCircleIcon
      : TriangleAlertIcon
  return (
    <span className={cn('cg-chain-mark', tone)} title={label} aria-label={label}>
      <Icon className={cn(loading && 'animate-spin motion-reduce:animate-none')} />
    </span>
  )
}

function TrafficSummary({
  up,
  down,
  rawUp,
  rawDown,
  multiplier,
}: {
  up?: number
  down?: number
  rawUp?: number
  rawDown?: number
  multiplier?: string
}) {
  const hasTraffic = up !== undefined && down !== undefined
  const adjusted = hasTraffic && rawUp !== undefined && rawDown !== undefined && (rawUp !== up || rawDown !== down)
  return (
    <div className="cg-chain-traffic" aria-label="累计流量">
      <div className="cg-chain-traffic-item is-up">
        <span className="cg-chain-traffic-label">
          <ArrowUpIcon />
          累计上传
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(up ?? 0) : '--'}
        </strong>
        {adjusted ? <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawUp ?? 0)}</span> : null}
      </div>
      <div className="cg-chain-traffic-item is-down">
        <span className="cg-chain-traffic-label">
          <ArrowDownIcon />
          累计下载
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(down ?? 0) : '--'}
        </strong>
        {adjusted ? <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawDown ?? 0)}</span> : null}
      </div>
      {multiplier ? (
        <span className="cg-chain-traffic-multiplier">流量倍率 x{multiplier}</span>
      ) : null}
    </div>
  )
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
  if (!isServerOnline(s)) {
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

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

function parseConfigRecord(value: unknown): Record<string, unknown> {
  const parsed: unknown = typeof value === 'string' ? JSON.parse(value) : value
  const record = asRecord(parsed)
  if (!record) throw new Error('config is not an object')
  return record
}

// 逐跳公网地址选择（§9）：候选 = 服务器 addresses（空则回退默认/学习地址）；空值 = 跟随服务器默认地址。
// 服务器同时有 IPv4/IPv6 字面量条目时提供族切换（域名条目两组均显示），切换后自动选中该族第一个地址。
function HopAddressField({
  server,
  value,
  onChange,
}: {
  server: Server | undefined
  value: string
  onChange: (addr: string) => void
}) {
  const [family, setFamily] = useState<'ipv4' | 'ipv6'>(() =>
    value && addressFamily(value) === 'ipv6' ? 'ipv6' : 'ipv4',
  )
  if (!server) {
    return null
  }
  const candidates = server.addresses.length > 0
    ? server.addresses
    : [...new Set([server.address, server.learned_addr].filter(Boolean))]
  if (candidates.length === 0) {
    return null
  }
  const hasV4 = candidates.some((a) => addressFamily(a) === 'ipv4')
  const hasV6 = candidates.some((a) => addressFamily(a) === 'ipv6')
  const showFamilySwitch = hasV4 && hasV6
  const invalid = value !== '' && !candidates.includes(value)
  const visible = candidates.filter((a) => {
    const f = addressFamily(a)
    return !showFamilySwitch || f === 'domain' || f === family
  })
  const items = [
    { value: '', label: '跟随服务器默认地址' },
    ...visible.map((a) => ({ value: a, label: a })),
    ...(invalid ? [{ value, label: `${value}（已失效，将回退默认地址）` }] : []),
  ]
  const switchFamily = (next: 'ipv4' | 'ipv6') => {
    setFamily(next)
    const first = candidates.find((a) => addressFamily(a) === next)
    if (first) {
      onChange(first)
    }
  }
  return (
    <div className="space-y-1.5">
      {showFamilySwitch ? (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <span>公网地址</span>
          {(['ipv4', 'ipv6'] as const).map((f) => (
            <label key={f} className="flex items-center gap-1">
              <input
                type="radio"
                checked={family === f}
                onChange={() => switchFamily(f)}
              />
              {f === 'ipv4' ? 'IPv4' : 'IPv6'}
            </label>
          ))}
        </div>
      ) : (
        <span className="text-xs text-muted-foreground">公网地址</span>
      )}
      <Select
        value={value}
        onValueChange={(v) => onChange(String(v ?? ''))}
        items={items}
      >
        <SelectTrigger className="w-full">
          <SelectValue placeholder="跟随服务器默认地址" />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value === '' ? '__default__' : item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {invalid ? (
        <p className="text-xs text-destructive">所选地址已不在该服务器地址列表中，保存后将回退默认地址</p>
      ) : null}
    </div>
  )
}

function TrafficHistoryChart({
  buckets,
  range,
}: {
  buckets: Array<ChainTrafficBucket & { date: string }>
  range: 'day' | 'month'
}) {
  const chartRef = useRef<SVGSVGElement>(null)
  const [activePoint, setActivePoint] = useState<{ index: number; y: number } | null>(null)
  const width = 680
  const height = 280
  const plot = { left: 48, right: 16, top: 16, bottom: 38 }
  const plotWidth = width - plot.left - plot.right
  const plotHeight = height - plot.top - plot.bottom
  const peak = Math.max(
    1,
    ...buckets.flatMap((bucket) => [bucket.effective_up, bucket.effective_down]),
  )
  const xAt = (index: number) =>
    plot.left + (buckets.length <= 1 ? plotWidth / 2 : (index / (buckets.length - 1)) * plotWidth)
  const yAt = (value: number) => plot.top + (1 - value / peak) * plotHeight
  const points = (key: 'effective_up' | 'effective_down') =>
    buckets.map((bucket, index) => `${xAt(index).toFixed(1)},${yAt(bucket[key]).toFixed(1)}`).join(' ')
  const tickStep = Math.max(1, Math.ceil((buckets.length - 1) / 6))
  const xTicks = buckets
    .map((bucket, index) => ({ bucket, index }))
    .filter(({ index }) => index === 0 || index === buckets.length - 1 || index % tickStep === 0)
  const labelDate = (date: string) => (range === 'day' ? date.slice(5) : date)
  const setPointFromPointer = (clientX: number, clientY: number) => {
    const bounds = chartRef.current?.getBoundingClientRect()
    if (!bounds || buckets.length === 0) return
    const svgX = ((clientX - bounds.left) / bounds.width) * width
    const svgY = ((clientY - bounds.top) / bounds.height) * height
    const ratio = Math.min(1, Math.max(0, (svgX - plot.left) / plotWidth))
    setActivePoint({
      index: Math.round(ratio * (buckets.length - 1)),
      y: Math.min(plot.top + plotHeight, Math.max(plot.top, svgY)),
    })
  }
  const moveActivePoint = (offset: number) => {
    setActivePoint((current) => ({
      index: Math.min(buckets.length - 1, Math.max(0, (current?.index ?? buckets.length - 1) + offset)),
      y: current?.y ?? plot.top + plotHeight / 2,
    }))
  }
  const activeBucket = activePoint ? buckets[activePoint.index] : null

  return (
    <section className="cg-chain-chart">
      <div className="cg-chain-chart-legend">
        <span>相对用量（当前视图峰值 = 100%）</span>
        <div className="cg-chain-chart-legend-keys">
          <span>
            <i className="cg-chain-chart-key-dot is-up" />上传
          </span>
          <span>
            <i className="cg-chain-chart-key-dot is-down" />下载
          </span>
        </div>
      </div>
      <div className="cg-chain-chart-frame">
        <svg
          ref={chartRef}
          viewBox={`0 0 ${width} ${height}`}
          className="cg-chain-chart-svg"
          role="img"
          aria-label="链路上传和下载流量趋势，使用左右方向键查看日期数据"
          tabIndex={0}
          onPointerMove={(event) => setPointFromPointer(event.clientX, event.clientY)}
          onPointerLeave={() => setActivePoint(null)}
          onFocus={() => setActivePoint({ index: buckets.length - 1, y: plot.top + plotHeight / 2 })}
          onBlur={() => setActivePoint(null)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
              event.preventDefault()
              moveActivePoint(event.key === 'ArrowLeft' ? -1 : 1)
            }
          }}
        >
          {[0, 25, 50, 75, 100].map((percent) => {
            const y = plot.top + (1 - percent / 100) * plotHeight
            return (
              <g key={percent}>
                <line
                  x1={plot.left}
                  x2={plot.left + plotWidth}
                  y1={y}
                  y2={y}
                  strokeWidth="1"
                  vectorEffect="non-scaling-stroke"
                  className="cg-chain-chart-grid"
                />
                <text
                  x={plot.left - 8}
                  y={y + 4}
                  textAnchor="end"
                  fontSize="11"
                  className="cg-chain-chart-tick"
                >
                  {percent}%
                </text>
              </g>
            )
          })}
          {xTicks.map(({ bucket, index }) => (
            <text
              key={`${bucket.date}-${index}`}
              x={xAt(index)}
              y={height - 10}
              textAnchor={index === 0 ? 'start' : index === buckets.length - 1 ? 'end' : 'middle'}
              fontSize="11"
              className="cg-chain-chart-tick"
            >
              {labelDate(bucket.date)}
            </text>
          ))}
          <polyline
            points={points('effective_up')}
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            vectorEffect="non-scaling-stroke"
            className="cg-chain-chart-line is-up"
          />
          <polyline
            points={points('effective_down')}
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            vectorEffect="non-scaling-stroke"
            className="cg-chain-chart-line is-down"
          />
          {activePoint && activeBucket ? (
            <>
              <line
                x1={xAt(activePoint.index)}
                x2={xAt(activePoint.index)}
                y1={plot.top}
                y2={plot.top + plotHeight}
                strokeWidth="1"
                strokeDasharray="3 3"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-cross"
              />
              <line
                x1={plot.left}
                x2={plot.left + plotWidth}
                y1={activePoint.y}
                y2={activePoint.y}
                strokeWidth="1"
                strokeDasharray="3 3"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-cross"
              />
              <circle
                cx={xAt(activePoint.index)}
                cy={yAt(activeBucket.effective_up)}
                r="4"
                stroke="currentColor"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-line is-up cg-chain-chart-dot"
              />
              <circle
                cx={xAt(activePoint.index)}
                cy={yAt(activeBucket.effective_down)}
                r="4"
                stroke="currentColor"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-line is-down cg-chain-chart-dot"
              />
            </>
          ) : null}
        </svg>
        {activeBucket && activePoint ? (
          <div
            className={cn(
              'cg-chain-chart-tip',
              activePoint.index < buckets.length / 2 ? 'is-right' : 'is-left',
            )}
          >
            <div className="cg-chain-chart-tip-date">{activeBucket.date}</div>
            <div className="cg-chain-chart-tip-row">
              <span>上传</span>
              <span>{humanizeBytes(activeBucket.effective_up)}</span>
            </div>
            <div className="cg-chain-chart-tip-row">
              <span>下载</span>
              <span>{humanizeBytes(activeBucket.effective_down)}</span>
            </div>
          </div>
        ) : null}
      </div>
    </section>
  )
}

export default function Chains() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [panelShort, setPanelShort] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState<string | null>(null)
  const [editingChainId, setEditingChainId] = useState<number | null>(null)
  const [trafficChain, setTrafficChain] = useState<Chain | null>(null)
  const [trafficHopId, setTrafficHopId] = useState(0)
  const [trafficRange, setTrafficRange] = useState<'day' | 'month'>('day')
  const [trafficHistory, setTrafficHistory] = useState<ChainTrafficBucket[]>([])
  const [trafficLoading, setTrafficLoading] = useState(false)
  const loadRequest = useRef(0)

  const [open, setOpen] = useState(false)
  const [chainType, setChainType] = useState<'direct' | 'relay'>('direct')
  const [name, setName] = useState('')
  const [entryId, setEntryId] = useState('')
  const [middleIds, setMiddleIds] = useState<string[]>([])
  const [exitId, setExitId] = useState('')
  const [entryAddr, setEntryAddr] = useState('')
  const [middleAddrs, setMiddleAddrs] = useState<string[]>([])
  const [exitAddr, setExitAddr] = useState('')
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
	const [trafficMultiplier, setTrafficMultiplier] = useState('1.000')
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
  const entryPortHint = (() => {
    const value = Number(entryPort)
    if (!value || !entryId) return ''
    const owner = chains.find(
      (c) =>
        c.entry_port === value &&
        c.hops[0]?.server_id === Number(entryId) &&
        c.endpoint_id !== 0 &&
        c.status !== 'deleted',
    )
    if (!owner) return ''
    if (owner.id === editingChainId) {
      return `该端口为本链现有监听端口，入口参数修改不会生效（以首次配置为准）`
    }
    return `端口已被链路「${owner.name}」的共享监听占用，将共享其入口参数（dest/short_id 以现有监听为准）`
  })()
  const strictNameResult = validateNameTemplate(name, {
    servers: topologyServers,
    protocol,
    port: entryPort,
    hopIndexes,
  })

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent ? { display: 'silent' as const } : undefined
    try {
      const [nextChains, nextNodes, nextServers] = await Promise.all([
        api.chains(options),
        api.nodes(options),
        api.servers(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setChains(nextChains)
      setNodes(nextNodes)
      setServers(nextServers)
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(err))
    } finally {
      if (!signal?.aborted && request === loadRequest.current) setLoading(false)
    }
  }, [])

  usePolling(load, loadRequest)

  // 名称模板 {{PANEL_SHORT}} 预览值；读取失败由 naming 层回退默认缩写。
  useEffect(() => {
    api.settings().then((s) => setPanelShort(s.panel_short)).catch(() => {})
  }, [])

  const resetChainForm = () => {
    setEditingChainId(null)
    setChainType('direct')
    setName('')
    setEntryId('')
    setMiddleIds([])
    setExitId('')
    setEntryAddr('')
    setMiddleAddrs([])
    setExitAddr('')
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
		setTrafficMultiplier('1.000')
    setCreateError('')
  }

  const openCreate = () => {
    resetChainForm()
    setError('')
    setOpen(true)
  }

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) resetChainForm()
  }

  const openEdit = (chain: Chain) => {
    const service = nodes.find((node) => node.id === chain.service_node_id)
    if (!service && !chain.service_config) {
      setError('链路出口配置不存在')
      return
    }
    let virtual: Record<string, unknown>
    let reality: Record<string, unknown>
    let settings: Record<string, unknown>
    try {
      const rawVirtual: unknown = service?.config_template ?? chain.service_config
      virtual = parseConfigRecord(rawVirtual)
      const template = virtual.template === undefined ? {} : parseConfigRecord(virtual.template)
      const streamSettings = asRecord(template.streamSettings) ?? {}
      reality = asRecord(streamSettings.realitySettings) ?? {}
      settings = asRecord(template.settings) ?? {}
    } catch {
      setError('链路出口配置无法解析')
      return
    }

    const shortIds = Array.isArray(reality.shortIds) ? reality.shortIds : []
    const configuredServerNames = Array.isArray(reality.serverNames)
      ? reality.serverNames.filter((value): value is string => typeof value === 'string')
      : []
    const configuredDest = String(reality.dest || `${DEFAULT_REALITY_DEST}:443`)
    const effectiveServerNames = configuredServerNames.length > 0
      ? configuredServerNames
      : [DEFAULT_REALITY_DEST]
    setEditingChainId(chain.id)
    setChainType(chain.hops.length === 1 ? 'direct' : 'relay')
    setName(chain.name)
    setEntryId(String(chain.hops[0]?.server_id ?? ''))
    setMiddleIds(chain.hops.slice(1, -1).map((hop) => String(hop.server_id)))
    setExitId(chain.hops.length > 1 ? String(chain.hops.at(-1)?.server_id ?? '') : '')
    // 逐跳地址回填：空串 = 跟随服务器默认地址；已失效值由选择器内标注。
    setEntryAddr(chain.hops[0]?.address ?? '')
    setMiddleAddrs(chain.hops.slice(1, -1).map((hop) => hop.address ?? ''))
    setExitAddr(chain.hops.length > 1 ? (chain.hops.at(-1)?.address ?? '') : '')
		setEntryPort(chain.entry_port ? String(chain.entry_port) : '')
    setTrafficMultiplier(chain.traffic_multiplier || '1.000')
    setProtocol(String(virtual.protocol ?? service?.protocol ?? 'vless'))
    setPort(virtual.port ? String(virtual.port) : '')
    setNetwork(String(virtual.network || 'tcp'))
    setFingerprint(String(virtual.fingerprint || 'chrome'))
    setFlow(String(virtual.flow || 'none'))
    setEncryption(String(virtual.encryption || 'none'))
    setPath(String(virtual.path || '/'))
    setMode(String(virtual.mode || 'auto'))
    setHost(String(virtual.host || ''))
    setShortId(typeof shortIds[0] === 'string' ? shortIds[0] : '')
    setDestPreset(inferRealityDestPreset(configuredDest, effectiveServerNames))
    setDest(configuredDest)
    setServerNames(effectiveServerNames.join(','))
    setTargetAddress(String(settings.address || ''))
    setTargetPort(settings.port ? String(settings.port) : '')
    setCreateError('')
    setError('')
    setOpen(true)
  }

  const onTypeChange = (value: string | null) => {
    if (value !== 'direct' && value !== 'relay') return
    setChainType(value)
    setMiddleIds([])
    setExitId('')
    setMiddleAddrs([])
    setExitAddr('')
    if (value === 'relay' && protocol === 'dokodemo-door') {
      setProtocol('vless')
    }
  }

  const setMiddle = (i: number, value: string) => {
    const next = middleIds.slice()
    next[i] = value
    setMiddleIds(next)
    setMiddleAddr(i, '')
  }

  const setMiddleAddr = (i: number, value: string) => {
    const next = middleAddrs.slice()
    next[i] = value
    setMiddleAddrs(next)
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const resolvedName = name.trim() || randomChainName()
    if (name.trim() && strictNameResult.error) {
      setCreateError(strictNameResult.error)
      return
    }
    if (!entryId) {
      setCreateError('请选择入口服务器')
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
    // 逐跳地址：空串 = 跟随服务器默认地址，提交时不携带 address 字段。
    const hopAddrList = chainType === 'direct' ? [entryAddr] : [entryAddr, ...middleAddrs, exitAddr]
    const mkHop = (id: string, addr: string | undefined): ChainHopInput =>
      addr ? { server_id: Number(id), address: addr } : { server_id: Number(id) }
    try {
		if (editingChainId !== null) {
			const body: EditChainRequest = {
				chain_id: editingChainId,
				name: resolvedName,
				hops: hopIds.map((id, i) => mkHop(id, hopAddrList[i])),
				node: nodeBody,
				traffic_multiplier: trafficMultiplier,
			}
			if (entryPort.trim()) body.entry_port = Number(entryPort)
			const { observeId } = await api.editChain(body)
			if (observeId) showOperation({ observeId })
		} else {
			const body: CreateChainRequest = {
				name: resolvedName,
				hops: hopIds.map((id, i) => mkHop(id, hopAddrList[i])),
				entry: mkHop(entryId, entryAddr),
				middle: middleIds.map((id, i) => mkHop(id, middleAddrs[i])),
				exit: mkHop(chainType === 'direct' ? entryId : exitId, chainType === 'direct' ? entryAddr : exitAddr),
				node: nodeBody,
				traffic_multiplier: trafficMultiplier,
			}
			if (entryPort.trim()) body.entry_port = Number(entryPort)
			const { observeId } = await api.createChain(body)
			if (observeId) showOperation({ observeId })
		}
      onOpenChange(false)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onForcePublish = async (chain: Chain) => {
    if (!(await confirm({
      title: '强制发布未确认配置',
      description: `链路「${chain.name}」将立即更新订阅，离线 Agent 的任务继续排队。此操作不会自动回滚。`,
      confirmLabel: '强制发布',
      destructive: true,
    }))) return
    try {
      const { observeId } = await api.forcePublishChain(chain.id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onResetTraffic = async (chain: Chain) => {
    if (!(await confirm({
      title: '重置流量统计',
      description: `重置链路「${chain.name}」及各跳当前显示的累计流量？历史原始计数不会回写 Agent。`,
      confirmLabel: '重置',
      destructive: true,
    }))) return
    try {
      await api.resetChainTraffic(chain.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const loadTrafficHistory = useCallback(async (chain: Chain, hopId: number, range: 'day' | 'month') => {
    setTrafficLoading(true)
    try {
      setTrafficHistory((await api.chainTrafficHistory(chain.id, hopId, range === 'day' ? 30 : 365)) ?? [])
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setTrafficLoading(false)
    }
  }, [])

  const openTraffic = (chain: Chain) => {
    setTrafficChain(chain)
    setTrafficHopId(0)
    setTrafficRange('day')
    void loadTrafficHistory(chain, 0, 'day')
  }

  const onRetry = async (id: number) => {
    setRetrying(`relay-${id}`)
    try {
      const { observeId } = await api.retryChain(id)
      if (observeId) showOperation({ observeId })
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
      const { observeId } = await api.retryNode(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRetrying(null)
    }
  }

  const onDelete = async (id: number) => {
    const chain = chains.find((c) => c.id === id)
    if (!(await confirm({
      title: '删除链路',
      description: `确定删除链路「${chain?.name || `#${id}`}」？将逐跳拆除转发/隧道并删除出口节点。`,
      confirmLabel: '删除链路',
      destructive: true,
    }))) {
      return
    }
    try {
      const { observeId } = await api.deleteChain(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onDeleteDirect = async (id: number) => {
    const node = nodes.find((candidate) => candidate.id === id)
    if (!(await confirm({
      title: '删除直连链路',
      description: `确定删除直连链路「${node?.name || `#${id}`}」？将从服务器移除业务入站。`,
      confirmLabel: '删除链路',
      destructive: true,
    }))) {
      return
    }
    try {
      const { observeId } = await api.deleteNode(id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const serverOnline = (id: number): boolean =>
		isServerOnline(servers.find((s) => s.id === id))

  const serverSelectItems = servers.map((s) => ({ value: String(s.id), label: serverLabel(s) }))
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
  const displayedTrafficHistory = useMemo(() => {
    const normalized = trafficHistory.map((bucket) => ({
      ...bucket,
      date: bucket.date ?? bucket.Date ?? '',
    }))
    if (trafficRange === 'day') return normalized
    const months = new Map<string, ChainTrafficBucket & { date: string }>()
    for (const bucket of normalized) {
      const month = bucket.date.slice(0, 7)
      const current = months.get(month) ?? {
        date: month,
        raw_up: 0,
        raw_down: 0,
        effective_up: 0,
        effective_down: 0,
      }
      current.raw_up += bucket.raw_up
      current.raw_down += bucket.raw_down
      current.effective_up += bucket.effective_up
      current.effective_down += bucket.effective_down
      months.set(month, current)
    }
    return [...months.values()]
  }, [trafficHistory, trafficRange])
  return (
    <Page className="cg-chains">
      <div className="cg-chains-topline">
        <span className="cg-eyebrow">LINKS / ROUTING</span>
        <span className="cg-micro cg-chains-topline-note">入口 → 中转 → 出口 · 客户端仅见入口</span>
      </div>

      <PageHeader
        title="链路"
        description="直连与中转链路的部署状态、跳点拓扑与累计流量，统一在此编排与维护。"
        actions={(
          <>
            <span className="cg-pill">{loading ? '同步中…' : `${entries.length} 条链路`}</span>
            <button type="button" className="cg-button is-primary" onClick={openCreate}>
              <PlusIcon />
              创建链路
            </button>
          </>
        )}
      />

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="cg-chain-list">
        {loading ? (
          <LoadingState />
        ) : entries.length === 0 ? (
          <EmptyState
            icon={<RouteIcon />}
            title="暂无链路"
            description="点击上方“创建链路”开始"
          />
        ) : (
          entries.map((entry) => {
            if (entry.type === 'direct') {
              const node = entry.node
              const st = chainStatusStyle[node.status] ?? chainStatusStyle.pending
              const server = servers.find((candidate) => candidate.id === node.server_id)
              const displayPort = node.realized_config?.port ?? node.port
              return (
                <article key={`direct-${node.id}`} className="cg-card cg-chain-card" data-tone={st.cg}>
                  <header className="cg-chain-card-head">
                    <div className="cg-chain-card-title">
                      <ChainStateMark tone={st.cg} label={st.label} />
                      <strong className="cg-chain-name">{node.name || `直连 #${node.id}`}</strong>
                      <span className="cg-chain-id">#{node.id}</span>
                      <span className="cg-chain-tag">直连</span>
                      <span className={cn('cg-status', st.cg)}>{st.label}</span>
                    </div>
                    <div className="cg-chain-card-side">
                      <span className="cg-chain-meta">
                        <Clock3Icon />
                        创建于 {formatDateTime(node.created_at, timezone)}
                      </span>
                      <div className="cg-chain-actions">
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
                    </div>
                  </header>
                  <div className="cg-chain-card-body">
                    {node.error ? <p className="cg-chain-error">{node.error}</p> : null}
                    <div className="cg-chain-split">
                      <div className="cg-chain-direct">
                        <span className={cn('cg-hop-dot', server && isServerOnline(server) ? 'is-lime' : 'is-red')} />
                        <div className="cg-chain-direct-copy">
                          <span className="cg-chain-direct-label">直连服务器</span>
                          <strong className="cg-chain-direct-name">{node.server_alias}</strong>
                          {displayPort ? <span className="cg-chain-direct-port">:{displayPort}</span> : null}
                        </div>
                        <span className={cn('cg-status', server ? (isServerOnline(server) ? 'is-lime' : 'is-red') : 'is-muted')}>
                          {server ? (isServerOnline(server) ? 'Agent 在线' : 'Agent 离线') : 'Agent 未知'}
                        </span>
                      </div>
                      <TrafficSummary up={node.traffic?.up} down={node.traffic?.down} />
                    </div>
                  </div>
                </article>
              )
            }
            const c = entry.chain
            const st = chainStatusStyle[c.status] ?? chainStatusStyle.pending
            const hasFailedHop = c.hops.some((h) => h.status === 'failed')
            const isDirect = c.hops.length === 1
            const pendingTasks = c.revision_tasks.filter((task) => task.status === 'pending' || task.status === 'queued')
            return (
              <article key={`relay-${c.id}`} className="cg-card cg-chain-card" data-tone={st.cg}>
                <header className="cg-chain-card-head">
                  <div className="cg-chain-card-title">
                    <ChainStateMark tone={st.cg} label={st.label} />
                    <strong className="cg-chain-name">{c.name || `中转 #${c.id}`}</strong>
                    <span className="cg-chain-id">#{c.id}</span>
                    <span className="cg-chain-tag">{isDirect ? '直连' : '中转'}</span>
                    <span className={cn('cg-status', st.cg)}>{st.label}</span>
                    {c.status === 'degraded' ? (
                      c.endpoint_status === 'failed' ? (
                        <span className="cg-chain-note is-red">共享入口部署失败，已发布链路仍保留</span>
                      ) : c.endpoint_status === 'applying' || c.endpoint_status === 'pending' ? (
                        <span className="cg-chain-note is-blue">共享入口部署中，已发布链路仍保留</span>
                      ) : (
                        <span className="cg-chain-note is-red">Agent 离线，已发布链路仍保留</span>
                      )
                    ) : null}
                    {c.revision_forced ? (
                      <span className="cg-chain-note is-blue">订阅已发布，配置等待 Agent 确认</span>
                    ) : null}
                  </div>
                  <div className="cg-chain-card-side">
                    <span className="cg-chain-meta">
                      <Clock3Icon />
                      创建于 {formatDateTime(c.created_at, timezone)}
                    </span>
                    <div className="cg-chain-actions">
                      {c.status === 'failed' || c.status === 'active_failed' || hasFailedHop ? (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={retrying === `relay-${c.id}`}
                          onClick={() => onRetry(c.id)}
                        >
                          {retrying === `relay-${c.id}` ? '重试中…' : '重试链路'}
                        </Button>
                      ) : null}
                      {c.desired_revision_id !== 0 ? (
                        <Button variant="outline" size="sm" onClick={() => onForcePublish(c)}>
                          <SendIcon />
                          强制发布
                        </Button>
                      ) : null}
                      <Button variant="outline" size="icon-sm" title="流量历史" onClick={() => openTraffic(c)}>
                        <BarChart3Icon />
                      </Button>
                      <Button variant="outline" size="icon-sm" title="重置流量" onClick={() => onResetTraffic(c)}>
                        <RotateCcwIcon />
                      </Button>
                      <Button
                        variant="outline"
                        size="icon-sm"
                        title="编辑链路"
                        disabled={
                          c.desired_revision_id !== 0 &&
                          c.status !== 'failed' &&
                          c.status !== 'active_failed'
                        }
                        onClick={() => openEdit(c)}
                      >
                        <PencilIcon />
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => onDelete(c.id)}>
                        删除链路
                      </Button>
                    </div>
                  </div>
                </header>
                <div className="cg-chain-card-body">
                  {c.error ? <p className="cg-chain-error">{c.error}</p> : null}
                  <div className="cg-chain-split">
                    <div className="cg-hop-scroll">
                      <div className="cg-hop-flow">
                        {c.hops.map((h, i) => {
                          const hst = hopStatusStyle[h.status] ?? hopStatusStyle.pending
                          const offline = !serverOnline(h.server_id)
                          const exitNode = h.role === 'exit'
                            ? nodes.find((n) => n.id === h.node_id)
                            : undefined
                          const hopPort = h.role === 'entry'
                            ? (c.entry_port !== 0 ? c.entry_port : h.forward_port)
                            : h.role === 'middle'
                              ? h.forward_port
                              : (exitNode?.realized_config?.port ?? exitNode?.port ?? h.forward_port)
                          return (
                            <div key={h.id} className="cg-hop-seg" title={h.error || undefined}>
                              {i > 0 ? (
                                <span className="cg-hop-link" aria-hidden="true">
                                  <i />
                                  <ArrowDownIcon className="cg-hop-link-v" />
                                  <ArrowRightIcon className="cg-hop-link-h" />
                                </span>
                              ) : null}
                              <div className="cg-hop">
                                <span className="cg-hop-head">
                                  <span className="cg-hop-num">{String(i + 1).padStart(2, '0')}</span>
                                  <span className={cn('cg-hop-dot', offline ? 'is-red' : hst.cg)} />
                                  <span className="cg-hop-role">{roleLabel[h.role]} · {hst.label}</span>
                                </span>
                                <strong className="cg-hop-name">{h.server_alias}</strong>
                                <span className="cg-hop-meta">
                                  {hopPort !== 0 ? <span>端口 {hopPort}</span> : null}
                                  {(h.role === 'entry' || c.hops.length === 1) && c.entry_shared ? (
                                    <span className="cg-hop-shared">共享入口</span>
                                  ) : null}
                                  <span className={offline ? 'is-red' : 'is-lime'}>{offline ? 'Agent 离线' : 'Agent 在线'}</span>
                                  {h.traffic ? <span>↑ {humanizeBytes(h.traffic.effective_up)} · ↓ {humanizeBytes(h.traffic.effective_down)}</span> : null}
                                </span>
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    </div>
                    <TrafficSummary
                      up={c.traffic?.effective_up}
                      down={c.traffic?.effective_down}
                      rawUp={c.traffic?.raw_up}
                      rawDown={c.traffic?.raw_down}
                      multiplier={c.traffic_multiplier}
                    />
                  </div>
                {pendingTasks.length > 0 ? (
                  <p className="cg-chain-queue">
                    {pendingTasks.filter((task) => task.phase === 'apply').length} 个部署任务、
                    {pendingTasks.filter((task) => task.phase === 'cleanup').length} 个清理任务在队列中
                  </p>
                ) : null}
                {c.hops.some((h) => h.error) ? (
                  <div className="cg-chain-hop-errors">
                    {c.hops
                      .filter((h) => h.error)
                      .map((h) => (
                        <p key={h.id}>
                          {roleLabel[h.role]}（{h.server_alias}）：{h.error}
                        </p>
                      ))}
                  </div>
                ) : null}
                </div>
              </article>
            )
          })
        )}
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editingChainId === null ? '创建链路' : '编辑链路'}</DialogTitle>
            <DialogDescription>
              {editingChainId === null
                ? '直连只包含一台服务器；中转依次选择入口 → 中转（0-2 个）→ 出口，客户端仅见入口。'
                : '修改将按出口到入口依次部署，已发布订阅在新 revision 完成前保持不变。'}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label id="chain-type-label">链路类型</Label>
              <div
                role="radiogroup"
                aria-labelledby="chain-type-label"
                className="grid grid-cols-2 gap-2"
              >
                {([
                  ['direct', '直连'],
                  ['relay', '中转'],
                ] as const).map(([value, label]) => (
                  <label
                    key={value}
                    className={cn('cg-chain-type', chainType === value && 'is-selected')}
                  >
                    <input
                      type="radio"
                      name="chain-type"
                      value={value}
                      checked={chainType === value}
                      onChange={(event) => onTypeChange(event.target.value)}
                      className="sr-only"
                    />
                    {label}
                  </label>
                ))}
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="chain-name-template">链路名称模板</Label>
              <NameTemplateInput
                id="chain-name-template"
                value={name}
                onChange={setName}
                context={{ servers: topologyServers, protocol, port: entryPort, hopIndexes, panelShort }}
                allowEmpty
                placeholder="留空自动生成 Chain #xxxx"
                emptyHint="留空将在创建时自动生成 Chain #xxxx（4 位随机大小写字母）"
              />
              <p className="cg-chain-hint">
                输入 {'{{'} 后可选择变量；中转节点显示为 HOP_1/HOP_2，对应模板中的
                HOP[1]/HOP[2]。
              </p>
            </div>
            <div className="space-y-2">
              <Label>{chainType === 'direct' ? '直连服务器' : '入口服务器'}</Label>
              <Select
                value={entryId}
                onValueChange={(v) => { setEntryId(String(v)); setEntryAddr('') }}
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
              <HopAddressField
                key={`entry-${entryId}`}
                server={servers.find((s) => String(s.id) === entryId)}
                value={entryAddr}
                onChange={setEntryAddr}
              />
            </div>
            {chainType === 'relay' ? (
              <>
            <div className="space-y-2">
              <Label>中转服务器（0-2 个）</Label>
              {middleIds.map((id, i) => (
                <div key={i} className="space-y-1.5">
                  <div className="flex items-center gap-2">
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
                      onClick={() => {
                        setMiddleIds(middleIds.filter((_, j) => j !== i))
                        setMiddleAddrs(middleAddrs.filter((_, j) => j !== i))
                      }}
                    >
                      <XIcon />
                    </Button>
                  </div>
                  <HopAddressField
                    key={`middle-${i}-${id}`}
                    server={servers.find((s) => String(s.id) === id)}
                    value={middleAddrs[i] ?? ''}
                    onChange={(addr) => setMiddleAddr(i, addr)}
                  />
                </div>
              ))}
              {middleIds.length < 2 && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setMiddleIds([...middleIds, ''])
                    setMiddleAddrs([...middleAddrs, ''])
                  }}
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
                onValueChange={(v) => { setExitId(String(v)); setExitAddr('') }}
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
              <HopAddressField
                key={`exit-${exitId}`}
                server={servers.find((s) => String(s.id) === exitId)}
                value={exitAddr}
                onChange={setExitAddr}
              />
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
              {entryPortHint ? <p className="cg-chain-hint">{entryPortHint}</p> : null}
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

            <div className="space-y-2">
              <Label htmlFor="chain-traffic-multiplier">流量倍率</Label>
              <Input
                id="chain-traffic-multiplier"
                type="number"
                min="0.001"
                max="1000"
                step="0.001"
                value={trafficMultiplier}
                onChange={(event) => setTrafficMultiplier(event.target.value)}
                required
              />
            </div>

            {createError && <p className="cg-chain-error">{createError}</p>}
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
                {creating ? (editingChainId === null ? '创建中…' : '保存中…') : (editingChainId === null ? '创建' : '保存修改')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={trafficChain !== null} onOpenChange={(next) => !next && setTrafficChain(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{trafficChain?.name} · 流量历史</DialogTitle>
          </DialogHeader>
          {trafficChain ? (
            <div className="space-y-4">
              <div className="flex flex-wrap gap-2">
                <div className="cg-chain-range">
                  {(['day', 'month'] as const).map((range) => (
                    <Button
                      key={range}
                      type="button"
                      size="sm"
                      variant={trafficRange === range ? 'secondary' : 'ghost'}
                      onClick={() => {
                        setTrafficRange(range)
                        void loadTrafficHistory(trafficChain, trafficHopId, range)
                      }}
                    >
                      {range === 'day' ? '日' : '月'}
                    </Button>
                  ))}
                </div>
                <Select
                  value={String(trafficHopId)}
                  items={[
                    { value: '0', label: '链路（出口权威）' },
                    ...trafficChain.hops.map((hop) => ({
                      value: String(hop.id),
                      label: `${roleLabel[hop.role]} · ${hop.server_alias || `Server #${hop.server_id}`}`,
                    })),
                  ]}
                  onValueChange={(value) => {
                    const hopId = Number(value)
                    setTrafficHopId(hopId)
                    void loadTrafficHistory(trafficChain, hopId, trafficRange)
                  }}
                >
                  <SelectTrigger className="w-48">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">链路（出口权威）</SelectItem>
                    {trafficChain.hops.map((hop) => (
                      <SelectItem key={hop.id} value={String(hop.id)}>
                        {roleLabel[hop.role]} · {hop.server_alias || `Server #${hop.server_id}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {trafficLoading ? (
                <p className="cg-chain-dialog-note">加载中…</p>
              ) : displayedTrafficHistory.length === 0 ? (
                <p className="cg-chain-dialog-note">暂无流量记录</p>
              ) : (
                <TrafficHistoryChart buckets={displayedTrafficHistory} range={trafficRange} />
              )}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </Page>
  )
}
