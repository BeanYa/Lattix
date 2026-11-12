import { useEffect, useRef, useState } from 'react'

import { requester } from '@/lib/requester'

import type { PublicationJob } from './publication-jobs'
import {
  VisibilityEditor,
  type VisibilityExternalSubscription,
  type VisibilityLink,
} from './VisibilityEditor'

interface VisibilityConfig {
  links: VisibilityLink[]
  external_subscriptions: VisibilityExternalSubscription[]
  hidden_ids: string[]
}

function sameHiddenIds(left: ReadonlySet<string>, right: ReadonlySet<string>) {
  return left.size === right.size && [...left].every((id) => right.has(id))
}

export function VisibilityCard({
  token,
  settledJobs,
  onSubmitted,
}: {
  token: string
  settledJobs: PublicationJob[]
  onSubmitted: (job: PublicationJob) => void
}) {
  const path = `/api/sub/${token}/visibility`
  const [config, setConfig] = useState<VisibilityConfig | null>(null)
  const [draft, setDraft] = useState<Map<string, boolean> | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const latestSubmission = useRef<{ id: string; draft: typeof draft } | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    const submission = latestSubmission.current
    requester
      .getJSON<VisibilityConfig>(path, { signal: controller.signal, display: 'silent' })
      .then((next) => {
        if (controller.signal.aborted) return
        const current = latestSubmission.current === submission
        // A newer save can be accepted while this earlier refresh is in flight.
        setConfig((previous) =>
          current || !previous ? next : { ...next, hidden_ids: previous.hidden_ids },
        )
        setLoadFailed(false)
        if (
          current &&
          submission &&
          settledJobs.some((job) => job.id === submission.id && job.status === 'completed')
        ) {
          setDraft((previous) => (previous === submission.draft ? null : previous))
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoadFailed(true)
      })
    return () => controller.abort()
  }, [loadAttempt, path, settledJobs])

  const baseline = new Set(config?.hidden_ids ?? [])
  const hiddenIds = new Set(baseline)
  for (const item of [...(config?.links ?? []), ...(config?.external_subscriptions ?? [])]) {
    const visible = draft?.get(item.id)
    if (visible === true) hiddenIds.delete(item.id)
    if (visible === false) hiddenIds.add(item.id)
  }
  const dirty = !sameHiddenIds(hiddenIds, baseline)
  const save = async () => {
    if (submitting || !dirty) return
    setSubmitting(true)
    setError('')
    const hidden_ids = [...hiddenIds]
    try {
      const job = await requester.postJSON<PublicationJob>(
        path,
        { hidden_ids },
        { display: 'silent' },
      )
      latestSubmission.current = { id: job.id, draft }
      setConfig((previous) => (previous ? { ...previous, hidden_ids } : previous))
      onSubmitted(job)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法提交链路可见性，请重试。')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="sub-mod-body sub-cfg-body">
      {loadFailed ? (
        <div className="sub-cfg-actions">
          <p className="sub-cfg-error" role="alert">
            无法读取链路可见性。
          </p>
          <button
            className="cg-button"
            type="button"
            onClick={() => setLoadAttempt((attempt) => attempt + 1)}
          >
            重试
          </button>
        </div>
      ) : !config ? (
        <p className="sub-cfg-hint" role="status">
          正在读取链路可见性…
        </p>
      ) : null}

      {config ? (
        <>
          <p className="sub-cfg-hint">勾选要保留在订阅中的链路和外部订阅。</p>
          <VisibilityEditor
            links={config.links}
            externalSubscriptions={config.external_subscriptions}
            hiddenIds={hiddenIds}
            onVisibilityChange={(id, visible) => {
              setDraft((previous) => new Map(previous).set(id, visible))
            }}
          />
          {error ? (
            <p className="sub-cfg-error" role="alert">
              {error}
            </p>
          ) : null}
          <div className="sub-cfg-actions">
            <button
              className="cg-button is-primary sub-visibility-save"
              type="button"
              onClick={save}
              disabled={!dirty || submitting}
            >
              {submitting ? '提交中…' : '保存'}
            </button>
          </div>
        </>
      ) : null}
    </div>
  )
}
