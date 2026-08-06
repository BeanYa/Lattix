import { useCallback, useEffect, useMemo, useReducer, type ReactNode } from 'react'

import OperationProgress from '@/components/OperationProgress'
import { api, isRequestError } from '@/lib/api'
import { OperationProgressContext } from '@/lib/operation-progress-context'
import {
  INITIAL_PROGRESS_STATE,
  progressTransition,
} from '@/lib/operation-progress-state'

const POLL_INTERVAL_MS = 400
const AUTO_CLOSE_DELAY_MS = 1000

export function OperationProgressProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(progressTransition, INITIAL_PROGRESS_STATE)

  const showOperation = useCallback((opts: { observeId: string }) => {
    dispatch({ type: 'show', observeId: opts.observeId })
  }, [])

  const observeId = state.phase === 'running' ? state.observeId : null

  useEffect(() => {
    if (observeId === null) return
    let stopped = false

    const poll = async () => {
      try {
        const observation = await api.observeTask(observeId)
        if (stopped) return
        dispatch({ type: 'observe', observation })
      } catch (error) {
        if (stopped) return
        if (isRequestError(error) && error.httpStatus === 404) {
          dispatch({ type: 'lost' })
        }
      }
    }

    const timer = window.setInterval(poll, POLL_INTERVAL_MS)
    void poll()
    return () => {
      stopped = true
      window.clearInterval(timer)
    }
  }, [observeId])

  const autoClose = state.phase === 'done' && state.autoClose
  useEffect(() => {
    if (!autoClose) return
    const timer = window.setTimeout(() => dispatch({ type: 'autoClose' }), AUTO_CLOSE_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [autoClose])

  const close = useCallback(() => dispatch({ type: 'close' }), [])
  const value = useMemo(() => ({ showOperation }), [showOperation])

  return (
    <OperationProgressContext.Provider value={value}>
      {children}
      <OperationProgress state={state} onClose={close} />
    </OperationProgressContext.Provider>
  )
}
