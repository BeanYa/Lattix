/** 服务器测试报告里 map[string]any 条目的宽松取值辅助。 */

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

export function asRecords(value: unknown): Array<Record<string, unknown>> {
  return Array.isArray(value) ? value.map(asRecord) : []
}

export function text(value: unknown, fallback = '-'): string {
  if (typeof value === 'string' && value !== '') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return fallback
}

export function number(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
