import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import {
  BanIcon,
  CalendarClockIcon,
  CircleCheckIcon,
  ExternalLinkIcon,
  EyeIcon,
  HistoryIcon,
  KeyRoundIcon,
  PlusIcon,
  QrCodeIcon,
  RefreshCwIcon,
  Settings2Icon,
  Trash2Icon,
  UsersIcon,
} from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { EmptyState, Notice, Page, PageHeader, Surface } from '@/components/PagePrimitives'
import { QRDialog } from '@/components/QRDialog'
import { SubscriptionRoutingFields } from '@/components/SubscriptionRoutingFields'
import { Badge } from '@/components/ui/badge'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { buildLinkOptions } from '@/lib/links'
import { defaultSubscriptionRouting } from '@/lib/subscription-routing'
import { useTimezone } from '@/lib/timezone'
import {
  formatTrafficLimit,
  parseTrafficLimit,
  parseTrafficResetDay,
  TRAFFIC_UNITS,
  type TrafficUnit,
} from '@/lib/user-subscription'
import type {
  Chain,
  ExternalSubscription,
  ExternalSubscriptionMode,
  SubUser,
  SubscriptionRoutingProfile,
  SubscriptionPreview,
  SubscriptionPreviewFormat,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
} from '@/lib/types'

const EXTERNAL_MODE_LABELS: Record<ExternalSubscriptionMode, string> = {
  stack: '叠加',
  merge: '并入',
  nodes: '附加',
}

