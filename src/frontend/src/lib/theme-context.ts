import { createContext, useContext } from 'react'

export type Theme = 'light' | 'dark'

/**
 * 设计主题 id（<html data-theme> 取值）。内置主题有字面量提示，
 * 经注册表安装的自定义主题同样接受任意合法 id。
 */
export type DesignTheme = 'hig' | 'cream' | (string & {})

export interface ThemeState {
  theme: Theme
  setTheme: (theme: Theme) => void
  toggleTheme: () => void
  design: string
  setDesign: (design: DesignTheme) => void
}

export const ThemeContext = createContext<ThemeState | undefined>(undefined)

export function useTheme(): ThemeState {
  const context = useContext(ThemeContext)
  if (!context) throw new Error('useTheme must be used within ThemeProvider')
  return context
}
