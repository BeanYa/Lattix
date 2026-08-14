import './tokens.css'

import type { ThemeDefinition } from '@/themes/types'

/**
 * 可选主题：Cream Grid（重构前的经典设计，取自 main 分支）。
 * tokens.css 覆写全量语义令牌；Dashboard 标记与样式差异较大，
 * 因此提供页面级覆写。
 */
export const cream: ThemeDefinition = {
  id: 'cream',
  label: 'Cream Grid',
  description: '经典奶油网格设计',
  overrides: {
    dashboard: () => import('./DashboardCream'),
  },
}
