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
import ParticleField from '@/components/ParticleField'
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

const navSections = [
  { label: '实时控制', items: navItems.slice(0, 4) },
  { label: '资源管理', items: navItems.slice(4, 8) },
  { label: '系统', items: navItems.slice(8) },
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
        'flex min-w-0 items-center gap-2 text-xs text-sidebar-foreground/65',
        compact && 'justify-center text-[10px]',
      )}
    >
      <span className={cn('relative size-2 shrink-0 rounded-full shadow-[0_0_10px_currentColor]', presentation.dot, snapshot?.state === 'updating' && 'animate-pulse')} />
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
    <div className="panel-canvas flex min-h-[100dvh] flex-col md:flex-row">
      <ParticleField />
      {foregroundPendingCount > 0 ? (
        <div
          role="status"
          aria-label="请求处理中"
          className="fixed inset-x-0 top-0 z-50 h-1 animate-pulse bg-primary"
        />
      ) : null}
      <header className="panel-sidebar flex h-15 shrink-0 items-center justify-between border-b border-sidebar-border px-4 text-sidebar-foreground md:hidden">
        <span className="flex items-center gap-3 font-semibold">
          <LattixMark className="size-8 shrink-0" />
          <span className="text-sm" aria-label="Lattix">LATTIX</span>
        </span>
        <div className="flex items-center gap-1">
          <PanelStateIndicator snapshot={panelState} />
          <ThemeToggle className="text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground" />
          <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
            <SheetTrigger render={<Button variant="ghost" size="icon" aria-label="打开导航菜单" />}>
              <MenuIcon />
            </SheetTrigger>
            <SheetContent side="left" className="panel-sidebar w-72 bg-sidebar p-0 text-sidebar-foreground" showCloseButton={false}>
              <SheetTitle className="flex items-center gap-3 border-b border-sidebar-border px-5 py-4 text-base font-semibold text-sidebar-foreground">
                <LattixMark className="size-8 shrink-0" />
                <span aria-label="Lattix">LATTIX</span>
              </SheetTitle>
              <nav className="flex-1 space-y-1 p-3" aria-label="主导航">
                {navItems.map((item) => {
                  const isActive = item.end ? location === item.to : location.startsWith(item.activePrefix)
                  return (
                    <Link
                      key={item.to}
                      href={item.to}
                      onClick={() => setMobileNavOpen(false)}
                      aria-current={isActive ? 'page' : undefined}
                      className={cn(
                        'panel-nav-item flex items-center gap-3 rounded-md border border-transparent px-3 py-2.5 text-sm text-sidebar-foreground/60 transition-[color,background-color,border-color,transform] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground active:translate-y-px',
                        isActive && 'border-sidebar-foreground/10 bg-sidebar-accent font-semibold text-sidebar-accent-foreground',
                      )}
                    >
                      <item.icon className="size-4" strokeWidth={1.8} />
                      {item.label}
                    </Link>
                  )
                })}
              </nav>
              <div className="grid gap-3 border-t border-sidebar-border p-3">
                <div className="flex items-center justify-between gap-2 rounded-md border border-sidebar-border bg-sidebar-accent/50 px-3 py-2">
                  <span className="truncate text-sm" title={username ?? ''}>{username}</span>
                  <PanelStateIndicator snapshot={panelState} />
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={onLogout}
                  disabled={loggingOut}
                  className="w-full text-sidebar-foreground/70 hover:bg-sidebar-accent"
                >
                  <LogOutIcon />
                  {loggingOut ? '登出中…' : '登出'}
                </Button>
              </div>
            </SheetContent>
          </Sheet>
        </div>
      </header>
      <aside className="panel-sidebar hidden w-[228px] shrink-0 flex-col border-r border-sidebar-border text-sidebar-foreground md:flex">
        <div className="flex h-[78px] items-center gap-3 border-b border-sidebar-border px-5">
          <span className="brand-mark-shell"><LattixMark className="size-8 shrink-0" /></span>
          <div className="min-w-0">
            <strong className="block text-[13px] font-semibold" aria-label="Lattix">LATTIX</strong>
            <span className="block text-[9px] text-sidebar-foreground/38">KNOWLEDGE NETWORK OS</span>
          </div>
        </div>
        <nav className="no-scrollbar flex-1 overflow-y-auto px-3 py-4" aria-label="主导航">
          {navSections.map((section) => (
            <div key={section.label} className="mb-5 last:mb-0">
              <div className="panel-section-label mb-2 px-3">{section.label}</div>
              <div className="space-y-1">
                {section.items.map((item) => {
                  const isActive = item.end ? location === item.to : location.startsWith(item.activePrefix)
                  return (
                    <Link
                      key={item.to}
                      href={item.to}
                      aria-current={isActive ? 'page' : undefined}
                      className={cn(
                        'panel-nav-item flex h-10 items-center gap-3 rounded-md border border-transparent px-3 text-xs text-sidebar-foreground/58 transition-[color,background-color,border-color,transform] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground active:translate-y-px',
                        isActive && 'border-sidebar-foreground/10 bg-sidebar-accent font-semibold text-sidebar-accent-foreground',
                      )}
                    >
                      <item.icon className="size-4 shrink-0" strokeWidth={1.7} />
                      <span className="truncate">{item.label}</span>
                    </Link>
                  )
                })}
              </div>
            </div>
          ))}
        </nav>
        <div className="border-t border-sidebar-border px-4 py-3">
          <div className="flex items-center justify-between gap-3">
            <span className="text-[10px] text-sidebar-foreground/40">PANEL STATUS</span>
            <PanelStateIndicator snapshot={panelState} compact />
          </div>
        </div>
        <div className="border-t border-sidebar-border p-3">
          <div className="flex items-center gap-2 rounded-md border border-sidebar-border bg-sidebar-accent/40 p-2">
            <span className="grid size-8 shrink-0 place-items-center rounded-sm bg-sidebar-primary text-xs font-bold text-sidebar-primary-foreground">
              {(username ?? 'A').slice(0, 1).toUpperCase()}
            </span>
            <span className="min-w-0 flex-1 truncate text-xs" title={username ?? ''}>{username}</span>
            <ThemeToggle className="size-8 text-sidebar-foreground/55 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground" />
            <Button
              variant="ghost"
              size="icon"
              className="size-8 text-sidebar-foreground/55 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
              onClick={onLogout}
              disabled={loggingOut}
              aria-label={`${username ?? ''} 登出`}
              title="登出"
            >
              <LogOutIcon />
            </Button>
          </div>
        </div>
      </aside>
      <main id="main-content" className="panel-main min-w-0 flex-1 overflow-auto p-3 md:p-4 lg:p-5">
        {logoutError ? (
          <div
            role="alert"
            className="mx-auto mb-4 w-full max-w-[1540px] rounded-md border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive"
          >
            登出失败：{logoutError}
          </div>
        ) : null}
        <div className="page-enter mx-auto w-full max-w-[1680px]">
          {children}
        </div>
      </main>
      <UpdateOverlay />
    </div>
  )
}
