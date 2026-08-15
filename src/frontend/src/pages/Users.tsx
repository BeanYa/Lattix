import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import {
  BanIcon,
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
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { QRDialog } from '@/components/QRDialog'
import { SubscriptionRoutingFields } from '@/components/SubscriptionRoutingFields'
import { TemplateAssignmentTab } from '@/components/TemplateAssignmentTab'
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
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
import { useOperationProgress } from '@/lib/operation-progress-context'
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { buildLinkOptions } from '@/lib/links'
import { defaultSubscriptionRouting } from '@/lib/subscription-routing'
import { useTimezone } from '@/lib/timezone'
import {
  expiryDateDay,
  formatTrafficLimit,
  localDateToRFC3339EndOfDay,
  parseTrafficLimit,
  parseTrafficResetDay,
  toLocalDateInput,
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
  UserGroup,
} from '@/lib/types'

import './users.css'

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

export default function Users() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [users, setUsers] = useState<SubUser[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [ruleCategories, setRuleCategories] = useState<SubscriptionRuleCategory[]>([])
  const [templates, setTemplates] = useState<SubscriptionTemplate[]>([])
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
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

  const [assignTarget, setAssignTarget] = useState<SubUser | null>(null)
  const [extSubs, setExtSubs] = useState<ExternalSubscription[]>([])
  const [createExt, setCreateExt] = useState<Record<number, ExternalSubscriptionMode>>({})
  const [qrText, setQrText] = useState('')
  const loadRequest = useRef(0)

  // 用户订阅设置对话框
  const [subTarget, setSubTarget] = useState<SubUser | null>(null)
  const [subExpiresAt, setSubExpiresAt] = useState('')
  const [subExpiryTouched, setSubExpiryTouched] = useState(false)
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
  const [usersTab, setUsersTab] = useState<'users' | 'assign'>('users')

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent ? { display: 'silent' as const } : undefined
    try {
      const [nextUsers, nextChains, nextCategories, nextTemplates, nextUserGroups] = await Promise.all([
        api.users(options),
        api.chains(options),
        api.subscriptionCategories(options),
        api.subscriptionTemplates(options),
        api.userGroups(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setUsers(nextUsers)
      setChains(nextChains)
      setRuleCategories(nextCategories)
      setTemplates(nextTemplates)
      setUserGroups(nextUserGroups)
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

  const assignLinks = useMemo(() => {
    if (!assignTarget) return []
    const effectiveIds = assignTarget.effective_chain_ids ?? assignTarget.chain_ids
    return linkOptions.filter((link) => effectiveIds.includes(link.chainId))
  }, [assignTarget, linkOptions])

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const trafficLimit = parseTrafficLimit(createTrafficLimit, createTrafficUnit)
      const resetDay = parseTrafficResetDay(createResetDay)
      const planName = createPlanName.trim()
      const appURL = createAppURL.trim()
      const { data: res, observeId } = await api.createUser(
        name.trim(),
        localDateToRFC3339EndOfDay(expiresAt),
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
      if (observeId) showOperation({ observeId })
      setCreated(res)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
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
      const { observeId } = await api.setUserDisabled(user.id, !user.disabled)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setToggling(null)
    }
  }

  const onOpenAssign = (u: SubUser) => {
    setAssignTarget(u)
  }

  const onOpenSubSettings = (u: SubUser) => {
    const trafficLimit = formatTrafficLimit(u.traffic_limit)
    setSubTarget(u)
    setSubExpiresAt(u.expires_at ? toLocalDateInput(u.expires_at) : '')
    setSubExpiryTouched(false)
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
      const { observeId } = await api.updateUserSubSettings({
        user_id: subTarget.id,
        traffic_limit: trafficLimit,
        traffic_reset_day: resetDay,
        sub_title: subTitleOverride,
        sub_announcement: subAnnouncementOverride,
        plan_name: subPlanName,
        app_url: subAppURL,
        routing: subRouting,
        expires_at: subExpiryTouched ? localDateToRFC3339EndOfDay(subExpiresAt) : subTarget.expires_at,
      })
      if (observeId) showOperation({ observeId })
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
      const { observeId } = await api.regenerateUserSubscription(user.id)
      if (observeId) showOperation({ observeId })
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
      <span className="cg-eyebrow">ACCESS / USERS</span>
      <PageHeader
        title="用户"
        description="订阅用户的创建、链路分配、订阅策略与用量概览。"
        actions={(
          <Button onClick={() => setOpen(true)}>
            <PlusIcon />
            创建用户
          </Button>
        )}
      />

      {error && <Notice tone="danger">{error}</Notice>}

      <Tabs value={usersTab} onValueChange={(value) => value && setUsersTab(value as 'users' | 'assign')}>
        <TabsList>
          <TabsTrigger value="users">用户</TabsTrigger>
          <TabsTrigger value="assign">模板指派</TabsTrigger>
        </TabsList>
      </Tabs>
      {usersTab === 'assign' ? (
        <TemplateAssignmentTab users={users} templates={templates} categories={ruleCategories} onChanged={() => load(true)} />
      ) : (
        <>
      {!loading && users.length === 0 ? (
        <EmptyState
          icon={<UsersIcon />}
          title="暂无用户"
          description="点击上方“创建用户”开始"
        />
      ) : null}
      {!loading && users.length === 0 ? null : (
        <div className="cg-users-list">
          {loading ? (
            <LoadingState />
          ) : (
            users.map((u) => {
              const effectiveChainIds = u.effective_chain_ids ?? u.chain_ids
              const effectiveLinks = effectiveChainIds.filter((id) => linkOptions.some((link) => link.chainId === id))
              return (
              <Card key={u.id}>
                <CardHeader className="border-b">
                  <CardTitle className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="truncate">{u.name}</span>
                    {u.user_group_ids.length > 0 && (
                      <span className="cg-status is-blue" title="订阅由用户分组派生">
                        {u.user_group_ids.map((id) => userGroups.find((g) => g.id === id)?.name ?? `#${id}`).join('、')}
                      </span>
                    )}
                    {u.disabled && <span className="cg-status is-red">已停用</span>}
                    {u.expired && <span className="cg-status is-red">已到期</span>}
                    {u.subscription_snapshot.status === 'ready' ? (
                      <span className="cg-status is-lime">订阅 r{u.subscription_snapshot.revision}</span>
                    ) : (
                      <span className="cg-status is-red" title={u.subscription_snapshot.error}>订阅异常</span>
                    )}
                  </CardTitle>
                </CardHeader>
                <CardContent className="cg-user-metrics">
                  <div className="cg-user-metric">
                    <span className="cg-user-metric-label">链路 / LINKS</span>
                    {effectiveLinks.length === 0 ? (
                      <span className="cg-user-metric-value is-muted">未分配</span>
                    ) : (
                      <span className="cg-user-metric-value">{effectiveLinks.length} / {linkOptions.length}</span>
                    )}
                  </div>
                  <div className="cg-user-metric">
                    <span className="cg-user-metric-label">流量 / TRAFFIC</span>
                    {u.traffic ? (
                      <span className="cg-user-metric-value whitespace-nowrap">↑{humanizeBytes(u.traffic.up)} ↓{humanizeBytes(u.traffic.down)}</span>
                    ) : (
                      <span className="cg-user-metric-value is-muted">-</span>
                    )}
                  </div>
                  <div className="cg-user-metric">
                    <span className="cg-user-metric-label">在线 / ONLINE</span>
                    {u.online_connections > 0 ? (
                      <span><span className="cg-status is-lime">{u.online_connections}</span></span>
                    ) : (
                      <span className="cg-user-metric-value is-muted">-</span>
                    )}
                  </div>
                  <div className="cg-user-metric">
                    <span className="cg-user-metric-label">有效期 / EXPIRES</span>
                    {u.expires_at ? (
                      <span className="cg-user-metric-value">{formatDateTime(u.expires_at, timezone)}</span>
                    ) : (
                      <span className="cg-user-metric-value is-muted">长期</span>
                    )}
                  </div>
                  <div className="cg-user-metric">
                    <span className="cg-user-metric-label">创建时间 / CREATED</span>
                    <span className="cg-user-metric-value">{formatDateTime(u.created_at, timezone)}</span>
                  </div>
                  <div className="cg-user-metric cg-user-metric-wide">
                    <span className="cg-user-metric-label">订阅链接 / SUB URL</span>
                    <div className="cg-user-suburl">
                      <span className="cg-user-suburl-text" title={u.sub_url}>
                        {u.sub_url}
                      </span>
                      <span className="cg-user-suburl-actions">
                        <CopyButton text={u.sub_url} />
                        <span title="复制分享链接订阅（vless:// 等链接集合）">
                          <CopyButton text={u.sub_links_url} />
                        </span>
                        <Button variant="ghost" size="icon-sm" title="订阅二维码" onClick={() => setQrText(u.sub_url)}>
                          <QrCodeIcon />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="打开订阅落地页（浏览器）"
                          onClick={() => window.open(u.sub_url, '_blank', 'noopener')}
                        >
                          <ExternalLinkIcon />
                        </Button>
                      </span>
                    </div>
                  </div>
                </CardContent>
                <CardFooter className="flex flex-wrap gap-2">
                  <Button variant="outline" size="sm" onClick={() => onOpenAssign(u)}>
                    查看链路
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
                    className="ml-auto"
                    disabled={deleting === u.id}
                    onClick={() => onDelete(u)}
                  >
                    <Trash2Icon />
                    {deleting === u.id ? '删除中…' : '删除'}
                  </Button>
                </CardFooter>
              </Card>
              )
            })
          )}
        </div>
      )}
        </>
      )}

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] sm:max-w-4xl overflow-y-auto [&>*]:min-w-0">
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
                <div className="cg-users-created-url">{created.sub_url}</div>
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
                <Label htmlFor="expires-at">有效期（可选，留空为长期；选日期则当天 23:59 到期）</Label>
                <Input
                  id="expires-at"
                  type="date"
                  value={expiresAt}
                  onChange={(e) => {
                    setExpiresAt(e.target.value)
                    if (!createResetDay) setCreateResetDay(expiryDateDay(e.target.value))
                  }}
                />
                {expiresAt && <p className="cg-hint">重置日默认取到期日（可修改）。</p>}
              </div>
              <div className="space-y-2">
                <Label>分配链路（可选）</Label>
                {linkOptions.length === 0 ? (
                  <p className="cg-hint">暂无链路，请先在「链路」页创建。</p>
                ) : (
                  linkOptions.map((link) => (
					<label key={link.chainId} className="cg-check-row">
                      <input
                        type="checkbox"
                        className="cg-checkbox"
						checked={createLinkSel.includes(link.chainId)}
						onChange={(e) => onToggleCreateLink(link.chainId, e.target.checked)}
                      />
                      <span className="cg-status is-blue">{link.type === 'direct' ? '直连' : '中转'}</span>
                      <span>{link.name}</span>
                      <span className="cg-check-row-detail">{link.detail}</span>
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
                      placeholder="重置日（留空跟随有效期/创建日）"
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
                  <p className="cg-hint">暂无外部订阅，请先在「外部订阅」页添加。</p>
                ) : (
                  extSubs.map((sub) => {
                    const checked = createExt[sub.id] !== undefined
                    return (
                      <label
                        key={sub.id}
                        className="cg-check-row"
                      >
                        <input
                          type="checkbox"
                          className="cg-checkbox"
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
                        <span className="cg-check-row-detail">
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
              {createError && <Notice tone="danger">{createError}</Notice>}
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

      <Dialog open={assignTarget !== null} onOpenChange={(next) => !next && setAssignTarget(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto [&>*]:min-w-0">
          <DialogHeader>
            <DialogTitle>查看链路</DialogTitle>
            <DialogDescription>
              「{assignTarget?.name}」实际生效的链路与外部订阅，仅供查看，不可在此修改。
            </DialogDescription>
          </DialogHeader>
          {assignTarget && (
            <Notice tone="info">
              {assignTarget.user_group_ids.length > 0 && (
                <>该用户位于用户分组「{assignTarget.user_group_ids.map((id) => userGroups.find((g) => g.id === id)?.name ?? `#${id}`).join('、')}」中。</>
              )}
              链路绑定与用户分组和链路分组有关，如需调整该用户可用链路，请在「分组」页为相应分组分配链路。
            </Notice>
          )}
          <div className="space-y-2">
            <Label>链路</Label>
            {assignLinks.length === 0 ? (
              <p className="cg-hint">未分配到链路。</p>
            ) : (
              assignLinks.map((link) => (
                <div key={link.chainId} className="cg-check-row">
                  <span className="cg-status is-blue">{link.type === 'direct' ? '直连' : '中转'}</span>
                  <span>{link.name}</span>
                  <span className="cg-check-row-detail ml-auto text-right">{link.detail}</span>
                  {link.status !== 'active' && (
                    <span className="cg-check-row-detail">（{link.status}）</span>
                  )}
                </div>
              ))
            )}
          </div>
          {assignTarget && (
            <div className="space-y-2 border-t pt-3">
              <Label>外部订阅</Label>
              {assignTarget.external_subscriptions.length === 0 ? (
                <p className="cg-hint">未分配外部订阅。</p>
              ) : (
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
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={subTarget !== null} onOpenChange={(next) => !next && setSubTarget(null)}>
        <DialogContent className="max-h-[90vh] sm:max-w-5xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>订阅设置</DialogTitle>
            <DialogDescription>
              「{subTarget?.name}」的有效期、落地页、分流策略与发布订阅快照。
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
                <Label>重置日（1–31，留空跟随有效期到期日/创建日）</Label>
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
              <Label htmlFor="sub-expires-at">有效期（留空并保存即为长期；选日期则当天 23:59 到期）</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="sub-expires-at"
                  type="date"
                  className="flex-1"
                  value={subExpiresAt}
                  onChange={(e) => {
                    setSubExpiresAt(e.target.value)
                    setSubExpiryTouched(true)
                    if (!subResetDay) setSubResetDay(expiryDateDay(e.target.value))
                  }}
                />
                {subExpiresAt && (
                  <Button type="button" variant="outline" onClick={() => { setSubExpiresAt(''); setSubExpiryTouched(true) }}>
                    清除有效期
                  </Button>
                )}
              </div>
              <p className="cg-hint">
                到期后自动停权（订阅保留但链路为空）；延长或清除有效期会恢复其链路。
              </p>
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
                className="cg-textarea"
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
                <p className="cg-hint">客户端 hover 流量信息时显示</p>
              </div>
              <div className="space-y-2">
                <Label>跳转链接</Label>
                <Input
                  value={subAppURL}
                  onChange={e => setSubAppURL(e.target.value)}
                  placeholder="留空跟随全局"
                />
                <p className="cg-hint">客户端流量卡片可点击跳转的按钮</p>
              </div>
            </div>
            <SubscriptionRoutingFields
              value={subRouting}
              onChange={setSubRouting}
              categories={ruleCategories}
              templates={templates}
            />
          </div>
          {subErr && <Notice tone="danger">{subErr}</Notice>}
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
            <p className="cg-hint cg-users-preview-loading">加载中…</p>
          ) : previewError ? (
            <Notice tone="danger">{previewError}</Notice>
          ) : (
            <pre className="cg-terminal cg-users-preview">
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
            <p className="cg-hint py-4 text-center">加载中…</p>
          ) : historyData.length === 0 ? (
            <p className="cg-hint py-4 text-center">暂无历史记录</p>
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
