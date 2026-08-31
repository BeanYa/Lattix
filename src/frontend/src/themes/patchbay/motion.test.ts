import { afterEach, describe, expect, it, vi } from 'vitest'

import { animateRackFeedback, hopFeedbackDelay, nextChainIndex } from './motion'

afterEach(() => vi.unstubAllGlobals())

describe('chain selection keyboard controls', () => {
  it('supports both rail orientations and clamps to the available chains', () => {
    expect(nextChainIndex('ArrowDown', 0, 6)).toBe(1)
    expect(nextChainIndex('ArrowRight', 0, 6)).toBe(1)
    expect(nextChainIndex('ArrowUp', 2, 6)).toBe(1)
    expect(nextChainIndex('ArrowLeft', 2, 6)).toBe(1)
    expect(nextChainIndex('ArrowUp', 0, 6)).toBe(0)
    expect(nextChainIndex('ArrowDown', 5, 6)).toBe(5)
    expect(nextChainIndex('Home', 3, 6)).toBe(0)
    expect(nextChainIndex('End', 3, 6)).toBe(5)
  })

  it('never captures Tab, Enter, Space or keys outside the selector', () => {
    for (const key of ['Tab', 'Enter', ' ', 'Escape', 'a'])
      expect(nextChainIndex(key, 0, 6)).toBeNull()
    expect(nextChainIndex('ArrowDown', 0, 0)).toBeNull()
  })

  it('caps feedback staggering even on very long routes', () => {
    expect([0, 1, 2, 3, 100].map(hopFeedbackDelay)).toEqual([0, 45, 90, 135, 135])
    expect(hopFeedbackDelay(-1)).toBe(0)
  })
})

function motionFixture({ reduced = false, hidden = false, offscreen = false } = {}) {
  const preference = { matches: reduced, addEventListener: vi.fn(), removeEventListener: vi.fn() }
  const doc = { hidden, addEventListener: vi.fn(), removeEventListener: vi.fn() }
  vi.stubGlobal('window', { matchMedia: () => preference, innerHeight: 800, innerWidth: 1200 })
  vi.stubGlobal('document', doc)
  let finish!: () => void
  const animation = {
    cancel: vi.fn(),
    finished: new Promise<void>((resolve) => {
      finish = resolve
    }),
  }
  const animate = vi.fn(() => animation)
  const element = {
    getBoundingClientRect: () => ({
      top: offscreen ? 900 : 10,
      bottom: offscreen ? 950 : 50,
      left: 10,
      right: 100,
    }),
    animate,
  } as unknown as HTMLElement
  return { element, animate, animation, preference, doc, finish }
}

describe('bounded rack feedback', () => {
  it.each([{ reduced: true }, { hidden: true }, { offscreen: true }])(
    'skips unnecessary animation: %o',
    (options) => {
      const fixture = motionFixture(options)
      animateRackFeedback(fixture.element, [{ opacity: 0.5 }, { opacity: 1 }])()
      expect(fixture.animate).not.toHaveBeenCalled()
    },
  )

  it('leaves content usable when the browser has no animation API', () => {
    const fixture = motionFixture()
    fixture.element.animate = undefined as unknown as HTMLElement['animate']
    expect(() => animateRackFeedback(fixture.element, [])()).not.toThrow()
  })

  it('uses short finite animations and removes listeners on completion', async () => {
    const fixture = motionFixture()
    animateRackFeedback(fixture.element, [{ opacity: 0.5 }, { opacity: 1 }])
    expect(fixture.animate).toHaveBeenCalledWith(
      expect.any(Array),
      expect.objectContaining({ duration: 260 }),
    )
    fixture.finish()
    await fixture.animation.finished
    expect(fixture.preference.removeEventListener).toHaveBeenCalled()
    expect(fixture.doc.removeEventListener).toHaveBeenCalled()
  })

  it('cancels superseded feedback and cleans up listeners', () => {
    const fixture = motionFixture()
    const cancel = animateRackFeedback(fixture.element, [])
    cancel()
    expect(fixture.animation.cancel).toHaveBeenCalledOnce()
    expect(fixture.preference.removeEventListener).toHaveBeenCalled()
  })

  it('responds to reduced-motion preference changes immediately', () => {
    const fixture = motionFixture()
    animateRackFeedback(fixture.element, [])
    fixture.preference.addEventListener.mock.calls[0][1]()
    expect(fixture.animation.cancel).toHaveBeenCalledOnce()
  })

  it('stops when the document becomes hidden', () => {
    const fixture = motionFixture()
    animateRackFeedback(fixture.element, [])
    fixture.doc.hidden = true
    fixture.doc.addEventListener.mock.calls[0][1]()
    expect(fixture.animation.cancel).toHaveBeenCalledOnce()
  })
})
