import type { Server } from '@/lib/types'

import type { CanvasNode, CanvasWarning } from './chain-canvas'

/** 空悬链路文案：与画布警告同源，结果预览与拓扑校验共用。 */
export function chainWarningText(warning: CanvasWarning, serverById: Map<number, Server>): string {
  const name = (node: CanvasNode) => serverById.get(node.serverId)?.alias ?? '#' + node.serverId
  if (!warning.path || warning.path.length === 0) return warning.message
  const route = warning.path.map(name).join(' → ')
  if (warning.code === 'entry_unreachable') {
    const tail = warning.reachesInternet ? ' → Internet' : ''
    return '不会生成：' + route + tail + '（' + name(warning.path[0]) + ' 未接入流量入口）'
  }
  if (warning.code === 'exit_unreachable') {
    const last = warning.path[warning.path.length - 1]
    return '不会生成：' + route + '（' + name(last) + ' 未连接到 Internet）'
  }
  return '不会生成：' + route
}
