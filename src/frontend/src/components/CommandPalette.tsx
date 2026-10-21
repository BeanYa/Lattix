import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { Dialog as DialogPrimitive } from '@base-ui/react/dialog'
import { CornerDownLeftIcon, SearchIcon } from 'lucide-react'
import { useLocation } from 'wouter'

import {
  COMMAND_PAGES,
  COMMON_PAGES,
  PRIMARY_NAV,
  filterCommandPages,
  type NavDestination,
} from '@/components/nav-config'
import { cn } from '@/lib/utils'

interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface PaletteGroup {
  label: string
  pages: NavDestination[]
}

/**
 * 命令面板骨架（docs/ui-refactor-proposal.md 9.6）。
 * 当前只索引前端页面：五个一级入口 + 常用二级页面，不请求后端对象搜索。
 * 焦点陷阱、Esc、背景 inert 由 Base UI Dialog 提供；输入框保持焦点，
 * 用 aria-activedescendant 驱动列表高亮。
 */
export default function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const [, navigate] = useLocation()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
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
  }, [open])

  const filteredPages = useMemo(() => filterCommandPages(query), [query])

  const groups = useMemo<PaletteGroup[]>(() => {
    if (query.trim()) return [{ label: '搜索结果', pages: filteredPages }]
    return [
      { label: '一级入口', pages: PRIMARY_NAV },
      { label: '常用页面', pages: COMMON_PAGES },
    ]
  }, [filteredPages, query])

  const flatPages = useMemo(() => groups.flatMap((group) => group.pages), [groups])
  const currentIndex = flatPages.length === 0 ? -1 : Math.min(activeIndex, flatPages.length - 1)

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

  const selectPage = (page: NavDestination | undefined) => {
    if (!page) return
    onOpenChange(false)
    navigate(page.to)
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
      if (flatPages.length === 0) return
      const delta = event.key === 'ArrowDown' ? 1 : -1
      setActiveIndex((currentIndex + delta + flatPages.length) % flatPages.length)
      return
    }

    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      if (flatPages.length === 0) return
      setActiveIndex(event.key === 'Home' ? 0 : flatPages.length - 1)
      return
    }

    if (event.key === 'Enter') {
      event.preventDefault()
      selectPage(flatPages[currentIndex])
    }
  }

  const shortcutHint = COMMAND_PAGES.length > 0 ? '⌘K' : 'Ctrl K'

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
            搜索并跳转到 Lattix 的页面。使用上下方向键选择，回车打开，Esc 关闭。
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
              placeholder="搜索页面或跳转…"
              spellCheck={false}
              className="h-full min-w-0 flex-1 bg-transparent text-sm text-ink outline-none placeholder:text-ink-3"
            />
            <kbd className="hidden h-6 items-center rounded-[var(--radius-sm)] border border-rule bg-raised px-1.5 font-mono text-[10px] font-semibold text-ink-3 sm:inline-flex">
              Esc
            </kbd>
          </div>

          <div
            ref={listRef}
            className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2"
          >
            {flatPages.length === 0 ? (
              <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
                <span className="grid size-9 place-items-center rounded-[var(--radius-md)] border border-rule bg-raised text-ink-3">
                  <SearchIcon className="size-4" aria-hidden="true" />
                </span>
                <p className="text-sm font-semibold text-ink">没有匹配的页面</p>
                <p className="text-xs text-ink-3">换个关键词，或直接进入对象列表。</p>
                <button
                  type="button"
                  onClick={() =>
                    selectPage(COMMON_PAGES.find((page) => page.to === '/network/servers'))
                  }
                  className="mt-2 h-8 rounded-[var(--radius-sm)] border border-rule bg-raised px-3 text-xs font-semibold text-ink transition-colors duration-[var(--duration-1)] hover:border-rule-strong hover:bg-inset focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--panel-focus)]"
                >
                  查看全部服务器
                </button>
              </div>
            ) : (
              <div id={listboxId} role="listbox" aria-label="可跳转页面" className="flex flex-col">
                {groups.map((group) => {
                  const startIndex = groups
                    .slice(0, groups.indexOf(group))
                    .reduce((sum, item) => sum + item.pages.length, 0)
                  if (group.pages.length === 0) return null
                  return (
                    <div key={group.label} className="flex flex-col">
                      <div
                        role="presentation"
                        className="sticky top-0 z-10 flex h-7 items-center justify-between bg-surface px-2 text-[10px] font-semibold tracking-[0.14em] text-ink-3"
                      >
                        <span>{group.label}</span>
                        <span className="font-mono tabular-nums">{group.pages.length}</span>
                      </div>
                      {group.pages.map((page, index) => {
                        const globalIndex = startIndex + index
                        const active = globalIndex === currentIndex
                        const Icon = page.icon
                        return (
                          <div
                            key={`${group.label}-${page.to}`}
                            id={`${listboxId}-option-${globalIndex}`}
                            role="option"
                            aria-selected={active}
                            data-command-index={globalIndex}
                            onMouseEnter={() => setActiveIndex(globalIndex)}
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => selectPage(page)}
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
                              {page.label}
                            </span>
                            <span className="hidden min-w-0 max-w-[240px] truncate text-xs text-ink-3 sm:block">
                              {page.description}
                            </span>
                            <span className="hidden shrink-0 font-mono text-[10px] text-ink-3 lg:block">
                              {page.to}
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

          <div className="flex h-9 shrink-0 items-center justify-between border-t border-rule px-3 text-[11px] text-ink-3">
            <span className="flex items-center gap-2">
              <span className="flex items-center gap-1">
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[10px]">
                  ↑
                </kbd>
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[10px]">
                  ↓
                </kbd>
                <span>选择</span>
              </span>
              <span className="text-rule">/</span>
              <span className="flex items-center gap-1">
                <kbd className="rounded-[var(--radius-xs)] border border-rule bg-raised px-1 font-mono text-[10px]">
                  <CornerDownLeftIcon className="inline size-3" aria-hidden="true" />
                </kbd>
                <span>打开</span>
              </span>
            </span>
            <span className="flex items-center gap-2">
              <span className="tabular-nums">{flatPages.length} 个结果</span>
              <span className="text-rule">/</span>
              <span className="font-mono">{shortcutHint}</span>
            </span>
            <span className="sr-only" role="status" aria-live="polite">
              {flatPages.length} 个结果
            </span>
          </div>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
