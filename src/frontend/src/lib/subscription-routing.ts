import type { SubscriptionRoutingProfile } from '@/lib/types'

/** 空的路由表单值：建议模式 + 未选分组 = 未配置面板级（回落默认级）。 */
export const defaultSubscriptionRouting: SubscriptionRoutingProfile = {
  region_groups_enabled: true,
  mode: 'suggested',
  preset: 'balanced',
  categories: [],
  portable_template_id: '',
  mihomo_template_id: '',
  singbox_template_id: '',
  quanx_template_id: '',
  user_configured: false,
  assigned_portable_template_id: '',
  assign_forced_portable: false,
  assigned_mihomo_template_id: '',
  assign_forced_mihomo: false,
  assigned_singbox_template_id: '',
  assign_forced_singbox: false,
  assigned_quanx_template_id: '',
  assign_forced_quanx: false,
  assigned_suggested_categories: [],
}

/**
 * routingFormFromProfile 从用户 routing DTO 折算路由表单初值：
 * 表单编辑的是面板级/强制级槽位（assigned_*），用户级字段面板侧只读。
 * forced 初值反映当前是否处于强制覆盖。
 */
export function routingFormFromProfile(
  routing: SubscriptionRoutingProfile,
): SubscriptionRoutingProfile {
  const hasSuggested = (routing.assigned_suggested_categories ?? []).length > 0
  const hasPortable = Boolean(routing.assigned_portable_template_id)
  return {
    ...routing,
    mode: hasSuggested || !hasPortable ? 'suggested' : 'template',
    categories: hasSuggested ? [...routing.assigned_suggested_categories] : [],
    portable_template_id: hasPortable ? routing.assigned_portable_template_id : '',
    mihomo_template_id: routing.assigned_mihomo_template_id,
    singbox_template_id: routing.assigned_singbox_template_id,
    quanx_template_id: routing.assigned_quanx_template_id,
    forced:
      routing.assign_forced_portable ||
      routing.assign_forced_mihomo ||
      routing.assign_forced_singbox ||
      routing.assign_forced_quanx,
  }
}
