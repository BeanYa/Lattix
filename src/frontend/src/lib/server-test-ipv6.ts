import type { ServerTestCategory, ServerTestEnvironment } from '@/lib/types'

export const IPV6_CATEGORIES: ServerTestCategory[] = [
  'tcp_ipv6',
  'cernet2_ipv6',
  'return_route_ipv6',
]

export function isIpv6Category(category: ServerTestCategory): boolean {
  return IPV6_CATEGORIES.includes(category)
}

export function ipv6Unavailable(environment: ServerTestEnvironment | null | undefined): boolean {
  return environment?.ipv6_available === false
}

export function withoutIpv6Categories<T extends { category: ServerTestCategory }>(items: T[]): T[] {
  return items.filter((item) => !isIpv6Category(item.category))
}
