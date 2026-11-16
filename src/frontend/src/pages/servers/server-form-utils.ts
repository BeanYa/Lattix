import { parsePortRange, validatePortRanges } from '@/lib/ports'
import type {
  BillingInput,
  ExternalExit,
  IntervalUnit,
  MachineType,
  ParseExternalLinkResponse,
  PortRange,
  Server,
  ServerKind,
  TrafficAccountingMode,
  TrafficPlanInput,
} from '@/lib/types'

export interface BillingFormState {
  enabled: boolean
  providerId: string
  amount: string
  currency: string
  startedOn: string
  intervalCount: number
  intervalUnit: IntervalUnit
  renewalOn: string
}

export interface TrafficFormState {
  limited: boolean
  quota: string
  quotaUnit: 'GB' | 'TB'
  accountingMode: TrafficAccountingMode
  anchorOn: string
  resetCount: number
  resetUnit: IntervalUnit
}

// 添加服务器对话框的服务器类型三选一：外部直链（未托管）不属于机器类型。
export type ServerTypeChoice = MachineType | 'external'

// 外部直链输入与解析状态（添加/编辑服务器共用）：解析结果与链接原文绑定，
// 链接改动即失效，避免把未解析/过期解析的链接提交给后端。
export interface ExternalLinkState {
  link: string
  parsedLink: string
  preview: ParseExternalLinkResponse | null
  parseError: string
  parsing: boolean
}

export interface ServerFormPayload {
  alias: string
  kind?: ServerKind
  external_link?: string
  address?: string
  machine_type?: MachineType
  allowed_ports?: PortRange[]
  tags?: string[]
  country_code: string
  location: string
  non_direct: boolean
  billing: BillingInput
  traffic_plan: TrafficPlanInput
}

export interface ServerCreateFormState {
  serverType: ServerTypeChoice
  alias: string
  address: string
  tags: string[]
  countryCode: string
  location: string
  nonDirect: boolean
  portRows: string[]
  billing: BillingFormState
  traffic: TrafficFormState
  external: ExternalLinkState
}

export function localDate() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

