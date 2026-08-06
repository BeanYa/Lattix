import {
  isRPCCode,
  type RPCCode,
  type RPCEnvelope,
  type RPCPathByMethod,
} from './api-contract.generated'

export type { RPCCode, RPCEnvelope } from './api-contract.generated'

export type RequestFailureKind = 'business' | 'transport' | 'protocol' | 'cancelled'
export type TrackedResult<T> = { data: T; observeId?: string }
export type RequestErrorCode =
  | RPCCode
  | `HTTP_${number}`
  | 'INVALID_RESPONSE'
  | 'REQUEST_CANCELLED'
  | 'REQUEST_TIMEOUT'
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
const GET_MAX_ATTEMPTS = 3
const GET_RETRY_DELAYS_MS = [500, 1_500] as const

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
    path: RPCPathByMethod<'GET'>,
    query?: Record<string, string | number | boolean | undefined>,
    options?: RequestOptions,
  ): Promise<T> {
    const search = new URLSearchParams()
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) search.set(key, String(value))
    }
    const suffix = search.size ? `?${search.toString()}` : ''
    return this.execute<T>('GET', path + suffix, undefined, options, GET_MAX_ATTEMPTS)
  }

  getJSON<T>(path: string, options?: RequestOptions): Promise<T> {
    return this.executeJSON<T>(path, options, GET_MAX_ATTEMPTS)
  }

  post<T>(path: RPCPathByMethod<'POST'>, body: object = {}, options?: RequestOptions): Promise<T> {
    return this.execute<T>('POST', path, body, options, 1)
  }

  postObserved<T>(
    path: RPCPathByMethod<'POST'>,
    body: object = {},
    options?: RequestOptions,
  ): Promise<TrackedResult<T>> {
    return this.executeObserved<T>('POST', path, body, options)
  }

  async download(path: string, options?: RequestOptions): Promise<void> {
    const traceId = options?.traceId ?? newRequestId()
    const requestId = newRequestId()
    const display = options?.display ?? 'foreground'
    const lifecycle = { requestId, traceId, method: 'DOWNLOAD' as const, path, display }
    this.emit({ phase: 'start', ...lifecycle })
    let requestError: RequestError | undefined
    const { signal, cleanup, abortSource } = combinedSignal(
      options?.signal,
      options?.timeoutMs ?? 30_000,
    )
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
        const error = businessError(envelope, response.status)
        if (error.code === 'AUTH_REQUIRED') this.unauthorizedHandler?.()
        throw error
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
      globalThis.setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error) {
      requestError = normalizeError(error, requestId, traceId, abortSource())
      throw requestError
    } finally {
      cleanup()
      this.emit({ phase: 'finish', ...lifecycle, error: requestError })
    }
  }

  private async executeJSON<T>(
    path: string,
    options: RequestOptions | undefined,
    maxAttempts: number,
  ): Promise<T> {
    const traceId = options?.traceId ?? newRequestId()
    const display = options?.display ?? 'foreground'
    let lastError: RequestError | undefined

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const requestId = newRequestId()
      const lifecycle = { requestId, traceId, method: 'GET' as const, path, display }
      this.emit({ phase: 'start', ...lifecycle })
      const { signal, cleanup, abortSource } = combinedSignal(
        options?.signal,
        options?.timeoutMs ?? 15_000,
      )
      try {
        const response = await fetch(path, {
          method: 'GET',
          credentials: 'include',
          signal,
          headers: { 'X-Request-ID': requestId, 'X-Trace-ID': traceId },
        })
        if (!response.ok) {
          throw new RequestError({
            kind: 'transport',
            code: `HTTP_${response.status}`,
            message: `请求失败（HTTP ${response.status}）`,
            httpStatus: response.status,
            requestId,
            traceId,
          })
        }
        const result = await parseJSON<T>(response, requestId, traceId)
        this.emit({ phase: 'finish', ...lifecycle })
        cleanup()
        return result
      } catch (error) {
        cleanup()
        lastError = normalizeError(error, requestId, traceId, abortSource())
        this.emit({ phase: 'finish', ...lifecycle, error: lastError })
        const retryable = lastError.code === 'NETWORK_UNREACHABLE' || lastError.code === 'REQUEST_TIMEOUT'
        if (attempt + 1 >= maxAttempts || !retryable) throw lastError
        const retryDelay = GET_RETRY_DELAYS_MS[Math.min(attempt, GET_RETRY_DELAYS_MS.length - 1)]
        if (!(await waitForRetry(retryDelay, options?.signal))) {
          throw cancelledError(requestId, traceId)
        }
      }
    }
    throw lastError
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
      const { signal, cleanup, abortSource } = combinedSignal(
        options?.signal,
        options?.timeoutMs ?? 15_000,
      )
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
        lastError = normalizeError(error, requestId, traceId, abortSource())
        this.emit({ phase: 'finish', ...lifecycle, error: lastError })
        if (attempt + 1 >= maxAttempts || lastError.kind !== 'transport') {
          throw lastError
        }
        const retryDelay = GET_RETRY_DELAYS_MS[Math.min(attempt, GET_RETRY_DELAYS_MS.length - 1)]
        if (!(await waitForRetry(retryDelay, options?.signal))) {
          throw cancelledError(requestId, traceId)
        }
      }
    }
    throw lastError
  }

  private async executeObserved<T>(
    method: 'POST',
    path: string,
    body: object | undefined,
    options: RequestOptions | undefined,
  ): Promise<TrackedResult<T>> {
    const traceId = options?.traceId ?? newRequestId()
    const display = options?.display ?? 'foreground'
    const idempotencyKey = options?.idempotencyKey ?? newRequestId()
    const requestId = newRequestId()
    const lifecycle = { requestId, traceId, method, path, display }
    this.emit({ phase: 'start', ...lifecycle })
    const { signal, cleanup, abortSource } = combinedSignal(
      options?.signal,
      options?.timeoutMs ?? 15_000,
    )
    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
        'X-Request-ID': requestId,
        'X-Trace-ID': traceId,
      }
      if (this.csrfToken) headers['X-CSRF-Token'] = this.csrfToken
      const response = await fetch(path, {
        method,
        credentials: 'include',
        headers,
        body: JSON.stringify(body ?? {}),
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
      return { data: envelope.data, observeId: envelope.observe_id }
    } catch (error) {
      cleanup()
      const lastError = normalizeError(error, requestId, traceId, abortSource())
      this.emit({ phase: 'finish', ...lifecycle, error: lastError })
      throw lastError
    }
  }

  private emit(event: RequestLifecycleEvent) {
    for (const listener of this.listeners) listener(event)
  }
}

