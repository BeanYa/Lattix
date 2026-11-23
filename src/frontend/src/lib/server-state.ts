import type { Server, ServerConnectionState } from './types'

export function isServerOnline(
  server: Pick<Server, 'connection_state'> | null | undefined,
): boolean {
  return server?.connection_state === 'online'
}

export function isServerUpdating(
  server: Pick<Server, 'connection_state'> | null | undefined,
): boolean {
  return server?.connection_state === 'updating'
}

// isServerBanned 判定服务器已被 Ban 巡检判定（§告警分类 Ban告警）：ban-watch 状态
// banned 且未标记非直连——非直连（大陆不可直连）服务器三网丢包属预期，不参与评估。
export function isServerBanned(
  server: Pick<Server, 'ban_watch' | 'non_direct'> | null | undefined,
): boolean {
  return server?.ban_watch?.state === 'banned' && !server.non_direct
}

export function serverConnectionLabel(state: ServerConnectionState): string {
  switch (state) {
    case 'never_connected':
      return '未连接'
    case 'connecting':
      return '连接中'
    case 'reconnecting':
      return '重连中'
    case 'online':
      return '在线'
    case 'updating':
      return '更新中'
    case 'offline':
      return '离线'
    case 'auth_rejected':
      return '凭据被拒绝'
    case 'external':
      return '外部节点'
  }
}

// supportsAgentActions 判定服务器可用的操作集：未托管外部直链节点（kind=external）
// 只有分享链接派生的出口，没有 Agent，安装/轮换凭证/修复/清理/重建/升级/
// 服务器测试/设置覆盖等动作一律不可用（后端返回 400）。
export function supportsAgentActions(server: Pick<Server, 'kind'>): boolean {
  return server.kind !== 'external'
}
