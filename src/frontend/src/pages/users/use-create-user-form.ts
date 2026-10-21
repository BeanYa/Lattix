import { useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { defaultSubscriptionRouting } from '@/lib/subscription-routing'
import {
  expiryDateTimeDay,
  localDateTimeToRFC3339,
  parseDurationDays,
  parseTrafficLimit,
  parseTrafficResetDay,
  type TrafficUnit,
} from '@/lib/user-subscription'
import type {
  ExternalSubscriptionMode,
  SubUser,
  SubscriptionRoutingProfile,
  UserGroup,
} from '@/lib/types'

/** 生效模式：立即 或 定时（datetime-local，精确到秒）。 */
export type EffectiveMode = 'now' | 'scheduled'
/** 有效期模式：长期、指定到期时间（精确到秒）或按天数（自生效时刻起算）。 */
export type ExpiryMode = 'never' | 'until' | 'days'
/** 创建模式：普通用户（长期/到期可选）或试用用户（必须有效期，到期自动删除）。 */
export type CreateUserMode = 'normal' | 'trial'

export interface CreateUserFormState {
  name: string
  userGroupId: string
  effectiveMode: EffectiveMode
  effectiveAt: string
  expiryMode: ExpiryMode
  expiresAt: string
  durationDays: string
  linkSel: number[]
  trafficLimit: string
  trafficUnit: TrafficUnit
  resetDay: string
  planName: string
  appURL: string
  routing: SubscriptionRoutingProfile
  ext: Record<number, ExternalSubscriptionMode>
}

const initialCreateUserForm = (mode: CreateUserMode): CreateUserFormState => ({
  name: '',
  userGroupId: '',
  effectiveMode: 'now',
  effectiveAt: '',
  // 试用用户必须设置有效期；默认按天数 1 天，普通用户默认长期。
  expiryMode: mode === 'trial' ? 'days' : 'never',
  expiresAt: '',
  durationDays: mode === 'trial' ? '1' : '',
  linkSel: [],
  trafficLimit: '',
  trafficUnit: 'GB',
  resetDay: '',
  planName: '',
  appURL: '',
  routing: defaultSubscriptionRouting,
  ext: {},
})

/**
 * 创建用户对话框的表单状态与提交逻辑。
 * 原为用户页内一组扁平 useState，拆分时装订为单一表单状态对象。
 */
export function useCreateUserForm({
  onSaved,
  showOperation,
}: {
  onSaved: () => void
  showOperation: (opts: { observeId: string }) => void
}) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<CreateUserMode>('normal')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<SubUser | null>(null)
  const [form, setForm] = useState<CreateUserFormState>(() => initialCreateUserForm('normal'))
  const [groups, setGroups] = useState<UserGroup[]>([])

  const patch = (partial: Partial<CreateUserFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setMode('normal')
      setForm(initialCreateUserForm('normal'))
      setCreateError('')
      setCreated(null)
    }
  }

  const openCreate = (nextMode: CreateUserMode = 'normal') => {
    setMode(nextMode)
    setForm(initialCreateUserForm(nextMode))
    setOpen(true)
    // 打开即拉取用户分组（创建时直选分组，§分组）；失败静默，分组选项留空。
    api
      .userGroups()
      .then(setGroups)
      .catch(() => setGroups([]))
  }

  const onToggleCreateLink = (id: number, checked: boolean) => {
    setForm((current) => ({
      ...current,
      linkSel: checked ? [...current.linkSel, id] : current.linkSel.filter((x) => x !== id),
    }))
  }

  const onExpiresAtChange = (value: string) => {
    setForm((current) => ({
      ...current,
      expiresAt: value,
      resetDay: current.resetDay ? current.resetDay : expiryDateTimeDay(value),
    }))
  }

  const toggleExt = (id: number, checked: boolean) => {
    setForm((current) => {
      const ext = { ...current.ext }
      if (checked) {
        ext[id] = 'stack'
      } else {
        delete ext[id]
      }
      return { ...current, ext }
    })
  }

  const setExtMode = (id: number, mode: ExternalSubscriptionMode) => {
    setForm((current) => ({ ...current, ext: { ...current.ext, [id]: mode } }))
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    let trafficLimit: number
    let durationDays = 0
    try {
      trafficLimit = parseTrafficLimit(form.trafficLimit, form.trafficUnit)
      if (form.expiryMode === 'days') {
        durationDays = parseDurationDays(form.durationDays)
      }
    } catch (err) {
      setCreateError(errorMessage(err))
      return
    }
    if (mode === 'trial' && form.expiryMode === 'never') {
      setCreateError('试用用户必须设置有效期（按天数或到期时间）')
      return
    }
    setCreating(true)
    try {
      const effectiveAt =
        form.effectiveMode === 'scheduled' ? localDateTimeToRFC3339(form.effectiveAt) : null
      const expiresAt = form.expiryMode === 'until' ? localDateTimeToRFC3339(form.expiresAt) : null
      const { data: res, observeId } = await api.createUser(
        form.name.trim(),
        expiresAt,
        form.linkSel,
        {
          traffic_limit: trafficLimit,
          traffic_reset_day: parseTrafficResetDay(form.resetDay),
          plan_name: form.planName.trim(),
          app_url: form.appURL.trim(),
          routing: form.routing,
          external_subscriptions: Object.entries(form.ext).map(([id, extMode]) => ({
            subscription_id: Number(id),
            mode: extMode,
          })),
          is_trial: mode === 'trial',
          ...(effectiveAt ? { effective_at: effectiveAt } : {}),
          ...(form.expiryMode === 'days' ? { duration_days: durationDays } : {}),
          ...(form.userGroupId ? { user_group_id: Number(form.userGroupId) } : {}),
        },
      )
      if (observeId) showOperation({ observeId })
      setCreated(res)
      onSaved()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  return {
    open,
    mode,
    creating,
    createError,
    created,
    form,
    groups,
    patch,
    openCreate,
    onOpenChange,
    onToggleCreateLink,
    onExpiresAtChange,
    toggleExt,
    setExtMode,
    onCreate,
  }
}

export type CreateUserFormController = ReturnType<typeof useCreateUserForm>
