import { startTransition, useEffect, useMemo, useState } from 'react'

import LowPolyEarth, { type EarthLink, type EarthNode } from '@/components/LowPolyEarth'
import { resolveGeographyLocations, type GeographyLocationResult } from '@/lib/geography'
import { isServerOnline } from '@/lib/server-state'
import type { Chain, Server } from '@/lib/types'

interface TopologyPoint {
  id: number
  alias: string
  location: string
  locationKey: string
  lat: number
  lng: number
  online: boolean
  positioned: boolean
  countryCode: string
  uploadRate: number | null
  downloadRate: number | null
}

interface GlobeTopologyProps {
  servers: Server[]
  chains: Chain[]
  activeServerId?: number
  demoMode?: boolean
  motionEnabled?: boolean
  onServerSelect?: (serverId: number) => void
}

const coordinateCache = new Map<string, { lat: number; lng: number } | null>()

function normalizePlace(value: string) {
  return value
    .trim()
    .toLocaleLowerCase()
    .replace(/[\s_-]+/g, '')
}

function positionServers(
  servers: Server[],
  resolvedCoordinates: ReadonlyMap<string, GeographyLocationResult>,
): TopologyPoint[] {
  const duplicateLocations = new Map<string, number>()

  return servers.map((server) => {
    const countryCode = server.country_code.trim().toUpperCase()
    const coordinateKey = `${countryCode}:${normalizePlace(server.location)}`
    const resolved = resolvedCoordinates.get(coordinateKey)
    const cached = coordinateCache.get(coordinateKey)
    const lat = resolved?.lat ?? cached?.lat ?? null
    const lng = resolved?.lng ?? cached?.lng ?? null

    const positioned = lat !== null && lng !== null
    const baseLat = lat ?? -68 + (server.id % 3) * 4
    const baseLng = lng ?? ((server.id * 137.508) % 340) - 170
    const locationKey = positioned
      ? `${baseLat.toFixed(3)}:${baseLng.toFixed(3)}`
      : `fallback:${server.id}`
    const duplicateIndex = duplicateLocations.get(locationKey) ?? 0
    duplicateLocations.set(locationKey, duplicateIndex + 1)
    const angle = duplicateIndex * 2.39996
    const radius = duplicateIndex === 0 ? 0 : 0.55 * Math.ceil(duplicateIndex / 5)

    return {
      id: server.id,
      alias: server.alias,
      location: server.location || countryCode || '位置待补全',
      locationKey,
      lat: Math.max(-82, Math.min(82, baseLat + Math.sin(angle) * radius)),
      lng: baseLng + Math.cos(angle) * radius,
      online: isServerOnline(server),
      positioned,
      countryCode,
      uploadRate: server.metrics?.network_tx_bps ?? null,
      downloadRate: server.metrics?.network_rx_bps ?? null,
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
      const status: EarthLink['status'] = (() => {
        switch (chain.status) {
          case 'active_unconfirmed':
            return 'active'
          case 'waiting_for_agent':
          case 'cleanup_pending':
            return 'applying'
          case 'active_failed':
          case 'invalid':
          case 'deleted':
            return 'failed'
          default:
            return chain.status
        }
      })()
      return [
        {
          id: `${chain.id}:${index}`,
          startLat: start.lat,
          startLng: start.lng,
          endLat: end.lat,
          endLng: end.lng,
          status,
        },
      ]
    })
  })
}

function buildDemoLinks(points: TopologyPoint[]): EarthLink[] {
  const positioned = points.filter((point) => point.positioned)
  const anchor = positioned[0]
  if (!anchor) return []
  return positioned.slice(1).map((point, index) => ({
    id: `demo:${anchor.id}:${point.id}`,
    startLat: anchor.lat,
    startLng: anchor.lng,
    endLat: point.lat,
    endLng: point.lng,
    status: index === 3 ? 'degraded' : 'active',
  }))
}

export default function GlobeTopology({
  servers,
  chains,
  activeServerId,
  demoMode = false,
  motionEnabled = true,
  onServerSelect,
}: GlobeTopologyProps) {
  const [points, setPoints] = useState<TopologyPoint[]>([])

  useEffect(() => {
    if (servers.length === 0) {
      setPoints([])
      return
    }

    const cachedCoordinates = new Map<string, GeographyLocationResult>()
    const missingRequests = new Map<
      string,
      { key: string; countryCode: string; location: string }
    >()
    servers.forEach((server) => {
      const countryCode = server.country_code.trim().toUpperCase()
      const key = `${countryCode}:${normalizePlace(server.location)}`
      const cached = coordinateCache.get(key)
      if (coordinateCache.has(key)) {
        cachedCoordinates.set(key, { key, lat: cached?.lat ?? null, lng: cached?.lng ?? null })
      } else if (countryCode) {
        missingRequests.set(key, { key, countryCode, location: server.location })
      }
    })

    setPoints(positionServers(servers, cachedCoordinates))
    if (missingRequests.size === 0) return

    const controller = new AbortController()
    resolveGeographyLocations([...missingRequests.values()], controller.signal)
      .then((results) => {
        const resolvedCoordinates = new Map(cachedCoordinates)
        results.forEach((result) => {
          coordinateCache.set(
            result.key,
            result.lat !== null && result.lng !== null
              ? { lat: result.lat, lng: result.lng }
              : null,
          )
          resolvedCoordinates.set(result.key, result)
        })
        startTransition(() => setPoints(positionServers(servers, resolvedCoordinates)))
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === 'AbortError') return
      })
    return () => controller.abort()
  }, [servers])

  const nodes = useMemo<EarthNode[]>(
    () =>
      points
        .filter((point) => point.positioned)
        .map((point) => ({
          id: point.id,
          label: point.alias,
          description: `${point.location}，${point.online ? '在线' : '离线'}`,
          clusterKey: point.locationKey,
          lat: point.lat,
          lng: point.lng,
          status: (point.online ? 'online' : 'offline') as EarthNode['status'],
          online: point.online,
          countryCode: point.countryCode,
          uploadRate: point.uploadRate,
          downloadRate: point.downloadRate,
          selected: point.id === activeServerId,
        })),
    [activeServerId, points],
  )
  const links = useMemo(() => {
    const actualLinks = buildLinks(chains, points)
    return demoMode && actualLinks.length === 0 ? buildDemoLinks(points) : actualLinks
  }, [chains, demoMode, points])
  const unresolvedCount = points.filter((point) => !point.positioned).length

  return (
    <div className="dashboard-globe">
      <LowPolyEarth
        nodes={nodes}
        links={links}
        className="dashboard-globe-canvas"
        motionEnabled={motionEnabled}
        selectedNodeId={activeServerId}
        onNodeClick={(node) => onServerSelect?.(Number(node.id))}
        ariaLabel={`链路拓扑，共 ${nodes.length} 个服务器节点，${links.length} 段链路`}
      />

      <div className="globe-telemetry" aria-hidden="true">
        <span>
          <b>{String(nodes.length).padStart(2, '0')}</b> NODES
        </span>
        <span>
          <b>{String(links.length).padStart(2, '0')}</b> ROUTES
        </span>
        {unresolvedCount > 0 && (
          <span>
            <b>{String(unresolvedCount).padStart(2, '0')}</b> UNLOCATED
          </span>
        )}
      </div>

      <div className="globe-legend" aria-hidden="true">
        <span>
          <i className="is-online" />
          在线
        </span>
        <span>
          <i />
          离线 / 重连
        </span>
      </div>
    </div>
  )
}
