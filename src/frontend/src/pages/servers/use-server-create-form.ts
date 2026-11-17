import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { api, errorMessage } from '@/lib/api'
import { loadCities, loadCountries, type CountryOption } from '@/lib/geography'
import type { CreateServerResponse } from '@/lib/types'

import {
  aliasAfterParse,
  applyExternalLinkError,
  applyExternalLinkPreview,
  buildServerCreatePayload,
  editExternalLink,
  initialServerCreateForm,
  switchServerType,
  type ServerCreateFormState,
  type ServerTypeChoice,
} from './server-form-utils'

export type { ServerCreateFormState }

/**
 * 添加服务器对话框的表单状态与提交逻辑。
 * 原为服务器页内一组扁平 useState，拆分时装订为单一表单状态对象；
 * 服务器类型三选一（独立 IP / NAT / 外部直链）与外部链接解析状态由
 * server-form-utils 的纯函数驱动，便于测试。
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

  // 切换类型：清掉目标类型无关的输入（见 switchServerType），公共资料保留。
  const setServerType = (serverType: ServerTypeChoice) =>
    setForm((current) => switchServerType(current, serverType))

  // 链接改动即视为未解析（回到已解析的原文时保留预览），提交时会被拦截；
  // 同时清掉上一次提交的「请先解析链接」提示。
  const setExternalLink = (link: string) => {
    setCreateError('')
    setForm((current) => ({ ...current, external: editExternalLink(current.external, link) }))
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
      setCreateError('')
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

  const setExternalExit = (partial: Partial<typeof form.external.exit>) => {
    setCreateError('')
    setForm((current) => ({
      ...current,
      external: { ...current.external, exit: { ...current.external.exit, ...partial } },
    }))
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const result = buildServerCreatePayload(form)
    if ('error' in result) {
      setCreateError(result.error)
      return
    }
    setCreating(true)
    try {
      const res = await api.createServer(result.body)
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
    setServerType,
    setExternalLink,
    setExternalExit,
    parseExternalLink,
    onCreate,
  }
}

export type ServerCreateFormController = ReturnType<typeof useServerCreateForm>
