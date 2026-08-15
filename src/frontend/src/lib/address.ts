// 公网地址族判断（§9）：仅用于 UI 分组展示，不做严格校验。
// 含冒号视为 IPv6，纯点分数字视为 IPv4，其余按域名处理。
export function addressFamily(addr: string): 'ipv4' | 'ipv6' | 'domain' {
  if (addr.includes(':')) return 'ipv6'
  if (/^\d+\.\d+\.\d+\.\d+$/.test(addr)) return 'ipv4'
  return 'domain'
}
