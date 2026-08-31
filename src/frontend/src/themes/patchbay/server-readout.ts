import { formatByteRate, humanizeBytes } from '@/lib/format'
import type { Server } from '@/lib/types'

const nonnegative = (value: number | null | undefined): number | null =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null

function usage(used: number | undefined, total: number | undefined) {
  const amount = nonnegative(used)
  const capacity = nonnegative(total)
  if (amount === null || capacity === null || capacity === 0) {
    return { percent: null, detail: '容量未上报' }
  }
  return {
    percent: Math.min(100, (amount / capacity) * 100),
    detail: `${humanizeBytes(amount)} / ${humanizeBytes(capacity)}`,
  }
}

export function serverReadout(server?: Server, fallbackAddress = '') {
  const metrics = server?.metrics
  const cpu = nonnegative(metrics?.cpu_percent)
  const load = nonnegative(metrics?.load1)
  const memory = usage(metrics?.mem_used, metrics?.mem_total)
  const disk = usage(metrics?.disk_used, metrics?.disk_total)
  const uptime = nonnegative(metrics?.uptime_seconds)
  const latency = nonnegative(metrics?.latency_ms)
  const address = server?.address?.trim() || server?.learned_addr?.trim() || fallbackAddress.trim()
  const addresses = [
    ...new Set(
      [address, ...(server?.addresses ?? [])].map((value) => value.trim()).filter(Boolean),
    ),
  ]
  return {
    location: server?.location?.trim() || server?.country_code || '位置未设置',
    address: addresses[0] || '地址待上报',
    addresses,
    resources: [
      {
        label: 'CPU',
        percent: cpu === null ? null : Math.min(100, cpu),
        detail: load === null ? '负载未上报' : `LOAD ${load.toFixed(2)}`,
      },
      { label: '内存', ...memory },
      { label: '磁盘', ...disk },
    ],
    tx: formatByteRate(nonnegative(metrics?.network_tx_bps)),
    rx: formatByteRate(nonnegative(metrics?.network_rx_bps)),
    latency: latency === null ? '--' : `${Math.round(latency)} ms`,
    uptime:
      uptime === null
        ? '--'
        : uptime >= 86400
          ? `${Math.floor(uptime / 86400)}d ${Math.floor((uptime % 86400) / 3600)}h`
          : uptime >= 3600
            ? `${Math.floor(uptime / 3600)}h ${Math.floor((uptime % 3600) / 60)}m`
            : `${Math.floor(uptime / 60)}m`,
  }
}
