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
