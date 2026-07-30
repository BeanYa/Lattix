import { describe, expect, it } from 'vitest'

import { formatTrafficLimit, parseTrafficLimit, parseTrafficResetDay } from './user-subscription'

describe('user subscription inputs', () => {
  it('converts quota quantities using binary traffic units', () => {
    expect(parseTrafficLimit('1', 'TB')).toBe(1099511627776)
    expect(parseTrafficLimit('2.5', 'GB')).toBe(2684354560)
    expect(parseTrafficLimit('512', 'MB')).toBe(536870912)
    expect(parseTrafficLimit('', 'GB')).toBe(0)
  })

  it('chooses the largest exact unit when editing a quota', () => {
    expect(formatTrafficLimit(1099511627776)).toEqual({ value: '1', unit: 'TB' })
    expect(formatTrafficLimit(1073741824000)).toEqual({ value: '1000', unit: 'GB' })
    expect(formatTrafficLimit(536870912)).toEqual({ value: '512', unit: 'MB' })
    expect(formatTrafficLimit(0)).toEqual({ value: '', unit: 'GB' })
  })

  it('accepts a blank or calendar day and rejects values outside 1-31', () => {
    expect(parseTrafficResetDay('')).toBe(0)
    expect(parseTrafficResetDay('1')).toBe(1)
    expect(parseTrafficResetDay('31')).toBe(31)
    expect(() => parseTrafficResetDay('0')).toThrow()
    expect(() => parseTrafficResetDay('32')).toThrow()
    expect(() => parseTrafficResetDay('1.5')).toThrow()
  })
})
