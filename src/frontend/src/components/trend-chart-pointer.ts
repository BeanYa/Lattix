/**
 * Converts a pointer position to TrendChart viewBox coordinates.
 *
 * The chart <svg> keeps its default preserveAspectRatio="xMidYMid meet", so
 * the viewBox is scaled uniformly and centered inside the stretched element,
 * leaving letterbox gutters on one axis. Element-relative ratios are
 * therefore not a linear map to viewBox units — replicate the meet geometry
 * (and clamp to the charted area) instead. If the svg ever sets
 * preserveAspectRatio="none", this mapping must change with it.
 */
export function trendChartViewBoxPoint(
  bounds: { left: number; top: number; width: number; height: number },
  clientX: number,
  clientY: number,
  viewWidth: number,
  viewHeight: number,
): { x: number; y: number } {
  const scale = Math.min(bounds.width / viewWidth, bounds.height / viewHeight)
  const offsetX = (bounds.width - viewWidth * scale) / 2
  const offsetY = (bounds.height - viewHeight * scale) / 2
  const clamp = (value: number, max: number) => Math.min(max, Math.max(0, value))
  return {
    x: clamp((clientX - bounds.left - offsetX) / scale, viewWidth),
    y: clamp((clientY - bounds.top - offsetY) / scale, viewHeight),
  }
}
