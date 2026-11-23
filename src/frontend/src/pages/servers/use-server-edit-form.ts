import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { ApiError, api, errorMessage } from '@/lib/api'
import { loadCities, loadCountries, type CountryOption } from '@/lib/geography'
import { formatPortRange } from '@/lib/ports'
import type { Server, UpdateServerResponse } from '@/lib/types'

import {
  addInterval,
  addrCandidates,
  aliasAfterParse,
  applyExternalLinkError,
  applyExternalLinkPreview,
  buildServerEditPayload,
  editExternalLink,
  initialExternalLink,
  localDate,
  serverTypeOf,
  type ServerEditFormState,
  type ServerTypeChoice,
} from './server-form-utils'

import {
  defaultExternalExitForm,
  externalExitFormFromServer,
} from './external-exit-form'

export type { ServerEditFormState }

function initialServerEditForm(): ServerEditFormState {
  return {
    serverType: 'direct',
    alias: '',
    addresses: [],
    defaultAddr: '',
    addrInput: '',
    portRows: [''],
    tags: [],
    countryCode: '',
    location: '',
    nonDirect: false,
    billing: {
      enabled: false,
      providerId: '',
      amount: '',
      currency: 'CNY',
      startedOn: '',
      intervalCount: 1,
      intervalUnit: 'month',
      renewalOn: '',
    },
    traffic: {
      limited: false,
      quota: '1000',
      quotaUnit: 'GB',
      accountingMode: 'outbound',
      anchorOn: '',
      resetCount: 1,
      resetUnit: 'month',
    },
    xrayOverride: '',
    xrayVersions: ['latest'],
    external: initialExternalLink(),
  }
}

/**
 * 编辑服务器对话框的表单状态与提交逻辑。
 * 支持独立 IP / NAT / 外部直链三类互转，提交统一走 api.updateServer。
 */
