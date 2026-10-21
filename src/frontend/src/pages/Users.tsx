import { useCallback, useMemo, useRef, useState } from 'react'
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
  RotateCcwIcon,
  RotateCwIcon,
  Settings2Icon,
  EllipsisIcon,
  Trash2Icon,
  UsersIcon,
} from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { QRDialog } from '@/components/QRDialog'
import { TemplateAssignmentTab } from '@/components/TemplateAssignmentTab'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import { chainStatusStyle } from '@/lib/status'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'
import type {
  Chain,
  ChainStatus,
  ExternalSubscription,
  SubUser,
  SubscriptionPreview,
  SubscriptionPreviewFormat,
  SubscriptionPreviewStage,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
  UserGroup,
} from '@/lib/types'

import { CreateUserDialog } from './users/CreateUserDialog'
import { EXTERNAL_MODE_LABELS } from './users/external-mode'
import { ReactivateUserDialog } from './users/ReactivateUserDialog'
import { useCreateUserForm } from './users/use-create-user-form'
import { useReactivateUser } from './users/use-reactivate-user'
import { useUserSubSettings } from './users/use-user-sub-settings'
import { UserSubSettingsDialog } from './users/UserSubSettingsDialog'

import './users.css'

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

  const [assignTarget, setAssignTarget] = useState<SubUser | null>(null)
  const [extSubs, setExtSubs] = useState<ExternalSubscription[]>([])
  const [qrText, setQrText] = useState('')
  const loadRequest = useRef(0)

  const [regenerating, setRegenerating] = useState<number | null>(null)
  const [resettingToken, setResettingToken] = useState<number | null>(null)
  const [resettingTraffic, setResettingTraffic] = useState<number | null>(null)
  const [previewTarget, setPreviewTarget] = useState<SubUser | null>(null)
  const [previewFormat, setPreviewFormat] = useState<SubscriptionPreviewFormat>('clash')
  const [previewStage, setPreviewStage] = useState<SubscriptionPreviewStage>('final')
  const [previewData, setPreviewData] = useState<SubscriptionPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  // 流量历史对话框
  const [historyTarget, setHistoryTarget] = useState<SubUser | null>(null)
  const [historyData, setHistoryData] = useState<
    Array<{ period_start: string; up: number; down: number }>
  >([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [usersTab, setUsersTab] = useState<'users' | 'assign'>('users')

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
    try {
      const [nextUsers, nextChains, nextCategories, nextTemplates, nextUserGroups] =
        await Promise.all([
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

  usePolling(load, loadRequest)

  const createForm = useCreateUserForm({ onSaved: load, showOperation })
  const subSettings = useUserSubSettings({ onSaved: load, showOperation })
  const reactivate = useReactivateUser({ onSaved: load, showOperation })

  const linkOptions = useMemo(() => buildLinkOptions(chains), [chains])

  const assignLinks = useMemo(() => {
    if (!assignTarget) return []
    const effectiveIds = assignTarget.effective_chain_ids ?? assignTarget.chain_ids
    return linkOptions.filter((link) => effectiveIds.includes(link.chainId))
  }, [assignTarget, linkOptions])

  const onlineUsers = users.filter((user) => user.online_connections > 0).length

  const onDelete = async (user: SubUser) => {
    if (
      !(await confirm({
        title: '删除用户',
        description: `确认删除用户「${user.name}」？删除后其订阅链接将失效。`,
        confirmLabel: '删除用户',
        destructive: true,
      }))
    ) {
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

  const onResetTraffic = async (user: SubUser) => {
    if (
      !(await confirm({
        title: '流量重置',
        description: `确认重置「${user.name}」的当期流量？用量将清零并归档到历史${
          user.quota_exhausted ? '，用户将立即恢复访问（节点与订阅）' : ''
        }。`,
        confirmLabel: '重置流量',
        destructive: true,
      }))
    ) {
      return
    }
    setResettingTraffic(user.id)
    setError('')
    try {
      const { observeId } = await api.resetUserTraffic(user.id)
      if (observeId) showOperation({ observeId })
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setResettingTraffic(null)
    }
  }

  const onOpenAssign = (u: SubUser) => {
    setAssignTarget(u)
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
    if (
      !(await confirm({
        title: '重置订阅地址',
        description: `确认重置「${user.name}」的订阅地址？新地址将立即生效，旧链接立即失效，客户端需要重新导入新链接。`,
        confirmLabel: '重置订阅地址',
        destructive: true,
      }))
    ) {
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

  const loadSubscriptionPreview = async (
    user: SubUser,
    format: SubscriptionPreviewFormat,
    stage: SubscriptionPreviewStage,
  ) => {
    setPreviewLoading(true)
    setPreviewError('')
    try {
      setPreviewData(await api.userSubscriptionPreview(user.id, format, stage))
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
    setPreviewStage('final')
    setPreviewData(null)
    void loadSubscriptionPreview(user, 'clash', 'final')
  }

  const onPreviewFormatChange = (format: SubscriptionPreviewFormat) => {
    setPreviewFormat(format)
    if (previewTarget) void loadSubscriptionPreview(previewTarget, format, previewStage)
  }

  const onPreviewStageChange = (stage: SubscriptionPreviewStage) => {
    setPreviewStage(stage)
    if (previewTarget) void loadSubscriptionPreview(previewTarget, previewFormat, stage)
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
    <Page className="cg-page-in">
      <PageHeader
        title="用户"
        description="订阅用户的创建、链路分配、订阅策略与用量概览。"
        actions={
          <Button onClick={createForm.openCreate}>
            <PlusIcon />
            创建用户
          </Button>
        }
      />

      {error && <Notice tone="danger">{error}</Notice>}

      <Tabs
        value={usersTab}
        onValueChange={(value) => value && setUsersTab(value as 'users' | 'assign')}
      >
        <TabsList>
          <TabsTrigger value="users">用户</TabsTrigger>
          <TabsTrigger value="assign">模板指派</TabsTrigger>
        </TabsList>
      </Tabs>
      {usersTab === 'assign' ? (
        <TemplateAssignmentTab
          users={users}
          templates={templates}
          categories={ruleCategories}
          onChanged={() => load(true)}
        />
      ) : (
        <section className="cg-users-board">
          <header className="cg-users-board-head">
            <span className="cg-users-tab">USERS</span>
            <span className="cg-users-board-title">用户读数</span>
            <span className="cg-users-board-meta">
              {loading ? '同步中…' : `${users.length} 用户 · 在线 ${onlineUsers}`}
            </span>
          </header>
          {loading ? (
            <LoadingState className="cg-users-board-state" />
          ) : users.length === 0 ? (
            <EmptyState
              className="cg-users-board-state"
              icon={<UsersIcon />}
              title="暂无用户"
              description="点击上方“创建用户”开始"
            />
          ) : (
            <div className="cg-users-scroll">
              <table className="cg-users-table">
                <thead>
                  <tr>
                    <th scope="col">用户</th>
                    <th scope="col">状态</th>
                    <th scope="col">链路</th>
                    <th scope="col">流量</th>
                    <th scope="col">在线</th>
                    <th scope="col">有效期</th>
                    <th scope="col">订阅地址</th>
                    <th scope="col">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => {
                    const effectiveChainIds = u.effective_chain_ids ?? u.chain_ids
                    const effectiveLinks = effectiveChainIds.filter((id) =>
                      linkOptions.some((link) => link.chainId === id),
                    )
                    const groupNames = u.user_group_ids.map(
                      (id) => userGroups.find((g) => g.id === id)?.name ?? `#${id}`,
                    )
                    const notYetEffective =
                      !!u.effective_at && new Date(u.effective_at).getTime() > Date.now()
                    const trialUsed = (u.traffic?.up ?? 0) + (u.traffic?.down ?? 0)
                    return (
                      <tr
                        key={u.id}
                        className="cg-user-row"
                        data-disabled={u.disabled || undefined}
                      >
                        <th scope="row" className="cg-user-cell">
                          <span className="cg-user-name">{u.name}</span>
                          {u.is_trial && <span className="cg-status is-blue">试用</span>}
                          {groupNames.length > 0 && (
                            <span className="cg-user-groups" title="订阅由用户分组派生">
                              {groupNames.join('、')}
                            </span>
                          )}
                        </th>
                        <td>
                          <span className="cg-user-states">
                            {u.disabled && <span className="cg-status is-red">已停用</span>}
                            {u.expired && <span className="cg-status is-red">已到期</span>}
                            {u.quota_exhausted && (
                              <span className="cg-status is-warn">流量用尽</span>
                            )}
                            {notYetEffective && !u.expired && (
                              <span className="cg-status is-warn">未生效</span>
                            )}
                            {u.subscription_snapshot.status === 'ready' ? (
                              <span className="cg-status is-muted">
                                订阅 r{u.subscription_snapshot.revision}
                              </span>
                            ) : (
                              <span
                                className="cg-status is-red"
                                title={u.subscription_snapshot.error}
                              >
                                订阅异常
                              </span>
                            )}
                          </span>
                        </td>
                        <td className="cg-user-num">
                          {effectiveLinks.length === 0 ? (
                            <span className="cg-muted">未分配</span>
                          ) : (
                            <>
                              {effectiveLinks.length} / {linkOptions.length}
                            </>
                          )}
                        </td>
                        <td className="cg-user-num cg-user-traffic">
                          {u.traffic ? (
                            <>
                              <span>↑ {humanizeBytes(u.traffic.up)}</span>
                              <span>↓ {humanizeBytes(u.traffic.down)}</span>
                              {u.traffic_limit > 0 && (
                                <span
                                  className="cg-muted"
                                  title="当期已用 / 配额（用尽即停权，周期重置或手动重置后恢复）"
                                >
                                  {humanizeBytes(trialUsed)} / {humanizeBytes(u.traffic_limit)}
                                </span>
                              )}
                            </>
                          ) : (
                            <span className="cg-muted">-</span>
                          )}
                        </td>
                        <td className="cg-user-num">
                          {u.online_connections > 0 ? (
                            <span className="cg-user-online">{u.online_connections}</span>
                          ) : (
                            <span className="cg-muted">-</span>
                          )}
                        </td>
                        <td className="cg-user-num cg-user-date">
                          {notYetEffective
                            ? `未生效 · ${u.effective_at ? formatDateTime(u.effective_at, timezone) : ''} 起`
                            : u.expires_at
                              ? formatDateTime(u.expires_at, timezone)
                              : '长期'}
                        </td>
                        <td className="cg-user-sub">
                          <span className="cg-user-suburl-text" title={u.sub_url}>
                            {u.sub_url}
                          </span>
                          <span className="cg-user-suburl-actions">
                            <CopyButton text={u.sub_url} />
                            <span title="复制分享链接订阅（vless:// 等链接集合）">
                              <CopyButton text={u.sub_links_url} />
                            </span>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              title="订阅二维码"
                              onClick={() => setQrText(u.sub_url)}
                            >
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
                        </td>
                        <td>
                          <span className="cg-user-actions">
                            <Button variant="outline" size="sm" onClick={() => onOpenAssign(u)}>
                              查看链路
                            </Button>
                            <DropdownMenu>
                              <DropdownMenuTrigger
                                render={
                                  <Button
                                    variant="ghost"
                                    size="icon-sm"
                                    aria-label={`管理用户 ${u.name}`}
                                  />
                                }
                              >
                                <EllipsisIcon />
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                <DropdownMenuGroup>
                                  <DropdownMenuLabel>用户管理</DropdownMenuLabel>
                                  {u.expired && (
                                    <DropdownMenuItem onClick={() => reactivate.open(u)}>
                                      <RotateCwIcon />
                                      重新激活（重设流量与有效期）
                                    </DropdownMenuItem>
                                  )}
                                  <DropdownMenuItem onClick={() => subSettings.open(u)}>
                                    <Settings2Icon />
                                    订阅设置
                                  </DropdownMenuItem>
                                  <DropdownMenuItem
                                    disabled={regenerating === u.id}
                                    onClick={() => onRegenerate(u)}
                                  >
                                    <RefreshCwIcon />
                                    {regenerating === u.id ? '重新生成中…' : '重新生成全部订阅格式'}
                                  </DropdownMenuItem>
                                  <DropdownMenuItem
                                    disabled={resettingToken === u.id}
                                    onClick={() => onResetToken(u)}
                                  >
                                    <KeyRoundIcon />
                                    {resettingToken === u.id
                                      ? '重置中…'
                                      : '重置订阅地址（更换链接，旧链接立即失效）'}
                                  </DropdownMenuItem>
                                  <DropdownMenuItem onClick={() => onOpenPreview(u)}>
                                    <EyeIcon />
                                    结果预览
                                  </DropdownMenuItem>
                                  <DropdownMenuItem onClick={() => onOpenHistory(u)}>
                                    <HistoryIcon />
                                    流量历史
                                  </DropdownMenuItem>
                                  <DropdownMenuItem
                                    disabled={resettingTraffic === u.id}
                                    onClick={() => onResetTraffic(u)}
                                  >
                                    <RotateCcwIcon />
                                    {resettingTraffic === u.id
                                      ? '重置中…'
                                      : u.quota_exhausted
                                        ? '流量重置（清零用量并恢复访问）'
                                        : '流量重置（清零当期用量）'}
                                  </DropdownMenuItem>
                                  <DropdownMenuItem
                                    disabled={toggling === u.id}
                                    onClick={() => onToggleDisabled(u)}
                                  >
                                    {u.disabled ? <CircleCheckIcon /> : <BanIcon />}
                                    {toggling === u.id
                                      ? '处理中…'
                                      : u.disabled
                                        ? '启用（恢复链路下发与订阅）'
                                        : '停用（立即停权，订阅链路清空）'}
                                  </DropdownMenuItem>
                                </DropdownMenuGroup>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  variant="destructive"
                                  disabled={deleting === u.id}
                                  onClick={() => onDelete(u)}
                                >
                                  <Trash2Icon />
                                  {deleting === u.id ? '删除中…' : '删除用户'}
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </span>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      <CreateUserDialog
        controller={createForm}
        linkOptions={linkOptions}
        ruleCategories={ruleCategories}
        templates={templates}
        extSubs={extSubs}
      />
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
                <>
                  该用户位于用户分组「
                  {assignTarget.user_group_ids
                    .map((id) => userGroups.find((g) => g.id === id)?.name ?? `#${id}`)
                    .join('、')}
                  」中。
                </>
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
                  <span className="cg-status is-blue">
                    {link.type === 'direct' ? '直连' : '中转'}
                  </span>
                  <span>{link.name}</span>
                  <span className="cg-check-row-detail ml-auto text-right">{link.detail}</span>
                  {link.status !== 'active' && (
                    <span className="cg-check-row-detail">
                      （{chainStatusStyle[link.status as ChainStatus]?.label ?? link.status}）
                    </span>
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
                        <TableCell className="text-xs">
                          {humanizeBytes(s.upload + s.download)}
                        </TableCell>
                        <TableCell className="text-xs">
                          {s.remaining === null ? '未知' : humanizeBytes(s.remaining)}
                        </TableCell>
                      </TableRow>
                    ))}
                    {assignTarget.merged_traffic ? (
                      <TableRow>
                        <TableCell className="text-xs font-medium" colSpan={2}>
                          合并后（含面板）
                        </TableCell>
                        <TableCell className="text-xs">
                          {assignTarget.merged_traffic.total > 0
                            ? humanizeBytes(assignTarget.merged_traffic.total)
                            : '不限'}
                        </TableCell>
                        <TableCell className="text-xs">
                          {humanizeBytes(
                            assignTarget.merged_traffic.upload +
                              assignTarget.merged_traffic.download,
                          )}
                        </TableCell>
                        <TableCell className="text-xs">
                          {assignTarget.merged_traffic.total > 0
                            ? humanizeBytes(
                                Math.max(
                                  0,
                                  assignTarget.merged_traffic.total -
                                    assignTarget.merged_traffic.upload -
                                    assignTarget.merged_traffic.download,
                                ),
                              )
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

      <UserSubSettingsDialog
        controller={subSettings}
        ruleCategories={ruleCategories}
        templates={templates}
      />

      <ReactivateUserDialog controller={reactivate} />

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
              「{previewTarget?.name}」已发布快照 r
              {previewData?.revision ?? previewTarget?.subscription_snapshot.revision ?? 0}
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <Select
                value={previewFormat}
                onValueChange={(value) =>
                  value && onPreviewFormatChange(value as SubscriptionPreviewFormat)
                }
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
              <Select
                value={previewStage}
                onValueChange={(value) =>
                  value && onPreviewStageChange(value as SubscriptionPreviewStage)
                }
              >
                <SelectTrigger className="w-36" aria-label="生成阶段">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="final">正式交付</SelectItem>
                  <SelectItem value="pre">预编译中间态</SelectItem>
                </SelectContent>
              </Select>
            </div>
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
            <pre className="cg-terminal cg-users-preview">{previewData?.content ?? ''}</pre>
          )}
        </DialogContent>
      </Dialog>

      <Dialog
        open={historyTarget !== null}
        onOpenChange={(next) => !next && setHistoryTarget(null)}
      >
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>流量历史</DialogTitle>
            <DialogDescription>「{historyTarget?.name}」的周期流量归档记录。</DialogDescription>
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
                {historyData.map((h) => (
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
