import { lazy, Suspense, type ReactElement } from 'react'
import { Redirect, Route, Router, Switch } from 'wouter'

import { AppDialogProvider } from '@/components/AppDialogProvider'
import { AuthProvider, useAuth } from '@/lib/auth'
import { ThemeProvider } from '@/lib/theme'
import { TimezoneProvider } from '@/lib/timezone'

const Layout = lazy(() => import('@/components/Layout'))
const Chains = lazy(() => import('@/pages/Chains'))
const Dashboard = lazy(() => import('@/pages/Dashboard'))
const Login = lazy(() => import('@/pages/Login'))
const LogsLayout = lazy(() => import('@/pages/LogsLayout'))
const OperationLogs = lazy(() => import('@/pages/OperationLogs'))
const RequestLogs = lazy(() => import('@/pages/RequestLogs'))
const Servers = lazy(() => import('@/pages/Servers'))
const Settings = lazy(() => import('@/pages/Settings'))
const Users = lazy(() => import('@/pages/Users'))

function SuspendedRoute({ children, fullPage = false }: {
  children: ReactElement
  fullPage?: boolean
}) {
  return (
    <Suspense
      fallback={(
        <div
          className={fullPage
            ? 'grid min-h-[100dvh] place-items-center bg-background p-4'
            : 'grid min-h-[40vh] place-items-center'}
          role="status"
        >
          <div className="game-panel flex items-center gap-3 px-5 py-4 text-sm text-muted-foreground">
            <span className="size-3 animate-pulse rounded-sm bg-primary" />
            正在加载页面...
          </div>
        </div>
      )}
    >
      {children}
    </Suspense>
  )
}

function RequireAuth({ children }: { children: ReactElement }) {
  const { username, loading } = useAuth()
  if (loading) {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center bg-background p-4">
        <div className="game-panel flex items-center gap-3 px-5 py-4 text-sm text-muted-foreground">
          <span className="size-3 animate-pulse rounded-sm bg-primary" />
          正在连接控制面板...
        </div>
      </div>
    )
  }
  if (!username) return <Redirect to="/login" replace />
  return children
}

function ProtectedRoutes() {
  return (
    <RequireAuth>
      <SuspendedRoute fullPage>
        <Layout>
          <Switch>
            <Route path="/">
              <SuspendedRoute><Dashboard /></SuspendedRoute>
            </Route>
            <Route path="/servers">
              <SuspendedRoute><Servers /></SuspendedRoute>
            </Route>
            <Route path="/chains">
              <SuspendedRoute><Chains /></SuspendedRoute>
            </Route>
            <Route path="/users">
              <SuspendedRoute><Users /></SuspendedRoute>
            </Route>
            <Route path="/logs/operations">
              <SuspendedRoute><LogsLayout><OperationLogs /></LogsLayout></SuspendedRoute>
            </Route>
            <Route path="/logs/requests">
              <SuspendedRoute><LogsLayout><RequestLogs /></LogsLayout></SuspendedRoute>
            </Route>
            <Route path="/logs">
              <Redirect to="/logs/operations" replace />
            </Route>
            <Route path="/settings">
              <SuspendedRoute><Settings /></SuspendedRoute>
            </Route>
            <Route>
              <Redirect to="/" replace />
            </Route>
          </Switch>
        </Layout>
      </SuspendedRoute>
    </RequireAuth>
  )
}

export default function App() {
  return (
    <Router>
      <ThemeProvider>
        <AppDialogProvider>
          <AuthProvider>
            <TimezoneProvider>
              <Switch>
                <Route path="/login">
                  <SuspendedRoute fullPage><Login /></SuspendedRoute>
                </Route>
                <Route><ProtectedRoutes /></Route>
              </Switch>
            </TimezoneProvider>
          </AuthProvider>
        </AppDialogProvider>
      </ThemeProvider>
    </Router>
  )
}
