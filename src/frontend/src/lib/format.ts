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
