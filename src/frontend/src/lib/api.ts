import type {
  AlertTestResult,
  Chain,
  CreateChainRequest,
  CreateNodeRequest,
  CreateServerResponse,
  DashboardStats,
  MachineType,
  PanelSettings,
  PortRange,
  Server,
  SubUser,
  UpdateSettingsRequest,
  XrayNode,
} from './types'

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

let onUnauthorized: (() => void) | null = null

/** 注册 401 回调（会话过期时清空登录态，路由守卫会自动跳登录页） */
export function setOnUnauthorized(fn: (() => void) | null) {
  onUnauthorized = fn
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  if (res.status === 401) {
    onUnauthorized?.()
    const data = (await res.json().catch(() => null)) as { error?: string } | null
    throw new ApiError(401, data?.error ?? '未登录或会话已过期')
  }
  if (res.status === 204) {
    return undefined as T
  }
  const data = (await res.json().catch(() => null)) as ({ error?: string } & T) | null
  if (!res.ok) {
    throw new ApiError(res.status, data?.error ?? `请求失败（${res.status}）`)
  }
  return data as T
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : '操作失败，请重试'
}

export const api = {
  login: (username: string, password: string) =>
    request<{ username: string }>('/api/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>('/api/logout', { method: 'POST' }),
  me: () => request<{ username: string }>('/api/me'),

  dashboard: () => request<DashboardStats>('/api/dashboard'),

  servers: () => request<Server[]>('/api/servers'),
  createServer: (body: {
    alias: string
    address?: string
    xray_version?: string
    machine_type?: MachineType
    allowed_ports?: PortRange[]
  }) =>
    request<CreateServerResponse>('/api/servers', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  rotateServerToken: (id: number) =>
    request<CreateServerResponse>(`/api/servers/${id}/rotate-token`, { method: 'POST' }),
  upgradeServer: (id: number, version: string) =>
    request<{ command_id: number; version: string }>(`/api/servers/${id}/upgrade`, {
      method: 'POST',
      body: JSON.stringify({ version }),
    }),
  upgradeAgent: (id: number, version: string) =>
    request<{ command_id: number; version: string }>(`/api/servers/${id}/upgrade-agent`, {
      method: 'POST',
      body: JSON.stringify({ version }),
    }),
  repairServer: (id: number) =>
    request<{ reapplied: number }>(`/api/servers/${id}/repair`, { method: 'POST' }),
  updateServerAddress: (id: number, address: string) =>
    request<Server>(`/api/servers/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ address: address.trim() }),
    }),
  // 编辑 NAT 可用端口段（§21）：allowed_ports 整体替换；机器类型建后不可互转。
  updateServerPorts: (id: number, address: string, allowedPorts: PortRange[]) =>
    request<Server>(`/api/servers/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ address: address.trim(), allowed_ports: allowedPorts }),
    }),
  deleteServer: (id: number, purge: 'xray' | 'agent') =>
    request<void>(`/api/servers/${id}?purge=${purge}`, { method: 'DELETE' }),

  chains: () => request<Chain[]>('/api/chains'),
  createChain: (body: CreateChainRequest) =>
    request<Chain>('/api/chains', { method: 'POST', body: JSON.stringify(body) }),
  retryChain: (id: number) => request<Chain>(`/api/chains/${id}/retry`, { method: 'POST' }),
  deleteChain: (id: number) => request<void>(`/api/chains/${id}`, { method: 'DELETE' }),

  nodes: () => request<XrayNode[]>('/api/nodes'),
  createNode: (body: CreateNodeRequest) =>
    request<XrayNode>('/api/nodes', { method: 'POST', body: JSON.stringify(body) }),
  retryNode: (id: number) => request<XrayNode>(`/api/nodes/${id}/retry`, { method: 'POST' }),
  deleteNode: (id: number) => request<void>(`/api/nodes/${id}`, { method: 'DELETE' }),

  users: () => request<SubUser[]>('/api/users'),
  createUser: (name: string, expiresAt?: string | null) =>
    request<SubUser>('/api/users', {
      method: 'POST',
      body: JSON.stringify({ name, ...(expiresAt ? { expires_at: expiresAt } : {}) }),
    }),
  updateUserExpiry: (id: number, expiresAt: string | null) =>
    request<SubUser>(`/api/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ expires_at: expiresAt }),
    }),
  setUserDisabled: (id: number, disabled: boolean) =>
    request<SubUser>(`/api/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ disabled }),
    }),
  setUserNodes: (id: number, nodeIds: number[]) =>
    request<{ node_ids: number[] }>(`/api/users/${id}/nodes`, {
      method: 'PUT',
      body: JSON.stringify({ node_ids: nodeIds }),
    }),
  deleteUser: (id: number) => request<void>(`/api/users/${id}`, { method: 'DELETE' }),

  settings: () => request<PanelSettings>('/api/settings'),
  updateSettings: (body: UpdateSettingsRequest) =>
    request<PanelSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(body) }),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('/api/settings/password', {
      method: 'PUT',
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    }),
  restartPanel: () =>
    request<{ status: string }>('/api/settings/restart', { method: 'POST' }),
  testAlerts: () =>
    request<AlertTestResult>('/api/settings/alerts/test', { method: 'POST' }),
}
