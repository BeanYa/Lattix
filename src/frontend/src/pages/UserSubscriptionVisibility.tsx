import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'

import { ExternalLinkIcon } from 'lucide-react'
import { useLocation, useSearch } from 'wouter'

import {
  EmptyState,
  LoadingState,
  Notice,
  Page,
  PageHeader,
  Surface,
} from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { requester } from '@/lib/requester'
import type { SubUser } from '@/lib/types'

import { PublicationNotice } from './subscription/PublicationNotice'
import { usePublicationJobs, type PublicationJob } from './subscription/publication-jobs'
import {
  VisibilityEditor,
  type VisibilityExternalSubscription,
  type VisibilityLink,
} from './subscription/VisibilityEditor'
import './subscription/visibility-admin.css'

const PAGE_TITLE = '可见性管理'
const PAGE_DESCRIPTION = '为指定用户选择订阅中显示的链路和外部订阅。'

interface UserVisibility {
  links: VisibilityLink[]
  external_subscriptions: VisibilityExternalSubscription[]
  hidden_ids: string[]
}

interface UserOption {
  value: string
  label: string
}

type StatusTone = 'ok' | 'warning' | 'danger' | 'unknown'
type SaveTone = 'ok' | 'info' | 'dirty' | 'danger' | 'muted'

/** changesBetween 统计两份隐藏集合之间发生变化的项目数（脏状态读数）。 */
function changesBetween(next: ReadonlySet<string>, baseline: ReadonlySet<string>) {
  const ids = new Set([...next, ...baseline])
  let changed = 0
  for (const id of ids) if (next.has(id) !== baseline.has(id)) changed += 1
  return changed
}

/** userStatus 用户订阅状态 → 状态词 + 状态色；颜色只做冗余，文字始终可读。 */
function userStatus(user: SubUser): { label: string; tone: StatusTone } {
  if (user.disabled) return { label: '已停用', tone: 'unknown' }
  if (user.expired) return { label: '已过期', tone: 'warning' }
  if (user.quota_exhausted) return { label: '流量耗尽', tone: 'danger' }
  return { label: '订阅正常', tone: 'ok' }
}

function userInitial(name: string) {
  return Array.from(name.trim())[0] ?? '用'
}

function PageScaffold({ children }: { children: ReactNode }) {
  return (
    <Page className="vis-admin-page cg-page-in">
      <PageHeader title={PAGE_TITLE} description={PAGE_DESCRIPTION} />
      {children}
    </Page>
  )
}

function Readout({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="vis-scope-readout">
      <dt className="vis-scope-label">{label}</dt>
      <dd className="vis-scope-value">{value}</dd>
    </div>
  )
}

