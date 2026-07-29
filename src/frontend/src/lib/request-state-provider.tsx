import { useEffect, useMemo, useState, type ReactNode } from 'react'

import { RequestStateContext } from './request-state-context'
import { requester, type RequestError, type RequestLifecycleEvent } from './requester'

export function RequestStateProvider({ children }: { children: ReactNode }) {
  const [active, setActive] = useState<Map<string, RequestLifecycleEvent>>(new Map())
  const [lastTransportError, setLastTransportError] = useState<RequestError | null>(null)

  useEffect(
    () =>
      requester.subscribe((event) => {
        setActive((current) => {
          const next = new Map(current)
          if (event.phase === 'start') next.set(event.requestId, event)
          else next.delete(event.requestId)
          return next
        })
        if (
          event.phase === 'finish' &&
          event.error &&
          (event.error.kind === 'transport' || event.error.kind === 'protocol')
        ) {
          setLastTransportError(event.error)
        }
      }),
    [],
  )

  const value = useMemo(
    () => ({
      pendingCount: active.size,
      foregroundPendingCount: Array.from(active.values()).filter(
        (event) => event.display === 'foreground',
      ).length,
      lastTransportError,
    }),
    [active, lastTransportError],
  )

  return <RequestStateContext.Provider value={value}>{children}</RequestStateContext.Provider>
}
