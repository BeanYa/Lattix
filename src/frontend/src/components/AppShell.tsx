import {
  useEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react'
import {
  ChevronDownIcon,
  LogOutIcon,
  MenuIcon,
  SearchIcon,
  TriangleAlertIcon,
  XIcon,
} from 'lucide-react'
import { Link, useLocation } from 'wouter'

import CommandPalette from '@/components/CommandPalette'
import { OperationProgressProvider } from '@/components/OperationProgressProvider'
import LattixMark from '@/components/LattixMark'
import ThemeToggle from '@/components/ThemeToggle'
import UpdateOverlay from '@/components/UpdateOverlay'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Sheet, SheetClose, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import {
  PRIMARY_NAV,
  isDestinationActive,
  isPrimaryActive,
  pageTitleForPath,
  type NavDestination,
} from '@/components/nav-config'
import { api, errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useBoardQueue } from '@/lib/board-queue'
import { useRequestState } from '@/lib/request-state'
import type { PanelLifecycleSnapshot, PanelLifecycleState } from '@/lib/types'
import { cn } from '@/lib/utils'

/** 所有可交互元素共用的焦点环，颜色跟随 tokens.css 的 --panel-focus。 */
const focusRing =
  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]'

const reducedMotion =
  typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches

/**
 * 底部玻璃停靠栏（液态玻璃语言）：一级入口的图标快捷切换。
 * 悬停时按光标距离放大图标（macOS dock 手感），当前入口下方有铜橙圆点。
 */
function Dock({ pathname }: { pathname: string }) {
  const dockRef = useRef<HTMLDivElement>(null)

  const handleMove = (event: ReactMouseEvent<HTMLDivElement>) => {
    const dock = dockRef.current
    if (!dock || reducedMotion) return
    for (const item of dock.querySelectorAll<HTMLElement>('.dock-item')) {
      const rect = item.getBoundingClientRect()
      const distance = Math.abs(event.clientX - (rect.left + rect.width / 2))
      const scale = 1 + 0.38 * Math.max(0, 1 - distance / 96)
      item.style.transform = `scale(${scale.toFixed(3)}) translateY(${(-(scale - 1) * 8).toFixed(1)}px)`
    }
  }

  const handleLeave = () => {
    dockRef.current
      ?.querySelectorAll<HTMLElement>('.dock-item')
      .forEach((item) => (item.style.transform = ''))
  }

  return (
    <nav aria-label="快捷导航" className="fixed bottom-4 left-1/2 z-40 -translate-x-1/2">
      <div
        ref={dockRef}
        onMouseMove={handleMove}
        onMouseLeave={handleLeave}
        className="flex items-center gap-1 rounded-2xl border border-edge bg-surface/70 px-2 py-1.5 shadow-[var(--panel-shadow-overlay)] backdrop-blur-2xl"
      >
        {PRIMARY_NAV.map((item) => {
          const Icon = item.icon
          const active = isPrimaryActive(pathname, item)
          return (
            <Link
              key={item.key}
              href={item.to}
              aria-label={item.label}
              aria-current={active ? 'page' : undefined}
              title={item.label}
              className={cn(
                'dock-item relative grid size-11 place-items-center rounded-xl text-ink-2 transition-transform duration-150 hover:text-ink',
                active && 'text-ink',
                focusRing,
              )}
            >
              <Icon className="size-5" strokeWidth={1.8} aria-hidden="true" />
              {active ? (
                <span
                  aria-hidden="true"
                  className="absolute bottom-0 size-1 rounded-full bg-signal"
                />
              ) : null}
            </Link>
          )
        })}
      </div>
    </nav>
  )
}

/** 面板状态文案沿用 Layout.tsx 的英文状态词，颜色只作辅助，状态词始终可读。 */
const panelTone: Record<
  PanelLifecycleState | 'offline',
  { label: string; className: string; pulse?: boolean }
> = {
  startup: { label: 'STARTUP', className: 'text-waiting' },
  active: { label: 'ACTIVE', className: 'text-ok', pulse: true },
  updating: { label: 'UPDATING', className: 'text-signal', pulse: true },
  faulted: { label: 'FAULTED', className: 'text-failed' },
  offline: { label: 'OFFLINE', className: 'text-ink-3' },
}

/**
 * 复刻 Layout.tsx 的面板状态轮询：挂载即取一次，之后每 5 秒刷新；
 * 请求失败时回到 OFFLINE，而不是保留过期状态假装健康。
 */
