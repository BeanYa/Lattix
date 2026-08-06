import type { components } from './api-contract.generated'

export type ObserveStage = components['schemas']['Observation']['stages'][number]
export type ObserveStatus = components['schemas']['Observation']['status']
export type Observation = components['schemas']['Observation']

export type OperationProgressContextValue = {
  showOperation: (opts: { observeId: string }) => void
}
