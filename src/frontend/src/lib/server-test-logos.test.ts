import { describe, expect, it } from 'vitest'

import {
  connectivitySiteLogo,
  connectivitySiteSlug,
  mediaMethodText,
  mediaRegionText,
  mediaStatusText,
  mediaVendorDisplayName,
  mediaVendorLogo,
  orderedMediaVendors,
} from '@/lib/server-test-logos'

describe('server-test-logos', () => {
  it('extracts site slug from connectivity target id', () => {
    expect(connectivitySiteSlug('connectivity:us:chatgpt')).toBe('chatgpt')
    expect(connectivitySiteSlug('connectivity:gl:appledash')).toBe('appledash')
    expect(connectivitySiteSlug('bogus')).toBe('')
  })

  it('resolves bundled logos for known slugs and vendors', () => {
    expect(connectivitySiteLogo('baidu')).toBe('/logos/connectivity/baidu.ico')
    expect(connectivitySiteLogo('nope')).toBeUndefined()
    expect(mediaVendorLogo('netflix')).toBe('/logos/media/netflix.ico')
    expect(mediaVendorLogo('amazonprimevideo')).toBe('/logos/media/amazonpv.svg')
  })

  it('orders media vendors like the reference script and merges duplicate keys', () => {
    const ordered = orderedMediaVendors({
      ChatGPT: { Status: 'Yes', Region: 'US', Type: 'Native' },
      TikTok: { Status: 'No', Region: '', Type: '' },
      DisneyPlus: { Status: 'Yes', Region: 'JP', Type: 'Native' },
      'Disney+': { Status: 'Yes', Region: '', Type: '' },
      Reddit: { Status: 'Yes', Region: 'US', Type: 'Native' },
      Netflix: { Status: 'Restricted', Region: 'US', Type: 'Native' },
      Youtube: { Status: 'Yes', Region: 'US', Type: 'Native' },
      AmazonPrimeVideo: { Status: 'Yes', Region: 'US', Type: 'Native' },
      Spotify: { Status: 'Block', Region: '', Type: '' },
      FutureStream: { Status: 'Yes', Region: 'US', Type: 'Native' },
    })
    expect(ordered.map((entry) => entry.key)).toEqual([
      'tiktok',
      'disneyplus',
      'netflix',
      'youtube',
      'amazonprimevideo',
      'reddit',
      'chatgpt',
      'spotify',
      'futurestream',
    ])
    // 同键去重保留数据更完整的一条
    const disney = ordered.find((entry) => entry.key === 'disneyplus')
    expect(disney?.rawName).toBe('DisneyPlus')
    expect(disney?.status.Region).toBe('JP')
  })

  it('maps unlock status to label and tone', () => {
    expect(mediaStatusText({ Status: 'Yes' })).toEqual({ label: '解锁', tone: 'success' })
    expect(mediaStatusText({ Status: 'Restricted' })).toEqual({ label: '仅自制', tone: 'warning' })
    expect(mediaStatusText({ Status: 'Block' })).toEqual({ label: '屏蔽', tone: 'destructive' })
    expect(mediaStatusText({ Status: 'Unsupported' })).toEqual({ label: '不支持', tone: 'muted' })
    expect(mediaStatusText({ Status: '' })).toEqual({ label: '未知', tone: 'muted' })
  })

  it('sanitizes garbage region/type fragments from stored reports', () => {
    expect(mediaRegionText({ Region: '},PROGRAM:{DATATYPE:A.MINERVA' })).toBe('-')
    expect(mediaRegionText({ Region: 'jp' })).toBe('JP')
    expect(mediaRegionText({ Region: '' })).toBe('-')
    expect(mediaMethodText({ Type: 'Native' })).toBe('原生')
    expect(mediaMethodText({ Type: 'DNS' })).toBe('DNS')
    expect(mediaMethodText({ Type: '},PROGRAM:{X' })).toBe('-')
  })

  it('maps vendor display names', () => {
    expect(mediaVendorDisplayName('disneyplus', 'DisneyPlus')).toBe('Disney+')
    expect(mediaVendorDisplayName('amazonprimevideo', 'AMAZONPRIMEVIDEO')).toBe(
      'Amazon Prime Video',
    )
    expect(mediaVendorDisplayName('unknownvendor', 'UnknownVendor')).toBe('UnknownVendor')
  })
})
