import type { ThemeDefinition } from '@/themes/types'

import './tokens.css'
import './base.css'
import './pages.css'

export const patchbay: ThemeDefinition = {
  id: 'patchbay',
  label: 'Signal Rack',
  description: '模块化信号台与路由矩阵',
  overrides: {
    dashboard: () => import('./DashboardPatchbay'),
  },
}
