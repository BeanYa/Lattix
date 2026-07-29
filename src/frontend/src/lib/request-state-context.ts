import { createContext, useContext } from 'react'

import type { RequestError } from './requester'

export interface RequestState {
  pendingCount: number
  foregroundPendingCount: number
  lastTransportError: RequestError | null
}

export const RequestStateContext = createContext<RequestState>({
  pendingCount: 0,
  foregroundPendingCount: 0,
  lastTransportError: null,
})

export function useRequestState(): RequestState {
  return useContext(RequestStateContext)
}
