import type { ExternalExit } from '@/lib/types'

// 未托管外部直链出口的展示派生（画布节点卡 / 细节摘要 / 链路列表共用）。
// 出口规格由后端随服务器 DTO（external_exit）或链路修订（service_external）下发，
// 凭据可能已被后端打码；这里再做一次幂等打码，避免原始凭据出现在只读摘要里。

export const EXTERNAL_PROTOCOL_LABELS: Record<ExternalExit['protocol'], string> = {
  vless: 'VLESS',
  vmess: 'VMess',
  trojan: 'Trojan',
  shadowsocks: 'Shadowsocks',
  socks: 'SOCKS5',
  http: 'HTTP',
}

/** 外部出口协议是否带传输/安全层参数（ss/socks/http 为纯 TCP 承载）。 */
export function externalExitHasTransport(exit: ExternalExit): boolean {
  return exit.protocol === 'vless' || exit.protocol === 'vmess' || exit.protocol === 'trojan'
}

/** 节点卡摘要：协议 · 传输/安全层首选（如 socks · TCP、vless · reality）。 */
export function externalExitSummary(exit: ExternalExit): string {
  const layer = externalExitHasTransport(exit)
    ? exit.security && exit.security !== 'none'
      ? exit.security
      : exit.network || 'tcp'
    : 'TCP'
  return `${exit.protocol} · ${layer}`
}

/** 细节摘要：传输 · 安全层（无传输层协议回退纯 TCP）。 */
export function externalExitTransport(exit: ExternalExit): string {
  if (!externalExitHasTransport(exit)) return 'TCP（无传输层）'
  return `${exit.network || 'tcp'} · ${exit.security || 'none'}`
}

/** 与后端 maskCredential 一致：长度 ≤4 全打码，否则保留首尾各 2 位（对已打码值幂等）。 */
export function maskExternalCredential(value: string): string {
  if (value.length <= 4) return '****'
  return `${value.slice(0, 2)}****${value.slice(-2)}`
}

/** 只读摘要的凭据字段：后端已打码时保持原样，未打码时兜底打码。 */
export function externalExitCredentialFields(
  exit: ExternalExit,
): { label: string; value: string }[] {
  const fields: { label: string; value: string }[] = []
  if (exit.uuid) fields.push({ label: 'UUID', value: maskExternalCredential(exit.uuid) })
  if (exit.username) fields.push({ label: '用户名', value: exit.username })
  if (exit.password) fields.push({ label: '密码', value: maskExternalCredential(exit.password) })
  if (exit.method) fields.push({ label: '加密方式', value: exit.method })
  if (exit.cipher) fields.push({ label: 'cipher', value: exit.cipher })
  return fields
}
