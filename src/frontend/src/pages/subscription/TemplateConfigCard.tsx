import { useEffect, useMemo, useState } from 'react'
import { CheckIcon, CircleAlertIcon, RefreshCwIcon, RotateCcwIcon, SlidersIcon } from 'lucide-react'

import { RequestError, requester } from '@/lib/requester'

/** SubRoutingConfig 是订阅页模板配置的一级视图（用户级原始值 / 实际生效值）。 */
interface SubRoutingConfig {
  mode: 'suggested' | 'template'
  preset: 'minimal' | 'balanced' | 'comprehensive'
  categories: string[]
  portable_template_id: string
  mihomo_template_id: string
  singbox_template_id: string
  quanx_template_id: string
}

interface SubTemplateOption {
  id: string
  name: string
  kind: 'portable' | 'acl4ssr' | 'mihomo' | 'singbox' | 'quanx'
}

interface SubRuleCategory {
  id: string
  label: string
  icon: string
  in_minimal: boolean
  in_balanced: boolean
}

/** SubTemplateConfig 是 GET/POST /api/sub/{token}/template-config 的响应。 */
interface SubTemplateConfig {
  user_configured: boolean
  effective_source: 'forced' | 'user' | 'panel' | 'default' | 'unset'
  forced_portable: boolean
  forced_mihomo: boolean
  forced_singbox: boolean
  forced_quanx: boolean
  forced_summary?: string
  panel_assigned_summary?: string
  default_summary?: string
  assigned_mihomo_template_id?: string
  assigned_singbox_template_id?: string
  assigned_quanx_template_id?: string
  user: SubRoutingConfig
  effective: SubRoutingConfig
  templates: SubTemplateOption[]
  categories: SubRuleCategory[]
}

const KIND_LABELS: Record<SubTemplateOption['kind'], string> = {
  portable: '主策略',
  acl4ssr: '主策略',
  mihomo: 'Mihomo',
  singbox: 'Sing-box',
  quanx: 'Quantumult X',
}

const PRESET_OPTIONS = [
  ['minimal', '极简规则'],
  ['balanced', '均衡规则'],
  ['comprehensive', '完整规则'],
] as const

type PresetId = (typeof PRESET_OPTIONS)[number][0]

function categoriesForPreset(preset: Exclude<PresetId, never>, categories: SubRuleCategory[]) {
  if (preset === 'comprehensive') return categories.map((category) => category.id)
  return categories
    .filter((category) => (preset === 'minimal' ? category.in_minimal : category.in_balanced))
    .map((category) => category.id)
}

function presetOfCategories(ids: string[], categories: SubRuleCategory[]): PresetId {
  const set = new Set(ids)
  for (const [preset] of PRESET_OPTIONS) {
    if (
      set.size === categoriesForPreset(preset, categories).length &&
      categoriesForPreset(preset, categories).every((id) => set.has(id))
    ) {
      return preset
    }
  }
  return 'balanced'
}

/** effectiveSummary 概括实际生效的主策略配置，供头部展示。 */
function effectiveSummary(config: SubRoutingConfig, templates: SubTemplateOption[]): string {
  if (config.mode === 'template') {
    const template = templates.find((item) => item.id === config.portable_template_id)
    return `模板「${template?.name ?? config.portable_template_id}」`
  }
  return `建议规则 · ${config.categories.length} 个分组`
}

/**
 * TemplateConfigCard 是订阅页的用户级模板配置卡（三级优先级：强制级 > 用户级 > 面板级）。
 * 管理员强制指派期间显示锁定提示；用户保存后立即重生成订阅快照。
 */
