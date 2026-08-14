import { CheckIcon, MoonIcon, PaletteIcon, SunIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme } from '@/lib/theme-context'
import type { Theme } from '@/lib/theme-context'
import { DEFAULT_DESIGN, DESIGN_THEMES } from '@/themes/registry'
import { cn } from '@/lib/utils'

interface ThemeToggleProps {
  className?: string
}

const MODE_LABELS: Record<Theme, string> = {
  light: '浅色',
  dark: '深色',
}

export default function ThemeToggle({ className }: ThemeToggleProps) {
  const { theme, setTheme, design, setDesign } = useTheme()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className={cn(className)}
            aria-label="切换主题"
            title="切换主题"
          >
            <PaletteIcon />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuLabel>外观模式</DropdownMenuLabel>
        {(['light', 'dark'] as const).map((mode) => (
          <DropdownMenuItem key={mode} onClick={() => setTheme(mode)} className="justify-between">
            <span className="flex items-center gap-2">
              {mode === 'light' ? <SunIcon /> : <MoonIcon />}
              {MODE_LABELS[mode]}
            </span>
            {theme === mode && <CheckIcon className="size-4 opacity-60" />}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuLabel>设计主题</DropdownMenuLabel>
        {DESIGN_THEMES.map(({ id, label, description }) => (
          <DropdownMenuItem
            key={id}
            onClick={() => setDesign(id)}
            title={description}
            className="justify-between"
          >
            <span className="truncate">
              {label}
              {id === DEFAULT_DESIGN && <span className="ml-1 text-xs opacity-50">默认</span>}
            </span>
            {design === id && <CheckIcon className="size-4 opacity-60" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
