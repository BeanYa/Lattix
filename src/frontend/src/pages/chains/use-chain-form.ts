import { useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { validateNameTemplate } from '@/lib/naming'
import { DEFAULT_REALITY_DEST, inferRealityDestPreset } from '@/lib/reality'
import type {
  Chain,
  ChainHopInput,
  CreateChainRequest,
  CreateNodeRequest,
  EditChainRequest,
  Server,
  XrayNode,
} from '@/lib/types'

import { MAX_HOPS, inboundCapable, type ChainFormTopology } from './chain-wizard/chain-canvas'

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
}

/** 编辑模式回填解析：出口配置 → 细节表单字段 + 出口节点监听端口（画布末节点端口回填用）。 */
export interface ChainEditParse {
  details: Partial<ChainFormState>
  exitPort: string
}

export function parseChainEdit(
  chain: Chain,
  nodes: XrayNode[],
): { ok: true; value: ChainEditParse } | { ok: false; error: string } {
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
  let entryReality: ReturnType<typeof parseRealityTemplate> | null = null
  if (hasEntryBlock) {
    try {
      const entryTemplate =
        chain.entry_config!.template === undefined
          ? {}
          : parseConfigRecord(chain.entry_config!.template)
      entryReality = parseRealityTemplate(entryTemplate)
    } catch {
      entryReality = null
    }
  }
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
      },
    },
  }
}

/**
 * 链路向导第二步（协议/细节）的表单状态与提交逻辑。
 * 拓扑由第一步画布推导后经 topology 注入，本 hook 不再持有服务器选择状态。
 */
