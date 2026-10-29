/** SubRoutingConfig 是订阅页模板配置的一级视图（用户级原始值 / 实际生效值）。 */
export interface SubRoutingConfig {
  mode: 'suggested' | 'template'
  preset: 'minimal' | 'balanced' | 'comprehensive'
  categories: string[]
  portable_template_id: string
  mihomo_template_id: string
  singbox_template_id: string
  quanx_template_id: string
}

export interface SubTemplateOption {
  id: string
  name: string
  kind: 'portable' | 'acl4ssr' | 'mihomo' | 'singbox' | 'quanx'
}

export interface SubRuleCategory {
  id: string
  label: string
  icon: string
  in_minimal: boolean
  in_balanced: boolean
}

/** SubTemplateConfig 是 GET/POST /api/sub/{token}/template-config 的响应。 */
export interface SubTemplateConfig {
  user_configured: boolean
  effective_source: 'forced' | 'user' | 'panel' | 'default' | 'unset'
  region_groups_enabled: boolean
  forced_portable: boolean
  forced_mihomo: boolean
  forced_singbox: boolean
  forced_quanx: boolean
  forced_summary?: string
  panel_assigned_summary?: string
  default_summary?: string
  assigned_mihomo_template_id?: string
  assigned_singbox_template_id?: string
  assigned_quanx_template_id?: string
  user: SubRoutingConfig
  effective: SubRoutingConfig
  templates: SubTemplateOption[]
  categories: SubRuleCategory[]
}

export const PRESET_OPTIONS = [
  ['minimal', '极简规则'],
  ['balanced', '均衡规则'],
  ['comprehensive', '完整规则'],
] as const

export type PresetId = (typeof PRESET_OPTIONS)[number][0]

/** FormValues 是模板配置卡表单的初值（编辑态）。 */
export interface FormValues {
  mode: SubRoutingConfig['mode']
  preset: PresetId
  categories: string[]
  portableId: string
  mihomoId: string
  singboxId: string
  quanxId: string
  regionGroups: boolean
}

export function categoriesForPreset(preset: PresetId, categories: SubRuleCategory[]) {
  if (preset === 'comprehensive') return categories.map((category) => category.id)
  return categories
    .filter((category) => (preset === 'minimal' ? category.in_minimal : category.in_balanced))
    .map((category) => category.id)
}

export function presetOfCategories(ids: string[], categories: SubRuleCategory[]): PresetId {
  const set = new Set(ids)
  for (const [preset] of PRESET_OPTIONS) {
    if (
      set.size === categoriesForPreset(preset, categories).length &&
      categoriesForPreset(preset, categories).every((id) => set.has(id))
    ) {
      return preset
    }
  }
  return 'balanced'
}

/**
 * formValuesOf 计算表单初值：
 * - 主策略槽位（规则来源 / 预设 / 生效分组 / 中立模板）与地区分组开关：用户已自选
 *   （user_configured）时用用户级原始值，否则用实际生效配置（面板级 → 默认级 →
 *   内置默认）。用户级缺席时若仍展示用户级残影（例如「恢复默认」后数据库里残留的
 *   占位值），页面上的分组与地区分组开关就会和订阅文件不一致，因此必须跟随生效值。
 * - 客户端原生模板槽位保持用户级原始值：空值 = 「跟随主策略」，这是后端的兜底语义；
 *   管理员非强制指派/强制指派分别由提示行与禁用态说明，回填成「已选中」会让用户
 *   误以为是自己选的，保存时还会把管理员默认固化成用户级。
 */
export function formValuesOf(config: SubTemplateConfig): FormValues {
  const source = config.user_configured ? config.user : config.effective
  const native = config.user
  return {
    mode: source.mode,
    preset: presetOfCategories(source.categories, config.categories),
    categories: source.categories,
    portableId: source.portable_template_id,
    mihomoId: native.mihomo_template_id,
    singboxId: native.singbox_template_id,
    quanxId: native.quanx_template_id,
    regionGroups: config.region_groups_enabled !== false,
  }
}

/** effectiveSummary 概括实际生效的主策略配置，供头部展示。 */
export function effectiveSummary(config: SubRoutingConfig, templates: SubTemplateOption[]): string {
  if (config.mode === 'template') {
    const template = templates.find((item) => item.id === config.portable_template_id)
    return `模板「${template?.name ?? config.portable_template_id}」`
  }
  return `建议规则 · ${config.categories.length} 个分组`
}
