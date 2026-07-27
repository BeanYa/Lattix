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
      className={cn('fi shrink-0', `fi-${normalized}`, className)}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    />
  )
}
