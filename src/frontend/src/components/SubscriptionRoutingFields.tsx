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

function categoriesForPreset(
  preset: SubscriptionRoutingProfile['preset'],
  categories: SubscriptionRuleCategory[],
): string[] {
  if (preset === 'comprehensive') return categories.map((category) => category.id)
  return categories
    .filter((category) => (preset === 'minimal' ? category.in_minimal : category.in_balanced))
    .map((category) => category.id)
}

/**
 * SubscriptionRoutingFields 是用户订阅设置中的路由表单，编辑的是面板级/强制级槽位：
 * 勾选「强制覆盖」保存为强制级，不勾选保存为面板级。用户级配置（订阅页自选）
 * 面板侧只读，仅在 value.user_configured 时提示。
 */
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
  const forced = Boolean(value.forced)
  const portable = templates.filter(
    (template) => ['portable', 'acl4ssr'].includes(template.kind) && template.content,
  )
  const native = (kind: SubscriptionTemplate['kind']) =>
    templates.filter((template) => template.kind === kind && template.content)
  const set = (patch: Partial<SubscriptionRoutingProfile>) => onChange({ ...value, ...patch })
  const hasPanelMain =
    value.mode === 'template' ? Boolean(value.portable_template_id) : value.categories.length > 0

  return (
    <div className="space-y-4 border-t pt-4">
      {value.user_configured && (
        <Notice tone="info">
          该用户已在订阅页自定义模板配置（用户级，优先于面板级与默认级）。此处保存不影响用户级配置；
          如需让其设置立即失效，请勾选下方「强制覆盖」。
        </Notice>
      )}

      <div className="space-y-2">
        <div className="flex items-baseline justify-between gap-2">
          <Label>面板级模板配置</Label>
          <span className="cg-hint">
            {forced ? '覆盖勾选：保存写入强制级，立即生效' : '未勾选覆盖：保存写入面板级，用户自选优先'}
          </span>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-2">
            <Label>规则来源</Label>
            <Select
              value={value.mode}
              items={[
                { value: 'suggested', label: '建议规则' },
                { value: 'template', label: '自定义模板' },
              ]}
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
                items={Object.entries(presetLabels).map(([value, label]) => ({ value, label }))}
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
                items={portable.map((template) => ({ value: template.id, label: template.name }))}
                onValueChange={(id) => set({ portable_template_id: id ?? '' })}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择模板" />
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
        {!hasPanelMain && (
          <p className="cg-hint">
            主策略未配置：保存后该级不生效，用户将回落到系统默认级配置（模板指派页维护）。
          </p>
        )}
      </div>

      {value.mode === 'suggested' ? (
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">
            生效分组 <span className="cg-hint">（不选任何分组 = 不设置面板级主策略）</span>
          </legend>
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

      <div className="space-y-1">
        <label className="cg-check-row">
          <input
            type="checkbox"
            role="switch"
            className="cg-checkbox"
            checked={value.region_groups_enabled}
            onChange={(event) => set({ region_groups_enabled: event.target.checked })}
            aria-describedby="routing-region-groups-hint"
          />
          开启地区分组
        </label>
        <p id="routing-region-groups-hint" className="cg-hint">
          按节点地区自动添加分组（含无地区分组）；关闭后保留节点和来源分组。
        </p>
      </div>

      <details className="cg-routing-details">
        <summary>客户端原生模板覆盖</summary>
        <div className="mt-3 grid gap-3 sm:grid-cols-3">
          {(
            [
              ['mihomo_template_id', 'Mihomo', 'mihomo'],
              ['singbox_template_id', 'Sing-box', 'singbox'],
              ['quanx_template_id', 'Quantumult X', 'quanx'],
            ] as const
          ).map(([field, label, kind]) => (
            <div key={field} className="space-y-2">
              <Label>{label}</Label>
              <Select
                value={value[field] || 'none'}
                items={[
                  { value: 'none', label: '跟随主策略' },
                  ...native(kind).map((template) => ({
                    value: template.id,
                    label: template.name,
                  })),
                ]}
                onValueChange={(id) => set({ [field]: id === 'none' ? '' : id })}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">跟随主策略</SelectItem>
                  {native(kind).map((template) => (
                    <SelectItem key={template.id} value={template.id}>
                      {template.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ))}
        </div>
      </details>

      <label className="cg-check-row">
        <input
          type="checkbox"
          className="cg-checkbox"
          checked={forced}
          onChange={(event) => set({ forced: event.target.checked })}
        />
        强制覆盖用户自选（写入强制级，立即对用户生效）
      </label>
      <p className="cg-hint">
        不勾选时写入面板级：用户自选优先，用户未自定义且无此配置时回落默认级；勾选后写入强制级，覆盖用户的一切自选。
      </p>
    </div>
  )
}
