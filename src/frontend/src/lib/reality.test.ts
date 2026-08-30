import { describe, expect, it } from 'vitest'

import { CUSTOM_REALITY_DEST, inferRealityDestPreset } from './reality'

describe('inferRealityDestPreset', () => {
  it('restores an Amazon preset from a saved Reality config', () => {
    expect(inferRealityDestPreset('www.amazon.com:443', ['www.amazon.com'])).toBe('www.amazon.com')
  })

  it('restores the default preset from its saved Reality config', () => {
    expect(inferRealityDestPreset('dl.google.com:443', ['dl.google.com'])).toBe('dl.google.com')
  })

  it('uses the custom option when dest and SNI do not form an exact preset', () => {
    expect(inferRealityDestPreset('example.com:443', ['example.com'])).toBe(CUSTOM_REALITY_DEST)
    expect(inferRealityDestPreset('www.amazon.com:443', ['www.amazon.com', 'amazon.com'])).toBe(
      CUSTOM_REALITY_DEST,
    )
  })
})
