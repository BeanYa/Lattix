import {
  PROBE_ROUND_SLOTS,
  probeRoundTone,
  probeStripRounds,
} from '@/components/server-probe-strip'
import { humanizeBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, Server } from '@/lib/types'

/** 三网测速 mini 色块条（12 槽、末位最新、tone 三档）；无测速数据回退上下行速率行。 */
export function ServerProbeMini({
  server,
  probeRows,
  probeTitle,
}: {
  server: Server
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
}) {
  if (probeRows.length === 0) {
    return (
      <div className="cw-node-traffic" aria-label="上下行速率">
        <span>
          ↓{' '}
          {server.metrics?.network_rx_bps != null
            ? `${humanizeBytes(server.metrics.network_rx_bps)}/s`
            : '—'}
        </span>
        <span>
          ↑{' '}
          {server.metrics?.network_tx_bps != null
            ? `${humanizeBytes(server.metrics.network_tx_bps)}/s`
            : '—'}
        </span>
      </div>
    )
  }
  return (
    <div className="cw-node-probes" aria-label="三网测速">
      {probeRows.map((row) => {
        const latest = row.rounds.at(-1)?.latency_ms ?? row.latency_ms
        return (
          <div
            key={row.label}
            className="cw-probe-row"
            title={`${row.label}${probeTitle ? ` · ${probeTitle}` : ''}：最新 ${
              latest ?? '—'
            }ms，丢包 ${row.loss_percent ?? '—'}%`}
          >
            <span className="cw-probe-label">{row.label.slice(0, 2)}</span>
            <span className="cw-probe-rounds">
              {probeStripRounds(row.rounds, PROBE_ROUND_SLOTS).map((round, index) => (
                <i
                  key={index}
                  className={round === null ? 'is-idle' : `is-${probeRoundTone(round.latency_ms)}`}
                />
              ))}
            </span>
            <span className={cn('cw-probe-latency', `is-${probeRoundTone(latest ?? null)}`)}>
              {latest ?? '—'}
            </span>
          </div>
        )
      })}
    </div>
  )
}
