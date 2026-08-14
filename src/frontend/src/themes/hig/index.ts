import type { ThemeDefinition } from '@/themes/types'

/**
 * 默认主题：Apple HIG 设计语言。
 * 令牌即 index.css / cream-grid.css 的根令牌（:root 与 .dark），
 * 所有页面组件的基础实现就是按这套主题编写的，因此无需任何覆写。
 */
export const hig: ThemeDefinition = {
  id: 'hig',
  label: 'Apple HIG',
  description: '默认设计语言',
}
