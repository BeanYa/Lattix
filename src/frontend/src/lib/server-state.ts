import type { Server, ServerConnectionState } from './types'

export function isServerOnline(
  server: Pick<Server, 'connection_state'> | null | undefined,
): boolean {
  return server?.connection_state === 'online'
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
