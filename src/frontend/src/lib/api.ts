import type {
  CreateNodeRequest,
  CreateServerResponse,
  DashboardStats,
  Server,
  SubUser,
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
  createServer: (alias: string) =>
    request<CreateServerResponse>('/api/servers', {
      method: 'POST',
      body: JSON.stringify({ alias }),
    }),

  nodes: () => request<XrayNode[]>('/api/nodes'),
  createNode: (body: CreateNodeRequest) =>
    request<XrayNode>('/api/nodes', { method: 'POST', body: JSON.stringify(body) }),
  retryNode: (id: number) => request<XrayNode>(`/api/nodes/${id}/retry`, { method: 'POST' }),
  deleteNode: (id: number) => request<void>(`/api/nodes/${id}`, { method: 'DELETE' }),

  users: () => request<SubUser[]>('/api/users'),
  createUser: (name: string) =>
    request<SubUser>('/api/users', { method: 'POST', body: JSON.stringify({ name }) }),
  deleteUser: (id: number) => request<void>(`/api/users/${id}`, { method: 'DELETE' }),
}
