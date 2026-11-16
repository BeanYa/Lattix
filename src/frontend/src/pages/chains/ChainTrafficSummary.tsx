import { ActivityIcon, ArrowDownIcon, ArrowUpIcon } from 'lucide-react'

import { humanizeBytes } from '@/lib/format'

// TrafficSummary 是链路详情右侧的累计流量读数板；结果预览复用同一版式，
// 未部署时各读数显示 "--"、仅保留流量倍率。
export function TrafficSummary({
  up,
  down,
  rawUp,
  rawDown,
  multiplier,
  today,
}: {
  up?: number
  down?: number
  rawUp?: number
  rawDown?: number
  multiplier?: string
  today?: number
}) {
  const hasTraffic = up !== undefined && down !== undefined
  const adjusted =
    hasTraffic && rawUp !== undefined && rawDown !== undefined && (rawUp !== up || rawDown !== down)
  return (
    <div className="cg-chain-traffic" aria-label="累计流量">
      <div className="cg-chain-traffic-item is-up">
        <span className="cg-chain-traffic-label">
          <ArrowUpIcon />
          累计上传
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(up ?? 0) : '--'}
        </strong>
        {adjusted ? (
          <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawUp ?? 0)}</span>
        ) : null}
      </div>
      <div className="cg-chain-traffic-item is-down">
        <span className="cg-chain-traffic-label">
          <ArrowDownIcon />
          累计下载
        </span>
        <strong className="cg-chain-traffic-value">
          {hasTraffic ? humanizeBytes(down ?? 0) : '--'}
        </strong>
        {adjusted ? (
          <span className="cg-chain-traffic-raw">原始 {humanizeBytes(rawDown ?? 0)}</span>
        ) : null}
      </div>
      {today !== undefined ? (
        <div className="cg-chain-traffic-item is-today" aria-label="今日负载">
          <span className="cg-chain-traffic-label">
            <ActivityIcon />
            今日负载
          </span>
          <strong className="cg-chain-traffic-value">
            {today > 0 ? humanizeBytes(today) : '--'}
          </strong>
        </div>
      ) : null}
      {multiplier ? (
        <span className="cg-chain-traffic-multiplier">流量倍率 x{multiplier}</span>
      ) : null}
    </div>
  )
}
