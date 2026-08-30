import type { ChainStatus, LogSeverity, NodeStatus } from './types'

/** cg-status 贴纸语义：lime=正常/在线，blue=部署流程中，red=异常/失败，muted=其他。 */
export type CgStatusTone = 'is-lime' | 'is-blue' | 'is-red' | 'is-muted'

export interface CgStatusStyle {
  label: string
  cg: CgStatusTone
  /** loading=true 表示部署流程中，状态标记显示旋转 loading 图标。 */
  loading: boolean
}

/** chainStatusStyle 链路状态 → 展示样式（中文标签/cg 色调/是否部署中）。 */
export const chainStatusStyle: Record<ChainStatus, CgStatusStyle> = {
  active: { label: '正常', cg: 'is-lime', loading: false },
  applying: { label: '部署中', cg: 'is-blue', loading: true },
  failed: { label: '异常', cg: 'is-red', loading: false },
  pending: { label: '部署中', cg: 'is-blue', loading: true },
  degraded: { label: '降级', cg: 'is-red', loading: false },
  waiting_for_agent: { label: '等待 Agent', cg: 'is-blue', loading: true },
  active_unconfirmed: { label: '已强制发布', cg: 'is-muted', loading: false },
  active_failed: { label: '发布后失败', cg: 'is-red', loading: false },
  cleanup_pending: { label: '等待清理', cg: 'is-blue', loading: true },
  invalid: { label: '已失效', cg: 'is-red', loading: false },
  deleted: { label: '已删除', cg: 'is-muted', loading: false },
}

/** hopStatusStyle 节点（跳）状态 → 展示样式。 */
export const hopStatusStyle: Record<NodeStatus, CgStatusStyle> = {
  active: { label: '正常', cg: 'is-lime', loading: false },
  applying: { label: '部署中', cg: 'is-blue', loading: true },
  failed: { label: '异常', cg: 'is-red', loading: false },
  pending: { label: '部署中', cg: 'is-blue', loading: true },
}

/** severityTone 日志级别 → cg-status 色调。 */
export function severityTone(severity: LogSeverity): CgStatusTone {
  if (severity === 'error') return 'is-red'
  if (severity === 'info') return 'is-blue'
  return 'is-muted'
}

/** serverTestStatusLabel 服务器测试任务/条目状态 → 中文标签；未知状态原样返回。 */
export function serverTestStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    queued: '等待 Agent',
    accepted: 'Agent 已接收',
    running: '正在测试',
    succeeded: '测试完成',
    completed_with_errors: '部分项目异常',
    failed: '测试失败',
    pending: '等待中',
    available: '可用',
    limited: '部分可用',
    unavailable: '不可用',
    provider_access_unavailable: '无公开访问方式',
    clean: '正常',
    listed: '已列入名单',
  }
  return labels[status] ?? status
}

/** serverTestStatusBadge 服务器测试状态 → Badge 变体。 */
export function serverTestStatusBadge(status: string): 'destructive' | 'secondary' | 'outline' {
  if (status === 'failed' || status === 'unavailable' || status === 'listed') return 'destructive'
  if (status === 'succeeded' || status === 'available' || status === 'clean') return 'secondary'
  return 'outline'
}

/** earthLinkColorKey 地球链路状态 → 调色板键；active/degraded/failed 之外一律按 pending。 */
export function earthLinkColorKey(
  status: string,
): 'linkActive' | 'linkDegraded' | 'linkFailed' | 'linkPending' {
  if (status === 'active') return 'linkActive'
  if (status === 'degraded') return 'linkDegraded'
  if (status === 'failed') return 'linkFailed'
  return 'linkPending'
}
