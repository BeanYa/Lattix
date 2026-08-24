import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Notice } from '@/components/PagePrimitives'
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

function suggestedCategoryLabels(ids: string[], categories: SubscriptionRuleCategory[]): string {
  const byId = new Map(categories.map((category) => [category.id, category.label]))
  const labels = ids.map((id) => byId.get(id) ?? id)
  return labels.length === 0 ? '未指定分组' : labels.join('、')
}

function categoriesForPreset(
  preset: SubscriptionRoutingProfile['preset'],
  categories: SubscriptionRuleCategory[],
): string[] {
  if (preset === 'comprehensive') return categories.map((category) => category.id)
  return categories
    .filter((category) => (preset === 'minimal' ? category.in_minimal : category.in_balanced))
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
  const portable = templates.filter(
    (template) => ['portable', 'acl4ssr'].includes(template.kind) && template.content,
  )
  const native = (kind: SubscriptionTemplate['kind']) =>
    templates.filter((template) => template.kind === kind && template.content)
  const set = (patch: Partial<SubscriptionRoutingProfile>) => onChange({ ...value, ...patch })
  const templateName = (id: string) => templates.find((template) => template.id === id)?.name ?? id

  return (
    <div className="space-y-4 border-t pt-4">
      {value.assigned_portable_template_id && (
        <Notice tone="info">
          {value.assign_forced_portable
            ? `已强制指派模板「${templateName(value.assigned_portable_template_id)}」，以下自选设置暂不生效。`
            : `已指派模板「${templateName(value.assigned_portable_template_id)}」（自选优先，可在此覆盖）。`}
        </Notice>
      )}
      {value.assigned_suggested_categories?.length > 0 && (
        <Notice tone="info">
          {value.assign_forced_portable
            ? `已强制指派建议规则（${suggestedCategoryLabels(value.assigned_suggested_categories, categories)}），以下自选设置暂不生效。`
            : `已指派建议规则（${suggestedCategoryLabels(value.assigned_suggested_categories, categories)}）（自选优先，可在此覆盖）。`}
        </Notice>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>规则来源</Label>
          <Select
            value={value.mode}
            disabled={value.assign_forced_portable}
            onValueChange={(mode) =>
              mode && set({ mode: mode as SubscriptionRoutingProfile['mode'] })
            }
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
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
              disabled={value.assign_forced_portable}
              onValueChange={(preset) => {
                if (!preset) return
                const next = preset as SubscriptionRoutingProfile['preset']
                set({ preset: next, categories: categoriesForPreset(next, categories) })
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(presetLabels).map(([id, label]) => (
                  <SelectItem key={id} value={id}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        ) : (
          <div className="space-y-2">
            <Label>中立 / ACL4SSR 模板</Label>
            <Select
              value={value.portable_template_id}
              disabled={value.assign_forced_portable}
              onValueChange={(id) => id && set({ portable_template_id: id })}
            >
              <SelectTrigger className="w-full">
                <SelectValue
                  placeholder={value.assign_forced_portable ? '已强制指派' : '选择模板'}
                />
              </SelectTrigger>
              <SelectContent>
                {portable.map((template) => (
                  <SelectItem key={template.id} value={template.id}>
                    {template.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
      </div>

      {value.mode === 'suggested' ? (
        <fieldset disabled={value.assign_forced_portable} className="space-y-2">
          <legend className="text-sm font-medium">生效分类</legend>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {categories.map((category) => (
              <label key={category.id} className="cg-check-row">
                <input
                  type="checkbox"
                  className="cg-checkbox"
                  checked={value.categories.includes(category.id)}
                  onChange={(event) =>
                    set({
                      categories: event.target.checked
                        ? [...value.categories, category.id]
                        : value.categories.filter((id) => id !== category.id),
                    })
                  }
                />
                <span aria-hidden="true">{category.icon}</span>
                <span className="min-w-0 break-words">{category.label}</span>
              </label>
            ))}
          </div>
        </fieldset>
      ) : null}

      <details className="cg-routing-details">
        <summary>客户端原生模板覆盖</summary>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          {(
            [
              [
                'mihomo_template_id',
                'assigned_mihomo_template_id',
                'assign_forced_mihomo',
                'Mihomo',
                'mihomo',
              ],
              [
                'singbox_template_id',
                'assigned_singbox_template_id',
                'assign_forced_singbox',
                'Sing-box',
                'singbox',
              ],
              [
                'quanx_template_id',
                'assigned_quanx_template_id',
                'assign_forced_quanx',
                'Quantumult X',
                'quanx',
              ],
            ] as const
          ).map(([field, assignedField, forcedField, label, kind]) => {
            const selected = value[field] as string
            const assigned = value[assignedField] as string
            const forced = Boolean(value[forcedField])
            return (
              <div key={field} className="space-y-2">
                <Label>{label}</Label>
                <Select
                  value={selected || 'none'}
                  disabled={forced}
                  onValueChange={(id) => id && set({ [field]: id === 'none' ? '' : id })}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">{forced ? '跟随指派' : '跟随主策略'}</SelectItem>
                    {native(kind).map((template) => (
                      <SelectItem key={template.id} value={template.id}>
                        {template.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {assigned ? (
                  <p className="cg-hint">
                    {forced
                      ? `已强制指派「${templateName(assigned)}」`
                      : `已指派「${templateName(assigned)}」，自选优先`}
                  </p>
                ) : null}
              </div>
            )
          })}
        </div>
      </details>
    </div>
  )
}
