import './tokens.css'
import './base.css'
import './pages.css'

import type { ThemeDefinition } from '@/themes/types'

/**
 * 可选主题：Cream Grid（重构前的经典设计，取自 main 分支）。
 * - tokens.css：全量语义令牌覆写（亮/暗）；
 * - base.css：组件基础层与基础样式（cream-grid.css 类规则 + index.css
 *   的 @layer base/components 等）整层作用域化到 [data-theme="cream"]，
 *   恢复经典设计的粗描边/硬阴影/网格肌理；
 * - pages.css：与默认主题存在样式差异的页面整层覆写；
 * - Dashboard 标记重写差异较大，走 overrides 插槽整体替换。
 * 所有 keyframe 已加 cream- 前缀，避免全局污染默认主题。
 */
export const cream: ThemeDefinition = {
  id: 'cream',
  label: 'Cream Grid',
  description: '经典奶油网格设计',
  overrides: {
    dashboard: () => import('./DashboardCream'),
  },
}
