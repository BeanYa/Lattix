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
    <div className="flex min-h-screen flex-col md:flex-row">
      {foregroundPendingCount > 0 ? (
        <div
          role="status"
          aria-label="请求处理中"
          className="fixed inset-x-0 top-0 z-50 h-0.5 animate-pulse bg-primary"
        />
      ) : null}
      <header className="flex h-14 shrink-0 items-center justify-between border-b bg-sidebar px-4 md:hidden">
        <span className="flex items-center gap-2 text-lg font-semibold">
          <LattixMark className="size-7 text-foreground" />
          Lattix
        </span>
        <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
          <SheetTrigger
            render={<Button variant="ghost" size="icon" aria-label="打开导航菜单" />}
          >
            <MenuIcon />
          </SheetTrigger>
          <SheetContent side="left" className="w-64 p-0" showCloseButton={false}>
            <SheetTitle className="flex items-center gap-2 border-b px-5 py-4 text-lg">
              <LattixMark className="size-7 text-foreground" />
              Lattix
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
                      'flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
                      isActive && 'bg-sidebar-accent font-medium text-sidebar-accent-foreground',
                    )
                  }
                >
                  <item.icon className="size-4" />
                  {item.label}
                </NavLink>
              ))}
            </nav>
            <div className="flex items-center justify-between gap-2 border-t p-3">
              <span className="truncate text-sm" title={username ?? ''}>
                {username}
              </span>
              <Button variant="ghost" size="sm" onClick={onLogout}>
                <LogOutIcon />
                登出
              </Button>
            </div>
          </SheetContent>
        </Sheet>
      </header>
      <aside className="hidden w-56 shrink-0 flex-col border-r bg-sidebar md:flex">
        <div className="border-b px-5 py-4">
          <span className="flex items-center gap-2 text-lg font-semibold">
            <LattixMark className="size-7 text-foreground" />
            Lattix
          </span>
        </div>
        <nav className="flex-1 space-y-1 p-3">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
                  isActive && 'bg-sidebar-accent font-medium text-sidebar-accent-foreground',
                )
              }
            >
              <item.icon className="size-4" />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="flex items-center justify-between gap-2 border-t p-3">
          <span className="truncate text-sm" title={username ?? ''}>
            {username}
          </span>
          <Button variant="ghost" size="sm" onClick={onLogout}>
            <LogOutIcon />
            登出
          </Button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto p-4 md:p-6">
        <Outlet />
      </main>
      <UpdateOverlay />
    </div>
  )
}
