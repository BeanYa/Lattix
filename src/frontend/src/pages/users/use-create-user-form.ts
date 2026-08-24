import { useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { defaultSubscriptionRouting } from '@/lib/subscription-routing'
import {
  expiryDateDay,
  localDateToRFC3339EndOfDay,
  parseTrafficLimit,
  parseTrafficResetDay,
  type TrafficUnit,
} from '@/lib/user-subscription'
import type { ExternalSubscriptionMode, SubUser, SubscriptionRoutingProfile } from '@/lib/types'

export interface CreateUserFormState {
  name: string
  expiresAt: string
  linkSel: number[]
  trafficLimit: string
  trafficUnit: TrafficUnit
  resetDay: string
  planName: string
  appURL: string
  routing: SubscriptionRoutingProfile
  ext: Record<number, ExternalSubscriptionMode>
}

const initialCreateUserForm: CreateUserFormState = {
  name: '',
  expiresAt: '',
  linkSel: [],
  trafficLimit: '',
  trafficUnit: 'GB',
  resetDay: '',
  planName: '',
  appURL: '',
  routing: defaultSubscriptionRouting,
  ext: {},
}

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
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<SubUser | null>(null)
  const [form, setForm] = useState<CreateUserFormState>(initialCreateUserForm)

  const patch = (partial: Partial<CreateUserFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setForm(initialCreateUserForm)
      setCreateError('')
      setCreated(null)
    }
  }

  const openCreate = () => setOpen(true)

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
      resetDay: current.resetDay ? current.resetDay : expiryDateDay(value),
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
    setCreating(true)
    try {
      const trafficLimit = parseTrafficLimit(form.trafficLimit, form.trafficUnit)
      const resetDay = parseTrafficResetDay(form.resetDay)
      const planName = form.planName.trim()
      const appURL = form.appURL.trim()
      const { data: res, observeId } = await api.createUser(
        form.name.trim(),
        localDateToRFC3339EndOfDay(form.expiresAt),
        form.linkSel,
        {
          traffic_limit: trafficLimit,
          traffic_reset_day: resetDay,
          plan_name: planName,
          app_url: appURL,
          routing: form.routing,
          external_subscriptions: Object.entries(form.ext).map(([id, mode]) => ({
            subscription_id: Number(id),
            mode,
          })),
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
    creating,
    createError,
    created,
    form,
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
