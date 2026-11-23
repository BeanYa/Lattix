import { describe, expect, it } from 'vitest'

import type { ExternalExit, ParseExternalLinkResponse } from '@/lib/types'

import {
  defaultExternalExitForm,
  externalExitFormFromExit,
  externalExitFormPayload,
  externalExitFormError,
} from './external-exit-form'
import {
  aliasAfterParse,
  applyExternalLinkError,
  applyExternalLinkPreview,
  buildServerCreatePayload,
  buildServerEditPayload,
  defaultBilling,
  defaultTraffic,
  editExternalLink,
  externalExitTarget,
  externalTransportSummary,
  initialExternalLink,
  initialServerCreateForm,
  serverTypeOf,
  switchServerType,
  type ExternalLinkState,
  type ServerCreateFormState,
  type ServerEditFormState,
} from './server-form-utils'

const LINK = 'vless://uuid@example.com:443?security=reality&pbk=PBK&sid=abcd#东京 01'

function preview(overrides: Partial<ParseExternalLinkResponse> = {}): ParseExternalLinkResponse {
  return {
    name: '东京 01',
    protocol: 'vless',
    address: 'example.com',
    port: 443,
    transport: 'tcp · reality',
    exit: {
      protocol: 'vless',
      address: 'example.com',
      port: 443,
      uuid: '11111111-2222-3333-4444-555555555555',
      network: 'tcp',
      security: 'reality',
      public_key: 'PBK',
      short_id: 'abcd',
    },
    ...overrides,
  }
}

function parsedLink(link = LINK): ExternalLinkState {
  return {
    ...initialExternalLink(),
    link,
    exit: externalExitFormFromExit(preview().exit),
  }
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
    expect(next.external.exit).toEqual(defaultExternalExitForm())
    // 公共资料保留，切回托管时无需重填。
    expect(next.alias).toBe('hk-01')
    expect(next.countryCode).toBe('HK')
    expect(next.location).toBe('香港')
    expect(next.tags).toEqual(['hk'])
    expect(next.nonDirect).toBe(true)
  })

  it('drops the external state when switching back to a managed type', () => {
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
  it('submits the protocol form for an external node and no managed fields', () => {
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
    expect(result.body.external_exit).toMatchObject({
      protocol: 'vless',
      address: 'example.com',
      port: 443,
      uuid: '11111111-2222-3333-4444-555555555555',
      network: 'tcp',
      security: 'reality',
      public_key: 'PBK',
      short_id: 'abcd',
    })
    expect(result.body.alias).toBe('hk-01')
    expect(result.body.country_code).toBe('HK')
    expect(result.body.machine_type).toBeUndefined()
    expect(result.body.address).toBeUndefined()
    expect(result.body.allowed_ports).toBeUndefined()
  })

  it('submits a manual protocol form with no link and no required fields', () => {
    const state = createState({
      serverType: 'external',
      alias: '',
      countryCode: '',
      external: {
        ...initialExternalLink(),
        exit: {
          ...defaultExternalExitForm(),
          protocol: 'socks',
          address: '198.51.100.7',
          port: '1080',
          username: 'alice',
          password: 'secret',
        },
      },
    })
    const result = buildServerCreatePayload(state)
    if (!('body' in result)) throw new Error('expected body')
    expect(result.body.alias).toBe('')
    expect(result.body.country_code).toBe('')
    expect(result.body.external_link).toBeUndefined()
    expect(result.body.external_exit).toMatchObject({
      protocol: 'socks',
      address: '198.51.100.7',
      port: 1080,
      username: 'alice',
      password: 'secret',
    })
  })

  it('allows an empty external config (placeholder row)', () => {
    const result = buildServerCreatePayload(createState({ serverType: 'external' }))
    if (!('body' in result)) throw new Error('expected body')
    expect(result.body.external_exit).toMatchObject({ protocol: 'http', address: '', port: 0 })
  })

  it('rejects an out-of-range port', () => {
    const state = createState({
      serverType: 'external',
      external: {
        ...initialExternalLink(),
        exit: { ...defaultExternalExitForm(), port: '70000' },
      },
    })
    expect(buildServerCreatePayload(state)).toEqual({ error: '端口须为 1-65535' })
  })

  it('keeps the managed payload free of external fields', () => {
    const result = buildServerCreatePayload(createState({ address: '1.2.3.4' }))
    if (!('body' in result)) throw new Error('expected body')
    expect(result.body.machine_type).toBe('direct')
    expect(result.body.address).toBe('1.2.3.4')
    expect(result.body.kind).toBeUndefined()
    expect(result.body.external_link).toBeUndefined()
    expect(result.body.external_exit).toBeUndefined()
  })

  it('requires a public address for NAT servers', () => {
    expect(buildServerCreatePayload(createState({ serverType: 'nat' }))).toEqual({
      error: 'NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）',
    })
  })
})

