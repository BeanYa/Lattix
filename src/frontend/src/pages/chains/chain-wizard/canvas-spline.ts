import type { Point, SceneBounds } from './canvas-viewport'

type Cubic = { start: Point; first: Point; second: Point; end: Point }
const mix = (a: Point, b: Point, t = 0.5): Point => ({
  x: a.x + (b.x - a.x) * t,
  y: a.y + (b.y - a.y) * t,
})
const length = (a: Point, b: Point) => Math.hypot(b.x - a.x, b.y - a.y)
const EPS = 1e-6

/** Conservative de Casteljau subdivision: a curve is clear only when every hull is outside the box. */
function intersects(curve: Cubic, box: SceneBounds, depth = 0): boolean {
  if (box.width <= 0 || box.height <= 0) return false
  const points = [curve.start, curve.first, curve.second, curve.end]
  if (
    Math.max(...points.map((p) => p.x)) <= box.x + EPS ||
    Math.min(...points.map((p) => p.x)) >= box.x + box.width - EPS ||
    Math.max(...points.map((p) => p.y)) <= box.y + EPS ||
    Math.min(...points.map((p) => p.y)) >= box.y + box.height - EPS
  )
    return false
  if (
    [curve.start, curve.end].some(
      (p) =>
        p.x > box.x + EPS &&
        p.x < box.x + box.width - EPS &&
        p.y > box.y + EPS &&
        p.y < box.y + box.height - EPS,
    )
  )
    return true
  if (depth >= 12) return true
  const a = mix(curve.start, curve.first),
    b = mix(curve.first, curve.second),
    c = mix(curve.second, curve.end)
  const d = mix(a, b),
    e = mix(b, c),
    middle = mix(d, e)
  return (
    intersects({ start: curve.start, first: a, second: d, end: middle }, box, depth + 1) ||
    intersects({ start: middle, first: e, second: c, end: curve.end }, box, depth + 1)
  )
}
const clear = (curves: Cubic[], boxes: SceneBounds[]) =>
  !curves.some((curve) => boxes.some((box) => intersects(curve, box)))
const line = (start: Point, end: Point): Cubic => ({
  start,
  first: mix(start, end, 1 / 3),
  second: mix(start, end, 2 / 3),
  end,
})

/** Join a safe guide with large tangent-continuous bends, reducing their radius when space is tight. */
function bendGuide(points: Point[], radius: number): Cubic[] {
  const curves: Cubic[] = []
  let cursor = points[0]
  const append = (segment: Cubic) => {
    if (length(segment.start, segment.end) > EPS) curves.push(segment)
  }
  for (let i = 1; i < points.length - 1; i++) {
    const a = points[i - 1],
      b = points[i],
      c = points[i + 1]
    const incoming = length(a, b),
      outgoing = length(b, c)
    if (incoming <= EPS || outgoing <= EPS) continue
    const r = Math.min(radius, incoming / 2, outgoing / 2)
    const before = mix(b, a, r / incoming),
      after = mix(b, c, r / outgoing)
    append(line(cursor, before))
    // Cubic quarter-circle approximation: both endpoint tangents match their adjoining cable.
    append({
      start: before,
      first: mix(before, b, 0.5522847498),
      second: mix(after, b, 0.5522847498),
      end: after,
    })
    cursor = after
  }
  append(line(cursor, points.at(-1)!))
  return curves
}

/** ComfyUI defaults to horizontal controls at 25% of endpoint distance.
 * https://github.com/Comfy-Org/ComfyUI_frontend/blob/main/src/renderer/core/canvas/litegraph/litegraphLinkAdapter.ts
 * Prefer that single flowing spline; only use a detour when nodes actually obstruct it.
 */
export function smoothCable(
  source: Point,
  target: Point,
  guide: Point[],
  boxes: SceneBounds[],
): { path: string; hull: Point[] } {
  const distance = length(source, target)
  let curves: Cubic[] | undefined
  // With equal endpoint heights a backwards cubic retraces itself; use the safe guide instead.
  if (target.x > source.x || Math.abs(target.y - source.y) > EPS) {
    for (const factor of [0.25, 0.35, 0.45, 0.6]) {
      const handle = distance * factor
      const candidate: Cubic = {
        start: source,
        first: { x: source.x + handle, y: source.y },
        second: { x: target.x - handle, y: target.y },
        end: target,
      }
      if (clear([candidate], boxes)) {
        curves = [candidate]
        break
      }
    }
  }
  if (!curves) {
    for (const radius of [96, 64, 40, 24, 12, 6, 0]) {
      const candidate = bendGuide(guide, radius)
      if (radius === 0 || clear(candidate, boxes)) {
        curves = candidate
        break
      }
    }
  }
  const path =
    `M ${source.x} ${source.y}` +
    (curves ?? [])
      .map((c) => ` C ${c.first.x} ${c.first.y} ${c.second.x} ${c.second.y} ${c.end.x} ${c.end.y}`)
      .join('')
  return { path, hull: [source, target, ...(curves ?? []).flatMap((c) => [c.first, c.second])] }
}
