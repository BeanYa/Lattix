import { useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { humanizeBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { ChainTrafficBucket } from '@/lib/types'

import { roleLabel, type TrafficHistoryController } from './use-traffic-history'

function TrafficHistoryChart({
  buckets,
  range,
}: {
  buckets: Array<ChainTrafficBucket & { date: string }>
  range: 'day' | 'month'
}) {
  const chartRef = useRef<SVGSVGElement>(null)
  const [activePoint, setActivePoint] = useState<{ index: number; y: number } | null>(null)
  const width = 680
  const height = 280
  const plot = { left: 48, right: 16, top: 16, bottom: 38 }
  const plotWidth = width - plot.left - plot.right
  const plotHeight = height - plot.top - plot.bottom
  const peak = Math.max(
    1,
    ...buckets.flatMap((bucket) => [bucket.effective_up, bucket.effective_down]),
  )
  const xAt = (index: number) =>
    plot.left + (buckets.length <= 1 ? plotWidth / 2 : (index / (buckets.length - 1)) * plotWidth)
  const yAt = (value: number) => plot.top + (1 - value / peak) * plotHeight
  const points = (key: 'effective_up' | 'effective_down') =>
    buckets
      .map((bucket, index) => `${xAt(index).toFixed(1)},${yAt(bucket[key]).toFixed(1)}`)
      .join(' ')
  const tickStep = Math.max(1, Math.ceil((buckets.length - 1) / 6))
  const xTicks = buckets
    .map((bucket, index) => ({ bucket, index }))
    .filter(({ index }) => index === 0 || index === buckets.length - 1 || index % tickStep === 0)
  const labelDate = (date: string) => (range === 'day' ? date.slice(5) : date)
  const setPointFromPointer = (clientX: number, clientY: number) => {
    const bounds = chartRef.current?.getBoundingClientRect()
    if (!bounds || buckets.length === 0) return
    const svgX = ((clientX - bounds.left) / bounds.width) * width
    const svgY = ((clientY - bounds.top) / bounds.height) * height
    const ratio = Math.min(1, Math.max(0, (svgX - plot.left) / plotWidth))
    setActivePoint({
      index: Math.round(ratio * (buckets.length - 1)),
      y: Math.min(plot.top + plotHeight, Math.max(plot.top, svgY)),
    })
  }
  const moveActivePoint = (offset: number) => {
    setActivePoint((current) => ({
      index: Math.min(
        buckets.length - 1,
        Math.max(0, (current?.index ?? buckets.length - 1) + offset),
      ),
      y: current?.y ?? plot.top + plotHeight / 2,
    }))
  }
  const activeBucket = activePoint ? buckets[activePoint.index] : null

  return (
    <section className="cg-chain-chart">
      <div className="cg-chain-chart-legend">
        <span>相对用量（当前视图峰值 = 100%）</span>
        <div className="cg-chain-chart-legend-keys">
          <span>
            <i className="cg-chain-chart-key-dot is-up" />
            上传
          </span>
          <span>
            <i className="cg-chain-chart-key-dot is-down" />
            下载
          </span>
        </div>
      </div>
      <div className="cg-chain-chart-frame">
        <svg
          ref={chartRef}
          viewBox={`0 0 ${width} ${height}`}
          className="cg-chain-chart-svg"
          role="img"
          aria-label="链路上传和下载流量趋势，使用左右方向键查看日期数据"
          tabIndex={0}
          onPointerMove={(event) => setPointFromPointer(event.clientX, event.clientY)}
          onPointerLeave={() => setActivePoint(null)}
          onFocus={() =>
            setActivePoint({ index: buckets.length - 1, y: plot.top + plotHeight / 2 })
          }
          onBlur={() => setActivePoint(null)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
              event.preventDefault()
              moveActivePoint(event.key === 'ArrowLeft' ? -1 : 1)
            }
          }}
        >
          {[0, 25, 50, 75, 100].map((percent) => {
            const y = plot.top + (1 - percent / 100) * plotHeight
            return (
              <g key={percent}>
                <line
                  x1={plot.left}
                  x2={plot.left + plotWidth}
                  y1={y}
                  y2={y}
                  strokeWidth="1"
                  vectorEffect="non-scaling-stroke"
                  className="cg-chain-chart-grid"
                />
                <text
                  x={plot.left - 8}
                  y={y + 4}
                  textAnchor="end"
                  fontSize="11"
                  className="cg-chain-chart-tick"
                >
                  {percent}%
                </text>
              </g>
            )
          })}
          {xTicks.map(({ bucket, index }) => (
            <text
              key={`${bucket.date}-${index}`}
              x={xAt(index)}
              y={height - 10}
              textAnchor={index === 0 ? 'start' : index === buckets.length - 1 ? 'end' : 'middle'}
              fontSize="11"
              className="cg-chain-chart-tick"
            >
              {labelDate(bucket.date)}
            </text>
          ))}
          <polyline
            points={points('effective_up')}
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            vectorEffect="non-scaling-stroke"
            className="cg-chain-chart-line is-up"
          />
          <polyline
            points={points('effective_down')}
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            vectorEffect="non-scaling-stroke"
            className="cg-chain-chart-line is-down"
          />
          {activePoint && activeBucket ? (
            <>
              <line
                x1={xAt(activePoint.index)}
                x2={xAt(activePoint.index)}
                y1={plot.top}
                y2={plot.top + plotHeight}
                strokeWidth="1"
                strokeDasharray="3 3"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-cross"
              />
              <line
                x1={plot.left}
                x2={plot.left + plotWidth}
                y1={activePoint.y}
                y2={activePoint.y}
                strokeWidth="1"
                strokeDasharray="3 3"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-cross"
              />
              <circle
                cx={xAt(activePoint.index)}
                cy={yAt(activeBucket.effective_up)}
                r="4"
                stroke="currentColor"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-line is-up cg-chain-chart-dot"
              />
              <circle
                cx={xAt(activePoint.index)}
                cy={yAt(activeBucket.effective_down)}
                r="4"
                stroke="currentColor"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                className="cg-chain-chart-line is-down cg-chain-chart-dot"
              />
            </>
          ) : null}
        </svg>
        {activeBucket && activePoint ? (
          <div
            className={cn(
              'cg-chain-chart-tip',
              activePoint.index < buckets.length / 2 ? 'is-right' : 'is-left',
            )}
          >
            <div className="cg-chain-chart-tip-date">{activeBucket.date}</div>
            <div className="cg-chain-chart-tip-row">
              <span>上传</span>
              <span>{humanizeBytes(activeBucket.effective_up)}</span>
            </div>
            <div className="cg-chain-chart-tip-row">
              <span>下载</span>
              <span>{humanizeBytes(activeBucket.effective_down)}</span>
            </div>
          </div>
        ) : null}
      </div>
    </section>
  )
}

