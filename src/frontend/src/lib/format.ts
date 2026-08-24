/** humanizeBytes 格式化字节数为可读单位（B/KB/MB/GB/TB）。 */
export function humanizeBytes(n: number): string {
  if (n < 1024) {
    return `${n} B`
  }
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`
}

/** formatByteRate formats a nullable bytes-per-second sample. */
export function formatByteRate(value: number | null): string {
  return value === null ? '--' : `${humanizeBytes(Math.round(value))}/s`
}

/** formatBytes 格式化字节数为 B/KB/MB（一位小数），用于请求日志占用等小米数场景。 */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/** CURRENCIES 面板支持的币种选项。 */
export const CURRENCIES = ['CNY', 'USD', 'EUR', 'CAD', 'HKD', 'JPY', 'AUD', 'GBP', 'SGD', 'CHF']

/** formatDateTime 按面板设置的全局时区格式化 RFC3339 时间；timezone 为空用浏览器本地。 */
export function formatDateTime(t: string | null | undefined, timezone?: string): string {
  if (!t) {
    return '-'
  }
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) {
    return '-'
  }
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      ...(timezone ? { timeZone: timezone } : {}),
    }).format(d)
  } catch {
    // 非法时区名时退回浏览器本地
    return d.toLocaleString()
  }
}
