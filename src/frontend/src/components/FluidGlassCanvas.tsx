import { useEffect, useRef, useState } from 'react'

import { cn } from '@/lib/utils'

// Canonical shader core from csuyincs-creator/fluidglass-ui (Apache-2.0).
// https://github.com/csuyincs-creator/fluidglass-ui
const VERTEX_SHADER = `
  attribute vec2 a_position;
  varying vec2 v_uv;
  void main(){
    v_uv=a_position*.5+.5;
    gl_Position=vec4(a_position,0.0,1.0);
  }
`

const FRAGMENT_SHADER = `
  precision highp float;
  varying vec2 v_uv;
  uniform vec2 u_resolution;
  uniform vec2 u_mouse;
  uniform vec2 u_mouseVelocity;
  uniform float u_mouseMix;
  uniform float u_time;
  uniform float u_speed;
  uniform float u_intensity;
  uniform float u_pointer;
  uniform float u_seed;
  uniform float u_surfaceOpacity;
  uniform vec3 u_colorA;
  uniform vec3 u_colorB;
  uniform vec3 u_colorC;

  float hash(vec2 p){
    p=fract(p*vec2(123.34,456.21));
    p+=dot(p,p+45.32);
    return fract(p.x*p.y);
  }
  float noise(vec2 p){
    vec2 i=floor(p),f=fract(p);
    f=f*f*(3.0-2.0*f);
    return mix(mix(hash(i),hash(i+vec2(1.0,0.0)),f.x),mix(hash(i+vec2(0.0,1.0)),hash(i+vec2(1.0,1.0)),f.x),f.y);
  }
  float fbm(vec2 p){
    float value=0.0;
    float amp=.53;
    mat2 rot=mat2(.80,-.60,.60,.80);
    for(int i=0;i<5;i++){
      value+=amp*noise(p);
      p=rot*p*2.02+vec2(17.13,9.27);
      amp*=.49;
    }
    return value;
  }
  float softBlob(vec2 p,vec2 center,float radius,float softness){
    return 1.0-smoothstep(radius-softness,radius+softness,length(p-center));
  }
  void main(){
    vec2 uv=v_uv;
    float aspect=u_resolution.x/max(1.0,u_resolution.y);
    vec2 p=(uv-.5)*vec2(aspect,1.0);
    vec2 mouse=(u_mouse-.5)*vec2(aspect,1.0);
    vec2 delta=p-mouse;
    float dist=length(delta);
    float mouseField=exp(-dist*dist*7.2)*u_mouseMix*u_pointer;
    vec2 normal=delta/max(dist,.035);
    vec2 tangent=vec2(-normal.y,normal.x);
    p+=normal*mouseField*.115+tangent*mouseField*(u_mouseVelocity.x-u_mouseVelocity.y)*.045;

    float t=u_time*u_speed;
    vec2 seedVec=vec2(u_seed*1.713,u_seed*.937);
    float w1=fbm(p*1.22+seedVec+vec2(t*.075,-t*.052));
    float w2=fbm(p*1.54-seedVec*.37+vec2(-t*.057,t*.064)+w1*.82);
    vec2 q=p+(vec2(w1,w2)-.5)*(.58*u_intensity);
    float broad=fbm(q*1.12+vec2(t*.041,-t*.033));
    float detail=fbm(q*2.18+vec2(-t*.083,t*.057)+broad*.95);
    float ribbon=.5+.5*sin(q.x*3.15+q.y*.76+detail*5.0+t*.25+u_seed);
    float colorMix=smoothstep(.16,.88,broad*.61+ribbon*.39);
    vec3 fluid=mix(u_colorA,u_colorB,colorMix);
    float shadow=smoothstep(.43,.84,detail*.69+(.5+.5*sin(q.y*4.2-q.x*.8-t*.17))*.31);
    fluid=mix(fluid,u_colorC,shadow*.74);

    float plume1=softBlob(p,vec2(aspect*.23+.12*sin(t*.08+u_seed),.16*cos(t*.11+u_seed)),.52,.38);
    float plume2=softBlob(p,vec2(aspect*.39+.10*cos(t*.07-u_seed),-.24+.11*sin(t*.09)),.43,.34);
    float haze=clamp(plume1*.72+plume2*.58,0.0,1.0);
    float reveal=smoothstep(.055,.735,uv.x+(.5-broad)*.27+.070*sin(uv.y*4.0+t*.12));
    reveal*=mix(.70,1.0,haze);
    reveal=clamp(reveal*u_intensity,0.0,1.0);

    float spec=pow(clamp(1.0-abs(detail-.52)*2.0,0.0,1.0),5.0)*reveal;
    float caustic=pow(clamp(.52+.48*sin((q.x-q.y)*5.2+detail*7.0-t*.18),0.0,1.0),7.0)*reveal;
    vec3 cyanGlow=mix(fluid,vec3(.34,1.0,.90),spec*.18+caustic*.09);
    cyanGlow*=.78+.25*haze;
    cyanGlow=mix(cyanGlow,cyanGlow*.70,u_surfaceOpacity*.34);
    float filament=smoothstep(.48,.86,detail)*reveal;
    float density=clamp(reveal*(.36+.48*haze)+filament*.22+mouseField*.28,0.0,1.0);
    float alpha=clamp(.035*haze+density*(.24+.50*u_intensity)+spec*.08+u_surfaceOpacity*density*.14,0.0,.92);
    float edgeFade=smoothstep(1.08,.70,length((uv-.5)*vec2(1.0,.92)));
    alpha*=edgeFade;
    cyanGlow=pow(max(cyanGlow,0.0),vec3(.94));
    gl_FragColor=vec4(cyanGlow,alpha);
  }
`