describe('external protocol form', () => {
  it('maps an ExternalExit to the form and back', () => {
    const exit: ExternalExit = {
      protocol: 'vmess',
      address: 'gw.example.com',
      port: 8443,
      uuid: 'uuid-1',
      cipher: 'auto',
      network: 'ws',
      security: 'tls',
      sni: 'cdn.example.com',
      path: '/ws',
    }
    const form = externalExitFormFromExit(exit)
    expect(form.port).toBe('8443')
    expect(form.network).toBe('ws')
    const payload = externalExitFormPayload(form)
    expect(payload).toMatchObject({
      protocol: 'vmess',
      address: 'gw.example.com',
      port: 8443,
      uuid: 'uuid-1',
      network: 'ws',
      security: 'tls',
      sni: 'cdn.example.com',
      path: '/ws',
    })
  })

  it('coerces reality away from ws/httpupgrade and keeps it for tcp', () => {
    const ws = externalExitFormPayload({
      ...defaultExternalExitForm(),
      protocol: 'vless',
      address: 'a.example.com',
      port: '443',
      network: 'ws',
      security: 'reality',
    })
    expect(ws.security).toBe('tls')
    const tcp = externalExitFormPayload({
      ...defaultExternalExitForm(),
      protocol: 'vless',
      address: 'a.example.com',
      port: '443',
      network: 'tcp',
      security: 'reality',
    })
    expect(tcp.security).toBe('reality')
  })

  it('validates only the filled port and address', () => {
    expect(externalExitFormError(defaultExternalExitForm())).toBeNull()
    expect(externalExitFormError({ ...defaultExternalExitForm(), port: 'abc' })).toBe(
      '端口须为 1-65535',
    )
    expect(externalExitFormError({ ...defaultExternalExitForm(), address: 'a b' })).toBe(
      '地址不能包含空格',
    )
  })
})

describe('editExternalLink', () => {
  it('keeps the filled protocol parameters when the link changes', () => {
    const state = { ...parsedLink(), parsing: true, parseError: '旧错误' }
    const next = editExternalLink(state, `  ${LINK}x`)
    expect(next.link).toBe(`  ${LINK}x`)
    expect(next.parsing).toBe(false)
    expect(next.parseError).toBe('')
    expect(next.exit.protocol).toBe('vless')
    expect(next.exit.address).toBe('example.com')
  })
})

