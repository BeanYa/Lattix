import { lazy, Suspense, useMemo } from 'react'

import { useTheme } from '@/lib/theme-context'
import { findDesign } from '@/themes/registry'

const DashboardHig = lazy(() => import('./dashboard/DashboardHig'))

/**
 * 仪表盘插槽分发：当前主题声明了 dashboard 覆写时渲染主题实现
 * （懒加载，未启用的主题不进入首屏 bundle），否则回落到基础实现。
 */
export default function Dashboard() {
  const { design } = useTheme()
  const loader = findDesign(design)?.overrides?.dashboard

  const DashboardOverride = useMemo(
    () => (loader ? lazy(loader) : null),
    // loader 由注册表静态决定，随 design 变化
    [loader],
  )

  return (
    <Suspense fallback={null}>
      {DashboardOverride ? <DashboardOverride /> : <DashboardHig />}
    </Suspense>
  )
}
