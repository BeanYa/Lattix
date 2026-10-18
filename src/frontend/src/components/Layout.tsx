import { useEffect, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import {
  ActivityIcon,
  CoinsIcon,
  FileCode2Icon,
  GlobeIcon,
  Layers as LayersIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
  RouteIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  UsersIcon,
} from 'lucide-react'

import { Sheet, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import LattixMark from '@/components/LattixMark'
import ThemeToggle from '@/components/ThemeToggle'
import UpdateOverlay from '@/components/UpdateOverlay'
import { OperationProgressProvider } from '@/components/OperationProgressProvider'
import { api, errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useRequestState } from '@/lib/request-state'
import type { PanelLifecycleSnapshot, PanelLifecycleState } from '@/lib/types'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', activePrefix: '/', label: '仪表盘', icon: LayoutDashboardIcon, end: true },
  { to: '/runtime', activePrefix: '/runtime', label: '运行监控', icon: ActivityIcon, end: false },
  { to: '/servers', activePrefix: '/servers', label: '服务器', icon: ServerIcon, end: false },
  { to: '/costs', activePrefix: '/costs', label: '成本统计', icon: CoinsIcon, end: false },
  { to: '/chains', activePrefix: '/chains', label: '链路', icon: RouteIcon, end: false },
  { to: '/users', activePrefix: '/users', label: '用户', icon: UsersIcon, end: false },
  { to: '/groups', activePrefix: '/groups', label: '分组', icon: LayersIcon, end: false },
  {
    to: '/subscription-templates',
    activePrefix: '/subscription-templates',
    label: '订阅模板',
    icon: FileCode2Icon,
    end: false,
  },
  {
    to: '/external-subscriptions',
    activePrefix: '/external-subscriptions',
    label: '外部订阅',
    icon: GlobeIcon,
    end: false,
  },
  {
    to: '/logs/operations',
    activePrefix: '/logs',
    label: '日志',
    icon: ScrollTextIcon,
    end: false,
  },
  { to: '/settings', activePrefix: '/settings', label: '设置', icon: SettingsIcon, end: false },
]

const navSections = [
  { label: '实时控制', code: 'LIVE OPS', items: navItems.slice(0, 5) },
  { label: '资源管理', code: 'RESOURCES', items: navItems.slice(5, 10) },
  { label: '系统', code: 'SYSTEM', items: navItems.slice(10) },
]

const panelStateTone: Record<PanelLifecycleState, { label: string; className: string }> = {
  startup: { label: 'STARTUP', className: 'is-muted' },
  active: { label: 'ACTIVE', className: 'is-lime' },
  updating: { label: 'UPDATING', className: 'is-blue' },
  faulted: { label: 'FAULTED', className: 'is-red' },
}

function PanelStateBadge({ snapshot }: { snapshot: PanelLifecycleSnapshot | null }) {
  const tone = snapshot
    ? panelStateTone[snapshot.state]
    : { label: 'OFFLINE', className: 'is-muted' }
  const title = snapshot?.fault ? `Panel ${tone.label}: ${snapshot.fault}` : `Panel ${tone.label}`

  return (
    <span role="status" title={title} className={cn('cg-status', tone.className)}>
      {tone.label}
    </span>
  )
}

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const [location] = useLocation()
  return (
    <>
      {navSections.map((section) => (
        <div key={section.label} className="cg-nav-section">
          <div className="cg-nav-section-label">
            {section.label}
            <span className="cg-nav-section-code">{section.code}</span>
          </div>
          <div className="cg-nav-list">
            {section.items.map((item) => {
              const isActive = item.end
                ? location === item.to
                : location.startsWith(item.activePrefix)
              return (
                <Link
                  key={item.to}
                  href={item.to}
                  onClick={onNavigate}
                  aria-current={isActive ? 'page' : undefined}
                  className={cn('cg-nav-item', isActive && 'is-active')}
                >
                  <item.icon strokeWidth={1.8} />
                  <span className="truncate">{item.label}</span>
                </Link>
              )
            })}
          </div>
        </div>
      ))}
    </>
  )
}

