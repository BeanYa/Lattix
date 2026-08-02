import { useCallback, useEffect, useMemo, useRef, type CSSProperties } from 'react'
import { gsap } from 'gsap'
import { InertiaPlugin } from 'gsap/InertiaPlugin'

gsap.registerPlugin(InertiaPlugin)

function throttle<TArgs extends unknown[]>(callback: (...args: TArgs) => void, limit: number) {
  let lastCall = 0
  return (...args: TArgs) => {
    const now = performance.now()
    if (now - lastCall < limit) return
    lastCall = now
    callback(...args)
  }
}

interface Dot {
  cx: number
  cy: number
  xOffset: number
  yOffset: number
  inertiaApplied: boolean
}

interface DotGridProps {
  dotSize?: number
  gap?: number
  baseColor?: string
  activeColor?: string
  proximity?: number
  speedTrigger?: number
  shockRadius?: number
  shockStrength?: number
  maxSpeed?: number
  resistance?: number
  returnDuration?: number
  className?: string
  style?: CSSProperties
}

function hexToRgb(hex: string) {
  const match = hex.match(/^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i)
  if (!match) return { r: 0, g: 0, b: 0 }
  return {
    r: Number.parseInt(match[1], 16),
    g: Number.parseInt(match[2], 16),
    b: Number.parseInt(match[3], 16),
  }
}

