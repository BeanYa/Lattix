import type { Point, SceneBounds } from './canvas-viewport'
import { smoothCable } from './canvas-spline'

export interface EdgeAnchor {
  point: Point
  bounds: SceneBounds
}
export interface EdgeRoute {
  path: string
  points: Point[]
  bounds: SceneBounds
}
type Box = SceneBounds
const EPS = 1e-7
const inside = (p: Point, r: Box) =>
  p.x > r.x + EPS && p.x < r.x + r.width - EPS && p.y > r.y + EPS && p.y < r.y + r.height - EPS
const distance = (a: Point, b: Point) => Math.abs(a.x - b.x) + Math.abs(a.y - b.y)
const inflate = (r: Box, padding: number): Box => ({
  x: r.x - padding,
  y: r.y - padding,
  width: r.width + padding * 2,
  height: r.height + padding * 2,
})
function clear(a: Point, b: Point, boxes: Box[]): boolean {
  return !boxes.some((r) =>
    a.x === b.x
      ? a.x > r.x + EPS &&
        a.x < r.x + r.width - EPS &&
        Math.max(a.y, b.y) > r.y + EPS &&
        Math.min(a.y, b.y) < r.y + r.height - EPS
      : a.y > r.y + EPS &&
        a.y < r.y + r.height - EPS &&
        Math.max(a.x, b.x) > r.x + EPS &&
        Math.min(a.x, b.x) < r.x + r.width - EPS,
  )
}
function lead(point: Point, sign: 1 | -1, boxes: Box[]): Point {
  let length = 24
  for (const box of boxes) {
    if (point.y <= box.y || point.y >= box.y + box.height) continue
    const gap = sign === 1 ? box.x - point.x : point.x - (box.x + box.width)
    if (gap > EPS) length = Math.min(length, gap / 2)
  }
  return { x: point.x + sign * length, y: point.y }
}
function coordinates(values: number[]): number[] {
  const sorted = [...new Set(values)].sort((a, b) => a - b)
  // 空隙中线让折弯远离节点边缘；障碍边界保证窄通道仍有可行路径。
  return [
    ...new Set([
      ...sorted,
      ...sorted.slice(1).flatMap((v, i) => (v - sorted[i] > 32 ? [(v + sorted[i]) / 2] : [])),
    ]),
  ].sort((a, b) => a - b)
}
type QueueItem = { key: number; cost: number }
class MinQueue {
  items: QueueItem[] = []
  push(item: QueueItem) {
    let i = this.items.length
    this.items.push(item)
    while (i > 0) {
      const p = (i - 1) >> 1
      if (this.items[p].cost <= item.cost) break
      this.items[i] = this.items[p]
      i = p
    }
    this.items[i] = item
  }
  pop(): QueueItem | undefined {
    const first = this.items[0],
      last = this.items.pop()
    if (!last || !this.items.length) return first
    let i = 0
    while (i * 2 + 1 < this.items.length) {
      let child = i * 2 + 1
      if (child + 1 < this.items.length && this.items[child + 1].cost < this.items[child].cost)
        child++
      if (this.items[child].cost >= last.cost) break
      this.items[i] = this.items[child]
      i = child
    }
    this.items[i] = last
    return first
  }
}