function VisibilityWorkspace({
  user,
  options,
  onSelectUser,
}: {
  user?: SubUser
  options: UserOption[]
  onSelectUser: (value: string | null) => void
}) {
  const userId = user?.id
  const [catalog, setCatalog] = useState<UserVisibility | null>(null)
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(new Set())
  const [savedIds, setSavedIds] = useState<Set<string>>(new Set())
  const [loadError, setLoadError] = useState('')
  const [saveError, setSaveError] = useState('')
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  const [lastJobId, setLastJobId] = useState('')

  useEffect(() => {
    if (userId === undefined) return
    const controller = new AbortController()
    requester
      .get<UserVisibility>(
        '/api/user/sub-visibility',
        { user_id: userId },
        { signal: controller.signal },
      )
      .then((data) => {
        if (controller.signal.aborted) return
        setCatalog(data)
        setHiddenIds(new Set(data.hidden_ids))
        setSavedIds(new Set(data.hidden_ids))
      })
      .catch((error) => {
        if (!controller.signal.aborted) setLoadError(errorMessage(error))
      })
    return () => controller.abort()
  }, [userId, loadAttempt])

  const loadJobs = useCallback(
    (signal: AbortSignal) =>
      userId === undefined
        ? Promise.resolve({ jobs: [] as PublicationJob[] })
        : requester.get<{ jobs: PublicationJob[] }>(
            '/api/user/sub-publication-jobs',
            { user_id: userId },
            { signal, display: 'silent' },
          ),
    [userId],
  )
  const { pending, settled, pollFailed, addJob } = usePublicationJobs(loadJobs, catalog !== null)
  const lastJobFailed = settled.some((job) => job.id === lastJobId && job.status === 'failed')

  const items: VisibilityLink[] = catalog
    ? [...catalog.links, ...catalog.external_subscriptions]
    : []
  const visibleCount = items.filter((item) => !hiddenIds.has(item.id)).length
  const hiddenCount = items.length - visibleCount
  const externalNodeCount = (catalog?.external_subscriptions ?? []).reduce(
    (sum, source) => sum + source.nodes.length,
    0,
  )

  const changes = changesBetween(hiddenIds, savedIds)
  const dirty = changes > 0
  const publishing = pending.length > 0
  const hasItems = items.length > 0
  const canSave = !saving && !loadError && hasItems && (dirty || lastJobFailed)

  const status = user ? userStatus(user) : null

  let chipTone: SaveTone = 'ok'
  let chipText = '已保存'
  if (!user) {
    chipTone = 'muted'
    chipText = '未选择用户'
  } else if (loadError) {
    chipTone = 'danger'
    chipText = '读取失败'
  } else if (!catalog) {
    chipTone = 'info'
    chipText = '正在读取…'
  } else if (saving) {
    chipTone = 'info'
    chipText = '正在提交…'
  } else if (dirty) {
    chipTone = 'dirty'
    chipText = `有 ${changes} 项更改未保存`
  } else if (publishing) {
    chipTone = 'info'
    chipText = pending.length > 1 ? `${pending.length} 个请求发布中` : '正在发布…'
  } else if (lastJobFailed) {
    chipTone = 'danger'
    chipText = '上次发布失败'
  }

  const footerText = saving
    ? '正在提交发布请求…'
    : dirty
      ? `有 ${changes} 项更改尚未保存`
      : publishing
        ? '已提交，正在后台发布…'
        : lastJobFailed
          ? '上次发布失败，可重新提交。'
          : '已与发布版本一致'
  const footerTone: SaveTone = saveError
    ? 'danger'
    : saving
      ? 'info'
      : dirty
        ? 'dirty'
        : publishing
          ? 'info'
          : lastJobFailed
            ? 'danger'
            : 'muted'

  function changeVisibility(id: string, visible: boolean) {
    setHiddenIds((current) => {
      const next = new Set(current)
      if (visible) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function resetDraft() {
    setHiddenIds(new Set(savedIds))
    setSaveError('')
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSave) return
    setSaving(true)
    setSaveError('')
    try {
      const job = await requester.post<PublicationJob>('/api/user/sub-visibility', {
        user_id: userId,
        hidden_ids: [...hiddenIds],
      })
      addJob(job)
      setSavedIds(hiddenIds)
      setLastJobId(job.id)
    } catch (error) {
      setSaveError(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Page className="vis-admin-page cg-page-in">
      <PageHeader
        title={PAGE_TITLE}
        description={PAGE_DESCRIPTION}
        actions={
          <>
            <span className="vis-save-chip" data-tone={chipTone} role="status">
              {chipText}
            </span>
            {user?.sub_url ? (
              <Button
                variant="outline"
                size="sm"
                nativeButton={false}
                render={<a href={user.sub_url} target="_blank" rel="noreferrer" role="link" />}
              >
                打开订阅页
                <ExternalLinkIcon aria-hidden="true" />
              </Button>
            ) : (
              <Button
                variant="outline"
                size="sm"
                disabled
                title={user ? '该用户还没有订阅地址' : '先选择订阅用户'}
              >
                打开订阅页
                <ExternalLinkIcon aria-hidden="true" />
              </Button>
            )}
          </>
        }
      />

      <div className="vis-admin-layout">
        <div className="vis-admin-side">
          <Surface className="vis-admin-card vis-admin-user">
            <h2 className="module-title">订阅用户</h2>
            <div className="vis-admin-field">
              <Label htmlFor="visibility-user">切换用户</Label>
              <Select
                value={user ? String(user.id) : null}
                items={options}
                onValueChange={onSelectUser}
              >
                <SelectTrigger id="visibility-user" className="w-full">
                  <SelectValue placeholder="选择用户" />
                </SelectTrigger>
                <SelectContent>
                  {options.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {user ? (
              <div className="vis-user-identity">
                <span className="vis-user-avatar" aria-hidden="true">
                  {userInitial(user.name)}
                </span>
                <div className="vis-user-copy">
                  <span className="vis-user-name">{user.name}</span>
                  <span className="vis-user-meta">
                    <span className="vis-user-id">#{user.id}</span>
                    {status ? (
                      <span className="vis-user-status" data-tone={status.tone}>
                        {status.label}
                      </span>
                    ) : null}
                  </span>
                </div>
              </div>
            ) : (
              <p className="vis-admin-card-hint vis-user-placeholder">
                选择用户后显示其身份与订阅状态。
              </p>
            )}
          </Surface>

          <Surface className="vis-admin-card vis-admin-scope">
            <h2 className="module-title">可见范围</h2>
            <dl className="vis-scope-readouts">
              <Readout label="可见项" value={catalog ? visibleCount : '—'} />
              <Readout label="已隐藏" value={catalog ? hiddenCount : '—'} />
              <Readout label="外部节点" value={catalog ? externalNodeCount : '—'} />
            </dl>
            <p className="vis-scope-note">隐藏项不会出现在该用户的订阅中。</p>
          </Surface>

          <Surface className="vis-admin-card vis-admin-publish">
            <h2 className="module-title">发布状态</h2>
            {pending.length > 0 || settled.length > 0 ? (
              <PublicationNotice pending={pending} settled={settled} pollFailed={pollFailed} />
            ) : (
              <p className="vis-publish-empty">暂无发布记录</p>
            )}
            <p className="vis-admin-card-hint">保存后会重新发布订阅，客户端下次刷新时生效。</p>
          </Surface>
        </div>

        <form className="surface-frame vis-admin-editor" onSubmit={save}>
          <header className="vis-admin-editor-head">
            <div className="vis-admin-editor-head-copy">
              <h2 className="module-title vis-admin-editor-title">订阅内容</h2>
              <p className="vis-admin-editor-hint">勾选要下发到该用户订阅的链路与外部订阅。</p>
            </div>
            {catalog && hasItems ? (
              <span className="vis-admin-editor-total">
                共 <strong>{items.length}</strong> 项
              </span>
            ) : null}
          </header>

          <div className="vis-admin-editor-body">
            {loadError ? (
              <Notice
                tone="danger"
                actions={
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setLoadError('')
                      setLoadAttempt((attempt) => attempt + 1)
                    }}
                  >
                    重试
                  </Button>
                }
              >
                {loadError}
              </Notice>
            ) : !user ? (
              <p className="vis-admin-editor-placeholder">先从左侧「订阅用户」选择要管理的用户。</p>
            ) : !catalog ? (
              <p className="vis-admin-editor-placeholder" role="status">
                正在读取订阅内容…
              </p>
            ) : (
              <VisibilityEditor
                links={catalog.links}
                externalSubscriptions={catalog.external_subscriptions}
                hiddenIds={hiddenIds}
                onVisibilityChange={changeVisibility}
              />
            )}
            {saveError ? <Notice tone="danger">{saveError}</Notice> : null}
          </div>

          {catalog && !loadError ? (
            <div className="vis-admin-actions">
              <p className="vis-admin-actions-copy" data-tone={footerTone}>
                {footerText}
              </p>
              <div className="vis-admin-actions-buttons">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!dirty || saving}
                  onClick={resetDraft}
                >
                  重置
                </Button>
                <Button type="submit" size="sm" disabled={!canSave}>
                  {saving ? '正在提交…' : '保存可见性'}
                </Button>
              </div>
            </div>
          ) : null}
        </form>
      </div>
    </Page>
  )
}

export default function UserSubscriptionVisibility() {
  const [, navigate] = useLocation()
  const search = useSearch()
  const userId = new URLSearchParams(search).get('user_id')
  const [users, setUsers] = useState<SubUser[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    api
      .users({ signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return
        setUsers(data.toSorted((a, b) => a.name.localeCompare(b.name, 'zh-CN') || a.id - b.id))
      })
      .catch((reason) => {
        if (!controller.signal.aborted) setError(errorMessage(reason))
      })
    return () => controller.abort()
  }, [])

  const selected = users?.find((user) => String(user.id) === userId)
  const options = (users ?? []).map((user) => ({
    value: String(user.id),
    label: `${user.name} (#${user.id})`,
  }))

  if (error) {
    return (
      <PageScaffold>
        <Notice tone="danger">{error}</Notice>
      </PageScaffold>
    )
  }
  if (users === null) {
    return (
      <PageScaffold>
        <LoadingState>正在读取用户列表…</LoadingState>
      </PageScaffold>
    )
  }
  if (users.length === 0) {
    return (
      <PageScaffold>
        <EmptyState title="暂无用户" description="创建订阅用户后即可在这里管理其可见范围。" />
      </PageScaffold>
    )
  }

  return (
    <VisibilityWorkspace
      key={selected ? selected.id : 'unselected'}
      user={selected}
      options={options}
      onSelectUser={(value) => {
        if (value) navigate(`/access/sub-visibility?user_id=${value}`, { replace: true })
      }}
    />
  )
}