describe('external link state transitions', () => {
  it('fills the protocol form on parse and discards stale results', () => {
    const state = { ...initialExternalLink(), link: LINK, parsing: true }
    const applied = applyExternalLinkPreview(state, LINK, preview())
    expect(applied?.parsing).toBe(false)
    expect(applied?.exit.protocol).toBe('vless')
    expect(applied?.exit.address).toBe('example.com')
    expect(applied?.exit.uuid).toBe('11111111-2222-3333-4444-555555555555')
    expect(applyExternalLinkPreview({ ...state, link: `${LINK}x` }, LINK, preview())).toBeNull()
  })

  it('records parse failures and discards stale ones', () => {
    const state = { ...initialExternalLink(), link: LINK, parsing: true }
    const failed = applyExternalLinkError(state, LINK, '无法解析链接')
    expect(failed?.parseError).toBe('无法解析链接')
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
})

function editState(overrides: Partial<ServerEditFormState> = {}): ServerEditFormState {
  return {
    serverType: 'direct',
    alias: 'edge-01',
    addresses: ['1.2.3.4'],
    defaultAddr: '1.2.3.4',
    addrInput: '',
    portRows: [''],
    tags: ['cn2'],
    countryCode: 'HK',
    location: 'Hong Kong',
    nonDirect: false,
    billing: defaultBilling(),
    traffic: defaultTraffic(),
    xrayOverride: '',
    xrayVersions: ['latest'],
    external: initialExternalLink(),
    ...overrides,
  }
}

describe('serverTypeOf', () => {
  it('maps kind and machine_type to the 3-way server type choice', () => {
    expect(serverTypeOf({ kind: 'managed', machine_type: 'direct' })).toBe('direct')
    expect(serverTypeOf({ kind: 'managed', machine_type: 'nat' })).toBe('nat')
    expect(serverTypeOf({ kind: 'external', machine_type: 'nat' })).toBe('external')
  })
})

describe('buildServerEditPayload', () => {
  it('builds managed direct payload with custom settings and omitted allowed_ports', () => {
    const res = buildServerEditPayload(
      { id: 12 },
      editState({ serverType: 'direct', xrayOverride: 'v1.8.24' }),
    )
    if (!('body' in res)) throw new Error('expected body')
    expect(res.body).toMatchObject({
      server_id: 12,
      kind: 'managed',
      machine_type: 'direct',
      alias: 'edge-01',
      address: '1.2.3.4',
      addresses: ['1.2.3.4'],
      country_code: 'HK',
      custom_settings: { xray_version: 'v1.8.24' },
    })
    expect(res.body.allowed_ports).toBeUndefined()
  })

  it('builds NAT payload with parsed port ranges and enforces public address', () => {
    expect(
      buildServerEditPayload({ id: 12 }, editState({ serverType: 'nat', defaultAddr: '', addresses: [] })),
    ).toEqual({
      error: 'NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）',
    })
    const res = buildServerEditPayload(
      { id: 12 },
      editState({ serverType: 'nat', portRows: ['20001-20010:10001-10010'] }),
    )
    if (!('body' in res)) throw new Error('expected body')
    expect(res.body.kind).toBe('managed')
    expect(res.body.machine_type).toBe('nat')
    expect(res.body.allowed_ports).toEqual([
      { pub_start: 20001, pub_end: 20010, listen_start: 10001, listen_end: 10010 },
    ])
  })

  it('builds external payload without requiring alias or country_code and validates ports', () => {
    expect(
      buildServerEditPayload(
        { id: 12 },
        editState({
          serverType: 'external',
          external: {
            ...initialExternalLink(),
            exit: { ...defaultExternalExitForm(), port: '99999' },
          },
        }),
      ),
    ).toEqual({ error: '端口须为 1-65535' })

    const res = buildServerEditPayload(
      { id: 12 },
      editState({
        serverType: 'external',
        alias: ' ',
        countryCode: '',
        external: parsedLink(),
      }),
    )
    if (!('body' in res)) throw new Error('expected body')
    expect(res.body.kind).toBe('external')
    expect(res.body.alias).toBe('')
    expect(res.body.country_code).toBe('')
    expect(res.body.machine_type).toBeUndefined()
    expect(res.body.external_link).toBe(LINK)
    expect(res.body.external_exit).toMatchObject({ protocol: 'vless', address: 'example.com', port: 443 })
  })
})