/** Rectilinear visibility graph. State includes arrival direction to forbid immediate U-turns. */
function findRoute(start: Point, end: Point, boxes: Box[]): Point[] | null {
  if (boxes.some((r) => inside(start, r) || inside(end, r))) return null
  const xs = coordinates([start.x, end.x, ...boxes.flatMap((r) => [r.x, r.x + r.width])])
  const ys = coordinates([start.y, end.y, ...boxes.flatMap((r) => [r.y, r.y + r.height])])
  const nx = xs.length,
    ny = ys.length
  const points = ys.flatMap((y) => xs.map((x) => ({ x, y })))
  const free = points.map((p) => !boxes.some((r) => inside(p, r)))
  const begin = ys.indexOf(start.y) * nx + xs.indexOf(start.x),
    finish = ys.indexOf(end.y) * nx + xs.indexOf(end.x)
  const queue = new MinQueue(),
    costs = new Map<number, number>(),
    previous = new Map<number, number>()
  const initial = begin * 4 // rightward arrival from the source port
  queue.push({ key: initial, cost: 0 })
  costs.set(initial, 0)
  while (queue.items.length) {
    const item = queue.pop()!
    if (item.cost !== costs.get(item.key)) continue
    const node = Math.floor(item.key / 4),
      direction = item.key % 4
    if (node === finish && direction !== 2) {
      const route: Point[] = []
      let key: number | undefined = item.key
      while (key !== undefined) {
        route.push(points[Math.floor(key / 4)])
        key = previous.get(key)
      }
      return route.reverse()
    }
    const x = node % nx,
      y = Math.floor(node / nx)
    const neighbors = [
      x + 1 < nx ? node + 1 : -1,
      y + 1 < ny ? node + nx : -1,
      x > 0 ? node - 1 : -1,
      y > 0 ? node - nx : -1,
    ]
    neighbors.forEach((next, dir) => {
      if (
        next < 0 ||
        !free[next] ||
        dir === (direction + 2) % 4 ||
        !clear(points[node], points[next], boxes)
      )
        return
      const key = next * 4 + dir
      const cost = item.cost + distance(points[node], points[next]) + (dir === direction ? 0 : 24)
      if (cost >= (costs.get(key) ?? Infinity)) return
      costs.set(key, cost)
      previous.set(key, item.key)
      queue.push({ key, cost })
    })
  }
  return null
}
function simplify(points: Point[]): Point[] {
  const result: Point[] = []
  for (const p of points) {
    if (result.length && distance(result.at(-1)!, p) < EPS) continue
    while (result.length > 1) {
      const a = result.at(-2)!,
        b = result.at(-1)!
      if ((a.x === b.x && b.x === p.x) || (a.y === b.y && b.y === p.y)) result.pop()
      else break
    }
    result.push(p)
  }
  return result
}
/** Smooth cable routing over an obstacle-aware guide.
 * Ports face right/left. Geometry is independent of viewport pan and zoom.
 */
export function routeCanvasEdge(
  source: EdgeAnchor,
  target: EdgeAnchor,
  obstacles: Box[] = [],
): EdgeRoute {
  const boxes = [source.bounds, target.bounds, ...obstacles].filter(
    (r, i, all) =>
      all.findIndex(
        (b) => b.x === r.x && b.y === r.y && b.width === r.width && b.height === r.height,
      ) === i,
  )
  const start = lead(source.point, 1, boxes),
    end = lead(target.point, -1, boxes)
  let routed: Point[] | null = null
  for (const gap of [16, 6, 0]) {
    const expanded = boxes.map((r) => inflate(r, gap))
    // The port lead may cross its own clearance margin, but never another node.
    if (!clear(source.point, start, boxes) || !clear(end, target.point, boxes)) continue
    routed = findRoute(start, end, expanded)
    if (routed) {
      break
    }
  }
  // Overlapping cards can enclose a port: retain a drawable connection until the user separates them.
  if (!routed) {
    routed = findRoute(start, end, [])
  }
  const points = simplify([source.point, ...(routed ?? [start, end]), target.point])
  const cable = smoothCable(source.point, target.point, points, boxes)
  const hull = [...points, ...cable.hull]
  const x = Math.min(...hull.map((p) => p.x)),
    y = Math.min(...hull.map((p) => p.y))
  return {
    path: cable.path,
    points,
    bounds: {
      x,
      y,
      width: Math.max(...hull.map((p) => p.x)) - x,
      height: Math.max(...hull.map((p) => p.y)) - y,
    },
  }
}
