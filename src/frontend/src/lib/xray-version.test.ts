import { describe, expect, it } from 'vitest'

import { xrayVersionAtLeast } from './xray-version'

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
