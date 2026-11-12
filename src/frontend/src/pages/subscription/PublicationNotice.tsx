import { Notice } from '@/components/PagePrimitives'

import type { PublicationJob } from './publication-jobs'

const jobLabels: Record<PublicationJob['kind'], string> = {
  visibility: '链路可见性',
  template: '模板配置',
  template_reset: '恢复默认模板',
}

export function PublicationNotice({
  pending,
  settled,
  pollFailed,
}: {
  pending: PublicationJob[]
  settled: PublicationJob[]
  pollFailed: boolean
}) {
  return (
    <div className="sub-publication" role="status" aria-atomic="false" aria-label="订阅发布进度">
      {[...settled.slice(-3), ...pending].map((job) => {
        const tone =
          job.status === 'failed' ? 'danger' : job.status === 'completed' ? 'success' : 'info'
        let message: string
        switch (job.status) {
          case 'completed':
            message = '订阅已发布，客户端下次刷新时生效。'
            break
          case 'failed':
            message = job.error ? `订阅发布失败。${job.error}` : '订阅发布失败，请重新提交。'
            break
          case 'running':
            message = '正在发布订阅…'
            break
          case 'queued':
            message = job.queued_behind
              ? '上一个请求正在发布，当前请求已排队。'
              : '请求已排队，等待发布…'
            break
        }
        return (
          <Notice key={job.id} tone={tone} title={jobLabels[job.kind]} role="group">
            {message}
          </Notice>
        )
      })}
      {pollFailed && pending.length > 0 ? (
        <Notice tone="warning" role="group">
          暂时无法读取发布进度，正在重试。
        </Notice>
      ) : null}
    </div>
  )
}