export function addInterval(date: string, count: number, unit: IntervalUnit) {
  const value = new Date(`${date}T00:00:00`)
  if (unit === 'day') value.setDate(value.getDate() + count)
  if (unit === 'month') value.setMonth(value.getMonth() + count)
  if (unit === 'year') value.setFullYear(value.getFullYear() + count)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

export function defaultBilling(): BillingFormState {
  const today = localDate()
  return {
    enabled: false,
    providerId: '',
    amount: '',
    currency: 'CNY',
    startedOn: today,
    intervalCount: 1,
    intervalUnit: 'month',
    renewalOn: addInterval(today, 1, 'month'),
  }
}

export function defaultTraffic(): TrafficFormState {
  return {
    limited: false,
    quota: '1000',
    quotaUnit: 'GB',
    accountingMode: 'outbound',
    anchorOn: localDate(),
    resetCount: 1,
    resetUnit: 'month',
  }
}

export function billingPayload(value: BillingFormState): BillingInput {
  const digits = ['JPY', 'KRW', 'ISK'].includes(value.currency) ? 0 : 2
  return {
    enabled: value.enabled,
    provider_id: Number(value.providerId || 0),
    amount_minor: Math.round(Number(value.amount || 0) * 10 ** digits),
    currency: value.currency,
    service_started_on: value.startedOn,
    interval_count: value.intervalCount,
    interval_unit: value.intervalUnit,
    next_renewal_on: value.renewalOn,
  }
}

export function trafficPayload(value: TrafficFormState): TrafficPlanInput {
  return {
    quota_bytes: value.limited
      ? Math.round(Number(value.quota) * (value.quotaUnit === 'TB' ? 1e12 : 1e9))
      : null,
    accounting_mode: value.accountingMode,
    reset_anchor_on: value.anchorOn,
    reset_count: value.resetCount,
    reset_unit: value.resetUnit,
  }
}

// 内置公网地址候选（§9）：拨入学习地址 + agent 上报的网卡非回环地址，去重。
export function addrCandidates(s: Server): string[] {
  return [...new Set([s.learned_addr, ...s.nic_addresses].filter(Boolean))]
}

/** 解析并校验端口段文本行；全部留空返回空数组（仅出口档），非法返回错误文案。 */
export function parsePortRows(rows: string[]): { ranges: PortRange[] } | { error: string } {
  const filled = rows.map((r) => r.trim()).filter(Boolean)
  const ranges: PortRange[] = []
  for (const row of filled) {
    const r = parsePortRange(row)
    if (!r) {
      return {
        error: `端口段「${row}」格式非法：支持单端口 10000、范围 10001-10010、映射 20001-20010:10001-10010`,
      }
    }
    ranges.push(r)
  }
  const err = validatePortRanges(ranges)
  if (err) {
    return { error: err }
  }
  return { ranges }
}

// ---------- 外部直链（未托管服务器）：解析状态、预览与提交载荷 ----------

export function initialExternalLink(link = ''): ExternalLinkState {
  return { link, parsedLink: '', preview: null, parseError: '', parsing: false }
}

export function initialServerCreateForm(): ServerCreateFormState {
  return {
    serverType: 'direct',
    alias: '',
    address: '',
    tags: [],
    countryCode: '',
    location: '',
    nonDirect: false,
    portRows: [''],
    billing: defaultBilling(),
    traffic: defaultTraffic(),
    external: initialExternalLink(),
  }
}

/** 切换服务器类型：清掉与目标类型无关的输入，公共资料（别名/地区/标签/非直连/
 *  账单/流量）保留。外部节点的地址与端口段由链接派生，切走即丢弃。 */
export function switchServerType(
  state: ServerCreateFormState,
  serverType: ServerTypeChoice,
): ServerCreateFormState {
  if (state.serverType === serverType) {
    return state
  }
  if (serverType === 'external') {
    return { ...state, serverType, address: '', portRows: [''], external: initialExternalLink() }
  }
  return { ...state, serverType, external: initialExternalLink() }
}

/** 最近一次解析是否对应当前链接（去首尾空白后比较）。 */
export function isExternalLinkParsed(
  state: Pick<ExternalLinkState, 'link' | 'parsedLink'>,
): boolean {
  const link = state.link.trim()
  return link !== '' && link === state.parsedLink.trim()
}

/**
 * 提交前校验外部链接：链接必填；未解析或解析后又改过链接一律拒绝。
 * savedLink 用于编辑：链接与已保存值一致时无需重新解析。
 */
export function externalLinkForSubmit(
  state: Pick<ExternalLinkState, 'link' | 'parsedLink'> & { savedLink?: string },
): { link: string } | { error: string } {
  const link = state.link.trim()
  if (!link) {
    return { error: '分享链接不能为空' }
  }
  if (state.savedLink !== undefined && link === state.savedLink.trim()) {
    return { link }
  }
  if (!isExternalLinkParsed({ link, parsedLink: state.parsedLink })) {
    return { error: '请先解析链接（链接已修改或尚未解析）' }
  }
  return { link }
}

/** 解析出节点名后，别名为空则回填（不覆盖用户已输入的名称）。 */
export function aliasAfterParse(alias: string, parsedName: string): string {
  return alias.trim() ? alias : parsedName.trim()
}

/** 编辑分享链接：仍是最近一次解析的同一链接时保留预览，否则清空解析状态。 */
export function editExternalLink(state: ExternalLinkState, link: string): ExternalLinkState {
  if (state.parsedLink !== '' && state.parsedLink === link.trim()) {
    return { ...state, link, parsing: false, parseError: '' }
  }
  return { ...state, link, parsedLink: '', preview: null, parseError: '', parsing: false }
}

/** 解析成功后的链接状态；解析期间链接被改动（与发起解析的原文不同）返回 null。 */
export function applyExternalLinkPreview(
  state: ExternalLinkState,
  link: string,
  preview: ParseExternalLinkResponse,
): ExternalLinkState | null {
  if (state.link.trim() !== link.trim()) {
    return null
  }
  return { ...state, parsedLink: link.trim(), preview, parseError: '', parsing: false }
}

/** 解析失败后的链接状态；解析期间链接被改动返回 null（丢弃过期错误）。 */
export function applyExternalLinkError(
  state: ExternalLinkState,
  link: string,
  message: string,
): ExternalLinkState | null {
  if (state.link.trim() !== link.trim()) {
    return null
  }
  return { ...state, parsedLink: '', preview: null, parseError: message, parsing: false }
}

/** 外部直链 host:port（IPv6 字面量自动加方括号）。 */
export function externalExitTarget(exit: Pick<ExternalExit, 'address' | 'port'>): string {
  const address = exit.address.trim()
  const host = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address
  return `${host}:${exit.port}`
}

/** 外部出口传输/安全层摘要（与后端 parse-link 的 transport 字段同源）。 */
export function externalTransportSummary(
  exit: Pick<ExternalExit, 'protocol' | 'network' | 'security'>,
): string {
  if (!['vless', 'vmess', 'trojan'].includes(exit.protocol)) {
    return 'TCP'
  }
  return `${exit.network?.trim() || 'tcp'} · ${exit.security?.trim() || 'none'}`
}

/** 已保存外部出口的解析预览（编辑对话框首次打开时回显当前节点）。 */
export function externalExitPreview(exit: ExternalExit, name: string): ParseExternalLinkResponse {
  return {
    name,
    protocol: exit.protocol,
    address: exit.address,
    port: exit.port,
    transport: externalTransportSummary(exit),
    exit,
  }
}

/** 组装创建服务器的提交载荷（纯函数）：外部节点只提交 kind/external_link，
 *  机器类型/地址/端口段由后端按链接强制派生。 */
export function buildServerCreatePayload(
  state: ServerCreateFormState,
): { body: ServerFormPayload } | { error: string } {
  const alias = state.alias.trim()
  if (!alias) {
    return { error: '别名不能为空' }
  }
  if (!state.countryCode) {
    return { error: '国家/地区不能为空' }
  }
  const body: ServerFormPayload = {
    alias,
    country_code: state.countryCode,
    location: state.location.trim(),
    non_direct: state.nonDirect,
    tags: state.tags,
    billing: billingPayload(state.billing),
    traffic_plan: trafficPayload(state.traffic),
  }
  if (state.serverType === 'external') {
    const link = externalLinkForSubmit(state.external)
    if ('error' in link) {
      return link
    }
    body.kind = 'external'
    body.external_link = link.link
    return { body }
  }
  body.machine_type = state.serverType
  if (state.address.trim()) {
    body.address = state.address.trim()
  }
  if (state.serverType === 'nat') {
    if (!state.address.trim()) {
      return { error: 'NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）' }
    }
    const parsed = parsePortRows(state.portRows)
    if ('error' in parsed) {
      return parsed
    }
    body.allowed_ports = parsed.ranges
  }
  return { body }
}
