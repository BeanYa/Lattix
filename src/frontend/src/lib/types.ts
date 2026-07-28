export interface DashboardStats {
  servers: number
  servers_online: number
  links: number
  links_active: number
  links_degraded: number
  users: number
}

export interface ServerMetrics {
  load1: number
  load5: number
  load15: number
  cpu_percent: number | null
  mem_total: number
  mem_used: number
  disk_total: number
  disk_used: number
  network_interface: string
  network_tx_bytes: number
  network_rx_bytes: number
  network_tx_bps: number | null
  network_rx_bps: number | null
  uptime_seconds: number
  latency_ms: number | null
  updated_at: string
}

export interface ServerMetricSeries {
  server_id: number
  samples: ServerMetrics[]
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
  address: string
  learned_addr: string
  nic_addresses: string[]
  config_drift: boolean
  machine_type: MachineType
  allowed_ports: PortRange[]
  tags: string[]
  country_code: string
  location: string
  agent_settings_status: 'synced' | 'pending' | 'failed'
  agent_settings_revision: number
  agent_settings_desired_revision: number
  agent_settings_error: string
  agent_settings_reported_at: string | null
  metrics: ServerMetrics | null
  created_at: string
}

// 命令日志条目（GET /api/server/list-commands，§4）。
export interface CommandLog {
  id: number
  request_id: string
  trace_id: string
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
  name: string
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
  name: string
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
  name: string
  status: ChainStatus
  error: string // 失败时定位到跳
  created_at: string
  hops: ChainHop[] // 按 seq 升序：首位入口，末位出口
}

// 链路构图提交（§21）：依次入口 / 中间跳（0-2）/ 出口，出口携带业务节点协议表单，
// 入口端口可空 = 自动。node.server_id 由后端按 exit.server_id 覆盖。
export interface CreateChainRequest {
  name: string
  entry: { server_id: number }
  middle: { server_id: number }[]
  exit: { server_id: number }
  entry_port?: number
  node: Omit<CreateNodeRequest, 'server_id' | 'name'>
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

export interface AgentSettings {
  revision: number
  reconnect: {
    mode: 'infinite' | 'limited'
    max_retries: number
  }
  telemetry: {
    interval_seconds: number
  }
  drift_detection: {
    interval_seconds: number
  }
}

export type InspectionUnit = 'minute' | 'hour' | 'day' | 'month' | 'year'

export interface InspectionSchedule {
  every: number
  unit: InspectionUnit
  at?: string
}

export interface ReleaseInspectionSettings {
  agent: InspectionSchedule
  xray: InspectionSchedule
}

export interface ReleaseVersions {
  kind: 'agent' | 'xray'
  versions: string[]
  fetched_at: string
  stale: boolean
  message?: string
}

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
  operation_log_limit: number
  request_log_max_mb: number
  log_dir: string
  request_log_usage_bytes: number
  request_log_dropped: number
  backup_includes_logs: boolean
  agent: AgentSettings
  release_inspection: ReleaseInspectionSettings
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
  operation_log_limit: number
  request_log_max_mb: number
  agent: AgentSettings
  release_inspection: ReleaseInspectionSettings
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

export type LogSeverity = 'info' | 'warning' | 'error'
export type OperationCategory =
  | 'server'
  | 'chain'
  | 'user'
  | 'settings'
  | 'panel'
  | 'agent'
  | 'command'
  | 'auth'
  | 'log'

export interface OperationLogEntry {
  id: number
  event_id: string
  timestamp: string
  severity: LogSeverity
  category: OperationCategory
  action: string
  server_id?: number
  server?: string
  node_id?: number
  detail: string
  operator?: string
  ip?: string
  request_id?: string
  trace_id?: string
}

export interface OperationLogPage {
  items: OperationLogEntry[]
  total: number
}

export interface RequestLogEntry {
  timestamp: string
  request_id: string
  trace_id: string
  severity: LogSeverity
  transport: 'http' | 'websocket'
  method?: string
  path?: string
  route?: string
  rpc_type?: string
  attributes?: Record<string, string>
  http_status?: number
  rpc_code?: string
  duration_ms: number
  response_bytes: number
  operator?: string
  ip?: string
  user_agent?: string
  error_summary?: string
  idempotency_replayed?: boolean
}

export interface RequestLogStatus {
  usage_bytes: number
  max_bytes: number
  dropped: number
  directory: string
  segment_count: number
}

export interface RequestLogPage {
  items: RequestLogEntry[]
  status: RequestLogStatus
}
