import type { ThemeDefinition } from '@/themes/types'
import { THEME_ID_PATTERN } from '@/themes/types'
import { hig } from '@/themes/hig'
import { cream } from '@/themes/cream'

/**
 * 已安装主题的注册表，切换菜单与主题分发均由此驱动。
 *
 * 安装一个新主题：
 * 1. 新建 src/themes/<id>/ 目录，编写 index.ts 导出 ThemeDefinition；
 *    令牌覆写写入同目录 tokens.css（选择器用 [data-theme="<id>"]，
 *    暗色用 [data-theme="<id>"].dark），并在 index.ts 顶部 import。
 * 2. 若主题重写了某个页面的标记，把组件放进主题目录并在 overrides
 *    中声明对应插槽的懒加载。
 * 3. 在下方数组中注册导出的清单，切换菜单即自动出现该主题。
 * 详见 src/themes/README.md。
 */
export const DEFAULT_DESIGN = 'hig'

export const DESIGN_THEMES: ThemeDefinition[] = [hig, cream]

export function findDesign(id: string | undefined | null): ThemeDefinition | undefined {
  return DESIGN_THEMES.find((theme) => theme.id === id)
}

export function isRegisteredDesign(id: unknown): id is string {
  return typeof id === 'string' && THEME_ID_PATTERN.test(id) && findDesign(id) !== undefined
}
