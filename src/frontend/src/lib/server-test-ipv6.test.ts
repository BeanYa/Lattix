import { describe, expect, it } from 'vitest'

import {
  IPV6_CATEGORIES,
  isIpv6Category,
  ipv6Unavailable,
  withoutIpv6Categories,
} from '@/lib/server-test-ipv6'
import type { ServerTestCategoryResult, ServerTestEnvironment } from '@/lib/types'

describe('server-test-ipv6', () => {
  it('ipv6Unavailable only for explicit false', () => {
    expect(ipv6Unavailable(undefined)).toBe(false)
    expect(ipv6Unavailable(null)).toBe(false)
    expect(ipv6Unavailable({} as ServerTestEnvironment)).toBe(false)
    expect(ipv6Unavailable({ ipv6_available: true } as ServerTestEnvironment)).toBe(false)
    expect(ipv6Unavailable({ ipv6_available: false } as ServerTestEnvironment)).toBe(true)
  })

  it('isIpv6Category covers the three ipv6 categories only', () => {
    for (const category of IPV6_CATEGORIES) {
      expect(isIpv6Category(category)).toBe(true)
    }
    expect(isIpv6Category('tcp_ipv4')).toBe(false)
    expect(isIpv6Category('ip_quality')).toBe(false)
  })

  it('withoutIpv6Categories filters ipv6 categories and keeps others', () => {
    const categories: ServerTestCategoryResult[] = [
      { category: 'ip_quality', status: 'available' },
      { category: 'tcp_ipv4', status: 'available' },
      { category: 'tcp_ipv6', status: 'unavailable' },
      { category: 'return_route_ipv6', status: 'unavailable' },
    ]
    const kept = withoutIpv6Categories(categories).map((item) => item.category)
    expect(kept).toEqual(['ip_quality', 'tcp_ipv4'])
  })
})
