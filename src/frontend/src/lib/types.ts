export interface DashboardStats {
  servers: number
  servers_online: number
  nodes: number
  nodes_active: number
  users: number
}

export interface ServerMetrics {
  load1: number
  cpu_percent: number
  mem_total: number
  mem_used: number
}

export interface Server {
  id: number
  alias: string
  online: boolean
  last_seen_at: string | null
  xray_version: string | null
  agent_version: string | null
  upgrade_needed: boolean
  address: string
  config_drift: boolean
  metrics: ServerMetrics | null
  created_at: string
}

export interface CreateServerResponse {
  server: Server
  bootstrap_token: string
  install_command: string
}

export type NodeStatus = 'pending' | 'applying' | 'active' | 'failed'

export interface RealizedConfig {
  port: number
  public_key?: string
  short_id?: string
  server_name?: string
  flow?: string
  fingerprint?: string
  network?: string
  service_name?: string
  path?: string
  mode?: string
  host?: string
  method?: string
  encryption?: string
}

export interface Traffic {
  up: number
  down: number
}

export interface XrayNode {
  id: number
  server_id: number
  server_alias: string
  protocol: string
  port: number | null
  status: NodeStatus
  error: string | null
  traffic: Traffic | null
  config_template: string
  realized_config: RealizedConfig | null
  created_at: string
}

export interface CreateNodeRequest {
  server_id: number
  protocol?: string
  port?: number
  short_id?: string
  dest?: string
  server_names?: string[]
  fingerprint?: string
  network?: string
  service_name?: string
  path?: string
  mode?: string
  host?: string
  flow?: string
  method?: string
  encryption?: string
  target_address?: string
  target_port?: number
}

export interface SubUser {
  id: number
  name: string
  uuid: string
  sub_token: string
  sub_url: string
  sub_links_url: string
  node_ids: number[]
  traffic: Traffic | null
  created_at: string
}

export interface CertInfo {
  common_name: string
  dns_names: string[]
  not_after: string
  expired: boolean
}

export type TLSMode = '' | 'off' | 'cert' | 'acme' | 'path'

export interface PanelSettings {
  timezone: string
  public_url: string
  tls_mode: TLSMode
  tls_cert: CertInfo | null
  tls_key_set: boolean
  tls_domain: string
  tls_dir: string
  acme_domain: string
  acme_email: string
  running_tls_mode: 'off' | 'cert' | 'acme' | 'path'
  restart_required: boolean
  admin_user: string
  password_override: boolean
}

export interface UpdateSettingsRequest {
  timezone: string
  public_url: string
  tls_mode: TLSMode
  tls_cert_pem?: string
  tls_key_pem?: string
  tls_domain?: string
  acme_domain: string
  acme_email: string
}
