import { createContext, useContext } from 'react'

export interface TimezoneState {
  /** 面板全局显示时区（IANA 名称）；空串 = 浏览器本地 */
  timezone: string
  /** 设置页保存后调用，重新拉取时区 */
  refresh: () => void
}

export const TimezoneContext = createContext<TimezoneState | null>(null)

export function useTimezone(): TimezoneState {
  const context = useContext(TimezoneContext)
  if (!context) {
    throw new Error('useTimezone 必须在 TimezoneProvider 内使用')
  }
  return context
}
