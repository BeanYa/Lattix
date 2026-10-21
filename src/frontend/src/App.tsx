import { lazy, Suspense, type ReactElement } from 'react'
import { Link, Redirect, Route, Router, Switch } from 'wouter'

import { AppDialogProvider } from '@/components/AppDialogProvider'
import { AuthProvider, useAuth } from '@/lib/auth'
import { ThemeProvider } from '@/lib/theme'
import { TimezoneProvider } from '@/lib/timezone'

// AppShell 是具名导出（同时保留 default），lazy 需要显式映射到 default。
const AppShell = lazy(() =>
  import('@/components/AppShell').then((module) => ({ default: module.AppShell })),
)
const Board = lazy(() => import('@/pages/Board'))
const Chains = lazy(() => import('@/pages/Chains'))
const Costs = lazy(() => import('@/pages/Costs'))
const ExternalSubscriptions = lazy(() => import('@/pages/ExternalSubscriptions'))
const Groups = lazy(() => import('@/pages/Groups'))
const Login = lazy(() => import('@/pages/Login'))
const LogsLayout = lazy(() => import('@/pages/LogsLayout'))
const OperationLogs = lazy(() => import('@/pages/OperationLogs'))
const RequestLogs = lazy(() => import('@/pages/RequestLogs'))
const RuntimeMonitor = lazy(() => import('@/pages/RuntimeMonitor'))
const Servers = lazy(() => import('@/pages/Servers'))
const Settings = lazy(() => import('@/pages/Settings'))
const Subscription = lazy(() => import('@/pages/Subscription'))
const SubscriptionTemplates = lazy(() => import('@/pages/SubscriptionTemplates'))
const Users = lazy(() => import('@/pages/Users'))

function SuspendedRoute({
  children,
  fullPage = false,
}: {
  children: ReactElement
  fullPage?: boolean
}) {
  return (
    <Suspense
      fallback={
        <div
          className={
            fullPage
              ? 'grid min-h-[100dvh] place-items-center p-4'
              : 'grid min-h-[40vh] place-items-center'
          }
          role="status"
        >
          <div className="cg-card-raised flex items-center gap-3 px-5 py-4">
            <span className="size-3 animate-pulse rounded-sm border-2 border-border bg-primary" />
            <span className="cg-micro text-muted-foreground">正在加载页面...</span>
          </div>
        </div>
      }
    >
      {children}
    </Suspense>
  )
}

function RequireAuth({ children }: { children: ReactElement }) {
  const { username, loading } = useAuth()
  if (loading) {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center p-4">
        <div className="cg-card-raised flex items-center gap-3 px-5 py-4">
          <span className="size-3 animate-pulse rounded-sm border-2 border-border bg-primary" />
          <span className="cg-micro text-muted-foreground">正在连接控制面板...</span>
        </div>
      </div>
    )
  }
  if (!username) return <Redirect to="/login" replace />
  return children
}

/**
 * 旧路由兼容：wouter 3.10 的 <Redirect> 只接收字符串，这里显式拼接
 * window.location.search，确保 ?tab=、?severity=、?view= 等查询参数不丢失。
 */
function Moved({ to }: { to: string }) {
  return <Redirect to={`${to}${window.location.search}`} replace />
}

/** 未匹配路径原地渲染 404，不再静默重定向到首页。 */
function NotFound() {
  return (
    <section
      aria-labelledby="not-found-title"
      className="mx-auto flex min-h-[60vh] max-w-xl flex-col justify-center gap-3 py-10"
    >
      <p className="font-mono text-[11px] font-semibold tracking-[0.12em] text-ink-3">
        404 / NOT FOUND
      </p>
      <h1 id="not-found-title" className="text-2xl font-bold tracking-[-0.02em] text-ink">
        页面不存在
      </h1>
      <p className="max-w-md text-sm leading-6 text-ink-2">
        这个地址可能已经移动或输入有误。请返回控制台，或从左侧导航重新选择入口。
      </p>
      <Link
        href="/board"
        className="mt-3 inline-flex min-h-11 w-fit items-center rounded-[2px] bg-signal px-4 text-sm font-semibold text-signal-ink transition-[filter] hover:brightness-105 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]"
      >
        返回控制台
      </Link>
    </section>
  )
}

