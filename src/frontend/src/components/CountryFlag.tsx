import { cn } from '@/lib/utils'

export function CountryFlag({
  code,
  label,
  className,
}: {
  code: string
  label?: string
  className?: string
}) {
  const normalized = code.trim().toLowerCase()
  if (!/^[a-z]{2}$/.test(normalized)) return null

  return (
    <span
      className={cn('fi shrink-0 select-none', `fi-${normalized}`, className)}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      // 细内描边让旗帜在奶油卡片上边缘更清晰；用 cg 墨水色 + color-mix 实现，深浅模式自适应
      style={{ boxShadow: 'inset 0 0 0 1px color-mix(in srgb, var(--cg-ink) 18%, transparent)' }}
    />
  )
}
