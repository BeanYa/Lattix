import { useEffect, useMemo, useState } from 'react'

import { Chart } from '@/components/echarts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { humanizeBytes } from '@/lib/format'
import { useTheme } from '@/lib/theme-context'
import type { TrafficStatBreakdownRow } from '@/lib/types'
import { chartThemeColors } from '@/pages/costs-shared'
import { buildTrafficBarOption } from '@/pages/runtime/traffic-chart'

import {
  TRAFFIC_RANGE_GRANULARITY,
  type TrafficStatsDialogController,
  type TrafficStatsRangeKey,
} from './use-traffic-stats-dialog'

const RANGE_ORDER: TrafficStatsRangeKey[] = ['24h', '7d', '30d', 'custom']
const RANGE_LABEL: Record<TrafficStatsRangeKey, string> = {
  '24h': '近24h',
  '7d': '近7天',
  '30d': '近30天',
  custom: '自定义',
}

// 非 user/server 维度行的标签前缀（server 行展示 label，缺省回退 #id）。
const DIM_TYPE_LABEL: Record<
  Exclude<TrafficStatBreakdownRow['dim_type'], 'user' | 'server'>,
  string
> = {
  node: '节点',
  endpoint: '端点',
  chain_hop: '链路跳',
}

function rowLabel(row: TrafficStatBreakdownRow): string {
  if (row.dim_type === 'user') return row.label || `用户 ${row.dim_id.slice(0, 8)}`
  if (row.dim_type === 'server') return row.label || `节点 #${row.dim_id}`
  return `${DIM_TYPE_LABEL[row.dim_type]} #${row.dim_id}`
}

function canDrill(row: TrafficStatBreakdownRow): boolean {
  return row.dim_type === 'user' || row.dim_type === 'server'
}

/**
 * TrafficStatsDialog 为流量统计下钻弹窗（用法照 TrafficHistoryDialog：
 * open 由 scope 非空驱动，onOpenChange 关闭调 close）。
 * 内容：标题 + 区间 Tabs（自定义展开两个日期输入）+ 时序堆叠柱图 + 分解表（可下钻行带「下钻 ›」）。
 */
export function TrafficStatsDialog({ controller }: { controller: TrafficStatsDialogController }) {
  const {
    scope,
    rangeKey,
    customRange,
    setCustomRange,
    points,
    rows,
    loading,
    close,
    setRange,
    drillRow,
  } = controller

  // 图表颜色在主题切换后从 CSS 变量重新解析（同 Costs/EstimatedCostsTab）。
  const { theme } = useTheme()
  const [chartColors, setChartColors] = useState(() => chartThemeColors())
  useEffect(() => {
    setChartColors(chartThemeColors())
  }, [theme])

  const chartOption = useMemo(
    () => buildTrafficBarOption(points, TRAFFIC_RANGE_GRANULARITY[rangeKey], chartColors),
    [points, rangeKey, chartColors],
  )

  const title =
    scope === null
      ? ''
      : scope.kind === 'server'
        ? scope.serverName || `节点 #${scope.serverId}`
        : scope.userName || `用户 ${scope.userUuid?.slice(0, 8) ?? ''}`

  return (
    <Dialog open={scope !== null} onOpenChange={(next) => !next && close()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{title} · 流量统计</DialogTitle>
        </DialogHeader>
        {scope ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Tabs
                value={rangeKey}
                onValueChange={(value) => setRange(value as TrafficStatsRangeKey)}
              >
                <TabsList>
                  {RANGE_ORDER.map((range) => (
                    <TabsTrigger key={range} value={range}>
                      {RANGE_LABEL[range]}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
              {rangeKey === 'custom' ? (
                <div className="flex items-center gap-2">
                  <Input
                    type="date"
                    aria-label="起始日期"
                    className="w-40"
                    max={customRange.to}
                    value={customRange.from}
                    onChange={(event) =>
                      setCustomRange({ from: event.target.value, to: customRange.to })
                    }
                  />
                  <span className="text-sm text-muted-foreground">至</span>
                  <Input
                    type="date"
                    aria-label="结束日期"
                    className="w-40"
                    min={customRange.from}
                    value={customRange.to}
                    onChange={(event) =>
                      setCustomRange({ from: customRange.from, to: event.target.value })
                    }
                  />
                </div>
              ) : null}
            </div>
            {loading ? (
              <p className="text-sm text-muted-foreground">加载中…</p>
            ) : points.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无流量记录</p>
            ) : (
              <Chart option={chartOption} className="h-72 w-full" />
            )}
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称 / 维度</TableHead>
                  <TableHead className="text-right">↑</TableHead>
                  <TableHead className="text-right">↓</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={3} className="text-muted-foreground">
                      暂无数据
                    </TableCell>
                  </TableRow>
                ) : (
                  rows.map((row) => (
                    <TableRow key={`${row.dim_type}-${row.dim_id}`}>
                      <TableCell>
                        <span className="inline-flex items-center gap-2">
                          {rowLabel(row)}
                          {canDrill(row) ? (
                            <Button
                              type="button"
                              size="sm"
                              variant="ghost"
                              onClick={() => drillRow(row)}
                            >
                              下钻 ›
                            </Button>
                          ) : null}
                        </span>
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {humanizeBytes(row.up)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {humanizeBytes(row.down)}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
