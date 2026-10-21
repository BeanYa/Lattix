import { useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import {
  formatTrafficLimit,
  localDateTimeToRFC3339,
  parseTrafficLimit,
  toLocalDateTimeInput,
  type TrafficUnit,
} from '@/lib/user-subscription'
import type { SubUser } from '@/lib/types'

export interface ReactivateUserFormState {
  trafficLimit: string
  trafficUnit: TrafficUnit
  expiresAt: string
}

/**
 * 到期用户「重新激活」的表单状态与提交逻辑。
 * 复用 /api/user/sub-settings（改流量 + 改有效期即触发 expired 复位、add_user 扇出
 * 与订阅重发布，试用用户同时归档清零当期流量）；sub_token/UUID 不变，订阅链接无感续用。
 */
export function useReactivateUser({
  onSaved,
  showOperation,
}: {
  onSaved: () => void
  showOperation: (opts: { observeId: string }) => void
}) {
  const [target, setTarget] = useState<SubUser | null>(null)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [form, setForm] = useState<ReactivateUserFormState>({
    trafficLimit: '',
    trafficUnit: 'GB',
    expiresAt: '',
  })

  const patch = (partial: Partial<ReactivateUserFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  const open = (u: SubUser) => {
    // 流量默认沿用之前的配额；有效期预填 +30 天（可改，精确到秒）。
    const trafficLimit = formatTrafficLimit(u.traffic_limit)
    setTarget(u)
    setForm({
      trafficLimit: trafficLimit.value,
      trafficUnit: trafficLimit.unit,
      expiresAt: toLocalDateTimeInput(new Date(Date.now() + 30 * 86400_000).toISOString()),
    })
    setErr('')
  }

  const close = () => setTarget(null)

  const onSave = async () => {
    if (!target) return
    setErr('')
    const expiresAt = localDateTimeToRFC3339(form.expiresAt)
    if (!expiresAt || new Date(form.expiresAt).getTime() <= Date.now()) {
      setErr('有效期须为未来的时间')
      return
    }
    setSaving(true)
    try {
      const trafficLimit = parseTrafficLimit(form.trafficLimit, form.trafficUnit)
      // 复合停权（expired + disabled）先解除停用，再改有效期触发恢复。
      if (target.disabled) {
        await api.setUserDisabled(target.id, false)
      }
      const { observeId } = await api.updateUserSubSettings({
        user_id: target.id,
        traffic_limit: trafficLimit,
        traffic_reset_day: target.traffic_reset_day,
        sub_title: target.sub_title,
        sub_announcement: target.sub_announcement,
        plan_name: target.plan_name,
        app_url: target.app_url,
        routing: target.routing,
        expires_at: expiresAt,
        effective_at: target.effective_at,
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

  return { target, saving, err, form, patch, open, close, onSave }
}

export type ReactivateUserController = ReturnType<typeof useReactivateUser>
