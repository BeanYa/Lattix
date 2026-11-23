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
  ServerUpdateRequest,
  TrafficAccountingMode,
  TrafficPlanInput,
} from '@/lib/types'

import {
  defaultExternalExitForm,
  externalExitFormError,
  externalExitFormFromPreview,
  externalExitFormPayload,
  type ExternalExitFormState,
} from './external-exit-form'

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

// 添加/编辑服务器对话框的服务器类型三选一：外部直链（未托管）不属于机器类型。
export type ServerTypeChoice = MachineType | 'external'

export const SERVER_TYPES: ReadonlyArray<readonly [ServerTypeChoice, string]> = [
  ['direct', '独立 IP'],
  ['nat', 'NAT'],
  ['external', '外部直链'],
]

export function serverTypeOf(server: Pick<Server, 'kind' | 'machine_type'>): ServerTypeChoice {
  if (server.kind === 'external') {
    return 'external'
  }
  return server.machine_type === 'nat' ? 'nat' : 'direct'
}

export interface ServerEditFormState {
  serverType: ServerTypeChoice
  alias: string
  addresses: string[]
  defaultAddr: string
  addrInput: string
  portRows: string[]
  tags: string[]
  countryCode: string
  location: string
  nonDirect: boolean
  billing: BillingFormState
  traffic: TrafficFormState
  xrayOverride: string
  xrayVersions: string[]
  external: ExternalLinkState
}

// 外部直链输入与协议表单状态（添加/编辑服务器共用）：分享链接只用于「解析 →
// 自动填写」，协议参数本身才是提交来源，因此链接改动不会清空已填参数。
export interface ExternalLinkState {
  link: string
  parseError: string
  parsing: boolean
  exit: ExternalExitFormState
}

export interface ServerFormPayload {
  alias: string
  kind?: ServerKind
  external_link?: string
  external_exit?: ExternalExit
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
  if (s.kind === 'external') {
    return []
  }
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

export function initialExternalLink(
  link = '',
  exit: ExternalExitFormState = defaultExternalExitForm(),
): ExternalLinkState {
  return { link, parseError: '', parsing: false, exit }
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

/** 解析出节点名后，别名为空则回填（不覆盖用户已输入的名称）。 */
export function aliasAfterParse(alias: string, parsedName: string): string {
  return alias.trim() ? alias : parsedName.trim()
}

/** 编辑分享链接：保留已填写的协议参数（链接只影响下一次解析）。 */
export function editExternalLink(state: ExternalLinkState, link: string): ExternalLinkState {
  return { ...state, link, parsing: false, parseError: '' }
}

/** 解析成功：把解析结果自动填进协议表单；解析期间链接被改动返回 null。 */
export function applyExternalLinkPreview(
  state: ExternalLinkState,
  link: string,
  preview: ParseExternalLinkResponse,
): ExternalLinkState | null {
  if (state.link.trim() !== link.trim()) {
    return null
  }
  return {
    ...state,
    parseError: '',
    parsing: false,
    exit: externalExitFormFromPreview(preview),
  }
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
  return { ...state, parseError: message, parsing: false }
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

/** 组装创建服务器的提交载荷（纯函数）：外部节点提交协议参数（external_exit），
 *  别名/国家/地区均可空——服务器表单没有必填项，不完整规格由链路页提示补全。 */
export function buildServerCreatePayload(
  state: ServerCreateFormState,
): { body: ServerFormPayload } | { error: string } {
  if (state.serverType === 'external') {
    const exitError = externalExitFormError(state.external.exit)
    if (exitError) {
      return { error: exitError }
    }
    const body: ServerFormPayload = {
      alias: state.alias.trim(),
      kind: 'external',
      external_exit: externalExitFormPayload(state.external.exit),
      country_code: state.countryCode,
      location: state.location.trim(),
      non_direct: state.nonDirect,
      tags: state.tags,
      billing: billingPayload(state.billing),
      traffic_plan: trafficPayload(state.traffic),
    }
    const link = state.external.link.trim()
    if (link) {
      body.external_link = link
    }
    return { body }
  }
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

/** 组装编辑/转换服务器的提交载荷（纯函数）：按目标类型校验并输出 ServerUpdateRequest。 */
export function buildServerEditPayload(
  target: Pick<Server, 'id'>,
  state: ServerEditFormState,
): { body: ServerUpdateRequest } | { error: string } {
  if (state.serverType === 'external') {
    const exitError = externalExitFormError(state.external.exit)
    if (exitError) {
      return { error: exitError }
    }
    const body: ServerUpdateRequest = {
      server_id: target.id,
      kind: 'external',
      alias: state.alias.trim(),
      external_exit: externalExitFormPayload(state.external.exit),
      tags: state.tags,
      country_code: state.countryCode,
      location: state.location.trim(),
      billing: billingPayload(state.billing),
      traffic_plan: trafficPayload(state.traffic),
      non_direct: state.nonDirect,
    }
    const link = state.external.link.trim()
    if (link) {
      body.external_link = link
    }
    return { body }
  }

  const finalAlias = state.alias.trim()
  if (!finalAlias) {
    return { error: '名称不能为空' }
  }
  if (!state.countryCode) {
    return { error: '国家/地区不能为空' }
  }
  const finalAddress = state.defaultAddr
  const isNat = state.serverType === 'nat'
  if (isNat && !finalAddress) {
    return { error: 'NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）' }
  }
  const nextAddresses = state.addresses.length > 0 ? state.addresses : undefined
  if (nextAddresses && !nextAddresses.includes(finalAddress)) {
    return { error: '默认地址必须是地址列表中的一项' }
  }
  let ranges: PortRange[] | undefined
  if (isNat) {
    const parsed = parsePortRows(state.portRows)
    if ('error' in parsed) {
      return parsed
    }
    ranges = parsed.ranges
  }
  const body: ServerUpdateRequest = {
    server_id: target.id,
    kind: 'managed',
    machine_type: state.serverType,
    alias: finalAlias,
    address: finalAddress,
    tags: state.tags,
    country_code: state.countryCode,
    location: state.location.trim(),
    billing: billingPayload(state.billing),
    traffic_plan: trafficPayload(state.traffic),
    custom_settings: state.xrayOverride ? { xray_version: state.xrayOverride } : {},
    non_direct: state.nonDirect,
  }
  if (nextAddresses !== undefined) {
    body.addresses = nextAddresses
  }
  if (ranges !== undefined) {
    body.allowed_ports = ranges
  }
  return { body }
}