async function parseEnvelope<T>(
  response: Response,
  fallbackRequestId: string,
  fallbackTraceId: string,
): Promise<RPCEnvelope<T> & { observe_id?: string }> {
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
  if (!isRecord(value)) {
    throw invalidEnvelope(response.status, fallbackRequestId, fallbackTraceId)
  }
  const {
    code,
    message,
    data,
    request_id: requestId,
    trace_id: traceId,
    observe_id: observeId,
  } = value
  if (
    !(isRPCCode(code) || isProtocolCode(code)) ||
    typeof message !== 'string' ||
    !isMessageId(requestId) ||
    !isMessageId(traceId) ||
    !('data' in value)
  ) {
    throw invalidEnvelope(response.status, fallbackRequestId, fallbackTraceId)
  }
  return {
    code,
    message,
    data: data as T,
    request_id: requestId,
    trace_id: traceId,
    ...(observeId !== undefined ? { observe_id: observeId as string } : {}),
  }
}

async function parseJSON<T>(
  response: Response,
  requestId: string,
  traceId: string,
): Promise<T> {
  try {
    return await response.json() as T
  } catch {
    throw new RequestError({
      kind: 'protocol',
      code: 'INVALID_RESPONSE',
      message: '服务端返回了无效 JSON',
      httpStatus: response.status,
      requestId,
      traceId,
    })
  }
}

function invalidEnvelope(httpStatus: number, requestId: string, traceId: string): RequestError {
  return new RequestError({
    kind: 'protocol',
    code: 'INVALID_RESPONSE',
    message: '服务端响应不符合 RPC 信封',
    httpStatus,
    requestId,
    traceId,
  })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function isProtocolCode(value: unknown): value is `HTTP_${number}` {
  return typeof value === 'string' && /^HTTP_[1-5]\d{2}$/.test(value)
}

function isMessageId(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value)
}

function businessError<T>(envelope: RPCEnvelope<T>, httpStatus: number): RequestError {
  return new RequestError({
    kind: isRPCCode(envelope.code) ? 'business' : 'protocol',
    code: envelope.code,
    message: envelope.message || envelope.code,
    httpStatus,
    requestId: envelope.request_id,
    traceId: envelope.trace_id,
  })
}

type AbortSource = 'parent' | 'timeout' | null

function normalizeError(
  error: unknown,
  requestId: string,
  traceId: string,
  abortSource: AbortSource = null,
): RequestError {
  if (error instanceof RequestError) return error
  if (abortSource === 'timeout') {
    return new RequestError({
      kind: 'transport',
      code: 'REQUEST_TIMEOUT',
      message: '请求超时',
      requestId,
      traceId,
    })
  }
  if (abortSource === 'parent' || (error instanceof DOMException && error.name === 'AbortError')) {
    return cancelledError(requestId, traceId)
  }
  return new RequestError({
    kind: 'transport',
    code: 'NETWORK_UNREACHABLE',
    message: error instanceof Error ? error.message : '网络不可达',
    requestId,
    traceId,
  })
}

function cancelledError(requestId: string, traceId: string): RequestError {
  return new RequestError({
    kind: 'cancelled',
    code: 'REQUEST_CANCELLED',
    message: '请求已取消',
    requestId,
    traceId,
  })
}

function combinedSignal(parent: AbortSignal | undefined, timeoutMs: number) {
  const controller = new AbortController()
  let source: AbortSource = null
  const timeout = globalThis.setTimeout(() => {
    if (source === null) source = 'timeout'
    controller.abort()
  }, timeoutMs)
  const abort = () => {
    if (source === null) source = 'parent'
    controller.abort(parent?.reason)
  }
  if (parent?.aborted) {
    abort()
  } else {
    parent?.addEventListener('abort', abort, { once: true })
  }
  return {
    signal: controller.signal,
    abortSource: () => source,
    cleanup: () => {
      globalThis.clearTimeout(timeout)
      parent?.removeEventListener('abort', abort)
    },
  }
}

function waitForRetry(delayMs: number, signal?: AbortSignal): Promise<boolean> {
  if (signal?.aborted) return Promise.resolve(false)
  return new Promise((resolve) => {
    const finish = (ready: boolean) => {
      globalThis.clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
      resolve(ready)
    }
    const timer = globalThis.setTimeout(() => finish(true), delayMs)
    const onAbort = () => finish(false)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

export const requester = new Requester()
