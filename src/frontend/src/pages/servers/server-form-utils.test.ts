import { describe, expect, it } from 'vitest'

import type { ParseExternalLinkResponse } from '@/lib/types'

import {
  aliasAfterParse,
  applyExternalLinkError,
  applyExternalLinkPreview,
  buildServerCreatePayload,
  editExternalLink,
  externalExitPreview,
  externalExitTarget,
  externalLinkForSubmit,
  externalTransportSummary,
  initialExternalLink,
  initialServerCreateForm,
  switchServerType,
  type ExternalLinkState,
  type ServerCreateFormState,
} from './server-form-utils'

const LINK = 'vless://uuid@example.com:443?security=reality&pbk=PBK&sid=abcd#东京 01'

function preview(overrides: Partial<ParseExternalLinkResponse> = {}): ParseExternalLinkResponse {
  return {
    name: '东京 01',
    protocol: 'vless',
    address: 'example.com',
    port: 443,
    transport: 'tcp · reality',
    exit: { protocol: 'vless', address: 'example.com', port: 443, uuid: 'ab****cd' },
    ...overrides,
  }
}

function parsedLink(link = LINK): ExternalLinkState {
  return { ...initialExternalLink(), link, parsedLink: link.trim(), preview: preview() }
}

function createState(overrides: Partial<ServerCreateFormState> = {}): ServerCreateFormState {
  return {
    ...initialServerCreateForm(),
    alias: 'hk-01',
    countryCode: 'HK',
    location: '香港',
    ...overrides,
  }
}

describe('switchServerType', () => {
  it('clears managed-only inputs when switching to the external link type', () => {
    const next = switchServerType(
      createState({ address: '1.2.3.4', portRows: ['10000'], tags: ['hk'], nonDirect: true }),
      'external',
    )
    expect(next.serverType).toBe('external')
    expect(next.address).toBe('')
    expect(next.portRows).toEqual([''])
    expect(next.external.link).toBe('')
    expect(next.external.preview).toBeNull()
    // 公共资料保留，切回托管时无需重填。
    expect(next.alias).toBe('hk-01')
    expect(next.countryCode).toBe('HK')
    expect(next.location).toBe('香港')
    expect(next.tags).toEqual(['hk'])
    expect(next.nonDirect).toBe(true)
  })

  it('drops the external link parse state when switching back to a managed type', () => {
    const external = switchServerType(createState(), 'external')
    const withLink = { ...external, external: parsedLink() }
    const next = switchServerType(withLink, 'direct')
    expect(next.serverType).toBe('direct')
    expect(next.external).toEqual(initialExternalLink())
  })

  it('returns the same state when the type does not change', () => {
    const state = createState({ address: '1.2.3.4' })
    expect(switchServerType(state, 'direct')).toBe(state)
    expect(switchServerType(state, 'nat')).not.toBe(state)
    expect(switchServerType(state, 'nat').address).toBe('1.2.3.4')
  })
})

describe('buildServerCreatePayload', () => {
  it('submits kind/external_link for a parsed external link and no managed fields', () => {
    const state = createState({
      serverType: 'external',
      address: 'ignored.example.com',
      portRows: ['10000'],
      external: parsedLink(),
    })
    const result = buildServerCreatePayload(state)
    expect('body' in result).toBe(true)
    if (!('body' in result)) return
    expect(result.body.kind).toBe('external')
    expect(result.body.external_link).toBe(LINK)
    expect(result.body.alias).toBe('hk-01')
    expect(result.body.country_code).toBe('HK')
    expect(result.body.machine_type).toBeUndefined()
    expect(result.body.address).toBeUndefined()
    expect(result.body.allowed_ports).toBeUndefined()
  })

  it('trims the link before submitting', () => {
    const state = createState({
      serverType: 'external',
      external: parsedLink(`  ${LINK}  `),
    })
    const result = buildServerCreatePayload(state)
    if (!('body' in result)) throw new Error('expected body')
    expect(result.body.external_link).toBe(LINK)
  })

  it('rejects an external link that has not been parsed', () => {
    const result = buildServerCreatePayload(
      createState({ serverType: 'external', external: { ...initialExternalLink(), link: LINK } }),
    )
    expect(result).toEqual({ error: '请先解析链接（链接已修改或尚未解析）' })
  })

  it('rejects an external link edited after parsing', () => {
    const state = createState({
      serverType: 'external',
      external: { ...parsedLink(), link: `${LINK}&edited=1` },
    })
    expect(buildServerCreatePayload(state)).toEqual({
      error: '请先解析链接（链接已修改或尚未解析）',
    })
  })

  it('requires the external link to be present', () => {
    const result = buildServerCreatePayload(
      createState({ serverType: 'external', external: initialExternalLink() }),
    )
    expect(result).toEqual({ error: '分享链接不能为空' })
  })

  it('keeps the managed payload free of external fields', () => {
    const result = buildServerCreatePayload(createState({ address: '1.2.3.4' }))
    if (!('body' in result)) throw new Error('expected body')
    expect(result.body.machine_type).toBe('direct')
    expect(result.body.address).toBe('1.2.3.4')
    expect(result.body.kind).toBeUndefined()
    expect(result.body.external_link).toBeUndefined()
  })

  it('requires a public address for NAT servers', () => {
    expect(buildServerCreatePayload(createState({ serverType: 'nat' }))).toEqual({
      error: 'NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）',
    })
  })
})

