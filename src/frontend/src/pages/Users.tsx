import { useCallback, useMemo, useRef, useState } from 'react'
import { useSearch } from 'wouter'
import { FlaskConicalIcon, PlusIcon, RefreshCwIcon, SearchIcon, UsersIcon } from 'lucide-react'

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
import { humanizeBytes } from '@/lib/format'
import { buildLinkOptions } from '@/lib/links'
import { chainStatusStyle } from '@/lib/status'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'
import type {
  Chain,
  ChainStatus,
  SubUser,
  SubscriptionPreview,
  SubscriptionPreviewFormat,
  SubscriptionPreviewStage,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
} from '@/lib/types'

import { CreateUserDialog } from './users/CreateUserDialog'
import { UserCard, type UserCardActions } from './users/UserCard'
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
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<number | null>(null)
  const [toggling, setToggling] = useState<number | null>(null)

  const [contentTarget, setContentTarget] = useState<SubUser | null>(null)
  const [qrText, setQrText] = useState('')
  const loadRequest = useRef(0)

  const [regenerating, setRegenerating] = useState<number | null>(null)
  const [regeneratingAll, setRegeneratingAll] = useState(false)
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
  const [query, setQuery] = useState('')
  const [userFilter, setUserFilter] = useState<'all' | 'online' | 'trial' | 'issue'>('all')
  const searchString = useSearch()
  const highlightedUserId = useMemo(() => {
    const raw = new URLSearchParams(searchString).get('sel')
    return raw && /^\d+$/.test(raw) ? Number(raw) : null
  }, [searchString])

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
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

  const visibleLinks = useMemo(() => {
    if (!contentTarget) return []
    const effectiveIds = contentTarget.effective_chain_ids
    return linkOptions.filter((link) => effectiveIds.includes(link.chainId))
  }, [contentTarget, linkOptions])

  const onlineUsers = users.filter((user) => user.online_connections > 0).length
  const trialUsers = users.filter((user) => user.is_trial).length
  const issueUsers = users.filter(
    (user) =>
      user.disabled ||
      user.expired ||
      user.quota_exhausted ||
      user.subscription_snapshot.status !== 'ready',
  ).length

  const filteredUsers = useMemo(() => {
    const q = query.trim().toLowerCase()
    return users.filter((user) => {
      const hasIssue =
        user.disabled ||
        user.expired ||
        user.quota_exhausted ||
        user.subscription_snapshot.status !== 'ready'
      if (userFilter === 'online' && user.online_connections <= 0) return false
      if (userFilter === 'trial' && !user.is_trial) return false
      if (userFilter === 'issue' && !hasIssue) return false
      return `${user.name} ${user.sub_url}`.toLowerCase().includes(q)
    })
  }, [query, userFilter, users])

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

  const onRegenerate = async (user: SubUser) => {
    setRegenerating(user.id)
    setError('')
    try {
      const { observeId } = await api.regenerateUserSubscription(user.id)
      if (observeId) showOperation({ observeId })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRegenerating(null)
      void load(true)
    }
  }

  const onRegenerateAll = async () => {
    if (
      !(await confirm({
        title: '刷新全部订阅快照',
        description: `确认刷新全部 ${users.length} 个用户的订阅快照？将按各用户当前设置重新生成并发布所有订阅格式，逐个用户推进；单个用户失败不影响其余，失败详情见操作进度。`,
        confirmLabel: '刷新全部快照',
      }))
    ) {
      return
    }
    setRegeneratingAll(true)
    setError('')
    try {
      const { observeId } = await api.regenerateAllUserSubscriptions()
      if (observeId) showOperation({ observeId })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRegeneratingAll(false)
      void load(true)
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

  const cardActions: UserCardActions = {
    onOpenContent: setContentTarget,
    onRegenerate: (user) => void onRegenerate(user),
    onReactivate: reactivate.open,
    onOpenSubSettings: subSettings.open,
    onResetToken: (user) => void onResetToken(user),
    onOpenPreview: onOpenPreview,
    onOpenHistory: (user) => void onOpenHistory(user),
    onResetTraffic: (user) => void onResetTraffic(user),
    onToggleDisabled: (user) => void onToggleDisabled(user),
    onDelete: (user) => void onDelete(user),
    onShowQr: setQrText,
    onOpenSubPage: (url) => window.open(url, '_blank', 'noopener'),
  }

  return (
    <Page className="cg-page-in">
      <PageHeader
        title="用户"
        description="订阅用户的创建、链路分配、订阅策略与用量概览。"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              title="按各用户当前设置重新生成并发布全部用户的订阅快照"
              disabled={regeneratingAll || loading || users.length === 0}
              onClick={onRegenerateAll}
            >
              <RefreshCwIcon />
              {regeneratingAll ? '刷新中…' : '刷新全部快照'}
            </Button>
            <Button variant="outline" onClick={() => createForm.openCreate('trial')}>
              <FlaskConicalIcon />
              创建试用用户
            </Button>
            <Button onClick={() => createForm.openCreate('normal')}>
              <PlusIcon />
              创建用户
            </Button>
          </div>
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
          {!loading && users.length > 0 ? (
            <div className="cg-users-toolbar">
              <div className="cg-search-wrap">
                <SearchIcon />
                <input
                  type="search"
                  className="cg-search-input"
                  placeholder="搜索用户名、订阅令牌…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  aria-label="搜索用户"
                />
              </div>
              <div className="cg-filter-pills" role="group" aria-label="用户筛选">
                {(
                  [
                    { id: 'all', label: '全部', count: users.length },
                    { id: 'online', label: '在线', count: onlineUsers },
                    { id: 'trial', label: '试用', count: trialUsers },
                    { id: 'issue', label: '异常/到期', count: issueUsers },
                  ] as const
                ).map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    className="cg-filter-pill"
                    data-active={userFilter === item.id ? 'true' : 'false'}
                    onClick={() => setUserFilter(item.id)}
                  >
                    <span>{item.label}</span>
                    <span className="cg-filter-pill-count">{item.count}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
          {loading ? (
            <LoadingState className="cg-users-board-state" />
          ) : users.length === 0 ? (
            <EmptyState
              className="cg-users-board-state"
              icon={<UsersIcon />}
              title="暂无用户"
              description="点击上方“创建用户”或“创建试用用户”开始"
            />
          ) : filteredUsers.length === 0 ? (
            <EmptyState
              className="cg-users-board-state"
              icon={<UsersIcon />}
              title="未找到匹配的用户"
              description="尝试调整搜索词或切换上方状态筛选"
            />
          ) : (
            <div className="cg-users-grid">
              {filteredUsers.map((u) => (
                <UserCard
                  key={u.id}
                  user={u}
                  timezone={timezone}
                  highlighted={highlightedUserId === u.id}
                  regenerating={regenerating === u.id}
                  resettingToken={resettingToken === u.id}
                  resettingTraffic={resettingTraffic === u.id}
                  toggling={toggling === u.id}
                  deleting={deleting === u.id}
                  actions={cardActions}
                />
              ))}
            </div>
          )}
        </section>
      )}

      <CreateUserDialog
        controller={createForm}
        ruleCategories={ruleCategories}
        templates={templates}
      />
      <QRDialog text={qrText} open={qrText !== ''} onClose={() => setQrText('')} />

      <Dialog
        open={contentTarget !== null}
        onOpenChange={(next) => !next && setContentTarget(null)}
      >
        <DialogContent className="max-h-[85vh] overflow-y-auto [&>*]:min-w-0">
          <DialogHeader>
            <DialogTitle>查看订阅内容</DialogTitle>
            <DialogDescription>
              「{contentTarget?.name}」当前订阅包含的链路与外部订阅。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label>链路</Label>
            {visibleLinks.length === 0 ? (
              <p className="cg-hint">订阅暂无可见链路。</p>
            ) : (
              visibleLinks.map((link) => (
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
          {contentTarget && (
            <div className="space-y-2 border-t pt-3">
              <Label>外部订阅</Label>
              {contentTarget.external_subscriptions.length === 0 ? (
                <p className="cg-hint">订阅暂无可见的外部订阅。</p>
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
                    {contentTarget.external_subscriptions.map((s) => (
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
                    {contentTarget.merged_traffic ? (
                      <TableRow>
                        <TableCell className="text-xs font-medium" colSpan={2}>
                          合并后（含面板）
                        </TableCell>
                        <TableCell className="text-xs">
                          {contentTarget.merged_traffic.total > 0
                            ? humanizeBytes(contentTarget.merged_traffic.total)
                            : '不限'}
                        </TableCell>
                        <TableCell className="text-xs">
                          {humanizeBytes(
                            contentTarget.merged_traffic.upload +
                              contentTarget.merged_traffic.download,
                          )}
                        </TableCell>
                        <TableCell className="text-xs">
                          {contentTarget.merged_traffic.total > 0
                            ? humanizeBytes(
                                Math.max(
                                  0,
                                  contentTarget.merged_traffic.total -
                                    contentTarget.merged_traffic.upload -
                                    contentTarget.merged_traffic.download,
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
