import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { Dialog as DialogPrimitive } from '@base-ui/react/dialog'
import {
  CornerDownLeftIcon,
  SearchIcon,
  ServerIcon,
  UsersIcon,
  WaypointsIcon,
  type LucideIcon,
} from 'lucide-react'
import { useLocation } from 'wouter'

import {
  COMMAND_PAGES,
  COMMON_PAGES,
  PRIMARY_NAV,
  filterCommandPages,
  type NavDestination,
} from '@/components/nav-config'
import { api, errorMessage } from '@/lib/api'
import type { Chain, Server, SubUser } from '@/lib/types'
import { cn } from '@/lib/utils'

interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface PaletteItem {
  id: string
  label: string
  description?: string
  to: string
  icon: LucideIcon
  meta?: string
}

interface PaletteGroup {
  label: string
  items: PaletteItem[]
}

interface ObjectIndex {
  at: number
  servers: Server[]
  chains: Chain[]
  users: SubUser[]
}

/** 对象索引缓存 30 秒：与方案 9.6 的「打开时缓存超过 30 秒先刷新」一致。 */
const OBJECT_CACHE_MS = 30_000
/** 单次搜索最多返回的对象条数，避免一次渲染上千个服务器。 */
const MAX_OBJECT_RESULTS = 12

/** 页面入口转成统一结果项；对象与页面共用同一套键盘与高亮逻辑。 */
function toPageItem(page: NavDestination): PaletteItem {
  return {
    id: `page:${page.to}`,
    label: page.label,
    description: page.description,
    to: page.to,
    icon: page.icon,
    meta: page.to,
  }
}

function serverItems(servers: Server[], query: string): PaletteItem[] {
  const items: PaletteItem[] = []
  for (const server of servers) {
    const haystack = `${server.alias} ${server.address} ${server.id}`.toLowerCase()
    if (!haystack.includes(query)) continue
    items.push({
      id: `server-${server.id}`,
      label: server.alias || `服务器 #${server.id}`,
      description: server.address,
      to: `/network/servers?sel=${server.id}`,
      icon: ServerIcon,
      meta: `#${server.id}`,
    })
  }
  return items
}

function chainItems(chains: Chain[], query: string): PaletteItem[] {
  const items: PaletteItem[] = []
  for (const chain of chains) {
    const haystack = `${chain.name} ${chain.id}`.toLowerCase()
    if (!haystack.includes(query)) continue
    items.push({
      id: `chain-${chain.id}`,
      label: chain.name || `链路 #${chain.id}`,
      description: chain.hops.map((hop) => hop.server_alias).join(' → '),
      to: `/network/chains/${chain.id}`,
      icon: WaypointsIcon,
      meta: `#${chain.id}`,
    })
  }
  return items
}

function userItems(users: SubUser[], query: string): PaletteItem[] {
  const items: PaletteItem[] = []
  for (const user of users) {
    const haystack = `${user.name} ${user.id}`.toLowerCase()
    if (!haystack.includes(query)) continue
    items.push({
      id: `user-${user.id}`,
      label: user.name || `用户 #${user.id}`,
      description: user.expired ? '已过期' : '有效',
      to: `/access/users?sel=${user.id}`,
      icon: UsersIcon,
      meta: `#${user.id}`,
    })
  }
  return items
}

/**
 * 命令面板（方案 9.6）。
 * 页面入口来自 nav-config；对象索引复用现有 requester（服务器 / 链路 / 用户），
 * 打开时按需拉取并缓存 30 秒，不新增后端搜索接口。焦点陷阱、Esc、背景 inert
 * 由 Base UI Dialog 提供；输入框保持焦点，用 aria-activedescendant 驱动高亮。
 */
