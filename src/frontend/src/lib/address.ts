// 公网地址族判断（§9）：仅用于 UI 分组展示，不做严格校验。
// 含冒号视为 IPv6，纯点分数字视为 IPv4，其余按域名处理。
export function addressFamily(addr: string): 'ipv4' | 'ipv6' | 'domain' {
  if (addr.includes(':')) return 'ipv6'
  if (/^\d+\.\d+\.\d+\.\d+$/.test(addr)) return 'ipv4'
  return 'domain'
}

// isPublicAddress 粗判公网可用性（与后端 isPublicAgentIP 同语义，仅用于候选展示过滤）：
// 剔除回环、链路本地、私网与 CGNAT 段；域名视为可选（由管理员判断）。
export function isPublicAddress(addr: string): boolean {
  const family = addressFamily(addr)
  if (family === 'domain') return true
  if (family === 'ipv6') {
    const lower = addr.toLowerCase()
    if (lower === '::1' || lower === '::') return false
    // fe80::/10 链路本地：fe80–febf
    if (/^fe[89ab]/.test(lower)) return false
    // fc00::/7 ULA
    if (/^f[cd]/.test(lower)) return false
    return true
  }
  const parts = addr.split('.').map(Number)
  if (parts.some((n) => n > 255)) return false
  const [a, b] = parts
  if (a === 0 || a === 10 || a === 127) return false
  if (a === 169 && b === 254) return false // 链路本地
  if (a === 172 && b >= 16 && b <= 31) return false
  if (a === 192 && b === 168) return false
  if (a === 100 && b >= 64 && b <= 127) return false // CGNAT
  if (a >= 224) return false // 组播/保留
  return true
}
