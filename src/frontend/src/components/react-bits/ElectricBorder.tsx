import {
  useCallback,
  useEffect,
  useRef,
  type CSSProperties,
  type ReactNode,
} from 'react'

function hexToRgba(hex: string, alpha = 1): string {
  if (!hex) return `rgba(0,0,0,${alpha})`
  let value = hex.replace('#', '')
  if (value.length === 3) value = value.split('').map((character) => character + character).join('')
  const integer = Number.parseInt(value.slice(0, 6), 16)
  const red = (integer >> 16) & 255
  const green = (integer >> 8) & 255
  const blue = integer & 255
  return `rgba(${red}, ${green}, ${blue}, ${alpha})`
}

interface ElectricBorderProps {
  children?: ReactNode
  color?: string
  speed?: number
  chaos?: number
  borderRadius?: number
  className?: string
  style?: CSSProperties
}

export default function ElectricBorder({
  children,
  color = '#5227FF',
  speed = 1,
  chaos = 0.12,
  borderRadius = 24,
  className,
  style,
}: ElectricBorderProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const animationRef = useRef<number | null>(null)
  const timeRef = useRef(0)
  const lastFrameTimeRef = useRef(0)

  const random = useCallback((x: number): number => (Math.sin(x * 12.9898) * 43758.5453) % 1, [])

  const noise2D = useCallback((x: number, y: number): number => {
    const integerX = Math.floor(x)
    const integerY = Math.floor(y)
    const fractionX = x - integerX
    const fractionY = y - integerY
    const a = random(integerX + integerY * 57)
    const b = random(integerX + 1 + integerY * 57)
    const c = random(integerX + (integerY + 1) * 57)
    const d = random(integerX + 1 + (integerY + 1) * 57)
    const smoothX = fractionX * fractionX * (3 - 2 * fractionX)
    const smoothY = fractionY * fractionY * (3 - 2 * fractionY)
    return a * (1 - smoothX) * (1 - smoothY)
      + b * smoothX * (1 - smoothY)
      + c * (1 - smoothX) * smoothY
      + d * smoothX * smoothY
  }, [random])

  const octavedNoise = useCallback((
    x: number,
    octaves: number,
    lacunarity: number,
    gain: number,
    baseAmplitude: number,
    baseFrequency: number,
    time: number,
    seed: number,
    baseFlatness: number,
  ): number => {
    let result = 0
    let amplitude = baseAmplitude
    let frequency = baseFrequency
    for (let index = 0; index < octaves; index++) {
      let octaveAmplitude = amplitude
      if (index === 0) octaveAmplitude *= baseFlatness
      result += octaveAmplitude * noise2D(frequency * x + seed * 100, time * frequency * 0.3)
      frequency *= lacunarity
      amplitude *= gain
    }
    return result
  }, [noise2D])

  const getCornerPoint = useCallback((
    centerX: number,
    centerY: number,
    radius: number,
    startAngle: number,
    arcLength: number,
    progress: number,
  ) => {
    const angle = startAngle + progress * arcLength
    return {
      x: centerX + radius * Math.cos(angle),
      y: centerY + radius * Math.sin(angle),
    }
  }, [])

  const getRoundedRectPoint = useCallback((
    progress: number,
    left: number,
    top: number,
    width: number,
    height: number,
    radius: number,
  ) => {
    const straightWidth = width - 2 * radius
    const straightHeight = height - 2 * radius
    const cornerArc = (Math.PI * radius) / 2
    const perimeter = 2 * straightWidth + 2 * straightHeight + 4 * cornerArc
    const distance = progress * perimeter
    let accumulated = 0

    if (distance <= accumulated + straightWidth) {
      const amount = (distance - accumulated) / straightWidth
      return { x: left + radius + amount * straightWidth, y: top }
    }
    accumulated += straightWidth
    if (distance <= accumulated + cornerArc) {
      return getCornerPoint(
        left + width - radius,
        top + radius,
        radius,
        -Math.PI / 2,
        Math.PI / 2,
        (distance - accumulated) / cornerArc,
      )
    }
    accumulated += cornerArc
    if (distance <= accumulated + straightHeight) {
      const amount = (distance - accumulated) / straightHeight
      return { x: left + width, y: top + radius + amount * straightHeight }
    }
    accumulated += straightHeight
    if (distance <= accumulated + cornerArc) {
      return getCornerPoint(
        left + width - radius,
        top + height - radius,
        radius,
        0,
        Math.PI / 2,
        (distance - accumulated) / cornerArc,
      )
    }
    accumulated += cornerArc
    if (distance <= accumulated + straightWidth) {
      const amount = (distance - accumulated) / straightWidth
      return { x: left + width - radius - amount * straightWidth, y: top + height }
    }
    accumulated += straightWidth
    if (distance <= accumulated + cornerArc) {
      return getCornerPoint(
        left + radius,
        top + height - radius,
        radius,
        Math.PI / 2,
        Math.PI / 2,
        (distance - accumulated) / cornerArc,
      )
    }
    accumulated += cornerArc
    if (distance <= accumulated + straightHeight) {
      const amount = (distance - accumulated) / straightHeight
      return { x: left, y: top + height - radius - amount * straightHeight }
    }
    accumulated += straightHeight
    return getCornerPoint(
      left + radius,
      top + radius,
      radius,
      Math.PI,
      Math.PI / 2,
      (distance - accumulated) / cornerArc,
    )
  }, [getCornerPoint])

  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    const context = canvas?.getContext('2d')
    if (!canvas || !container || !context) return

    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const borderOffset = 60
    const displacement = 60
    let pixelRatio = Math.min(window.devicePixelRatio || 1, 2)

    const updateSize = () => {
      const bounds = container.getBoundingClientRect()
      const width = bounds.width + borderOffset * 2
      const height = bounds.height + borderOffset * 2
      pixelRatio = Math.min(window.devicePixelRatio || 1, 2)
      canvas.width = width * pixelRatio
      canvas.height = height * pixelRatio
      canvas.style.width = `${width}px`
      canvas.style.height = `${height}px`
      context.scale(pixelRatio, pixelRatio)
      return { width, height }
    }

    let { width, height } = updateSize()
    const draw = (currentTime: number) => {
      const elapsed = (currentTime - lastFrameTimeRef.current) / 1000
      timeRef.current += elapsed * speed
      lastFrameTimeRef.current = currentTime
      context.setTransform(1, 0, 0, 1, 0, 0)
      context.clearRect(0, 0, canvas.width, canvas.height)
      context.scale(pixelRatio, pixelRatio)
      context.strokeStyle = color
      context.lineWidth = 1
      context.lineCap = 'round'
      context.lineJoin = 'round'

      const left = borderOffset
      const top = borderOffset
      const borderWidth = width - borderOffset * 2
      const borderHeight = height - borderOffset * 2
      const radius = Math.min(borderRadius, Math.min(borderWidth, borderHeight) / 2)
      const approximatePerimeter = 2 * (borderWidth + borderHeight) + 2 * Math.PI * radius
      const sampleCount = Math.floor(approximatePerimeter / 2)
      context.beginPath()
      for (let index = 0; index <= sampleCount; index++) {
        const progress = index / sampleCount
        const point = getRoundedRectPoint(progress, left, top, borderWidth, borderHeight, radius)
        const xNoise = octavedNoise(progress * 8, 10, 1.6, 0.7, chaos, 10, timeRef.current, 0, 0)
        const yNoise = octavedNoise(progress * 8, 10, 1.6, 0.7, chaos, 10, timeRef.current, 1, 0)
        const x = point.x + xNoise * displacement
        const y = point.y + yNoise * displacement
        if (index === 0) context.moveTo(x, y)
        else context.lineTo(x, y)
      }
      context.closePath()
      context.stroke()
      if (!reducedMotion) animationRef.current = requestAnimationFrame(draw)
    }

    const resizeObserver = new ResizeObserver(() => {
      const size = updateSize()
      width = size.width
      height = size.height
    })
    resizeObserver.observe(container)
    animationRef.current = requestAnimationFrame(draw)
    return () => {
      if (animationRef.current) cancelAnimationFrame(animationRef.current)
      resizeObserver.disconnect()
    }
  }, [borderRadius, chaos, color, getRoundedRectPoint, octavedNoise, speed])

  return (
    <div
      ref={containerRef}
      className={`relative isolate overflow-visible ${className ?? ''}`}
      style={{ '--electric-border-color': color, borderRadius, ...style } as CSSProperties}
    >
      <div className="pointer-events-none absolute left-1/2 top-1/2 z-[2] -translate-x-1/2 -translate-y-1/2">
        <canvas ref={canvasRef} className="block" />
      </div>
      <div className="pointer-events-none absolute inset-0 z-0 rounded-[inherit]">
        <div
          className="pointer-events-none absolute inset-0 rounded-[inherit]"
          style={{ border: `2px solid ${hexToRgba(color, 0.6)}`, filter: 'blur(1px)' }}
        />
        <div
          className="pointer-events-none absolute inset-0 rounded-[inherit]"
          style={{ border: `2px solid ${color}`, filter: 'blur(4px)' }}
        />
        <div
          className="pointer-events-none absolute inset-0 -z-[1] scale-110 rounded-[inherit] opacity-30"
          style={{ filter: 'blur(32px)', background: `linear-gradient(-30deg, ${color}, transparent, ${color})` }}
        />
      </div>
      <div className="relative z-[1] rounded-[inherit]">{children}</div>
    </div>
  )
}
