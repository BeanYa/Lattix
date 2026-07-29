import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { Building2Icon, PencilIcon, PlusIcon, Trash2Icon, XIcon } from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { CountryCombobox } from '@/components/CountryCombobox'
import { ServerMonitorGrid } from '@/components/ServerMonitor'
import { TagInput } from '@/components/TagInput'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime } from '@/lib/format'
import { loadCities, loadCountries, type CountryOption } from '@/lib/geography'
import { formatPortRange, parsePortRange, validatePortRanges } from '@/lib/ports'
import { isServerOnline } from '@/lib/server-state'
import { useTimezone } from '@/lib/timezone'
import type { BillingInput, IntervalUnit, MachineType, PortRange, Provider, ReleaseVersions, Server, ServerMetricSeries, TrafficAccountingMode, TrafficPlanInput } from '@/lib/types'

const DEPENDENCIES_COMMAND = 'apk add --no-cache bash curl ca-certificates unzip util-linux'
const CURRENCIES = ['CNY', 'USD', 'EUR', 'CAD', 'HKD', 'JPY', 'AUD', 'GBP', 'SGD', 'CHF']

interface BillingFormState {
  enabled: boolean
  providerId: string
  amount: string
  currency: string
  startedOn: string
  intervalCount: number
  intervalUnit: IntervalUnit
  renewalOn: string
}

interface TrafficFormState {
  limited: boolean
  quota: string
  quotaUnit: 'GB' | 'TB'
  accountingMode: TrafficAccountingMode
  anchorOn: string
  resetCount: number
  resetUnit: IntervalUnit
}

function localDate() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