function ProtectedRoutes() {
  return (
    <RequireAuth>
      <SuspendedRoute fullPage>
        <AppShell>
          <Switch>
            {/* 旧路由：全部 replace 到新 IA，并由 Moved 保留 query。 */}
            <Route path="/">
              <Moved to="/board" />
            </Route>
            <Route path="/servers">
              <Moved to="/network/servers" />
            </Route>
            <Route path="/chains">
              <Moved to="/network/chains" />
            </Route>
            <Route path="/groups">
              <Moved to="/network/link-groups" />
            </Route>
            <Route path="/users">
              <Moved to="/access/users" />
            </Route>
            <Route path="/subscription-templates">
              <Moved to="/access/templates" />
            </Route>
            <Route path="/external-subscriptions">
              <Moved to="/access/external-subscriptions" />
            </Route>
            <Route path="/runtime">
              <Moved to="/run/console" />
            </Route>
            <Route path="/costs">
              <Moved to="/run/costs" />
            </Route>
            <Route path="/logs">
              <Moved to="/run/logs/operations" />
            </Route>
            <Route path="/logs/operations">
              <Moved to="/run/logs/operations" />
            </Route>
            <Route path="/logs/requests">
              <Moved to="/run/logs/requests" />
            </Route>

            {/* 域默认页：直接访问 /network 等时落到该域默认二级页。 */}
            <Route path="/network">
              <Moved to="/network/servers" />
            </Route>
            <Route path="/access">
              <Moved to="/access/users" />
            </Route>
            <Route path="/run">
              <Moved to="/run/console" />
            </Route>
            <Route path="/run/logs">
              <Moved to="/run/logs/operations" />
            </Route>

            {/* 控制台 */}
            <Route path="/board">
              <SuspendedRoute>
                <Board />
              </SuspendedRoute>
            </Route>

            {/* 网络域 */}
            <Route path="/network/servers">
              <SuspendedRoute>
                <Servers />
              </SuspendedRoute>
            </Route>
            <Route path="/network/chains">
              <SuspendedRoute>
                <Chains />
              </SuspendedRoute>
            </Route>
            {/* 顺序要求：new 与 :id/edit 必须先于 :id，避免被详情路由截获。 */}
            <Route path="/network/chains/new">
              <SuspendedRoute>
                <Chains />
              </SuspendedRoute>
            </Route>
            <Route path="/network/chains/:id/edit">
              <SuspendedRoute>
                <Chains />
              </SuspendedRoute>
            </Route>
            {/* P2 再拆分：链路详情与独立节点详情暂由列表页承载深链接。 */}
            <Route path="/network/chains/:id">
              <SuspendedRoute>
                <Chains />
              </SuspendedRoute>
            </Route>
            <Route path="/network/nodes/:id">
              <SuspendedRoute>
                <Chains />
              </SuspendedRoute>
            </Route>
            <Route path="/network/link-groups">
              <SuspendedRoute>
                <Groups />
              </SuspendedRoute>
            </Route>

            {/* 访问域 */}
            <Route path="/access/users">
              <SuspendedRoute>
                <Users />
              </SuspendedRoute>
            </Route>
            {/* 旧「分组」页的两个 tab：链路分组归网络，用户分组归访问；P3 再拆页。 */}
            <Route path="/access/user-groups">
              <SuspendedRoute>
                <Groups />
              </SuspendedRoute>
            </Route>
            <Route path="/access/templates">
              <SuspendedRoute>
                <SubscriptionTemplates />
              </SuspendedRoute>
            </Route>
            <Route path="/access/external-subscriptions">
              <SuspendedRoute>
                <ExternalSubscriptions />
              </SuspendedRoute>
            </Route>

            {/* 运行域 */}
            <Route path="/run/console">
              <SuspendedRoute>
                <RuntimeMonitor />
              </SuspendedRoute>
            </Route>
            <Route path="/run/costs">
              <SuspendedRoute>
                <Costs />
              </SuspendedRoute>
            </Route>
            <Route path="/run/logs/operations">
              <SuspendedRoute>
                <LogsLayout>
                  <OperationLogs />
                </LogsLayout>
              </SuspendedRoute>
            </Route>
            <Route path="/run/logs/requests">
              <SuspendedRoute>
                <LogsLayout>
                  <RequestLogs />
                </LogsLayout>
              </SuspendedRoute>
            </Route>

            {/* /settings 是旧入口：先 replace 到默认分区并保留 query；其余分区直接渲染。 */}
            <Route path="/settings">
              <Moved to="/settings/agent" />
            </Route>
            <Route path="/settings/:section">
              <SuspendedRoute>
                <Settings />
              </SuspendedRoute>
            </Route>

            <Route>
              <NotFound />
            </Route>
          </Switch>
        </AppShell>
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
                  <SuspendedRoute fullPage>
                    <Login />
                  </SuspendedRoute>
                </Route>
                <Route path="/sub/:token">
                  <SuspendedRoute fullPage>
                    <Subscription />
                  </SuspendedRoute>
                </Route>
                <Route>
                  <ProtectedRoutes />
                </Route>
              </Switch>
            </TimezoneProvider>
          </AuthProvider>
        </AppDialogProvider>
      </ThemeProvider>
    </Router>
  )
}