export default function DotGrid({
  dotSize = 16,
  gap = 32,
  baseColor = '#5227FF',
  activeColor = '#5227FF',
  proximity = 150,
  speedTrigger = 100,
  shockRadius = 250,
  shockStrength = 5,
  maxSpeed = 5000,
  resistance = 750,
  returnDuration = 1.5,
  className = '',
  style,
}: DotGridProps) {
  const wrapperRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const dotsRef = useRef<Dot[]>([])
  const pointerRef = useRef({
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    speed: 0,
    lastTime: 0,
    lastX: 0,
    lastY: 0,
  })

  const baseRgb = useMemo(() => hexToRgb(baseColor), [baseColor])
  const activeRgb = useMemo(() => hexToRgb(activeColor), [activeColor])
  const circlePath = useMemo(() => {
    if (typeof window === 'undefined' || !window.Path2D) return null
    const path = new Path2D()
    path.arc(0, 0, dotSize / 2, 0, Math.PI * 2)
    return path
  }, [dotSize])

  const buildGrid = useCallback(() => {
    const wrapper = wrapperRef.current
    const canvas = canvasRef.current
    if (!wrapper || !canvas) return

    const { width, height } = wrapper.getBoundingClientRect()
    const pixelRatio = window.devicePixelRatio || 1
    canvas.width = width * pixelRatio
    canvas.height = height * pixelRatio
    canvas.style.width = `${width}px`
    canvas.style.height = `${height}px`
    const context = canvas.getContext('2d')
    if (context) context.scale(pixelRatio, pixelRatio)

    const columns = Math.floor((width + gap) / (dotSize + gap))
    const rows = Math.floor((height + gap) / (dotSize + gap))
    const cell = dotSize + gap
    const gridWidth = cell * columns - gap
    const gridHeight = cell * rows - gap
    const startX = (width - gridWidth) / 2 + dotSize / 2
    const startY = (height - gridHeight) / 2 + dotSize / 2

    const dots: Dot[] = []
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < columns; x++) {
        dots.push({
          cx: startX + x * cell,
          cy: startY + y * cell,
          xOffset: 0,
          yOffset: 0,
          inertiaApplied: false,
        })
      }
    }
    dotsRef.current = dots
  }, [dotSize, gap])

  useEffect(() => {
    if (!circlePath) return
    let animationFrame: number
    const proximitySquared = proximity * proximity

    const draw = () => {
      const canvas = canvasRef.current
      const context = canvas?.getContext('2d')
      if (!canvas || !context) return
      context.clearRect(0, 0, canvas.width, canvas.height)

      const { x: pointerX, y: pointerY } = pointerRef.current
      for (const dot of dotsRef.current) {
        const distanceX = dot.cx - pointerX
        const distanceY = dot.cy - pointerY
        const distanceSquared = distanceX * distanceX + distanceY * distanceY
        let color = baseColor
        if (distanceSquared <= proximitySquared) {
          const blend = 1 - Math.sqrt(distanceSquared) / proximity
          const red = Math.round(baseRgb.r + (activeRgb.r - baseRgb.r) * blend)
          const green = Math.round(baseRgb.g + (activeRgb.g - baseRgb.g) * blend)
          const blue = Math.round(baseRgb.b + (activeRgb.b - baseRgb.b) * blend)
          color = `rgb(${red},${green},${blue})`
        }
        context.save()
        context.translate(dot.cx + dot.xOffset, dot.cy + dot.yOffset)
        context.fillStyle = color
        context.fill(circlePath)
        context.restore()
      }
      animationFrame = requestAnimationFrame(draw)
    }

    draw()
    return () => cancelAnimationFrame(animationFrame)
  }, [activeRgb, baseColor, baseRgb, circlePath, proximity])

  useEffect(() => {
    buildGrid()
    const resizeObserver = new ResizeObserver(buildGrid)
    if (wrapperRef.current) resizeObserver.observe(wrapperRef.current)
    return () => resizeObserver.disconnect()
  }, [buildGrid])

  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const move = (event: MouseEvent) => {
      const canvas = canvasRef.current
      if (!canvas) return
      const now = performance.now()
      const pointer = pointerRef.current
      const elapsed = pointer.lastTime ? now - pointer.lastTime : 16
      const movementX = event.clientX - pointer.lastX
      const movementY = event.clientY - pointer.lastY
      let velocityX = (movementX / elapsed) * 1000
      let velocityY = (movementY / elapsed) * 1000
      let speed = Math.hypot(velocityX, velocityY)
      if (speed > maxSpeed) {
        const scale = maxSpeed / speed
        velocityX *= scale
        velocityY *= scale
        speed = maxSpeed
      }

      Object.assign(pointer, {
        lastTime: now,
        lastX: event.clientX,
        lastY: event.clientY,
        vx: velocityX,
        vy: velocityY,
        speed,
      })
      const bounds = canvas.getBoundingClientRect()
      pointer.x = event.clientX - bounds.left
      pointer.y = event.clientY - bounds.top

      for (const dot of dotsRef.current) {
        const distance = Math.hypot(dot.cx - pointer.x, dot.cy - pointer.y)
        if (speed <= speedTrigger || distance >= proximity || dot.inertiaApplied) continue
        dot.inertiaApplied = true
        gsap.killTweensOf(dot)
        gsap.to(dot, {
          inertia: {
            xOffset: dot.cx - pointer.x + velocityX * 0.005,
            yOffset: dot.cy - pointer.y + velocityY * 0.005,
            resistance,
          },
          onComplete: () => {
            gsap.to(dot, {
              xOffset: 0,
              yOffset: 0,
              duration: returnDuration,
              ease: 'elastic.out(1,0.75)',
            })
            dot.inertiaApplied = false
          },
        })
      }
    }

    const click = (event: MouseEvent) => {
      const canvas = canvasRef.current
      if (!canvas) return
      const bounds = canvas.getBoundingClientRect()
      const clickX = event.clientX - bounds.left
      const clickY = event.clientY - bounds.top
      for (const dot of dotsRef.current) {
        const distance = Math.hypot(dot.cx - clickX, dot.cy - clickY)
        if (distance >= shockRadius || dot.inertiaApplied) continue
        dot.inertiaApplied = true
        gsap.killTweensOf(dot)
        const falloff = Math.max(0, 1 - distance / shockRadius)
        gsap.to(dot, {
          inertia: {
            xOffset: (dot.cx - clickX) * shockStrength * falloff,
            yOffset: (dot.cy - clickY) * shockStrength * falloff,
            resistance,
          },
          onComplete: () => {
            gsap.to(dot, {
              xOffset: 0,
              yOffset: 0,
              duration: returnDuration,
              ease: 'elastic.out(1,0.75)',
            })
            dot.inertiaApplied = false
          },
        })
      }
    }

    const throttledMove = throttle(move, 50)
    window.addEventListener('mousemove', throttledMove, { passive: true })
    window.addEventListener('click', click)
    return () => {
      window.removeEventListener('mousemove', throttledMove)
      window.removeEventListener('click', click)
    }
  }, [
    maxSpeed,
    proximity,
    resistance,
    returnDuration,
    shockRadius,
    shockStrength,
    speedTrigger,
  ])

  return (
    <section className={`relative flex h-full w-full items-center justify-center ${className}`} style={style}>
      <div ref={wrapperRef} className="relative h-full w-full">
        <canvas ref={canvasRef} className="pointer-events-none absolute inset-0 h-full w-full" />
      </div>
    </section>
  )
}
