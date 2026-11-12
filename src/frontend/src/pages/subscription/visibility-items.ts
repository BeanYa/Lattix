export interface VisibilityLink {
  id: string
  label: string
  /** 入口角色：后端返回「入口」为中转链路，「服务器」为单跳直连链路。 */
  entry?: string
}

export interface VisibilityExternalSubscription extends VisibilityLink {
  nodes: string[]
}

/** 订阅项类型：chain 既可以是中转链路，也可以是单跳直连链路，必须读后端下发的
 * entry（「服务器」= 单跳直连）；仅按 ID 前缀会把直连链路误标为中转。 */
export function visibilityItemKind(item: VisibilityLink): string {
  if (item.id.startsWith('node:')) return '直连节点'
  if (item.id.startsWith('chain:')) {
    return item.entry === '服务器' ? '直连链路' : '中转链路'
  }
  if (item.id.startsWith('external:')) return '外部订阅'
  return '订阅项'
}
