import { describe, expect, it } from 'vitest'

import { addressFamily, isPublicAddress } from './address'

describe('addressFamily', () => {
  it('按字面量与域名分类', () => {
    expect(addressFamily('1.2.3.4')).toBe('ipv4')
    expect(addressFamily('2400:cb00::1')).toBe('ipv6')
    expect(addressFamily('example.com')).toBe('domain')
  })
})

describe('isPublicAddress', () => {
  it('接受公网地址与域名', () => {
    expect(isPublicAddress('23.94.143.194')).toBe(true)
    expect(isPublicAddress('2400:cb00::1')).toBe(true)
    expect(isPublicAddress('hk-01.example.com')).toBe(true)
  })
  it('剔除私网/链路本地/CGNAT/回环', () => {
    expect(isPublicAddress('10.0.0.1')).toBe(false)
    expect(isPublicAddress('172.16.0.1')).toBe(false)
    expect(isPublicAddress('192.168.1.1')).toBe(false)
    expect(isPublicAddress('100.64.0.1')).toBe(false)
    expect(isPublicAddress('169.254.1.1')).toBe(false)
    expect(isPublicAddress('127.0.0.1')).toBe(false)
    expect(isPublicAddress('fe80::216:3cff:fe62:fb31')).toBe(false)
    expect(isPublicAddress('fd00::1')).toBe(false)
    expect(isPublicAddress('::1')).toBe(false)
  })
})
