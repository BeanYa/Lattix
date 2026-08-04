export type TrafficUnit = 'TB' | 'GB' | 'MB'

export const TRAFFIC_UNITS: Array<{ value: TrafficUnit; label: TrafficUnit }> = [
  { value: 'TB', label: 'TB' },
  { value: 'GB', label: 'GB' },
  { value: 'MB', label: 'MB' },
]

const TRAFFIC_UNIT_BYTES: Record<TrafficUnit, number> = {
  TB: 1099511627776,
  GB: 1073741824,
  MB: 1048576,
}

export function parseTrafficLimit(value: string, unit: TrafficUnit): number {
  if (!value.trim()) return 0
  const quantity = Number(value)
  const bytes = Math.round(quantity * TRAFFIC_UNIT_BYTES[unit])
  if (!Number.isFinite(quantity) || quantity < 0 || !Number.isSafeInteger(bytes)) {
    throw new Error('流量配额须为有效的非负数')
  }
  return bytes
}

export function parseTrafficResetDay(value: string): number {
  if (!value.trim()) return 0
  const day = Number(value)
  if (!Number.isInteger(day) || day < 1 || day > 31) {
    throw new Error('重置日须留空或填写 1–31 的整数')
  }
  return day
}

export function formatTrafficLimit(bytes: number): { value: string; unit: TrafficUnit } {
  if (bytes <= 0) return { value: '', unit: 'GB' }
  for (const unit of TRAFFIC_UNITS) {
    const unitBytes = TRAFFIC_UNIT_BYTES[unit.value]
    if (bytes % unitBytes === 0) {
      return { value: String(bytes / unitBytes), unit: unit.value }
    }
  }
  return { value: String(bytes / TRAFFIC_UNIT_BYTES.MB), unit: 'MB' }
}

/** toLocalDateInput 把 RFC3339 时间转成 date 输入框所需的本地日期（yyyy-MM-dd）；无效返回空串。 */
export function toLocalDateInput(t: string): string {
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) {
    return ''
  }
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** localDateToRFC3339EndOfDay 把 date 输入框值（yyyy-MM-dd）转成所选日期当天结束（本地 23:59:59）的 RFC3339（UTC）；空串/无效返回 null。 */
export function localDateToRFC3339EndOfDay(v: string): string | null {
  if (!v) {
    return null
  }
  const d = new Date(`${v}T23:59:59`)
  return Number.isNaN(d.getTime()) ? null : d.toISOString()
}

/** expiryDateDay 返回所选日期中的“日”（1-31）；无效/空值返回空串。 */
export function expiryDateDay(v: string): string {
  if (!v) {
    return ''
  }
  const d = new Date(`${v}T00:00:00`)
  return Number.isNaN(d.getTime()) ? '' : String(d.getDate())
}
