import { describe, expect, it } from 'vitest'

import { initialSeverityFromSearch, stripSeverityParam } from './log-severity-param'

describe('initialSeverityFromSearch', () => {
  it('parses a severity offered by the operation logs filter', () => {
    expect(initialSeverityFromSearch('?severity=error')).toBe('error')
    expect(initialSeverityFromSearch('?severity=warning')).toBe('warning')
    expect(initialSeverityFromSearch('?severity=info')).toBe('info')
  })

  it('rejects values outside the filter options', () => {
    expect(initialSeverityFromSearch('?severity=bogus')).toBe('')
    expect(initialSeverityFromSearch('?severity=debug')).toBe('')
    expect(initialSeverityFromSearch('?severity=')).toBe('')
  })

  it('returns empty for a missing param or empty search', () => {
    expect(initialSeverityFromSearch('')).toBe('')
    expect(initialSeverityFromSearch('?')).toBe('')
    expect(initialSeverityFromSearch('?foo=1')).toBe('')
  })

  it('parses severity among other params', () => {
    expect(initialSeverityFromSearch('?foo=1&severity=error')).toBe('error')
  })
})

describe('stripSeverityParam', () => {
  it('removes the severity param and the leading question mark', () => {
    expect(stripSeverityParam('?severity=error')).toBe('')
  })

  it('preserves unrelated params', () => {
    expect(stripSeverityParam('?severity=error&foo=1')).toBe('?foo=1')
    expect(stripSeverityParam('?foo=1&severity=warning&bar=2')).toBe('?foo=1&bar=2')
  })

  it('returns the input unchanged when there is no severity param', () => {
    expect(stripSeverityParam('')).toBe('')
    expect(stripSeverityParam('?foo=1')).toBe('?foo=1')
  })
})