function ExternalModeSelect({
  value,
  onChange,
  disabled,
}: {
  value: ExternalSubscriptionMode
  onChange: (mode: ExternalSubscriptionMode) => void
  disabled?: boolean
}) {
  return (
    <Select value={value} onValueChange={(next) => next && onChange(next as ExternalSubscriptionMode)} disabled={disabled}>
      <SelectTrigger className="w-24" aria-label="引入模式">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => (
          <SelectItem key={mode} value={mode}>{EXTERNAL_MODE_LABELS[mode]}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function TrafficLimitInput({
  value,
  unit,
  onValueChange,
  onUnitChange,
  placeholder = '流量配额',
}: {
  value: string
  unit: TrafficUnit
  onValueChange: (value: string) => void
  onUnitChange: (unit: TrafficUnit) => void
  placeholder?: string
}) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_4.5rem] gap-2">
      <Input
        type="number"
        min={0}
        step="any"
        value={value}
        onChange={(event) => onValueChange(event.target.value)}
        placeholder={placeholder}
      />
      <Select
        value={unit}
        onValueChange={(next) => next && onUnitChange(next as TrafficUnit)}
        items={TRAFFIC_UNITS}
      >
        <SelectTrigger className="w-full" aria-label="流量配额单位">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {TRAFFIC_UNITS.map((option) => (
            <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/** toLocalInput 把 RFC3339 时间转成 datetime-local 输入框所需的本地格式（yyyy-MM-ddTHH:mm）。 */
function toLocalInput(t: string): string {
  const d = new Date(t)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** localInputToRFC3339 把 datetime-local 值转成 RFC3339（UTC）；空串返回 null。 */
function localInputToRFC3339(v: string): string | null {
  if (!v) {
    return null
  }
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? null : d.toISOString()
}

export default function Users() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const [users, setUsers] = useState<SubUser[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [ruleCategories, setRuleCategories] = useState<SubscriptionRuleCategory[]>([])
  const [templates, setTemplates] = useState<SubscriptionTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<number | null>(null)
  const [toggling, setToggling] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<SubUser | null>(null)
  const [createLinkSel, setCreateLinkSel] = useState<number[]>([])
  const [createTrafficLimit, setCreateTrafficLimit] = useState('')
  const [createTrafficUnit, setCreateTrafficUnit] = useState<TrafficUnit>('GB')
  const [createResetDay, setCreateResetDay] = useState('')
  const [createPlanName, setCreatePlanName] = useState('')
  const [createAppURL, setCreateAppURL] = useState('')
  const [createRouting, setCreateRouting] = useState<SubscriptionRoutingProfile>(defaultSubscriptionRouting)

  const [expiryTarget, setExpiryTarget] = useState<SubUser | null>(null)
  const [expiryValue, setExpiryValue] = useState('')
  const [expirySaving, setExpirySaving] = useState(false)
  const [expiryError, setExpiryError] = useState('')

  const [assignTarget, setAssignTarget] = useState<SubUser | null>(null)
  const [assignSelection, setAssignSelection] = useState<number[]>([])
  const [assignSaving, setAssignSaving] = useState(false)
  const [assignError, setAssignError] = useState('')
  const [extSubs, setExtSubs] = useState<ExternalSubscription[]>([])
  const [assignExt, setAssignExt] = useState<Record<number, ExternalSubscriptionMode>>({})
  const [createExt, setCreateExt] = useState<Record<number, ExternalSubscriptionMode>>({})
  const [qrText, setQrText] = useState('')
  const loadRequest = useRef(0)

  // 用户订阅设置对话框
  const [subTarget, setSubTarget] = useState<SubUser | null>(null)
  const [subTrafficLimit, setSubTrafficLimit] = useState('')
  const [subTrafficUnit, setSubTrafficUnit] = useState<TrafficUnit>('GB')
  const [subResetDay, setSubResetDay] = useState('')
  const [subTitleOverride, setSubTitleOverride] = useState('')
  const [subAnnouncementOverride, setSubAnnouncementOverride] = useState('')
  const [subPlanName, setSubPlanName] = useState('')
  const [subAppURL, setSubAppURL] = useState('')
  const [subSaving, setSubSaving] = useState(false)
  const [subErr, setSubErr] = useState('')
  const [subRouting, setSubRouting] = useState<SubscriptionRoutingProfile>(defaultSubscriptionRouting)
  const [regenerating, setRegenerating] = useState<number | null>(null)
  const [resettingToken, setResettingToken] = useState<number | null>(null)
  const [previewTarget, setPreviewTarget] = useState<SubUser | null>(null)
  const [previewFormat, setPreviewFormat] = useState<SubscriptionPreviewFormat>('clash')
  const [previewData, setPreviewData] = useState<SubscriptionPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  // 流量历史对话框
  const [historyTarget, setHistoryTarget] = useState<SubUser | null>(null)
  const [historyData, setHistoryData] = useState<Array<{ period_start: string; up: number; down: number }>>([])
  const [historyLoading, setHistoryLoading] = useState(false)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent ? { display: 'silent' as const } : undefined
    try {
      const [nextUsers, nextChains, nextCategories, nextTemplates] = await Promise.all([
        api.users(options),
        api.chains(options),
        api.subscriptionCategories(options),
        api.subscriptionTemplates(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setUsers(nextUsers)
      setChains(nextChains)
      setRuleCategories(nextCategories)
      setTemplates(nextTemplates)
      try {
        setExtSubs(await api.externalSubscriptions({ display: 'silent' }))
      } catch {
        // 外部订阅列表不可用不阻断用户页
      }
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(err))
    } finally {
      if (!signal?.aborted && request === loadRequest.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    let stopped = false
    let timer: number | undefined
    const poll = async (initial: boolean) => {
      await load(!initial, controller.signal)
      if (!stopped) timer = window.setTimeout(() => void poll(false), 5000)
    }
    void poll(true)
    return () => {
      stopped = true
      loadRequest.current += 1
      controller.abort()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [load])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setName('')
      setExpiresAt('')
      setCreateError('')
      setCreated(null)
      setCreateLinkSel([])
      setCreateTrafficLimit('')
      setCreateTrafficUnit('GB')
      setCreateResetDay('')
      setCreatePlanName('')
      setCreateAppURL('')
      setCreateRouting(defaultSubscriptionRouting)
      setCreateExt({})
    }
  }

  const onToggleCreateLink = (id: number, checked: boolean) => {
    setCreateLinkSel((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  const linkOptions = useMemo(() => buildLinkOptions(chains), [chains])

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const trafficLimit = parseTrafficLimit(createTrafficLimit, createTrafficUnit)
      const resetDay = parseTrafficResetDay(createResetDay)
      const planName = createPlanName.trim()
      const appURL = createAppURL.trim()
      const res = await api.createUser(
        name.trim(),
        localInputToRFC3339(expiresAt),
        createLinkSel,
        {
          traffic_limit: trafficLimit,
          traffic_reset_day: resetDay,
          plan_name: planName,
          app_url: appURL,
          routing: createRouting,
          external_subscriptions: Object.entries(createExt).map(([id, mode]) => ({ subscription_id: Number(id), mode })),
        },
      )
      setCreated(res)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onOpenExpiry = (u: SubUser) => {
    setExpiryTarget(u)
    setExpiryValue(u.expires_at ? toLocalInput(u.expires_at) : '')
    setExpiryError('')
  }

  const onSaveExpiry = async (clear: boolean) => {
    if (!expiryTarget) {
      return
    }
    setExpiryError('')
    setExpirySaving(true)
    try {
      await api.updateUserExpiry(expiryTarget.id, clear ? null : localInputToRFC3339(expiryValue))
      setExpiryTarget(null)
      load()
    } catch (err) {
      setExpiryError(errorMessage(err))
    } finally {
      setExpirySaving(false)
    }
  }

  const onDelete = async (user: SubUser) => {
    if (!(await confirm({
      title: '删除用户',
      description: `确认删除用户「${user.name}」？删除后其订阅链接将失效。`,
      confirmLabel: '删除用户',
      destructive: true,
    }))) {
      return
    }
    setDeleting(user.id)
    try {
      await api.deleteUser(user.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(null)
    }
  }

  const onToggleDisabled = async (user: SubUser) => {
    setToggling(user.id)
    try {
      await api.setUserDisabled(user.id, !user.disabled)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setToggling(null)
    }
  }

  const onOpenAssign = (u: SubUser) => {
    setAssignTarget(u)
    setAssignSelection(u.chain_ids)
    setAssignExt(
      Object.fromEntries(u.external_subscriptions.map((s) => [s.subscription_id, s.mode])),
    )
    setAssignError('')
  }

  const onToggleNode = (id: number, checked: boolean) => {
    setAssignSelection((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  const onSaveAssign = async () => {
    if (!assignTarget) {
      return
    }
    setAssignError('')
    setAssignSaving(true)
    try {
			await api.setUserAssignments(assignTarget.id, assignTarget.node_ids, assignSelection)
      await api.setUserExternalSubscriptions(
        assignTarget.id,
        Object.entries(assignExt).map(([id, mode]) => ({ subscription_id: Number(id), mode })),
      )
      setAssignTarget(null)
      load()
    } catch (err) {
      setAssignError(errorMessage(err))
    } finally {
      setAssignSaving(false)
    }
  }

  const onOpenSubSettings = (u: SubUser) => {
    const trafficLimit = formatTrafficLimit(u.traffic_limit)
    setSubTarget(u)
    setSubTrafficLimit(trafficLimit.value)
    setSubTrafficUnit(trafficLimit.unit)
    setSubResetDay(u.traffic_reset_day > 0 ? String(u.traffic_reset_day) : '')
    setSubTitleOverride(u.sub_title)
    setSubAnnouncementOverride(u.sub_announcement)
    setSubPlanName(u.plan_name)
    setSubAppURL(u.app_url)
    setSubRouting(u.routing)
    setSubErr('')
  }

  const onSaveSubSettings = async () => {
    if (!subTarget) return
    setSubErr('')
    setSubSaving(true)
    try {
      const trafficLimit = parseTrafficLimit(subTrafficLimit, subTrafficUnit)
      const resetDay = parseTrafficResetDay(subResetDay)
      await api.updateUserSubSettings({
        user_id: subTarget.id,
        traffic_limit: trafficLimit,
        traffic_reset_day: resetDay,
        sub_title: subTitleOverride,
        sub_announcement: subAnnouncementOverride,
        plan_name: subPlanName,
        app_url: subAppURL,
        routing: subRouting,
      })
      setSubTarget(null)
      load()
    } catch (err) {
      setSubErr(errorMessage(err))
    } finally {
      setSubSaving(false)
    }
  }

  const onRegenerate = async (user: SubUser) => {
    setRegenerating(user.id)
    setError('')
    try {
      await api.regenerateUserSubscription(user.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRegenerating(null)
    }
  }

  const onResetToken = async (user: SubUser) => {
    if (!(await confirm({
      title: '重置订阅地址',
      description: `确认重置「${user.name}」的订阅地址？新地址将立即生效，旧链接立即失效，客户端需要重新导入新链接。`,
      confirmLabel: '重置订阅地址',
      destructive: true,
    }))) {
      return
    }
    setResettingToken(user.id)
    setError('')
    try {
      await api.resetUserSubscriptionToken(user.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setResettingToken(null)
    }
  }

  const loadSubscriptionPreview = async (user: SubUser, format: SubscriptionPreviewFormat) => {
    setPreviewLoading(true)
    setPreviewError('')
    try {
      setPreviewData(await api.userSubscriptionPreview(user.id, format))
    } catch (err) {
      setPreviewData(null)
      setPreviewError(errorMessage(err))
    } finally {
      setPreviewLoading(false)
    }
  }

  const onOpenPreview = (user: SubUser) => {
    setPreviewTarget(user)
    setPreviewFormat('clash')
    setPreviewData(null)
    void loadSubscriptionPreview(user, 'clash')
  }

  const onPreviewFormatChange = (format: SubscriptionPreviewFormat) => {
    setPreviewFormat(format)
    if (previewTarget) void loadSubscriptionPreview(previewTarget, format)
  }

  const onOpenHistory = async (u: SubUser) => {
    setHistoryTarget(u)
    setHistoryLoading(true)
    setHistoryData([])
    try {
      setHistoryData(await api.userTrafficHistory(u.id))
    } catch {
      // ignore
    } finally {
      setHistoryLoading(false)
    }
  }

  return (
    <Page>
      <PageHeader
        title="用户"
        actions={(
          <Button onClick={() => setOpen(true)}>
            <PlusIcon />
            创建用户
          </Button>
        )}
      />

      {error && <Notice tone="danger">{error}</Notice>}

      {!loading && users.length === 0 ? (
        <EmptyState
          icon={<UsersIcon />}
          title="暂无用户"
          description="点击上方“创建用户”开始"
        />
      ) : null}
      <Surface className={!loading && users.length === 0 ? 'hidden' : undefined}>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>姓名</TableHead>
              <TableHead>链路</TableHead>
              <TableHead>流量</TableHead>
              <TableHead>有效期</TableHead>
              <TableHead>订阅链接</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : (
              users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {u.name}
                      {u.disabled && <Badge variant="destructive">已停用</Badge>}
                      {u.expired && <Badge variant="destructive">已到期</Badge>}
                      {u.subscription_snapshot.status === 'ready' ? (
                        <Badge variant="secondary">订阅 r{u.subscription_snapshot.revision}</Badge>
                      ) : (
                        <Badge variant="destructive" title={u.subscription_snapshot.error}>订阅异常</Badge>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
					{u.chain_ids.length === 0 ? (
                      <span className="text-muted-foreground">未分配</span>
                    ) : (
						`${u.chain_ids.filter((id) => linkOptions.some((link) => link.chainId === id)).length} / ${linkOptions.length}`
                    )}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {u.traffic ? `↑${humanizeBytes(u.traffic.up)} ↓${humanizeBytes(u.traffic.down)}` : '-'}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {u.expires_at ? formatDateTime(u.expires_at, timezone) : <span className="text-muted-foreground">长期</span>}
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-md items-center gap-2">
                      <span className="truncate font-mono text-xs text-muted-foreground" title={u.sub_url}>
                        {u.sub_url}
                      </span>
                      <CopyButton text={u.sub_url} />
                      <span title="复制分享链接订阅（vless:// 等链接集合）">
                        <CopyButton text={u.sub_links_url} />
                      </span>
                      <Button variant="ghost" size="icon" title="订阅二维码" onClick={() => setQrText(u.sub_url)}>
                        <QrCodeIcon />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="打开订阅落地页（浏览器）"
                        onClick={() => window.open(u.sub_url, '_blank', 'noopener')}
                      >
                        <ExternalLinkIcon />
                      </Button>
                    </div>
                  </TableCell>
                  <TableCell>{formatDateTime(u.created_at, timezone)}</TableCell>
                  <TableCell className="space-x-2">
                    <Button variant="outline" size="sm" onClick={() => onOpenAssign(u)}>
                      分配链路
                    </Button>
                    <Button variant="outline" size="sm" title="订阅设置" onClick={() => onOpenSubSettings(u)}>
                      <Settings2Icon />
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      title="重新生成全部订阅格式"
                      disabled={regenerating === u.id}
                      onClick={() => onRegenerate(u)}
                    >
                      <RefreshCwIcon className={regenerating === u.id ? 'animate-spin' : undefined} />
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      title="重置订阅地址（更换链接，旧链接立即失效）"
                      disabled={resettingToken === u.id}
                      onClick={() => onResetToken(u)}
                    >
                      <KeyRoundIcon className={resettingToken === u.id ? 'animate-spin' : undefined} />
                    </Button>
                    <Button variant="outline" size="sm" title="结果预览" onClick={() => onOpenPreview(u)}>
                      <EyeIcon />
                    </Button>
                    <Button variant="outline" size="sm" title="流量历史" onClick={() => onOpenHistory(u)}>
                      <HistoryIcon />
                    </Button>
                    <Button variant="outline" size="sm" title="修改有效期" onClick={() => onOpenExpiry(u)}>
                      <CalendarClockIcon />
                      有效期
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      title={u.disabled ? '启用（恢复链路下发与订阅）' : '停用（立即停权，订阅链路清空）'}
                      disabled={toggling === u.id}
                      onClick={() => onToggleDisabled(u)}
                    >
                      {u.disabled ? <CircleCheckIcon /> : <BanIcon />}
                      {toggling === u.id ? '处理中…' : u.disabled ? '启用' : '停用'}
                    </Button>
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={deleting === u.id}
                      onClick={() => onDelete(u)}
                    >
                      <Trash2Icon />
                      {deleting === u.id ? '删除中…' : '删除'}
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Surface>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] sm:max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>创建用户</DialogTitle>
            <DialogDescription>
              {created ? '用户已创建，请将订阅链接发给用户。' : '输入姓名创建用户。'}
            </DialogDescription>
          </DialogHeader>
          {created ? (
            <div className="space-y-3">
              <div className="space-y-2">
                <Label>订阅链接</Label>
                <div className="rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs break-all">
                  {created.sub_url}
                </div>
              </div>
              <DialogFooter showCloseButton>
                <CopyButton text={created.sub_url} />
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="name">姓名</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：张三"
                  required
                  autoFocus
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="expires-at">有效期（可选，留空为长期）</Label>
                <Input
                  id="expires-at"
                  type="datetime-local"
                  value={expiresAt}
                  onChange={(e) => setExpiresAt(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label>分配链路（可选）</Label>
                {linkOptions.length === 0 ? (
                  <p className="text-sm text-muted-foreground">暂无链路，请先在「链路」页创建。</p>
                ) : (
                  linkOptions.map((link) => (
					<label key={link.chainId} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
						checked={createLinkSel.includes(link.chainId)}
						onChange={(e) => onToggleCreateLink(link.chainId, e.target.checked)}
                      />
                      <Badge variant="secondary">{link.type === 'direct' ? '直连' : '中转'}</Badge>
                      <span>{link.name}</span>
                      <span className="text-xs text-muted-foreground">{link.detail}</span>
                    </label>
                  ))
                )}
              </div>
              <div className="space-y-2">
                <Label>订阅设置（可选，留空跟随全局）</Label>
                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="space-y-1">
                    <TrafficLimitInput
                      value={createTrafficLimit}
                      unit={createTrafficUnit}
                      onValueChange={setCreateTrafficLimit}
                      onUnitChange={setCreateTrafficUnit}
                    />
                  </div>
                  <div className="space-y-1">
                    <Input
                      type="number"
                      min={1}
                      max={31}
                      step={1}
                      value={createResetDay}
                      onChange={e => setCreateResetDay(e.target.value)}
                      placeholder="重置日（留空=创建日）"
                    />
                  </div>
                  <div className="space-y-1">
                    <Input
                      value={createPlanName}
                      onChange={e => setCreatePlanName(e.target.value)}
                      placeholder="套餐名，如 VIP1"
                    />
                  </div>
                  <div className="space-y-1">
                    <Input
                      value={createAppURL}
                      onChange={e => setCreateAppURL(e.target.value)}
                      placeholder="客户端跳转链接"
                    />
                  </div>
                </div>
              </div>
              <SubscriptionRoutingFields
                value={createRouting}
                onChange={setCreateRouting}
                categories={ruleCategories}
                templates={templates}
              />
              <div className="space-y-2 border-t pt-3">
                <Label>外部订阅（叠加 = 额度相加，并入 = 已用计入面板配额，附加 = 仅节点）</Label>
                {extSubs.length === 0 ? (
                  <p className="text-sm text-muted-foreground">暂无外部订阅，请先在「外部订阅」页添加。</p>
                ) : (
                  extSubs.map((sub) => {
                    const checked = createExt[sub.id] !== undefined
                    return (
                      <label
                        key={sub.id}
                        className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm"
                      >
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          checked={checked}
                          onChange={(e) => {
                            setCreateExt((cur) => {
                              const next = { ...cur }
                              if (e.target.checked) {
                                next[sub.id] = 'stack'
                              } else {
                                delete next[sub.id]
                              }
                              return next
                            })
                          }}
                        />
                        <span>{sub.name}</span>
                        <span className="text-xs text-muted-foreground">
                          {sub.total > 0
                            ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}`
                            : '额度未知'}
                        </span>
                        <span className="ml-auto">
                          <ExternalModeSelect
                            value={checked ? createExt[sub.id] : 'stack'}
                            disabled={!checked}
                            onChange={(mode) => setCreateExt((cur) => ({ ...cur, [sub.id]: mode }))}
                          />
                        </span>
                      </label>
                    )
                  })
                )}
              </div>
              {createError && <p className="text-sm text-destructive">{createError}</p>}
              <DialogFooter>
                <Button type="submit" disabled={creating || !name.trim()}>
                  {creating ? '创建中…' : '创建'}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
      <QRDialog text={qrText} open={qrText !== ''} onClose={() => setQrText('')} />

      <Dialog open={expiryTarget !== null} onOpenChange={(next) => !next && setExpiryTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>修改有效期</DialogTitle>
            <DialogDescription>
              设置「{expiryTarget?.name}」的到期时刻；到期后自动停权（订阅保留但链路为空），
              延长或清除有效期会恢复其链路。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="edit-expires-at">有效期（留空并保存即为长期）</Label>
            <Input
              id="edit-expires-at"
              type="datetime-local"
              value={expiryValue}
              onChange={(e) => setExpiryValue(e.target.value)}
            />
          </div>
          {expiryError && <p className="text-sm text-destructive">{expiryError}</p>}
          <DialogFooter>
            {expiryTarget?.expires_at && (
              <Button variant="outline" disabled={expirySaving} onClick={() => onSaveExpiry(true)}>
                清除有效期
              </Button>
            )}
            <Button disabled={expirySaving} onClick={() => onSaveExpiry(false)}>
              {expirySaving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={assignTarget !== null} onOpenChange={(next) => !next && setAssignTarget(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>分配链路</DialogTitle>
            <DialogDescription>
              勾选「{assignTarget?.name}」可使用的链路；未勾选的链路不会出现在其订阅中，
              保存后即时下发变更（默认全关，§16）。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {linkOptions.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无链路，请先在「链路」页创建。</p>
            ) : (
              linkOptions.map((link) => (
				<label key={link.chainId} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
					checked={assignSelection.includes(link.chainId)}
					onChange={(e) => onToggleNode(link.chainId, e.target.checked)}
                  />
                  <Badge variant="secondary">{link.type === 'direct' ? '直连' : '中转'}</Badge>
                  <span>{link.name}</span>
                  <span className="text-xs text-muted-foreground">{link.detail}</span>
                  {link.status !== 'active' && (
                    <span className="text-xs text-muted-foreground">（{link.status}）</span>
                  )}
                </label>
              ))
            )}
          </div>
          <div className="space-y-2 border-t pt-3">
            <Label>外部订阅（叠加 = 额度相加，并入 = 已用计入面板配额，附加 = 仅节点）</Label>
            {extSubs.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无外部订阅，请先在「外部订阅」页添加。</p>
            ) : (
              extSubs.map((sub) => {
                const checked = assignExt[sub.id] !== undefined
                return (
                  <label
                    key={sub.id}
                    className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm"
                  >
                    <input
                      type="checkbox"
                      className="size-4 accent-primary"
                      checked={checked}
                      onChange={(e) => {
                        setAssignExt((cur) => {
                          const next = { ...cur }
                          if (e.target.checked) {
                            next[sub.id] = 'stack'
                          } else {
                            delete next[sub.id]
                          }
                          return next
                        })
                      }}
                    />
                    <span>{sub.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {sub.total > 0
                        ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}`
                        : '额度未知'}
                    </span>
                    <span className="ml-auto">
                      <ExternalModeSelect
                        value={checked ? assignExt[sub.id] : 'stack'}
                        disabled={!checked}
                        onChange={(mode) => setAssignExt((cur) => ({ ...cur, [sub.id]: mode }))}
                      />
                    </span>
                  </label>
                )
              })
            )}
          </div>
          {assignTarget && assignTarget.external_subscriptions.length > 0 ? (
            <div className="space-y-2 border-t pt-3">
              <Label>外部订阅统计</Label>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>模式</TableHead>
                    <TableHead>额度</TableHead>
                    <TableHead>已用</TableHead>
                    <TableHead>剩余</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {assignTarget.external_subscriptions.map((s) => (
                    <TableRow key={s.subscription_id}>
                      <TableCell className="text-xs">{s.name}</TableCell>
                      <TableCell className="text-xs">{EXTERNAL_MODE_LABELS[s.mode]}</TableCell>
                      <TableCell className="text-xs">
                        {s.total > 0 ? humanizeBytes(s.total) : '未知'}
                      </TableCell>
                      <TableCell className="text-xs">{humanizeBytes(s.upload + s.download)}</TableCell>
                      <TableCell className="text-xs">
                        {s.remaining === null ? '未知' : humanizeBytes(s.remaining)}
                      </TableCell>
                    </TableRow>
                  ))}
                  {assignTarget.merged_traffic ? (
                    <TableRow>
                      <TableCell className="text-xs font-medium" colSpan={2}>合并后（含面板）</TableCell>
                      <TableCell className="text-xs">
                        {assignTarget.merged_traffic.total > 0 ? humanizeBytes(assignTarget.merged_traffic.total) : '不限'}
                      </TableCell>
                      <TableCell className="text-xs">
                        {humanizeBytes(assignTarget.merged_traffic.upload + assignTarget.merged_traffic.download)}
                      </TableCell>
                      <TableCell className="text-xs">
                        {assignTarget.merged_traffic.total > 0
                          ? humanizeBytes(Math.max(0, assignTarget.merged_traffic.total - assignTarget.merged_traffic.upload - assignTarget.merged_traffic.download))
                          : '—'}
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>
          ) : null}
          {assignError && <p className="text-sm text-destructive">{assignError}</p>}
          <DialogFooter>
            <Button onClick={onSaveAssign} disabled={assignSaving}>
              {assignSaving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={subTarget !== null} onOpenChange={(next) => !next && setSubTarget(null)}>
        <DialogContent className="max-h-[90vh] sm:max-w-5xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>订阅设置</DialogTitle>
            <DialogDescription>
              「{subTarget?.name}」的落地页、分流策略与发布订阅快照。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="grid items-end gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label>流量配额（留空为不限）</Label>
                <TrafficLimitInput
                  value={subTrafficLimit}
                  unit={subTrafficUnit}
                  onValueChange={setSubTrafficLimit}
                  onUnitChange={setSubTrafficUnit}
                />
              </div>
              <div className="space-y-2">
                <Label>重置日（1–31，留空为创建日）</Label>
                <Input
                  type="number"
                  min={1}
                  max={31}
                  step={1}
                  value={subResetDay}
                  onChange={e => setSubResetDay(e.target.value)}
                  placeholder="创建日"
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label>落地页标题覆盖</Label>
              <Input
                value={subTitleOverride}
                onChange={e => setSubTitleOverride(e.target.value)}
                placeholder="留空跟随全局"
              />
            </div>
            <div className="space-y-2">
              <Label>公告覆盖（Markdown）</Label>
              <textarea
                className="w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-xs transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                rows={3}
                value={subAnnouncementOverride}
                onChange={e => setSubAnnouncementOverride(e.target.value)}
                placeholder="留空跟随全局"
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label>套餐名</Label>
                <Input
                  value={subPlanName}
                  onChange={e => setSubPlanName(e.target.value)}
                  placeholder="留空跟随全局"
                />
                <p className="text-xs text-muted-foreground">客户端 hover 流量信息时显示</p>
              </div>
              <div className="space-y-2">
                <Label>跳转链接</Label>
                <Input
                  value={subAppURL}
                  onChange={e => setSubAppURL(e.target.value)}
                  placeholder="留空跟随全局"
                />
                <p className="text-xs text-muted-foreground">客户端流量卡片可点击跳转的按钮</p>
              </div>
            </div>
            <SubscriptionRoutingFields
              value={subRouting}
              onChange={setSubRouting}
              categories={ruleCategories}
              templates={templates}
            />
          </div>
          {subErr && <p className="text-sm text-destructive">{subErr}</p>}
          <DialogFooter>
            <Button disabled={subSaving} onClick={onSaveSubSettings}>
              {subSaving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={previewTarget !== null}
        onOpenChange={(next) => {
          if (!next) {
            setPreviewTarget(null)
            setPreviewData(null)
            setPreviewError('')
          }
        }}
      >
        <DialogContent className="max-h-[90vh] sm:max-w-5xl overflow-hidden">
          <DialogHeader>
            <DialogTitle>订阅结果预览</DialogTitle>
            <DialogDescription>
              「{previewTarget?.name}」已发布快照 r{previewData?.revision ?? previewTarget?.subscription_snapshot.revision ?? 0}
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center justify-between gap-3">
            <Select
              value={previewFormat}
              onValueChange={(value) => value && onPreviewFormatChange(value as SubscriptionPreviewFormat)}
            >
              <SelectTrigger className="w-48" aria-label="订阅格式">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="clash">Mihomo YAML</SelectItem>
                <SelectItem value="singbox">sing-box JSON</SelectItem>
                <SelectItem value="quanx">Quantumult X 节点</SelectItem>
                <SelectItem value="quanx-config">Quantumult X 配置</SelectItem>
                <SelectItem value="links">Base64 分享链接</SelectItem>
              </SelectContent>
            </Select>
            {previewData ? <CopyButton text={previewData.content} /> : null}
          </div>
          {previewData?.warnings && previewData.warnings.length > 0 ? (
            <Notice tone="warning" title="部分条目未纳入本次订阅">
              <ul className="list-disc space-y-1 pl-4">
                {previewData.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </Notice>
          ) : null}
          {previewLoading ? (
            <p className="flex h-80 items-center justify-center text-sm text-muted-foreground">加载中…</p>
          ) : previewError ? (
            <Notice tone="danger">{previewError}</Notice>
          ) : (
            <pre className="h-[min(65vh,42rem)] overflow-auto border bg-muted/40 p-4 font-mono text-xs leading-5 whitespace-pre">
              {previewData?.content ?? ''}
            </pre>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={historyTarget !== null} onOpenChange={(next) => !next && setHistoryTarget(null)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>流量历史</DialogTitle>
            <DialogDescription>
              「{historyTarget?.name}」的周期流量归档记录。
            </DialogDescription>
          </DialogHeader>
          {historyLoading ? (
            <p className="py-4 text-center text-sm text-muted-foreground">加载中…</p>
          ) : historyData.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted-foreground">暂无历史记录</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>周期开始</TableHead>
                  <TableHead>上行</TableHead>
                  <TableHead>下行</TableHead>
                  <TableHead>合计</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {historyData.map(h => (
                  <TableRow key={h.period_start}>
                    <TableCell className="text-xs">{h.period_start || '-'}</TableCell>
                    <TableCell className="text-xs">{humanizeBytes(h.up)}</TableCell>
                    <TableCell className="text-xs">{humanizeBytes(h.down)}</TableCell>
                    <TableCell className="text-xs">{humanizeBytes(h.up + h.down)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </DialogContent>
      </Dialog>
    </Page>
  )
}
