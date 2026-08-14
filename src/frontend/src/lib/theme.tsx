import { useCallback, useLayoutEffect, useState } from 'react'
import type { ReactNode } from 'react'

import { ThemeContext } from '@/lib/theme-context'
import type { DesignTheme, Theme } from '@/lib/theme-context'
import { DEFAULT_DESIGN, isRegisteredDesign } from '@/themes/registry'

const THEME_STORAGE_KEY = 'lattix-theme'
const DESIGN_STORAGE_KEY = 'lattix-design-theme'

function preferredTheme(): Theme {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // Storage can be unavailable in hardened browser contexts.
  }
  return 'dark'
}

function preferredDesign(): string {
  try {
    const stored = window.localStorage.getItem(DESIGN_STORAGE_KEY)
    // 未注册（含被卸载）的主题回落到默认主题
    if (isRegisteredDesign(stored)) return stored
  } catch {
    // The active design still applies when persistence is unavailable.
  }
  return DEFAULT_DESIGN
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(preferredTheme)
  const [design, setDesignState] = useState<string>(preferredDesign)

  useLayoutEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    document.documentElement.style.colorScheme = theme
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme)
    } catch {
      // The active theme still applies when persistence is unavailable.
    }
  }, [theme])

  useLayoutEffect(() => {
    document.documentElement.dataset.theme = design
    try {
      window.localStorage.setItem(DESIGN_STORAGE_KEY, design)
    } catch {
      // The active design still applies when persistence is unavailable.
    }
  }, [design])

  const toggleTheme = useCallback(() => {
    setTheme((current) => (current === 'dark' ? 'light' : 'dark'))
  }, [])

  const setDesign = useCallback((next: DesignTheme) => {
    if (isRegisteredDesign(next)) setDesignState(next)
  }, [])

  return (
    <ThemeContext.Provider value={{ theme, setTheme, toggleTheme, design, setDesign }}>
      {children}
    </ThemeContext.Provider>
  )
}
