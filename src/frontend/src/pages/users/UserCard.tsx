import { useState } from 'react'
import type { ReactNode } from 'react'
import {
  BanIcon,
  CheckIcon,
  CircleCheckIcon,
  CopyIcon,
  EllipsisIcon,
  LinkIcon,
  ExternalLinkIcon,
  EyeIcon,
  HistoryIcon,
  KeyRoundIcon,
  QrCodeIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  RotateCwIcon,
  Settings2Icon,
  Trash2Icon,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { humanizeBytes } from '@/lib/format'
import type { SubUser } from '@/lib/types'

import { UserSubscriptionStatus } from './UserSubscriptionStatus'

/** formatCardDate 只保留日期：卡片里的有效期不需要秒级时间，完整时间放在 title。 */
function formatCardDate(value: string, timezone?: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      ...(timezone ? { timeZone: timezone } : {}),
    }).format(date)
  } catch {
    return date.toLocaleDateString()
  }
}

/** IconCopyButton 是卡片内的图标复制按钮：保持与 CopyButton 相同的复制反馈，
 * 但只占一个图标位，避免卡片里出现两个并列的「复制」文字按钮。 */
function IconCopyButton({ text, label, icon }: { text: string; label: string; icon?: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // 剪贴板不可用（非安全上下文等），忽略。
    }
  }
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      title={copied ? '已复制' : label}
      aria-label={label}
      onClick={copy}
    >
      {copied ? <CheckIcon /> : (icon ?? <CopyIcon />)}
    </Button>
  )
}

export interface UserCardActions {
  onOpenContent: (user: SubUser) => void
  onRegenerate: (user: SubUser) => void
  onReactivate: (user: SubUser) => void
  onOpenSubSettings: (user: SubUser) => void
  onResetToken: (user: SubUser) => void
  onOpenPreview: (user: SubUser) => void
  onOpenHistory: (user: SubUser) => void
  onResetTraffic: (user: SubUser) => void
  onToggleDisabled: (user: SubUser) => void
  onDelete: (user: SubUser) => void
  onShowQr: (url: string) => void
  onOpenSubPage: (url: string) => void
}

