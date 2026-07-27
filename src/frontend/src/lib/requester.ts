import type { RPCCode, RPCEnvelope } from './api-contract.generated'

export type { RPCCode, RPCEnvelope } from './api-contract.generated'

export type RequestFailureKind = 'business' | 'transport' | 'protocol' | 'cancelled'
export type RequestErrorCode =
  | RPCCode
  | `HTTP_${number}`
  | 'INVALID_RESPONSE'
  | 'REQUEST_CANCELLED'
  | 'NETWORK_UNREACHABLE'

export class RequestError extends Error {
  readonly kind: RequestFailureKind
  readonly code: RequestErrorCode
  readonly httpStatus: number
  readonly requestId: string
  readonly traceId: string

  constructor(options: {
    kind: RequestFailureKind
    code: RequestErrorCode
    message: string
    httpStatus?: number
    requestId?: string
    traceId?: string
  }) {
    super(options.message)
    this.name = 'RequestError'
    this.kind = options.kind
    this.code = options.code
    this.httpStatus = options.httpStatus ?? 0
    this.requestId = options.requestId ?? ''
    this.traceId = options.traceId ?? ''
  }
}

export interface RequestLifecycleEvent {
  phase: 'start' | 'finish'
  requestId: string
  traceId: string
  method: 'GET' | 'POST' | 'DOWNLOAD'
  path: string
  display: 'foreground' | 'silent'
  error?: RequestError
}

export interface RequestOptions {
  signal?: AbortSignal
  timeoutMs?: number
  display?: 'foreground' | 'silent'
  traceId?: string
  idempotencyKey?: string
}

type LifecycleListener = (event: RequestLifecycleEvent) => void

const ID_BYTES = 16

export function newRequestId(): string {
  const bytes = new Uint8Array(ID_BYTES)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

export class Requester {
  private csrfToken = ''
  private unauthorizedHandler: (() => void) | null = null
  private readonly listeners = new Set<LifecycleListener>()

  setCSRFToken(token: string | null) {
    this.csrfToken = token ?? ''
  }

  setUnauthorizedHandler(handler: (() => void) | null) {
    this.unauthorizedHandler = handler
  }

  subscribe(listener: LifecycleListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  get<T>(
    path: string,
    query?: Record<string, string | number | boolean | undefined>,
    options?: RequestOptions,
  ): Promise<T> {
    const search = new URLSearchParams()
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) search.set(key, String(value))
    }
    const suffix = search.size ? `?${search.toString()}` : ''
    return this.execute<T>('GET', path + suffix, undefined, options, 2)
  }

  post<T>(path: string, body: object = {}, options?: RequestOptions): Promise<T> {
    return this.execute<T>('POST', path, body, options, 1)
  }

  async download(path: string, options?: RequestOptions): Promise<void> {
    const traceId = options?.traceId ?? newRequestId()
    const requestId = newRequestId()
    const display = options?.display ?? 'foreground'
    const lifecycle = { requestId, traceId, method: 'DOWNLOAD' as const, path, display }
    this.emit({ phase: 'start', ...lifecycle })
    let requestError: RequestError | undefined
    const { signal, cleanup } = combinedSignal(options?.signal, options?.timeoutMs ?? 30_000)
    try {
      const response = await fetch(path, {
        method: 'GET',
        credentials: 'include',
        signal,
        headers: { 'X-Request-ID': requestId, 'X-Trace-ID': traceId },
      })
      const contentType = response.headers.get('Content-Type') ?? ''
      if (contentType.includes('json')) {
        const envelope = await parseEnvelope<never>(response, requestId, traceId)
        throw businessError(envelope, response.status)
      }
      if (!response.ok) {
        throw new RequestError({
          kind: 'transport',
          code: `HTTP_${response.status}`,
          message: `下载失败（HTTP ${response.status}）`,
          httpStatus: response.status,
          requestId,
          traceId,
        })
      }
      const blob = await response.blob()
      const disposition = response.headers.get('Content-Disposition') ?? ''
      const match = disposition.match(/filename="?([^";]+)"?/)
      const filename = match?.[1] ?? 'lattix-backup.db'
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = filename
      anchor.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      requestError = normalizeError(error, requestId, traceId)
      throw requestError
    } finally {
      cleanup()
      this.emit({ phase: 'finish', ...lifecycle, error: requestError })
    }
  }

  private async execute<T>(
    method: 'GET' | 'POST',
    path: string,
    body: object | undefined,
    options: RequestOptions | undefined,
    maxAttempts: number,
  ): Promise<T> {
    const traceId = options?.traceId ?? newRequestId()
    const display = options?.display ?? 'foreground'
    const idempotencyKey = options?.idempotencyKey ?? (method === 'POST' ? newRequestId() : '')
    let lastError: RequestError | undefined

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const requestId = newRequestId()
      const lifecycle = { requestId, traceId, method, path, display }
      this.emit({ phase: 'start', ...lifecycle })
      const { signal, cleanup } = combinedSignal(options?.signal, options?.timeoutMs ?? 15_000)
      try {
        const headers: Record<string, string> = {
          'X-Request-ID': requestId,
          'X-Trace-ID': traceId,
        }
        if (method === 'POST') {
          headers['Content-Type'] = 'application/json'
          headers['Idempotency-Key'] = idempotencyKey
          if (this.csrfToken) headers['X-CSRF-Token'] = this.csrfToken
        }
        const response = await fetch(path, {
          method,
          credentials: 'include',
          headers,
          body: method === 'POST' ? JSON.stringify(body ?? {}) : undefined,
          signal,
        })
        const envelope = await parseEnvelope<T>(response, requestId, traceId)
        if (!response.ok) {
          throw new RequestError({
            kind: 'protocol',
            code: envelope.code || `HTTP_${response.status}`,
            message: envelope.message || `请求协议失败（HTTP ${response.status}）`,
            httpStatus: response.status,
            requestId: envelope.request_id,
            traceId: envelope.trace_id,
          })
        }
        if (envelope.code !== 'OK' && envelope.code !== 'ACCEPTED') {
          const error = businessError(envelope, response.status)
          if (error.code === 'AUTH_REQUIRED') this.unauthorizedHandler?.()
          throw error
        }
        this.emit({ phase: 'finish', ...lifecycle })
        cleanup()
        return envelope.data
      } catch (error) {
        cleanup()
        lastError = normalizeError(error, requestId, traceId)
        this.emit({ phase: 'finish', ...lifecycle, error: lastError })
        if (attempt + 1 >= maxAttempts || lastError.kind !== 'transport') {
          throw lastError
        }
      }
    }
    throw lastError
  }

  private emit(event: RequestLifecycleEvent) {
    for (const listener of this.listeners) listener(event)
  }
}