export interface FluidGlassConfig {
  colorA: string
  colorB: string
  colorC: string
  speed?: number
  intensity?: number
  pointer?: number
  surface?: number
  seed?: number
}

const MAX_CONTEXTS = 8
const renderers = new Set<FluidGlassRenderer>()
let liveContexts = 0
let animationFrame = 0

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value))
}

function hexToRgb(value: string) {
  const normalized = value.replace('#', '')
  const numeric = Number.parseInt(
    normalized.length === 3
      ? normalized
          .split('')
          .map((part) => part + part)
          .join('')
      : normalized,
    16,
  )
  return [((numeric >> 16) & 255) / 255, ((numeric >> 8) & 255) / 255, (numeric & 255) / 255]
}

function compileShader(gl: WebGLRenderingContext, type: number, source: string) {
  const shader = gl.createShader(type)
  if (!shader) throw new Error('Shader allocation failed')
  gl.shaderSource(shader, source)
  gl.compileShader(shader)
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const stage = type === gl.FRAGMENT_SHADER ? 'fragment' : 'vertex'
    const message = gl.getShaderInfoLog(shader) || 'No shader compiler log available'
    gl.deleteShader(shader)
    throw new Error(`${stage}: ${message}`)
  }
  return shader
}

function buildProgram(gl: WebGLRenderingContext) {
  const program = gl.createProgram()
  if (!program) throw new Error('Program allocation failed')
  const vertex = compileShader(gl, gl.VERTEX_SHADER, VERTEX_SHADER)
  const fragment = compileShader(gl, gl.FRAGMENT_SHADER, FRAGMENT_SHADER)
  gl.attachShader(program, vertex)
  gl.attachShader(program, fragment)
  gl.linkProgram(program)
  gl.deleteShader(vertex)
  gl.deleteShader(fragment)
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) || 'Program link failed'
    gl.deleteProgram(program)
    throw new Error(message)
  }
  return program
}

function runFrame(now: number) {
  renderers.forEach((renderer) => renderer.render(now))
  animationFrame = window.requestAnimationFrame(runFrame)
}

function registerRenderer(renderer: FluidGlassRenderer) {
  renderers.add(renderer)
  if (!animationFrame) animationFrame = window.requestAnimationFrame(runFrame)
}

function unregisterRenderer(renderer: FluidGlassRenderer) {
  renderers.delete(renderer)
  if (renderers.size === 0 && animationFrame) {
    window.cancelAnimationFrame(animationFrame)
    animationFrame = 0
  }
}

class FluidGlassRenderer {
  private element: HTMLElement
  private canvas: HTMLCanvasElement
  private index: number
  private gl: WebGLRenderingContext | null = null
  private program: WebGLProgram | null = null
  private buffer: WebGLBuffer | null = null
  private uniforms = new Map<string, WebGLUniformLocation | null>()
  private controller = new AbortController()
  private resizeObserver: ResizeObserver
  private intersectionObserver: IntersectionObserver
  private active = true
  private lastFrame = 0
  private mouse = [0.76, 0.46]
  private mouseTarget = [0.76, 0.46]
  private mouseVelocity = [0, 0]
  private mouseMix = 0
  private pointerInside = false
  private config: Required<FluidGlassConfig>
  private failureReason = ''

