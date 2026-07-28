import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import {
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
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
import { useAuth } from '@/lib/auth'
import { useRequestState } from '@/lib/request-state'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', label: '仪表盘', icon: LayoutDashboardIcon, end: true },
  { to: '/servers', label: '服务器', icon: ServerIcon, end: false },
  { to: '/chains', label: '链路', icon: RouteIcon, end: false },
  { to: '/users', label: '用户', icon: UsersIcon, end: false },
  { to: '/logs/operations', label: '日志', icon: ScrollTextIcon, end: false },
  { to: '/settings', label: '设置', icon: SettingsIcon, end: false },
]

export default function Layout() {
  const { username, logout } = useAuth()
  const { foregroundPendingCount } = useRequestState()
  const navigate = useNavigate()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)

  const onLogout = async () => {
    await logout()
    navigate('/login', { replace: true })
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
                {navItems.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.end}
                    onClick={() => setMobileNavOpen(false)}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center gap-3 rounded-lg border border-transparent px-3 py-2.5 text-sm text-sidebar-foreground/70 transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
                        isActive && 'border-sidebar-foreground/25 bg-sidebar-accent font-semibold text-sidebar-accent-foreground',
                      )
                    }
                  >
                    <item.icon className="size-4" />
                    {item.label}
                  </NavLink>
                ))}
              </nav>
              <div className="flex items-center justify-between gap-2 border-t border-sidebar-border p-3">
                <span className="truncate text-sm" title={username ?? ''}>{username}</span>
                <Button variant="ghost" size="sm" onClick={onLogout} className="text-sidebar-foreground hover:bg-sidebar-accent">
                  <LogOutIcon />
                  登出
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
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  'relative flex flex-col items-center gap-1.5 rounded-lg border border-transparent px-2 py-2.5 text-xs text-sidebar-foreground/65 transition-[color,background-color,transform] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground active:translate-y-px',
                  isActive && 'border-sidebar-foreground/20 bg-sidebar-accent font-semibold text-sidebar-accent-foreground before:absolute before:-left-2 before:top-2 before:bottom-2 before:w-1 before:rounded-r-full before:bg-sidebar-primary',
                )
              }
            >
              <item.icon className="size-5" strokeWidth={1.8} />
              {item.label}
            </NavLink>
          ))}
        </nav>
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
            aria-label={`${username ?? ''} 登出`}
          >
            <LogOutIcon />
          </Button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto p-4 md:p-6 lg:p-8">
        <div className="page-enter mx-auto w-full max-w-[1480px]">
          <Outlet />
        </div>
      </main>
      <UpdateOverlay />
    </div>
  )
}
