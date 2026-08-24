import { afterEach, describe, expect, it, vi } from 'vitest'

import { RequestError, Requester, type RequestLifecycleEvent } from './requester'

const REQUEST_ID = '0123456789abcdef0123456789abcdef'
const TRACE_ID = 'fedcba9876543210fedcba9876543210'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function envelope(code: string, data: unknown = null, message = '') {
  return {
    code,
    message,
    data,
    request_id: REQUEST_ID,
    trace_id: TRACE_ID,
  }
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('Requester', () => {
  it('reads raw JSON endpoints through the shared lifecycle', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse({ title: 'Lattix', nodes_count: 8 })),
    )
    vi.stubGlobal('fetch', fetchMock)

    const client = new Requester()
    const events: RequestLifecycleEvent[] = []
    client.subscribe((event) => events.push(event))

    await expect(
      client.getJSON<{ title: string; nodes_count: number }>('/api/sub/token/info', {
        traceId: TRACE_ID,
      }),
    ).resolves.toEqual({ title: 'Lattix', nodes_count: 8 })

    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0]?.[1]?.headers).toMatchObject({ 'X-Trace-ID': TRACE_ID })
    expect(events).toHaveLength(2)
    expect(events[0]).toMatchObject({ phase: 'start', method: 'GET' })
    expect(events[1]).toMatchObject({ phase: 'finish', method: 'GET' })
  })

  it('returns data from a valid envelope and emits a balanced lifecycle', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(envelope('OK', { username: 'admin' }))),
    )
    vi.stubGlobal('fetch', fetchMock)

    const client = new Requester()
    const events: RequestLifecycleEvent[] = []
    client.subscribe((event) => {
      events.push(event)
    })
    client.setCSRFToken('csrf-token')

    await expect(
      client.post<{ username: string }>(
        '/api/auth/login',
        { username: 'admin', password: 'secret' },
        { traceId: TRACE_ID, idempotencyKey: 'operation-key' },
      ),
    ).resolves.toEqual({ username: 'admin' })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe('/api/auth/login')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(JSON.stringify({ username: 'admin', password: 'secret' }))
    expect(init?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'Idempotency-Key': 'operation-key',
      'X-CSRF-Token': 'csrf-token',
      'X-Trace-ID': TRACE_ID,
    })
    expect(events).toHaveLength(2)
    expect(events[0]).toMatchObject({ phase: 'start', method: 'POST', traceId: TRACE_ID })
    expect(events[1]).toMatchObject({
      phase: 'finish',
      method: 'POST',
      requestId: events[0]?.requestId,
      traceId: TRACE_ID,
    })
    expect(events[0]?.requestId).toMatch(/^[0-9a-f]{32}$/)
    expect(events[1]?.error).toBeUndefined()
  })

  it('narrows an authentication envelope to a business error', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(envelope('AUTH_REQUIRED', null, 'login required'))),
    )
    vi.stubGlobal('fetch', fetchMock)

    const client = new Requester()
    const onUnauthorized = vi.fn()
    client.setUnauthorizedHandler(onUnauthorized)

    const request = client.get('/api/auth/me', undefined, { traceId: TRACE_ID })

    await expect(request).rejects.toMatchObject({
      name: 'RequestError',
      kind: 'business',
      code: 'AUTH_REQUIRED',
      message: 'login required',
      requestId: REQUEST_ID,
      traceId: TRACE_ID,
    })
    expect(onUnauthorized).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('rejects an unknown response code as an invalid protocol envelope', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(envelope('UNKNOWN_CODE'))),
    )
    vi.stubGlobal('fetch', fetchMock)

    const request = new Requester().get('/api/auth/me', undefined, { traceId: TRACE_ID })
    const error = await request.catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(RequestError)
    expect(error).toMatchObject({
      kind: 'protocol',
      code: 'INVALID_RESPONSE',
      httpStatus: 200,
      traceId: TRACE_ID,
    })
    expect((error as RequestError).requestId).toMatch(/^[0-9a-f]{32}$/)
    expect((error as RequestError).requestId).not.toBe(REQUEST_ID)
  })

  it('classifies HTTP protocol envelopes separately from business failures', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(envelope('HTTP_415', null, 'unsupported content type'), 415)),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(new Requester().get('/api/auth/me')).rejects.toMatchObject({
      kind: 'protocol',
      code: 'HTTP_415',
      httpStatus: 415,
      requestId: REQUEST_ID,
      traceId: TRACE_ID,
    })
  })

  it('normalizes an aborted request and closes its lifecycle', async () => {
    const fetchMock = vi.fn(
      (_input: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init?.signal
          if (!signal) throw new Error('missing request signal')
          const rejectAbort = () => reject(signal.reason)
          if (signal.aborted) rejectAbort()
          else signal.addEventListener('abort', rejectAbort, { once: true })
        }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const client = new Requester()
    const events: RequestLifecycleEvent[] = []
    client.subscribe((event) => {
      events.push(event)
    })
    const controller = new AbortController()

    const request = client.get('/api/auth/me', undefined, {
      signal: controller.signal,
      traceId: TRACE_ID,
    })
    controller.abort()

    await expect(request).rejects.toMatchObject({
      kind: 'cancelled',
      code: 'REQUEST_CANCELLED',
      traceId: TRACE_ID,
    })
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(events).toHaveLength(2)
    expect(events[1]?.error).toMatchObject({
      kind: 'cancelled',
      code: 'REQUEST_CANCELLED',
    })
  })

  it('retries a timed-out GET after a short recovery delay', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (fetchMock.mock.calls.length > 1) {
        return Promise.resolve(jsonResponse(envelope('OK', { username: 'admin' })))
      }
      return new Promise<Response>((_resolve, reject) => {
        const signal = init?.signal
        if (!signal) throw new Error('missing request signal')
        signal.addEventListener('abort', () => reject(signal.reason), { once: true })
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const request = new Requester().get<{ username: string }>('/api/auth/me', undefined, {
      timeoutMs: 100,
      traceId: TRACE_ID,
    })

    await vi.advanceTimersByTimeAsync(100)
    await vi.advanceTimersByTimeAsync(500)

    await expect(request).resolves.toEqual({ username: 'admin' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('postObserved resolves observeId from envelope', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(
        jsonResponse({
          ...envelope('OK', { id: 42 }),
          observe_id: '0123456789abcdef0123456789abcdef',
        }),
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const client = new Requester()
    client.setCSRFToken('csrf-token')

    await expect(
      client.postObserved<{ id: number }>(
        '/api/server/rebuild-xray',
        { server_id: 1 },
        {
          traceId: TRACE_ID,
        },
      ),
    ).resolves.toEqual({
      data: { id: 42 },
      observeId: '0123456789abcdef0123456789abcdef',
    })
    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe('/api/server/rebuild-xray')
    expect(init?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'Idempotency-Key': expect.stringMatching(/^[0-9a-f]{32}$/),
      'X-CSRF-Token': 'csrf-token',
    })
  })

  it('postObserved resolves undefined observeId when envelope lacks it', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(envelope('OK', { id: 42 }))),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await new Requester().postObserved<{ id: number }>('/api/user-group/create', {
      name: 'admin',
    })

    expect(result.data).toEqual({ id: 42 })
    expect(result.observeId).toBeUndefined()
  })
})
