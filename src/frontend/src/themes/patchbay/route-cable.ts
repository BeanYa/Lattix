export type RouteTone = 'active' | 'degraded' | 'inactive'

export function getCableState(route: RouteTone, source: RouteTone, target: RouteTone) {
  if (source === 'degraded' || target === 'degraded') {
    return { tone: 'degraded', label: '节点异常' } as const
  }
  if (source === 'inactive' || target === 'inactive') {
    return { tone: 'inactive', label: '等待部署' } as const
  }
  if (route === 'active') {
    return { tone: 'active', label: '链路正常' } as const
  }
  return { tone: 'inactive', label: route === 'degraded' ? '待恢复' : '待确认' } as const
}
