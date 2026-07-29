import type { ComponentProps, ReactNode } from 'react'

import { cn } from '@/lib/utils'

export type FeedbackTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

export function Page({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('page-shell', className)} {...props} />
}

export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  className?: string
}) {
  return (
    <header className={cn('page-header', className)}>
      <div className="page-header-copy">
        <h1 className="page-title">{title}</h1>
        {description ? <p className="page-description">{description}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  )
}

export function Notice({
  tone = 'neutral',
  title,
  actions,
  className,
  children,
  ...props
}: ComponentProps<'div'> & {
  tone?: FeedbackTone
  title?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div
      role={tone === 'danger' ? 'alert' : 'status'}
      data-tone={tone}
      className={cn('notice', className)}
      {...props}
    >
      <div className="notice-content">
        {title ? <strong className="notice-title">{title}</strong> : null}
        <div className="notice-message">{children}</div>
      </div>
      {actions ? <div className="notice-actions">{actions}</div> : null}
    </div>
  )
}

export function EmptyState({
  icon,
  title,
  description,
  className,
  children,
  ...props
}: ComponentProps<'div'> & {
  icon?: ReactNode
  title?: ReactNode
  description?: ReactNode
}) {
  return (
    <div className={cn('empty-state', className)} {...props}>
      {icon ? <div className="empty-state-icon">{icon}</div> : null}
      {title ? <strong className="empty-state-title">{title}</strong> : null}
      {description ? <p className="empty-state-description">{description}</p> : null}
      {children}
    </div>
  )
}

export function Surface({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('surface-frame', className)} {...props} />
}

export function LoadingState({ children = '加载中…', className, ...props }: ComponentProps<'div'>) {
  return (
    <div role="status" className={cn('loading-state', className)} {...props}>
      {children}
    </div>
  )
}