describe('externalLinkForSubmit', () => {
  it('requires a link and a matching successful parse for creation', () => {
    expect(externalLinkForSubmit({ link: '', parsedLink: '' })).toEqual({
      error: '分享链接不能为空',
    })
    expect(externalLinkForSubmit({ link: LINK, parsedLink: '' })).toEqual({
      error: '请先解析链接（链接已修改或尚未解析）',
    })
    expect(externalLinkForSubmit({ link: ` ${LINK} `, parsedLink: LINK })).toEqual({
      link: LINK,
    })
  })

  it('accepts an unchanged saved link without re-parsing when editing', () => {
    expect(externalLinkForSubmit({ link: ` ${LINK} `, parsedLink: '', savedLink: LINK })).toEqual({
      link: LINK,
    })
  })

  it('requires a new parse when an edited link differs from the saved one', () => {
    expect(
      externalLinkForSubmit({ link: `${LINK}`, parsedLink: '', savedLink: 'trojan://x@y:443' }),
    ).toEqual({ error: '请先解析链接（链接已修改或尚未解析）' })
  })
})

describe('editExternalLink', () => {
  it('keeps the preview when only surrounding whitespace changes', () => {
    const state = { ...parsedLink(), parsing: true, parseError: '旧错误' }
    const next = editExternalLink(state, `  ${LINK}\n`)
    expect(next.parsedLink).toBe(LINK)
    expect(next.preview?.name).toBe('东京 01')
    expect(next.parsing).toBe(false)
    expect(next.parseError).toBe('')
  })

  it('clears the parse state when the link content changes', () => {
    const next = editExternalLink(parsedLink(), `${LINK}x`)
    expect(next.parsedLink).toBe('')
    expect(next.preview).toBeNull()
    expect(next.link).toBe(`${LINK}x`)
  })

  it('keeps an unparsed state unparsed', () => {
    const next = editExternalLink(initialExternalLink(), LINK)
    expect(next.link).toBe(LINK)
    expect(next.preview).toBeNull()
  })
})

describe('external link state transitions', () => {
  it('applies a parse preview only while the link is unchanged', () => {
    const state = { ...initialExternalLink(), link: LINK, parsing: true }
    const applied = applyExternalLinkPreview(state, LINK, preview())
    expect(applied?.parsedLink).toBe(LINK)
    expect(applied?.preview?.name).toBe('东京 01')
    expect(applied?.parsing).toBe(false)
    expect(applyExternalLinkPreview({ ...state, link: `${LINK}x` }, LINK, preview())).toBeNull()
  })

  it('records parse failures and discards stale ones', () => {
    const state = { ...initialExternalLink(), link: LINK, parsing: true }
    const failed = applyExternalLinkError(state, LINK, '无法解析链接')
    expect(failed?.parseError).toBe('无法解析链接')
    expect(failed?.parsedLink).toBe('')
    expect(failed?.parsing).toBe(false)
    expect(applyExternalLinkError({ ...state, link: `${LINK}x` }, LINK, '无法解析链接')).toBeNull()
  })
})

describe('external display helpers', () => {
  it('fills the alias only when it is empty', () => {
    expect(aliasAfterParse('', '东京 01')).toBe('东京 01')
    expect(aliasAfterParse('  ', '东京 01')).toBe('东京 01')
    expect(aliasAfterParse('自定义', '东京 01')).toBe('自定义')
  })

  it('formats host:port and brackets IPv6 literals', () => {
    expect(externalExitTarget({ address: 'example.com', port: 443 })).toBe('example.com:443')
    expect(externalExitTarget({ address: '2001:db8::1', port: 8443 })).toBe('[2001:db8::1]:8443')
  })

  it('summarizes the transport/security layer like the backend', () => {
    expect(externalTransportSummary({ protocol: 'vless' })).toBe('tcp · none')
    expect(externalTransportSummary({ protocol: 'vmess', network: 'ws', security: 'tls' })).toBe(
      'ws · tls',
    )
    expect(externalTransportSummary({ protocol: 'socks' })).toBe('TCP')
  })

  it('builds a preview from a saved external exit', () => {
    const result = externalExitPreview(
      {
        protocol: 'vless',
        address: 'example.com',
        port: 443,
        network: 'tcp',
        security: 'reality',
        uuid: 'ab****cd',
      },
      '东京 01',
    )
    expect(result.name).toBe('东京 01')
    expect(result.protocol).toBe('vless')
    expect(result.transport).toBe('tcp · reality')
    expect(result.exit.uuid).toBe('ab****cd')
  })
})
