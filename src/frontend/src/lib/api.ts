import { requester, RequestError } from './requester'
import type {
  AlertTestResult,
  BillingInput,
  Chain,
  CommandLog,
  CreateChainRequest,
  EditChainRequest,
  ChainTrafficBucket,
  CreateNodeRequest,
  CreateServerResponse,
  DashboardStats,
  LogSeverity,
  MachineType,
  Provider,
  OperationCategory,
  OperationLogPage,
  PanelSettings,
  PanelUpdateStatus,
  PanelVersionInfo,
  PortRange,
  RequestLogPage,
  ReleaseVersions,
  Server,
  ServerMetrics,
  ServerMetricSeries,
  TrafficPlanInput,
  CustomExchangeRate,
  ExchangeRateSettings,
  SubUser,
  UpdateSettingsRequest,
  XrayNode,
} from './types'

export { RequestError as ApiError }

export function setOnUnauthorized(fn: (() => void) | null) {
  requester.setUnauthorizedHandler(
    fn
      ? () => {
          requester.setCSRFToken(null)
          fn()
        }
      : null,
  )
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : '操作失败，请重试'
}

interface AuthSession {
  username: string
  csrf_token: string
}

export const api = {
  login: async (username: string, password: string) => {
    const result = await requester.post<AuthSession>('/api/auth/login', { username, password })
    requester.setCSRFToken(result.csrf_token)
    return result
  },
  logout: async () => {
    try {
      return await requester.post<void>('/api/auth/logout', {})
    } finally {
      requester.setCSRFToken(null)
    }
  },
  me: async () => {
    const result = await requester.get<AuthSession>('/api/auth/me')
    requester.setCSRFToken(result.csrf_token)
    return result
  },

  dashboard: () => requester.get<DashboardStats>('/api/dashboard/get'),

  servers: () => requester.get<Server[]>('/api/server/list'),
  serverMetricSamples: (limit = 30) =>
    requester.get<ServerMetricSeries[]>('/api/server/list-metric-samples', { limit }, {
      display: 'silent',
    }),
  serverMetricHistory: (serverId: number, hours = 24) =>
    requester.get<ServerMetrics[]>('/api/server/get-metric-history', {
      server_id: serverId,
      hours,
    }, {
      display: 'silent',
    }),
  createServer: (body: {
    alias: string
    address?: string
    machine_type?: MachineType
    allowed_ports?: PortRange[]
    tags?: string[]
    country_code: string
    location: string
    billing?: BillingInput
    traffic_plan?: TrafficPlanInput
  }) => requester.post<CreateServerResponse>('/api/server/create', body),
  rotateServerToken: (serverId: number) =>
    requester.post<CreateServerResponse>('/api/server/rotate-token', { server_id: serverId }),
  upgradeServer: (serverId: number, version: string) =>
    requester.post<{ command_id: number; version: string }>('/api/server/upgrade-xray', {
      server_id: serverId,
      version,
    }),
  upgradeAgent: (serverId: number, version: string) =>
    requester.post<{ command_id: number; version: string }>('/api/server/upgrade-agent', {
      server_id: serverId,
      version,
    }),
  releaseVersions: (kind: 'agent' | 'xray') =>
    requester.get<ReleaseVersions>('/api/server/list-release-versions', { kind }),
  serverCommands: (serverId: number, limit = 50) =>
    requester.get<CommandLog[]>('/api/server/list-commands', {
      server_id: serverId,
      limit,
    }),
  repairServer: (serverId: number) =>
    requester.post<{ reapplied: number }>('/api/server/repair', { server_id: serverId }),
  updateServerAddress: (
    serverId: number,
    alias: string,
    address: string,
    tags: string[],
    countryCode: string,
    location: string,
    billing?: BillingInput,
    trafficPlan?: TrafficPlanInput,
  ) =>
    requester.post<Server>('/api/server/update', {
      server_id: serverId,
      alias: alias.trim(),
      address: address.trim(),
      tags,
      country_code: countryCode,
      location,
      billing,
      traffic_plan: trafficPlan,
    }),
  updateServerPorts: (
    serverId: number,
    alias: string,
    address: string,
    allowedPorts: PortRange[],
    tags: string[],
    countryCode: string,
    location: string,
    billing?: BillingInput,
    trafficPlan?: TrafficPlanInput,
  ) =>
    requester.post<Server>('/api/server/update', {
      server_id: serverId,
      alias: alias.trim(),
      address: address.trim(),
      allowed_ports: allowedPorts,
      tags,
      country_code: countryCode,
      location,
      billing,
      traffic_plan: trafficPlan,
    }),
  deleteServer: (serverId: number, purge: 'xray' | 'agent') =>
    requester.post<void>('/api/server/delete', { server_id: serverId, purge }),
  confirmServerRenewal: (serverId: number, nextRenewalOn: string) =>
    requester.post<{ status: string; next_renewal_on: string }>('/api/server/confirm-renewal', {
      server_id: serverId,
      next_renewal_on: nextRenewalOn,
    }),
  providers: () => requester.get<Provider[]>('/api/provider/list'),
  createProvider: (name: string, websiteUrl: string) =>
    requester.post<Provider>('/api/provider/create', { name, website_url: websiteUrl }),
  updateProvider: (id: number, name: string, websiteUrl: string) =>
    requester.post<Provider>('/api/provider/update', { id, name, website_url: websiteUrl }),
  deleteProvider: (id: number) => requester.post<void>('/api/provider/delete', { id }),
  exchangeRates: () => requester.get<ExchangeRateSettings>('/api/exchange-rate/list'),
  refreshExchangeRates: () => requester.post<ExchangeRateSettings>('/api/exchange-rate/refresh', {}),
  saveCustomExchangeRate: (rate: Omit<CustomExchangeRate, 'updated_at'>) =>
    requester.post<CustomExchangeRate>('/api/exchange-rate/save-custom', rate),
  deleteCustomExchangeRate: (id: number) => requester.post<void>('/api/exchange-rate/delete-custom', { id }),

  chains: () => requester.get<Chain[]>('/api/chain/list'),
  createChain: (body: CreateChainRequest) => requester.post<Chain>('/api/chain/create', body),
  editChain: (body: EditChainRequest) => requester.post<Chain>('/api/chain/edit', body),
  forcePublishChain: (chainId: number) =>
    requester.post<Chain>('/api/chain/force-publish', { chain_id: chainId }),
  resetChainTraffic: (chainId: number) =>
    requester.post<void>('/api/chain/reset-traffic', { chain_id: chainId }),
  chainTrafficHistory: (chainId: number, hopId = 0, days = 30) =>
    requester.get<ChainTrafficBucket[]>(`/api/chain/get-traffic-history?chain_id=${chainId}&hop_id=${hopId}&days=${days}`),
  retryChain: (chainId: number) =>
    requester.post<Chain>('/api/chain/retry', { chain_id: chainId }),
  deleteChain: (chainId: number) =>
    requester.post<void>('/api/chain/delete', { chain_id: chainId }),

  nodes: () => requester.get<XrayNode[]>('/api/node/list'),
  createNode: (body: CreateNodeRequest) => requester.post<XrayNode>('/api/node/create', body),
  retryNode: (nodeId: number) =>
    requester.post<XrayNode>('/api/node/retry', { node_id: nodeId }),
  deleteNode: (nodeId: number) =>
    requester.post<void>('/api/node/delete', { node_id: nodeId }),

  users: () => requester.get<SubUser[]>('/api/user/list'),
  createUser: (name: string, expiresAt?: string | null, nodeIds?: number[]) =>
    requester.post<SubUser>('/api/user/create', {
      name,
      ...(expiresAt ? { expires_at: expiresAt } : {}),
      ...(nodeIds && nodeIds.length ? { node_ids: nodeIds } : {}),
    }),
  updateUserExpiry: (userId: number, expiresAt: string | null) =>
    requester.post<SubUser>('/api/user/update', {
      user_id: userId,
      expires_at: expiresAt,
    }),
  setUserDisabled: (userId: number, disabled: boolean) =>
    requester.post<SubUser>('/api/user/update', { user_id: userId, disabled }),
  setUserNodes: (userId: number, nodeIds: number[]) =>
    requester.post<{ node_ids: number[] }>('/api/user/set-nodes', {
      user_id: userId,
      node_ids: nodeIds,
    }),
  deleteUser: (userId: number) =>
    requester.post<void>('/api/user/delete', { user_id: userId }),

  settings: () => requester.get<PanelSettings>('/api/setting/get'),
  updateSettings: (body: UpdateSettingsRequest) =>
    requester.post<PanelSettings>('/api/setting/update', body),
  changePassword: async (currentPassword: string, newPassword: string) => {
    const result = await requester.post<void>('/api/setting/change-password', {
      current_password: currentPassword,
      new_password: newPassword,
    })
    requester.setCSRFToken(null)
    return result
  },
  restartPanel: () => requester.post<{ status: string }>('/api/panel/restart', {}),
  testAlerts: () => requester.post<AlertTestResult>('/api/setting/test-alerts', {}),

  panelVersion: () => requester.get<PanelVersionInfo>('/api/panel/get-version'),
  startPanelUpdate: (version?: string) =>
    requester.post<PanelUpdateStatus>('/api/panel/start-update', version ? { version } : {}),
  panelUpdateStatus: () =>
    requester.get<PanelUpdateStatus>('/api/panel/get-update-status', undefined, {
      display: 'silent',
    }),

  operationLogs: (params: {
    severity?: LogSeverity | ''
    category?: OperationCategory | ''
    server_id?: number
    operator?: string
    q?: string
    from?: string
    to?: string
    limit?: number
    offset?: number
  }) =>
    requester.get<OperationLogPage>(
      '/api/log/list-operations',
      {
        severity: params.severity || undefined,
        category: params.category || undefined,
        server_id: params.server_id,
        operator: params.operator || undefined,
        q: params.q || undefined,
        from: params.from || undefined,
        to: params.to || undefined,
        limit: params.limit,
        offset: params.offset,
      },
      { display: 'silent' },
    ),
  clearOperationLogs: () => requester.post<void>('/api/log/clear-operations', {}),
  requestLogs: (limit: 10 | 30 | 50 | 100) =>
    requester.get<RequestLogPage>(
      '/api/log/list-requests',
      { limit },
      { display: 'silent' },
    ),
  clearRequestLogs: () => requester.post<void>('/api/log/clear-requests', {}),
  downloadBackup: () => requester.download('/api/backup/download'),
}

export function isRequestError(error: unknown): error is RequestError {
  return error instanceof RequestError
}
