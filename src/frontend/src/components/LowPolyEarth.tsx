import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import simplify from '@turf/simplify'
import type { FeatureCollection, Position } from 'geojson'
import type { GlobeMethods } from 'react-globe.gl'
import {
  Color,
  DodecahedronGeometry,
  Float32BufferAttribute,
  Group,
  IcosahedronGeometry,
  Mesh,
  MeshLambertMaterial,
  MeshToonMaterial,
  Quaternion,
  Vector3,
  type BufferGeometry,
  type Material,
  type Object3D,
} from 'three'

import { formatByteRate } from '@/lib/format'
import { useTheme } from '@/lib/theme-context'
import { DEFAULT_EARTH_PALETTE, readEarthPalette, type EarthPalette } from '@/lib/visual-theme'

const Globe = lazy(() => import('react-globe.gl'))

const AXIAL_TILT_DEGREES = 36
const INITIAL_LONGITUDE = 105
const SELF_ROTATION_RADIANS_PER_SECOND = 0.12
const CLOUD_VOLUME_SCALE = 1.8
const CLOUD_DRAG_FOLLOW = 0.58
const CLOUD_CATCH_UP_STRENGTH = 3.6
const CLOUD_MAX_OFFSET_RADIANS = 0.26
const LAND_CURVATURE_RESOLUTION = 12
const LAND_SIMPLIFICATION_TOLERANCE = 0.4
const SOUTH_POLAR_SEAM_LATITUDE = -60
const ANTIMERIDIAN_JUMP_DEGREES = 300
const DESKTOP_MIN_CAMERA_DISTANCE = 260
const MOBILE_MIN_CAMERA_DISTANCE = 300
const MAX_CAMERA_DISTANCE = 480
const LINK_DASH_LENGTH = 0.03
const LINK_DASH_GAP = 0.02
const LINK_PATTERN_LENGTH = LINK_DASH_LENGTH + LINK_DASH_GAP

export type EarthNodeStatus = 'online' | 'offline' | 'warning'
export type EarthLinkStatus = 'pending' | 'applying' | 'active' | 'degraded' | 'failed'

export interface EarthNode {
  id: string | number
  label: string
  description?: string
  clusterKey?: string
  lat: number
  lng: number
  status: EarthNodeStatus
  online?: boolean
  countryCode?: string
  uploadRate?: number | null
  downloadRate?: number | null
}

export interface EarthLink {
  id: string
  startLat: number
  startLng: number
  endLat: number
  endLng: number
  status: EarthLinkStatus
}

export interface LowPolyEarthProps {
  nodes: EarthNode[]
  links: EarthLink[]
  ariaLabel?: string
  className?: string
}

interface AnimatedEarthLink extends EarthLink {
  dashPhase: number
  dashDuration: number
}

interface EarthNodeLabel extends EarthNode {
  members: EarthNode[]
}

const cloudLocations = [
  { lat: 45, lng: -145, scale: 0.9 },
  { lat: 18, lng: -76, scale: 1.05 },
  { lat: -18, lng: -22, scale: 0.82 },
  { lat: 48, lng: 34, scale: 0.78 },
  { lat: 4, lng: 88, scale: 1.08 },
  { lat: -34, lng: 146, scale: 0.92 },
]

function normalizeLongitudeDelta(value: number) {
  return ((value + 540) % 360) - 180
}

function buildLinkMotion(id: string) {
  let hash = 2166136261
  for (let index = 0; index < id.length; index += 1) {
    hash = Math.imul(hash ^ id.charCodeAt(index), 16777619)
  }
  const phase = (hash >>> 0) / 4294967295
  const durationSeed = ((hash >>> 8) & 0xffff) / 0xffff
  return {
    dashPhase: phase * LINK_PATTERN_LENGTH,
    dashDuration: 3400 + durationSeed * 1600,
  }
}

