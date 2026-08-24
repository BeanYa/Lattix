import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TRAFFIC_UNITS, type TrafficUnit } from '@/lib/user-subscription'
import type { ExternalSubscriptionMode } from '@/lib/types'

import { EXTERNAL_MODE_LABELS } from './external-mode'

export function ExternalModeSelect({
  value,
  onChange,
  disabled,
}: {
  value: ExternalSubscriptionMode
  onChange: (mode: ExternalSubscriptionMode) => void
  disabled?: boolean
}) {
  return (
    <Select
      value={value}
      onValueChange={(next) => next && onChange(next as ExternalSubscriptionMode)}
      disabled={disabled}
    >
      <SelectTrigger className="w-24" aria-label="引入模式">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => (
          <SelectItem key={mode} value={mode}>
            {EXTERNAL_MODE_LABELS[mode]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function TrafficLimitInput({
  value,
  unit,
  onValueChange,
  onUnitChange,
  placeholder = '流量配额',
}: {
  value: string
  unit: TrafficUnit
  onValueChange: (value: string) => void
  onUnitChange: (unit: TrafficUnit) => void
  placeholder?: string
}) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_4.5rem] gap-2">
      <Input
        type="number"
        min={0}
        step="any"
        value={value}
        onChange={(event) => onValueChange(event.target.value)}
        placeholder={placeholder}
      />
      <Select
        value={unit}
        onValueChange={(next) => next && onUnitChange(next as TrafficUnit)}
        items={TRAFFIC_UNITS}
      >
        <SelectTrigger className="w-full" aria-label="流量配额单位">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {TRAFFIC_UNITS.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
