import { useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import { defaultSubscriptionRouting } from '@/lib/subscription-routing'
import {
  expiryDateDay,
  formatTrafficLimit,
  localDateToRFC3339EndOfDay,
  parseTrafficLimit,
  parseTrafficResetDay,
  toLocalDateInput,
  type TrafficUnit,
} from '@/lib/user-subscription'
import type { SubUser, SubscriptionRoutingProfile } from '@/lib/types'

export interface UserSubSettingsFormState {
  expiresAt: string
  expiryTouched: boolean
  trafficLimit: string
  trafficUnit: TrafficUnit
  resetDay: string
  titleOverride: string
  announcementOverride: string
  planName: string
  appURL: string
  routing: SubscriptionRoutingProfile
}

/**
 * 用户订阅设置对话框的表单状态与保存逻辑。
 * 原为用户页内一组扁平 sub* useState，拆分时装订为单一表单状态对象。
 */
export function useUserSubSettings({
  onSaved,
  showOperation,
}: {
  onSaved: () => void
  showOperation: (opts: { observeId: string }) => void
}) {
  const [target, setTarget] = useState<SubUser | null>(null)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [form, setForm] = useState<UserSubSettingsFormState>({
    expiresAt: '',
    expiryTouched: false,
    trafficLimit: '',
    trafficUnit: 'GB',
    resetDay: '',
    titleOverride: '',
    announcementOverride: '',
    planName: '',
    appURL: '',
    routing: defaultSubscriptionRouting,
  })

  const patch = (partial: Partial<UserSubSettingsFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  const open = (u: SubUser) => {
    const trafficLimit = formatTrafficLimit(u.traffic_limit)
    setTarget(u)
    setForm({
      expiresAt: u.expires_at ? toLocalDateInput(u.expires_at) : '',
      expiryTouched: false,
      trafficLimit: trafficLimit.value,
      trafficUnit: trafficLimit.unit,
      resetDay: u.traffic_reset_day > 0 ? String(u.traffic_reset_day) : '',
      titleOverride: u.sub_title,
      announcementOverride: u.sub_announcement,
      planName: u.plan_name,
      appURL: u.app_url,
      routing: u.routing,
    })
    setErr('')
  }

  const close = () => setTarget(null)

  const onExpiresAtChange = (value: string) => {
    setForm((current) => ({
      ...current,
      expiresAt: value,
      expiryTouched: true,
      resetDay: current.resetDay ? current.resetDay : expiryDateDay(value),
    }))
  }

  const clearExpiresAt = () => {
    setForm((current) => ({ ...current, expiresAt: '', expiryTouched: true }))
  }

  const onSave = async () => {
    if (!target) return
    setErr('')
    setSaving(true)
    try {
      const trafficLimit = parseTrafficLimit(form.trafficLimit, form.trafficUnit)
      const resetDay = parseTrafficResetDay(form.resetDay)
      const { observeId } = await api.updateUserSubSettings({
        user_id: target.id,
        traffic_limit: trafficLimit,
        traffic_reset_day: resetDay,
        sub_title: form.titleOverride,
        sub_announcement: form.announcementOverride,
        plan_name: form.planName,
        app_url: form.appURL,
        routing: form.routing,
        expires_at: form.expiryTouched
          ? localDateToRFC3339EndOfDay(form.expiresAt)
          : target.expires_at,
      })
      if (observeId) showOperation({ observeId })
      setTarget(null)
      onSaved()
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setSaving(false)
    }
  }

  return {
    target,
    saving,
    err,
    form,
    patch,
    open,
    close,
    onExpiresAtChange,
    clearExpiresAt,
    onSave,
  }
}

export type UserSubSettingsController = ReturnType<typeof useUserSubSettings>