function globePosition(lat: number, lng: number, altitude: number) {
  const radius = 100 * (1 + altitude)
  const phi = (90 - lat) * (Math.PI / 180)
  const theta = (90 - lng) * (Math.PI / 180)
  return {
    x: radius * Math.sin(phi) * Math.cos(theta),
    y: radius * Math.cos(phi),
    z: radius * Math.sin(phi) * Math.sin(theta),
  }
}

function closeSouthPolarSeam(ring: Position[]) {
  const closedRing: Position[] = []

  ring.forEach((position, index) => {
    closedRing.push(position)
    const nextPosition = ring[index + 1]
    if (!nextPosition) return

    const crossesAntimeridian = Math.abs(position[0] - nextPosition[0]) > ANTIMERIDIAN_JUMP_DEGREES
    const isSouthPolarEdge = position[1] < SOUTH_POLAR_SEAM_LATITUDE
      && nextPosition[1] < SOUTH_POLAR_SEAM_LATITUDE
      && Math.max(position[1], nextPosition[1]) > -89.5
    if (!crossesAntimeridian || !isSouthPolarEdge) return

    if (position[0] > nextPosition[0]) {
      closedRing.push([180, -90], [-180, -90])
    } else {
      closedRing.push([-180, -90], [180, -90])
    }
  })

  return closedRing
}

function closeSouthPolarPolygons(collection: FeatureCollection): FeatureCollection {
  return {
    ...collection,
    features: collection.features.map((feature) => {
      const isAntarctica = feature.id === 'ATA' || feature.properties?.name === 'Antarctica'
      if (!isAntarctica || !feature.geometry) return feature

      if (feature.geometry.type === 'Polygon') {
        return {
          ...feature,
          geometry: {
            ...feature.geometry,
            coordinates: feature.geometry.coordinates.map(closeSouthPolarSeam),
          },
        }
      }

      if (feature.geometry.type === 'MultiPolygon') {
        return {
          ...feature,
          geometry: {
            ...feature.geometry,
            coordinates: feature.geometry.coordinates.map((polygon) => polygon.map(closeSouthPolarSeam)),
          },
        }
      }

      return feature
    }),
  }
}

function linkColor(link: AnimatedEarthLink, palette: EarthPalette) {
  if (link.status === 'active') return palette.linkActive
  if (link.status === 'degraded') return palette.linkDegraded
  if (link.status === 'failed') return palette.linkFailed
  return palette.linkPending
}

function clusterNodeLabels(nodes: EarthNode[]): EarthNodeLabel[] {
  const groups = new Map<string, EarthNode[]>()
  nodes.forEach((node) => {
    const key = node.clusterKey ?? `node:${node.id}`
    const members = groups.get(key)
    if (members) members.push(node)
    else groups.set(key, [node])
  })
  return Array.from(groups.values(), (members) => ({ ...members[0], members }))
}

