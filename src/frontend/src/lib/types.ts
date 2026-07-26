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

// NAT 可用端口段的一项（§21）：外部段 [pub_start,pub_end] 映射到内部监听段；
// listen_* 省略 = 1:1 映射。
export interface PortRange {
  pub_start: number
  pub_end: number
  listen_start?: number
  listen_end?: number
}

export type MachineType = 'direct' | 'nat'

export interface Server {
  id: number
  alias: string
  online: boolean
  last_seen_at: string | null
  xray_version: string | null
  agent_version: string | null
  upgrade_needed: boolean
  address: string
  learned_addr: string
  nic_addresses: string[]
  config_drift: boolean
  machine_type: MachineType
  allowed_ports: PortRange[]
  metrics: ServerMetrics | null
  created_at: string
}

// 命令日志条目（GET /api/servers/{id}/commands，§4）。
export interface CommandLog {
  id: number
  type: string
  status: 'queued' | 'sent' | 'acked' | 'failed'
  error?: string
  attempts: number
  created_at: string
  updated_at: string
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

export type ChainStatus = 'pending' | 'applying' | 'active' | 'degraded' | 'failed'
export type ChainHopRole = 'entry' | 'middle' | 'exit'

export interface ChainHop {
  id: number
  seq: number
  server_id: number
  server_alias: string
  role: ChainHopRole
  node_id: number
  status: NodeStatus
  error: string
  forward_port: number // entry 跳 = 订阅端口（监听侧）
  portal_port: number
  portal_public_key?: string
  tunnel_uuid?: string
}

export interface Chain {
  id: number
  status: ChainStatus
  error: string // 失败时定位到跳
  created_at: string
  hops: ChainHop[] // 按 seq 升序：首位入口，末位出口
}

// 链路构图提交（§21）：依次入口 / 中间跳（0-2）/ 出口，出口携带业务节点协议表单，
// 入口端口可空 = 自动。node.server_id 由后端按 exit.server_id 覆盖。
export interface CreateChainRequest {
  entry: { server_id: number }
  middle: { server_id: number }[]
  exit: { server_id: number }
  entry_port?: number
  node: Omit<CreateNodeRequest, 'server_id'>
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
  expires_at: string | null
  expired: boolean
  disabled: boolean
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
  panel_version: string
  password_override: boolean
  alert_webhook_url: string
  alert_telegram_bot_token_set: boolean
  alert_telegram_chat_id: string
  resource_source: '' | 'panel'
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
  alert_webhook_url: string
  alert_telegram_bot_token?: string
  alert_telegram_chat_id: string
  resource_source: '' | 'github' | 'panel'
}

export interface AlertChannelResult {
  configured: boolean
  ok: boolean
  error?: string
}

export interface AlertTestResult {
  webhook: AlertChannelResult
  telegram: AlertChannelResult
}

// 面板自更新（GitHub release 钉版）：版本检测结果与更新进度快照。
export interface PanelVersionInfo {
  current: string
  latest: string
  update_available: boolean
  can_update: boolean
  message?: string
}

export type PanelUpdateStage =
  | ''
  | 'check'
  | 'download'
  | 'verify'
  | 'extract'
  | 'apply'
  | 'restart'
  | 'done'
  | 'failed'

export interface PanelUpdateStatus {
  running: boolean
  stage: PanelUpdateStage
  percent: number
  message: string
  error?: string
  current_version: string
  target_version: string
}

// 事件日志（§log 日志审查页面）：统一时间线，汇聚 command/node/agent/admin 四类。
export type EventCategory = 'command' | 'node' | 'agent' | 'admin'

export interface EventLogEntry {
  id: number
  ts: string
  category: EventCategory
  action: string
  server_id?: number
  server?: string // alias，后端按 server_id 关联填充
  node_id?: number
  detail: string // JSON 串
  operator?: string
  ip?: string
}

export interface EventLogPage {
  items: EventLogEntry[]
  total: number
}
