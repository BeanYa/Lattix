import type { DashboardStats, Server, ServerMetrics } from './types'

const GIB = 1024 ** 3

interface DemoServerInput {
  id: number
  alias: string
  countryCode: string
  location: string
  address: string
  state?: Server['connection_state']
  cpu: number
  memory: [used: number, total: number]
  disk: [used: number, total: number]
  network: [txBps: number, rxBps: number]
  uptime: number
  latency: number
  tags: string[]
}

function createMetrics(input: DemoServerInput): ServerMetrics {
  return {
    load1: input.cpu / 18,
    load5: input.cpu / 20,
    load15: input.cpu / 22,
    cpu_percent: input.cpu,
    mem_used: input.memory[0] * GIB,
    mem_total: input.memory[1] * GIB,
    disk_used: input.disk[0] * GIB,
    disk_total: input.disk[1] * GIB,
    network_interface: 'eth0',
    network_tx_bytes: input.network[0] * 86400 * 9,
    network_rx_bytes: input.network[1] * 86400 * 9,
    network_tx_bps: input.network[0],
    network_rx_bps: input.network[1],
    uptime_seconds: input.uptime,
    latency_ms: input.latency,
    updated_at: new Date().toISOString(),
  }
}

function createDemoServer(input: DemoServerInput): Server {
  const now = new Date().toISOString()
  return {
    id: input.id,
    alias: input.alias,
    connection_state: input.state ?? 'online',
    session_id: `demo-${input.id}`,
    session_kind: 'initial',
    last_connected_at: now,
    last_disconnected_at: null,
    last_reconnected_at: null,
    reconnect_count: 0,
    last_disconnect_reason: '',
    last_seen_at: now,
    xray_version: '25.6.8',
    agent_version: '0.9.4',
    address: input.address,
    learned_addr: input.address,
    nic_addresses: [input.address],
    addresses: [input.address],
    config_drift: false,
    machine_type: 'direct',
    allowed_ports: [{ pub_start: 10000, pub_end: 29999 }],
    tags: input.tags,
    country_code: input.countryCode,
    location: input.location,
    agent_settings_status: 'synced',
    agent_settings_revision: 12,
    agent_settings_desired_revision: 12,
    agent_settings_error: '',
    agent_settings_reported_at: now,
    custom_settings: null,
    effective_xray_version: '25.6.8',
    metrics: createMetrics(input),
    billing: {
      enabled: false,
      provider: null,
      amount_minor: 0,
      currency: 'USD',
      service_started_on: '2026-01-01',
      interval_count: 1,
      interval_unit: 'month',
      next_renewal_on: '2026-09-01',
      status: 'disabled',
      assumed_valid_through: '',
      status_changed_at: now,
    },
    traffic_plan: {
      quota_bytes: 2 * 1024 ** 4,
      accounting_mode: 'bidirectional',
      reset_anchor_on: '2026-01-01',
      reset_count: 1,
      reset_unit: 'month',
      period_started_on: '2026-08-01',
      next_reset_on: '2026-09-01',
      tx_bytes: input.network[0] * 86400 * 9,
      rx_bytes: input.network[1] * 86400 * 9,
      used_bytes: (input.network[0] + input.network[1]) * 86400 * 9,
      complete: true,
    },
    created_at: '2026-01-01T00:00:00.000Z',
  }
}

export const DEMO_DASHBOARD_STATS: DashboardStats = {
  servers: 6,
  servers_online: 5,
  links: 8,
  links_active: 7,
  links_degraded: 1,
  users: 24,
}

export const DEMO_SERVERS: Server[] = [
  createDemoServer({ id: 9001, alias: 'Tokyo Edge 01', countryCode: 'JP', location: '东京 · 品川', address: '203.0.113.18', cpu: 34, memory: [3.8, 8], disk: [41, 120], network: [8_420_000, 19_760_000], uptime: 1_238_400, latency: 28, tags: ['edge', 'premium'] }),
  createDemoServer({ id: 9002, alias: 'Singapore Relay', countryCode: 'SG', location: '新加坡 · 裕廊', address: '198.51.100.42', cpu: 58, memory: [6.1, 12], disk: [77, 160], network: [12_840_000, 26_310_000], uptime: 846_720, latency: 43, tags: ['relay', 'apac'] }),
  createDemoServer({ id: 9003, alias: 'Frankfurt Core', countryCode: 'DE', location: '法兰克福 · HE', address: '192.0.2.76', cpu: 21, memory: [5.4, 16], disk: [96, 320], network: [18_250_000, 31_600_000], uptime: 2_764_800, latency: 156, tags: ['core', 'eu'] }),
  createDemoServer({ id: 9004, alias: 'Los Angeles Exit', countryCode: 'US', location: '洛杉矶 · LAX', address: '203.0.113.104', cpu: 72, memory: [10.7, 16], disk: [138, 240], network: [21_470_000, 38_920_000], uptime: 518_400, latency: 132, tags: ['exit', 'media'] }),
  createDemoServer({ id: 9005, alias: 'Hong Kong Gateway', countryCode: 'HK', location: '香港 · 将军澳', address: '198.51.100.133', state: 'reconnecting', cpu: 46, memory: [4.9, 8], disk: [54, 120], network: [5_610_000, 11_320_000], uptime: 309_600, latency: 18, tags: ['gateway', 'backup'] }),
  createDemoServer({ id: 9006, alias: 'Sydney Edge', countryCode: 'AU', location: '悉尼 · Mascot', address: '192.0.2.188', cpu: 43, memory: [3.1, 8], disk: [68, 160], network: [7_930_000, 15_480_000], uptime: 1_641_600, latency: 184, tags: ['edge', 'oceania'] }),
]
