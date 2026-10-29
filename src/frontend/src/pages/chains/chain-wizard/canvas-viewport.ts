export interface Point {
  x: number
  y: number
}

export interface CanvasViewport extends Point {
  zoom: number
}

export interface SceneBounds extends Point {
  width: number
  height: number
}

const MIN_ZOOM = 0.35
const MAX_ZOOM = 1.75

function finite(value: number, fallback = 0): number {
  return Number.isFinite(value) ? value : fallback
}

function stableViewport(viewport: CanvasViewport): CanvasViewport {
  return {
    x: finite(viewport.x),
    y: finite(viewport.y),
    zoom: Number.isFinite(viewport.zoom) && viewport.zoom > 0 ? viewport.zoom : 1,
  }
}

/** The viewport translation is measured in container CSS pixels. */
export function screenToWorld(point: Point, viewport: CanvasViewport): Point {
  const current = stableViewport(viewport)
  return { x: (point.x - current.x) / current.zoom, y: (point.y - current.y) / current.zoom }
}

export function worldToScreen(point: Point, viewport: CanvasViewport): Point {
  const current = stableViewport(viewport)
  return { x: point.x * current.zoom + current.x, y: point.y * current.zoom + current.y }
}

export function zoomAt(
  viewport: CanvasViewport,
  screenPoint: Point,
  nextZoom: number,
): CanvasViewport {
  const current = stableViewport(viewport)
  if (
    !Number.isFinite(nextZoom) ||
    !Number.isFinite(screenPoint.x) ||
    !Number.isFinite(screenPoint.y)
  ) {
    return current
  }
  const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, nextZoom))
  const anchor = screenToWorld(screenPoint, current)
  const x = screenPoint.x - anchor.x * zoom
  const y = screenPoint.y - anchor.y * zoom
  return Number.isFinite(x) && Number.isFinite(y) ? { x, y, zoom } : current
}

export function fitViewport(
  bounds: SceneBounds,
  size: { width: number; height: number },
  padding = 56,
): CanvasViewport {
  const width = Math.max(0, finite(size.width))
  const height = Math.max(0, finite(size.height))
  const sceneWidth = Math.max(0, finite(bounds.width))
  const sceneHeight = Math.max(0, finite(bounds.height))
  const inset = Math.max(0, finite(padding, 56))
  const availableWidth = Math.max(0, width - inset * 2)
  const availableHeight = Math.max(0, height - inset * 2)
  const zoom = Math.max(
    MIN_ZOOM,
    Math.min(
      1,
      sceneWidth > 0 ? availableWidth / sceneWidth : 1,
      sceneHeight > 0 ? availableHeight / sceneHeight : 1,
    ),
  )
  return {
    x: finite(width / 2 - finite(bounds.x) * zoom - (sceneWidth * zoom) / 2),
    y: finite(height / 2 - finite(bounds.y) * zoom - (sceneHeight * zoom) / 2),
    zoom,
  }
}

export function panViewport(viewport: CanvasViewport, delta: Point): CanvasViewport {
  const current = stableViewport(viewport)
  return {
    x: finite(current.x + finite(delta.x), current.x),
    y: finite(current.y + finite(delta.y), current.y),
    zoom: current.zoom,
  }
}