  constructor(
    element: HTMLElement,
    canvas: HTMLCanvasElement,
    config: FluidGlassConfig,
    index: number,
  ) {
    this.element = element
    this.canvas = canvas
    this.index = index
    this.config = this.normalizeConfig(config)
    this.resizeObserver = new ResizeObserver(() => this.resize())
    this.intersectionObserver = new IntersectionObserver(
      (entries) => {
        this.active = entries[0]?.isIntersecting !== false
      },
      { rootMargin: '80px' },
    )
    this.bindPointer()
    this.resizeObserver.observe(element)
    this.intersectionObserver.observe(element)
  }

  private normalizeConfig(config: FluidGlassConfig): Required<FluidGlassConfig> {
    return {
      colorA: config.colorA,
      colorB: config.colorB,
      colorC: config.colorC,
      speed: config.speed ?? 0.92,
      intensity: config.intensity ?? 1.02,
      pointer: config.pointer ?? 0.82,
      surface: config.surface ?? 0.08,
      seed: config.seed ?? 1.7,
    }
  }

  setConfig(config: FluidGlassConfig) {
    this.config = this.normalizeConfig(config)
  }

  private bindPointer() {
    const signal = this.controller.signal
    let last = [0, 0]
    let lastTime = 0
    const update = (event: PointerEvent) => {
      const rect = this.element.getBoundingClientRect()
      const x = clamp((event.clientX - rect.left) / Math.max(rect.width, 1), 0, 1)
      const y = clamp(1 - (event.clientY - rect.top) / Math.max(rect.height, 1), 0, 1)
      const now = performance.now()
      const delta = Math.max(8, now - lastTime || 16)
      this.mouseTarget = [x, y]
      this.mouseVelocity = [
        clamp((x - last[0]) / (delta / 16.67), -0.12, 0.12),
        clamp((y - last[1]) / (delta / 16.67), -0.12, 0.12),
      ]
      last = [x, y]
      lastTime = now
      this.mouseMix = 1
    }
    this.element.addEventListener(
      'pointerenter',
      (event) => {
        this.pointerInside = true
        update(event)
      },
      { signal, passive: true },
    )
    this.element.addEventListener('pointermove', update, { signal, passive: true })
    this.element.addEventListener(
      'pointerleave',
      () => {
        this.pointerInside = false
        this.mouseTarget = [0.76, 0.46]
      },
      { signal, passive: true },
    )
    this.element.addEventListener(
      'pointerdown',
      (event) => {
        this.mouseMix = 1.2
        update(event)
      },
      { signal, passive: true },
    )
    this.canvas.addEventListener('webglcontextlost', (event) => event.preventDefault(), { signal })
  }

