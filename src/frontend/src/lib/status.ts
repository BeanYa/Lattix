import type { ChainStatus, NodeStatus } from './types'

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
