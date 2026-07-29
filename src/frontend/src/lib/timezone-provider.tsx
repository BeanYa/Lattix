import { useCallback, useEffect, useState, type ReactNode } from 'react'

import { api } from './api'
import { useAuth } from './auth-context'
import { TimezoneContext } from './timezone-context'

export function TimezoneProvider({ children }: { children: ReactNode }) {
  const { username } = useAuth()
  const [timezone, setTimezone] = useState('')

  const refresh = useCallback(() => {
    api
      .settings()
      .then((settings) => setTimezone(settings.timezone))
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
