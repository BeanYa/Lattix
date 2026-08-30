import { parsePortRange, validatePortRanges } from '@/lib/ports'
import type {
  BillingInput,
  IntervalUnit,
  MachineType,
  PortRange,
  Server,
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

export interface ServerFormPayload {
  alias: string
  address?: string
  machine_type: MachineType
  allowed_ports?: PortRange[]
  tags?: string[]
  country_code: string
  location: string
  billing: BillingInput
  traffic_plan: TrafficPlanInput
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
