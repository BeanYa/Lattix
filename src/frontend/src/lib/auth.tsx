import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

import { api, setOnUnauthorized } from './api'

interface AuthState {
  username: string | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [username, setUsername] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setOnUnauthorized(() => setUsername(null))
    api
      .me()
      .then((u) => setUsername(u.username))
      .catch(() => {})
      .finally(() => setLoading(false))
    return () => setOnUnauthorized(null)
  }, [])

  const login = async (name: string, password: string) => {
    const res = await api.login(name, password)
    setUsername(res.username)
  }

  const logout = async () => {
    await api.logout().catch(() => {})
    setUsername(null)
  }

  return (
    <AuthContext.Provider value={{ username, loading, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth 必须在 AuthProvider 内使用')
  }
  return ctx
}
