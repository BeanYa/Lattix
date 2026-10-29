import { describe, expect, it } from 'vitest'

import { fitViewport, panViewport, screenToWorld, worldToScreen, zoomAt } from './canvas-viewport'

describe('canvas coordinate transforms', () => {
  it('converts container CSS pixels to world coordinates', () => {
    expect(screenToWorld({ x: 300, y: 180 }, { x: 100, y: -20, zoom: 0.5 })).toEqual({
      x: 400,
      y: 400,
    })
  })

  it('keeps negative world coordinates and reverses the transform', () => {
    const viewport = { x: -900, y: 500, zoom: 1.5 }
    expect(worldToScreen({ x: -200, y: -600 }, viewport)).toEqual({ x: -1200, y: -400 })
    expect(screenToWorld({ x: -1200, y: -400 }, viewport)).toEqual({ x: -200, y: -600 })
  })
})

describe('zoomAt', () => {
  it('holds the world point under the screen anchor still', () => {
    expect(zoomAt({ x: 100, y: -20, zoom: 0.5 }, { x: 300, y: 180 }, 1.25)).toEqual({
      x: -200,
      y: -320,
      zoom: 1.25,
    })
  })

  it('clamps zoom while preserving the anchor', () => {
    expect(zoomAt({ x: 0, y: 0, zoom: 1 }, { x: 200, y: 100 }, 100)).toEqual({
      x: -150,
      y: -75,
      zoom: 1.75,
    })
    expect(zoomAt({ x: 0, y: 0, zoom: 1 }, { x: 200, y: 100 }, -5)).toEqual({
      x: 130,
      y: 65,
      zoom: 0.35,
    })
  })

  it.each([NaN, Infinity, -Infinity])('ignores invalid requested zoom %s', (zoom) => {
    expect(zoomAt({ x: 45, y: -60, zoom: 0.75 }, { x: 200, y: 100 }, zoom)).toEqual({
      x: 45,
      y: -60,
      zoom: 0.75,
    })
  })

  it('ignores an invalid anchor and repairs an invalid current viewport', () => {
    expect(zoomAt({ x: 45, y: -60, zoom: 0.75 }, { x: NaN, y: 100 }, 1)).toEqual({
      x: 45,
      y: -60,
      zoom: 0.75,
    })
    expect(zoomAt({ x: Infinity, y: NaN, zoom: 0 }, { x: 200, y: 100 }, NaN)).toEqual({
      x: 0,
      y: 0,
      zoom: 1,
    })
  })

  it('keeps a finite viewport when extreme arithmetic overflows', () => {
    const viewport = { x: -Number.MAX_VALUE, y: 0, zoom: 0.35 }
    expect(zoomAt(viewport, { x: Number.MAX_VALUE, y: 0 }, 1.75)).toEqual(viewport)
  })
})

describe('fitViewport', () => {
  it('fits a wide scene in a narrow container with default padding', () => {
    expect(
      fitViewport({ x: -200, y: 100, width: 400, height: 200 }, { width: 312, height: 600 }),
    ).toEqual({ x: 156, y: 200, zoom: 0.5 })
  })

  it('fits a tall scene in a wide container', () => {
    expect(
      fitViewport({ x: 100, y: -200, width: 200, height: 400 }, { width: 900, height: 312 }),
    ).toEqual({ x: 350, y: 156, zoom: 0.5 })
  })

  it('centers small scenes without enlarging them', () => {
    expect(
      fitViewport({ x: -100, y: -50, width: 200, height: 100 }, { width: 1000, height: 800 }),
    ).toEqual({ x: 500, y: 400, zoom: 1 })
  })

  it('supports explicit padding', () => {
    expect(
      fitViewport({ x: 0, y: 0, width: 400, height: 200 }, { width: 300, height: 400 }, 50),
    ).toEqual({ x: 50, y: 150, zoom: 0.5 })
  })

  it('centers oversized scenes at the minimum zoom', () => {
    expect(
      fitViewport({ x: 0, y: 0, width: 10000, height: 10000 }, { width: 400, height: 300 }),
    ).toEqual({ x: -1550, y: -1600, zoom: 0.35 })
  })

  it('centers an empty scene and stays finite for an unmeasured container', () => {
    expect(
      fitViewport({ x: -100, y: 50, width: 0, height: 0 }, { width: 400, height: 300 }),
    ).toEqual({ x: 300, y: 100, zoom: 1 })
    const viewport = fitViewport(
      { x: NaN, y: Infinity, width: -1, height: NaN },
      { width: 0, height: NaN },
    )
    expect(Object.values(viewport).every(Number.isFinite)).toBe(true)
  })
})

describe('panViewport', () => {
  it('pans freely beyond former positive and negative world limits', () => {
    const original = { x: 100, y: -200, zoom: 0.5 }
    expect(panViewport(original, { x: 100000, y: -100000 })).toEqual({
      x: 100100,
      y: -100200,
      zoom: 0.5,
    })
    expect(original).toEqual({ x: 100, y: -200, zoom: 0.5 })
  })

  it('ignores invalid or overflowing pan components', () => {
    expect(
      panViewport({ x: Number.MAX_VALUE, y: 40, zoom: 1 }, { x: Number.MAX_VALUE, y: NaN }),
    ).toEqual({
      x: Number.MAX_VALUE,
      y: 40,
      zoom: 1,
    })
  })
})
