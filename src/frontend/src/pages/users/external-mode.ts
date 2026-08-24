import type { ExternalSubscriptionMode } from '@/lib/types'

export const EXTERNAL_MODE_LABELS: Record<ExternalSubscriptionMode, string> = {
  stack: '叠加',
  merge: '并入',
  nodes: '附加',
}