export function useServerEditForm({
  reload,
  onUpdated,
}: {
  reload: () => void
  onUpdated?: (res: UpdateServerResponse) => void
}) {
  const [target, setTarget] = useState<Server | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [linkConflict, setLinkConflict] = useState(false)
  const [form, setForm] = useState<ServerEditFormState>(initialServerEditForm)
  const [countryOptions, setCountryOptions] = useState<CountryOption[]>([])
  const [cities, setCities] = useState<string[]>([])

  const patch = (partial: Partial<ServerEditFormState>) =>
    setForm((current) => ({ ...current, ...partial }))

  useEffect(() => {
    if (!target) return
    loadCountries()
      .then(setCountryOptions)
      .catch(() => setCountryOptions([]))
  }, [target])

  const targetId = target?.id ?? null
  const needXrayVersions = targetId !== null && form.serverType !== 'external'
  useEffect(() => {
    if (!needXrayVersions) return
    let active = true
    api
      .releaseVersions('xray')
      .then((versions) => {
        if (active) {
          setForm((current) => ({ ...current, xrayVersions: versions.versions }))
        }
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [targetId, needXrayVersions])

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

  // 「添加地址」下拉候选：agent 上报地址中尚未列入清单的项
  // （含私网/链路本地，局域网场景可用；是否选用由管理员判断）。
  const addCandidates = target
    ? addrCandidates(target).filter((c) => !form.addresses.includes(c))
    : []

  // 添加地址：去重；列表为空时新条目自动成为默认地址。
  const addAddress = (raw: string) => {
    const addr = raw.trim()
    if (!addr || form.addresses.includes(addr)) {
      return
    }
    patch({
      addresses: [...form.addresses, addr],
      defaultAddr: form.addresses.length === 0 || !form.defaultAddr ? addr : form.defaultAddr,
      addrInput: '',
    })
  }

  const setCountry = (value: string) => patch({ countryCode: value, location: '' })

  const setExternalLink = (link: string) => {
    // 链接改动后清掉上一次提交的错误与冲突提示，避免与最新解析状态矛盾。
    setError('')
    setLinkConflict(false)
    setForm((current) => ({ ...current, external: editExternalLink(current.external, link) }))
  }

  const setExternalExit = (partial: Partial<typeof form.external.exit>) => {
    setError('')
    setForm((current) => ({
      ...current,
      external: { ...current.external, exit: { ...current.external.exit, ...partial } },
    }))
  }

  const parseExternalLink = async () => {
    const link = form.external.link.trim()
    if (!link) {
      return
    }
    setForm((current) => ({
      ...current,
      external: { ...current.external, parsing: true, parseError: '' },
    }))
    try {
      const preview = await api.parseExternalLink(link)
      setError('')
      setLinkConflict(false)
      setForm((current) => {
        const external = applyExternalLinkPreview(current.external, link, preview)
        return external
          ? { ...current, alias: aliasAfterParse(current.alias, preview.name), external }
          : current
      })
    } catch (err) {
      setForm((current) => {
        const external = applyExternalLinkError(current.external, link, errorMessage(err))
        return external ? { ...current, external } : current
      })
    }
  }

  const setServerType = (serverType: ServerTypeChoice) => {
    setError('')
    setLinkConflict(false)
    patch({ serverType })
  }

  const openEdit = (s: Server) => {
    setTarget(s)
    const serverType = serverTypeOf(s)
    // 外部直链节点的 server.address 保存的是外部出口域名/IP，不应预填成托管服务器公网地址候选。
    const initialAddrs =
      s.kind === 'external'
        ? []
        : s.addresses.length > 0
          ? s.addresses
          : [...new Set([s.address, s.learned_addr].filter(Boolean))]
    const divisor = ['JPY', 'KRW', 'ISK'].includes(s.billing.currency) ? 1 : 100
    const quota = s.traffic_plan.quota_bytes
    const quotaUnit: 'GB' | 'TB' = quota !== null && quota >= 1e12 ? 'TB' : 'GB'
    setForm({
      serverType,
      alias: s.alias,
      addresses: initialAddrs,
      defaultAddr: s.kind === 'external' ? '' : s.address,
      addrInput: '',
      portRows: s.allowed_ports.length > 0 ? s.allowed_ports.map(formatPortRange) : [''],
      tags: s.tags,
      countryCode: s.country_code,
      location: s.location,
      nonDirect: s.non_direct,
      billing: {
        enabled: s.billing.enabled,
        providerId: s.billing.provider ? String(s.billing.provider.id) : '',
        amount: String(s.billing.amount_minor / divisor),
        currency: s.billing.currency || 'CNY',
        startedOn: s.billing.service_started_on || localDate(),
        intervalCount: s.billing.interval_count || 1,
        intervalUnit: s.billing.interval_unit || 'month',
        renewalOn: s.billing.next_renewal_on || addInterval(localDate(), 1, 'month'),
      },
      traffic: {
        limited: quota !== null,
        quota: quota === null ? '1000' : String(quota / (quotaUnit === 'TB' ? 1e12 : 1e9)),
        quotaUnit,
        accountingMode: s.traffic_plan.accounting_mode,
        anchorOn: s.traffic_plan.reset_anchor_on || localDate(),
        resetCount: s.traffic_plan.reset_count || 1,
        resetUnit: s.traffic_plan.reset_unit || 'month',
      },
      xrayOverride: s.custom_settings?.xray_version ?? '',
      xrayVersions: ['latest'],
      external:
        s.kind === 'external'
          ? initialExternalLink(s.external_link ?? '', externalExitFormFromServer(s))
          : initialExternalLink('', defaultExternalExitForm()),
    })
    setError('')
    setLinkConflict(false)
  }

  const closeEdit = () => setTarget(null)

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!target) {
      return
    }
    setError('')
    setLinkConflict(false)
    const built = buildServerEditPayload(target, form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setSaving(true)
    try {
      const res = await api.updateServer(built.body)
      setTarget(null)
      onUpdated?.(res)
      reload()
    } catch (err) {
      // 外部直链协议变更被链路引用等冲突场景由后端 409/CONFLICT 返回：原样展示服务端消息。
      setError(errorMessage(err))
      setLinkConflict(
        err instanceof ApiError && (err.code === 'CONFLICT' || err.httpStatus === 409),
      )
    } finally {
      setSaving(false)
    }
  }

  return {
    target,
    saving,
    error,
    linkConflict,
    form,
    patch,
    countryOptions,
    citySuggestions,
    addCandidates,
    addAddress,
    setServerType,
    setCountry,
    setExternalLink,
    setExternalExit,
    parseExternalLink,
    openEdit,
    closeEdit,
    onSubmit,
  }
}

export type ServerEditFormController = ReturnType<typeof useServerEditForm>
