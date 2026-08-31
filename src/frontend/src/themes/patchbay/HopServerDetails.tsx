import { ArrowDownIcon, ArrowUpIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { formatDateTime } from '@/lib/format'
import { isServerOnline, serverConnectionLabel } from '@/lib/server-state'
import { useTimezone } from '@/lib/timezone'
import type { Server } from '@/lib/types'

import { serverReadout } from './server-readout'

/** Read-only server-page data, kept inside the topology's existing socket geometry. */
export function HopServerDetails({
  server,
  fallbackAddress,
}: {
  server?: Server
  fallbackAddress: string
}) {
  const { timezone } = useTimezone()
  const view = serverReadout(server, fallbackAddress)
  const metrics = server?.metrics
  const online = isServerOnline(server)
  const updated = formatDateTime(metrics?.updated_at, timezone)
  const connection = server ? serverConnectionLabel(server.connection_state) : '服务器未找到'

  return (
    <div className="pb-hop-details" data-historical={Boolean(metrics && !online)}>
      <div className="pb-hop-location">
        <CountryFlag code={server?.country_code ?? ''} />
        <span>{view.location}</span>
        <span className="pb-hop-agent" data-online={online}>
          Agent {connection}
        </span>
      </div>
      <dl className="pb-hop-address">
        <dt>域名 / IP</dt>
        <dd title={view.addresses.join('\n') || view.address}>
          <span>{view.address}</span>
          {view.addresses.length > 1 ? (
            <details>
              <summary>另 {view.addresses.length - 1} 个地址</summary>
              {view.addresses.slice(1).map((address) => (
                <span key={address}>{address}</span>
              ))}
            </details>
          ) : null}
        </dd>
      </dl>
      <div className="pb-hop-telemetry">
        <span>{metrics ? (online ? '最近遥测' : '历史数据 · 非实时') : '等待遥测上报'}</span>
        {metrics ? (
          <time dateTime={metrics.updated_at} title={updated}>
            {online ? updated.split(' ').at(-1) : updated}
          </time>
        ) : null}
      </div>
      <dl className="pb-hop-resources">
        {view.resources.map(({ label, percent, detail }) => (
          <div
            key={label}
            className="pb-hop-resource"
            data-warning={online && percent !== null && percent >= 80}
          >
            <dt>{label}</dt>
            <dd>{percent === null ? '--' : `${percent.toFixed(1)}%`}</dd>
            <span className="pb-hop-gauge" aria-hidden="true">
              <span style={{ transform: `scaleX(${(percent ?? 0) / 100})` }} />
            </span>
            <small>{detail}</small>
          </div>
        ))}
      </dl>
      <dl className="pb-hop-network">
        <div>
          <dt>
            <ArrowUpIcon aria-hidden="true" />
            上行
          </dt>
          <dd>{view.tx}</dd>
        </div>
        <div>
          <dt>
            <ArrowDownIcon aria-hidden="true" />
            下行
          </dt>
          <dd>{view.rx}</dd>
        </div>
      </dl>
      <dl className="pb-hop-system">
        <div>
          <dt>延迟</dt>
          <dd>{view.latency}</dd>
        </div>
        <div>
          <dt>运行时间</dt>
          <dd>{view.uptime}</dd>
        </div>
      </dl>
      <div className="pb-hop-versions">
        <span title={`Agent ${server?.agent_version || '未上报'}`}>
          Agent {server?.agent_version || '--'}
        </span>
        <span title={`Xray ${server?.xray_version || '未上报'}`}>
          Xray {server?.xray_version || '--'}
        </span>
      </div>
    </div>
  )
}
