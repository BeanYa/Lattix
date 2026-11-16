import { describe, expect, it } from 'vitest'

import type { ExternalExit } from '@/lib/types'

import {
  EXTERNAL_PROTOCOL_LABELS,
  externalExitCredentialFields,
  externalExitSummary,
  externalExitTransport,
  maskExternalCredential,
} from './external-exit'

function exit(overrides: Partial<ExternalExit> = {}): ExternalExit {
  return { protocol: 'socks', address: '198.51.100.9', port: 1080, ...overrides }
}

describe('externalExitSummary', () => {
  it('无传输层协议回退 TCP；带安全层时优先展示安全层', () => {
    expect(externalExitSummary(exit())).toBe('socks · TCP')
    expect(
      externalExitSummary(exit({ protocol: 'vless', network: 'tcp', security: 'reality' })),
    ).toBe('vless · reality')
    expect(externalExitSummary(exit({ protocol: 'vmess', network: 'ws' }))).toBe('vmess · ws')
  })
})

describe('externalExitTransport', () => {
  it('ss/socks/http 无传输层；vless/vmess/trojan 展示 network · security', () => {
    expect(externalExitTransport(exit())).toBe('TCP（无传输层）')
    expect(
      externalExitTransport(exit({ protocol: 'trojan', network: 'grpc', security: 'tls' })),
    ).toBe('grpc · tls')
  })
})

describe('凭据打码', () => {
  it('maskExternalCredential 与后端 maskCredential 一致且对已打码值幂等', () => {
    expect(maskExternalCredential('abcd')).toBe('****')
    expect(maskExternalCredential('abcdefgh')).toBe('ab****gh')
    expect(maskExternalCredential(maskExternalCredential('abcdefgh'))).toBe('ab****gh')
  })

  it('只读摘要字段：UUID/密码打码，用户名与加密方式保留', () => {
    expect(
      externalExitCredentialFields(
        exit({ uuid: '11111111-2222-3333-4444-555555555555', method: 'aes-128-gcm' }),
      ),
    ).toEqual([
      { label: 'UUID', value: '11****55' },
      { label: '加密方式', value: 'aes-128-gcm' },
    ])
    expect(
      externalExitCredentialFields(
        exit({ protocol: 'socks', username: 'alice', password: 'supersecret' }),
      ),
    ).toEqual([
      { label: '用户名', value: 'alice' },
      { label: '密码', value: 'su****et' },
    ])
  })

  it('协议标签覆盖全部支持的外部出口协议', () => {
    expect(EXTERNAL_PROTOCOL_LABELS.socks).toBe('SOCKS5')
    expect(EXTERNAL_PROTOCOL_LABELS.shadowsocks).toBe('Shadowsocks')
  })
})
