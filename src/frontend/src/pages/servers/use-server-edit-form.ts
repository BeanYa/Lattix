import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { ApiError, api, errorMessage } from '@/lib/api'
import { loadCities, loadCountries, type CountryOption } from '@/lib/geography'
import { formatPortRange } from '@/lib/ports'
import type { PortRange, Server } from '@/lib/types'

import {
  addInterval,
  addrCandidates,
  aliasAfterParse,
  applyExternalLinkError,
  applyExternalLinkPreview,
  billingPayload,
  editExternalLink,
  initialExternalLink,
  localDate,
  parsePortRows,
  trafficPayload,
  type BillingFormState,
  type ExternalLinkState,
  type TrafficFormState,
} from './server-form-utils'

import {
  defaultExternalExitForm,
  externalExitFormError,
  externalExitFormFromServer,
  externalExitFormPayload,
} from './external-exit-form'

export interface ServerEditFormState {
  alias: string
  addresses: string[]
  defaultAddr: string
  addrInput: string
  portRows: string[]
  tags: string[]
  countryCode: string
  location: string
  nonDirect: boolean
  billing: BillingFormState
  traffic: TrafficFormState
  xrayOverride: string
  xrayVersions: string[]
  external: ExternalLinkState
}

function initialServerEditForm(): ServerEditFormState {
  return {
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
 * 原为服务器页内一组扁平 useState，拆分时装订为单一表单状态对象；
 * 打开对话框时由 openEdit 按目标服务器整体回填。未托管外部直链节点（kind=external）
 * 只维护链接与资料，保存走 updateServerExternal；被链路引用时的链接变更由后端 409 兜底。
 */
export function useServerEditForm({ reload }: { reload: () => void }) {
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

  const openEdit = (s: Server) => {
    setTarget(s)
    // 地址列表回填：优先 server.addresses，空则回退默认/学习地址（去重保序）。
    const initialAddrs =
      s.addresses.length > 0
        ? s.addresses
        : [...new Set([s.address, s.learned_addr].filter(Boolean))]
    const divisor = ['JPY', 'KRW', 'ISK'].includes(s.billing.currency) ? 1 : 100
    const quota = s.traffic_plan.quota_bytes
    const quotaUnit: 'GB' | 'TB' = quota !== null && quota >= 1e12 ? 'TB' : 'GB'
    setForm({
      alias: s.alias,
      addresses: initialAddrs,
      defaultAddr: s.address,
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
    if (s.kind !== 'external') {
      api
        .releaseVersions('xray')
        .then((versions) => patch({ xrayVersions: versions.versions }))
        .catch(() => {})
    }
    setError('')
    setLinkConflict(false)
  }

  const closeEdit = () => setTarget(null)

  // 未托管外部直链节点：提交协议表单（地址由规格派生），没有 Agent 字段；
  // 协议参数变更且被链路引用时由后端 409 拒绝。
  const saveExternal = async (s: Server, alias: string) => {
    const exitError = externalExitFormError(form.external.exit)
    if (exitError) {
      setError(exitError)
      return
    }
    setSaving(true)
    try {
      await api.updateServerExternal(
        s.id,
        alias,
        form.tags,
        form.countryCode,
        form.location.trim(),
        billingPayload(form.billing),
        trafficPayload(form.traffic),
        form.nonDirect,
        externalExitFormPayload(form.external.exit),
        form.external.link.trim(),
      )
      setTarget(null)
      reload()
    } catch (err) {
      // 被链路引用时的链接变更由后端 409 拒绝：原样展示服务端消息并提示处理方式。
      // 后端把 HTTP 409 映射为 RPC 业务码 CONFLICT（信封走 HTTP 200），两种形态都识别。
      setError(errorMessage(err))
      setLinkConflict(
        err instanceof ApiError && (err.code === 'CONFLICT' || err.httpStatus === 409),
      )
    } finally {
      setSaving(false)
    }
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!target) {
      return
    }
    setError('')
    setLinkConflict(false)
    // 未托管节点没有必填项：别名留空由后端按地址派生，国家/地区可空。
    if (target.kind === 'external') {
      await saveExternal(target, form.alias.trim())
      return
    }
    const finalAlias = form.alias.trim()
    if (!finalAlias) {
      setError('名称不能为空')
      return
    }
    if (!form.countryCode) {
      setError('国家/地区不能为空')
      return
    }
    // 默认地址 = 列表中选中的条目；列表非空时提交 addresses 整体替换。
    const finalAddress = form.defaultAddr
    const isNat = target.machine_type === 'nat'
    if (isNat && !finalAddress) {
      setError('NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）')
      return
    }
    const nextAddresses = form.addresses.length > 0 ? form.addresses : undefined
    if (nextAddresses && !nextAddresses.includes(finalAddress)) {
      setError('默认地址必须是地址列表中的一项')
      return
    }
    let ranges: PortRange[] = []
    const nextTags = form.tags
    if (isNat) {
      const parsed = parsePortRows(form.portRows)
      if ('error' in parsed) {
        setError(parsed.error)
        return
      }
      ranges = parsed.ranges
    }
    setSaving(true)
    try {
      if (isNat) {
        await api.updateServerPorts(
          target.id,
          finalAlias,
          finalAddress,
          ranges,
          nextTags,
          form.countryCode,
          form.location.trim(),
          billingPayload(form.billing),
          trafficPayload(form.traffic),
          form.xrayOverride ? { xray_version: form.xrayOverride } : {},
          nextAddresses,
          form.nonDirect,
        )
      } else {
        await api.updateServerAddress(
          target.id,
          finalAlias,
          finalAddress,
          nextTags,
          form.countryCode,
          form.location.trim(),
          billingPayload(form.billing),
          trafficPayload(form.traffic),
          form.xrayOverride ? { xray_version: form.xrayOverride } : {},
          nextAddresses,
          form.nonDirect,
        )
      }
      setTarget(null)
      reload()
    } catch (err) {
      setError(errorMessage(err))
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