export function UserCard({
  user,
  timezone,
  highlighted,
  regenerating,
  resettingToken,
  resettingTraffic,
  toggling,
  deleting,
  actions,
}: {
  user: SubUser
  timezone: string
  highlighted: boolean
  regenerating: boolean
  resettingToken: boolean
  resettingTraffic: boolean
  toggling: boolean
  deleting: boolean
  actions: UserCardActions
}) {
  const notYetEffective = !!user.effective_at && new Date(user.effective_at).getTime() > Date.now()
  const used = (user.traffic?.up ?? 0) + (user.traffic?.down ?? 0)
  const quotaRatio =
    user.traffic_limit > 0 ? Math.min(100, Math.round((used / user.traffic_limit) * 100)) : 0
  const blocked = user.disabled || user.expired || user.quota_exhausted
  return (
    <article
      className="cg-user-card"
      data-blocked={blocked || undefined}
      data-disabled={user.disabled || undefined}
      data-highlighted={highlighted ? 'true' : undefined}
      data-tone={
        blocked
          ? 'danger'
          : notYetEffective || user.subscription_snapshot.status !== 'ready'
            ? 'warn'
            : undefined
      }
    >
      <header className="cg-user-card-head">
        <span
          className="cg-line-badge cg-user-avatar"
          style={{ ['--line-color' as string]: `var(--panel-line-${user.id % 9})` }}
          aria-hidden="true"
        >
          {user.name.slice(0, 1).toUpperCase()}
        </span>
        <div className="cg-user-card-ident">
          <h3 className="cg-user-name" title={user.name}>
            {user.name}
          </h3>
          <span className="cg-user-handle">
            #{user.id}
            {user.is_trial ? <span className="cg-status is-blue">试用</span> : null}
          </span>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`管理用户 ${user.name}`}
                title="用户操作"
              />
            }
          >
            <EllipsisIcon />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuGroup>
              <DropdownMenuLabel>用户管理</DropdownMenuLabel>
              {user.expired && (
                <DropdownMenuItem
                  title="重设流量与有效期"
                  onClick={() => actions.onReactivate(user)}
                >
                  <RotateCwIcon />
                  重新激活
                </DropdownMenuItem>
              )}
              <DropdownMenuItem onClick={() => actions.onOpenSubSettings(user)}>
                <Settings2Icon />
                订阅设置
              </DropdownMenuItem>
              <DropdownMenuItem
                disabled={resettingToken}
                title="更换链接，旧链接立即失效"
                onClick={() => actions.onResetToken(user)}
              >
                <KeyRoundIcon />
                {resettingToken ? '重置中…' : '重置订阅地址'}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => actions.onOpenPreview(user)}>
                <EyeIcon />
                结果预览
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => actions.onOpenHistory(user)}>
                <HistoryIcon />
                流量历史
              </DropdownMenuItem>
              <DropdownMenuItem
                disabled={resettingTraffic}
                title={user.quota_exhausted ? '清零用量并恢复访问' : '清零当期用量'}
                onClick={() => actions.onResetTraffic(user)}
              >
                <RotateCcwIcon />
                {resettingTraffic ? '重置中…' : '重置流量'}
              </DropdownMenuItem>
              <DropdownMenuItem
                disabled={toggling}
                title={user.disabled ? '恢复链路下发与订阅' : '立即停权，订阅链路清空'}
                onClick={() => actions.onToggleDisabled(user)}
              >
                {user.disabled ? <CircleCheckIcon /> : <BanIcon />}
                {toggling ? '处理中…' : user.disabled ? '启用' : '停用'}
              </DropdownMenuItem>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              disabled={deleting}
              onClick={() => actions.onDelete(user)}
            >
              <Trash2Icon />
              {deleting ? '删除中…' : '删除用户'}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </header>

      <div className="cg-user-card-states">
        {user.disabled && <span className="cg-status is-red">已停用</span>}
        {user.expired && <span className="cg-status is-red">已到期</span>}
        {user.quota_exhausted && <span className="cg-status is-warn">流量用尽</span>}
        {notYetEffective && !user.expired && <span className="cg-status is-warn">未生效</span>}
        <UserSubscriptionStatus snapshot={user.subscription_snapshot} />
      </div>

      <dl className="cg-user-metrics">
        <div className="cg-user-metric is-wide">
          <dt>流量</dt>
          <dd>
            {user.traffic ? (
              <>
                <span className="cg-user-traffic-pair">
                  <span>↑ {humanizeBytes(user.traffic.up)}</span>
                  <span>↓ {humanizeBytes(user.traffic.down)}</span>
                </span>
                {user.traffic_limit > 0 ? (
                  <span
                    className="cg-user-traffic-quota"
                    title="当期已用 / 配额（用尽即停权，周期重置或手动重置后恢复）"
                  >
                    {humanizeBytes(used)} / {humanizeBytes(user.traffic_limit)}
                  </span>
                ) : (
                  <span className="cg-user-traffic-quota">不限流量</span>
                )}
              </>
            ) : (
              <span className="cg-muted">—</span>
            )}
          </dd>
          {user.traffic && user.traffic_limit > 0 ? (
            <span className="cg-user-quota-bar" aria-hidden="true">
              <i
                className={quotaRatio >= 90 ? 'is-crit' : quotaRatio >= 75 ? 'is-warn' : undefined}
                style={{ width: `${quotaRatio}%` }}
              />
            </span>
          ) : null}
        </div>
        <div className="cg-user-metric">
          <dt>链路</dt>
          <dd>{user.effective_chain_ids.length}</dd>
        </div>
        <div className="cg-user-metric">
          <dt>在线</dt>
          <dd>
            {user.online_connections > 0 ? (
              <span className="cg-user-online">
                <i className="cg-beacon-live" aria-hidden="true" />
                {user.online_connections}
              </span>
            ) : (
              <span className="cg-muted">—</span>
            )}
          </dd>
        </div>
        <div className="cg-user-metric">
          <dt>有效期</dt>
          <dd
            className="cg-user-metric-date"
            title={
              (notYetEffective ? user.effective_at : user.expires_at) ??
              (user.expires_at ? undefined : '长期有效')
            }
          >
            {notYetEffective
              ? `未生效 · ${user.effective_at ? formatCardDate(user.effective_at, timezone) : ''} 起`
              : user.expires_at
                ? formatCardDate(user.expires_at, timezone)
                : '长期'}
          </dd>
        </div>
      </dl>

      <div className="cg-user-subline">
        <span className="cg-user-suburl-text" title={user.sub_url}>
          {user.sub_url}
        </span>
        <span className="cg-user-suburl-actions">
          <IconCopyButton text={user.sub_url} label="复制订阅地址" />
          <IconCopyButton
            text={user.sub_links_url}
            label="复制分享链接（vless:// 等）"
            icon={<LinkIcon />}
          />
          <Button
            variant="ghost"
            size="icon-sm"
            title="订阅二维码"
            aria-label="订阅二维码"
            onClick={() => actions.onShowQr(user.sub_url)}
          >
            <QrCodeIcon />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            title="打开订阅落地页（浏览器）"
            aria-label="打开订阅落地页"
            onClick={() => actions.onOpenSubPage(user.sub_url)}
          >
            <ExternalLinkIcon />
          </Button>
        </span>
      </div>

      <footer className="cg-user-card-foot">
        <Button variant="outline" size="sm" onClick={() => actions.onOpenContent(user)}>
          查看订阅内容
        </Button>
        <Button
          variant="outline"
          size="sm"
          title="根据当前可见性和模板设置重新生成全部订阅格式"
          aria-label={`刷新 ${user.name} 的订阅快照`}
          disabled={regenerating}
          onClick={() => actions.onRegenerate(user)}
        >
          <RefreshCwIcon />
          {regenerating ? '刷新中…' : '刷新快照'}
        </Button>
      </footer>
    </article>
  )
}
