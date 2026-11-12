import { useCallback, useEffect, useState, type FormEvent } from 'react'
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

interface UserVisibility {
  links: VisibilityLink[]
  external_subscriptions: VisibilityExternalSubscription[]
  hidden_ids: string[]
}

function UserVisibilityForm({ userId }: { userId: number }) {
  const [catalog, setCatalog] = useState<UserVisibility | null>(null)
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(new Set())
  const [savedIds, setSavedIds] = useState<Set<string>>(new Set())
  const [loadError, setLoadError] = useState('')
  const [saveError, setSaveError] = useState('')
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  const [lastJobId, setLastJobId] = useState('')

  useEffect(() => {
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
      requester.get<{ jobs: PublicationJob[] }>(
        '/api/user/sub-publication-jobs',
        { user_id: userId },
        { signal, display: 'silent' },
      ),
    [userId],
  )
  const { pending, settled, pollFailed, addJob } = usePublicationJobs(loadJobs, catalog !== null)
  const lastJobFailed = settled.some((job) => job.id === lastJobId && job.status === 'failed')
  const dirty = hiddenIds.size !== savedIds.size || [...hiddenIds].some((id) => !savedIds.has(id))
  const canSave = !saving && (dirty || lastJobFailed)

  function changeVisibility(id: string, visible: boolean) {
    setHiddenIds((current) => {
      const next = new Set(current)
      if (visible) next.delete(id)
      else next.add(id)
      return next
    })
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

  if (loadError) {
    return (
      <Notice
        tone="danger"
        actions={
          <Button
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
    )
  }
  if (!catalog) return <LoadingState>正在读取订阅内容…</LoadingState>

  return (
    <form className="grid gap-4 border-t border-border pt-4" onSubmit={save}>
      <VisibilityEditor
        links={catalog.links}
        externalSubscriptions={catalog.external_subscriptions}
        hiddenIds={hiddenIds}
        onVisibilityChange={changeVisibility}
      />
      {saveError && <Notice tone="danger">{saveError}</Notice>}
      {(pending.length > 0 || settled.length > 0) && (
        <PublicationNotice pending={pending} settled={settled} pollFailed={pollFailed} />
      )}
      {(catalog.links.length > 0 || catalog.external_subscriptions.length > 0) && (
        <div className="flex justify-end">
          <Button type="submit" className="sub-visibility-save" disabled={!canSave}>
            {saving ? '正在提交…' : '保存可见性'}
          </Button>
        </div>
      )}
    </form>
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

  return (
    <Page className="cg-page-in">
      <PageHeader title="可见性管理" description="为指定用户选择订阅中显示的链路和外部订阅。" />
      {error ? (
        <Notice tone="danger">{error}</Notice>
      ) : users === null ? (
        <LoadingState>正在读取用户列表…</LoadingState>
      ) : users.length === 0 ? (
        <EmptyState title="暂无用户" />
      ) : (
        <Surface className="grid max-w-3xl gap-4 p-4 sm:p-5">
          <div className="grid max-w-sm gap-2">
            <Label htmlFor="visibility-user">订阅用户</Label>
            <Select
              value={selected ? String(selected.id) : null}
              items={options}
              onValueChange={(value) => {
                if (value) navigate(`/access/sub-visibility?user_id=${value}`, { replace: true })
              }}
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
          {selected && <UserVisibilityForm key={selected.id} userId={selected.id} />}
        </Surface>
      )}
    </Page>
  )
}
