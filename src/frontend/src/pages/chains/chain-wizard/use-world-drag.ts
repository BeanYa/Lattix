import { useRef, useState } from 'react'

type Point = { x: number; y: number }
/** 同一套世界坐标拖动用于服务器和两个终端，控件/portal 不会触发拖动。 */
export function useWorldDrag(
  position: Point,
  toWorld: (x: number, y: number) => Point,
  onMove: (x: number, y: number) => void,
) {
  const active = useRef<{ pointerId: number; offset: Point } | null>(null)
  const [dragging, setDragging] = useState(false)
  const end = (event: React.PointerEvent<HTMLDivElement>) => {
    if (active.current?.pointerId !== event.pointerId) return
    active.current = null
    setDragging(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId)
  }
  return {
    dragging,
    handlers: {
      onPointerDown: (event: React.PointerEvent<HTMLDivElement>) => {
        if (
          active.current ||
          event.button !== 0 ||
          !event.currentTarget.contains(event.target as Node) ||
          (event.target as HTMLElement).closest('button,input,select,label,a,[role="combobox"]')
        )
          return
        event.stopPropagation()
        const point = toWorld(event.clientX, event.clientY)
        active.current = {
          pointerId: event.pointerId,
          offset: { x: point.x - position.x, y: point.y - position.y },
        }
        event.currentTarget.setPointerCapture(event.pointerId)
        setDragging(true)
      },
      onPointerMove: (event: React.PointerEvent<HTMLDivElement>) => {
        if (active.current?.pointerId !== event.pointerId) return
        event.stopPropagation()
        const point = toWorld(event.clientX, event.clientY)
        onMove(point.x - active.current.offset.x, point.y - active.current.offset.y)
      },
      onPointerUp: end,
      onPointerCancel: end,
      onLostPointerCapture: end,
    },
  }
}
