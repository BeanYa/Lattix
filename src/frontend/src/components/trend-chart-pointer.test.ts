import { describe, expect, it } from 'vitest'

import { trendChartViewBoxPoint } from './trend-chart-pointer'

const VIEW_WIDTH = 480
const VIEW_HEIGHT = 96

describe('trendChartViewBoxPoint', () => {
  it('maps 1:1 when the element matches the viewBox aspect ratio', () => {
    const bounds = { left: 0, top: 0, width: 480, height: 96 }
    expect(trendChartViewBoxPoint(bounds, 240, 48, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 240,
      y: 48,
    })
    expect(trendChartViewBoxPoint(bounds, 0, 0, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({ x: 0, y: 0 })
  })

  it('compensates horizontal letterboxing on wide fixed-height charts', () => {
    // 卡片图表:svg 固定 96px 高、579px 宽,480 宽的内容按 xMidYMid meet 居中,两侧各留 49.5px 空白
    const bounds = { left: 100, top: 50, width: 579, height: 96 }
    expect(trendChartViewBoxPoint(bounds, 629.5, 98, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 480,
      y: 48,
    })
    expect(trendChartViewBoxPoint(bounds, 149.5, 98, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 0,
      y: 48,
    })
    expect(trendChartViewBoxPoint(bounds, 389.5, 98, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 240,
      y: 48,
    })
  })

  it('clamps pointers hovering the letterbox gutters to the chart edges', () => {
    const bounds = { left: 100, top: 50, width: 579, height: 96 }
    expect(trendChartViewBoxPoint(bounds, 120, 98, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 0,
      y: 48,
    })
    expect(trendChartViewBoxPoint(bounds, 650, 98, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 480,
      y: 48,
    })
  })

  it('compensates vertical letterboxing when the element is width-constrained', () => {
    const bounds = { left: 0, top: 0, width: 480, height: 192 }
    expect(trendChartViewBoxPoint(bounds, 240, 48, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 240,
      y: 0,
    })
    expect(trendChartViewBoxPoint(bounds, 240, 240, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 240,
      y: 96,
    })
  })

  it('scales uniformly when the element is larger than the viewBox', () => {
    const bounds = { left: 0, top: 0, width: 960, height: 192 }
    expect(trendChartViewBoxPoint(bounds, 480, 96, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 240,
      y: 48,
    })
    expect(trendChartViewBoxPoint(bounds, 960, 192, VIEW_WIDTH, VIEW_HEIGHT)).toEqual({
      x: 480,
      y: 96,
    })
  })
})
