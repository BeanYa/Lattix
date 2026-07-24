export interface DashboardStats {
  servers: number
  servers_online: number
  nodes: number
  nodes_active: number
  users: number
}

export interface Server {
  id: number
  alias: string
  online: boolean
  last_seen_at: string | null
  xray_version: string | null
  address: string
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
  public_key: string
  short_id: string
  server_name: string
  flow: string
  fingerprint: string
}

export interface XrayNode {
  id: number
  server_id: number
  server_alias: string
  protocol: string
  port: number | null
  status: NodeStatus
  error: string | null
  config_template: string
  realized_config: RealizedConfig | null
  created_at: string
}

export interface CreateNodeRequest {
  server_id: number
  port?: number
  short_id?: string
  dest?: string
  server_names?: string[]
}

export interface SubUser {
  id: number
  name: string
  uuid: string
  sub_token: string
  sub_url: string
  created_at: string
}
