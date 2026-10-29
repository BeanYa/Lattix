import { describe, expect, it } from 'vitest'
import type { Point, SceneBounds } from './canvas-viewport'
import { routeCanvasEdge, type EdgeAnchor } from './canvas-edge-routing'

const source: EdgeAnchor = {
  point: { x: 68, y: -56 },
  bounds: { x: -36, y: -108, width: 104, height: 128 },
}
const target: EdgeAnchor = {
  point: { x: 60, y: 312 },
  bounds: { x: 60, y: 140, width: 264, height: 200 },
}

function crosses(a: Point, b: Point, r: SceneBounds) {
  const e = 0.0001
  return a.x === b.x
    ? a.x > r.x + e &&
        a.x < r.x + r.width - e &&
        Math.max(a.y, b.y) > r.y + e &&
        Math.min(a.y, b.y) < r.y + r.height - e
    : a.y > r.y + e &&
        a.y < r.y + r.height - e &&
        Math.max(a.x, b.x) > r.x + e &&
        Math.min(a.x, b.x) < r.x + r.width - e
}
function assertRoute(a: EdgeAnchor, b: EdgeAnchor, others: SceneBounds[] = []) {
  const route = routeCanvasEdge(a, b, others)
  expect(route.points[0]).toEqual(a.point)
  expect(route.points.at(-1)).toEqual(b.point)
  expect(route.points[1].x).toBeGreaterThan(a.point.x)
  expect(route.points.at(-2)!.x).toBeLessThan(b.point.x)
  for (let i = 1; i < route.points.length; i++) {
    const p = route.points[i - 1],
      q = route.points[i]
    expect(p.x === q.x || p.y === q.y).toBe(true)
    expect(p).not.toEqual(q)
    for (const box of [a.bounds, b.bounds, ...others]) expect(crosses(p, q, box)).toBe(false)
    if (i > 1) {
      const previous = route.points[i - 2]
      // No retraced segment or 180-degree corner.
      expect(
        (p.x - previous.x) * (q.x - p.x) + (p.y - previous.y) * (q.y - p.y),
      ).toBeGreaterThanOrEqual(0)
    }
    for (let j = 1; j < i - 1; j++) {
      const u = route.points[j - 1],
        v = route.points[j]
      if ((p.x === q.x) !== (u.x === v.x)) {
        const vertical = p.x === q.x ? [p, q] : [u, v],
          horizontal = p.x === q.x ? [u, v] : [p, q]
        expect(
          vertical[0].x > Math.min(horizontal[0].x, horizontal[1].x) &&
            vertical[0].x < Math.max(horizontal[0].x, horizontal[1].x) &&
            horizontal[0].y > Math.min(vertical[0].y, vertical[1].y) &&
            horizontal[0].y < Math.max(vertical[0].y, vertical[1].y),
        ).toBe(false)
      }
    }
  }
  // 检查真正渲染的曲线，而不仅检查避障骨架。
  const tokens = route.path.trim().split(/\s+/)
  let cursor = { x: 0, y: 0 },
    index = 0
  const read = () => ({ x: Number(tokens[index++]), y: Number(tokens[index++]) })
  while (index < tokens.length) {
    const command = tokens[index++]
    if (command === 'M') {
      cursor = read()
      continue
    }
    const first = read(),
      second = command === 'C' ? read() : first,
      end = command === 'C' || command === 'Q' ? read() : first
    for (let sample = 1; sample <= 24; sample++) {
      const t = sample / 24
      const point =
        command === 'C'
          ? {
              x:
                (1 - t) ** 3 * cursor.x +
                3 * (1 - t) ** 2 * t * first.x +
                3 * (1 - t) * t * t * second.x +
                t ** 3 * end.x,
              y:
                (1 - t) ** 3 * cursor.y +
                3 * (1 - t) ** 2 * t * first.y +
                3 * (1 - t) * t * t * second.y +
                t ** 3 * end.y,
            }
          : command === 'Q'
            ? {
                x: (1 - t) ** 2 * cursor.x + 2 * (1 - t) * t * first.x + t * t * end.x,
                y: (1 - t) ** 2 * cursor.y + 2 * (1 - t) * t * first.y + t * t * end.y,
              }
            : { x: cursor.x + (end.x - cursor.x) * t, y: cursor.y + (end.y - cursor.y) * t }
      for (const box of [a.bounds, b.bounds, ...others]) {
        expect(
          point.x > box.x + 1e-5 &&
            point.x < box.x + box.width - 1e-5 &&
            point.y > box.y + 1e-5 &&
            point.y < box.y + box.height - 1e-5,
        ).toBe(false)
      }
    }
    cursor = end
  }
  expect(route.path).not.toMatch(/NaN|Infinity/)
  return route
}

