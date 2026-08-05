import type { SubscriptionRoutingProfile } from '@/lib/types'

export const defaultSubscriptionRouting: SubscriptionRoutingProfile = {
  mode: 'suggested',
  preset: 'balanced',
  categories: ['ai', 'youtube', 'google', 'private', 'domestic', 'telegram', 'github', 'overseas'],
  portable_template_id: '',
  mihomo_template_id: '',
  singbox_template_id: '',
  quanx_template_id: '',
  assigned_portable_template_id: '',
  assign_forced_portable: false,
  assigned_mihomo_template_id: '',
  assign_forced_mihomo: false,
  assigned_singbox_template_id: '',
  assign_forced_singbox: false,
  assigned_quanx_template_id: '',
  assign_forced_quanx: false,
}