export default function Layout({ children }: { children: ReactNode }) {
  const { username, logout } = useAuth()
  const { foregroundPendingCount } = useRequestState()
  const [, navigate] = useLocation()
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

  const logoutButton = (
    <button
      type="button"
      className="cg-icon-button"
      onClick={onLogout}
      disabled={loggingOut}
      aria-label={`${username ?? ''} 登出`}
      title={loggingOut ? '登出中…' : '登出'}
    >
      <LogOutIcon />
    </button>
  )

  return (
    <OperationProgressProvider>
      <div className="cg-shell">
        {foregroundPendingCount > 0 ? (
          <div
            role="status"
            aria-label="请求处理中"
            className="fixed inset-x-0 top-0 z-50 h-0.5 animate-pulse"
            style={{ background: 'var(--panel-accent)' }}
          />
        ) : null}

        <header className="cg-commandbar">
          <Link href="/" className="cg-commandbar-brand" aria-label="Lattix 仪表盘">
            <span className="cg-commandbar-mark">
              <LattixMark className="size-4 shrink-0" />
            </span>
            <span className="min-w-0">
              <span className="cg-commandbar-name" aria-label="Lattix">
                LATTIX
              </span>
              <span className="cg-commandbar-sub">NETWORK CONTROL PANEL</span>
            </span>
          </Link>

          <div className="cg-commandbar-meta">
            <PanelStateBadge snapshot={panelState} />
            <span className="cg-commandbar-date">
              {new Intl.DateTimeFormat('zh-CN', {
                month: '2-digit',
                day: '2-digit',
                weekday: 'short',
              }).format(new Date())}
            </span>
          </div>

          <span className="cg-commandbar-spacer" />

          <div className="cg-commandbar-actions">
            <ThemeToggle />
            <div className="cg-commandbar-user">
              <span className="cg-commandbar-avatar">
                {(username ?? 'A').slice(0, 1).toUpperCase()}
              </span>
              <span className="cg-commandbar-username" title={username ?? ''}>
                {username}
              </span>
              {logoutButton}
            </div>
            <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
              <SheetTrigger
                render={
                  <button
                    type="button"
                    className="cg-icon-button cg-menu-trigger"
                    aria-label="打开导航菜单"
                  />
                }
              >
                <MenuIcon />
              </SheetTrigger>
              <SheetContent
                side="left"
                showCloseButton={false}
                className="cg-sheet w-64 gap-0 border-r border-[var(--panel-rule)] bg-[var(--panel-surface)] p-0 text-[var(--panel-ink)]"
              >
                <SheetTitle className="cg-sidebar-brand">
                  <span className="cg-sidebar-brand-icon">
                    <LattixMark className="size-5 shrink-0" />
                  </span>
                  <span>
                    <span className="cg-sidebar-brand-name" aria-label="Lattix">
                      LATTIX
                    </span>
                    <span className="cg-sidebar-brand-sub">NETWORK CONTROL PANEL</span>
                  </span>
                </SheetTitle>
                <nav className="cg-sidebar-nav" aria-label="主导航">
                  <NavList onNavigate={() => setMobileNavOpen(false)} />
                </nav>
                <div className="cg-sidebar-footer">
                  <div className="cg-sidebar-user">
                    <span className="cg-sidebar-avatar">
                      {(username ?? 'A').slice(0, 1).toUpperCase()}
                    </span>
                    <span className="cg-sidebar-username" title={username ?? ''}>
                      {username}
                    </span>
                    {logoutButton}
                  </div>
                </div>
              </SheetContent>
            </Sheet>
          </div>
        </header>

        <div className="cg-body">
          <aside className="cg-sidebar">
            <nav className="cg-sidebar-nav no-scrollbar" aria-label="主导航">
              <NavList />
            </nav>
            <div className="cg-sidebar-footer">
              <div className="cg-sidebar-state">
                <span className="cg-micro">PANEL</span>
                <PanelStateBadge snapshot={panelState} />
              </div>
              <p className="cg-sidebar-note">NETWORK CONTROL / SELF-HOSTED</p>
            </div>
          </aside>

          <main id="main-content" className="cg-main">
            {logoutError ? (
              <div role="alert" className="cg-main-inner" style={{ paddingBottom: 0 }}>
                <div className="cg-semantic-card is-bad">
                  <header>登出失败</header>
                  <div className="cg-semantic-body">{logoutError}</div>
                </div>
              </div>
            ) : null}
            <div className="cg-main-inner cg-page-in">{children}</div>
          </main>
        </div>
        <UpdateOverlay />
      </div>
    </OperationProgressProvider>
  )
}
