import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { loadCities, loadCountries, type CountryOption } from '@/lib/geography'
import type { CreateServerResponse, MachineType } from '@/lib/types'

import {
  billingPayload,
  defaultBilling,
  defaultTraffic,
  parsePortRows,
  trafficPayload,
  type BillingFormState,
  type ServerFormPayload,
  type TrafficFormState,
} from './server-form-utils'

export interface ServerCreateFormState {
  alias: string
  address: string
  machineType: MachineType
  tags: string[]
  countryCode: string
  location: string
  portRows: string[]
  billing: BillingFormState
  traffic: TrafficFormState
}

function initialServerCreateForm(): ServerCreateFormState {
  return {
    alias: '',
    address: '',
    machineType: 'direct',
    tags: [],
    countryCode: '',
    location: '',
    portRows: [''],
    billing: defaultBilling(),
    traffic: defaultTraffic(),
  }
}

/**
 * 添加服务器对话框的表单状态与提交逻辑。
 * 原为服务器页内一组扁平 useState，拆分时装订为单一表单状态对象。
 */
export function useServerCreateForm({
  onCreated,
  reload,
}: {
  onCreated: (res: CreateServerResponse) => void
  reload: () => void
}) {
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [form, setForm] = useState<ServerCreateFormState>(initialServerCreateForm)
  const [countryOptions, setCountryOptions] = useState<CountryOption[]>([])
  const [cities, setCities] = useState<string[]>([])

  const patch = (partial: Partial<ServerCreateFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  useEffect(() => {
    if (!open) return
    loadCountries()
      .then(setCountryOptions)
      .catch(() => setCountryOptions([]))
  }, [open])

  useEffect(() => {
    if (!form.countryCode) {
      setCities([])
      return
    }
    loadCities(form.countryCode)
      .then(setCities)
      .catch(() => setCities([]))
  }, [form.countryCode])

  const citySuggestions = useMemo(() => {
    const query = form.location.trim().toLocaleLowerCase()
    return cities.filter((city) => !query || city.toLocaleLowerCase().includes(query)).slice(0, 30)
  }, [cities, form.location])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setForm(initialServerCreateForm())
      setCreateError('')
    }
  }

  const openCreate = () => setOpen(true)

  const setCountry = (value: string) => patch({ countryCode: value, location: '' })

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const body: ServerFormPayload = {
      alias: form.alias.trim(),
      machine_type: form.machineType,
      country_code: form.countryCode,
      location: form.location.trim(),
      billing: billingPayload(form.billing),
      traffic_plan: trafficPayload(form.traffic),
    }
    body.tags = form.tags
    if (form.address.trim()) {
      body.address = form.address.trim()
    }
    if (form.machineType === 'nat') {
      if (!form.address.trim()) {
        setCreateError('NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）')
        return
      }
      const parsed = parsePortRows(form.portRows)
      if ('error' in parsed) {
        setCreateError(parsed.error)
        return
      }
      body.allowed_ports = parsed.ranges
    }
    setCreating(true)
    try {
      const res = await api.createServer(body)
      onOpenChange(false)
      onCreated(res)
      reload()
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
    form,
    patch,
    countryOptions,
    citySuggestions,
    openCreate,
    onOpenChange,
    setCountry,
    onCreate,
  }
}

export type ServerCreateFormController = ReturnType<typeof useServerCreateForm>
