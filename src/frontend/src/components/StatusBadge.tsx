import type { ComponentProps } from 'react'

import { Badge } from '@/components/ui/badge'
import type { FeedbackTone } from '@/components/PagePrimitives'
import { cn } from '@/lib/utils'

export function StatusBadge({
  tone = 'neutral',
  className,
  ...props
}: ComponentProps<typeof Badge> & { tone?: FeedbackTone }) {
  return (
    <Badge
      variant="outline"
      data-tone={tone}
      className={cn('status-badge', className)}
      {...props}
    />
  )
}