export function useChainForm({
  chains,
  servers,
  topology,
  onError,
  onSaved,
  showOperation,
}: {
  chains: Chain[]
  servers: Server[]
  topology: ChainFormTopology
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

  const isReality = REALITY_PROTOCOLS.includes(form.protocol)
  const isHy2 = form.protocol === 'hysteria'
  const { chainType, entryId, middleIds, exitId, entryPort } = topology
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

  const onNetworkChange = (value: string | null) => {
    if (!value) return
    setForm((current) => ({
      ...current,
      network: value,
      // 跨传输纠偏：vision flow 仅 tcp；security 按矩阵即时纠正（清理 #3）
      flow: value === 'tcp' ? current.flow : 'none',
      security: coerceSecurity(current.protocol, value, current.security),
      // vless 明文传输必须有 VLESS Encryption 兜底（后端矩阵，前端即时纠正）
      encryption:
        current.protocol === 'vless' && isPlainNetwork(value) && current.encryption === 'none'
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
    if (!value) return
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

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const resolvedName = form.name.trim() || randomChainName()
    if (form.name.trim() && strictNameResult.error) {
      setCreateError(strictNameResult.error)
      return
    }
    if (!entryId || (chainType === 'relay' && (!exitId || middleIds.some((m) => !m)))) {
      setCreateError('链路拓扑不完整，请回第一步画布补全')
      return
    }
    const hopIds = chainType === 'direct' ? [entryId] : [entryId, ...middleIds, exitId]
    if (hopIds.length > MAX_HOPS) {
      setCreateError(`链长上限 ${MAX_HOPS} 跳（入口 + 中间跳 ≤2 + 出口）`)
      return
    }
    if (new Set(hopIds).size !== hopIds.length) {
      setCreateError('同一服务器在一条链中不重复')
      return
    }
    // 直连唯一服务器、或中转的入口与中转跳必须有入站能力；中转出口任意（画布校验的提交前兜底）。
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
      protocol: form.protocol,
    }
    const exitPort = topology.port
    if ((chainType === 'direct' ? entryPort : exitPort).trim()) {
      nodeBody.port = Number(chainType === 'direct' ? entryPort : exitPort)
    }
    // 入口协议区块（P4，v1 固定 vless+reality，子参数留空自动生成）；仅多跳链可勾选。
    // 存量无端点链锁定勾选（终审修复 I-2）：锁定时即使状态被绕过也不携带 entry_node。
    let entryNode: EditChainRequest['entry_node']
    if (form.protocol === 'hysteria') {
      // hy2 恒 QUIC+TLS：证书模式复用 TLS 区域；矩阵外字段一律不提交（后端 400 兜底）。
      nodeBody.cert_mode = form.certMode
      if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
        nodeBody.tls_domain = form.tlsDomain.trim()
      }
      if (form.obfsPassword.trim()) {
        nodeBody.obfs_password = form.obfsPassword.trim()
      }
      if (form.upMbps.trim()) {
        nodeBody.up_mbps = Number(form.upMbps)
      }
      if (form.downMbps.trim()) {
        nodeBody.down_mbps = Number(form.downMbps)
      }
      if (form.portHop === 'off' || form.portHop.trim()) {
        nodeBody.port_hop = form.portHop.trim() // 'off' 或 'a-b'；'' = 默认开（后端自动分配）
      }
    }
    if (
      (isHy2 || form.protocol === 'vless') &&
      chainType === 'relay' &&
      form.entryProtocolEnabled &&
      !entryBlockLocked
    ) {
      entryNode = { protocol: 'vless', security: 'reality' }
      if (form.entryShortId.trim()) entryNode.short_id = form.entryShortId.trim()
      if (form.entryDest.trim()) entryNode.dest = form.entryDest.trim()
      const entryNames = form.entryServerNames
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      if (entryNames.length > 0) entryNode.server_names = entryNames
      entryNode.fingerprint = form.entryFingerprint
    }
    if (isReality) {
      nodeBody.network = form.network
      nodeBody.security = form.security
      if (form.network === 'ws' || form.network === 'httpupgrade') {
        nodeBody.path = form.path.trim() || '/'
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
        }
      }
      if (form.network === 'xhttp') {
        nodeBody.path = form.path.trim() || '/'
        nodeBody.mode = form.mode
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
        }
      }
      if (form.network === 'grpc') {
        nodeBody.service_name = form.serviceName.trim() || 'grpc'
      }
      if (form.security === 'reality') {
        nodeBody.fingerprint = form.fingerprint
        if (form.shortId.trim()) {
          nodeBody.short_id = form.shortId.trim()
        }
        if (form.dest.trim()) {
          nodeBody.dest = form.dest.trim()
        }
        const names = form.serverNames
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
        if (names.length > 0) {
          nodeBody.server_names = names
        }
      }
      if (form.security === 'tls') {
        nodeBody.fingerprint = form.fingerprint
        nodeBody.cert_mode = form.certMode
        // acme 的 tls_domain 由后端从落地服务器地址检测填充（前端不传）
        if (form.certMode === 'selfsign' && form.tlsDomain.trim()) {
          nodeBody.tls_domain = form.tlsDomain.trim()
        }
      }
      if (form.security === 'none' && form.protocol === 'vless' && form.encryption === 'none') {
        setCreateError('vless 在安全层 none 下必须启用 VLESS Encryption')
        return
      }
      if (form.protocol === 'vless') {
        // vision 仅 tcp；vision + Encryption 允许组合（§15）；tls 下 vision 同样合法（§2）
        nodeBody.flow = form.network === 'tcp' ? form.flow : 'none'
        if (form.encryption !== 'none') {
          nodeBody.encryption = form.encryption
        }
      }
    }
    if (form.protocol === 'shadowsocks') {
      nodeBody.method = form.method
    }
    if (form.protocol === 'vmess') {
      nodeBody.cipher = form.cipher
    }
    if (form.protocol === 'dokodemo-door') {
      if (!form.targetAddress.trim() || !form.targetPort.trim()) {
        setCreateError('dokodemo-door 需要目标地址与目标端口')
        return
      }
      nodeBody.target_address = form.targetAddress.trim()
      nodeBody.target_port = Number(form.targetPort)
    }
    setCreating(true)
    // 逐跳地址：空串 = 跟随服务器默认地址，提交时不携带 address 字段。
    const hopAddrList =
      chainType === 'direct'
        ? [topology.entryAddr]
        : [topology.entryAddr, ...topology.middleAddrs, topology.exitAddr]
    const mkHop = (id: string, addr: string | undefined): ChainHopInput =>
      addr ? { server_id: Number(id), address: addr } : { server_id: Number(id) }
    try {
      if (editingChainId !== null) {
        const body: EditChainRequest = {
          chain_id: editingChainId,
          name: resolvedName,
          hops: hopIds.map((id, i) => mkHop(id, hopAddrList[i])),
          node: nodeBody,
          traffic_multiplier: form.trafficMultiplier,
        }
        if (entryPort.trim()) body.entry_port = Number(entryPort)
        if (entryNode) body.entry_node = entryNode
        const { observeId } = await api.editChain(body)
        if (observeId) showOperation({ observeId })
      } else {
        const body: CreateChainRequest = {
          name: resolvedName,
          hops: hopIds.map((id, i) => mkHop(id, hopAddrList[i])),
          entry: mkHop(entryId, topology.entryAddr),
          middle: middleIds.map((id, i) => mkHop(id, topology.middleAddrs[i])),
          exit: mkHop(
            chainType === 'direct' ? entryId : exitId,
            chainType === 'direct' ? topology.entryAddr : topology.exitAddr,
          ),
          node: nodeBody,
          traffic_multiplier: form.trafficMultiplier,
        }
        if (entryPort.trim()) body.entry_port = Number(entryPort)
        if (entryNode) body.entry_node = entryNode
        const { observeId } = await api.createChain(body)
        if (observeId) showOperation({ observeId })
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
    topologyServers,
    hopIndexes,
    entryPortHint,
    entryBlockLocked,
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