  init() {
    if (liveContexts >= MAX_CONTEXTS) {
      this.failureReason = 'WebGL context budget reached'
      return false
    }
    try {
      const gl = this.canvas.getContext('webgl', {
        alpha: true,
        antialias: false,
        depth: false,
        stencil: false,
        premultipliedAlpha: false,
        preserveDrawingBuffer: false,
        powerPreference: 'high-performance',
      })
      if (!gl) {
        this.failureReason = 'WebGL context unavailable'
        return false
      }
      this.gl = gl
      liveContexts += 1
      gl.clearColor(0, 0, 0, 0)
      this.program = buildProgram(gl)
      gl.useProgram(this.program)
      this.buffer = gl.createBuffer()
      gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer)
      gl.bufferData(
        gl.ARRAY_BUFFER,
        new Float32Array([-1, -1, 1, -1, -1, 1, -1, 1, 1, -1, 1, 1]),
        gl.STATIC_DRAW,
      )
      const position = gl.getAttribLocation(this.program, 'a_position')
      gl.enableVertexAttribArray(position)
      gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0)
      ;[
        'u_resolution',
        'u_mouse',
        'u_mouseVelocity',
        'u_mouseMix',
        'u_time',
        'u_speed',
        'u_intensity',
        'u_pointer',
        'u_seed',
        'u_surfaceOpacity',
        'u_colorA',
        'u_colorB',
        'u_colorC',
      ].forEach((name) => this.uniforms.set(name, gl.getUniformLocation(this.program!, name)))
      this.resize()
      registerRenderer(this)
      return true
    } catch (error) {
      this.failureReason = error instanceof Error ? error.message : 'WebGL initialization failed'
      this.destroyGl()
      return false
    }
  }

  getFailureReason() {
    return this.failureReason
  }

  private resize() {
    if (!this.gl) return
    const rect = this.element.getBoundingClientRect()
    const dpr = Math.min(window.devicePixelRatio || 1, 1.5)
    const width = Math.max(2, Math.round(rect.width * dpr))
    const height = Math.max(2, Math.round(rect.height * dpr))
    if (this.canvas.width === width && this.canvas.height === height) return
    this.canvas.width = width
    this.canvas.height = height
    this.gl.viewport(0, 0, width, height)
  }

  render(now: number) {
    if (!this.gl || !this.program || !this.active || document.hidden) return
    if (now - this.lastFrame < 1000 / 45) return
    this.lastFrame = now
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    this.mouse[0] += (this.mouseTarget[0] - this.mouse[0]) * 0.105
    this.mouse[1] += (this.mouseTarget[1] - this.mouse[1]) * 0.105
    this.mouseVelocity[0] *= 0.9
    this.mouseVelocity[1] *= 0.9
    this.mouseMix += ((this.pointerInside ? 1 : 0) - this.mouseMix) * 0.075
    if (!this.pointerInside) this.mouseMix *= 0.945
    const gl = this.gl
    const config = this.config
    gl.useProgram(this.program)
    gl.uniform2f(this.uniforms.get('u_resolution') ?? null, this.canvas.width, this.canvas.height)
    gl.uniform2f(this.uniforms.get('u_mouse') ?? null, this.mouse[0], this.mouse[1])
    gl.uniform2f(
      this.uniforms.get('u_mouseVelocity') ?? null,
      this.mouseVelocity[0],
      this.mouseVelocity[1],
    )
    gl.uniform1f(this.uniforms.get('u_mouseMix') ?? null, clamp(this.mouseMix, 0, 1.2))
    gl.uniform1f(this.uniforms.get('u_time') ?? null, now * 0.001 + this.index * 3.73)
    gl.uniform1f(
      this.uniforms.get('u_speed') ?? null,
      reduced ? Math.min(config.speed, 0.08) : config.speed,
    )
    gl.uniform1f(this.uniforms.get('u_intensity') ?? null, config.intensity)
    gl.uniform1f(this.uniforms.get('u_pointer') ?? null, config.pointer)
    gl.uniform1f(this.uniforms.get('u_seed') ?? null, config.seed)
    gl.uniform1f(this.uniforms.get('u_surfaceOpacity') ?? null, config.surface)
    gl.uniform3fv(this.uniforms.get('u_colorA') ?? null, hexToRgb(config.colorA))
    gl.uniform3fv(this.uniforms.get('u_colorB') ?? null, hexToRgb(config.colorB))
    gl.uniform3fv(this.uniforms.get('u_colorC') ?? null, hexToRgb(config.colorC))
    gl.clear(gl.COLOR_BUFFER_BIT)
    gl.drawArrays(gl.TRIANGLES, 0, 6)
  }

  private destroyGl() {
    if (!this.gl) return
    try {
      if (this.program) this.gl.deleteProgram(this.program)
    } catch {
      /* Context can already be lost. */
    }
    try {
      if (this.buffer) this.gl.deleteBuffer(this.buffer)
    } catch {
      /* Context can already be lost. */
    }
    liveContexts = Math.max(0, liveContexts - 1)
    this.gl = null
    this.program = null
    this.buffer = null
  }

  destroy() {
    unregisterRenderer(this)
    this.resizeObserver.disconnect()
    this.intersectionObserver.disconnect()
    this.controller.abort()
    this.destroyGl()
  }
}

export default function FluidGlassCanvas({
  config,
  index,
}: {
  config: FluidGlassConfig
  index: number
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const configRef = useRef(config)
  const rendererRef = useRef<FluidGlassRenderer | null>(null)
  const [mode, setMode] = useState<'loading' | 'webgl' | 'fallback'>('loading')
  configRef.current = config

  useEffect(() => {
    const canvas = canvasRef.current
    const element = canvas?.parentElement
    if (!canvas || !element) return
    const renderer = new FluidGlassRenderer(element, canvas, configRef.current, index)
    rendererRef.current = renderer
    const initialized = renderer.init()
    canvas.dataset.fluidReason = initialized ? '' : renderer.getFailureReason()
    setMode(initialized ? 'webgl' : 'fallback')
    return () => {
      renderer.destroy()
      rendererRef.current = null
    }
  }, [index])

  useEffect(() => rendererRef.current?.setConfig(config), [config])

  return (
    <>
      <canvas
        ref={canvasRef}
        className="fluid-metric-canvas"
        data-fluid-mode={mode}
        aria-hidden="true"
      />
      <span
        className={cn('fluid-metric-plume', mode === 'webgl' && 'is-hidden')}
        aria-hidden="true"
      />
    </>
  )
}
