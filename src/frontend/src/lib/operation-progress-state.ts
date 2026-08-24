import type { Observation } from './operation-progress'

export type ProgressPhase = 'idle' | 'running' | 'done' | 'failed' | 'lost'

export type ProgressState =
  | { phase: 'idle' }
  | { phase: 'running'; observeId: string; observation: Observation | null }
  | { phase: 'done'; observeId: string; observation: Observation; autoClose: boolean }
  | { phase: 'failed'; observeId: string; observation: Observation }
  | { phase: 'lost'; observeId: string }

export type ProgressEvent =
  | { type: 'show'; observeId: string }
  | { type: 'observe'; observation: Observation }
  | { type: 'lost' }
  | { type: 'close' }
  | { type: 'autoClose' }

export const INITIAL_PROGRESS_STATE: ProgressState = { phase: 'idle' }

const hasWarnings = (observation: Observation): boolean => (observation.warnings?.length ?? 0) > 0

export function progressTransition(prev: ProgressState, event: ProgressEvent): ProgressState {
  switch (event.type) {
    case 'show':
      return { phase: 'running', observeId: event.observeId, observation: null }
    case 'observe': {
      if (prev.phase !== 'running') return prev
      const { observation } = event
      if (observation.status === 'done') {
        return {
          phase: 'done',
          observeId: prev.observeId,
          observation,
          autoClose: !hasWarnings(observation),
        }
      }
      if (observation.status === 'failed') {
        return { phase: 'failed', observeId: prev.observeId, observation }
      }
      return { phase: 'running', observeId: prev.observeId, observation }
    }
    case 'lost':
      return prev.phase === 'running' ? { phase: 'lost', observeId: prev.observeId } : prev
    case 'autoClose':
      return prev.phase === 'done' && prev.autoClose ? { phase: 'idle' } : prev
    case 'close':
      return prev.phase === 'idle' ? prev : { phase: 'idle' }
  }
}
