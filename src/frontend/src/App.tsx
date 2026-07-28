import type { ReactElement } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'

import { AppDialogProvider } from '@/components/AppDialogProvider'
import Layout from '@/components/Layout'
import { AuthProvider, useAuth } from '@/lib/auth'
import { ThemeProvider } from '@/lib/theme'
import { TimezoneProvider } from '@/lib/timezone'
import Chains from '@/pages/Chains'
import Dashboard from '@/pages/Dashboard'
import Login from '@/pages/Login'
import LogsLayout from '@/pages/LogsLayout'
import OperationLogs from '@/pages/OperationLogs'
import RequestLogs from '@/pages/RequestLogs'
import Servers from '@/pages/Servers'
import Settings from '@/pages/Settings'
import Users from '@/pages/Users'

function RequireAuth({ children }: { children: ReactElement }) {
  const { username, loading } = useAuth()
  if (loading) {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center bg-background p-4">
        <div className="game-panel flex items-center gap-3 px-5 py-4 text-sm text-muted-foreground">
          <span className="size-3 animate-pulse rounded-sm bg-primary" />
          正在连接控制面板…
        </div>
      </div>
    )
  }
  if (!username) {
    return <Navigate to="/login" replace />
  }
  return children
}

export default function App() {
  return (
    <BrowserRouter>
      <ThemeProvider>
        <AppDialogProvider>
          <AuthProvider>
            <TimezoneProvider>
              <Routes>
              <Route path="/login" element={<Login />} />
              <Route
                element={
                  <RequireAuth>
                    <Layout />
                  </RequireAuth>
                }
              >
                <Route index element={<Dashboard />} />
                <Route path="servers" element={<Servers />} />
                <Route path="chains" element={<Chains />} />
                <Route path="users" element={<Users />} />
                <Route path="logs" element={<LogsLayout />}>
                  <Route index element={<Navigate to="operations" replace />} />
                  <Route path="operations" element={<OperationLogs />} />
                  <Route path="requests" element={<RequestLogs />} />
                </Route>
                <Route path="settings" element={<Settings />} />
              </Route>
              <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </TimezoneProvider>
          </AuthProvider>
        </AppDialogProvider>
      </ThemeProvider>
    </BrowserRouter>
  )
}