export default function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const [, navigate] = useLocation()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [objectIndex, setObjectIndex] = useState<ObjectIndex | null>(null)
  const [objectsLoading, setObjectsLoading] = useState(false)
  const [objectsError, setObjectsError] = useState<string | null>(null)
  const cacheRef = useRef<ObjectIndex | null>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const listboxId = useId()

  // 全局快捷键：⌘K / Ctrl+K。组合键在输入框内同样生效，普通字符不劫持。
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat || event.altKey) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        onOpenChange(!open)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onOpenChange, open])

  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveIndex(0)

    // 30 秒内复用缓存，避免每次打开都重新拉全量对象。
    const cached = cacheRef.current
    if (cached) setObjectIndex(cached)
    if (cached && Date.now() - cached.at < OBJECT_CACHE_MS) return

    let active = true
    const failures: string[] = []
    setObjectsLoading(true)
    void Promise.allSettled([
      api.servers({ display: 'silent' }),
      api.chains({ display: 'silent' }),
      api.users({ display: 'silent' }),
    ]).then(([servers, chains, users]) => {
      if (!active) return
      if (servers.status === 'rejected') failures.push(`服务器：${errorMessage(servers.reason)}`)
      if (chains.status === 'rejected') failures.push(`链路：${errorMessage(chains.reason)}`)
      if (users.status === 'rejected') failures.push(`用户：${errorMessage(users.reason)}`)
      const next: ObjectIndex = {
        at: Date.now(),
        servers: servers.status === 'fulfilled' ? servers.value : [],
        chains: chains.status === 'fulfilled' ? chains.value : [],
        users: users.status === 'fulfilled' ? users.value : [],
      }
      cacheRef.current = next
      setObjectIndex(next)
      setObjectsError(failures.length > 0 ? failures.join('；') : null)
      setObjectsLoading(false)
    })
    return () => {
      active = false
    }
  }, [open])

  const queryLower = query.trim().toLowerCase()

  const objectMatches = useMemo<PaletteItem[]>(() => {
    if (!queryLower || !objectIndex) return []
    return [
      ...serverItems(objectIndex.servers, queryLower),
      ...chainItems(objectIndex.chains, queryLower),
      ...userItems(objectIndex.users, queryLower),
    ].slice(0, MAX_OBJECT_RESULTS)
  }, [objectIndex, queryLower])

  const pageMatches = useMemo(() => filterCommandPages(query), [query])

  const groups = useMemo<PaletteGroup[]>(() => {
    if (!queryLower) {
      return [
        { label: '一级入口', items: PRIMARY_NAV.map(toPageItem) },
        { label: '常用页面', items: COMMON_PAGES.map(toPageItem) },
      ]
    }
    return [
      { label: '对象', items: objectMatches },
      { label: '页面', items: pageMatches.map(toPageItem) },
    ].filter((group) => group.items.length > 0)
  }, [objectMatches, pageMatches, queryLower])

  const flatItems = useMemo(() => groups.flatMap((group) => group.items), [groups])
  const currentIndex = flatItems.length === 0 ? -1 : Math.min(activeIndex, flatItems.length - 1)

  useEffect(() => {
    setActiveIndex(0)
  }, [query])

  useEffect(() => {
    if (currentIndex < 0) return
    const active = listRef.current?.querySelector<HTMLElement>(
      `[data-command-index="${currentIndex}"]`,
    )
    active?.scrollIntoView({ block: 'nearest' })
  }, [currentIndex])

  const selectItem = (item: PaletteItem | undefined) => {
    if (!item) return
    onOpenChange(false)
    navigate(item.to)
  }

  const onInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return

    if (event.key === 'Escape') {
      event.preventDefault()
      onOpenChange(false)
      return
    }

    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      if (flatItems.length === 0) return
      const delta = event.key === 'ArrowDown' ? 1 : -1
      setActiveIndex((currentIndex + delta + flatItems.length) % flatItems.length)
      return
    }

    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      if (flatItems.length === 0) return
      setActiveIndex(event.key === 'Home' ? 0 : flatItems.length - 1)
      return
    }

    if (event.key === 'Enter') {
      event.preventDefault()
      selectItem(flatItems[currentIndex])
    }
  }

  const shortcutHint = COMMAND_PAGES.length > 0 ? '⌘K' : 'Ctrl K'
  const emptyHint = objectsLoading
    ? '正在索引对象…'
    : objectsError
      ? '对象索引不可用，只能搜索页面。'
      : '换个关键词，或直接进入对象列表。'

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop
          data-slot="command-palette-backdrop"
          className="fixed inset-0 z-[70] bg-[var(--panel-scrim)] transition-opacity duration-[var(--duration-2)] data-starting-style:opacity-0 data-ending-style:opacity-0 motion-reduce:transition-none"
        />
        <DialogPrimitive.Popup
          data-slot="command-palette"
          aria-label="命令面板"
          className="fixed top-[12vh] left-1/2 z-[70] flex max-h-[min(560px,70vh)] w-[min(640px,calc(100vw-32px))] -translate-x-1/2 flex-col overflow-hidden rounded-[var(--radius-lg)] border border-rule bg-surface text-ink shadow-[var(--panel-shadow-overlay)] outline-none transition-[opacity,translate] duration-[var(--duration-3)] data-starting-style:opacity-0 data-ending-style:opacity-0 motion-reduce:transition-none"
        >
          <DialogPrimitive.Title className="sr-only">命令面板</DialogPrimitive.Title>
          <DialogPrimitive.Description className="sr-only">
            搜索并跳转到 Lattix 的页面、服务器、链路或用户。使用上下方向键选择，回车打开，Esc 关闭。
          </DialogPrimitive.Description>

          <div className="flex h-12 shrink-0 items-center gap-3 border-b border-rule px-4">
            <SearchIcon className="size-4 shrink-0 text-ink-3" aria-hidden="true" />
            <input
              autoFocus
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={onInputKeyDown}
              role="combobox"
              aria-expanded={open}
              aria-controls={listboxId}
              aria-activedescendant={
                currentIndex >= 0 ? `${listboxId}-option-${currentIndex}` : undefined
              }
              aria-autocomplete="list"
              aria-label="搜索或跳转"
              placeholder="搜索页面、服务器、链路或用户…"
              spellCheck={false}
              className="h-full min-w-0 flex-1 bg-transparent text-sm text-ink outline-none placeholder:text-ink-3"
            />
            <kbd className="hidden h-6 items-center rounded-[var(--radius-sm)] border border-rule bg-raised px-1.5 font-mono text-[12px] font-semibold text-ink-3 sm:inline-flex">
              Esc
            </kbd>
          </div>

          <div
            ref={listRef}
            className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2"
          >
            {flatItems.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
                <span className="grid size-9 place-items-center rounded-[var(--radius-md)] border border-rule bg-raised text-ink-3">
                  <SearchIcon className="size-4" aria-hidden="true" />
                </span>
                <p className="text-sm font-semibold text-ink">没有匹配结果</p>
                <p className="text-xs text-ink-3">{emptyHint}</p>
                <button
                  type="button"
                  onClick={() =>
                    selectItem(
                      toPageItem(
                        COMMON_PAGES.find((page) => page.to === '/network/servers') ??
                          COMMON_PAGES[0] ??
                          PRIMARY_NAV[0],
                      ),
                    )
                  }
                  className="mt-2 h-8 rounded-[var(--radius-sm)] border border-rule bg-raised px-3 text-xs font-semibold text-ink transition-colors duration-[var(--duration-1)] hover:border-rule-strong hover:bg-inset focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]"
                >
                  查看全部服务器
                </button>
              </div>
            ) : (
              <div
                id={listboxId}
                role="listbox"
                aria-label="可跳转页面与对象"
                className="flex flex-col"
              >
                {groups.map((group) => {
                  const startIndex = groups
                    .slice(0, groups.indexOf(group))
                    .reduce((sum, item) => sum + item.items.length, 0)
                  return (
                    <div key={group.label} className="flex flex-col">
                      <div
                        role="presentation"
                        className="sticky top-0 z-10 flex h-7 items-center justify-between bg-surface px-2 text-[12px] font-semibold tracking-[0.14em] text-ink-3"
                      >
                        <span>{group.label}</span>
                        <span className="font-mono tabular-nums">{group.items.length}</span>
                      </div>
                      {group.items.map((item, index) => {
                        const globalIndex = startIndex + index
                        const active = globalIndex === currentIndex
                        const Icon = item.icon
                        return (
                          <div
                            key={item.id}
                            id={`${listboxId}-option-${globalIndex}`}
                            role="option"
                            aria-selected={active}
                            data-command-index={globalIndex}
                            onMouseEnter={() => setActiveIndex(globalIndex)}
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => selectItem(item)}
                            className={cn(
                              'flex h-10 cursor-default items-center gap-2.5 rounded-[var(--radius-sm)] px-2 text-sm transition-colors duration-[var(--duration-1)]',
                              active ? 'bg-signal-soft text-ink' : 'text-ink-2',
                            )}
                          >
                            <span
                              className={cn(
                                'grid size-6 shrink-0 place-items-center rounded-[var(--radius-xs)] border',
                                active
                                  ? 'border-[var(--panel-signal)]/30 text-signal'
                                  : 'border-transparent text-ink-3',
                              )}
                            >
                              <Icon className="size-3.5" strokeWidth={1.8} aria-hidden="true" />
                            </span>
                            <span className="min-w-0 flex-1 truncate font-medium">
                              {item.label}
                            </span>
                            <span className="hidden min-w-0 max-w-[240px] truncate text-xs text-ink-3 sm:block">
                              {item.description}
                            </span>
                            <span className="hidden shrink-0 font-mono text-[12px] text-ink-3 lg:block">
                              {item.meta}
                            </span>
                          </div>
                        )
                      })}
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          <div className="flex h-9 shrink-0 items-center justify-between border-t border-rule px-3 text-[13px] text-ink-3">
            <span className="flex items-center gap-2">
              <span className="flex items-center gap-1">
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[12px]">
                  ↑
                </kbd>
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[12px]">
                  ↓
                </kbd>
                <span>选择</span>
              </span>
              <span className="text-rule">/</span>
              <span className="flex items-center gap-1">
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[12px]">
                  <CornerDownLeftIcon className="inline size-3" aria-hidden="true" />
                </kbd>
                <span>打开</span>
              </span>
            </span>
            <span className="flex items-center gap-2">
              {objectsLoading ? <span>索引对象中…</span> : null}
              <span className="tabular-nums">{flatItems.length} 个结果</span>
              <span className="text-rule">/</span>
              <span className="font-mono">{shortcutHint}</span>
            </span>
            <span className="sr-only" role="status" aria-live="polite">
              {flatItems.length} 个结果
            </span>
          </div>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
