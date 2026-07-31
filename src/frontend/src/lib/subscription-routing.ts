import type { SubscriptionRoutingProfile } from '@/lib/types'

export const defaultSubscriptionRouting: SubscriptionRoutingProfile = {
  mode: 'suggested',
  preset: 'balanced',
  categories: ['ai', 'youtube', 'google', 'private', 'domestic', 'telegram', 'github', 'overseas'],
  portable_template_id: '',
  mihomo_template_id: '',
  singbox_template_id: '',
  quanx_template_id: '',
}
