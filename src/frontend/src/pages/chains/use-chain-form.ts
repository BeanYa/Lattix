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

// 与后端 shared 包保持一致的协议/选项常量（出口节点协议表单复用 Nodes 向导的 vless+reality 字段）。
export const DIRECT_PROTOCOLS = ['vless', 'socks', 'http', 'dokodemo-door'] as const
export const RELAY_PROTOCOLS = ['vless', 'socks', 'http'] as const
export const NETWORKS = ['tcp', 'xhttp']
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

const REALITY_PROTOCOLS = ['vless']

/** 入站能力（§21）：direct 或 NAT 受限直连（有端口段）。仅出口档 NAT 不能作入口/中间跳。 */
export function inboundCapable(s: Server): boolean {
  return s.machine_type === 'direct' || s.allowed_ports.length > 0
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

export interface ChainFormState {
  chainType: 'direct' | 'relay'
  name: string
  entryId: string
  middleIds: string[]
  exitId: string
  entryAddr: string
  middleAddrs: string[]
  exitAddr: string
  entryPort: string
  protocol: string
  port: string
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
  targetAddress: string
  targetPort: string
  trafficMultiplier: string
}

const initialChainForm: ChainFormState = {
  chainType: 'direct',
  name: '',
  entryId: '',
  middleIds: [],
  exitId: '',
  entryAddr: '',
  middleAddrs: [],
  exitAddr: '',
  entryPort: '',
  protocol: 'vless',
  port: '',
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
  targetAddress: '',
  targetPort: '',
  trafficMultiplier: '1.000',
}

/**
 * 创建/编辑链路对话框的表单状态与提交逻辑。
 * 原为 Chains 页内约 26 个扁平 useState，拆分时装订为单一表单状态对象。
 */
export function useChainForm({
  chains,
  nodes,
  servers,
  onError,
  onSaved,
  showOperation,
}: {
  chains: Chain[]
  nodes: XrayNode[]
  servers: Server[]
  onError: (message: string) => void
  onSaved: () => void
  showOperation: (opts: { observeId: string }) => void
}) {
  const [open, setOpen] = useState(false)
  const [editingChainId, setEditingChainId] = useState<number | null>(null)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [form, setForm] = useState<ChainFormState>(initialChainForm)

  const patch = (partial: Partial<ChainFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  const isReality = REALITY_PROTOCOLS.includes(form.protocol)
  const selectedEntry = servers.find((s) => String(s.id) === form.entryId)
  const selectedExit = servers.find((s) => String(s.id) === form.exitId)
  const selectedMiddleServers = form.middleIds.flatMap((id) => {
    const server = servers.find((candidate) => String(candidate.id) === id)
    return server ? [server] : []
  })
  const topologyServers = [
    ...(selectedEntry ? [selectedEntry] : []),
    ...selectedMiddleServers,
    ...(form.chainType === 'relay' && selectedExit ? [selectedExit] : []),
  ]
  const hopIndexes =
    form.chainType === 'relay' ? selectedMiddleServers.map((_, index) => index + 1) : []
  const entryPortHint = (() => {
    const value = Number(form.entryPort)
    if (!value || !form.entryId) return ''
    const owner = chains.find(
      (c) =>
        c.entry_port === value &&
        c.hops[0]?.server_id === Number(form.entryId) &&
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
    port: form.entryPort,
    hopIndexes,
  })

  const resetChainForm = () => {
    setEditingChainId(null)
    setForm(initialChainForm)
    setCreateError('')
  }

  const openCreate = () => {
    resetChainForm()
    onError('')
    setOpen(true)
  }

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) resetChainForm()
  }

  const openEdit = (chain: Chain) => {
    const service = nodes.find((node) => node.id === chain.service_node_id)
    if (!service && !chain.service_config) {
      onError('链路出口配置不存在')
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
      onError('链路出口配置无法解析')
      return
    }

    const shortIds = Array.isArray(reality.shortIds) ? reality.shortIds : []
    const configuredServerNames = Array.isArray(reality.serverNames)
      ? reality.serverNames.filter((value): value is string => typeof value === 'string')
      : []
    const configuredDest = String(reality.dest || `${DEFAULT_REALITY_DEST}:443`)
    const effectiveServerNames =
      configuredServerNames.length > 0 ? configuredServerNames : [DEFAULT_REALITY_DEST]
    setEditingChainId(chain.id)
    // 逐跳地址回填：空串 = 跟随服务器默认地址；已失效值由选择器内标注。
    setForm({
      chainType: chain.hops.length === 1 ? 'direct' : 'relay',
      name: chain.name,
      entryId: String(chain.hops[0]?.server_id ?? ''),
      middleIds: chain.hops.slice(1, -1).map((hop) => String(hop.server_id)),
      exitId: chain.hops.length > 1 ? String(chain.hops.at(-1)?.server_id ?? '') : '',
      entryAddr: chain.hops[0]?.address ?? '',
      middleAddrs: chain.hops.slice(1, -1).map((hop) => hop.address ?? ''),
      exitAddr: chain.hops.length > 1 ? (chain.hops.at(-1)?.address ?? '') : '',
      entryPort: chain.entry_port ? String(chain.entry_port) : '',
      trafficMultiplier: chain.traffic_multiplier || '1.000',
      protocol: String(virtual.protocol ?? service?.protocol ?? 'vless'),
      port: virtual.port ? String(virtual.port) : '',
      network: String(virtual.network || 'tcp'),
      fingerprint: String(virtual.fingerprint || 'chrome'),
      flow: String(virtual.flow || 'none'),
      encryption: String(virtual.encryption || 'none'),
      path: String(virtual.path || '/'),
      mode: String(virtual.mode || 'auto'),
      host: String(virtual.host || ''),
      shortId: typeof shortIds[0] === 'string' ? shortIds[0] : '',
      destPreset: inferRealityDestPreset(configuredDest, effectiveServerNames),
      dest: configuredDest,
      serverNames: effectiveServerNames.join(','),
      targetAddress: String(settings.address || ''),
      targetPort: settings.port ? String(settings.port) : '',
    })
    setCreateError('')
    onError('')
    setOpen(true)
  }

  const onTypeChange = (value: string | null) => {
    if (value !== 'direct' && value !== 'relay') return
    setForm((current) => ({
      ...current,
      chainType: value,
      middleIds: [],
      exitId: '',
      middleAddrs: [],
      exitAddr: '',
      protocol:
        value === 'relay' && current.protocol === 'dokodemo-door' ? 'vless' : current.protocol,
    }))
  }

  const setMiddle = (i: number, value: string) => {
    setForm((current) => {
      const middleIds = current.middleIds.slice()
      middleIds[i] = value
      const middleAddrs = current.middleAddrs.slice()
      middleAddrs[i] = ''
      return { ...current, middleIds, middleAddrs }
    })
  }

  const setMiddleAddr = (i: number, value: string) => {
    setForm((current) => {
      const middleAddrs = current.middleAddrs.slice()
      middleAddrs[i] = value
      return { ...current, middleAddrs }
    })
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const resolvedName = form.name.trim() || randomChainName()
    if (form.name.trim() && strictNameResult.error) {
      setCreateError(strictNameResult.error)
      return
    }
    if (!form.entryId) {
      setCreateError('请选择入口服务器')
      return
    }
    if (form.chainType === 'relay' && (!form.exitId || form.middleIds.some((m) => !m))) {
      setCreateError('请完整选择入口、中间跳与出口服务器')
      return
    }
    const hopIds =
      form.chainType === 'direct' ? [form.entryId] : [form.entryId, ...form.middleIds, form.exitId]
    if (hopIds.length > 4) {
      setCreateError('链长上限 4 跳（入口 + 中间跳 ≤2 + 出口）')
      return
    }
    if (new Set(hopIds).size !== hopIds.length) {
      setCreateError('同一服务器在一条链中不重复')
      return
    }
    // 直连唯一服务器、或中转的入口与中转跳必须有入站能力；中转出口任意。
    const inboundIds = form.chainType === 'direct' ? hopIds : hopIds.slice(0, -1)
    for (const id of inboundIds) {
      const srv = servers.find((s) => String(s.id) === id)
      if (srv && !inboundCapable(srv)) {
        setCreateError(`服务器 ${srv.alias} 无入站能力（仅出口档 NAT），不能作入口/中间跳`)
        return
      }
    }
    const nodeBody: CreateNodeRequest = {
      name: resolvedName,
      server_id: Number(form.chainType === 'direct' ? form.entryId : form.exitId),
      protocol: form.protocol,
    }
    if ((form.chainType === 'direct' ? form.entryPort : form.port).trim()) {
      nodeBody.port = Number(form.chainType === 'direct' ? form.entryPort : form.port)
    }
    if (isReality) {
      nodeBody.fingerprint = form.fingerprint
      nodeBody.network = form.network
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
      if (form.network === 'xhttp') {
        nodeBody.path = form.path.trim() || '/'
        nodeBody.mode = form.mode
        if (form.host.trim()) {
          nodeBody.host = form.host.trim()
        }
      }
      if (form.protocol === 'vless') {
        // vision 仅 tcp；xhttp 必须无 flow；vision + Encryption 允许组合（§15）
        nodeBody.flow = form.network === 'tcp' ? form.flow : 'none'
        if (form.encryption !== 'none') {
          nodeBody.encryption = form.encryption
        }
      }
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
      form.chainType === 'direct'
        ? [form.entryAddr]
        : [form.entryAddr, ...form.middleAddrs, form.exitAddr]
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
        if (form.entryPort.trim()) body.entry_port = Number(form.entryPort)
        const { observeId } = await api.editChain(body)
        if (observeId) showOperation({ observeId })
      } else {
        const body: CreateChainRequest = {
          name: resolvedName,
          hops: hopIds.map((id, i) => mkHop(id, hopAddrList[i])),
          entry: mkHop(form.entryId, form.entryAddr),
          middle: form.middleIds.map((id, i) => mkHop(id, form.middleAddrs[i])),
          exit: mkHop(
            form.chainType === 'direct' ? form.entryId : form.exitId,
            form.chainType === 'direct' ? form.entryAddr : form.exitAddr,
          ),
          node: nodeBody,
          traffic_multiplier: form.trafficMultiplier,
        }
        if (form.entryPort.trim()) body.entry_port = Number(form.entryPort)
        const { observeId } = await api.createChain(body)
        if (observeId) showOperation({ observeId })
      }
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  return {
    open,
    editingChainId,
    creating,
    createError,
    form,
    patch,
    isReality,
    topologyServers,
    hopIndexes,
    entryPortHint,
    strictNameResult,
    openCreate,
    openEdit,
    onOpenChange,
    onTypeChange,
    setMiddle,
    setMiddleAddr,
    onSubmit,
  }
}

export type ChainFormController = ReturnType<typeof useChainForm>