export function TrafficHistoryDialog({ controller }: { controller: TrafficHistoryController }) {
  const {
    trafficChain,
    trafficHopId,
    trafficRange,
    trafficLoading,
    displayedTrafficHistory,
    close,
    selectRange,
    selectHop,
  } = controller
  return (
    <Dialog open={trafficChain !== null} onOpenChange={(next) => !next && close()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{trafficChain?.name} · 流量历史</DialogTitle>
        </DialogHeader>
        {trafficChain ? (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-2">
              <div className="cg-chain-range">
                {(['day', 'month'] as const).map((range) => (
                  <Button
                    key={range}
                    type="button"
                    size="sm"
                    variant={trafficRange === range ? 'secondary' : 'ghost'}
                    onClick={() => selectRange(range)}
                  >
                    {range === 'day' ? '日' : '月'}
                  </Button>
                ))}
              </div>
              <Select
                value={String(trafficHopId)}
                items={[
                  { value: '0', label: '链路（出口权威）' },
                  ...trafficChain.hops.map((hop) => ({
                    value: String(hop.id),
                    label: `${roleLabel[hop.role]} · ${hop.server_alias || `Server #${hop.server_id}`}`,
                  })),
                ]}
                onValueChange={(value) => selectHop(Number(value))}
              >
                <SelectTrigger className="w-48">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">链路（出口权威）</SelectItem>
                  {trafficChain.hops.map((hop) => (
                    <SelectItem key={hop.id} value={String(hop.id)}>
                      {roleLabel[hop.role]} · {hop.server_alias || `Server #${hop.server_id}`}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {trafficLoading ? (
              <p className="cg-chain-dialog-note">加载中…</p>
            ) : displayedTrafficHistory.length === 0 ? (
              <p className="cg-chain-dialog-note">暂无流量记录</p>
            ) : (
              <TrafficHistoryChart buckets={displayedTrafficHistory} range={trafficRange} />
            )}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
