import { describe, expect, it } from 'vitest'

import { providerWebsiteUrl } from '@/lib/provider-website'

describe('providerWebsiteUrl', () => {
  it('returns null for missing or blank urls', () => {
    expect(providerWebsiteUrl(null)).toBeNull()
    expect(providerWebsiteUrl(undefined)).toBeNull()
    expect(providerWebsiteUrl('')).toBeNull()
    expect(providerWebsiteUrl('   ')).toBeNull()
  })

  it('keeps http/https urls verbatim and trims whitespace', () => {
    expect(providerWebsiteUrl('https://example.com/console')).toBe('https://example.com/console')
    expect(providerWebsiteUrl('http://example.com')).toBe('http://example.com')
    expect(providerWebsiteUrl('  https://example.com  ')).toBe('https://example.com')
  })

  it('prepends https when the scheme is missing', () => {
    expect(providerWebsiteUrl('example.com')).toBe('https://example.com')
    expect(providerWebsiteUrl(' example.com/console ')).toBe('https://example.com/console')
  })
})
