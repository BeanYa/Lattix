import { Combobox } from '@base-ui/react/combobox'
import { CheckIcon, ChevronDownIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import type { CountryOption } from '@/lib/geography'
import { cn } from '@/lib/utils'

export function CountryCombobox({
  id,
  value,
  options,
  onValueChange,
}: {
  id: string
  value: string
  options: CountryOption[]
  onValueChange: (value: string) => void
}) {
  const selected = options.find((country) => country.code === value) ?? null

  return (
    <Combobox.Root
      items={options}
      value={selected}
      onValueChange={(country) => {
        if (country) onValueChange(country.code)
      }}
      itemToStringLabel={(country) => country.label}
      itemToStringValue={(country) => country.code}
      isItemEqualToValue={(country, current) => country.code === current.code}
      filter={(country, query) => {
        const normalizedQuery = query.trim().toLocaleLowerCase()
        if (!normalizedQuery) return true
        return `${country.code} ${country.name} ${country.label}`
          .toLocaleLowerCase()
          .includes(normalizedQuery)
      }}
      autoHighlight
    >
      <Combobox.InputGroup className="sv-combobox relative flex h-9 w-full items-center text-sm">
        {selected ? (
          <CountryFlag
            code={selected.code}
            label={`${selected.label}国旗`}
            className="pointer-events-none absolute left-2.5"
          />
        ) : null}
        <Combobox.Input
          id={id}
          required
          autoComplete="off"
          placeholder="输入国家、英文名或代码"
          className={cn(
            'h-full min-w-0 flex-1 bg-transparent pr-8 pl-2.5 outline-none placeholder:text-muted-foreground',
            selected && 'pl-8',
          )}
        />
        <Combobox.Trigger
          aria-label="打开国家列表"
          className="absolute right-0 flex h-full w-8 items-center justify-center text-muted-foreground outline-none"
        >
          <ChevronDownIcon />
        </Combobox.Trigger>
      </Combobox.InputGroup>

      <Combobox.Portal>
        <Combobox.Positioner sideOffset={4} className="isolate z-50 outline-none">
          <Combobox.Popup className="sv-combobox-popup relative isolate max-h-(--available-height) w-(--anchor-width) min-w-64 origin-(--transform-origin) overflow-hidden duration-100 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95">
            <Combobox.Empty className="px-3 py-4 text-center text-sm text-muted-foreground">
              未找到匹配的国家
            </Combobox.Empty>
            <Combobox.List className="max-h-[min(22.5rem,var(--available-height))] overflow-y-auto overscroll-contain p-1 outline-none data-empty:p-0">
              {(country: CountryOption) => (
                <Combobox.Item
                  key={country.code}
                  value={country}
                  className="sv-combobox-item relative flex cursor-default items-center gap-2 rounded-md py-1.5 pr-8 pl-2 text-sm outline-none select-none"
                >
                  <CountryFlag code={country.code} label={`${country.label}国旗`} />
                  <span>{country.label}</span>
                  <span className="text-xs text-muted-foreground">{country.name}</span>
                  <span className="ml-auto font-mono text-xs text-muted-foreground">{country.code}</span>
                  <Combobox.ItemIndicator className="absolute right-2 flex size-4 items-center justify-center">
                    <CheckIcon />
                  </Combobox.ItemIndicator>
                </Combobox.Item>
              )}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  )
}
