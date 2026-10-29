import type { Server, ServerConnectionState } from './types'

export function isServerOnline(
  server: Pick<Server, 'connection_state'> | null | undefined,
): boolean {
  return server?.connection_state === 'online'
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
    case 'offline':
      return '离线'
    case 'auth_rejected':
      return '凭据被拒绝'
  }
}
