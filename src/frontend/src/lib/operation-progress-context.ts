import { createContext, useContext } from 'react'

import type { OperationProgressContextValue } from './operation-progress'

export const OperationProgressContext = createContext<OperationProgressContextValue | null>(null)

export function useOperationProgress(): OperationProgressContextValue {
  const context = useContext(OperationProgressContext)
  if (!context) {
    throw new Error('useOperationProgress must be used within OperationProgressProvider')
  }
  return context
}