function addInterval(date: string, count: number, unit: IntervalUnit) {
  const value = new Date(`${date}T00:00:00`)
  if (unit === 'day') value.setDate(value.getDate() + count)
  if (unit === 'month') value.setMonth(value.getMonth() + count)
  if (unit === 'year') value.setFullYear(value.getFullYear() + count)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

function defaultBilling(): BillingFormState {
  const today = localDate()
  return { enabled: false, providerId: '', amount: '', currency: 'CNY', startedOn: today, intervalCount: 1, intervalUnit: 'month', renewalOn: addInterval(today, 1, 'month') }
}

function defaultTraffic(): TrafficFormState {
  return { limited: false, quota: '1000', quotaUnit: 'GB', accountingMode: 'outbound', anchorOn: localDate(), resetCount: 1, resetUnit: 'month' }
}

function billingPayload(value: BillingFormState): BillingInput {
  const digits = ['JPY', 'KRW', 'ISK'].includes(value.currency) ? 0 : 2
  return { enabled: value.enabled, provider_id: Number(value.providerId || 0), amount_minor: Math.round(Number(value.amount || 0) * 10 ** digits), currency: value.currency, service_started_on: value.startedOn, interval_count: value.intervalCount, interval_unit: value.intervalUnit, next_renewal_on: value.renewalOn }
}

function trafficPayload(value: TrafficFormState): TrafficPlanInput {
  return { quota_bytes: value.limited ? Math.round(Number(value.quota) * (value.quotaUnit === 'TB' ? 1e12 : 1e9)) : null, accounting_mode: value.accountingMode, reset_anchor_on: value.anchorOn, reset_count: value.resetCount, reset_unit: value.resetUnit }
}

function BillingTrafficFields({ billing, setBilling, traffic, setTraffic, providers, onManageProviders }: {
  billing: BillingFormState
  setBilling: (value: BillingFormState) => void
  traffic: TrafficFormState
  setTraffic: (value: TrafficFormState) => void
  providers: Provider[]
  onManageProviders: () => void
}) {
  return (
    <div className="space-y-5">
      <Separator />
      <section className="space-y-3">
        <div>
          <h3 className="text-sm font-medium">流量额度</h3>
          <p className="text-xs text-muted-foreground">十进制换算：1 GB = 10^9 bytes，1 TB = 1000 GB</p>
        </div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={traffic.limited} onChange={(e) => setTraffic({ ...traffic, limited: e.target.checked })} />有限额度</label>
        {traffic.limited ? <div className="grid grid-cols-[1fr_110px] gap-2"><Input type="number" min="0.01" step="0.01" value={traffic.quota} onChange={(e) => setTraffic({ ...traffic, quota: e.target.value })} /><Select value={traffic.quotaUnit} onValueChange={(v) => v && setTraffic({ ...traffic, quotaUnit: v as 'GB' | 'TB' })} items={[{ value: 'GB', label: 'GB' }, { value: 'TB', label: 'TB' }]}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="GB">GB</SelectItem><SelectItem value="TB">TB</SelectItem></SelectContent></Select></div> : null}
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-2"><Label>计流方式</Label><Select value={traffic.accountingMode} onValueChange={(v) => v && setTraffic({ ...traffic, accountingMode: v as TrafficAccountingMode })} items={[{ value: 'outbound', label: '仅出站' }, { value: 'bidirectional', label: '入站 + 出站' }, { value: 'max', label: '取较大方向' }]}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="outbound">仅出站</SelectItem><SelectItem value="bidirectional">入站 + 出站</SelectItem><SelectItem value="max">取较大方向</SelectItem></SelectContent></Select></div>
          <div className="space-y-2"><Label>重置锚点</Label><Input type="date" value={traffic.anchorOn} onChange={(e) => setTraffic({ ...traffic, anchorOn: e.target.value })} /></div>
        </div>
      </section>
      <Separator />
      <section className="space-y-3">
        <label className="flex items-center gap-2 text-sm font-medium"><input type="checkbox" checked={billing.enabled} onChange={(e) => setBilling({ ...billing, enabled: e.target.checked })} />统计计费</label>
        {billing.enabled ? <>
          <div className="grid grid-cols-[1fr_auto] items-end gap-2"><div className="space-y-2"><Label>服务商</Label><Select value={billing.providerId} onValueChange={(v) => v && setBilling({ ...billing, providerId: v })} items={providers.map((p) => ({ value: String(p.id), label: p.name }))}><SelectTrigger><SelectValue placeholder="选择服务商" /></SelectTrigger><SelectContent>{providers.map((p) => <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>)}</SelectContent></Select></div><Button type="button" variant="outline" size="icon" title="管理服务商" onClick={onManageProviders}><Building2Icon /></Button></div>
          <div className="grid grid-cols-[1fr_110px] gap-2"><div className="space-y-2"><Label>每周期实付金额</Label><Input type="number" min="0" step="0.01" value={billing.amount} onChange={(e) => setBilling({ ...billing, amount: e.target.value })} /></div><div className="space-y-2"><Label>币种</Label><Select value={billing.currency} onValueChange={(v) => v && setBilling({ ...billing, currency: v })} items={CURRENCIES.map((c) => ({ value: c, label: c }))}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{CURRENCIES.map((c) => <SelectItem key={c} value={c}>{c}</SelectItem>)}</SelectContent></Select></div></div>
          <div className="grid gap-3 sm:grid-cols-2"><div className="space-y-2"><Label>开通日期</Label><Input type="date" max={localDate()} value={billing.startedOn} onChange={(e) => setBilling({ ...billing, startedOn: e.target.value, renewalOn: addInterval(e.target.value, billing.intervalCount, billing.intervalUnit) })} /></div><div className="space-y-2"><Label>下次续费日</Label><Input type="date" value={billing.renewalOn} onChange={(e) => setBilling({ ...billing, renewalOn: e.target.value })} /></div></div>
          <div className="grid grid-cols-[1fr_140px] gap-2"><div className="space-y-2"><Label>计费周期</Label><Input type="number" min="1" value={billing.intervalCount} onChange={(e) => setBilling({ ...billing, intervalCount: Number(e.target.value) })} /></div><div className="space-y-2"><Label>单位</Label><Select value={billing.intervalUnit} onValueChange={(v) => v && setBilling({ ...billing, intervalUnit: v as IntervalUnit })} items={[{ value: 'day', label: '天' }, { value: 'month', label: '月' }, { value: 'year', label: '年' }]}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="day">天</SelectItem><SelectItem value="month">月</SelectItem><SelectItem value="year">年</SelectItem></SelectContent></Select></div></div>
        </> : null}
      </section>
    </div>
  )
}

// 内置公网地址候选（§9）：拨入学习地址 + agent 上报的网卡非回环地址，去重。
function addrCandidates(s: Server): string[] {
  return [...new Set([s.learned_addr, ...s.nic_addresses].filter(Boolean))]
}

// 可用端口动态行（§21）：每行一个文本输入，支持 10000 / 10001-10010 / 20001-20010:10001-10010。
function PortRowsEditor({
  rows,
  onChange,
}: {
  rows: string[]
  onChange: (rows: string[]) => void
}) {
  const setRow = (i: number, value: string) => {
    const next = rows.slice()
    next[i] = value
    onChange(next)
  }
  return (
    <div className="space-y-2">
      {rows.map((row, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={row}
            onChange={(e) => setRow(i, e.target.value)}
            placeholder="10000 或 10001-10010 或 20001-20010:10001-10010"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={rows.length <= 1}
            onClick={() => onChange(rows.filter((_, j) => j !== i))}
          >
            <XIcon />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...rows, ''])}>
        <PlusIcon />
        添加端口段
      </Button>
    </div>
  )
}

/** 解析并校验端口段文本行；全部留空返回空数组（仅出口档），非法返回错误文案。 */
function parsePortRows(rows: string[]): { ranges: PortRange[] } | { error: string } {
  const filled = rows.map((r) => r.trim()).filter(Boolean)
  const ranges: PortRange[] = []
  for (const row of filled) {
    const r = parsePortRange(row)
    if (!r) {
      return { error: `端口段「${row}」格式非法：支持单端口 10000、范围 10001-10010、映射 20001-20010:10001-10010` }
    }
    ranges.push(r)
  }
  const err = validatePortRanges(ranges)
  if (err) {
    return { error: err }
  }
  return { ranges }
}

export default function Servers() {
  const { timezone } = useTimezone()
  const { confirm, notify } = useAppDialog()
  const [servers, setServers] = useState<Server[]>([])
  const [metricSamples, setMetricSamples] = useState<ServerMetricSeries[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [providers, setProviders] = useState<Provider[]>([])
  const [providerManagerOpen, setProviderManagerOpen] = useState(false)
  const [providerEditID, setProviderEditID] = useState<number | null>(null)
  const [providerName, setProviderName] = useState('')
  const [providerWebsite, setProviderWebsite] = useState('')
  const [providerError, setProviderError] = useState('')
  const [billing, setBilling] = useState<BillingFormState>(defaultBilling)
  const [traffic, setTraffic] = useState<TrafficFormState>(defaultTraffic)

  const [open, setOpen] = useState(false)
  const [alias, setAlias] = useState('')
  const [address, setAddress] = useState('')
  const [machineType, setMachineType] = useState<MachineType>('direct')
  const [tags, setTags] = useState<string[]>([])
  const [countryCode, setCountryCode] = useState('')
  const [location, setLocation] = useState('')
  const [countryOptions, setCountryOptions] = useState<CountryOption[]>([])
  const [cities, setCities] = useState<string[]>([])
  const [portRows, setPortRows] = useState<string[]>([''])
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [cmdView, setCmdView] = useState<{ title: string; command: string } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Server | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [editTarget, setEditTarget] = useState<Server | null>(null)
  const [editAlias, setEditAlias] = useState('')
  const [editAddress, setEditAddress] = useState('')
  const [editAddrMode, setEditAddrMode] = useState<'builtin' | 'custom'>('custom')
  const [editAddrChoice, setEditAddrChoice] = useState('')
  const [editPortRows, setEditPortRows] = useState<string[]>([''])
  const [editTags, setEditTags] = useState<string[]>([])
  const [editCountryCode, setEditCountryCode] = useState('')
  const [editLocation, setEditLocation] = useState('')
  const [editCities, setEditCities] = useState<string[]>([])
  const [editSaving, setEditSaving] = useState(false)
  const [editError, setEditError] = useState('')
  const [editBilling, setEditBilling] = useState<BillingFormState>(defaultBilling)
  const [editTraffic, setEditTraffic] = useState<TrafficFormState>(defaultTraffic)
  const [renewTarget, setRenewTarget] = useState<Server | null>(null)
  const [renewalOn, setRenewalOn] = useState('')
  const [renewing, setRenewing] = useState(false)
  const [upgradeTarget, setUpgradeTarget] = useState<Server | null>(null)
  const [upgradeKind, setUpgradeKind] = useState<'xray' | 'agent'>('xray')
  const [upgradeVersion, setUpgradeVersion] = useState('latest')
  const [upgradeVersions, setUpgradeVersions] = useState<ReleaseVersions | null>(null)
  const [upgradeVersionsLoading, setUpgradeVersionsLoading] = useState(false)
  const upgradeVersionsRequest = useRef(0)
  const [upgrading, setUpgrading] = useState(false)
  const [upgradeError, setUpgradeError] = useState('')
  // 升级命令追踪：下发后轮询命令终态（acked/failed），替代旧版"alert 后即关闭"。
  const [upgradeCmdId, setUpgradeCmdId] = useState<number | null>(null)
  const [upgradeResult, setUpgradeResult] = useState<'pending' | 'success' | 'failed' | null>(null)
  const [upgradeResultError, setUpgradeResultError] = useState('')

  const load = useCallback((silent = false) => {
    api
      .servers(silent ? { display: 'silent' } : undefined)
      .then(setServers)
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [])

  const loadProviders = useCallback(() => api.providers().then(setProviders).catch(() => setProviders([])), [])

  useEffect(() => {
    load()
    const timer = setInterval(() => load(true), 5000)
    return () => clearInterval(timer)
  }, [load])

  useEffect(() => { loadProviders() }, [loadProviders])

  useEffect(() => {
    let active = true
    const loadSamples = () => {
      api.serverMetricSamples()
        .then((result) => {
          if (active) setMetricSamples(result)
        })
        .catch(() => {
          if (active) setMetricSamples([])
        })
    }
    loadSamples()
    const timer = setInterval(loadSamples, 60000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!open && !editTarget) return
    loadCountries().then(setCountryOptions).catch(() => setCountryOptions([]))
  }, [open, editTarget])

  useEffect(() => {
    if (!countryCode) {
      setCities([])
      return
    }
    loadCities(countryCode).then(setCities).catch(() => setCities([]))
  }, [countryCode])

  useEffect(() => {
    if (!editCountryCode) {
      setEditCities([])
      return
    }
    loadCities(editCountryCode).then(setEditCities).catch(() => setEditCities([]))
  }, [editCountryCode])

  const citySuggestions = useMemo(() => {
    const query = location.trim().toLocaleLowerCase()
    return cities.filter((city) => !query || city.toLocaleLowerCase().includes(query)).slice(0, 30)
  }, [cities, location])

  const editCitySuggestions = useMemo(() => {
    const query = editLocation.trim().toLocaleLowerCase()
    return editCities.filter((city) => !query || city.toLocaleLowerCase().includes(query)).slice(0, 30)
  }, [editCities, editLocation])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setAlias('')
      setAddress('')
      setMachineType('direct')
      setTags([])
      setCountryCode('')
      setLocation('')
      setPortRows([''])
      setBilling(defaultBilling())
      setTraffic(defaultTraffic())
      setCreateError('')
    }
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    const body: {
      alias: string
      address?: string
      machine_type: MachineType
      allowed_ports?: PortRange[]
      tags?: string[]
      country_code: string
      location: string
      billing: BillingInput
      traffic_plan: TrafficPlanInput
    } = {
      alias: alias.trim(),
      machine_type: machineType,
      country_code: countryCode,
      location: location.trim(),
      billing: billingPayload(billing),
      traffic_plan: trafficPayload(traffic),
    }
    body.tags = tags
    if (address.trim()) {
      body.address = address.trim()
    }
    if (machineType === 'nat') {
      if (!address.trim()) {
        setCreateError('NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）')
        return
      }
      const parsed = parsePortRows(portRows)
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
      setCmdView({ title: '服务器已创建，请在目标机器上执行安装命令', command: res.install_command })
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  // 未安装（从未上线）→ 重新获取安装命令；已安装 → 凭证刷新（旧凭证立即失效）。
  const onRotateToken = async (s: Server) => {
    const installed = s.last_seen_at !== null
    if (
      installed &&
      !(await confirm({
        title: '刷新服务器凭证',
        description: '刷新后该服务器的旧凭证（含长期凭证）立即失效，agent 重连前需重新执行安装命令。',
        confirmLabel: '刷新凭证',
        destructive: true,
      }))
    ) {
      return
    }
    try {
      const res = await api.rotateServerToken(s.id)
      setCmdView({
        title: installed ? '凭证已刷新，请重新执行安装命令' : '安装命令（bootstrap token 已刷新）',
        command: res.install_command,
      })
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  // 配置漂移修复（§17）：重放该服务器全部 active 节点，agent 重建配置后漂移标志自动清除。
  const onRepair = async (s: Server) => {
    if (!(await confirm({
      title: '修复配置漂移',
      description: `确定修复「${s.alias}」的配置漂移？将按面板节点状态重建该机 xray 配置。`,
      confirmLabel: '开始修复',
    }))) {
      return
    }
    try {
      const res = await api.repairServer(s.id)
      setError('')
      await notify({
        title: '修复命令已下发',
        description: `已下发 ${res.reapplied} 个节点的重放命令，漂移标志将在 agent 重建后自动清除。`,
      })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onOpenEdit = (s: Server) => {
    setEditTarget(s)
    setEditAlias(s.alias)
    setEditAddress(s.address)
    const candidates = addrCandidates(s)
    if (candidates.includes(s.address)) {
      setEditAddrMode('builtin')
      setEditAddrChoice(s.address)
    } else {
      setEditAddrMode('custom')
      setEditAddrChoice(candidates[0] ?? '')
    }
    setEditPortRows(
      s.allowed_ports.length > 0 ? s.allowed_ports.map(formatPortRange) : [''],
    )
    setEditTags(s.tags)
    setEditCountryCode(s.country_code)
    setEditLocation(s.location)
    const divisor = ['JPY', 'KRW', 'ISK'].includes(s.billing.currency) ? 1 : 100
    setEditBilling({ enabled: s.billing.enabled, providerId: s.billing.provider ? String(s.billing.provider.id) : '', amount: String(s.billing.amount_minor / divisor), currency: s.billing.currency || 'CNY', startedOn: s.billing.service_started_on || localDate(), intervalCount: s.billing.interval_count || 1, intervalUnit: s.billing.interval_unit || 'month', renewalOn: s.billing.next_renewal_on || addInterval(localDate(), 1, 'month') })
    const quota = s.traffic_plan.quota_bytes
    const quotaUnit: 'GB' | 'TB' = quota !== null && quota >= 1e12 ? 'TB' : 'GB'
    setEditTraffic({ limited: quota !== null, quota: quota === null ? '1000' : String(quota / (quotaUnit === 'TB' ? 1e12 : 1e9)), quotaUnit, accountingMode: s.traffic_plan.accounting_mode, anchorOn: s.traffic_plan.reset_anchor_on || localDate(), resetCount: s.traffic_plan.reset_count || 1, resetUnit: s.traffic_plan.reset_unit || 'month' })
    setEditError('')
  }

  const onUpdateAddress = async (e: FormEvent) => {
    e.preventDefault()
    if (!editTarget) {
      return
    }
    setEditError('')
    const finalAlias = editAlias.trim()
    if (!finalAlias) {
      setEditError('名称不能为空')
      return
    }
    // 内置地址 = 候选下拉选中值；自定义 = 文本框输入。
    const finalAddress = editAddrMode === 'builtin' ? editAddrChoice : editAddress.trim()
    const isNat = editTarget.machine_type === 'nat'
    if (isNat && !finalAddress) {
      setEditError('NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）')
      return
    }
    let ranges: PortRange[] = []
    const nextTags = editTags
    if (!editCountryCode) {
      setEditError('国家/地区不能为空')
      return
    }
    if (isNat) {
      const parsed = parsePortRows(editPortRows)
      if ('error' in parsed) {
        setEditError(parsed.error)
        return
      }
      ranges = parsed.ranges
    }
    setEditSaving(true)
    try {
      if (isNat) {
        await api.updateServerPorts(
          editTarget.id,
          finalAlias,
          finalAddress,
          ranges,
          nextTags,
          editCountryCode,
          editLocation.trim(),
          billingPayload(editBilling),
          trafficPayload(editTraffic),
        )
      } else {
        await api.updateServerAddress(
          editTarget.id,
          finalAlias,
          finalAddress,
          nextTags,
          editCountryCode,
          editLocation.trim(),
          billingPayload(editBilling),
          trafficPayload(editTraffic),
        )
      }
      setEditTarget(null)
      load()
    } catch (err) {
      setEditError(errorMessage(err))
    } finally {
      setEditSaving(false)
    }
  }

  // 版本升级（§18）：命令入队后由 agent 下载/校验/替换/重启，版本号经 hello/遥测自动刷新。
  // kind=xray 升级 xray-core；kind=agent 升级 agent 自身（兼容窗口外的机器经此收敛）。
  const onOpenUpgrade = (s: Server, kind: 'xray' | 'agent') => {
    const requestID = ++upgradeVersionsRequest.current
    setUpgradeTarget(s)
    setUpgradeKind(kind)
    setUpgradeVersion('latest')
    setUpgradeVersions(null)
    setUpgradeVersionsLoading(true)
    setUpgradeError('')
    setUpgradeCmdId(null)
    setUpgradeResult(null)
    setUpgradeResultError('')
    api.releaseVersions(kind)
      .then((versions) => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeVersions(versions)
      })
      .catch((err) => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeError(errorMessage(err))
      })
      .finally(() => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeVersionsLoading(false)
      })
  }

  const onUpgrade = async (e: FormEvent) => {
    e.preventDefault()
    if (!upgradeTarget) {
      return
    }
    setUpgradeError('')
    setUpgrading(true)
    setUpgradeResult(null)
    setUpgradeResultError('')
    try {
      const version = upgradeVersion.trim() || 'latest'
      const res =
        upgradeKind === 'agent'
          ? await api.upgradeAgent(upgradeTarget.id, version)
          : await api.upgradeServer(upgradeTarget.id, version)
      // 下发成功：进入轮询模式（弹窗保留，显示命令执行进度直到终态）。
      setUpgradeCmdId(res.command_id)
      setUpgradeResult('pending')
    } catch (err) {
      setUpgradeError(errorMessage(err))
    } finally {
      setUpgrading(false)
    }
  }

  // 升级命令轮询：下发后跟踪 command_id 终态（acked=成功 / failed=失败），
  // 替代旧版"alert 后即关闭、失败无感知"。agent 升级成功后会退出重连，
  // 命令可能停在 sent（agent 重启未回执），故设超时兜底提示。
  useEffect(() => {
    if (upgradeResult !== 'pending' || upgradeCmdId === null || !upgradeTarget) {
      return
    }
    const serverId = upgradeTarget.id
    const cmdId = upgradeCmdId
    let stopped = false
    const poll = async () => {
      try {
        const cmds = await api.serverCommands(serverId, 50, { display: 'silent' })
        if (stopped) {
          return
        }
        const cmd = cmds.find((c) => c.id === cmdId)
        if (!cmd) {
          return // 命令尚未出现在日志，等下次轮询
        }
        if (cmd.status === 'acked') {
          setUpgradeResult('success')
          load() // 刷新服务器列表（版本号/在线状态）
        } else if (cmd.status === 'failed') {
          setUpgradeResult('failed')
          setUpgradeResultError(cmd.error || '命令执行失败（详见命令日志）')
        }
        // queued/sent：继续轮询
      } catch {
        // 轮询本身的网络错误静默，下次重试
      }
    }
    poll()
    const interval = setInterval(poll, 3000)
    // 90s 超时：agent 自升级成功会退出重连，可能来不及回执；提示用户自行核对版本。
    const timeout = setTimeout(() => {
      if (!stopped) {
        setUpgradeResult(null)
        setUpgradeResultError('未在超时内收到 agent 回执（agent 自升级会重启重连，请稍后核对版本或查看命令日志）')
      }
    }, 90000)
    return () => {
      stopped = true
      clearInterval(interval)
      clearTimeout(timeout)
    }
  }, [load, upgradeResult, upgradeCmdId, upgradeTarget])

  const onDelete = async (purge: 'xray' | 'agent') => {
    if (!deleteTarget) {
      return
    }
    setDeleting(true)
    try {
      await api.deleteServer(deleteTarget.id, purge)
      setDeleteTarget(null)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(false)
    }
  }

  const editCandidates = editTarget ? addrCandidates(editTarget) : []

  const saveProvider = async (event: FormEvent) => {
    event.preventDefault()
    setProviderError('')
    try {
      if (providerEditID) await api.updateProvider(providerEditID, providerName, providerWebsite)
      else await api.createProvider(providerName, providerWebsite)
      setProviderEditID(null); setProviderName(''); setProviderWebsite('')
      await loadProviders()
    } catch (err) { setProviderError(errorMessage(err)) }
  }

  const removeProvider = async (provider: Provider) => {
    if (!(await confirm({ title: '删除服务商', description: `确定删除「${provider.name}」？已被服务器使用时无法删除。`, confirmLabel: '删除', destructive: true }))) return
    try { await api.deleteProvider(provider.id); await loadProviders() } catch (err) { setProviderError(errorMessage(err)) }
  }

  const openRenewal = (server: Server) => {
    setRenewTarget(server)
    setRenewalOn(addInterval(localDate(), server.billing.interval_count, server.billing.interval_unit))
  }

  const confirmRenewal = async (event: FormEvent) => {
    event.preventDefault()
    if (!renewTarget) return
    setRenewing(true)
    try { await api.confirmServerRenewal(renewTarget.id, renewalOn); setRenewTarget(null); load() }
    catch (err) { setError(errorMessage(err)) }
    finally { setRenewing(false) }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">服务器</h1>
        <Button onClick={() => setOpen(true)}>
          <PlusIcon />
          添加服务器
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">加载失败：{error}</p>}

      <ServerMonitorGrid
        servers={servers}
        samples={metricSamples}
        loading={loading}
        timezone={timezone}
        onEdit={onOpenEdit}
        onRepair={onRepair}
        onRotateToken={onRotateToken}
        onUpgrade={onOpenUpgrade}
        onRenew={openRenewal}
        onDelete={setDeleteTarget}
      />

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>添加服务器</DialogTitle>
            <DialogDescription>输入别名创建服务器。</DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="alias">别名</Label>
              <Input
                id="alias"
                value={alias}
                onChange={(e) => setAlias(e.target.value)}
                placeholder="例如：hk-01"
                required
                autoFocus
              />
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="country">国家/地区</Label>
                <CountryCombobox
                  id="country"
                  value={countryCode}
                  options={countryOptions}
                  onValueChange={(value) => {
                    setCountryCode(value)
                    setLocation('')
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="location">地区/城市</Label>
                <Input
                  id="location"
                  value={location}
                  onChange={(e) => setLocation(e.target.value)}
                  placeholder="选择城市或输入机房位置"
                  list="server-location-options"
                  maxLength={100}
                />
                <datalist id="server-location-options">
                  {citySuggestions.map((city) => (
                    <option key={city} value={city} />
                  ))}
                </datalist>
                <p className="text-xs text-muted-foreground">城市列表仅作辅助，也可填写自定义机房区域。</p>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="tags">标签（Tag）</Label>
              <TagInput
                id="tags"
                value={tags}
                onChange={setTags}
                placeholder="输入标签后按回车"
              />
              <p className="text-xs text-muted-foreground">
                回车或逗号确认，最多 10 个；名称模板可按顺序使用 {'{{TAG[0]}}'}、{'{{TAG[1]}}'}。
              </p>
            </div>
            <div className="space-y-2">
              <Label>机器类型</Label>
              <Select
                value={machineType}
                onValueChange={(v) => v && setMachineType(v as MachineType)}
                items={[
                  { value: 'direct', label: '独立 IP（direct）' },
                  { value: 'nat', label: 'NAT' },
                ]}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="direct">独立 IP（direct）</SelectItem>
                  <SelectItem value="nat">NAT</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="address">公网地址{machineType === 'nat' ? '（必填）' : ''}</Label>
              <Input
                id="address"
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder={
                  machineType === 'nat' ? '共享公网 IP 或域名（由 IDC 提供）' : '留空按 agent 拨入地址自动学习'
                }
              />
            </div>
            {machineType === 'nat' && (
              <div className="space-y-2">
                <Label>可用端口</Label>
                <PortRowsEditor rows={portRows} onChange={setPortRows} />
                <p className="text-xs text-muted-foreground">
                  每行一段：单端口 10000、范围 10001-10010、非 1:1 映射
                  20001-20010:10001-10010（外部段:内部段）。全部留空 = 仅出口档（无入站能力）。
                </p>
              </div>
            )}
            <BillingTrafficFields billing={billing} setBilling={setBilling} traffic={traffic} setTraffic={setTraffic} providers={providers} onManageProviders={() => setProviderManagerOpen(true)} />
            {createError && <p className="text-sm text-destructive">{createError}</p>}
            <DialogFooter>
              <Button
                type="submit"
                disabled={
                  creating ||
                  !alias.trim() ||
                  !countryCode ||
                  (machineType === 'nat' && !address.trim())
                }
              >
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={cmdView !== null} onOpenChange={(next) => !next && setCmdView(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>安装命令</DialogTitle>
            <DialogDescription>{cmdView?.title}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <p className="text-sm text-muted-foreground">bash/curl 等依赖安装（按需执行）</p>
              <CopyButton text={DEPENDENCIES_COMMAND} />
            </div>
            <pre className="overflow-auto rounded-lg bg-muted p-3 text-xs break-all whitespace-pre-wrap">
              {DEPENDENCIES_COMMAND}
            </pre>
          </div>
          <div className="space-y-2">
            <p className="text-sm font-medium">Agent 安装命令</p>
            <pre className="max-h-40 overflow-auto rounded-lg bg-muted p-3 text-xs break-all whitespace-pre-wrap">
              {cmdView?.command}
            </pre>
          </div>
          <DialogFooter showCloseButton>
            <CopyButton text={cmdView?.command ?? ''} size="default" />
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={editTarget !== null} onOpenChange={(next) => !next && setEditTarget(null)}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>编辑服务器</DialogTitle>
            <DialogDescription>
              {editTarget?.machine_type === 'nat'
                ? `修改「${editTarget?.alias}」的公网地址与可用端口段（NAT 类型地址必填；端口段收窄时存量节点/链跳端口不得越界）。机器类型建后不可互转。`
                : `修改「${editTarget?.alias}」的公网地址，订阅中节点地址随之更新。内置地址来自 agent 上报的网卡地址与拨入学习地址；自定义留空则下次 agent 连接时按拨入地址重新自动学习。`}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onUpdateAddress} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="editAlias">名称</Label>
              <Input
                id="editAlias"
                value={editAlias}
                onChange={(e) => setEditAlias(e.target.value)}
                maxLength={100}
                required
              />
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="edit-country">国家/地区</Label>
                <CountryCombobox
                  id="edit-country"
                  value={editCountryCode}
                  options={countryOptions}
                  onValueChange={(value) => {
                    setEditCountryCode(value)
                    setEditLocation('')
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="editLocation">地区/城市</Label>
                <Input
                  id="editLocation"
                  value={editLocation}
                  onChange={(e) => setEditLocation(e.target.value)}
                  placeholder="选择城市或输入机房位置"
                  list="edit-server-location-options"
                  maxLength={100}
                />
                <datalist id="edit-server-location-options">
                  {editCitySuggestions.map((city) => (
                    <option key={city} value={city} />
                  ))}
                </datalist>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="editTags">标签（Tag）</Label>
              <TagInput
                id="editTags"
                value={editTags}
                onChange={setEditTags}
                placeholder="输入标签后按回车"
              />
              <p className="text-xs text-muted-foreground">
                回车或逗号确认；新建链路时可通过 {'{{TAG[0]}}'} 等参数引用。
              </p>
            </div>
            <div className="space-y-2">
              <Label>公网地址{editTarget?.machine_type === 'nat' ? '（必填）' : ''}</Label>
              <Select
                value={editAddrMode}
                onValueChange={(v) => v && setEditAddrMode(v as 'builtin' | 'custom')}
                items={[
                  { value: 'builtin', label: '内置地址（agent 上报）' },
                  { value: 'custom', label: '自定义' },
                ]}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="builtin">内置地址（agent 上报）</SelectItem>
                  <SelectItem value="custom">自定义</SelectItem>
                </SelectContent>
              </Select>
              {editAddrMode === 'builtin' ? (
                editCandidates.length > 0 ? (
                  <Select
                    value={editAddrChoice}
                    onValueChange={(v) => v && setEditAddrChoice(v)}
                    items={editCandidates.map((c) => ({ value: c, label: c }))}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {editCandidates.map((c) => (
                        <SelectItem key={c} value={c}>
                          {c}
                          {c === editTarget?.learned_addr ? '（拨入学习）' : ''}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    暂无内置地址候选：agent 尚未上报网卡地址（上线后自动收集），请改用自定义。
                  </p>
                )
              ) : (
                <Input
                  id="editAddress"
                  value={editAddress}
                  onChange={(e) => setEditAddress(e.target.value)}
                  placeholder="例如：1.2.3.4 或 hk-01.example.com"
                  autoFocus
                />
              )}
            </div>
            {editTarget?.machine_type === 'nat' && (
              <div className="space-y-2">
                <Label>可用端口</Label>
                <PortRowsEditor rows={editPortRows} onChange={setEditPortRows} />
                <p className="text-xs text-muted-foreground">
                  每行一段：单端口 10000、范围 10001-10010、非 1:1 映射
                  20001-20010:10001-10010（外部段:内部段）。全部留空 = 仅出口档（无入站能力）。
                </p>
              </div>
            )}
            <BillingTrafficFields billing={editBilling} setBilling={setEditBilling} traffic={editTraffic} setTraffic={setEditTraffic} providers={providers} onManageProviders={() => setProviderManagerOpen(true)} />
            {editError && <p className="text-sm text-destructive">{editError}</p>}
            <DialogFooter>
              <Button
                type="submit"
                disabled={editSaving || !editAlias.trim() || (editAddrMode === 'builtin' && !editAddrChoice)}
              >
                {editSaving ? '保存中…' : '保存'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog
        open={upgradeTarget !== null}
        onOpenChange={(next) => {
          if (!next) {
            upgradeVersionsRequest.current++
            setUpgradeTarget(null)
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>升级 {upgradeKind === 'agent' ? 'agent' : 'xray'}</DialogTitle>
            <DialogDescription>
              {upgradeKind === 'agent' ? (
                <>
                  将「{upgradeTarget?.alias}」的 agent 升级到指定版本（当前：
                  {upgradeTarget?.agent_version ?? '未知'}）。agent 将从 GitHub release
                  下载二进制、校验 SHA256 后自替换并重启；该操作也用于收敛落后出兼容窗口的
                  agent。
                </>
              ) : (
                <>
                  将「{upgradeTarget?.alias}」的 xray 升级到指定版本（当前：
                  {upgradeTarget?.xray_version ?? '未知'}）。agent 将下载官方 release、
                  校验 SHA2-256 后替换并重启；失败自动回滚。
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onUpgrade} className="space-y-4">
            {upgradeResult === null && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="upgradeVersion">目标版本</Label>
                  <Select
                    value={upgradeVersion}
                    onValueChange={(value) => value && setUpgradeVersion(value)}
                    items={(upgradeVersions?.versions ?? ['latest']).map((version) => ({
                      value: version,
                      label: version,
                    }))}
                    disabled={upgradeVersionsLoading}
                  >
                    <SelectTrigger id="upgradeVersion" className="w-full" autoFocus>
                      <SelectValue placeholder={upgradeVersionsLoading ? '正在获取版本…' : '选择版本'} />
                    </SelectTrigger>
                    <SelectContent>
                      {(upgradeVersions?.versions ?? ['latest']).map((version) => (
                        <SelectItem key={version} value={version}>{version}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {upgradeVersions && (
                    <p className="text-xs text-muted-foreground">
                      缓存更新于 {formatDateTime(upgradeVersions.fetched_at, timezone)}
                      {upgradeVersions.stale ? `；${upgradeVersions.message ?? '本次更新失败，正在使用缓存'}` : ''}
                    </p>
                  )}
                </div>
                {upgradeError && <p className="text-sm text-destructive">{upgradeError}</p>}
                <DialogFooter>
                  <Button type="submit" disabled={upgrading || upgradeVersionsLoading}>
                    {upgrading ? '下发中…' : '下发升级'}
                  </Button>
                </DialogFooter>
              </>
            )}
            {upgradeResult === 'pending' && (
              <div className="space-y-3">
                <p className="text-sm">
                  升级命令已下发（#{upgradeCmdId}），正在等待 agent 执行回执…
                </p>
                <p className="text-sm text-muted-foreground">
                  agent 自升级成功后会退出并由 systemd 拉起重连，可能需要数十秒。
                </p>
              </div>
            )}
            {upgradeResult === 'success' && (
              <div className="space-y-3">
                <p className="text-sm text-emerald-600">升级命令执行成功，版本号已刷新。</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
            {upgradeResult === 'failed' && (
              <div className="space-y-3">
                <p className="text-sm text-destructive">升级失败：</p>
                <p className="text-sm text-destructive whitespace-pre-wrap">{upgradeResultError}</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
            {/* 超时兜底（upgradeResult 被清空但 upgradeResultError 非空） */}
            {upgradeResult === null && upgradeResultError && (
              <div className="space-y-3">
                <p className="text-sm text-amber-600 whitespace-pre-wrap">{upgradeResultError}</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteTarget !== null} onOpenChange={(next) => !next && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除服务器</DialogTitle>
            <DialogDescription>
              {deleteTarget && isServerOnline(deleteTarget)
                ? `确定删除「${deleteTarget.alias}」？将向 agent 发送卸载命令并删除记录，请选择卸载范围。`
                : `确定删除「${deleteTarget?.alias}」？当前无可投递会话，仅删除记录；该机上的 agent 需手动清理。`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            {deleteTarget && isServerOnline(deleteTarget) ? (
              <>
                <Button variant="outline" disabled={deleting} onClick={() => onDelete('agent')}>
                  仅卸载 agent
                </Button>
                <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                  连同 xray 卸载
                </Button>
              </>
            ) : (
              <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                删除记录
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={providerManagerOpen} onOpenChange={setProviderManagerOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>服务商管理</DialogTitle><DialogDescription>服务器列表中仅显示服务商名称，官网用于快捷打开控制台。</DialogDescription></DialogHeader>
          <form onSubmit={saveProvider} className="space-y-3">
            <div className="space-y-2"><Label>服务商名称</Label><Input value={providerName} onChange={(e) => setProviderName(e.target.value)} required maxLength={100} /></div>
            <div className="space-y-2"><Label>官网地址</Label><Input type="url" value={providerWebsite} onChange={(e) => setProviderWebsite(e.target.value)} placeholder="https://provider.example" /></div>
            {providerError ? <p className="text-sm text-destructive">{providerError}</p> : null}
            <DialogFooter><Button type="submit">{providerEditID ? '保存修改' : '添加服务商'}</Button></DialogFooter>
          </form>
          <Separator />
          <div className="max-h-56 space-y-2 overflow-y-auto">
            {providers.map((provider) => <div key={provider.id} className="flex items-center justify-between gap-3 border-b py-2 last:border-0"><div className="min-w-0"><p className="truncate text-sm font-medium">{provider.name}</p><p className="truncate text-xs text-muted-foreground">{provider.website_url || '未配置官网'}</p></div><div className="flex gap-1"><Button type="button" variant="ghost" size="icon" title="编辑服务商" onClick={() => { setProviderEditID(provider.id); setProviderName(provider.name); setProviderWebsite(provider.website_url) }}><PencilIcon /></Button><Button type="button" variant="ghost" size="icon" title="删除服务商" onClick={() => removeProvider(provider)}><Trash2Icon /></Button></div></div>)}
            {providers.length === 0 ? <p className="py-6 text-center text-sm text-muted-foreground">暂无服务商</p> : null}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={renewTarget !== null} onOpenChange={(next) => !next && setRenewTarget(null)}>
        <DialogContent>
          <DialogHeader><DialogTitle>续费确认</DialogTitle><DialogDescription>确认「{renewTarget?.alias}」已经续费，并设置新的下次续费日。</DialogDescription></DialogHeader>
          <form onSubmit={confirmRenewal} className="space-y-4"><div className="space-y-2"><Label>下次续费日</Label><Input type="date" min={addInterval(localDate(), 1, 'day')} value={renewalOn} onChange={(e) => setRenewalOn(e.target.value)} required /></div><DialogFooter><Button type="submit" disabled={renewing}>{renewing ? '确认中…' : '确认续费'}</Button></DialogFooter></form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
