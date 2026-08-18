import { requester, RequestError, type RequestOptions, type TrackedResult } from './requester'
import type { RequestWindow } from './log-preferences'
import type { Observation } from './operation-progress'
import type {
  AlertTestResult,
  BillingActualStats,
  BillingEstimatedStats,
  BillingInput,
  BillingStatsGranularity,
  BillingStatsRateMode,
  Chain,
  CleanupXrayResult,
  CommandLog,
  RebuildXrayResult,
  CreateChainRequest,
  EditChainRequest,
  ExternalChain,
  ExternalSubscription,
  ExternalSubscriptionMode,
  ChainTrafficBucket,
  CreateNodeRequest,
  CreateServerResponse,
  DashboardStats,
  LinkGroup,
  LinkGroupInput,
  LogSeverity,
  MachineType,
  Provider,
  OperationCategory,
  OperationLogPage,
  PanelSettings,
  PanelLifecycleSnapshot,
  PanelRuntimeSnapshot,
  PanelUpdateStatus,
  PanelVersionInfo,
  PortRange,
  RequestLogPage,
  ReleaseVersions,
  Server,
  ServerMetrics,
  ServerMetricSeries,
  ServerSettings,
  ServerTestCatalogStatus,
  ServerTestCategory,
  ServerTestTask,
  SubSettings,
  SubscriptionRoutingProfile,
  SubscriptionPreview,
  SubscriptionPreviewFormat,
  SubscriptionPreviewStage,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
  TrafficPlanInput,
  CustomExchangeRate,
  ExchangeRateSettings,
  SubUser,
  UpdateSettingsRequest,
  UserGroup,
  UserGroupInput,
  XrayNode,
} from './types'

