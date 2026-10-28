import { type ReactNode } from 'react'

import { BoardQueueContext } from './board-queue-context'
import { useBoardQueue } from './board-queue'

/**
 * 全应用唯一的控制台队列轮询实例：壳层角标与控制台页共用同一份数据，
 * 任一侧 refresh（进入控制台、手动刷新、tab 回前台）后两侧立即一致。
 */
export function BoardQueueProvider({ children }: { children: ReactNode }) {
  const queue = useBoardQueue()
  return <BoardQueueContext.Provider value={queue}>{children}</BoardQueueContext.Provider>
}
