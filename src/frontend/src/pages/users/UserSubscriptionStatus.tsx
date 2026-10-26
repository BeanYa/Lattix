import type { SubscriptionSnapshotStatus } from '@/lib/types'

export function UserSubscriptionStatus({ snapshot }: { snapshot: SubscriptionSnapshotStatus }) {
  if (snapshot.status === 'ready') {
    return <span className="cg-status is-muted">订阅 r{snapshot.revision}</span>
  }
  if (snapshot.status === 'pending') {
    return <span className="cg-status is-blue">订阅生成中</span>
  }
  if (snapshot.status === 'missing') {
    return (
      <span className="cg-status is-warn" title="点击刷新订阅快照生成全部订阅格式">
        尚未生成
      </span>
    )
  }

  const reason = snapshot.error?.trim() || '订阅生成失败，服务端未返回具体原因。'
  return (
    <details className="cg-user-subscription-error">
      <summary>
        <span className="cg-status is-red">订阅异常</span>
        <span className="cg-hint">查看原因</span>
      </summary>
      <div className="cg-user-subscription-error-detail" role="status">
        <p>{reason}</p>
        <p className="cg-muted">
          修正相关设置后，点击“刷新订阅快照”重试；如仍失败，可查看面板日志。
        </p>
      </div>
    </details>
  )
}
