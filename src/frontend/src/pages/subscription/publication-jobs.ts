import { useCallback, useEffect, useState } from 'react'

export interface PublicationJob {
  id: string
  kind: 'visibility' | 'template' | 'template_reset'
  status: 'queued' | 'running' | 'completed' | 'failed'
  queued_behind: boolean
  error?: string
}

interface PublicationJobs {
  pending: PublicationJob[]
  settled: PublicationJob[]
}

function isPending(job: PublicationJob) {
  return job.status === 'queued' || job.status === 'running'
}

function updateJobs(current: PublicationJobs, incoming: PublicationJob[], submitted = false) {
  const pending = new Map(current.pending.map((job) => [job.id, job]))
  const settledIds = new Set(current.settled.map((job) => job.id))
  let settled = current.settled
  for (const job of incoming) {
    if (settledIds.has(job.id)) continue
    const previous = pending.get(job.id)
    if (isPending(job)) {
      // An overlapping status read may arrive after the submission response.
      if (submitted && previous?.status === 'running' && job.status === 'queued') continue
      pending.set(job.id, job)
    } else if (previous || submitted) {
      pending.delete(job.id)
      settled = [...settled, job]
      settledIds.add(job.id)
    }
  }
  return { pending: [...pending.values()], settled }
}

export function usePublicationJobs(
  loadJobs: (signal: AbortSignal) => Promise<{ jobs: PublicationJob[] }>,
  enabled: boolean,
) {
  const [jobs, setJobs] = useState<PublicationJobs>({ pending: [], settled: [] })
  const [pollFailed, setPollFailed] = useState(false)
  const hasPending = jobs.pending.length > 0
  const addJob = useCallback((job: PublicationJob) => {
    setJobs((current) => updateJobs(current, [job], true))
  }, [])

  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    let timer: number

    const poll = async () => {
      try {
        const response = await loadJobs(controller.signal)
        if (controller.signal.aborted) return
        setJobs((current) => updateJobs(current, [...response.jobs].reverse()))
        setPollFailed(false)
        if (hasPending || response.jobs.some(isPending)) timer = window.setTimeout(poll, 700)
      } catch {
        if (controller.signal.aborted || !hasPending) return
        setPollFailed(true)
        timer = window.setTimeout(poll, 1500)
      }
    }

    timer = window.setTimeout(poll, hasPending ? 700 : 0)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [enabled, hasPending, loadJobs])

  return { ...jobs, addJob, pollFailed }
}
