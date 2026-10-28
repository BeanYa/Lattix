import { useState } from 'react'
import { GlobeIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { connectivitySiteLogo, connectivitySiteSlug } from '@/lib/server-test-logos'
import { number, text } from '@/lib/records'
import type { ServerTestCategoryResult } from '@/lib/types'
import { cn } from '@/lib/utils'

/** 分组显示顺序：中国 → 日本 → 美国 → 全球，其余组按字母序追加。 */
const groupOrder = ['中国', '日本', '美国', '全球']

const groupFlagCodes: Record<string, string> = { 中国: 'cn', 日本: 'jp', 美国: 'us' }

/** latencyTone 延迟评级，阈值与 ip.net.coffee 参考实现一致：<100 优，<400 良，其余慢。 */
function latencyTone(latency: number | null): 'good' | 'fair' | 'slow' | 'timeout' {
  if (latency === null) return 'timeout'
  if (latency < 100) return 'good'
  if (latency < 400) return 'fair'
  return 'slow'
}

const toneTextClass: Record<string, string> = {
  good: 'text-success',
  fair: 'text-success/75',
  slow: 'text-warning',
  timeout: 'text-destructive',
}

const toneDotClass: Record<string, string> = {
  good: 'bg-success',
  fair: 'bg-success/50',
  slow: 'bg-warning',
  timeout: 'bg-destructive/50',
}

/** BrandMark 品牌图标：浅色底片保证单色 logo 在深浅主题下都可见，加载失败回退首字母。 */
export function BrandMark({ src, label }: { src?: string; label: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <span className="flex size-6 shrink-0 items-center justify-center border bg-muted/40">
      {src && !failed ? (
        <img
          src={src}
          alt=""
          loading="lazy"
          className="size-4 object-contain"
          onError={() => setFailed(true)}
        />
      ) : (
        <span className="text-[10px] font-semibold text-muted-foreground">
          {label.slice(0, 1).toUpperCase()}
        </span>
      )}
    </span>
  )
}

export function ConnectivityReport({ category }: { category: ServerTestCategoryResult }) {
  const items = (category.items ?? []).map((item) => ({ item, id: text(item.id) }))
  const groups = new Map<string, typeof items>()
  for (const entry of items) {
    const name = text(entry.item.group, '其他')
    const bucket = groups.get(name)
    if (bucket) bucket.push(entry)
    else groups.set(name, [entry])
  }
  const ordered = Array.from(groups.entries()).sort(([left], [right]) => {
    const leftIndex = groupOrder.indexOf(left)
    const rightIndex = groupOrder.indexOf(right)
    if (leftIndex !== -1 || rightIndex !== -1) {
      if (leftIndex === -1) return 1
      if (rightIndex === -1) return -1
      return leftIndex - rightIndex
    }
    return left.localeCompare(right)
  })
  return (
    <div className="space-y-6">
      {ordered.map(([name, entries]) => (
        <ConnectivityGroup key={name} name={name} entries={entries} />
      ))}
    </div>
  )
}

function ConnectivityGroup({
  name,
  entries,
}: {
  name: string
  entries: Array<{ item: Record<string, unknown>; id: string }>
}) {
  const reached = entries.filter((entry) => number(entry.item.rtt_median_ms) !== null)
  const average = reached.length
    ? Math.round(
        reached.reduce((sum, entry) => sum + (number(entry.item.rtt_median_ms) ?? 0), 0) /
          reached.length,
      )
    : null
  const flagCode = groupFlagCodes[name]
  return (
    <section>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        {flagCode ? (
          <CountryFlag code={flagCode} label={`${name}旗帜`} className="h-3 w-4" />
        ) : (
          <GlobeIcon className="size-4 text-info" />
        )}
        <h4 className="text-sm font-semibold">{name}</h4>
        <span className="text-xs text-muted-foreground">
          {reached.length > 0 ? (
            <>
              可达 <span className="font-medium text-ink">{reached.length}</span>
              <span>/{entries.length}</span>
              {average !== null ? (
                <>
                  {' · '}平均{' '}
                  <span className={cn('font-medium', toneTextClass[latencyTone(average)])}>
                    {Math.min(average, 999)}ms
                  </span>
                </>
              ) : null}
            </>
          ) : (
            `可达 0/${entries.length}`
          )}
        </span>
      </div>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {entries.map((entry) => (
          <ConnectivitySite key={entry.id} item={entry.item} />
        ))}
      </div>
    </section>
  )
}

function ConnectivitySite({ item }: { item: Record<string, unknown> }) {
  const slug = connectivitySiteSlug(text(item.id))
  const label = text(item.label)
  const median = number(item.rtt_median_ms)
  const tone = latencyTone(median)
  const sent = number(item.sent) ?? 0
  const samples = Array.isArray(item.samples)
    ? item.samples.filter((value): value is number => typeof value === 'number')
    : []
  return (
    <div
      className={cn(
        'flex items-center gap-2.5 border px-2.5 py-2',
        tone === 'timeout' && 'opacity-60',
      )}
    >
      <BrandMark src={connectivitySiteLogo(slug)} label={label} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <span className="truncate text-xs font-medium" title={label}>
            {label}
          </span>
          <span className={cn('shrink-0 text-xs font-medium tabular-nums', toneTextClass[tone])}>
            {median === null ? '超时' : `${Math.min(Math.round(median), 999)}ms`}
          </span>
        </div>
        {sent > 0 ? (
          <div className="mt-1.5 flex gap-0.5" aria-hidden>
            {Array.from({ length: sent }, (_, round) => {
              const sample = samples[round]
              const roundTone = sample === undefined ? 'timeout' : latencyTone(sample)
              return (
                <span
                  key={round}
                  className={cn('h-1.5 w-3 rounded-full', toneDotClass[roundTone])}
                />
              )
            })}
          </div>
        ) : null}
      </div>
    </div>
  )
}