async function parseEnvelope<T>(
  response: Response,
  fallbackRequestId: string,
  fallbackTraceId: string,
): Promise<RPCEnvelope<T>> {
  let value: unknown
  try {
    value = await response.json()
  } catch {
    throw new RequestError({
      kind: 'protocol',
      code: 'INVALID_RESPONSE',
      message: '服务端返回了无效 JSON',
      httpStatus: response.status,
      requestId: response.headers.get('X-Request-ID') ?? fallbackRequestId,
      traceId: response.headers.get('X-Trace-ID') ?? fallbackTraceId,
    })
  }
  if (
    !value ||
    typeof value !== 'object' ||
    typeof (value as RPCEnvelope<T>).code !== 'string' ||
    typeof (value as RPCEnvelope<T>).message !== 'string' ||
    !isMessageId((value as RPCEnvelope<T>).request_id) ||
    !isMessageId((value as RPCEnvelope<T>).trace_id) ||
    !('data' in value)
  ) {
    throw new RequestError({
      kind: 'protocol',
      code: 'INVALID_RESPONSE',
      message: '服务端响应不符合 RPC 信封',
      httpStatus: response.status,
      requestId: fallbackRequestId,
      traceId: fallbackTraceId,
    })
  }
  return value as RPCEnvelope<T>
}

function isMessageId(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value)
}

function businessError<T>(envelope: RPCEnvelope<T>, httpStatus: number): RequestError {
  return new RequestError({
    kind: 'business',
    code: envelope.code,
    message: envelope.message || envelope.code,
    httpStatus,
    requestId: envelope.request_id,
    traceId: envelope.trace_id,
  })
}

function normalizeError(error: unknown, requestId: string, traceId: string): RequestError {
  if (error instanceof RequestError) return error
  if (error instanceof DOMException && error.name === 'AbortError') {
    return new RequestError({
      kind: 'cancelled',
      code: 'REQUEST_CANCELLED',
      message: '请求已取消或超时',
      requestId,
      traceId,
    })
  }
  return new RequestError({
    kind: 'transport',
    code: 'NETWORK_UNREACHABLE',
    message: error instanceof Error ? error.message : '网络不可达',
    requestId,
    traceId,
  })
}

function combinedSignal(parent: AbortSignal | undefined, timeoutMs: number) {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs)
  const abort = () => controller.abort()
  parent?.addEventListener('abort', abort, { once: true })
  return {
    signal: controller.signal,
    cleanup: () => {
      window.clearTimeout(timeout)
      parent?.removeEventListener('abort', abort)
    },
  }
}

export const requester = new Requester()
