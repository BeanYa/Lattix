import { describe, expect, it } from 'vitest'

import {
  expiryDateDay,
  formatTrafficLimit,
  localDateToRFC3339EndOfDay,
  parseTrafficLimit,
  parseTrafficResetDay,
  toLocalDateInput,
} from './user-subscription'

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

describe('expiry date inputs', () => {
  it('formats RFC3339 as a local calendar date', () => {
    const input = new Date('2026-08-13T12:00:00Z')
    const pad = (n: number) => String(n).padStart(2, '0')
    const want = `${input.getFullYear()}-${pad(input.getMonth() + 1)}-${pad(input.getDate())}`
    expect(toLocalDateInput('2026-08-13T12:00:00Z')).toBe(want)
    expect(toLocalDateInput('')).toBe('')
    expect(toLocalDateInput('not-a-date')).toBe('')
  })

  it('turns a calendar date into the end of that local day', () => {
    const got = localDateToRFC3339EndOfDay('2026-08-13')
    expect(got).not.toBeNull()
    const d = new Date(got!)
    const pad = (n: number) => String(n).padStart(2, '0')
    expect(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`).toBe('2026-08-13')
    expect(d.getHours()).toBe(23)
    expect(d.getMinutes()).toBe(59)
    expect(d.getSeconds()).toBe(59)
  })

  it('keeps manual date-only typing and blank clearing meaningful', () => {
    expect(localDateToRFC3339EndOfDay('2026-08-13')).not.toBeNull()
    expect(localDateToRFC3339EndOfDay('')).toBeNull()
    expect(localDateToRFC3339EndOfDay('2026/08/13')).toBeNull()
  })

  it('extracts the day of month for reset-day sync', () => {
    expect(expiryDateDay('2026-08-13')).toBe('13')
    expect(expiryDateDay('2026-12-31')).toBe('31')
    expect(expiryDateDay('')).toBe('')
    expect(expiryDateDay('garbage')).toBe('')
  })
})
