import { describe, expect, it } from 'vitest'
import { DEMO_SERVERS } from '@/lib/dashboard-demo'
import type { Server } from '@/lib/types'
import { serverReadout } from './server-readout'

const server = DEMO_SERVERS[0]
const withMetrics = (metrics: Partial<NonNullable<Server['metrics']>>) => ({
  ...server,
  metrics: { ...server.metrics!, ...metrics },
})

describe('topology server readouts', () => {
  it('uses each server’s actual location, address and telemetry', () => {
    const view = serverReadout(server)
    expect(view.location).toBe(server.location)
    expect(view.address).toBe(server.address)
    expect(view.resources[0].percent).toBe(server.metrics!.cpu_percent)
    expect(serverReadout(DEMO_SERVERS[1]).address).not.toBe(view.address)
  })
  it('retains domain names and IPv6 addresses without URL parsing', () => {
    const view = serverReadout({
      ...server,
      address: 'relay.example.com',
      addresses: ['relay.example.com', '2001:db8::1', '2001:db8::1'],
    })
    expect(view.addresses).toEqual(['relay.example.com', '2001:db8::1'])
  })
  it('falls back to the learned address, then the hop address', () => {
    expect(serverReadout({ ...server, address: '', learned_addr: '2001:db8::2' }).address).toBe(
      '2001:db8::2',
    )
    expect(serverReadout(undefined, 'hop.example.com').address).toBe('hop.example.com')
    expect(serverReadout().location).toBe('位置未设置')
  })
  it('does not turn missing telemetry into healthy zero readings', () => {
    const view = serverReadout({ ...server, metrics: null })
    expect(view.resources.every((item) => item.percent === null)).toBe(true)
    expect([view.tx, view.rx, view.latency, view.uptime]).toEqual(['--', '--', '--', '--'])
  })
  it('uses the first normalized address when only an address list is available', () => {
    const view = serverReadout({
      ...server,
      address: '',
      learned_addr: '',
      addresses: [' ', '  relay.example.com  ', '2001:db8::8'],
    })
    expect(view.address).toBe('relay.example.com')
    expect(view.addresses).toEqual(['relay.example.com', '2001:db8::8'])
  })
  it('preserves legitimate zero CPU, network and latency samples', () => {
    const view = serverReadout(
      withMetrics({ cpu_percent: 0, network_tx_bps: 0, latency_ms: 0, uptime_seconds: 0 }),
    )
    expect(view.resources[0].percent).toBe(0)
    expect(view.tx).toBe('0 B/s')
    expect(view.latency).toBe('0 ms')
    expect(view.uptime).toBe('0m')
  })
  it('handles null CPU, unknown capacities and invalid numbers safely', () => {
    const view = serverReadout(
      withMetrics({ cpu_percent: null, mem_total: 0, disk_used: NaN, network_rx_bps: Infinity }),
    )
    expect(view.resources.map((item) => item.percent)).toEqual([null, null, null])
    expect(view.rx).toBe('--')
  })
  it('clamps meter ranges without losing capacity details', () => {
    const view = serverReadout(withMetrics({ mem_used: 2048, mem_total: 1024, cpu_percent: 110 }))
    expect(view.resources[0].percent).toBe(100)
    expect(view.resources[1]).toMatchObject({ percent: 100, detail: '2.0 KB / 1.0 KB' })
  })
  it('retains historical samples for explicit offline labeling', () => {
    expect(serverReadout({ ...server, connection_state: 'offline' }).resources).toEqual(
      serverReadout(server).resources,
    )
  })
})
