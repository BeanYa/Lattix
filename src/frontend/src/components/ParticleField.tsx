import { useEffect, useRef } from 'react'

interface Particle {
  x: number
  y: number
  vx: number
  vy: number
  radius: number
  alpha: number
}

export default function ParticleField() {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const context = canvas.getContext('2d')
    if (!context) return

    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
    let frame = 0
    let width = 0
    let height = 0
    let particles: Particle[] = []
    let running = !document.hidden

    const buildParticles = () => {
      const count = Math.min(76, Math.max(24, Math.round((width * height) / 26000)))
      particles = Array.from({ length: count }, (_, index) => ({
        x: (index * 97.3) % width,
        y: (index * 53.7) % height,
        vx: 0.035 + (index % 5) * 0.008,
        vy: -0.018 + (index % 3) * 0.012,
        radius: 0.45 + (index % 4) * 0.18,
        alpha: 0.12 + (index % 5) * 0.025,
      }))
    }

    const resize = () => {
      width = window.innerWidth
      height = window.innerHeight
      const ratio = Math.min(window.devicePixelRatio || 1, 1.5)
      canvas.width = Math.round(width * ratio)
      canvas.height = Math.round(height * ratio)
      canvas.style.width = `${width}px`
      canvas.style.height = `${height}px`
      context.setTransform(ratio, 0, 0, ratio, 0, 0)
      buildParticles()
    }

    const draw = () => {
      context.clearRect(0, 0, width, height)
      particles.forEach((particle, index) => {
        if (!reducedMotion.matches) {
          particle.x += particle.vx
          particle.y += particle.vy
          if (particle.x > width + 3) particle.x = -3
          if (particle.y < -3) particle.y = height + 3
        }

        context.beginPath()
        context.arc(particle.x, particle.y, particle.radius, 0, Math.PI * 2)
        context.fillStyle = `rgba(119, 255, 201, ${particle.alpha})`
        context.fill()

        const next = particles[(index + 7) % particles.length]
        const distance = Math.hypot(next.x - particle.x, next.y - particle.y)
        if (distance < 116) {
          context.beginPath()
          context.moveTo(particle.x, particle.y)
          context.lineTo(next.x, next.y)
          context.strokeStyle = `rgba(103, 241, 186, ${0.025 * (1 - distance / 116)})`
          context.stroke()
        }
      })
      if (running && !reducedMotion.matches) frame = window.requestAnimationFrame(draw)
    }

    const onVisibilityChange = () => {
      running = !document.hidden
      window.cancelAnimationFrame(frame)
      if (running) draw()
    }

    resize()
    draw()
    window.addEventListener('resize', resize)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('resize', resize)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [])

  return <canvas ref={canvasRef} className="particle-field" aria-hidden="true" />
}
