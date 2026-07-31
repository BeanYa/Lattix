import { describe, expect, it } from 'vitest'

import { getTemplateSuggestions, validateNameTemplate, type NameTemplateContext } from '@/lib/naming'
import type { Server } from '@/lib/types'

function server(id: number, alias: string, countryCode: string, location: string): Server {
  return {
    id,
    alias,
    country_code: countryCode,
    location,
    address: `${alias.toLowerCase()}.example.com`,
    tags: [],
  } as unknown as Server
}

const relayContext: NameTemplateContext = {
  servers: [
    server(1, 'US Entry', 'US', 'Los Angeles'),
    server(2, 'SG Middle', 'SG', 'Singapore'),
    server(3, 'JP Exit', 'JP', 'Tokyo'),
  ],
  protocol: 'vless',
  port: '8443',
}

describe('multi-hop name templates', () => {
  it('resolves a hop country flag from that server country code', () => {
    expect(validateNameTemplate('{{HOP[1].COUNTRY_FLAG}}', relayContext)).toEqual({
      preview: '🇸🇬',
      error: '',
    })
  })

  it('rejects unscoped server attributes', () => {
    const result = validateNameTemplate('{{COUNTRY_CODE}}', relayContext)
    expect(result.error).toContain('必须使用 ENTRY、EXIT 或 HOP[n] 作用域')
  })

  it('suggests scoped country flags for every hop only', () => {
    for (const index of [0, 1, 2]) {
      const template = `{{HOP[${index}].COUNTRY_F`
      const hopSuggestions = getTemplateSuggestions(template, template.length, relayContext)
      expect(hopSuggestions?.items).toContain(`HOP[${index}].COUNTRY_FLAG`)
    }

    const globalTemplate = '{{COUNTRY_'
    const globalSuggestions = getTemplateSuggestions(globalTemplate, globalTemplate.length, relayContext)
    expect(globalSuggestions).toBeNull()
  })
})
