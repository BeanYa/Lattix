import { useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export function CopyButton({
  text,
  className,
  size = 'sm',
}: {
  text: string
  className?: string
  size?: 'default' | 'sm'
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // 剪贴板不可用（非安全上下文等），忽略
    }
  }

  return (
    <Button type="button" variant="outline" size={size} className={cn('bg-card', className)} onClick={copy}>
      {copied ? <CheckIcon /> : <CopyIcon />}
      {copied ? '已复制' : '复制'}
    </Button>
  )
}