describe('obstacle-aware smooth cable connections', () => {
  it('无障碍时使用 ComfyUI 风格的单段三次曲线，平滑水平出入端口', () => {
    const a = { point: { x: 264, y: 60 }, bounds: { x: 0, y: 0, width: 264, height: 200 } }
    const b = { point: { x: 600, y: 272 }, bounds: { x: 600, y: 100, width: 264, height: 200 } }
    const route = assertRoute(a, b)
    expect(route.path.match(/[A-Z]/g)).toEqual(['M', 'C'])
    const numbers = route.path
      .split(/\s+/)
      .filter((token) => !/[A-Z]/.test(token))
      .map(Number)
    expect(numbers[3]).toBe(60)
    expect(numbers[5]).toBe(272)
    expect(numbers[2]).toBeGreaterThan(264)
    expect(numbers[4]).toBeLessThan(600)
  })

  it('复现截图：垂直交错时从两个节点之间穿行，不再绕到来源上方', () => {
    const route = assertRoute(source, target)
    expect(Math.min(...route.points.map((p) => p.y))).toBe(source.point.y)
    expect(Math.max(...route.points.map((p) => p.y))).toBe(target.point.y)
    expect(route.path).toContain('C')
    expect(route.path).not.toMatch(/[LQ]/)
  })
  it.each([-500, -260, -1, 0, 1, 30, 64, 260, 500])(
    '横向偏移 %i 时避免回环、穿卡和立即折返',
    (offset) => {
      const b = {
        point: { x: source.point.x + offset, y: 400 },
        bounds: { x: source.point.x + offset, y: 228, width: 264, height: 200 },
      }
      assertRoute(source, b)
    },
  )
  it('上下顺序反转也走节点间隙，不穿越来源或目标', () => {
    const a = { point: { x: 324, y: 350 }, bounds: { x: 60, y: 290, width: 264, height: 200 } }
    const b = { point: { x: 68, y: 0 }, bounds: { x: 68, y: -172, width: 264, height: 200 } }
    assertRoute(a, b)
  })
  it('同一水平线的反向连接绕过节点外侧', () => {
    const a = { point: { x: 664, y: 60 }, bounds: { x: 400, y: 0, width: 264, height: 200 } }
    const b = { point: { x: 40, y: 60 }, bounds: { x: 40, y: -112, width: 264, height: 200 } }
    assertRoute(a, b)
  })
  it('不只避让两端，还避让中间的其他服务器', () => {
    const a = { point: { x: 264, y: 60 }, bounds: { x: 0, y: 0, width: 264, height: 200 } }
    const b = { point: { x: 800, y: 272 }, bounds: { x: 800, y: 100, width: 264, height: 200 } }
    assertRoute(a, b, [{ x: 380, y: -80, width: 264, height: 430 }])
  })
  it('全场平移不改变连线形状，边界覆盖所有转折', () => {
    const original = assertRoute(source, target),
      delta = { x: -1000, y: 700 }
    const shift = (a: EdgeAnchor) => ({
      point: { x: a.point.x + delta.x, y: a.point.y + delta.y },
      bounds: { ...a.bounds, x: a.bounds.x + delta.x, y: a.bounds.y + delta.y },
    })
    const moved = assertRoute(shift(source), shift(target))
    expect(moved.points.map((p) => ({ x: p.x - delta.x, y: p.y - delta.y }))).toEqual(
      original.points,
    )
    for (const p of original.points) {
      expect(p.x).toBeGreaterThanOrEqual(original.bounds.x)
      expect(p.x).toBeLessThanOrEqual(original.bounds.x + original.bounds.width)
      expect(p.y).toBeGreaterThanOrEqual(original.bounds.y)
      expect(p.y).toBeLessThanOrEqual(original.bounds.y + original.bounds.height)
    }
  })
  it('重叠节点暂时无通道时仍保留有限路径，分开后恢复避障', () => {
    const overlap = routeCanvasEdge(source, {
      point: { x: 0, y: -30 },
      bounds: { x: 0, y: -100, width: 200, height: 120 },
    })
    expect(overlap.path).not.toMatch(/NaN|Infinity/)
    assertRoute(source, target)
  })
  it('两端距离很小时缩短端口引线，避免引线彼此重叠', () => {
    assertRoute(
      { point: { x: 100, y: 60 }, bounds: { x: 0, y: 0, width: 100, height: 100 } },
      { point: { x: 118, y: 60 }, bounds: { x: 118, y: 0, width: 100, height: 100 } },
    )
  })
})
