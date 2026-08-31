import { useEffect, useRef, useState, type RefObject } from 'react'
import { ArrowLeftIcon, ArrowRightIcon, MapPinIcon } from 'lucide-react'

import { visibleHopRange } from './chain-tools'

export function RouteNavigator({
  stageRef,
  chainId,
  count,
  motionPaused,
}: {
  stageRef: RefObject<HTMLDivElement | null>
  chainId: string
  count: number
  motionPaused: boolean
}) {
  const progressRef = useRef<HTMLSpanElement>(null)
  const [view, setView] = useState({
    first: 0,
    last: count - 1,
    overflow: false,
    atStart: true,
    atEnd: true,
  })

  // The stage is a later sibling; wait until all sibling refs have been attached.
  useEffect(() => {
    const stage = stageRef.current
    if (!stage) return
    let frame = 0
    const measure = () => {
      const rect = stage.getBoundingClientRect()
      const hops = Array.from(stage.querySelectorAll<HTMLElement>('.pb-route-steps > li')).map(
        (item) => {
          const bounds = item.getBoundingClientRect()
          return { left: bounds.left - rect.left + stage.scrollLeft, width: bounds.width }
        },
      )
      const maxScroll = Math.max(0, stage.scrollWidth - stage.clientWidth)
      const range = visibleHopRange(hops, stage.scrollLeft, stage.clientWidth)
      const next = {
        ...range,
        overflow: maxScroll > 2,
        atStart: stage.scrollLeft <= 2,
        atEnd: stage.scrollLeft >= maxScroll - 2,
      }
      setView((current) =>
        Object.keys(next).every(
          (key) => current[key as keyof typeof next] === next[key as keyof typeof next],
        )
          ? current
          : next,
      )
      // Update the small viewport thumb without rerendering React on every scroll frame.
      if (progressRef.current) {
        const trackWidth = progressRef.current.parentElement?.clientWidth ?? 0
        const fraction = Math.min(1, stage.clientWidth / Math.max(1, stage.scrollWidth))
        progressRef.current.style.width = `${fraction * 100}%`
        progressRef.current.style.transform = `translateX(${(Math.max(0, stage.scrollLeft) / Math.max(1, maxScroll)) * trackWidth * (1 - fraction)}px)`
      }
    }
    const schedule = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(measure)
    }
    measure()
    stage.addEventListener('scroll', schedule, { passive: true })
    const observer = new ResizeObserver(schedule)
    observer.observe(stage)
    if (stage.firstElementChild) observer.observe(stage.firstElementChild)
    return () => {
      cancelAnimationFrame(frame)
      stage.removeEventListener('scroll', schedule)
      observer.disconnect()
    }
  }, [chainId, count, stageRef])

  const goTo = (index: number) => {
    const stage = stageRef.current
    const hop = stage?.querySelectorAll<HTMLElement>('.pb-route-steps > li')[index]
    if (!stage || !hop) return
    const padding = parseFloat(getComputedStyle(stage).paddingLeft) || 0
    const left =
      hop.getBoundingClientRect().left -
      stage.getBoundingClientRect().left +
      stage.scrollLeft -
      padding
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    stage.scrollTo({
      left: Math.max(0, left),
      behavior: motionPaused || reduced ? 'instant' : 'smooth',
    })
  }

  return (
    <nav className="pb-topology-nav" aria-label="拓扑视口导航">
      <span className="pb-topology-position">
        <MapPinIcon aria-hidden="true" />
        {view.overflow
          ? `节点 ${view.first + 1}${view.last > view.first ? `–${view.last + 1}` : ''} / ${count}`
          : `${count} 个节点 · 完整拓扑`}
      </span>
      <span className="pb-topology-track" aria-hidden="true">
        <span ref={progressRef} />
      </span>
      {view.overflow ? (
        <div className="pb-topology-actions">
          <button type="button" onClick={() => goTo(0)} disabled={view.atStart} title="回到入口">
            <MapPinIcon aria-hidden="true" />
            <span>入口</span>
          </button>
          <button
            type="button"
            onClick={() => goTo(Math.max(0, view.first - 1))}
            disabled={view.atStart}
            aria-label="查看前一节点"
          >
            <ArrowLeftIcon aria-hidden="true" />
          </button>
          <button
            type="button"
            onClick={() => goTo(Math.min(count - 1, view.first + 1))}
            disabled={view.atEnd}
            aria-label="查看后一节点"
          >
            <ArrowRightIcon aria-hidden="true" />
          </button>
        </div>
      ) : (
        <span className="pb-topology-direction">OUT → IN</span>
      )}
    </nav>
  )
}