export { RequestError as ApiError }
export type { TrackedResult }

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
    const result = await requester.post<void>('/api/auth/logout', {})
    requester.setCSRFToken(null)
    return result
  },
  me: async () => {
    const result = await requester.get<AuthSession>('/api/auth/me')
    requester.setCSRFToken(result.csrf_token)
    return result
  },

  dashboard: (options?: RequestOptions) => requester.get<DashboardStats>('/api/dashboard/get', undefined, options),

  servers: (options?: RequestOptions) => requester.get<Server[]>('/api/server/list', undefined, options),
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
  serverTest: (serverId: number, options?: RequestOptions) =>
    requester.get<ServerTestTask | null>('/api/server/get-test', { server_id: serverId }, options),
  runServerTest: (serverId: number, categories: ServerTestCategory[]) =>
    requester.post<ServerTestTask>('/api/server/run-test', { server_id: serverId, categories }),
  serverTestCatalogStatus: (options?: RequestOptions) =>
    requester.get<ServerTestCatalogStatus>('/api/server-test/catalog-status', undefined, options),
  refreshServerTestCatalog: () =>
    requester.post<ServerTestCatalogStatus>('/api/server-test/refresh-catalog', {}),
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
  serverCommands: (serverId: number, limit = 50, options?: RequestOptions) =>
    requester.get<CommandLog[]>('/api/server/list-commands', {
      server_id: serverId,
      limit,
    }, options),
  repairServer: (serverId: number) =>
    requester.postObserved<{ reapplied: number }>('/api/server/repair', { server_id: serverId }),
  cleanupXray: (serverId: number, dryRun: boolean) =>
    requester.postObserved<CleanupXrayResult>('/api/server/cleanup-xray', {
      server_id: serverId,
      dry_run: dryRun,
    }),
  rebuildXray: (serverId: number) =>
    requester.postObserved<RebuildXrayResult>('/api/server/rebuild-xray', {
      server_id: serverId,
    }),
  updateServerAddress: (
    serverId: number,
    alias: string,
    address: string,
    tags: string[],
    countryCode: string,
    location: string,
    billing?: BillingInput,
    trafficPlan?: TrafficPlanInput,
    customSettings?: ServerSettings,
    addresses?: string[],
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
      ...(customSettings !== undefined ? { custom_settings: customSettings } : {}),
      ...(addresses !== undefined ? { addresses } : {}),
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
    customSettings?: ServerSettings,
    addresses?: string[],
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
      ...(customSettings !== undefined ? { custom_settings: customSettings } : {}),
      ...(addresses !== undefined ? { addresses } : {}),
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

  chains: (options?: RequestOptions) => requester.get<Chain[]>('/api/chain/list', undefined, options),
  createChain: (body: CreateChainRequest) => requester.postObserved<Chain>('/api/chain/create', body),
  editChain: (body: EditChainRequest) => requester.postObserved<Chain>('/api/chain/edit', body),
  forcePublishChain: (chainId: number) =>
    requester.postObserved<Chain>('/api/chain/force-publish', { chain_id: chainId }),
  resetChainTraffic: (chainId: number) =>
    requester.post<void>('/api/chain/reset-traffic', { chain_id: chainId }),
  chainTrafficHistory: (chainId: number, hopId = 0, days = 30) =>
    requester.get<ChainTrafficBucket[]>('/api/chain/get-traffic-history', {
      chain_id: chainId,
      hop_id: hopId,
      days,
    }),
  retryChain: (chainId: number) =>
    requester.postObserved<Chain>('/api/chain/retry', { chain_id: chainId }),
  deleteChain: (chainId: number) =>
    requester.postObserved<void>('/api/chain/delete', { chain_id: chainId }),

  nodes: (options?: RequestOptions) => requester.get<XrayNode[]>('/api/node/list', undefined, options),
  createNode: (body: CreateNodeRequest) => requester.postObserved<XrayNode>('/api/node/create', body),
  retryNode: (nodeId: number) =>
    requester.postObserved<XrayNode>('/api/node/retry', { node_id: nodeId }),
  deleteNode: (nodeId: number) =>
    requester.postObserved<void>('/api/node/delete', { node_id: nodeId }),

  users: (options?: RequestOptions) => requester.get<SubUser[]>('/api/user/list', undefined, options),
  createUser: (
    name: string,
    expiresAt?: string | null,
    chainIds?: number[],
    sub?: { traffic_limit?: number; traffic_reset_day?: number; plan_name?: string; app_url?: string; routing?: SubscriptionRoutingProfile; external_subscriptions?: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }> },
  ) =>
    requester.postObserved<SubUser>('/api/user/create', {
      name,
      ...(expiresAt ? { expires_at: expiresAt } : {}),
      ...(chainIds && chainIds.length ? { chain_ids: chainIds } : {}),
      ...(sub ?? {}),
    }),
  setUserDisabled: (userId: number, disabled: boolean) =>
    requester.postObserved<SubUser>('/api/user/update', { user_id: userId, disabled }),
  setUserAssignments: (userId: number, nodeIds: number[], chainIds: number[]) =>
    requester.postObserved<{ node_ids: number[]; chain_ids: number[] }>('/api/user/set-nodes', {
      user_id: userId,
      node_ids: nodeIds,
      chain_ids: chainIds,
    }),
  setUserExternalSubscriptions: (
    userId: number,
    items: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }>,
  ) =>
    requester.postObserved<{ items: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }> }>(
      '/api/user/set-external-subscriptions',
      { user_id: userId, items },
    ),
  deleteUser: (userId: number) =>
    requester.post<void>('/api/user/delete', { user_id: userId }),
  updateUserSubSettings: (body: {
    user_id: number
    traffic_limit: number
    traffic_reset_day: number
    sub_title: string
    sub_announcement: string
    plan_name: string
    app_url: string
    routing: SubscriptionRoutingProfile
    expires_at: string | null
  }) => requester.postObserved<void>('/api/user/sub-settings', body),
  regenerateUserSubscription: (userId: number) =>
    requester.postObserved<void>('/api/user/regenerate-subscription', { user_id: userId }),
  resetUserSubscriptionToken: (userId: number) =>
    requester.post<{ sub_token: string; sub_url: string; sub_links_url: string }>(
      '/api/user/reset-subscription-token', { user_id: userId }),
  userSubscriptionPreview: (userId: number, format: SubscriptionPreviewFormat, stage?: SubscriptionPreviewStage) =>
    requester.get<SubscriptionPreview>('/api/user/subscription-preview', { user_id: userId, format, stage }),
  subscriptionCategories: (options?: RequestOptions) =>
    requester.get<SubscriptionRuleCategory[]>('/api/subscription/categories', undefined, options),
  subscriptionTemplates: (options?: RequestOptions) =>
    requester.get<SubscriptionTemplate[]>('/api/subscription/templates', undefined, options),
  saveSubscriptionTemplate: (body: {
    id?: string
    name: string
    kind: SubscriptionTemplate['kind']
    source_url?: string
    content?: string
    license?: string
  }) => requester.post<SubscriptionTemplate>('/api/subscription/template/save', body),
  cloneSubscriptionTemplate: (id: string, name?: string) =>
    requester.post<SubscriptionTemplate>('/api/subscription/template/clone', { id, name: name ?? '' }),
  deleteSubscriptionTemplate: (id: string) =>
    requester.post<void>('/api/subscription/template/delete', { id }),
  refreshSubscriptionTemplates: (id = '') =>
    requester.postObserved<SubscriptionTemplate[]>('/api/subscription/template/refresh', { id }),
  assignSubscriptionTemplate: (userIds: number[], target: { template_id?: string; suggested_categories?: string[] }, forced: boolean) =>
    requester.postObserved<{ user_ids: number[]; template_id?: string; suggested_categories?: string[]; forced: boolean }>(
      '/api/subscription/template/assign',
      { user_ids: userIds, ...target, forced },
    ),
  unassignSubscriptionTemplate: (userIds: number[], target: { template_id?: string; suggested_categories?: string[] }) =>
    requester.postObserved<{ user_ids: number[]; template_id?: string; suggested_categories?: string[] }>(
      '/api/subscription/template/unassign',
      { user_ids: userIds, ...target },
    ),
  externalSubscriptions: (options?: RequestOptions) =>
    requester.get<ExternalSubscription[]>('/api/external-subscription/list', undefined, options),
  linkGroups: (options?: RequestOptions) => requester.get<LinkGroup[]>('/api/link-group/list', undefined, options),
  linkGroupCreate: (input: LinkGroupInput) => requester.postObserved<{ id: number }>('/api/link-group/create', input),
  linkGroupUpdate: (input: LinkGroupInput) => requester.postObserved<{ id: number }>('/api/link-group/update', input),
  linkGroupDelete: (id: number) => requester.postObserved<null>('/api/link-group/delete', { id }),
  userGroups: (options?: RequestOptions) => requester.get<UserGroup[]>('/api/user-group/list', undefined, options),
  userGroupCreate: (input: UserGroupInput) => requester.postObserved<{ id: number }>('/api/user-group/create', input),
  userGroupUpdate: (input: UserGroupInput) => requester.postObserved<{ id: number }>('/api/user-group/update', input),
  userGroupDelete: (id: number) => requester.postObserved<null>('/api/user-group/delete', { id }),
  externalSubscriptionChains: (id: number, options?: RequestOptions) =>
    requester.get<ExternalChain[]>('/api/external-subscription/chains', { id }, options),
  createExternalSubscription: (body: {
    name: string
    url: string
    user_agent?: string
    skip_cert_verify?: boolean
    auto_update?: boolean
    update_interval_hours?: number
  }) => requester.postObserved<ExternalSubscription>('/api/external-subscription/create', body),
  updateExternalSubscription: (body: {
    id: number
    name: string
    url: string
    user_agent?: string
    skip_cert_verify?: boolean
    auto_update?: boolean
    update_interval_hours?: number
  }) => requester.postObserved<ExternalSubscription>('/api/external-subscription/update', body),
  deleteExternalSubscription: (id: number) =>
    requester.postObserved<void>('/api/external-subscription/delete', { id }),
  syncExternalSubscription: (id: number) =>
    requester.postObserved<ExternalSubscription>('/api/external-subscription/sync', { id }),
  userTrafficHistory: (userId: number) =>
    requester.get<Array<{ period_start: string; up: number; down: number }>>(
      '/api/user/traffic-history', { user_id: userId },
    ),

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
  panelState: () => requester.get<PanelLifecycleSnapshot>('/api/panel/state', undefined, {
    display: 'silent',
  }),
  panelRuntime: (options?: RequestOptions) => requester.get<PanelRuntimeSnapshot>(
    '/api/panel/runtime', undefined, options,
  ),
  testAlerts: () => requester.post<AlertTestResult>('/api/setting/test-alerts', {}),
  subSettings: () => requester.get<SubSettings>('/api/setting/sub'),
  updateSubSettings: (body: SubSettings) =>
    requester.post<SubSettings>('/api/setting/sub', body),

  billingStats: (
    params: {
      from: string
      to: string
      granularity: BillingStatsGranularity
      rate_mode?: BillingStatsRateMode
    },
    options?: RequestOptions,
  ) => requester.get<BillingActualStats>('/api/billing/stats', params, { display: 'silent', ...options }),

  billingStatsEstimated: (
    params: {
      from: string
      to: string
      granularity: BillingStatsGranularity
      rate_mode?: BillingStatsRateMode
    },
    options?: RequestOptions,
  ) => requester.get<BillingEstimatedStats>('/api/billing/stats/estimated', params, { display: 'silent', ...options }),

  panelVersion: () => requester.get<PanelVersionInfo>('/api/panel/get-version'),
  startPanelUpdate: (version?: string, force?: boolean) =>
    requester.post<PanelUpdateStatus>('/api/panel/start-update', {
      ...(version ? { version } : {}),
      ...(force ? { force: true } : {}),
    }),
  panelUpdateStatus: () =>
    requester.get<PanelUpdateStatus>('/api/panel/get-update-status', undefined, {
      display: 'silent',
    }),
  observeTask: (observeId: string) =>
    requester.get<Observation>('/api/observe-task/get', { observe_id: observeId }, {
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
  requestLogs: (limit: RequestWindow) =>
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