export function TemplateConfigCard({
  token,
  onSaved,
}: {
  token: string
  onSaved: () => void
}) {
  const [config, setConfig] = useState<SubTemplateConfig | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [mode, setMode] = useState<'suggested' | 'template'>('suggested')
  const [preset, setPreset] = useState<PresetId>('balanced')
  const [categories, setCategories] = useState<string[]>([])
  const [portableId, setPortableId] = useState('')
  const [mihomoId, setMihomoId] = useState('')
  const [singboxId, setSingboxId] = useState('')
  const [quanxId, setQuanxId] = useState('')
  const [saving, setSaving] = useState(false)
  const [savedAt, setSavedAt] = useState(0)
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    requester
      .getJSON<SubTemplateConfig>(`/api/sub/${token}/template-config`, {
        signal: controller.signal,
        display: 'silent',
      })
      .then((next) => {
        setConfig(next)
        setMode(next.user.mode)
        setPreset(presetOfCategories(next.user.categories, next.categories))
        setCategories(next.user.categories)
        setPortableId(next.user.portable_template_id)
        setMihomoId(next.user.mihomo_template_id)
        setSingboxId(next.user.singbox_template_id)
        setQuanxId(next.user.quanx_template_id)
      })
      .catch((reason: unknown) => {
        if (reason instanceof RequestError && reason.code === 'REQUEST_CANCELLED') return
        setLoadFailed(true)
      })
    return () => controller.abort()
  }, [token])

  const portable = useMemo(
    () =>
      (config?.templates ?? []).filter(
        (template) =>
          (template.kind === 'portable' || template.kind === 'acl4ssr') && template.id !== '',
      ),
    [config],
  )
  const native = (kind: SubTemplateOption['kind']) =>
    (config?.templates ?? []).filter((template) => template.kind === kind)

  if (loadFailed) return null
  if (!config) return null

  const locked = config.forced_portable
  const toggleCategory = (id: string, checked: boolean) =>
    setCategories((cur) => (checked ? [...cur, id] : cur.filter((item) => item !== id)))

  const save = async () => {
    if (saving) return
    setSaving(true)
    setError('')
    try {
      const next = await requester.postJSON<SubTemplateConfig>(
        `/api/sub/${token}/template-config`,
        {
          mode,
          preset,
          categories,
          portable_template_id: portableId,
          mihomo_template_id: mihomoId,
          singbox_template_id: singboxId,
          quanx_template_id: quanxId,
        },
      )
      setConfig(next)
      setSavedAt(Date.now())
      onSaved()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '保存失败，请稍后重试。')
    } finally {
      setSaving(false)
    }
  }

  // 恢复默认：清除用户级配置（仅订阅页可清除），回落面板级 → 默认级。
  const reset = async () => {
    if (resetting) return
    setResetting(true)
    setError('')
    try {
      const next = await requester.postJSON<SubTemplateConfig>(
        `/api/sub/${token}/template-config/reset`,
      )
      setConfig(next)
      setMode(next.user.mode)
      setPreset(presetOfCategories(next.user.categories, next.categories))
      setCategories(next.user.categories)
      setPortableId(next.user.portable_template_id)
      setMihomoId(next.user.mihomo_template_id)
      setSingboxId(next.user.singbox_template_id)
      setQuanxId(next.user.quanx_template_id)
      setSavedAt(0)
      onSaved()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '恢复默认失败，请稍后重试。')
    } finally {
      setResetting(false)
    }
  }

  return (
    <section className="sub-mod sub-cfg" aria-label="模板配置">
      <header className="sub-mod-head">
        <span className="sub-tab">POLICY</span>
        <h2 className="sub-mod-title">模板配置</h2>
        <span className="sub-mod-meta">
          当前生效：{effectiveSummary(config.effective, config.templates)}
        </span>
      </header>
      <div className="sub-mod-body sub-cfg-body">
        {locked ? (
          <div className="sub-cfg-lock" role="note">
            <CircleAlertIcon size={14} aria-hidden="true" />
            <span>
              {config.forced_summary}，你的自选配置暂时不生效。如需调整请联系管理员解除强制。
            </span>
          </div>
        ) : (
          <>
            {config.panel_assigned_summary && (
              <p className="sub-cfg-hint">{config.panel_assigned_summary}。</p>
            )}
            {config.default_summary && !config.panel_assigned_summary && (
              <p className="sub-cfg-hint">{config.default_summary}，你可以在下方设置自己的模板配置。</p>
            )}
            {config.user_configured ? (
              <p className="sub-cfg-hint">
                正在使用你自己的模板配置（用户级，优先于管理员默认）。保存后订阅立即更新。
              </p>
            ) : null}
            <div className="sub-cfg-row">
              <label className="sub-field-label" htmlFor="sub-cfg-mode">
                规则来源
              </label>
              <div className="sub-cfg-seg" role="group" aria-label="规则来源">
                <button
                  type="button"
                  aria-pressed={mode === 'suggested'}
                  onClick={() => {
                    setMode('suggested')
                    setCategories(categoriesForPreset(preset, config.categories))
                  }}
                >
                  建议规则
                </button>
                <button
                  type="button"
                  aria-pressed={mode === 'template'}
                  onClick={() => setMode('template')}
                >
                  自定义模板
                </button>
              </div>
            </div>

            {mode === 'suggested' ? (
              <>
                <div className="sub-cfg-row">
                  <label className="sub-field-label" htmlFor="sub-cfg-preset">
                    规则预设
                  </label>
                  <select
                    id="sub-cfg-preset"
                    className="sub-select"
                    value={preset}
                    onChange={(event) => {
                      const next = event.target.value as PresetId
                      setPreset(next)
                      setCategories(categoriesForPreset(next, config.categories))
                    }}
                  >
                    {PRESET_OPTIONS.map(([id, label]) => (
                      <option key={id} value={id}>
                        {label}
                      </option>
                    ))}
                  </select>
                </div>
                <fieldset className="sub-cfg-fieldset">
                  <legend className="sub-field-label">生效分组</legend>
                  <div className="sub-cfg-cats">
                    {config.categories.map((category) => (
                      <label key={category.id} className="sub-cfg-cat">
                        <input
                          type="checkbox"
                          checked={categories.includes(category.id)}
                          onChange={(event) =>
                            toggleCategory(category.id, event.target.checked)
                          }
                        />
                        <span aria-hidden="true">{category.icon}</span>
                        <span>{category.label}</span>
                      </label>
                    ))}
                  </div>
                </fieldset>
              </>
            ) : (
              <div className="sub-cfg-row">
                <label className="sub-field-label" htmlFor="sub-cfg-portable">
                  中立 / ACL4SSR 模板
                </label>
                <select
                  id="sub-cfg-portable"
                  className="sub-select"
                  value={portableId}
                  onChange={(event) => setPortableId(event.target.value)}
                >
                  <option value="">选择模板</option>
                  {portable.map((template) => (
                    <option key={template.id} value={template.id}>
                      {template.name}
                    </option>
                  ))}
                </select>
              </div>
            )}

            <details className="sub-cfg-native">
              <summary>客户端原生模板</summary>
              <div className="sub-cfg-native-grid">
                {(
                  [
                    ['mihomo', mihomoId, setMihomoId, config.forced_mihomo, config.assigned_mihomo_template_id],
                    ['singbox', singboxId, setSingboxId, config.forced_singbox, config.assigned_singbox_template_id],
                    ['quanx', quanxId, setQuanxId, config.forced_quanx, config.assigned_quanx_template_id],
                  ] as const
                ).map(([kind, value, setValue, isForced, assignedId]) => {
                  const assigned = assignedId
                    ? config.templates.find((template) => template.id === assignedId)?.name
                    : ''
                  return (
                    <div key={kind} className="sub-cfg-native-cell">
                      <label className="sub-field-label" htmlFor={`sub-cfg-${kind}`}>
                        {KIND_LABELS[kind]}
                      </label>
                      <select
                        id={`sub-cfg-${kind}`}
                        className="sub-select"
                        value={value}
                        disabled={isForced}
                        onChange={(event) => setValue(event.target.value)}
                      >
                        <option value="">{isForced ? '跟随强制指派' : '跟随主策略'}</option>
                        {native(kind).map((template) => (
                          <option key={template.id} value={template.id}>
                            {template.name}
                          </option>
                        ))}
                      </select>
                      {assigned && !isForced ? (
                        <p className="sub-cfg-hint">管理员默认「{assigned}」，你的选择优先</p>
                      ) : null}
                    </div>
                  )
                })}
              </div>
            </details>

            {error && (
              <p className="sub-cfg-error" role="alert">
                {error}
              </p>
            )}
            <div className="sub-cfg-actions">
              <button
                type="button"
                className="cg-button is-primary"
                disabled={
                  saving ||
                  (mode === 'template' && !portableId) ||
                  (mode === 'suggested' && categories.length === 0)
                }
                onClick={save}
              >
                {saving ? (
                  <RefreshCwIcon aria-hidden="true" />
                ) : savedAt ? (
                  <CheckIcon aria-hidden="true" />
                ) : (
                  <SlidersIcon aria-hidden="true" />
                )}
                {saving ? '保存中…' : savedAt ? '已保存，订阅更新中' : '保存模板配置'}
              </button>
              {config.user_configured ? (
                <button
                  type="button"
                  className="cg-button"
                  disabled={resetting}
                  onClick={reset}
                >
                  {resetting ? (
                    <RefreshCwIcon aria-hidden="true" />
                  ) : (
                    <RotateCcwIcon aria-hidden="true" />
                  )}
                  {resetting ? '恢复中…' : '恢复默认（清除我的配置）'}
                </button>
              ) : null}
              <span className="sub-cfg-note">
                保存后订阅文件立即重新生成；客户端下次刷新订阅即生效。
              </span>
            </div>
          </>
        )}
      </div>
    </section>
  )
}