function createNodeElement(item: object, palette: EarthPalette) {
  const node = item as EarthNodeLabel
  const members = node.members ?? [node]
  const root = document.createElement('div')
  root.setAttribute('aria-hidden', 'true')
  root.style.pointerEvents = 'none'
  root.style.opacity = '0'
  root.style.filter = 'blur(12px)'
  root.style.transition = 'opacity 500ms ease, filter 500ms ease'
  root.style.willChange = 'opacity, filter'
  root.style.zIndex = '100'

  root.style.display = 'grid'
  root.style.gap = '4px'
  root.style.transform = `rotate(-${AXIAL_TILT_DEGREES}deg)`

  const createBadge = (member: EarthNode) => {
    const badge = document.createElement('div')
    badge.className = 'earth-node-badge'
    badge.style.display = 'grid'
    badge.style.gap = '4px'
    badge.style.padding = '6px 8px'
    badge.style.borderRadius = '6px'
    badge.style.fontFamily = "'Fusion Pixel 10px Proportional SC', 'Microsoft YaHei', sans-serif"
    badge.style.fontSize = '12px'
    badge.style.fontWeight = '400'
    badge.style.fontSynthesis = 'none'
    badge.style.letterSpacing = '0'
    badge.style.lineHeight = '1.15'
    badge.style.whiteSpace = 'nowrap'

    const header = document.createElement('div')
    header.style.display = 'flex'
    header.style.alignItems = 'center'
    header.style.gap = '6px'

    const normalizedCountryCode = member.countryCode?.trim().toLowerCase() ?? ''
    if (/^[a-z]{2}$/.test(normalizedCountryCode)) {
      const flag = document.createElement('span')
      flag.className = `fi fi-${normalizedCountryCode}`
      flag.style.width = '16px'
      flag.style.flex = '0 0 16px'
      flag.style.borderRadius = '2px'
      header.append(flag)
    }

    const label = document.createElement('span')
    label.textContent = member.label
    label.style.maxWidth = '128px'
    label.style.overflow = 'hidden'
    label.style.textOverflow = 'ellipsis'

    const status = document.createElement('span')
    status.style.width = '7px'
    status.style.height = '7px'
    status.style.flex = '0 0 7px'
    status.style.borderRadius = '999px'
    status.style.background = member.status === 'warning'
      ? palette.nodeWarning
      : member.status === 'online' ? palette.nodeOnline : palette.nodeOffline
    status.style.marginLeft = 'auto'

    header.append(label, status)

    const rates = document.createElement('div')
    rates.className = 'earth-node-rates'
    rates.style.display = 'grid'
    rates.style.gap = '2px'
    rates.style.fontVariantNumeric = 'tabular-nums'

    const addRate = (direction: 'upload' | 'download', value: number | null | undefined) => {
      const row = document.createElement('span')
      row.className = `earth-node-rate earth-node-rate-${direction}`
      const arrow = direction === 'upload' ? '↑' : '↓'
      row.textContent = `${arrow} ${member.online === false ? '--' : formatByteRate(value ?? null)}`
      rates.append(row)
    }
    addRate('upload', member.uploadRate)
    addRate('download', member.downloadRate)

    badge.append(header, rates)
    return { badge, header }
  }

  const hiddenBadges: HTMLElement[] = []
  const first = createBadge(members[0])
  root.append(first.badge)

  if (members.length > 1) {
    root.dataset.interactive = 'true'
    root.removeAttribute('aria-hidden')

    const toggle = document.createElement('button')
    toggle.type = 'button'
    toggle.className = 'earth-node-cluster-toggle'
    toggle.style.pointerEvents = 'auto'
    first.header.insertBefore(toggle, first.header.lastElementChild)

    members.slice(1).forEach((member) => {
      const { badge } = createBadge(member)
      badge.style.display = 'none'
      badge.setAttribute('aria-hidden', 'true')
      hiddenBadges.push(badge)
      root.append(badge)
    })

    let expanded = false
    const updateExpansion = () => {
      hiddenBadges.forEach((badge) => {
        badge.style.display = expanded ? 'grid' : 'none'
        badge.setAttribute('aria-hidden', String(!expanded))
      })
      toggle.textContent = expanded ? '-' : `+${members.length - 1}`
      toggle.setAttribute('aria-expanded', String(expanded))
      toggle.setAttribute('aria-label', expanded ? '收起同地服务器' : `展开另外 ${members.length - 1} 台同地服务器`)
    }
    toggle.addEventListener('click', (event) => {
      event.stopPropagation()
      expanded = !expanded
      updateExpansion()
    })
    updateExpansion()
  }

  return root
}

function updateNodeVisibility(element: HTMLElement, isVisible: boolean) {
  element.style.opacity = isVisible ? '1' : '0'
  element.style.filter = isVisible ? 'blur(0)' : 'blur(12px)'
  if (element.dataset.interactive === 'true') {
    element.inert = !isVisible
    element.setAttribute('aria-hidden', String(!isVisible))
  }
}

