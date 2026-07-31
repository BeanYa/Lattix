import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type {
  SubscriptionRoutingProfile,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
} from '@/lib/types'

const presetLabels = {
  minimal: 'Minimal',
  balanced: 'Balanced',
  comprehensive: 'Comprehensive',
} as const

function categoriesForPreset(
  preset: SubscriptionRoutingProfile['preset'],
  categories: SubscriptionRuleCategory[],
): string[] {
  if (preset === 'comprehensive') return categories.map((category) => category.id)
  return categories
    .filter((category) => preset === 'minimal' ? category.in_minimal : category.in_balanced)
    .map((category) => category.id)
}

export function SubscriptionRoutingFields({
  value,
  onChange,
  categories,
  templates,
}: {
  value: SubscriptionRoutingProfile
  onChange: (value: SubscriptionRoutingProfile) => void
  categories: SubscriptionRuleCategory[]
  templates: SubscriptionTemplate[]
}) {
  const portable = templates.filter((template) => ['portable', 'acl4ssr'].includes(template.kind) && template.content)
  const native = (kind: SubscriptionTemplate['kind']) => templates.filter((template) => template.kind === kind && template.content)
  const set = (patch: Partial<SubscriptionRoutingProfile>) => onChange({ ...value, ...patch })

  return (
    <div className="space-y-4 border-t pt-4">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>规则来源</Label>
          <Select value={value.mode} onValueChange={(mode) => mode && set({ mode: mode as SubscriptionRoutingProfile['mode'] })}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="suggested">建议规则</SelectItem>
              <SelectItem value="template">自定义模板</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {value.mode === 'suggested' ? (
          <div className="space-y-2">
            <Label>规则预设</Label>
            <Select
              value={value.preset}
              onValueChange={(preset) => {
                if (!preset) return
                const next = preset as SubscriptionRoutingProfile['preset']
                set({ preset: next, categories: categoriesForPreset(next, categories) })
              }}
            >
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                {Object.entries(presetLabels).map(([id, label]) => <SelectItem key={id} value={id}>{label}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
        ) : (
          <div className="space-y-2">
            <Label>中立 / ACL4SSR 模板</Label>
            <Select value={value.portable_template_id} onValueChange={(id) => id && set({ portable_template_id: id })}>
              <SelectTrigger className="w-full"><SelectValue placeholder="选择模板" /></SelectTrigger>
              <SelectContent>
                {portable.map((template) => <SelectItem key={template.id} value={template.id}>{template.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
        )}
      </div>

      {value.mode === 'suggested' ? (
        <div className="space-y-2">
          <Label>生效分类</Label>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {categories.map((category) => (
              <label key={category.id} className="flex min-w-0 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 shrink-0 accent-primary"
                  checked={value.categories.includes(category.id)}
                  onChange={(event) => set({
                    categories: event.target.checked
                      ? [...value.categories, category.id]
                      : value.categories.filter((id) => id !== category.id),
                  })}
                />
                <span aria-hidden="true">{category.icon}</span>
                <span className="min-w-0 break-words">{category.label}</span>
              </label>
            ))}
          </div>
        </div>
      ) : null}

      <details className="rounded-md border px-3 py-2">
        <summary className="cursor-pointer text-sm font-medium">客户端原生模板覆盖</summary>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          {([
            ['mihomo_template_id', 'Mihomo', 'mihomo'],
            ['singbox_template_id', 'Sing-box', 'singbox'],
            ['quanx_template_id', 'Quantumult X', 'quanx'],
          ] as const).map(([field, label, kind]) => (
            <div key={field} className="space-y-2">
              <Label>{label}</Label>
              <Select value={value[field] || 'none'} onValueChange={(id) => id && set({ [field]: id === 'none' ? '' : id })}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">跟随主策略</SelectItem>
                  {native(kind).map((template) => <SelectItem key={template.id} value={template.id}>{template.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
          ))}
        </div>
      </details>
    </div>
  )
}
