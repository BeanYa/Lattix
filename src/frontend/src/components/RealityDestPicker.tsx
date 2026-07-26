import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { CUSTOM_REALITY_DEST, REALITY_DEST_OPTIONS } from '@/lib/reality'

interface RealityDestPickerProps {
  idPrefix: string
  preset: string
  onPresetChange: (value: string) => void
  dest: string
  onDestChange: (value: string) => void
  serverNames: string
  onServerNamesChange: (value: string) => void
}

export function RealityDestPicker({
  idPrefix,
  preset,
  onPresetChange,
  dest,
  onDestChange,
  serverNames,
  onServerNamesChange,
}: RealityDestPickerProps) {
  const selectPreset = (value: string) => {
    onPresetChange(value)
    if (value !== CUSTOM_REALITY_DEST) {
      onDestChange(`${value}:443`)
      onServerNamesChange(value)
    }
  }

  return (
    <>
      <div className="space-y-2">
        <Label htmlFor={`${idPrefix}-dest-preset`}>Reality 伪装站点</Label>
        <select
          id={`${idPrefix}-dest-preset`}
          className="h-8 w-full rounded-lg border border-input bg-transparent px-2.5 py-1 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
          value={preset}
          onChange={(event) => selectPreset(event.target.value)}
        >
          {REALITY_DEST_OPTIONS.map((option) => (
            <option key={option.domain} value={option.domain}>
              {option.label}（{option.domain}）
            </option>
          ))}
          <option value={CUSTOM_REALITY_DEST}>自定义…</option>
        </select>
        <p className="text-xs text-muted-foreground">
          内置项会同时设置 dest 与 SNI；具体可用性取决于服务器所在网络。
        </p>
      </div>
      {preset === CUSTOM_REALITY_DEST ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={`${idPrefix}-dest`}>dest</Label>
            <Input
              id={`${idPrefix}-dest`}
              value={dest}
              onChange={(event) => onDestChange(event.target.value)}
              placeholder="example.com:443"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${idPrefix}-server-names`}>SNI / serverNames（逗号分隔）</Label>
            <Input
              id={`${idPrefix}-server-names`}
              value={serverNames}
              onChange={(event) => onServerNamesChange(event.target.value)}
              placeholder="example.com"
            />
          </div>
        </>
      ) : (
        <div className="rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          dest：{dest} · SNI：{serverNames}
        </div>
      )}
    </>
  )
}
