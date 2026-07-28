import { useEffect, useMemo, useState } from 'react'

import LowPolyEarth, { type EarthLink, type EarthNode } from '@/components/LowPolyEarth'
import type { Chain, Server } from '@/lib/types'

interface TopologyPoint {
  id: number
  alias: string
  location: string
  lat: number
  lng: number
  online: boolean
  positioned: boolean
}

interface GlobeTopologyProps {
  servers: Server[]
  chains: Chain[]
}

let geographyModule: Promise<typeof import('country-state-city')> | null = null
const coordinateCache = new Map<string, { lat: number; lng: number } | null>()

function loadGeography() {
  geographyModule ??= import('country-state-city')
  return geographyModule
}

function normalizePlace(value: string) {
  return value.trim().toLocaleLowerCase().replace(/[\s_-]+/g, '')
}

function finiteCoordinate(value: string | null | undefined) {
  if (value == null || value.trim() === '') return null
  const coordinate = Number(value)
  return Number.isFinite(coordinate) ? coordinate : null
}

async function locateServers(servers: Server[]): Promise<TopologyPoint[]> {
  const { City, Country } = await loadGeography()
  const citiesByCountry = new Map<string, ReturnType<typeof City.getCitiesOfCountry>>()
  const duplicateLocations = new Map<string, number>()

  return servers.map((server) => {
    const countryCode = server.country_code.trim().toUpperCase()
    const place = normalizePlace(server.location)
    let lat: number | null = null
    let lng: number | null = null

    if (countryCode) {
      const coordinateKey = `${countryCode}:${place}`
      if (coordinateCache.has(coordinateKey)) {
        const cached = coordinateCache.get(coordinateKey)
        lat = cached?.lat ?? null
        lng = cached?.lng ?? null
      } else {
        let cities = citiesByCountry.get(countryCode)
        if (!cities) {
          cities = City.getCitiesOfCountry(countryCode)
          citiesByCountry.set(countryCode, cities)
        }
        const city = cities?.find((candidate) => {
          const candidateName = normalizePlace(candidate.name)
          return candidateName === place || (place.length > 2 && candidateName.includes(place))
        })
        const country = Country.getCountryByCode(countryCode)
        lat = finiteCoordinate(city?.latitude) ?? finiteCoordinate(country?.latitude)
        lng = finiteCoordinate(city?.longitude) ?? finiteCoordinate(country?.longitude)
        coordinateCache.set(coordinateKey, lat !== null && lng !== null ? { lat, lng } : null)
      }
    }

    const positioned = lat !== null && lng !== null
    const baseLat = lat ?? -68 + (server.id % 3) * 4
    const baseLng = lng ?? ((server.id * 137.508) % 340) - 170
    const locationKey = positioned ? `${baseLat.toFixed(3)}:${baseLng.toFixed(3)}` : `fallback:${server.id}`
    const duplicateIndex = duplicateLocations.get(locationKey) ?? 0
    duplicateLocations.set(locationKey, duplicateIndex + 1)
    const angle = duplicateIndex * 2.39996
    const radius = duplicateIndex === 0 ? 0 : 0.55 * Math.ceil(duplicateIndex / 5)

    return {
      id: server.id,
      alias: server.alias,
      location: server.location || countryCode || '位置待补全',
      lat: Math.max(-82, Math.min(82, baseLat + Math.sin(angle) * radius)),
      lng: baseLng + Math.cos(angle) * radius,
      online: server.online,
      positioned,
    }
  })
}

function buildLinks(chains: Chain[], points: TopologyPoint[]): EarthLink[] {
  const pointsByServer = new Map(points.map((point) => [point.id, point]))

  return chains.flatMap((chain) => {
    const hops = chain.hops.toSorted((left, right) => left.seq - right.seq)
    return hops.slice(1).flatMap((hop, index) => {
      const start = pointsByServer.get(hops[index].server_id)
      const end = pointsByServer.get(hop.server_id)
      if (!start || !end) return []
      return [{
        id: `${chain.id}:${index}`,
        startLat: start.lat,
        startLng: start.lng,
        endLat: end.lat,
        endLng: end.lng,
        status: chain.status,
      }]
    })
  })
}

export default function GlobeTopology({ servers, chains }: GlobeTopologyProps) {
  const [points, setPoints] = useState<TopologyPoint[]>([])

  useEffect(() => {
    if (servers.length === 0) {
      setPoints([])
      return
    }

    let active = true
    locateServers(servers)
      .then((nextPoints) => {
        if (active) setPoints(nextPoints)
      })
      .catch(() => {
        if (!active) return
        setPoints(servers.map((server) => ({
          id: server.id,
          alias: server.alias,
          location: server.location || server.country_code || '位置待补全',
          lat: -68 + (server.id % 3) * 4,
          lng: ((server.id * 137.508) % 340) - 170,
          online: server.online,
          positioned: false,
        })))
      })
    return () => {
      active = false
    }
  }, [servers])

  const nodes = useMemo<EarthNode[]>(
    () => points.map((point) => ({
      id: point.id,
      label: point.alias,
      description: `${point.location}，${point.online ? '在线' : '离线'}`,
      lat: point.lat,
      lng: point.lng,
      status: point.positioned ? (point.online ? 'online' : 'offline') : 'warning',
    })),
    [points],
  )
  const links = useMemo(() => buildLinks(chains, points), [chains, points])
  const unresolvedCount = points.filter((point) => !point.positioned).length

  return (
    <div className="relative min-h-[430px] overflow-hidden bg-transparent sm:min-h-[520px]">
      <LowPolyEarth
        nodes={nodes}
        links={links}
        className="absolute inset-0"
        ariaLabel={`链路拓扑，共 ${nodes.length} 个服务器节点，${links.length} 段链路`}
      />

      <div className="pointer-events-none absolute left-4 top-4 grid gap-2 text-xs font-semibold sm:left-5 sm:top-5">
        <span className="w-fit rounded-lg border bg-card/90 px-3 py-2 shadow-[0_3px_0_rgb(57_53_72/0.16)]">
          节点 {nodes.length} · 链路 {links.length}
        </span>
        {unresolvedCount > 0 && (
          <span className="w-fit rounded-lg border bg-[var(--pastel-yellow)] px-3 py-2">
            {unresolvedCount} 个节点待补充位置
          </span>
        )}
      </div>

      <div className="pointer-events-none absolute bottom-4 right-4 flex flex-wrap justify-end gap-2 text-[11px] font-semibold sm:bottom-5 sm:right-5">
        <span className="inline-flex items-center gap-2 rounded-lg border bg-card/90 px-2.5 py-1.5"><i className="size-2 rounded-full bg-[#7ce5b2]" />在线</span>
        <span className="inline-flex items-center gap-2 rounded-lg border bg-card/90 px-2.5 py-1.5"><i className="size-2 rounded-full bg-[#ff8d87]" />离线</span>
      </div>
    </div>
  )
}
