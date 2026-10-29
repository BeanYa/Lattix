import { useEffect, useMemo, useRef, useState } from 'react'
import { CheckIcon, CircleAlertIcon, RefreshCwIcon, RotateCcwIcon, SlidersIcon } from 'lucide-react'

import { RequestError, requester } from '@/lib/requester'
import {
  PRESET_OPTIONS,
  categoriesForPreset,
  effectiveSummary,
  formValuesOf,
  type PresetId,
  type SubTemplateConfig,
  type SubTemplateOption,
} from './template-config-form'

const KIND_LABELS: Record<SubTemplateOption['kind'], string> = {
  portable: '主策略',
  acl4ssr: '主策略',
  mihomo: 'Mihomo',
  singbox: 'Sing-box',
  quanx: 'Quantumult X',
}

/**
 * TemplateConfigCard 是订阅页的用户级模板配置卡（四级优先级：强制级 > 用户级 > 面板级 > 默认级）。
 * 管理员强制指派期间显示锁定提示；用户保存后立即重生成订阅快照。
 */
export function TemplateConfigCard({ token, onSaved }: { token: string; onSaved: () => void }) {
  const [config, setConfig] = useState<SubTemplateConfig | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [mode, setMode] = useState<'suggested' | 'template'>('suggested')
  const [preset, setPreset] = useState<PresetId>('balanced')
  const [categories, setCategories] = useState<string[]>([])
  const [portableId, setPortableId] = useState('')
  const [mihomoId, setMihomoId] = useState('')
  const [singboxId, setSingboxId] = useState('')
  const [quanxId, setQuanxId] = useState('')
  const [regionGroups, setRegionGroups] = useState(true)
  const [saving, setSaving] = useState(false)
  const [savedNotice, setSavedNotice] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState('')
  const noticeTimer = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearTimeout(noticeTimer.current), [])

  const showSavedNotice = () => {
    setSavedNotice(true)
    window.clearTimeout(noticeTimer.current)
    noticeTimer.current = window.setTimeout(() => setSavedNotice(false), 4000)
  }

  // applyFormValues 用后端返回的「表单初值」覆盖编辑态：主策略槽位与地区分组开关
  // 取用户级已自选值或实际生效值；客户端原生模板槽位保持用户级原值（空 = 跟随主
  // 策略），因此保存 / 恢复默认后页面展示与订阅文件一致（原生槽位由提示行说明）。
  const applyFormValues = (next: SubTemplateConfig) => {
    const values = formValuesOf(next)
    setMode(values.mode)
    setPreset(values.preset)
    setCategories(values.categories)
    setPortableId(values.portableId)
    setMihomoId(values.mihomoId)
    setSingboxId(values.singboxId)
    setQuanxId(values.quanxId)
    setRegionGroups(values.regionGroups)
  }

  const applyConfig = (next: SubTemplateConfig) => {
    setConfig(next)
    applyFormValues(next)
  }

  useEffect(() => {
    const controller = new AbortController()
    requester
      .getJSON<SubTemplateConfig>(`/api/sub/${token}/template-config`, {
        signal: controller.signal,
        display: 'silent',
      })
      .then(applyConfig)
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
          region_groups_enabled: regionGroups,
        },
      )
      applyConfig(next)
      showSavedNotice()
      onSaved()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '保存失败，请稍后重试。')
    } finally {
      setSaving(false)
    }
  }

  // 恢复默认：清除用户级配置（仅订阅页可清除），回落到面板级 → 默认级。
  // 没有用户级配置时按钮仍可用：它会把表单重置回当前生效配置（撤销未保存的改动）。
  const reset = async () => {
    if (resetting) return
    setResetting(true)
    setError('')
    const hadUserConfig = config?.user_configured === true
    try {
      const next = await requester.postJSON<SubTemplateConfig>(
        `/api/sub/${token}/template-config/reset`,
      )
      applyConfig(next)
      if (hadUserConfig) {
        showSavedNotice()
        onSaved()
      }
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
              <p className="sub-cfg-hint">
                {config.default_summary}，你可以在下方设置自己的模板配置。
              </p>
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
                          onChange={(event) => toggleCategory(category.id, event.target.checked)}
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
                    [
                      'mihomo',
                      mihomoId,
                      setMihomoId,
                      config.forced_mihomo,
                      config.assigned_mihomo_template_id,
                    ],
                    [
                      'singbox',
                      singboxId,
                      setSingboxId,
                      config.forced_singbox,
                      config.assigned_singbox_template_id,
                    ],
                    [
                      'quanx',
                      quanxId,
                      setQuanxId,
                      config.forced_quanx,
                      config.assigned_quanx_template_id,
                    ],
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

            <div className="space-y-1">
              <label className="sub-cfg-cat">
                <input
                  type="checkbox"
                  checked={regionGroups}
                  onChange={(event) => setRegionGroups(event.target.checked)}
                  aria-describedby="sub-cfg-region-groups-hint"
                />
                <span>开启地区分组</span>
              </label>
              <p id="sub-cfg-region-groups-hint" className="sub-cfg-hint">
                按节点地区自动添加分组（含无地区分组）；关闭后保留节点和来源分组。
              </p>
            </div>

            {error && (
              <p className="sub-cfg-error" role="alert">
                {error}
              </p>
            )}
            {savedNotice && (
              <p className="sub-cfg-saved" role="status">
                <CheckIcon size={13} aria-hidden="true" />
                变更已经提交，等待快照刷新
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
                {saving ? <RefreshCwIcon aria-hidden="true" /> : <SlidersIcon aria-hidden="true" />}
                {saving ? '保存中…' : '保存'}
              </button>
              <button
                type="button"
                className="cg-button"
                disabled={resetting}
                onClick={reset}
                title={
                  config.user_configured
                    ? '清除我在订阅页保存的模板配置，回落到管理员默认'
                    : '把表单重置为当前生效配置'
                }
              >
                {resetting ? (
                  <RefreshCwIcon aria-hidden="true" />
                ) : (
                  <RotateCcwIcon aria-hidden="true" />
                )}
                {resetting ? '恢复中…' : '恢复默认（清除我的配置）'}
              </button>
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
