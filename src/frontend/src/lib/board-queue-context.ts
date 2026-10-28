import { createContext, useContext } from 'react'

import type { useBoardQueue } from './board-queue'

/** 控制台待处理队列的共享状态；全应用由 BoardQueueProvider 持有唯一实例。 */
export type BoardQueueState = ReturnType<typeof useBoardQueue>

export const BoardQueueContext = createContext<BoardQueueState | null>(null)

export function useBoardQueueContext(): BoardQueueState {
  const context = useContext(BoardQueueContext)
  if (!context) {
    throw new Error('useBoardQueueContext 必须在 BoardQueueProvider 内使用')
  }
  return context
}
