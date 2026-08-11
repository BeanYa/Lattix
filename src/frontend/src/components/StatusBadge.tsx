import type { ComponentProps } from 'react'

import { Badge } from '@/components/ui/badge'
import type { FeedbackTone } from '@/components/PagePrimitives'
import { cn } from '@/lib/utils'

const toneClasses: Record<FeedbackTone, string> = {
  neutral: 'border-border bg-secondary text-secondary-foreground',
  info: 'border-info bg-info text-white',
  success: 'border-border bg-primary text-primary-foreground',
  warning: 'border-border bg-muted text-warning',
  danger: 'border-border bg-destructive text-[#29282D]',
}

export function StatusBadge({
  tone = 'neutral',
  className,
  ...props
}: ComponentProps<typeof Badge> & { tone?: FeedbackTone }) {
  return (
    <Badge
      variant="outline"
      data-tone={tone}
      className={cn(
        'status-badge h-auto px-2 py-1 before:size-1.5 before:shrink-0 before:rounded-full before:bg-current before:content-[""]',
        toneClasses[tone],
        className
      )}
      {...props}
    />
  )
}
