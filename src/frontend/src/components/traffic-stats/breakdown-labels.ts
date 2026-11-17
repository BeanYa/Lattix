import type { TrafficStatBreakdownRow } from '@/lib/types'

// 非 user/server 维度行的标签前缀（server 行展示 label，缺省回退 #id）。
const DIM_TYPE_LABEL: Record<
  Exclude<TrafficStatBreakdownRow['dim_type'], 'user' | 'server'>,
  string
> = {
  node: '节点',
  endpoint: '端点',
  chain_hop: '链路跳',
  external: '外部出口',
}

// breakdownRowLabel 生成下钻明细行的展示名：external 行来自外部直链出口归属，
// label 为来源链路名称（dim_id 为链路 id），因此显示「外部出口 · 链路名」。
export function breakdownRowLabel(row: TrafficStatBreakdownRow): string {
  if (row.dim_type === 'user') return row.label || `用户 ${row.dim_id.slice(0, 8)}`
  if (row.dim_type === 'server') return row.label || `节点 #${row.dim_id}`
  if (row.dim_type === 'external') {
    return row.label
      ? `${DIM_TYPE_LABEL.external} · ${row.label}`
      : `${DIM_TYPE_LABEL.external} #${row.dim_id}`
  }
  return `${DIM_TYPE_LABEL[row.dim_type]} #${row.dim_id}`
}

export function canDrillBreakdownRow(row: TrafficStatBreakdownRow): boolean {
  return row.dim_type === 'user' || row.dim_type === 'server'
}
