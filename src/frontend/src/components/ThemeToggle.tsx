import { CheckIcon, MoonIcon, SunIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme } from '@/lib/theme-context'
import type { Theme } from '@/lib/theme-context'
import { cn } from '@/lib/utils'

interface ThemeToggleProps {
  className?: string
}

const MODE_LABELS: Record<Theme, string> = {
  light: '浅色',
  dark: '深色',
}

export default function ThemeToggle({ className }: ThemeToggleProps) {
  const { theme, setTheme } = useTheme()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className={cn('text-muted-foreground', className)}
            aria-label={`切换外观（当前${MODE_LABELS[theme]}）`}
            title={`外观：${MODE_LABELS[theme]}`}
          >
            {theme === 'dark' ? <MoonIcon /> : <SunIcon />}
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="w-36">
        <DropdownMenuGroup>
          <DropdownMenuLabel>外观</DropdownMenuLabel>
          {(['dark', 'light'] as const).map((mode) => (
            <DropdownMenuItem key={mode} onClick={() => setTheme(mode)} className="justify-between">
              <span className="flex items-center gap-2">
                {mode === 'light' ? <SunIcon /> : <MoonIcon />}
                {MODE_LABELS[mode]}
              </span>
              {theme === mode && <CheckIcon className="size-3.5 opacity-60" />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