function usePanelLifecycleSnapshot() {
  const [snapshot, setSnapshot] = useState<PanelLifecycleSnapshot | null>(null)

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const next = await api.panelState()
        if (active) setSnapshot(next)
      } catch {
        if (active) setSnapshot(null)
      }
    }

    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  return snapshot
}

function PanelStateChip({ snapshot }: { snapshot: PanelLifecycleSnapshot | null }) {
  const tone = panelTone[snapshot?.state ?? 'offline']
  const title = snapshot
    ? `面板状态 ${tone.label}${snapshot.fault ? `：${snapshot.fault}` : ''}`
    : '面板状态 OFFLINE：暂时无法读取 /api/panel/state'

  return (
    <span
      role="status"
      aria-label={title}
      title={title}
      className={cn(
        'inline-flex h-6 shrink-0 items-center gap-1.5 rounded-sm border border-rule bg-raised px-2 text-[12px] font-semibold tracking-[0.08em]',
        tone.className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn('size-1.5 shrink-0 rounded-full bg-current', tone.pulse && 'cg-beacon-live')}
      />
      <span lang="en">{tone.label}</span>
    </span>
  )
}

interface NavRowProps {
  destination: NavDestination
  mode: 'desktop' | 'sheet'
  secondary?: boolean
  parentLabel?: string
  /** 当前页：显示信号底、左侧 2px 标记，并写入 aria-current。 */
  active: boolean
  /** 当前一级域：只负责把一级入口文字提到 ink 色。 */
  domainActive?: boolean
  badgeCount?: number
  badgeTone?: 'critical' | 'warning'
  onNavigate?: () => void
}

function NavRow({
  destination,
  mode,
  secondary = false,
  parentLabel,
  active,
  domainActive = false,
  badgeCount,
  badgeTone,
  onNavigate,
}: NavRowProps) {
  const Icon = destination.icon
  const isDesktop = mode === 'desktop'
  const name = secondary && parentLabel ? `${parentLabel}：${destination.label}` : destination.label

  return (
    <Link
      href={destination.to}
      onClick={onNavigate}
      aria-current={active ? 'page' : undefined}
      aria-label={name}
      title={isDesktop ? name : undefined}
      className={cn(
        'group relative flex h-10 w-full items-center gap-3 rounded-sm text-[15px] transition-all duration-[var(--duration-2)]',
        secondary ? 'font-medium' : 'font-semibold',
        isDesktop
          ? secondary
            ? 'justify-center px-0 xl:justify-start xl:pl-8 xl:pr-3'
            : 'justify-center px-0 xl:justify-start xl:px-3'
          : secondary
            ? 'justify-start pl-7 pr-3'
            : 'justify-start px-3',
        active
          ? 'bg-signal-soft text-ink shadow-2xs'
          : domainActive
            ? 'text-ink hover:bg-raised'
            : 'text-ink-2 hover:bg-raised hover:text-ink',
        focusRing,
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          'absolute inset-y-2 left-0 w-0.5 origin-center rounded-full bg-signal transition-transform duration-[var(--duration-2)]',
          active ? 'scale-y-100' : 'scale-y-0',
        )}
      />
      <Icon
        className={cn(
          'size-4 shrink-0 transition-transform duration-[var(--duration-1)] group-hover:scale-105',
          active && 'text-signal',
        )}
        strokeWidth={1.8}
        aria-hidden="true"
      />
      <span className={cn('min-w-0 flex-1 truncate', isDesktop && 'hidden xl:block')}>
        {destination.label}
      </span>
      {badgeCount !== undefined && badgeCount > 0 ? (
        <span
          className={cn(
            'hidden h-4 min-w-4 items-center justify-center rounded-xs px-1 font-mono text-[11px] font-semibold tabular-nums xl:inline-flex',
            badgeTone === 'critical' ? 'bg-failed/15 text-failed' : 'bg-degraded/15 text-degraded',
          )}
        >
          {badgeCount}
        </span>
      ) : null}
    </Link>
  )
}

/**
 * 五个一级入口 + 当前域的二级导航。
 * desktop 用于 1024–1279 的 72px 图标栏与 ≥1280 的 224px 侧栏：
 * 1024–1279 只留图标（title/aria-label 兜底），≥1280 恢复文字与 32px 二级缩进。
 */
function NavList({
  mode,
  pendingCount,
  criticalCount,
  onNavigate,
}: {
  mode: 'desktop' | 'sheet'
  pendingCount: number
  criticalCount: number
  onNavigate?: () => void
}) {
  const [location] = useLocation()

  return (
    <nav
      aria-label="主导航"
      className={cn(
        'flex min-h-0 flex-1 flex-col overflow-y-auto overscroll-contain',
        mode === 'desktop' ? 'gap-1 p-2' : 'gap-1 p-3',
      )}
    >
      <ul className="flex flex-col gap-1">
        {PRIMARY_NAV.map((item) => {
          const children = item.children ?? []
          const domainActive = isPrimaryActive(location, item)
          const activeChild = children.find((child) => isDestinationActive(location, child))
          const current = domainActive && !activeChild

          return (
            <li key={item.key}>
              <NavRow
                destination={item}
                mode={mode}
                active={current}
                domainActive={domainActive}
                badgeCount={item.key === 'board' ? pendingCount : undefined}
                badgeTone={criticalCount > 0 ? 'critical' : 'warning'}
                onNavigate={onNavigate}
              />

              {/* 子菜单默认全部展开：仅当前域高亮，不再按域折叠。 */}
              {children.length > 0 ? (
                <ul
                  aria-label={`${item.label}二级导航`}
                  className={cn(
                    'mt-1 flex flex-col gap-1',
                    mode === 'desktop'
                      ? 'border-t border-rule pt-1 xl:mt-0 xl:border-0 xl:pt-0'
                      : 'ml-3 border-l border-rule pl-1',
                  )}
                >
                  {children.map((child) => (
                    <li key={child.to}>
                      <NavRow
                        destination={child}
                        mode={mode}
                        secondary
                        parentLabel={item.label}
                        active={isDestinationActive(location, child)}
                        onNavigate={onNavigate}
                      />
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

function AccountMenu({
  username,
  loggingOut,
  onLogout,
}: {
  username: string | null
  loggingOut: boolean
  onLogout: () => void
}) {
  const displayName = (username ?? 'A').slice(0, 1).toUpperCase()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            aria-label={username ? `账户菜单：${username}` : '账户菜单'}
            title={username ?? '账户'}
            className={cn(
              'flex h-8 items-center gap-1.5 rounded-sm px-1.5 text-ink-2 transition-colors hover:bg-raised hover:text-ink',
              focusRing,
            )}
          >
            <span
              aria-hidden="true"
              className="grid size-6 place-items-center rounded-full border border-rule-strong bg-inset text-[13px] font-semibold text-ink"
            >
              {displayName}
            </span>
            <span className="hidden max-w-24 truncate text-xs font-medium xl:block">
              {username ?? '账户'}
            </span>
            <ChevronDownIcon className="hidden size-3.5 xl:block" aria-hidden="true" />
          </button>
        }
      />
      <DropdownMenuContent
        align="end"
        sideOffset={6}
        className="w-56 rounded-md border border-rule-strong bg-surface p-1 text-ink shadow-[var(--panel-shadow-overlay)]"
      >
        <DropdownMenuLabel className="px-2 py-1.5 text-[12px] font-semibold tracking-[0.12em] text-ink-3">
          账户
        </DropdownMenuLabel>
        <div className="px-2 pb-2 text-xs text-ink-2">
          <span className="block truncate font-medium text-ink">{username ?? '未登录'}</span>
          <span className="mt-1 block text-ink-3">自托管管理员</span>
        </div>
        <DropdownMenuSeparator className="my-1 bg-rule" />
        <DropdownMenuItem
          onClick={onLogout}
          disabled={loggingOut}
          className={cn(
            'flex h-9 cursor-default items-center gap-2 rounded-sm px-2 text-xs text-ink-2 focus:bg-signal-soft focus:text-ink',
            focusRing,
          )}
        >
          <LogOutIcon className="size-4" aria-hidden="true" />
          {loggingOut ? '登出中…' : '登出'}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const [location, navigate] = useLocation()
  const { username, logout } = useAuth()
  const { foregroundPendingCount } = useRequestState()
  const panelSnapshot = usePanelLifecycleSnapshot()
  const { counts } = useBoardQueue()
  const criticalCount = counts?.critical ?? 0
  const warningCount = counts?.warning ?? 0
  const pendingCount = criticalCount + warningCount
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const [logoutError, setLogoutError] = useState('')

  // 路由切换时同步文档标题；nav-config 的 pageTitleForPath 是唯一标题来源。
  useEffect(() => {
    document.title = `${pageTitleForPath(location)} · Lattix`
  }, [location])

  // 抽屉里的链接点击后会关闭自身；这里兜住命令面板等外部导航。
  useEffect(() => {
    setMobileNavOpen(false)
  }, [location])

  // 命令面板与抽屉不同时打开，避免两个模态层叠。
  useEffect(() => {
    if (paletteOpen) setMobileNavOpen(false)
  }, [paletteOpen])

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

  const pendingTone =
    criticalCount > 0
      ? 'border-failed/40 bg-failed/10 text-failed'
      : warningCount > 0
        ? 'border-degraded/40 bg-degraded/10 text-degraded'
        : 'border-rule bg-raised text-ink-3'

  return (
    <div
      data-slot="app-shell"
      className="relative flex h-[100dvh] flex-col overflow-hidden bg-ground text-ink"
    >
      {/* 顶部 2px 前台请求加载条（方案 9.10） */}
      {foregroundPendingCount > 0 ? (
        <div
          aria-hidden="true"
          className="pointer-events-none fixed inset-x-0 top-0 z-[90] h-0.5 overflow-hidden bg-signal/20"
        >
          <div className="h-full w-1/3 animate-pulse bg-signal" />
        </div>
      ) : null}

      {/* 跳转链接必须是 DOM 中第一个可聚焦元素，且只在键盘聚焦时出现。 */}
      <div className="fixed left-4 top-4 z-[100]">
        <a
          href="#main-content"
          className={cn(
            'sr-only focus:not-sr-only focus:inline-flex focus:h-9 focus:items-center focus:rounded-sm focus:border focus:border-rule-strong focus:bg-surface focus:px-3 focus:text-sm focus:font-semibold focus:text-ink',
            focusRing,
          )}
        >
          跳到主内容
        </a>
      </div>

      {/* 顶栏固定 56px；<1024 的抽屉触发器在右侧动作区。 */}
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-rule bg-surface/70 px-3 backdrop-blur-2xl sm:px-4">
        <Link
          href="/board"
          aria-label="Lattix 控制台"
          className={cn(
            'group flex min-w-0 shrink-0 items-center gap-2.5 rounded-sm text-ink',
            focusRing,
          )}
        >
          <span className="grid size-8 place-items-center rounded-sm border border-rule bg-raised transition-colors group-hover:border-rule-strong">
            <LattixMark className="size-4 shrink-0 transition-transform duration-150 group-hover:scale-105" />
          </span>
          <span className="hidden text-[15px] font-bold tracking-[0.16em] sm:block" lang="en">
            LATTIX
          </span>
        </Link>

        <span aria-hidden="true" className="mx-0.5 hidden h-4 w-px bg-rule min-[375px]:block" />
        <div className="hidden min-[375px]:block">
          <PanelStateChip snapshot={panelSnapshot} />
        </div>

        <div className="ml-1 flex min-w-0 flex-1 items-center">
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            aria-label="打开命令面板（⌘K）"
            aria-haspopup="dialog"
            aria-expanded={paletteOpen}
            aria-keyshortcuts="Meta+K Control+K"
            className={cn(
              'hidden h-9 shrink-0 items-center gap-2 rounded-sm border border-rule bg-raised px-3 text-left text-xs text-ink-3 transition-all hover:border-rule-strong hover:bg-inset/50 hover:text-ink-2 lg:inline-flex lg:w-64 xl:w-80',
              focusRing,
            )}
          >
            <SearchIcon className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate">搜索页面、服务器、链路或用户…</span>
            <kbd className="ml-auto inline-flex h-5 shrink-0 items-center rounded-xs border border-rule bg-surface px-1.5 font-mono text-[11px] font-semibold text-ink-3">
              ⌘K
            </kbd>
          </button>
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            aria-label="打开命令面板（⌘K）"
            aria-haspopup="dialog"
            aria-expanded={paletteOpen}
            aria-keyshortcuts="Meta+K Control+K"
            className={cn(
              'grid size-9 place-items-center rounded-sm text-ink-2 transition-colors hover:bg-raised hover:text-ink lg:hidden',
              focusRing,
            )}
          >
            <SearchIcon className="size-4" aria-hidden="true" />
          </button>
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          <Link
            href="/board"
            aria-label={`${pendingCount} 项待处理，前往控制台`}
            title={`严重 ${criticalCount} 项 / 警告 ${warningCount} 项`}
            className={cn(
              'inline-flex h-8 items-center gap-1.5 rounded-sm border px-2.5 text-xs font-medium transition-colors hover:brightness-105',
              pendingTone,
              focusRing,
            )}
          >
            <TriangleAlertIcon className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="font-mono text-[13px] font-semibold tabular-nums">{pendingCount}</span>
            <span className="hidden md:inline">项待处理</span>
          </Link>

          <ThemeToggle
            className={cn(
              'size-8 rounded-sm text-ink-2 hover:bg-raised hover:text-ink focus-visible:ring-0',
              focusRing,
            )}
          />

          <AccountMenu
            username={username}
            loggingOut={loggingOut}
            onLogout={() => void onLogout()}
          />

          <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
            <SheetTrigger
              render={
                <button
                  type="button"
                  className={cn(
                    'grid size-9 place-items-center rounded-sm text-ink-2 transition-colors hover:bg-raised hover:text-ink lg:hidden',
                    focusRing,
                  )}
                  aria-label="打开导航菜单"
                />
              }
            >
              <MenuIcon className="size-5" aria-hidden="true" />
            </SheetTrigger>
            {/* Base UI Dialog 自带焦点陷阱与 Esc 关闭；导航后由 NavList 回调关闭。 */}
            <SheetContent
              side="left"
              showCloseButton={false}
              className="w-[280px] gap-0 rounded-none border-r border-rule bg-surface p-0 text-ink sm:max-w-[280px]"
            >
              <div className="flex h-14 shrink-0 items-center justify-between border-b border-rule px-4">
                <SheetTitle className="flex items-center gap-2 text-[15px] font-bold tracking-[0.16em] text-ink">
                  <LattixMark className="size-4 shrink-0" />
                  <span lang="en">LATTIX</span>
                  <span className="sr-only">导航菜单</span>
                </SheetTitle>
                <SheetClose
                  render={
                    <button
                      type="button"
                      className={cn(
                        'grid size-8 place-items-center rounded-sm text-ink-2 transition-colors hover:bg-raised hover:text-ink',
                        focusRing,
                      )}
                      aria-label="关闭导航菜单"
                    />
                  }
                >
                  <XIcon className="size-4" aria-hidden="true" />
                </SheetClose>
              </div>
              <NavList
                mode="sheet"
                pendingCount={pendingCount}
                criticalCount={criticalCount}
                onNavigate={() => setMobileNavOpen(false)}
              />
              <div className="shrink-0 border-t border-rule px-4 py-3">
                <p className="text-[12px] font-semibold tracking-[0.08em] text-ink-3">自托管面板</p>
              </div>
            </SheetContent>
          </Sheet>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 overflow-hidden">
        {/* 侧栏：<1024 隐藏，1024–1279 为 72px 图标栏，≥1280 为 224px 展开栏。 */}
        <aside className="hidden min-h-0 shrink-0 flex-col border-r border-rule bg-surface/55 backdrop-blur-2xl lg:flex lg:w-[72px] xl:w-56">
          <div className="hidden h-10 shrink-0 items-center justify-between border-b border-rule px-4 text-[11px] font-semibold tracking-[0.14em] text-ink-3 xl:flex">
            <span>导航 · NAVIGATION</span>
            <span className="size-1.5 rounded-full bg-signal" aria-hidden="true" />
          </div>
          <NavList mode="desktop" pendingCount={pendingCount} criticalCount={criticalCount} />
          <div className="shrink-0 border-t border-rule p-3">
            <p className="hidden text-[11px] font-semibold leading-4 tracking-[0.08em] text-ink-3 xl:block">
              自托管面板
            </p>
            <span
              aria-hidden="true"
              className="mx-auto block size-1.5 rounded-full bg-signal xl:hidden"
            />
          </div>
        </aside>

        {/* 主列独立滚动；主内容最多 1600px，<1024 使用 16px 内边距。 */}
        <div className="min-w-0 flex-1 overflow-y-auto overscroll-contain">
          <main
            id="main-content"
            tabIndex={-1}
            className="mx-auto w-full max-w-[1600px] px-4 pb-28 pt-6 lg:px-6 lg:pb-24"
          >
            {logoutError ? (
              <div
                role="alert"
                className="mb-4 flex items-start gap-2 rounded-md border border-failed/40 bg-failed/10 px-3 py-2 text-sm text-ink"
              >
                <TriangleAlertIcon
                  className="mt-0.5 size-4 shrink-0 text-failed"
                  aria-hidden="true"
                />
                <span>{logoutError}</span>
              </div>
            ) : null}
            <OperationProgressProvider>{children}</OperationProgressProvider>
          </main>
        </div>
      </div>

      <Dock pathname={location} />
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      <UpdateOverlay />
    </div>
  )
}

export default AppShell
