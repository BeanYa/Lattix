import { useEffect, useState, type ReactNode } from 'react'

import { api, setOnUnauthorized } from './api'
import { AuthContext } from './auth-context'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [username, setUsername] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setOnUnauthorized(() => setUsername(null))
    api
      .me()
      .then((user) => setUsername(user.username))
      .catch(() => {})
      .finally(() => setLoading(false))
    return () => setOnUnauthorized(null)
  }, [])

  const login = async (name: string, password: string) => {
    const response = await api.login(name, password)
    setUsername(response.username)
  }

  const logout = async () => {
    await api.logout()
    setUsername(null)
  }

  return (
    <AuthContext.Provider value={{ username, loading, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}
