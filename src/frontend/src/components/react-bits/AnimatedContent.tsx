import { useEffect, useRef, type HTMLAttributes, type ReactNode } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'

gsap.registerPlugin(ScrollTrigger)

interface AnimatedContentProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode
  container?: Element | string | null
  distance?: number
  direction?: 'vertical' | 'horizontal'
  reverse?: boolean
  duration?: number
  ease?: string
  initialOpacity?: number
  animateOpacity?: boolean
  scale?: number
  threshold?: number
  delay?: number
}

export default function AnimatedContent({
  children,
  container,
  distance = 100,
  direction = 'vertical',
  reverse = false,
  duration = 0.8,
  ease = 'power3.out',
  initialOpacity = 0,
  animateOpacity = true,
  scale = 1,
  threshold = 0.1,
  delay = 0,
  className = '',
  ...props
}: AnimatedContentProps) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const element = ref.current
    if (!element) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      element.style.visibility = 'visible'
      return
    }

    let scrollerTarget: Element | string | null = container || null
    if (typeof scrollerTarget === 'string') scrollerTarget = document.querySelector(scrollerTarget)

    const axis = direction === 'horizontal' ? 'x' : 'y'
    const offset = reverse ? -distance : distance
    const startPercent = (1 - threshold) * 100

    gsap.set(element, {
      [axis]: offset,
      scale,
      opacity: animateOpacity ? initialOpacity : 1,
      visibility: 'visible',
    })

    const timeline = gsap.timeline({ paused: true, delay })
    timeline.to(element, {
      [axis]: 0,
      scale: 1,
      opacity: 1,
      duration,
      ease,
    })

    const trigger = ScrollTrigger.create({
      trigger: element,
      scroller: scrollerTarget || window,
      start: `top ${startPercent}%`,
      once: true,
      onEnter: () => timeline.play(),
    })

    return () => {
      trigger.kill()
      timeline.kill()
    }
  }, [
    animateOpacity,
    container,
    delay,
    direction,
    distance,
    duration,
    ease,
    initialOpacity,
    reverse,
    scale,
    threshold,
  ])

  return (
    <div ref={ref} className={`invisible ${className}`} {...props}>
      {children}
    </div>
  )
}
