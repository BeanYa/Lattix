import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TRAFFIC_UNITS, type TrafficUnit } from '@/lib/user-subscription'

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