export default function LowPolyEarth({ nodes, links, ariaLabel, className = '' }: LowPolyEarthProps) {
  const { theme } = useTheme()
  const containerRef = useRef<HTMLDivElement>(null)
  const globeRef = useRef<GlobeMethods | undefined>(undefined)
  const longitudeRef = useRef(INITIAL_LONGITUDE)
  const latitudeRef = useRef(20)
  const animatedLinkCacheRef = useRef(new Map<string, AnimatedEarthLink>())
  const [size, setSize] = useState({ width: 760, height: 520 })
  const [landFeatures, setLandFeatures] = useState<object[]>([])
  const [oceanMesh, setOceanMesh] = useState<Object3D | undefined>(undefined)
  const [landCapMaterial, setLandCapMaterial] = useState<Material | undefined>(undefined)
  const [landSideMaterial, setLandSideMaterial] = useState<Material | undefined>(undefined)
  const [cloudLayer, setCloudLayer] = useState<Object3D | undefined>(undefined)
  const [reducedMotion, setReducedMotion] = useState(false)
  const [palette, setPalette] = useState<EarthPalette>(DEFAULT_EARTH_PALETTE)

  useEffect(() => {
    setPalette(readEarthPalette())
  }, [theme])

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const observer = new ResizeObserver(([entry]) => {
      const width = Math.max(280, Math.floor(entry.contentRect.width))
      const height = width < 520 ? 430 : 520
      setSize({ width, height })
    })
    observer.observe(container)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setReducedMotion(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    fetch('/assets/world.geojson', { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error('世界地图数据加载失败')
        return response.json() as Promise<FeatureCollection>
      })
      .then((collection) => {
        const simplified = simplify(closeSouthPolarPolygons(collection), {
          tolerance: LAND_SIMPLIFICATION_TOLERANCE,
          highQuality: true,
          mutate: false,
        })
        setLandFeatures(closeSouthPolarPolygons(simplified).features)
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === 'AbortError') return
        setLandFeatures([])
      })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    let active = true
    let material: Material | undefined
    let oceanGeometry: BufferGeometry | undefined
    let capMaterial: Material | undefined
    let sideMaterial: Material | undefined
    let cloudMaterial: Material | undefined
    let cloudGeometry: BufferGeometry | undefined

    Promise.resolve().then(() => {
      material = new MeshLambertMaterial({
        color: palette.oceanColor,
        emissive: palette.oceanEmissive,
        emissiveIntensity: 0.1,
        flatShading: true,
        vertexColors: true,
      })
      oceanGeometry = new IcosahedronGeometry(100, 3)
      const oceanPositions = oceanGeometry.getAttribute('position')
      const oceanColors: number[] = []
      const facetColor = new Color()
      for (let vertexIndex = 0; vertexIndex < oceanPositions.count; vertexIndex += 3) {
        const facetIndex = vertexIndex / 3
        const noise = Math.sin((facetIndex + 1) * 12.9898) * 43758.5453
        const shade = noise - Math.floor(noise)
        facetColor.setHSL(
          palette.oceanHue + shade * 0.012,
          palette.oceanSaturation,
          palette.oceanLightness + shade * 0.055,
        )
        for (let corner = 0; corner < 3; corner += 1) {
          oceanColors.push(facetColor.r, facetColor.g, facetColor.b)
        }
      }
      oceanGeometry.setAttribute('color', new Float32BufferAttribute(oceanColors, 3))
      const nextOceanMesh = new Mesh(oceanGeometry, material)
      nextOceanMesh.name = 'low-poly-ocean'

      capMaterial = new MeshLambertMaterial({
        color: palette.land,
        emissive: palette.landEmissive,
        emissiveIntensity: 0.08,
        flatShading: true,
      })
      sideMaterial = new MeshLambertMaterial({
        color: palette.landSide,
        emissive: palette.landSideEmissive,
        emissiveIntensity: 0.06,
        flatShading: true,
      })
      cloudMaterial = new MeshToonMaterial({
        color: palette.cloud,
        transparent: true,
        opacity: 0.82,
        depthWrite: false,
      })
      cloudGeometry = new DodecahedronGeometry(6.4, 1)
      const nextCloudLayer = new Group()
      nextCloudLayer.name = 'independent-cloud-layer'
      nextCloudLayer.rotation.order = 'YXZ'
      const cloudForward = new Vector3(0, 0, 1)
      cloudLocations.forEach((location) => {
        const anchor = new Group()
        anchor.scale.setScalar(CLOUD_VOLUME_SCALE)
        const position = globePosition(location.lat, location.lng, 0.09)
        anchor.position.set(position.x, position.y, position.z)
        anchor.quaternion.setFromUnitVectors(
          cloudForward,
          new Vector3(position.x, position.y, position.z).normalize(),
        )
        nextCloudLayer.add(anchor)
        const blobs = [
          { x: -5, y: -0.4, z: 0, sx: 1.05, sy: 0.64, sz: 0.58 },
          { x: 0, y: 0.5, z: 0.8, sx: 1.25, sy: 0.82, sz: 0.72 },
          { x: 5, y: -0.25, z: 0.1, sx: 1, sy: 0.62, sz: 0.54 },
          { x: -1.4, y: 3.2, z: 1.2, sx: 0.78, sy: 0.86, sz: 0.7 },
          { x: 0.8, y: -2.4, z: 0.3, sx: 1.35, sy: 0.5, sz: 0.56 },
        ]
        blobs.forEach((blob) => {
          const mesh = new Mesh(cloudGeometry, cloudMaterial)
          mesh.position.set(blob.x, blob.y, blob.z)
          mesh.scale.set(blob.sx * location.scale, blob.sy * location.scale, blob.sz * location.scale)
          anchor.add(mesh)
        })
      })

      if (active) {
        setOceanMesh(nextOceanMesh)
        setLandCapMaterial(capMaterial)
        setLandSideMaterial(sideMaterial)
        setCloudLayer(nextCloudLayer)
      } else {
        material.dispose()
        oceanGeometry.dispose()
        capMaterial.dispose()
        sideMaterial.dispose()
        cloudMaterial.dispose()
        cloudGeometry.dispose()
      }
    })

    return () => {
      active = false
      material?.dispose()
      oceanGeometry?.dispose()
      capMaterial?.dispose()
      sideMaterial?.dispose()
      cloudMaterial?.dispose()
      cloudGeometry?.dispose()
    }
  }, [palette])

  const animatedLinks = useMemo<AnimatedEarthLink[]>(() => {
    const cache = animatedLinkCacheRef.current
    const activeIds = new Set<string>()
    const nextLinks = links.map((link) => {
      const motion = buildLinkMotion(link.id)
      const id = `${link.id}:signal`
      activeIds.add(id)
      const cached = cache.get(id)
      if (cached) {
        Object.assign(cached, link, motion, { id })
        return cached
      }
      const animatedLink = { ...link, ...motion, id }
      cache.set(id, animatedLink)
      return animatedLink
    })

    cache.forEach((_link, id) => {
      if (!activeIds.has(id)) cache.delete(id)
    })
    return nextLinks
  }, [links])
  const nodeLabels = useMemo(() => clusterNodeLabels(nodes), [nodes])
  const customObjects = useMemo(
    () => [oceanMesh, cloudLayer].filter((item): item is Object3D => item !== undefined),
    [cloudLayer, oceanMesh],
  )

  const configureGlobe = useCallback(() => {
    const globe = globeRef.current
    if (!globe) return
    const controls = globe.controls()
    controls.autoRotate = false
    controls.enableDamping = true
    controls.dampingFactor = 0.08
    controls.enablePan = false
    controls.enableZoom = true
    controls.minDistance = size.width < 520 ? MOBILE_MIN_CAMERA_DISTANCE : DESKTOP_MIN_CAMERA_DISTANCE
    controls.maxDistance = MAX_CAMERA_DISTANCE
    globe.pointOfView({
      lat: 20,
      lng: longitudeRef.current,
      altitude: size.width < 520 ? 2.72 : 2.05,
    }, 0)
    latitudeRef.current = 20
    globe.resumeAnimation()
  }, [size.width])

  useEffect(() => {
    let frame = 0
    const initializeControls = () => {
      if (!globeRef.current) {
        frame = requestAnimationFrame(initializeControls)
        return
      }
      configureGlobe()
    }
    frame = requestAnimationFrame(initializeControls)
    return () => cancelAnimationFrame(frame)
  }, [configureGlobe, oceanMesh])

  useEffect(() => {
    let frame = 0
    let cancelled = false
    Promise.resolve().then(() => {
      if (cancelled) return
      let previousTime = performance.now()
      const identity = new Quaternion()
      const cameraDelta = new Quaternion()
      const followedDelta = new Quaternion()
      const cappedOffset = new Quaternion()
      const previousOrientation = new Quaternion()
      const currentOrientation = new Quaternion()
      const yaw = new Quaternion()
      const pitch = new Quaternion()
      const worldUp = new Vector3(0, 1, 0)
      const worldRight = new Vector3(1, 0, 0)

      const setCameraOrientation = (target: InstanceType<typeof Quaternion>, lat: number, lng: number) => {
        yaw.setFromAxisAngle(worldUp, lng * (Math.PI / 180))
        pitch.setFromAxisAngle(worldRight, -lat * (Math.PI / 180))
        target.copy(yaw).multiply(pitch)
      }

      const rotateGlobe = (time: number) => {
        const elapsedSeconds = Math.min(time - previousTime, 50) / 1000
        previousTime = time
        const globe = globeRef.current
        if (!globe) {
          frame = requestAnimationFrame(rotateGlobe)
          return
        }

        const pointOfView = globe.pointOfView()
        if (!reducedMotion) {
          const elapsedDegrees = elapsedSeconds * SELF_ROTATION_RADIANS_PER_SECOND * (180 / Math.PI)
          const longitudeInteraction = normalizeLongitudeDelta(pointOfView.lng - longitudeRef.current)
          const latitudeInteraction = pointOfView.lat - latitudeRef.current

          if (cloudLayer && (Math.abs(longitudeInteraction) > 0.00001 || Math.abs(latitudeInteraction) > 0.00001)) {
            setCameraOrientation(previousOrientation, latitudeRef.current, longitudeRef.current)
            setCameraOrientation(currentOrientation, pointOfView.lat, pointOfView.lng)
            cameraDelta.copy(currentOrientation).multiply(previousOrientation.invert())
            followedDelta.copy(identity).slerp(cameraDelta, CLOUD_DRAG_FOLLOW)
            cloudLayer.quaternion.premultiply(followedDelta).normalize()
          }

          if (cloudLayer) {
            cloudLayer.quaternion.slerp(identity, 1 - Math.exp(-CLOUD_CATCH_UP_STRENGTH * elapsedSeconds))
            const offsetAngle = cloudLayer.quaternion.angleTo(identity)
            if (offsetAngle > CLOUD_MAX_OFFSET_RADIANS) {
              cappedOffset.copy(cloudLayer.quaternion)
              cloudLayer.quaternion.copy(identity).slerp(cappedOffset, CLOUD_MAX_OFFSET_RADIANS / offsetAngle)
            }
          }

          longitudeRef.current = ((pointOfView.lng + elapsedDegrees + 180) % 360) - 180
          latitudeRef.current = pointOfView.lat
          globe.pointOfView({ ...pointOfView, lng: longitudeRef.current }, 0)
        } else {
          longitudeRef.current = pointOfView.lng
          latitudeRef.current = pointOfView.lat
          if (cloudLayer) cloudLayer.quaternion.identity()
        }
        frame = requestAnimationFrame(rotateGlobe)
      }
      frame = requestAnimationFrame(rotateGlobe)
    })
    return () => {
      cancelled = true
      cancelAnimationFrame(frame)
    }
  }, [cloudLayer, reducedMotion])

  return (
    <div
      ref={containerRef}
      className={`relative min-h-[430px] overflow-hidden sm:min-h-[520px] ${className}`}
      aria-label={ariaLabel ?? `地球拓扑，共 ${nodes.length} 个节点，${links.length} 条连接`}
    >
      <div className="absolute inset-0 flex items-center justify-center">
        <div style={{ transform: `rotate(${AXIAL_TILT_DEGREES}deg)` }}>
          <Suspense fallback={<div className="size-64 rounded-full border-2 border-primary/40 bg-primary/20 shadow-[inset_-24px_-18px_0_var(--shadow-color)] sm:size-80" aria-label="地球模型载入中" />}>
            <Globe
              ref={globeRef}
              width={size.width}
              height={size.height}
              backgroundColor="rgba(0,0,0,0)"
              showGlobe={false}
              showAtmosphere
              atmosphereColor={palette.atmosphere}
              atmosphereAltitude={0.1}
              polygonsData={landFeatures}
              polygonCapMaterial={landCapMaterial}
              polygonSideMaterial={landSideMaterial}
              polygonStrokeColor={() => null}
              polygonAltitude={0.018}
              polygonCapCurvatureResolution={LAND_CURVATURE_RESOLUTION}
              polygonsTransitionDuration={0}
              customLayerData={customObjects}
              customThreeObject={(item) => item as Object3D}
              pointsData={nodes}
              pointLat={(item) => (item as EarthNode).lat}
              pointLng={(item) => (item as EarthNode).lng}
              pointColor={(item) => {
                const node = item as EarthNode
                if (node.status === 'warning') return palette.nodeWarning
                return node.status === 'online' ? palette.nodeOnline : palette.nodeOffline
              }}
              pointAltitude={0.025}
              pointRadius={0.34}
              pointResolution={10}
              pointsTransitionDuration={500}
              htmlElementsData={nodeLabels}
              htmlLat={(item) => (item as EarthNodeLabel).lat}
              htmlLng={(item) => (item as EarthNodeLabel).lng}
              htmlAltitude={0.065}
              htmlElement={(item: object) => createNodeElement(item, palette)}
              htmlElementVisibilityModifier={updateNodeVisibility}
              htmlTransitionDuration={500}
              arcsData={animatedLinks}
              arcStartLat={(item) => (item as EarthLink).startLat}
              arcStartLng={(item) => (item as EarthLink).startLng}
              arcEndLat={(item) => (item as EarthLink).endLat}
              arcEndLng={(item) => (item as EarthLink).endLng}
              arcColor={(item: object) => linkColor(item as AnimatedEarthLink, palette)}
              arcAltitudeAutoScale={0.42}
              arcStroke={2.35}
              arcCircularResolution={4}
              arcDashLength={LINK_DASH_LENGTH}
              arcDashGap={LINK_DASH_GAP}
              arcDashInitialGap={(item: object) => ((item as AnimatedEarthLink).dashPhase)}
              arcDashAnimateTime={(item: object) => {
                const link = item as AnimatedEarthLink
                return reducedMotion ? 0 : link.dashDuration
              }}
              arcsTransitionDuration={0}
              ringsData={reducedMotion ? [] : nodes.filter((node) => node.status === 'online')}
              ringLat={(item) => (item as EarthNode).lat}
              ringLng={(item) => (item as EarthNode).lng}
              ringColor={(_item: object) => [palette.ring, 'rgba(0,0,0,0)']}
              ringMaxRadius={2.2}
              ringPropagationSpeed={1.2}
              ringRepeatPeriod={1500}
              onGlobeReady={configureGlobe}
            />
          </Suspense>
        </div>
      </div>

      <ul className="sr-only">
        {nodes.map((node) => (
          <li key={node.id}>{node.label}{node.description ? `，${node.description}` : ''}</li>
        ))}
      </ul>
    </div>
  )
}
