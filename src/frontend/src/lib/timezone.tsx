import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

import { api } from './api'
import { useAuth } from './auth'

interface TimezoneState {
  /** 面板全局显示时区（IANA 名称）；空串 = 浏览器本地 */
  timezone: string
  /** 设置页保存后调用，重新拉取时区 */
  refresh: () => void
}

const TimezoneContext = createContext<TimezoneState | null>(null)

export function TimezoneProvider({ children }: { children: ReactNode }) {
  const { username } = useAuth()
  const [timezone, setTimezone] = useState('')

  const refresh = useCallback(() => {
    api
      .settings()
      .then((s) => setTimezone(s.timezone))
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (username) {
      refresh()
    } else {
      setTimezone('')
    }
  }, [username, refresh])

  return <TimezoneContext.Provider value={{ timezone, refresh }}>{children}</TimezoneContext.Provider>
}

export function useTimezone(): TimezoneState {
  const ctx = useContext(TimezoneContext)
  if (!ctx) {
    throw new Error('useTimezone 必须在 TimezoneProvider 内使用')
  }
  return ctx
}
