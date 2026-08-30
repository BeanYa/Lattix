import { useEffect, type RefObject } from 'react'

/**
 * usePolling 以固定间隔轮询加载：挂载时立即全量加载一次，之后每 intervalMs 静默刷新；
 * 卸载时中止在途请求，并递增 requestRef 使其回包被忽略。
 */
export function usePolling(
  load: (silent: boolean, signal?: AbortSignal) => Promise<void>,
  requestRef: RefObject<number>,
  intervalMs = 5000,
) {
  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    let timer: number | undefined
    const poll = async (initial: boolean) => {
      await load(!initial, controller.signal)
      if (!stopped) timer = window.setTimeout(() => void poll(false), intervalMs)
    }
    void poll(true)
    return () => {
      stopped = true
      requestRef.current += 1
      controller.abort()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [load, requestRef, intervalMs])
}
