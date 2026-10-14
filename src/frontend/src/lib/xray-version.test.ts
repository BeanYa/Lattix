import { describe, expect, it } from 'vitest'

import { xrayVersionAtLeast, xrayVersionKnownBelow } from './xray-version'

describe('xrayVersionAtLeast', () => {
  it('按三段数字比较', () => {
    expect(xrayVersionAtLeast('26.3.27')).toBe(true)
    expect(xrayVersionAtLeast('26.10.1')).toBe(true)
    expect(xrayVersionAtLeast('26.3.26')).toBe(false)
    expect(xrayVersionAtLeast('25.12.8')).toBe(false)
    expect(xrayVersionAtLeast(null)).toBe(false)
    expect(xrayVersionAtLeast('unknown')).toBe(false)
  })
})

describe('xrayVersionKnownBelow', () => {
  it('版本已知且过低才判定为 true；未上报（空）不算过低', () => {
    expect(xrayVersionKnownBelow('26.3.26')).toBe(true)
    expect(xrayVersionKnownBelow('25.12.8')).toBe(true)
    expect(xrayVersionKnownBelow('26.3.27')).toBe(false)
    expect(xrayVersionKnownBelow('26.10.1')).toBe(false)
    expect(xrayVersionKnownBelow('')).toBe(false)
    expect(xrayVersionKnownBelow('  ')).toBe(false)
    expect(xrayVersionKnownBelow(null)).toBe(false)
    expect(xrayVersionKnownBelow(undefined)).toBe(false)
  })
})
