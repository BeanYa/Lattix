import type { ComponentType } from 'react'

/**
 * 允许被主题覆写的页面级插槽。
 * 基础实现位于 src/pages/，主题在清单中声明同名插槽即可整体替换该页面。
 */
export type ThemeSlot = 'dashboard'

/**
 * 主题（设计语言）清单。一个主题 = 一个 themes/<id>/ 目录 + 一条注册表记录。
 *
 * - 令牌层：默认主题（hig）直接使用 index.css / cream-grid.css 的根令牌，
 *   其余主题通过 tokens.css 以 [data-theme="<id>"] 覆写同名令牌生效，
 *   覆盖亮/暗两套即可，组件类零改动。
 * - 结构层：若某主题对页面标记做了重写（如 cream 的 Dashboard），
 *   通过 overrides 声明懒加载组件；未声明的插槽回落到基础实现。
 */
export interface ThemeDefinition {
  /** 主题 id，同时是 <html data-theme> 的取值，仅限小写字母/数字/连字符 */
  id: string
  /** 切换菜单中展示的名称 */
  label: string
  /** 可选的主题说明，作为菜单项提示 */
  description?: string
  /**
   * 页面级覆写。key 为插槽名，value 为返回默认导出组件的动态 import，
   * 保持懒加载以便未启用的主题不进入首屏 bundle。
   */
  overrides?: Partial<Record<ThemeSlot, () => Promise<{ default: ComponentType }>>>
}

export const THEME_ID_PATTERN = /^[a-z][a-z0-9-]*$/
