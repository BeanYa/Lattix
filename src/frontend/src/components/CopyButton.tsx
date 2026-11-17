import { useState, type ReactNode } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// IconCopyButton 是图标复制按钮：与 CopyButton 相同的复制/“已复制”反馈，
// 只占一个图标位（服务器外部直链卡片 / 用户分享链接等紧凑场景）。
export function IconCopyButton({
  text,
  label,
  icon,
  disabled = false,
  disabledTitle,
}: {
  text: string
  label: string
  icon?: ReactNode
  disabled?: boolean
  disabledTitle?: string
}) {
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
      title={disabled ? (disabledTitle ?? label) : copied ? '已复制' : label}
      aria-label={label}
      disabled={disabled}
      onClick={copy}
    >
      {copied ? <CheckIcon /> : (icon ?? <CopyIcon />)}
    </Button>
  )
}

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
    <Button
      type="button"
      variant="outline"
      size={size}
      className={cn('bg-card', className)}
      onClick={copy}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
      {copied ? '已复制' : '复制'}
    </Button>
  )
}
