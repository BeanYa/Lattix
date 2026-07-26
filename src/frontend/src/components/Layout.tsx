import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import {
  LayoutDashboardIcon,
  LogOutIcon,
  NetworkIcon,
  RouteIcon,
  ServerIcon,
  SettingsIcon,
  UsersIcon,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import UpdateOverlay from '@/components/UpdateOverlay'
import { useAuth } from '@/lib/auth'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', label: '仪表盘', icon: LayoutDashboardIcon, end: true },
  { to: '/servers', label: '服务器', icon: ServerIcon, end: false },
  { to: '/nodes', label: '节点', icon: NetworkIcon, end: false },
  { to: '/chains', label: '链路', icon: RouteIcon, end: false },
  { to: '/users', label: '用户', icon: UsersIcon, end: false },
  { to: '/settings', label: '设置', icon: SettingsIcon, end: false },
]

export default function Layout() {
  const { username, logout } = useAuth()
  const navigate = useNavigate()

  const onLogout = async () => {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r bg-sidebar">
        <div className="border-b px-5 py-4">
          <span className="text-lg font-semibold">Lattix</span>
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
      <main className="min-w-0 flex-1 overflow-auto p-6">
        <Outlet />
      </main>
      <UpdateOverlay />
    </div>
  )
}
