import { useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { validateNameTemplate } from '@/lib/naming'
import { DEFAULT_REALITY_DEST, inferRealityDestPreset } from '@/lib/reality'
import type {
  Chain,
  ChainAutoPolicy,
  ChainHopCandidateInput,
  ChainHopInput,
  CreateChainRequest,
  CreateNodeRequest,
  EditChainRequest,
  ExternalExit,
  Server,
  XrayNode,
} from '@/lib/types'

import {
  MAX_HOPS,
  inboundCapable,
  isExternalServer,
  normalizePortInput,
  type CanvasNode,
  type ChainFormTopology,
} from './chain-wizard/chain-canvas'

/** 结果预览的一跳：端口为编辑器中所见值（null = 自动分配，部署后才生成真实端口）。 */
export interface ChainPreviewHop {
  server: Server
  role: 'entry' | 'middle' | 'exit'
  /** 未托管外部直链节点（只能作末位跳）。 */
  external: boolean
  externalExit: ExternalExit | null
  port: number | null
  candidateServers: Server[]
}

/** 结果预览的一条链路（由画布枚举出的路径 + 详情表单派生）。 */
export interface ChainPreviewPath {
  key: string
  servers: Server[]
  hops: ChainPreviewHop[]
  externalExit: ExternalExit | null
}

// 与后端 shared 包保持一致的协议/选项常量（出口节点协议表单复用 Nodes 向导的 vless+reality 字段）。
export const DIRECT_PROTOCOLS = [
  'vless',
  'vmess',
  'trojan',
  'shadowsocks',
  'hysteria',
  'socks',
  'http',
  'dokodemo-door',
] as const
export const RELAY_PROTOCOLS = [
  'vless',
  'vmess',
  'trojan',
  'shadowsocks',
  'hysteria',
  'socks',
  'http',
] as const
// 与后端 shared 包保持一致（顺序即 shared.Networks：tcp/grpc/xhttp 为 reality 兼容传输，
// ws/httpupgrade 支持 tls/none 安全层）。
export const NETWORKS = ['tcp', 'grpc', 'xhttp', 'ws', 'httpupgrade']
export const SS_METHODS = [
  { value: '2022-blake3-aes-128-gcm', label: '2022-blake3-aes-128-gcm（推荐）' },
  { value: '2022-blake3-aes-256-gcm', label: '2022-blake3-aes-256-gcm' },
  { value: '2022-blake3-chacha20-poly1305', label: '2022-blake3-chacha20-poly1305' },
  { value: 'aes-128-gcm', label: 'aes-128-gcm' },
  { value: 'aes-256-gcm', label: 'aes-256-gcm' },
  { value: 'chacha20-ietf-poly1305', label: 'chacha20-ietf-poly1305' },
]
export const VMESS_CIPHERS = [
  { value: 'auto', label: 'auto（推荐）' },
  { value: 'aes-128-gcm', label: 'aes-128-gcm' },
  { value: 'chacha20-poly1305', label: 'chacha20-poly1305' },
]
// 协议一句话定位（不懂协议的用户按此选择）。
export const PROTOCOL_LABELS: Record<string, string> = {
  vless: 'VLESS（推荐 · Reality 抗封锁最强）',
  vmess: 'VMess（兼容性广）',
  trojan: 'Trojan（兼容性广）',
  shadowsocks: 'Shadowsocks（轻量 · 特征明显）',
  hysteria: 'Hysteria2（UDP 高速）',
  socks: 'SOCKS5（明文 · 特殊用途）',
  http: 'HTTP（明文 · 特殊用途）',
  'dokodemo-door': '端口转发',
}
export const FINGERPRINTS = [
  'chrome',
  'firefox',
  'safari',
  'edge',
  'ios',
  'android',
  '360',
  'qq',
  'random',
  'randomized',
]
export const FLOWS = ['xtls-rprx-vision', 'none']
export const VLESS_ENCS = [
  { value: 'none', label: '无' },
  { value: 'mlkem768', label: 'mlkem768（后量子，推荐）' },
  { value: 'x25519', label: 'x25519' },
]
export const XHTTP_MODES = ['auto', 'packet-up', 'stream-up']

const REALITY_PROTOCOLS = ['vless', 'vmess', 'trojan']

/** ws/httpupgrade 为明文传输（security=none，无 reality 字段）。 */
export function isPlainNetwork(network: string): boolean {
  return network === 'ws' || network === 'httpupgrade'
}

// 与后端 shared 包保持一致（RealityNetworks）。
const REALITY_NETWORKS = ['tcp', 'grpc', 'xhttp']

/** 安全层可选项（镜像后端 normalize 矩阵）：trojan 不允许 none；reality 仅 tcp/grpc/xhttp。 */
export function securityOptions(protocol: string, network: string): string[] {
  const realityOK = REALITY_NETWORKS.includes(network)
  if (protocol === 'trojan') return realityOK ? ['reality', 'tls'] : ['tls']
  return realityOK ? ['reality', 'tls', 'none'] : ['tls', 'none']
}

/** 安全层纠偏：当前值不在可选项中时回退到首个（reality 兼容传输首选 reality，否则 tls 系首选 none/trojan 强制 tls）。 */
export function coerceSecurity(protocol: string, network: string, security: string): string {
  const options = securityOptions(protocol, network)
  if (options.includes(security)) return security
  return options.includes('reality') ? 'reality' : options.includes('none') ? 'none' : 'tls'
}

const chainNameAlphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'

function randomChainName(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(4))
  const suffix = [...bytes]
    .map((byte) => chainNameAlphabet[byte % chainNameAlphabet.length])
    .join('')
  return `Chain #${suffix}`
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function parseConfigRecord(value: unknown): Record<string, unknown> {
  const parsed: unknown = typeof value === 'string' ? JSON.parse(value) : value
  const record = asRecord(parsed)
  if (!record) throw new Error('config is not an object')
  return record
}

/** 从 xray 模板提取 reality 参数（出口 service_config 与入口 entry_config 回填共用）。 */
function parseRealityTemplate(template: Record<string, unknown>): {
  shortId: string
  dest: string
  serverNames: string[]
} {
  const streamSettings = asRecord(template.streamSettings) ?? {}
  const reality = asRecord(streamSettings.realitySettings) ?? {}
  const shortIds = Array.isArray(reality.shortIds) ? reality.shortIds : []
  const configuredServerNames = Array.isArray(reality.serverNames)
    ? reality.serverNames.filter((value): value is string => typeof value === 'string')
    : []
  const configuredDest = String(reality.dest || `${DEFAULT_REALITY_DEST}:443`)
  return {
    shortId: typeof shortIds[0] === 'string' ? shortIds[0] : '',
    dest: configuredDest,
    serverNames: configuredServerNames.length > 0 ? configuredServerNames : [DEFAULT_REALITY_DEST],
  }
}

/** 表单只承载协议/细节字段；拓扑（服务器序列、端口、地址）由向导画布注入。 */
export interface ChainFormState {
  name: string
  protocol: string
  shortId: string
  destPreset: string
  dest: string
  serverNames: string
  fingerprint: string
  network: string
  path: string
  mode: string
  host: string
  flow: string
  encryption: string
  security: string
  certMode: string
  tlsDomain: string
  serviceName: string
  method: string
  cipher: string
  obfsPassword: string
  upMbps: string
  downMbps: string
  portHop: string // ''=默认开（自动分配 32 段）；'off'=关闭；'a-b'=显式段
  entryProtocolEnabled: boolean
  entryShortId: string
  entryDestPreset: string
  entryDest: string
  entryServerNames: string
  entryFingerprint: string
  targetAddress: string
  targetPort: string
  trafficMultiplier: string
  // 自动编排策略（§链路自动编排）
  autoEnabled: boolean
  onBan: boolean
  banTriggerLevel: 'suspected' | 'banned'
  onUnreachable: boolean
  unreachableStreak: string
  onQuotaExhausted: boolean
  onAgentOffline: boolean
  offlineTimeoutSec: string
  failbackMode: 'none' | 'preemptive'
  cooldownSec: string
}

const initialChainForm: ChainFormState = {
  name: '',
  protocol: 'vless',
  shortId: '',
  destPreset: DEFAULT_REALITY_DEST,
  dest: 'dl.google.com:443',
  serverNames: 'dl.google.com',
  fingerprint: 'chrome',
  network: 'tcp',
  path: '/',
  mode: 'auto',
  host: '',
  flow: 'xtls-rprx-vision',
  encryption: 'none',
  security: 'reality',
  certMode: 'selfsign',
  tlsDomain: '',
  serviceName: 'grpc',
  method: '2022-blake3-aes-128-gcm',
  cipher: 'auto',
  obfsPassword: '',
  upMbps: '50',
  downMbps: '100',
  portHop: '',
  entryProtocolEnabled: false,
  entryShortId: '',
  entryDestPreset: DEFAULT_REALITY_DEST,
  entryDest: 'dl.google.com:443',
  entryServerNames: 'dl.google.com',
  entryFingerprint: 'chrome',
  targetAddress: '',
  targetPort: '',
  trafficMultiplier: '1.000',
  autoEnabled: false,
  onBan: true,
  banTriggerLevel: 'banned',
  onUnreachable: true,
  unreachableStreak: '3',
  onQuotaExhausted: true,
  onAgentOffline: true,
  offlineTimeoutSec: '180',
  failbackMode: 'none',
  cooldownSec: '300',
}

function parseAutoPolicyDetails(chain: Chain): Partial<ChainFormState> {
  const pol = chain.auto_policy
  if (!pol) return {}
  return {
    autoEnabled: Boolean(pol.auto_enabled),
    onBan: Boolean(pol.on_ban),
    banTriggerLevel: pol.ban_trigger_level === 'suspected' ? 'suspected' : 'banned',
    onUnreachable: Boolean(pol.on_unreachable),
    unreachableStreak: String(pol.unreachable_streak || 3),
    onQuotaExhausted: Boolean(pol.on_quota_exhausted),
    onAgentOffline: Boolean(pol.on_agent_offline),
    offlineTimeoutSec: String(pol.offline_timeout_sec || 180),
    failbackMode: pol.failback_mode === 'preemptive' ? 'preemptive' : 'none',
    cooldownSec: String(pol.cooldown_sec || 300),
  }
}

/** 编辑模式回填解析：出口配置 → 细节表单字段 + 出口节点监听端口（画布末节点端口回填用）。 */
export interface ChainEditParse {
  details: Partial<ChainFormState>
  exitPort: string
}

/** 入口区块 reality 参数回填（entry_config 缺失/解析失败返回 null，退回默认值）。 */
function parseEntryReality(chain: Chain): ReturnType<typeof parseRealityTemplate> | null {
  if (!chain.entry_config) return null
  try {
    const entryTemplate =
      chain.entry_config.template === undefined
        ? {}
        : parseConfigRecord(chain.entry_config.template)
    return parseRealityTemplate(entryTemplate)
  } catch {
    return null
  }
}

/**
 * 外部直链出口链的编辑回填：没有出口业务节点（service_node_id=0）与 service_config；
 * 入口服务器 A 与普通链路一样可配置协议细节，因此从 entry_config（入口共享端点配置）
 * 回填传输/安全层/证书/flow/encryption 与 Reality 子参数；出口细节保持默认即可。
 */
function parseExternalExitEdit(chain: Chain): { ok: true; value: ChainEditParse } {
  const entry = chain.entry_config
  const entryReality = parseEntryReality(chain)
  const network = String(entry?.network || 'tcp')
  const security = String(entry?.security || (isPlainNetwork(network) ? 'none' : 'reality'))
  const shortId = entryReality?.shortId ?? ''
  const dest = entryReality?.dest ?? DEFAULT_REALITY_DEST
  const serverNames = entryReality?.serverNames ?? ['dl.google.com']
  const destPreset = inferRealityDestPreset(dest, serverNames)
  return {
    ok: true,
    value: {
      // 外部出口端口来自链接，画布末位不回填出口节点端口。
      exitPort: '',
      details: {
        name: chain.name,
        protocol: 'vless',
        network,
        security,
        certMode: String(entry?.cert_mode || 'selfsign'),
        tlsDomain: String(entry?.tls_domain || ''),
        fingerprint: String(entry?.fingerprint || 'chrome'),
        flow: String(
          entry?.flow ||
            (network === 'tcp' && security === 'reality' ? 'xtls-rprx-vision' : 'none'),
        ),
        encryption: String(entry?.encryption || 'none'),
        serviceName: String(entry?.service_name || 'grpc'),
        path: String(entry?.path || '/'),
        mode: String(entry?.mode || 'auto'),
        host: String(entry?.host || ''),
        shortId,
        destPreset,
        dest,
        serverNames: serverNames.join(','),
        // 兼容入口协议区块字段（外部直链出口的入口协议已由主表单承载）。
        entryProtocolEnabled: true,
        entryShortId: shortId,
        entryDestPreset: destPreset,
        entryDest: dest,
        entryServerNames: serverNames.join(','),
        entryFingerprint: String(entry?.fingerprint || 'chrome'),
        trafficMultiplier: chain.traffic_multiplier || '1.000',
        ...parseAutoPolicyDetails(chain),
      },
    },
  }
}

export function parseChainEdit(
  chain: Chain,
  nodes: XrayNode[],
): { ok: true; value: ChainEditParse } | { ok: false; error: string } {
  // 外部直链出口没有出口业务节点/ service_config，走外部出口分支（不能按出口配置不存在报错）。
  if (chain.service_external) return parseExternalExitEdit(chain)
  const service = nodes.find((node) => node.id === chain.service_node_id)
  if (!service && !chain.service_config) {
    return { ok: false, error: '链路出口配置不存在' }
  }
  let virtual: Record<string, unknown>
  let template: Record<string, unknown>
  let settings: Record<string, unknown>
  try {
    const rawVirtual: unknown = service?.config_template ?? chain.service_config
    virtual = parseConfigRecord(rawVirtual)
    template = virtual.template === undefined ? {} : parseConfigRecord(virtual.template)
    settings = asRecord(template.settings) ?? {}
  } catch {
    return { ok: false, error: '链路出口配置无法解析' }
  }

  const mainReality = parseRealityTemplate(template)
  // 入口协议区块回填（P4）：vless 出口链的 entry_config 即主协议端点，不算独立入口区块；
  // 其余协议存在 entry_config 时勾选回填，子参数解析失败退回默认（留空自动生成）。
  const hasEntryBlock = chain.entry_config != null && String(virtual.protocol ?? '') !== 'vless'
  const entryReality = hasEntryBlock ? parseEntryReality(chain) : null
  return {
    ok: true,
    value: {
      exitPort: virtual.port ? String(virtual.port) : '',
      details: {
        name: chain.name,
        protocol: String(virtual.protocol ?? service?.protocol ?? 'vless'),
        network: String(virtual.network || 'tcp'),
        security: String(
          virtual.security ||
            (isPlainNetwork(String(virtual.network || 'tcp')) ? 'none' : 'reality'),
        ),
        certMode: String(virtual.cert_mode || 'selfsign'),
        tlsDomain: String(virtual.tls_domain || ''),
        fingerprint: String(virtual.fingerprint || 'chrome'),
        flow: String(virtual.flow || 'none'),
        encryption: String(virtual.encryption || 'none'),
        serviceName: String(virtual.service_name || 'grpc'),
        method: String(virtual.method || '2022-blake3-aes-128-gcm'),
        cipher: String(virtual.cipher || 'auto'),
        obfsPassword: String(virtual.obfs_password || ''),
        upMbps: virtual.up_mbps ? String(virtual.up_mbps) : '50',
        downMbps: virtual.down_mbps ? String(virtual.down_mbps) : '100',
        portHop: String(virtual.port_hop || ''),
        entryProtocolEnabled: hasEntryBlock,
        entryShortId: entryReality?.shortId ?? '',
        entryDestPreset: entryReality
          ? inferRealityDestPreset(entryReality.dest, entryReality.serverNames)
          : DEFAULT_REALITY_DEST,
        entryDest: entryReality?.dest ?? 'dl.google.com:443',
        entryServerNames: entryReality?.serverNames.join(',') ?? 'dl.google.com',
        entryFingerprint: String(chain.entry_config?.fingerprint || 'chrome'),
        path: String(virtual.path || '/'),
        mode: String(virtual.mode || 'auto'),
        host: String(virtual.host || ''),
        shortId: mainReality.shortId,
        destPreset: inferRealityDestPreset(mainReality.dest, mainReality.serverNames),
        dest: mainReality.dest,
        serverNames: mainReality.serverNames.join(','),
        targetAddress: String(settings.address || ''),
        targetPort: settings.port ? String(settings.port) : '',
        trafficMultiplier: chain.traffic_multiplier || '1.000',
        ...parseAutoPolicyDetails(chain),
      },
    },
  }
}

/** 单条路径的提交载荷（create/edit 共用；外部出口路径的 node 为占位节点）。 */
interface ChainPayload {
  hops: ChainHopInput[]
  entry: ChainHopInput
  middle: ChainHopInput[]
  exit: ChainHopInput
  node: CreateNodeRequest
  entry_port?: number
  entry_node?: EditChainRequest['entry_node']
  auto_policy?: Partial<ChainAutoPolicy>
}

/**
 * 链路向导第二步（协议/细节）的表单状态与提交逻辑。
 * 拓扑由第一步画布推导后经 topology/paths 注入，本 hook 不再持有服务器选择状态。
 */
export function useChainForm({
  chains,
  servers,
  topology,
  paths,
  onError,
  onSaved,
  showOperation,
}: {
  chains: Chain[]
  servers: Server[]
  /** 第一条合法链路的拓扑（名称模板/端口提示基准）。 */
  topology: ChainFormTopology
  /** 画布校验枚举出的全部合法链路：每条简单路径创建一条链路。 */
  paths: CanvasNode[][]
  onError: (message: string) => void
  onSaved: () => void
  showOperation: (opts: { observeId: string }) => void
}) {
  const [editingChainId, setEditingChainId] = useState<number | null>(null)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [form, setForm] = useState<ChainFormState>(initialChainForm)
  // 终审修复 I-2：无入口端点的存量链不能补勾入口协议区块（后端同样 400，镜像文案）。
  const [entryBlockLocked, setEntryBlockLocked] = useState(false)

  const patch = (partial: Partial<ChainFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  // 全部路径的末位都是外部直链节点（外部直链出口链）：此时协议表单承载入口服务器 A 的
  // VLESS 参数（入口共享端点），协议固定 VLESS——与普通链路一样可配传输/安全层/证书。
  const exitIsExternal =
    paths.length > 0 &&
    paths.every((path) => {
      const exitNode = path[path.length - 1]
      const server = exitNode
        ? servers.find((candidate) => candidate.id === exitNode.serverId)
        : undefined
      return server !== undefined && isExternalServer(server)
    })
  const effectiveProtocol = exitIsExternal ? 'vless' : form.protocol
  const isReality = REALITY_PROTOCOLS.includes(effectiveProtocol)
  const isHy2 = effectiveProtocol === 'hysteria'
  const { chainType, entryId, middleIds, exitId, entryPort } = topology
  const selectedEntry = servers.find((s) => String(s.id) === entryId)
  const selectedExit = servers.find((s) => String(s.id) === exitId)
  // 多链/外部出口：按每条路径的末位服务器聚合出口形态，并为结果预览准备好每一跳的
  // 服务器、角色与「编辑器中所见」的端口（自动端口显示为自动，落地后才生成真实端口）。
  const chainPaths: ChainPreviewPath[] = paths.map((path) => {
    const pathServers: Server[] = []
    const hops: ChainPreviewHop[] = []
    path.forEach((node, index) => {
      const server = servers.find((candidate) => candidate.id === node.serverId)
      if (!server) return
      pathServers.push(server)
      const role: 'entry' | 'middle' | 'exit' =
        path.length === 1
          ? 'exit'
          : index === 0
            ? 'entry'
            : index === path.length - 1
              ? 'exit'
              : 'middle'
      const external = isExternalServer(server)
      const externalExit = external ? (server.external_exit ?? null) : null
      const typedPort = Number(normalizePortInput(node.port))
      const candidateServers = (node.candidates ?? []).flatMap((cand) => {
        const s = servers.find((c) => c.id === cand.serverId)
        return s ? [s] : []
      })
      hops.push({
        server,
        role,
        external,
        externalExit,
        // 外部节点端口来自链接；中间跳由后端自动分配；首/末跳取画布输入（空 = 自动）。
        port: externalExit?.port ?? (role === 'middle' ? null : typedPort > 0 ? typedPort : null),
        candidateServers,
      })
    })
    const exitServer = pathServers[pathServers.length - 1]
    return {
      key: path.map((node) => node.id).join('>'),
      servers: pathServers,
      hops,
      externalExit:
        exitServer && isExternalServer(exitServer) ? (exitServer.external_exit ?? null) : null,
    }
  })
  // 结果预览名称：与提交时的派生规则一致（基准名 + 序号；留空显示自动生成）。
  const previewBaseName = form.name.trim()
  const chainPreviewNames = paths.map((_, index) => {
    if (!previewBaseName) return paths.length > 1 ? `自动生成-${index + 1}` : '自动生成'
    return paths.length > 1 ? `${previewBaseName}-${index + 1}` : previewBaseName
  })
  // 全部路径的末位都是外部直链节点时，详情步骤隐藏出口协议表单（协议取外部规格）。
  const externalExits: ExternalExit[] = []
  let exitConfigMissing = false
  for (const entry of chainPaths) {
    if (!entry.externalExit) {
      const exitServer = entry.servers[entry.servers.length - 1]
      if (exitServer && isExternalServer(exitServer)) exitConfigMissing = true
      continue
    }
    const key = JSON.stringify(entry.externalExit)
    if (!externalExits.some((exit) => JSON.stringify(exit) === key)) {
      externalExits.push(entry.externalExit)
    }
  }
  const externalExit: ExternalExit | null = externalExits[0] ?? null
  // 入口共享端点预览：vless 出口 / 勾选入口协议区块的 hy2 / 外部直链出口链都会落到
  // 入口共享监听（客户端以 VLESS+Reality 接入），预览据此在入口跳打「共享入口」标。
  const entrySharedPreview =
    !entryBlockLocked &&
    (exitIsExternal || form.protocol === 'vless' || (isHy2 && form.entryProtocolEnabled))
  const selectedMiddleServers = middleIds.flatMap((id) => {
    const server = servers.find((candidate) => String(candidate.id) === id)
    return server ? [server] : []
  })
  const topologyServers = [
    ...(selectedEntry ? [selectedEntry] : []),
    ...selectedMiddleServers,
    ...(chainType === 'relay' && selectedExit ? [selectedExit] : []),
  ]
  const hopIndexes = chainType === 'relay' ? selectedMiddleServers.map((_, index) => index + 1) : []
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
  const strictNameResult = validateNameTemplate(form.name, {
    servers: topologyServers,
    protocol: form.protocol,
    port: entryPort,
    hopIndexes,
  })

  const beginCreate = () => {
    setEditingChainId(null)
    setForm(initialChainForm)
    setCreateError('')
    setEntryBlockLocked(false)
  }

  /** 进入编辑：解析结果由向导页经 parseChainEdit 预先取得（顺带回填画布）。 */
  const beginEdit = (chain: Chain, parsed: ChainEditParse) => {
    setEditingChainId(chain.id)
    // 无入口端点的存量链锁定入口区块勾选（v1 不支持编辑新增；取消勾选不受影响）。
    setEntryBlockLocked(chain.endpoint_id === 0)
    setForm({ ...initialChainForm, ...parsed.details })
    setCreateError('')
    onError('')
  }

  // 外部直链出口链的表单承载入口 A 的 VLESS 细节，联动照常生效（协议固定 VLESS）。
  const onNetworkChange = (value: string | null) => {
    if (!value) return
    setForm((current) => ({
      ...current,
      network: value,
      // 跨传输纠偏：vision flow 仅 tcp；security 按矩阵即时纠正（清理 #3）
      flow: value === 'tcp' ? current.flow : 'none',
      security: coerceSecurity(effectiveProtocol, value, current.security),
      // vless 明文传输必须有 VLESS Encryption 兜底（后端矩阵，前端即时纠正）
      encryption:
        effectiveProtocol === 'vless' && isPlainNetwork(value) && current.encryption === 'none'
          ? 'mlkem768'
          : current.encryption,
    }))
  }

  const onSecurityChange = (value: string | null) => {
    if (!value) return
    setForm((current) => ({
      ...current,
      security: value,
      // vision flow 与安全层 none 不兼容（后端矩阵，前端即时纠正）；reality/tls 不动用户已选 flow
      flow: value === 'none' && current.flow !== 'none' ? 'none' : current.flow,
    }))
  }

  const onProtocolChange = (value: string | null) => {
    if (!value || exitIsExternal) return
    setForm((current) => ({
      ...current,
      protocol: value,
      // 跨协议纠偏：flow/encryption 仅 vless 有意义；security 按矩阵即时纠正（清理 #3）
      // hy2 无 network/security 概念：选择后隐藏对应选择器（提交载荷不携带）。
      flow: value === 'vless' ? current.flow : 'none',
      security: coerceSecurity(value, current.network, current.security),
      encryption:
        value === 'vless'
          ? isPlainNetwork(current.network) && current.encryption === 'none'
            ? 'mlkem768'
            : current.encryption
          : 'none',
    }))
  }

  /** 把当前表单的协议细节写入节点载荷（protocol 显式传入：外部出口链用它构建入口 VLESS 端点）。 */
  const applyProtocolDetails = (body: CreateNodeRequest, protocol: string): CreateNodeRequest => {
    if (protocol === 'hysteria') {
      // hy2 恒 QUIC+TLS：证书模式复用 TLS 区域；矩阵外字段一律不提交（后端 400 兜底）。
      body.cert_mode = form.certMode
      if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
        body.tls_domain = form.tlsDomain.trim()
      }
      if (form.obfsPassword.trim()) {
        body.obfs_password = form.obfsPassword.trim()
      }
      if (form.upMbps.trim()) {
        body.up_mbps = Number(form.upMbps)
      }
      if (form.downMbps.trim()) {
        body.down_mbps = Number(form.downMbps)
      }
      if (form.portHop === 'off' || form.portHop.trim()) {
        body.port_hop = form.portHop.trim() // 'off' 或 'a-b'；'' = 默认开（后端自动分配）
      }
      return body
    }
    if (REALITY_PROTOCOLS.includes(protocol)) {
      body.network = form.network
      body.security = form.security
      if (form.network === 'ws' || form.network === 'httpupgrade') {
        body.path = form.path.trim() || '/'
        if (form.host.trim()) {
          body.host = form.host.trim()
        }
      }
      if (form.network === 'xhttp') {
        body.path = form.path.trim() || '/'
        body.mode = form.mode
        if (form.host.trim()) {
          body.host = form.host.trim()
        }
      }
      if (form.network === 'grpc') {
        body.service_name = form.serviceName.trim() || 'grpc'
      }
      if (form.security === 'reality') {
        body.fingerprint = form.fingerprint
        if (form.shortId.trim()) {
          body.short_id = form.shortId.trim()
        }
        if (form.dest.trim()) {
          body.dest = form.dest.trim()
        }
        const names = form.serverNames
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
        if (names.length > 0) {
          body.server_names = names
        }
      }
      if (form.security === 'tls') {
        body.fingerprint = form.fingerprint
        body.cert_mode = form.certMode
        // acme 的 tls_domain 由后端从落地服务器地址检测填充（前端不传）
        if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
          body.tls_domain = form.tlsDomain.trim()
        }
      }
      if (protocol === 'vless') {
        // vision 仅 tcp；vision + Encryption 允许组合（§15）；tls 下 vision 同样合法（§2）
        body.flow = form.network === 'tcp' ? form.flow : 'none'
        if (form.encryption !== 'none') {
          body.encryption = form.encryption
        }
      }
      return body
    }
    if (protocol === 'shadowsocks') {
      body.method = form.method
    }
    if (protocol === 'vmess') {
      body.cipher = form.cipher
    }
    if (protocol === 'dokodemo-door') {
      body.target_address = form.targetAddress.trim()
      body.target_port = Number(form.targetPort)
    }
    return body
  }

  /** 入口协议区块（托管出口链 / 混合图的外部路径）：v1 固定 VLESS+Reality 子参数。 */
  const entryBlockPayload = (): NonNullable<EditChainRequest['entry_node']> => {
    const entryNode: NonNullable<EditChainRequest['entry_node']> = {
      protocol: 'vless',
      security: 'reality',
    }
    if (form.entryShortId.trim()) entryNode.short_id = form.entryShortId.trim()
    if (form.entryDest.trim()) entryNode.dest = form.entryDest.trim()
    const entryNames = form.entryServerNames
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    if (entryNames.length > 0) entryNode.server_names = entryNames
    entryNode.fingerprint = form.entryFingerprint
    return entryNode
  }

  /** 单条路径 → 提交载荷（多链共享同一套协议参数；外部出口路径提交最小占位 node）。 */
  const buildChainPayload = (path: CanvasNode[], name: string): ChainPayload => {
    const first = path[0]
    const last = path[path.length - 1]
    const relay = path.length > 1
    const exitNode = relay ? last : first
    const exitServer = servers.find((server) => server.id === exitNode.serverId)
    const exitExternal =
      exitServer && isExternalServer(exitServer) ? exitServer.external_exit : undefined
    // 外部直链出口：后端忽略 node 表单（协议/凭据取外部规格），只提交最小占位节点。
    const nodeBody: CreateNodeRequest = exitExternal
      ? { name, server_id: exitNode.serverId, protocol: 'vless' }
      : applyProtocolDetails(
          { name, server_id: exitNode.serverId, protocol: form.protocol },
          form.protocol,
        )
    const entryPortValue = normalizePortInput(first.port)
    if (!exitExternal) {
      // 直连 = 入口端口复用为节点端口；中转 = 末位出口节点端口。
      const exitPortValue = relay ? normalizePortInput(last.port) : entryPortValue
      if (exitPortValue) nodeBody.port = Number(exitPortValue)
    }
    // 入口协议：外部直链出口恒挂入口共享端点，全部出口为外部时入口服务器 A 与普通链路
    // 一样使用本表单的完整 VLESS 细节（传输/安全层/证书/reality 子参数）；混合图沿用
    // 入口协议区块子参数（v1）。托管出口链保持原有入口区块行为。
    let entryNode: EditChainRequest['entry_node']
    if (relay && exitExternal !== undefined) {
      entryNode = exitIsExternal
        ? applyProtocolDetails({ name, server_id: exitNode.serverId, protocol: 'vless' }, 'vless')
        : entryBlockPayload()
    } else if (
      relay &&
      (isHy2 || form.protocol === 'vless') &&
      form.entryProtocolEnabled &&
      !entryBlockLocked
    ) {
      entryNode = entryBlockPayload()
    }
    // 逐跳地址与备选池：空串 = 跟随服务器默认地址，提交时不携带 address 字段；无备选时不携带 candidates。
    const mkHop = (node: CanvasNode): ChainHopInput => {
      const candidates: ChainHopCandidateInput[] = (node.candidates ?? []).map((cand, idx) => ({
        server_id: cand.serverId,
        priority: idx + 1,
        ...(cand.address ? { address: cand.address } : {}),
        ...(cand.port ? { port: Number(cand.port) } : {}),
        enabled: true,
      }))
      return {
        server_id: node.serverId,
        ...(node.address ? { address: node.address } : {}),
        ...(candidates.length > 0 ? { candidates } : {}),
      }
    }
    const hasAnyCandidates = path.some((n) => (n.candidates?.length ?? 0) > 0)
    const autoPolicy: Partial<ChainAutoPolicy> | undefined =
      form.autoEnabled || hasAnyCandidates || editingChainId !== null
        ? {
            auto_enabled: form.autoEnabled,
            on_ban: form.onBan,
            ban_trigger_level: form.banTriggerLevel,
            on_unreachable: form.onUnreachable,
            unreachable_streak: Math.max(1, Number(form.unreachableStreak) || 3),
            on_quota_exhausted: form.onQuotaExhausted,
            on_agent_offline: form.onAgentOffline,
            offline_timeout_sec: Math.max(30, Number(form.offlineTimeoutSec) || 180),
            failback_mode: form.failbackMode,
            cooldown_sec: Math.max(30, Number(form.cooldownSec) || 300),
          }
        : undefined
    return {
      hops: path.map(mkHop),
      entry: mkHop(first),
      middle: path.slice(1, -1).map(mkHop),
      exit: mkHop(exitNode),
      node: nodeBody,
      entry_port: entryPortValue ? Number(entryPortValue) : undefined,
      entry_node: entryNode,
      auto_policy: autoPolicy,
    }
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    if (form.name.trim() && strictNameResult.error) {
      setCreateError(strictNameResult.error)
      return
    }
    if (paths.length === 0) {
      setCreateError('没有可创建的链路，请回第一步画布补全拓扑')
      return
    }
    // 编辑模式只维护既有单链：多路径请新建链路。
    if (editingChainId !== null && paths.length > 1) {
      setCreateError('编辑模式仅支持单条链路，多路径请新建链路')
      return
    }
    // 画布校验的提交前兜底（镜像后端 400 文案）。
    for (const path of paths) {
      if (path.length > MAX_HOPS) {
        setCreateError(`链长上限 ${MAX_HOPS} 跳（入口 + 中间跳 ≤2 + 出口）`)
        return
      }
      for (let index = 0; index < path.length; index += 1) {
        const node = path[index]
        const srv = servers.find((candidate) => candidate.id === node.serverId)
        // 服务器缺失由画布校验拦截；此处只做已知服务器的形态兜底。
        if (!srv) continue
        if (isExternalServer(srv)) {
          if (index !== path.length - 1 || path.length === 1) {
            setCreateError(`未托管服务器 ${srv.alias} 只能作为链路的最后一跳（出口）`)
            return
          }
          if (!srv.external_exit) {
            setCreateError(`未托管服务器 ${srv.alias} 的外部直链配置损坏，请重新保存该服务器`)
            return
          }
          continue
        }
        if ((index === 0 || index < path.length - 1) && !inboundCapable(srv)) {
          setCreateError(`服务器 ${srv.alias} 无入站能力（仅出口档 NAT），不能作入口/中间跳`)
          return
        }
      }
    }
    // 出口节点表单只对托管出口链路生效：先做一次性校验，避免创建到一半才失败。
    const hasManagedExit = paths.some((path) => {
      const exitNode = path[path.length - 1]
      const srv = servers.find((server) => server.id === exitNode.serverId)
      return srv !== undefined && !isExternalServer(srv)
    })
    if (hasManagedExit && isReality) {
      if (form.security === 'none' && form.protocol === 'vless' && form.encryption === 'none') {
        setCreateError('vless 在安全层 none 下必须启用 VLESS Encryption')
        return
      }
    }
    if (hasManagedExit && form.protocol === 'dokodemo-door') {
      if (!form.targetAddress.trim() || !form.targetPort.trim()) {
        setCreateError('dokodemo-door 需要目标地址与目标端口')
        return
      }
    }
    // N>1 时每条链路名称 = 基准-1、基准-2……；基准为空时沿用随机名再拼序号。
    const baseName = form.name.trim() || randomChainName()
    const names =
      paths.length > 1 ? paths.map((_, index) => `${baseName}-${index + 1}`) : [baseName]
    const payloads = paths.map((path, index) => buildChainPayload(path, names[index]))
    setCreating(true)
    try {
      // 后端一次只接受一条链路：按预览顺序逐条入队，逐个展示观察进度。
      for (let index = 0; index < payloads.length; index += 1) {
        const payload = payloads[index]
        if (editingChainId !== null) {
          const body: EditChainRequest = {
            chain_id: editingChainId,
            name: names[index],
            hops: payload.hops,
            node: payload.node,
            traffic_multiplier: form.trafficMultiplier,
            auto_policy: payload.auto_policy,
          }
          if (payload.entry_port !== undefined) body.entry_port = payload.entry_port
          if (payload.entry_node) body.entry_node = payload.entry_node
          const { observeId } = await api.editChain(body)
          if (observeId) showOperation({ observeId })
        } else {
          const body: CreateChainRequest = {
            name: names[index],
            hops: payload.hops,
            entry: payload.entry,
            middle: payload.middle,
            exit: payload.exit,
            node: payload.node,
            traffic_multiplier: form.trafficMultiplier,
            auto_policy: payload.auto_policy,
          }
          if (payload.entry_port !== undefined) body.entry_port = payload.entry_port
          if (payload.entry_node) body.entry_node = payload.entry_node
          const { observeId } = await api.createChain(body)
          if (observeId) showOperation({ observeId })
        }
      }
      onSaved()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  return {
    editingChainId,
    creating,
    createError,
    form,
    patch,
    isReality,
    isHy2,
    // 有效协议：全部出口为外部直链时固定 vless（表单承载入口 A 的 VLESS 细节）。
    effectiveProtocol,
    topologyServers,
    hopIndexes,
    entryPortHint,
    entryBlockLocked,
    // 多链：chainCount/chainPaths 供细节页展示将创建的链路列表；
    // 外部直链出口：exitIsExternal 供细节页隐藏出口表单，externalExit(s) 提供只读摘要；
    // entryBlockLocked 对这类链不适用（入口端点恒存在，entry_node 恒提交）。
    chainCount: paths.length,
    chainPaths,
    chainPreviewNames,
    entrySharedPreview,
    exitIsExternal,
    exitConfigMissing,
    externalExit,
    externalExits,
    strictNameResult,
    beginCreate,
    beginEdit,
    onNetworkChange,
    onSecurityChange,
    onProtocolChange,
    onSubmit,
  }
}

export type ChainFormController = ReturnType<typeof useChainForm>
