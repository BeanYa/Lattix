import type { PublicationJob } from './publication-jobs'
import './visibility.css'

const jobLabels: Record<PublicationJob['kind'], string> = {
  visibility: '链路可见性',
  template: '模板配置',
  template_reset: '恢复默认模板',
}

type RowTone = 'info' | 'success' | 'warning' | 'danger'

function StatusRow({
  tone,
  title,
  text,
  meta,
}: {
  tone: RowTone
  title: string
  text: string
  meta?: string
}) {
  return (
    <div className="pub-row" data-tone={tone} role="group">
      <span className="pub-dot" aria-hidden="true" />
      <strong className="pub-title">{title}</strong>
      <span className="pub-text">{text}</span>
      {meta ? <span className="pub-meta">{meta}</span> : null}
    </div>
  )
}

/**
 * PublicationNotice 把同一时刻的发布任务合并为一条状态行：
 * 多个排队任务不再逐条重复通知，完成后只保留最新一条结果。
 */
export function PublicationNotice({
  pending,
  settled,
  pollFailed,
}: {
  pending: PublicationJob[]
  settled: PublicationJob[]
  pollFailed: boolean
}) {
  const current = pending.find((job) => job.status === 'running') ?? pending[0]
  const failed = settled.filter((job) => job.status === 'failed').slice(-2)
  const completed = settled.filter((job) => job.status === 'completed').slice(-1)

  return (
    <div
      className="sub-publication pub-feed"
      role="status"
      aria-atomic="false"
      aria-label="订阅发布进度"
    >
      {current ? (
        <StatusRow
          tone="info"
          title={jobLabels[current.kind]}
          text={
            current.status === 'running'
              ? '正在发布订阅…'
              : current.queued_behind
                ? '上一个请求正在发布，当前请求已排队。'
                : '请求已排队，等待发布…'
          }
          meta={pending.length > 1 ? `共 ${pending.length} 个请求排队` : undefined}
        />
      ) : null}
      {failed.map((job) => (
        <StatusRow
          key={job.id}
          tone="danger"
          title={jobLabels[job.kind]}
          text={job.error ? `订阅发布失败。${job.error}` : '订阅发布失败，请重新提交。'}
        />
      ))}
      {!current && failed.length === 0 && completed.length > 0 ? (
        <StatusRow
          tone="success"
          title={jobLabels[completed[0].kind]}
          text="订阅已发布，客户端下次刷新时生效。"
        />
      ) : null}
      {pollFailed && pending.length > 0 ? (
        <StatusRow tone="warning" title="发布进度" text="暂时无法读取进度，正在重试。" />
      ) : null}
    </div>
  )
}
