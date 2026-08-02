import { useEffect, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import {
  CoinsIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
  FileCode2Icon,
  GlobeIcon,
  RouteIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  UsersIcon,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import LattixMark from '@/components/LattixMark'
import ThemeToggle from '@/components/ThemeToggle'
import UpdateOverlay from '@/components/UpdateOverlay'
import { api, errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useRequestState } from '@/lib/request-state'
import type { PanelLifecycleSnapshot, PanelLifecycleState } from '@/lib/types'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', activePrefix: '/', label: '仪表盘', icon: LayoutDashboardIcon, end: true },
  { to: '/servers', activePrefix: '/servers', label: '服务器', icon: ServerIcon, end: false },
  { to: '/costs', activePrefix: '/costs', label: '成本统计', icon: CoinsIcon, end: false },
  { to: '/chains', activePrefix: '/chains', label: '链路', icon: RouteIcon, end: false },
  { to: '/users', activePrefix: '/users', label: '用户', icon: UsersIcon, end: false },
  { to: '/subscription-templates', activePrefix: '/subscription-templates', label: '订阅模板', icon: FileCode2Icon, end: false },
  { to: '/external-subscriptions', activePrefix: '/external-subscriptions', label: '外部订阅', icon: GlobeIcon, end: false },
  { to: '/logs/operations', activePrefix: '/logs', label: '日志', icon: ScrollTextIcon, end: false },
  { to: '/settings', activePrefix: '/settings', label: '设置', icon: SettingsIcon, end: false },
]

const panelStatePresentation: Record<PanelLifecycleState, { label: string; dot: string }> = {
  startup: { label: '启动中', dot: 'bg-primary' },
  active: { label: '正常', dot: 'bg-success' },
  updating: { label: '更新中', dot: 'bg-warning' },
  faulted: { label: '故障', dot: 'bg-destructive' },
}

function PanelStateIndicator({ snapshot, compact = false }: {
  snapshot: PanelLifecycleSnapshot | null
  compact?: boolean
}) {
  const presentation = snapshot
    ? panelStatePresentation[snapshot.state]
    : { label: '不可用', dot: 'bg-muted-foreground' }
  const title = snapshot?.fault
    ? `Panel ${presentation.label}: ${snapshot.fault}`
    : `Panel ${presentation.label}`

  return (
    <div
      role="status"
      title={title}
      className={cn(
        'flex items-center gap-2 font-heading text-xs text-sidebar-foreground/70 [font-synthesis:none]',
        compact && 'flex-col gap-1 text-[10px]',
      )}
    >
      <span className={cn('size-2 shrink-0 rounded-full', presentation.dot, snapshot?.state === 'updating' && 'animate-pulse')} />
      <span className="truncate">{presentation.label}</span>
    </div>
  )
}

export default function Layout({ children }: { children: ReactNode }) {
  const { username, logout } = useAuth()
  const { foregroundPendingCount } = useRequestState()
  const [location, navigate] = useLocation()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [panelState, setPanelState] = useState<PanelLifecycleSnapshot | null>(null)
  const [loggingOut, setLoggingOut] = useState(false)
  const [logoutError, setLogoutError] = useState('')

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const snapshot = await api.panelState()
        if (active) setPanelState(snapshot)
      } catch {
        if (active) setPanelState(null)
      }
    }
    void refresh()
    const timer = window.setInterval(refresh, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  const onLogout = async () => {
    setLoggingOut(true)
    setLogoutError('')
    try {
      await logout()
      navigate('/login', { replace: true })
    } catch (error) {
      setMobileNavOpen(false)
      setLogoutError(errorMessage(error))
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <div className="flex min-h-[100dvh] flex-col bg-background md:flex-row">
      {foregroundPendingCount > 0 ? (
        <div
          role="status"
          aria-label="请求处理中"
          className="fixed inset-x-0 top-0 z-50 h-1 animate-pulse bg-primary"
        />
      ) : null}
      <header className="flex h-16 shrink-0 items-center justify-between border-b border-sidebar-border bg-sidebar px-4 text-sidebar-foreground md:hidden">
        <span className="flex items-center gap-3 font-heading font-normal tracking-normal">
          <LattixMark className="size-9 shrink-0" />
          <span aria-label="Lattix">LATTIX</span>
        </span>
        <div className="flex items-center gap-1">
          <PanelStateIndicator snapshot={panelState} />
          <ThemeToggle className="text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground" />
          <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
            <SheetTrigger render={<Button variant="ghost" size="icon" aria-label="打开导航菜单" />}>
              <MenuIcon />
            </SheetTrigger>
            <SheetContent side="left" className="w-72 bg-sidebar p-0 text-sidebar-foreground" showCloseButton={false}>
              <SheetTitle className="flex items-center gap-3 border-b border-sidebar-border px-5 py-4 text-lg font-normal tracking-normal text-sidebar-foreground">
                <LattixMark className="size-9 shrink-0" />
                <span aria-label="Lattix">LATTIX</span>
              </SheetTitle>
              <nav className="flex-1 space-y-1 p-3">
                {navItems.map((item) => {
                  const isActive = item.end ? location === item.to : location.startsWith(item.activePrefix)
                  return (
                    <Link
                      key={item.to}
                      href={item.to}
                      onClick={() => setMobileNavOpen(false)}
                      className={cn(
                        'flex items-center gap-3 rounded-lg border border-transparent px-3 py-2.5 text-sm text-sidebar-foreground/70 transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
                        isActive && 'border-sidebar-foreground/25 bg-sidebar-accent font-semibold text-sidebar-accent-foreground',
                      )}
                    >
                      <item.icon className="size-4" />
                      {item.label}
                    </Link>
                  )
                })}
              </nav>
              <div className="flex items-center justify-between gap-2 border-t border-sidebar-border p-3">
                <span className="truncate text-sm" title={username ?? ''}>{username}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={onLogout}
                  disabled={loggingOut}
                  className="text-sidebar-foreground hover:bg-sidebar-accent"
                >
                  <LogOutIcon />
                  {loggingOut ? '登出中…' : '登出'}
                </Button>
              </div>
            </SheetContent>
          </Sheet>
        </div>
      </header>
      <aside className="hidden w-24 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-20 items-center justify-center border-b border-sidebar-border">
          <LattixMark className="size-11" />
        </div>
        <nav className="flex-1 space-y-2 px-2 py-4">
          {navItems.map((item) => {
            const isActive = item.end ? location === item.to : location.startsWith(item.activePrefix)
            return (
              <Link
                key={item.to}
                href={item.to}
                className={cn(
                  'relative flex flex-col items-center gap-1.5 rounded-lg border border-transparent px-2 py-2.5 text-xs text-sidebar-foreground/65 transition-[color,background-color,transform] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground active:translate-y-px',
                  isActive && 'border-sidebar-foreground/20 bg-sidebar-accent font-semibold text-sidebar-accent-foreground before:absolute before:-left-2 before:top-2 before:bottom-2 before:w-1 before:rounded-r-full before:bg-sidebar-primary',
                )}
              >
                <item.icon className="size-5" strokeWidth={1.8} />
                {item.label}
              </Link>
            )
          })}
        </nav>
        <div className="border-t border-sidebar-border py-3">
          <PanelStateIndicator snapshot={panelState} compact />
        </div>
        <div className="border-t border-sidebar-border p-2">
          <ThemeToggle className="mb-2 w-full text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground" />
          <div className="mb-2 grid place-items-center">
            <span className="grid size-9 place-items-center rounded-full bg-sidebar-primary text-xs font-bold text-sidebar-primary-foreground">
              {(username ?? 'A').slice(0, 1).toUpperCase()}
            </span>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="w-full text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            onClick={onLogout}
            disabled={loggingOut}
            aria-label={`${username ?? ''} 登出`}
          >
            <LogOutIcon />
          </Button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto p-4 md:p-6 lg:p-8">
        {logoutError ? (
          <div
            role="alert"
            className="mx-auto mb-4 w-full max-w-[1480px] rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive"
          >
            登出失败：{logoutError}
          </div>
        ) : null}
        <div className="page-enter mx-auto w-full max-w-[1480px]">
          {children}
        </div>
      </main>
      <UpdateOverlay />
    </div>
  )
}
